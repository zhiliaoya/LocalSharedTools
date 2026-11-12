package main

import (
	"net/http"
	"testing"
)

func TestHostBatchUserManagement(t *testing.T) {
	app := newTestApp(t)
	srv := newTestServer(t, app)
	host := hostCaller(t, app)

	uid1, tok1 := makeUser(t, app, "batch-1", RoleLV2, host.UserID, ShareTypeSpecific, 3)
	uid2, tok2 := makeUser(t, app, "batch-2", RoleLV3, host.UserID, ShareTypeSpecific, 3)
	_, adminTok := makeUser(t, app, "batch-admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)

	// 非 host 不能调用批量管理。
	if status, _ := doJSON(t, srv, "POST", "/admin/users/role", adminTok, map[string]interface{}{
		"user_ids": []int64{uid1}, "role_id": RoleLV5,
	}); status != http.StatusForbidden {
		t.Fatalf("admin batch role = %d, want 403", status)
	}

	// 批量改权限。
	status, out := doJSON(t, srv, "POST", "/admin/users/role", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid1, uid2}, "role_id": RoleLV5,
	})
	if status != http.StatusOK {
		t.Fatalf("batch role = %d, %v", status, out)
	}
	for _, tk := range []string{tok1, tok2} {
		status, me := doJSON(t, srv, "GET", "/auth/me", tk, nil)
		if status != http.StatusOK {
			t.Fatalf("me after role change = %d", status)
		}
		if me["perm_mask"].(float64) != float64(PermView|PermSend|PermUpload|PermDownload) {
			t.Fatalf("perm after role change = %v", me["perm_mask"])
		}
	}

	// 批量撤销 token：用户仍启用，但不能再用旧 token。
	status, out = doJSON(t, srv, "POST", "/admin/users/revoke-token", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid1, uid2},
	})
	if status != http.StatusOK || int(out["revoked"].(float64)) != 2 {
		t.Fatalf("batch revoke = %d, %v", status, out)
	}
	for _, tk := range []string{tok1, tok2} {
		if status, _ := doJSON(t, srv, "GET", "/auth/me", tk, nil); status != http.StatusUnauthorized {
			t.Fatalf("revoked token status = %d, want 401", status)
		}
	}

	// 单用户重签后恢复。
	status, out = doJSON(t, srv, "POST", "/admin/reissue", app.hostToken, map[string]interface{}{"user_id": uid1})
	if status != http.StatusOK {
		t.Fatalf("reissue uid1 = %d, %v", status, out)
	}
	newTok1, _ := out["token"].(string)
	if status, _ := doJSON(t, srv, "GET", "/auth/me", newTok1, nil); status != http.StatusOK {
		t.Fatalf("new token1 = %d", status)
	}

	// 软删除：立即 401，重签被拒；恢复后可重签。
	status, out = doJSON(t, srv, "POST", "/admin/users/active", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid2}, "active": false,
	})
	if status != http.StatusOK {
		t.Fatalf("soft delete = %d, %v", status, out)
	}
	var active int
	if err := app.db.QueryRow(`SELECT active FROM users WHERE id=?`, uid2).Scan(&active); err != nil {
		t.Fatalf("query active: %v", err)
	}
	if active != 0 {
		t.Fatal("soft-deleted user still active")
	}
	if status, _ := doJSON(t, srv, "POST", "/admin/reissue", app.hostToken, map[string]interface{}{"user_id": uid2}); status != http.StatusBadRequest {
		t.Fatalf("reissue inactive = %d, want 400", status)
	}

	status, out = doJSON(t, srv, "POST", "/admin/users/active", app.hostToken, map[string]interface{}{
		"user_ids": []int64{uid2}, "active": true,
	})
	if status != http.StatusOK {
		t.Fatalf("restore = %d, %v", status, out)
	}
	status, out = doJSON(t, srv, "POST", "/admin/reissue", app.hostToken, map[string]interface{}{"user_id": uid2})
	if status != http.StatusOK {
		t.Fatalf("reissue restored = %d, %v", status, out)
	}
	restoredTok, _ := out["token"].(string)
	if status, _ := doJSON(t, srv, "GET", "/auth/me", restoredTok, nil); status != http.StatusOK {
		t.Fatalf("restored token = %d", status)
	}

	// host 自身受保护。
	for path, payload := range map[string]map[string]interface{}{
		"/admin/users/role":         {"user_ids": []int64{host.UserID}, "role_id": RoleLV5},
		"/admin/users/active":       {"user_ids": []int64{host.UserID}, "active": false},
		"/admin/users/revoke-token": {"user_ids": []int64{host.UserID}},
	} {
		if status, _ := doJSON(t, srv, "POST", path, app.hostToken, payload); status != http.StatusForbidden {
			t.Fatalf("%s host target = %d, want 403", path, status)
		}
	}

	// 空批次与不存在用户。
	if status, _ := doJSON(t, srv, "POST", "/admin/users/revoke-token", app.hostToken, map[string]interface{}{
		"user_ids": []int64{},
	}); status != http.StatusBadRequest {
		t.Fatalf("empty batch = %d, want 400", status)
	}
	if status, _ := doJSON(t, srv, "POST", "/admin/users/revoke-token", app.hostToken, map[string]interface{}{
		"user_ids": []int64{999999},
	}); status != http.StatusNotFound {
		t.Fatalf("missing user = %d, want 404", status)
	}
}
