package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func newTestServer(t *testing.T, app *App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return srv
}

func historyIDs(t *testing.T, srv *httptest.Server, token string) []int64 {
	t.Helper()
	status, out := doJSON(t, srv, "GET", "/history", token, nil)
	if status != http.StatusOK {
		t.Fatalf("history status = %d, body=%v", status, out)
	}
	rawRows, _ := out["rows"].([]interface{})
	ids := make([]int64, 0, len(rawRows))
	for _, r := range rawRows {
		m, _ := r.(map[string]interface{})
		if id, ok := m["id"].(float64); ok {
			ids = append(ids, int64(id))
		}
	}
	return ids
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestAuthRequired(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)

	if status, _ := doJSON(t, srv, "GET", "/history", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", status)
	}
	if status, _ := doJSON(t, srv, "GET", "/history", "not-a-token", nil); status != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", status)
	}
	if status, _ := doJSON(t, srv, "GET", "/healthz", "", nil); status != http.StatusOK {
		t.Fatalf("health status = %d", status)
	}
}

func TestLoginCookieAndCSRF(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	payload, _ := json.Marshal(map[string]string{"token": app.hostToken})
	resp, err := client.Post(srv.URL+"/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("login did not set cookie")
	}

	// Cookie 可用于 GET。
	getResp, err := client.Get(srv.URL + "/history")
	if err != nil {
		t.Fatalf("cookie history: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("cookie GET history = %d, want 200", getResp.StatusCode)
	}

	// Cookie 不能用于 POST（CSRF 防护）。
	postResp, err := client.Post(srv.URL+"/texts", "application/x-www-form-urlencoded", bytes.NewBufferString("content=x"))
	if err != nil {
		t.Fatalf("cookie post: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie-only POST = %d, want 401", postResp.StatusCode)
	}
}

func TestHistoryPermissionFiltering(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	hostRec := insertTestRecord(t, app, host, FileTypeText, "host-visible", 0, "")

	_, otherToken := makeUser(t, app, "other", RoleLV5, host.UserID, ShareTypeSpecific, 3)
	other := callerFor(t, app, otherToken)
	otherRec := insertTestRecord(t, app, other, FileTypeText, "other-hidden", 0, "")

	_, lv1Token := makeUser(t, app, "lv1", RoleLV1, host.UserID, ShareTypeSpecific, 1)
	ids := historyIDs(t, srv, lv1Token)
	if !containsID(ids, hostRec.ID) {
		t.Fatalf("lv1 history missing host record: %v", ids)
	}
	if containsID(ids, otherRec.ID) {
		t.Fatalf("lv1 history leaked other record: %v", ids)
	}

	// host 全量。
	hostIDs := historyIDs(t, srv, app.hostToken)
	if !containsID(hostIDs, otherRec.ID) {
		t.Fatalf("host history missing other record: %v", hostIDs)
	}
}

func TestTextPermissionsAndIdentity(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	_, lv1Token := makeUser(t, app, "lv1", RoleLV1, host.UserID, ShareTypeSpecific, 1)
	_, lv2Token := makeUser(t, app, "lv2", RoleLV2, host.UserID, ShareTypeSpecific, 1)
	lv2 := callerFor(t, app, lv2Token)

	if status, _ := doForm(t, srv, "POST", "/texts", lv1Token, map[string]string{"content": "nope"}); status != http.StatusForbidden {
		t.Fatalf("lv1 texts = %d, want 403", status)
	}

	// lv2 可以发；请求里伪造 cid/username 应被忽略。
	status, out := doForm(t, srv, "POST", "/texts", lv2Token, map[string]string{
		"content": "hello", "cid": "spoof", "username": "spoof",
	})
	if status != http.StatusOK {
		t.Fatalf("lv2 texts = %d, body=%v", status, out)
	}
	var uid int64
	if err := app.db.QueryRow(`SELECT user_id FROM records ORDER BY id DESC LIMIT 1`).Scan(&uid); err != nil {
		t.Fatalf("query record: %v", err)
	}
	if uid != lv2.UserID {
		t.Fatalf("record owner = %d, want %d", uid, lv2.UserID)
	}
}

func TestUploadPermissions(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	_, lv2Token := makeUser(t, app, "lv2", RoleLV2, host.UserID, ShareTypeSpecific, 3)
	_, lv4Token := makeUser(t, app, "lv4", RoleLV4, host.UserID, ShareTypeSpecific, 3)

	body, ct := multipartBody(t, "file", "doc.txt", []byte("hello-file"))
	if resp, raw := doRequest(t, srv, "POST", "/upload", lv2Token, body, ct); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("lv2 upload = %d, body=%s", resp.StatusCode, raw)
	}

	body, ct = multipartBody(t, "file", "doc.txt", []byte("hello-file"))
	if resp, raw := doRequest(t, srv, "POST", "/upload", lv4Token, body, ct); resp.StatusCode != http.StatusOK {
		t.Fatalf("lv4 upload = %d, body=%s", resp.StatusCode, raw)
	}
	if _, err := os.Stat(filepath.Join(app.cfg.RootDir, "doc.txt")); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	var ft int
	if err := app.db.QueryRow(`SELECT file_type_id FROM records ORDER BY id DESC LIMIT 1`).Scan(&ft); err != nil {
		t.Fatalf("query file type: %v", err)
	}
	if ft == FileTypeText {
		t.Fatal("uploaded file recorded as text")
	}
}

func TestDownloadPermissions(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)

	body, ct := multipartBody(t, "file", "pic.png", []byte("png-bytes"))
	if resp, raw := doRequest(t, srv, "POST", "/upload", app.hostToken, body, ct); resp.StatusCode != http.StatusOK {
		t.Fatalf("host upload = %d, body=%s", resp.StatusCode, raw)
	}
	var recID int64
	if err := app.db.QueryRow(`SELECT id FROM records ORDER BY id DESC LIMIT 1`).Scan(&recID); err != nil {
		t.Fatalf("query record: %v", err)
	}

	_, lv1Token := makeUser(t, app, "lv1", RoleLV1, host.UserID, ShareTypeSpecific, 3)
	_, lv2Token := makeUser(t, app, "lv2", RoleLV2, host.UserID, ShareTypeSpecific, 3)
	_, lv3Token := makeUser(t, app, "lv3", RoleLV3, host.UserID, ShareTypeSpecific, 3)
	_, lv4Token := makeUser(t, app, "lv4", RoleLV4, host.UserID, ShareTypeSpecific, 3)
	_, lv5Token := makeUser(t, app, "lv5", RoleLV5, host.UserID, ShareTypeSpecific, 3)

	for name, tk := range map[string]string{"lv1": lv1Token, "lv2": lv2Token, "lv4": lv4Token} {
		resp, _ := doRequest(t, srv, "GET", "/download?msg="+strconv.FormatInt(recID, 10), tk, nil, "")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s download = %d, want 403", name, resp.StatusCode)
		}
	}
	for name, tk := range map[string]string{"host": app.hostToken, "lv3": lv3Token, "lv5": lv5Token} {
		resp, raw := doRequest(t, srv, "GET", "/download?msg="+strconv.FormatInt(recID, 10), tk, nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s download = %d, want 200", name, resp.StatusCode)
		}
		if string(raw) != "png-bytes" {
			t.Errorf("%s download body = %q", name, raw)
		}
	}

	// 预览同样要求 DOWNLOAD。
	resp, _ := doRequest(t, srv, "GET", "/download?msg="+strconv.FormatInt(recID, 10)+"&inline=1", lv3Token, nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") == "" {
		t.Errorf("lv3 inline = %d, content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// file= 参数仅 host。
	resp, _ = doRequest(t, srv, "GET", "/download?file=pic.png", lv5Token, nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("lv5 file= = %d, want 403", resp.StatusCode)
	}
	resp, raw := doRequest(t, srv, "GET", "/download?file=pic.png", app.hostToken, nil, "")
	if resp.StatusCode != http.StatusOK || string(raw) != "png-bytes" {
		t.Errorf("host file= = %d, body=%q", resp.StatusCode, raw)
	}
}

func TestAdminUserLifecycle(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)

	// host 创建 admin。
	status, out := doJSON(t, srv, "POST", "/admin/users", app.hostToken, map[string]interface{}{
		"name": "admin1", "role_id": RoleAdmin, "share_type_id": ShareTypeSpecific, "share_level_id": 3,
	})
	if status != http.StatusOK {
		t.Fatalf("host create admin = %d, %v", status, out)
	}
	adminToken, _ := out["token"].(string)
	if adminToken == "" {
		t.Fatal("no admin token")
	}

	// admin 创建 lv1。
	status, out = doJSON(t, srv, "POST", "/admin/users", adminToken, map[string]interface{}{
		"name": "lv1-user", "role_id": RoleLV1, "share_type_id": ShareTypeSpecific, "share_level_id": 3,
	})
	if status != http.StatusOK {
		t.Fatalf("admin create lv1 = %d, %v", status, out)
	}
	lv1Token, _ := out["token"].(string)

	// admin 不能再创建 admin。
	if status, _ := doJSON(t, srv, "POST", "/admin/users", adminToken, map[string]interface{}{
		"name": "admin2", "role_id": RoleAdmin, "share_type_id": ShareTypeSpecific, "share_level_id": 3,
	}); status != http.StatusForbidden {
		t.Fatalf("admin create admin = %d, want 403", status)
	}
	// 不能通过接口创建 host。
	if status, _ := doJSON(t, srv, "POST", "/admin/users", app.hostToken, map[string]interface{}{
		"name": "host2", "role_id": RoleHost, "share_type_id": ShareTypeSpecific, "share_level_id": 3,
	}); status != http.StatusBadRequest {
		t.Fatalf("create host = %d, want 400", status)
	}

	// admin 先发一条文本，lv1 应能看到。
	adminCaller := callerFor(t, app, adminToken)
	adminRec := insertTestRecord(t, app, adminCaller, FileTypeText, "admin text", 0, "")
	if ids := historyIDs(t, srv, lv1Token); !containsID(ids, adminRec.ID) {
		t.Fatalf("lv1 missing admin record: %v", ids)
	}

	// admin 的分享列表。
	status, out = doJSON(t, srv, "GET", "/admin/shares", adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("admin shares = %d", status)
	}
	shares, _ := out["shares"].([]interface{})
	if len(shares) == 0 {
		t.Fatal("admin shares empty")
	}
	first, _ := shares[0].(map[string]interface{})
	shareID := int64(first["id"].(float64))

	// 撤销后 lv1 看不到 admin 的记录，但 token 仍有效。
	if status, _ := doJSON(t, srv, "POST", "/share/revoke", adminToken, map[string]interface{}{"share_id": shareID}); status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	if ids := historyIDs(t, srv, lv1Token); containsID(ids, adminRec.ID) {
		t.Fatalf("lv1 still sees admin record after revoke: %v", ids)
	}

	// host 用户列表。
	status, out = doJSON(t, srv, "GET", "/admin/users", app.hostToken, nil)
	if status != http.StatusOK {
		t.Fatalf("host users = %d", status)
	}
	users, _ := out["users"].([]interface{})
	if len(users) < 3 {
		t.Fatalf("users = %d, want >=3", len(users))
	}

	// 重签 lv1：旧 token 失效，分享迁移到新 token。
	var lv1ID int64
	if err := app.db.QueryRow(`SELECT id FROM users WHERE name='lv1-user'`).Scan(&lv1ID); err != nil {
		t.Fatalf("query lv1 id: %v", err)
	}
	status, out = doJSON(t, srv, "POST", "/admin/reissue", app.hostToken, map[string]interface{}{"user_id": lv1ID})
	if status != http.StatusOK {
		t.Fatalf("reissue = %d, %v", status, out)
	}
	newToken, _ := out["token"].(string)
	if newToken == "" || newToken == lv1Token {
		t.Fatal("reissue did not rotate token")
	}
	if status, _ := doJSON(t, srv, "GET", "/history", lv1Token, nil); status != http.StatusUnauthorized {
		t.Fatalf("old token = %d, want 401", status)
	}
	if status, _ := doJSON(t, srv, "GET", "/history", newToken, nil); status != http.StatusOK {
		t.Fatalf("new token = %d, want 200", status)
	}
}

func TestProfileAndNewUserQR(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)

	// 自己的登录二维码入口已移除：/share/info 不再暴露。
	if status, _ := doJSON(t, srv, "GET", "/share/info", app.hostToken, nil); status != http.StatusNotFound {
		t.Fatalf("share/info = %d, want 404", status)
	}

	loginURL := "http://example.invalid/login?token=new-user-token"
	resp, raw := doRequest(t, srv, "GET", "/share/qr?url="+url.QueryEscape(loginURL), app.hostToken, nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || len(raw) == 0 {
		t.Fatalf("qr = %d, ct=%q, len=%d", resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	}

	status, out := doJSON(t, srv, "POST", "/profile", app.hostToken, map[string]string{"name": "主机"})
	if status != http.StatusOK {
		t.Fatalf("profile = %d", status)
	}
	user, _ := out["user"].(map[string]interface{})
	if user["name"] != "主机" {
		t.Fatalf("profile name = %v", user["name"])
	}
}
