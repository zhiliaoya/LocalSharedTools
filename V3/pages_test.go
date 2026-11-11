package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func pageBody(t *testing.T, app *App, token string) string {
	t.Helper()
	srv := newTestServer(t, app)
	_, raw := doRequest(t, srv, "GET", "/", token, nil, "")
	return string(raw)
}

func TestRolePageComposition(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	_, adminToken := makeUser(t, app, "page-admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)
	_, lv1Token := makeUser(t, app, "page-lv1", RoleLV1, host.UserID, ShareTypeSpecific, 3)
	_, lv3Token := makeUser(t, app, "page-lv3", RoleLV3, host.UserID, ShareTypeSpecific, 3)
	_, lv4Token := makeUser(t, app, "page-lv4", RoleLV4, host.UserID, ShareTypeSpecific, 3)

	hostPage := pageBody(t, app, app.hostToken)
	for _, want := range []string{`id="userList"`, `id="shareList"`, `window.AdminUI`, `window.HostUI`, `id="roleBadge"`, `settings-panel wide`} {
		if !strings.Contains(hostPage, want) {
			t.Errorf("host page missing %q", want)
		}
	}

	adminPage := pageBody(t, app, adminToken)
	for _, want := range []string{`id="shareList"`, `window.AdminUI`, `id="roleBadge"`, `settings-panel wide`} {
		if !strings.Contains(adminPage, want) {
			t.Errorf("admin page missing %q", want)
		}
	}
	for _, notWant := range []string{`id="userList"`, `window.HostUI`} {
		if strings.Contains(adminPage, notWant) {
			t.Errorf("admin page should not contain %q", notWant)
		}
	}

	lv1Page := pageBody(t, app, lv1Token)
	for _, notWant := range []string{`id="content"`, `id="toolbar"`, `id="shareList"`, `id="userList"`, `window.AdminUI`, `window.HostUI`, `id="roleBadge"`} {
		if strings.Contains(lv1Page, notWant) {
			t.Errorf("lv1 page should not contain %q", notWant)
		}
	}
	if !strings.Contains(lv1Page, "用户 ID：") {
		t.Error("lv1 page missing user id line")
	}

	lv3Page := pageBody(t, app, lv3Token)
	for _, want := range []string{`id="content"`, `id="toolbar"`} {
		if !strings.Contains(lv3Page, want) {
			t.Errorf("lv3 page missing %q", want)
		}
	}
	if strings.Contains(lv3Page, `id="shareList"`) || strings.Contains(lv3Page, `window.AdminUI`) {
		t.Error("lv3 page leaked admin components")
	}

	lv4Page := pageBody(t, app, lv4Token)
	for _, want := range []string{`id="content"`, `id="attachBtn"`} {
		if !strings.Contains(lv4Page, want) {
			t.Errorf("lv4 page missing %q", want)
		}
	}
	if strings.Contains(lv4Page, `id="toolbar"`) {
		t.Error("lv4 page should not contain toolbar")
	}
}

func TestNormalUserPayloadHasNoRoleMetadata(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	_, lv1Token := makeUser(t, app, "payload-lv1", RoleLV1, host.UserID, ShareTypeSpecific, 3)
	_, adminToken := makeUser(t, app, "payload-admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)

	status, out := doJSON(t, srv, "GET", "/auth/me", lv1Token, nil)
	if status != http.StatusOK {
		t.Fatalf("lv1 me = %d", status)
	}
	user, _ := out["user"].(map[string]interface{})
	if _, ok := user["role_id"]; ok {
		t.Fatal("normal user payload should not contain role_id")
	}
	if _, ok := user["role_name"]; ok {
		t.Fatal("normal user payload should not contain role_name")
	}
	if _, ok := out["meta"]; ok {
		t.Fatal("normal user payload should not contain meta dictionaries")
	}
	if out["caps"] == nil {
		t.Fatal("normal user payload should still contain caps")
	}

	status, out = doJSON(t, srv, "GET", "/auth/me", adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("admin me = %d", status)
	}
	user, _ = out["user"].(map[string]interface{})
	if user["role_id"] == nil || out["meta"] == nil {
		t.Fatal("admin payload should contain role and meta")
	}
}

func TestLoginAndPageRedirects(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	client := noRedirectClient()

	// 未登录访问 / → /login
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("GET / = %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 无 token 的 /login 返回说明页，不再有输入框
	req, _ = http.NewRequest("GET", srv.URL+"/login", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /login = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(string(body), "需要登录链接") {
		t.Fatal("noauth page missing message")
	}
	if strings.Contains(string(body), `id="token"`) {
		t.Fatal("noauth page should not contain token input")
	}

	// 带 token 的 /login 写 Cookie 并跳转 /
	req, _ = http.NewRequest("GET", srv.URL+"/login?token="+app.hostToken, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("GET /login?token = %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("login redirect did not set cookie")
	}

	// 带 token 的 / 也只跳转一次，避免 token 留在地址栏
	req, _ = http.NewRequest("GET", srv.URL+"/?token="+app.hostToken, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("GET /?token = %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("app token redirect did not set cookie")
	}
}
