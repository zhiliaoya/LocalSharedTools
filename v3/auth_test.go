package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"
)

func TestCookieDownloadWorks(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)

	body, ct := multipartBody(t, "file", "cookie.png", []byte("cookie-bytes"))
	if resp, raw := doRequest(t, srv, "POST", "/upload", app.hostToken, body, ct); resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d, %s", resp.StatusCode, raw)
	}
	var recID int64
	if err := app.db.QueryRow(`SELECT id FROM records ORDER BY id DESC LIMIT 1`).Scan(&recID); err != nil {
		t.Fatalf("query record: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	payload, _ := json.Marshal(map[string]string{"token": app.hostToken})
	resp, err := client.Post(srv.URL+"/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	dl, err := client.Get(srv.URL + "/download?msg=" + strconv.FormatInt(recID, 10))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("cookie download = %d, want 200", dl.StatusCode)
	}
}

func TestAdminCannotTouchOthersSharesOrReissue(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)

	// host 创建 lv1：share 的 sharer 是 host。
	_, lv1Token := makeUser(t, app, "lv1", RoleLV1, host.UserID, ShareTypeSpecific, 3)
	var hostShareID int64
	if err := app.db.QueryRow(`SELECT id FROM shares WHERE target_token=?`, lv1Token).Scan(&hostShareID); err != nil {
		t.Fatalf("query share: %v", err)
	}

	_, adminToken := makeUser(t, app, "admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)
	if status, _ := doJSON(t, srv, "POST", "/share/revoke", adminToken, map[string]interface{}{"share_id": hostShareID}); status != http.StatusForbidden {
		t.Fatalf("admin revoke host share = %d, want 403", status)
	}
	var lv1ID int64
	if err := app.db.QueryRow(`SELECT id FROM users WHERE name='lv1'`).Scan(&lv1ID); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if status, _ := doJSON(t, srv, "POST", "/admin/reissue", adminToken, map[string]interface{}{"user_id": lv1ID}); status != http.StatusForbidden {
		t.Fatalf("admin reissue = %d, want 403", status)
	}
}

func TestInactiveTokenRejected(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	_, token := makeUser(t, app, "x", RoleLV1, host.UserID, ShareTypeSpecific, 3)
	if _, err := app.db.Exec(`UPDATE tokens SET active=0 WHERE token=?`, token); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if status, _ := doJSON(t, srv, "GET", "/history", token, nil); status != http.StatusUnauthorized {
		t.Fatalf("inactive token = %d, want 401", status)
	}
}
