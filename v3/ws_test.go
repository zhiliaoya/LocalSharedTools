package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func wsURL(srvURL, token string) string {
	return strings.Replace(srvURL, "http", "ws", 1) + "/ws?token=" + token
}

func readWSEvent(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var ev map[string]interface{}
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("ws json: %v", err)
	}
	return ev
}

func TestWSRejectsMissingToken(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatalf("ws request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ws no token = %d, want 401", resp.StatusCode)
	}
}

func TestWSPermissionsChangedOnRoleUpdate(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)
	uid, token := makeUser(t, app, "role-change", RoleLV2, host.UserID, ShareTypeSpecific, 3)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv.URL, token), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()
	if ev := readWSEvent(t, conn); ev["type"] != "history" {
		t.Fatalf("first event = %v", ev)
	}

	status, out := doJSON(t, srv, "POST", "/admin/users/role", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid}, "role_id": RoleLV5,
	})
	if status != http.StatusOK {
		t.Fatalf("role change = %d, %v", status, out)
	}
	if ev := readWSEvent(t, conn); ev["type"] != "permissions_changed" {
		t.Fatalf("event = %v, want permissions_changed", ev)
	}
	status, me := doJSON(t, srv, "GET", "/auth/me", token, nil)
	if status != http.StatusOK || me["perm_mask"].(float64) != float64(PermView|PermSend|PermUpload|PermDownload) {
		t.Fatalf("me after role change = %d, %v", status, me)
	}

	// 软删除会立即踢掉旧连接并让 token 失效。
	status, out = doJSON(t, srv, "POST", "/admin/users/active", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid}, "active": false,
	})
	if status != http.StatusOK {
		t.Fatalf("soft delete = %d, %v", status, out)
	}
	if ev := readWSEvent(t, conn); ev["type"] != "session_revoked" {
		t.Fatalf("event = %v, want session_revoked", ev)
	}
	if status, _ := doJSON(t, srv, "GET", "/auth/me", token, nil); status != http.StatusUnauthorized {
		t.Fatalf("soft-deleted token = %d, want 401", status)
	}
}

func TestWSFilteringAndRevoke(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)

	adminID, adminToken := makeUser(t, app, "admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)
	admin := callerFor(t, app, adminToken)
	first := insertTestRecord(t, app, admin, FileTypeText, "first", 0, "")
	_, lv1Token := makeUser(t, app, "lv1", RoleLV1, adminID, ShareTypeSpecific, 3)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv.URL, lv1Token), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	ev := readWSEvent(t, conn)
	if ev["type"] != "history" {
		t.Fatalf("first event = %v", ev)
	}
	rows, _ := ev["rows"].([]interface{})
	found := false
	for _, raw := range rows {
		row, _ := raw.(map[string]interface{})
		if int64(row["id"].(float64)) == first.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing shared record: %v", ev)
	}

	// 新记录广播给可见的 lv1。
	second := insertTestRecord(t, app, admin, FileTypeText, "second", 0, "")
	app.hub.broadcastRecord(second)
	ev = readWSEvent(t, conn)
	if ev["type"] != "message_upsert" {
		t.Fatalf("event = %v, want message_upsert", ev)
	}
	row, _ := ev["row"].(map[string]interface{})
	if int64(row["id"].(float64)) != second.ID {
		t.Fatalf("upsert row id = %v, want %d", row["id"], second.ID)
	}

	// 撤销分享后推送 acl_changed。
	var shareID int64
	if err := app.db.QueryRow(
		`SELECT id FROM shares WHERE target_token=? AND active=1`, lv1Token).Scan(&shareID); err != nil {
		t.Fatalf("query share: %v", err)
	}
	status, out := doJSON(t, srv, "POST", "/share/revoke", adminToken, map[string]interface{}{"share_id": shareID})
	if status != http.StatusOK {
		t.Fatalf("revoke = %d, %v", status, out)
	}
	ev = readWSEvent(t, conn)
	if ev["type"] != "acl_changed" {
		t.Fatalf("event = %v, want acl_changed", ev)
	}

	// 重签后旧连接收到 session_revoked。
	var lv1ID int64
	if err := app.db.QueryRow(`SELECT id FROM users WHERE name='lv1'`).Scan(&lv1ID); err != nil {
		t.Fatalf("query lv1: %v", err)
	}
	status, out = doJSON(t, srv, "POST", "/admin/reissue", app.hostToken, map[string]interface{}{"user_id": lv1ID})
	if status != http.StatusOK {
		t.Fatalf("reissue = %d, %v", status, out)
	}
	ev = readWSEvent(t, conn)
	if ev["type"] != "session_revoked" {
		t.Fatalf("event = %v, want session_revoked", ev)
	}
}
