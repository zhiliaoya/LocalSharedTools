package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		RootDir:    filepath.Join(dir, "shared"),
		DBPath:     filepath.Join(dir, "data", "LocalShT.db"),
		Port:       5599,
		History:    200,
		DualWrite:  false,
		BackupDays: 0,
		HostToken:  "host-token-test",
		HostName:   "test-host",
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	return newTestAppWith(t, testConfig(t))
}

func newTestAppWith(t *testing.T, cfg Config) *App {
	t.Helper()
	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func hostCaller(t *testing.T, app *App) *Caller {
	t.Helper()
	c, err := app.lookupCaller(app.hostToken)
	if err != nil {
		t.Fatalf("host lookup: %v", err)
	}
	return c
}

func makeUser(t *testing.T, app *App, name string, roleID int, sharer int64, shareType, shareLevel int) (int64, string) {
	t.Helper()
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	uid, token, _, err := createUserTx(tx, name, roleID, "", sharer, shareType, shareLevel, "")
	if err != nil {
		tx.Rollback()
		t.Fatalf("create user: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return uid, token
}

func callerFor(t *testing.T, app *App, token string) *Caller {
	t.Helper()
	c, err := app.lookupCaller(token)
	if err != nil {
		t.Fatalf("lookup %s: %v", maskToken(token), err)
	}
	return c
}

func insertTestRecord(t *testing.T, app *App, c *Caller, fileType int, content string, size int64, ext string) *Record {
	t.Helper()
	now := nowText()
	rec := &Record{
		UserID: c.UserID, Username: c.UserName, FileTypeID: fileType,
		Content: content, Filesize: size, Ext: ext,
		CreatedAt: now, Ts: 1700000000000 + c.UserID,
	}
	id, err := app.insertRecord(c, rec)
	if err != nil {
		t.Fatalf("insert record: %v", err)
	}
	rec.ID = id
	return rec
}

func seedLegacyMessages(t *testing.T, dbPath string, rows []legacyMessage) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyMessagesSchema); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	for _, r := range rows {
		created := r.CreatedAt
		if created == "" {
			created = "2026-01-01 10:00:00"
		}
		if _, err := db.Exec(
			`INSERT INTO messages(client_id, username, kind, content, filename, filesize, ext, created_at, ts)
			 VALUES(?,?,?,?,?,?,?,?,?)`,
			r.ClientID, r.Username, r.Kind, r.Content, r.Filename, r.Filesize, r.Ext, created, r.Ts); err != nil {
			t.Fatalf("insert legacy: %v", err)
		}
	}
}

func getCount(t *testing.T, app *App, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := app.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, token string, body io.Reader, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, token string, payload interface{}) (int, map[string]interface{}) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	resp, raw := doRequest(t, srv, method, path, token, body, "application/json")
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func doForm(t *testing.T, srv *httptest.Server, method, path, token string, values map[string]string) (int, map[string]interface{}) {
	t.Helper()
	form := url.Values{}
	for k, v := range values {
		form.Set(k, v)
	}
	resp, raw := doRequest(t, srv, method, path, token, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func multipartBody(t *testing.T, fieldName, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
