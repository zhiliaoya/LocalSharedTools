package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	authCookieName = "lst_token"
	authCookieDays = 30
	maxBodyBytes   = 1 << 20
)

// Caller 是经过鉴权的请求身份。
type Caller struct {
	UserID   int64
	UserName string
	RoleID   int
	RoleName string
	PermMask uint32
	Token    string
	ClientID string
}

type tokenSource int

const (
	sourceNone tokenSource = iota
	sourceHeader
	sourceXToken
	sourceQuery
	sourceCookie
)

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败属于系统级异常，退回时间戳保证进程可继续。
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			time.Now().Unix(), time.Now().UnixNano()&0xffff, 0x4, 0x8, time.Now().UnixNano()&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "…"
}

// ---------- 通用响应 ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写响应失败: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(v)
}

// ---------- token 解析 ----------

func (a *App) lookupCaller(token string) (*Caller, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, sql.ErrNoRows
	}
	var c Caller
	err := a.db.QueryRow(
		`SELECT u.id, u.name, u.role_type_id, rt.name, rt.perm_mask, t.token, u.client_id
		 FROM tokens t
		 JOIN users u ON u.id = t.user_id
		 JOIN role_types rt ON rt.id = u.role_type_id
		 WHERE t.token = ? AND t.active = 1 AND u.active = 1`, token).Scan(
		&c.UserID, &c.UserName, &c.RoleID, &c.RoleName, &c.PermMask, &c.Token, &c.ClientID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (a *App) resolveCaller(r *http.Request) (*Caller, tokenSource, error) {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			if c, err := a.lookupCaller(strings.TrimSpace(h[7:])); err == nil {
				return c, sourceHeader, nil
			}
			return nil, sourceHeader, sql.ErrNoRows
		}
	}
	if t := strings.TrimSpace(r.Header.Get("X-Token")); t != "" {
		if c, err := a.lookupCaller(t); err == nil {
			return c, sourceXToken, nil
		}
		return nil, sourceXToken, sql.ErrNoRows
	}
	if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
		if c, err := a.lookupCaller(t); err == nil {
			return c, sourceQuery, nil
		}
		return nil, sourceQuery, sql.ErrNoRows
	}
	// Cookie 只用于 GET/HEAD（下载、二维码等），避免 CSRF。
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if ck, err := r.Cookie(authCookieName); err == nil {
			if c, err := a.lookupCaller(ck.Value); err == nil {
				return c, sourceCookie, nil
			}
			return nil, sourceCookie, sql.ErrNoRows
		}
	}
	return nil, sourceNone, sql.ErrNoRows
}

func (a *App) setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   authCookieDays * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) auth(h func(http.ResponseWriter, *http.Request, *Caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, src, err := a.resolveCaller(r)
		if err != nil || c == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "缺少或无效的 token")
			return
		}
		if src == sourceCookie && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "写操作必须使用 Authorization 头")
			return
		}
		h(w, r, c)
	}
}

// ---------- 身份载荷 ----------

func (a *App) authPayload(c *Caller) map[string]interface{} {
	user := map[string]interface{}{
		"id":   c.UserID,
		"name": c.UserName,
	}
	payload := map[string]interface{}{
		"user":      user,
		"token":     c.Token,
		"perm_mask": c.PermMask,
		"caps":      CapsFromMask(c.PermMask),
		"host":      a.hostname,
	}
	// 普通用户不需要角色 / 权限字典：页面已由服务端按角色生成，前端只消费 caps。
	// admin / host 需要字典来渲染分享与用户管理表单。
	if c.Has(PermShare) || c.Has(PermManage) {
		user["role_id"] = c.RoleID
		user["role_name"] = c.RoleName
		payload["meta"] = a.perm.Meta()
	}
	return payload
}

func (a *App) baseURL() string {
	p := a.port
	if p == 0 {
		p = a.cfg.Port
	}
	host := "localhost"
	if ips := localIPs(); len(ips) > 0 {
		host = ips[0]
	}
	return fmt.Sprintf("http://%s:%d", host, p)
}

func (a *App) loginURL(token string) string {
	return a.baseURL() + "/login?token=" + url.QueryEscape(token)
}

// ---------- /auth/* ----------

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var token string
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body struct {
			Token string `json:"token"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
			return
		}
		token = body.Token
	} else {
		if err := r.ParseForm(); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "表单解析失败")
			return
		}
		token = r.FormValue("token")
	}

	c, err := a.lookupCaller(token)
	if err != nil || c == nil {
		a.auditRaw(r, 0, 0, "auth.login.fail", "无效 token: "+maskToken(strings.TrimSpace(token)))
		writeErr(w, http.StatusUnauthorized, "unauthorized", "token 无效或已停用")
		return
	}
	a.setAuthCookie(w, c.Token)
	a.audit(r, c, "auth.login", "登录成功")
	writeJSON(w, http.StatusOK, a.authPayload(c))
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.clearAuthCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	a.setAuthCookie(w, c.Token)
	writeJSON(w, http.StatusOK, a.authPayload(c))
}

func (a *App) handleProfile(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var name string
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
			return
		}
		name = body.Name
	} else {
		if err := r.ParseForm(); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "表单解析失败")
			return
		}
		name = r.FormValue("name")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "昵称不能为空")
		return
	}
	if len([]rune(name)) > 24 {
		writeErr(w, http.StatusBadRequest, "bad_request", "昵称最多 24 个字符")
		return
	}
	if _, err := a.db.Exec(`UPDATE users SET name=?, updated_at=? WHERE id=?`, name, nowText(), c.UserID); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "更新昵称失败")
		return
	}
	c.UserName = name
	a.audit(r, c, "profile.update", "昵称改为 "+name)
	writeJSON(w, http.StatusOK, a.authPayload(c))
}

// ---------- 用户创建（事务：users + collections + tokens + shares） ----------

func createUserTx(tx *sql.Tx, name string, roleID int, clientID string, sharerID int64,
	shareTypeID, shareLevelID int, token string) (int64, string, int64, error) {

	now := nowText()
	res, err := tx.Exec(
		`INSERT INTO users(name, role_type_id, client_id, active, created_at, updated_at)
		 VALUES(?,?,?,1,?,?)`, name, roleID, clientID, now, now)
	if err != nil {
		return 0, "", 0, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return 0, "", 0, err
	}
	res, err = tx.Exec(
		`INSERT INTO collections(user_id, name, is_default, created_at) VALUES(?,?,1,?)`,
		userID, "默认集合", now)
	if err != nil {
		return 0, "", 0, err
	}
	collID, err := res.LastInsertId()
	if err != nil {
		return 0, "", 0, err
	}
	if token == "" {
		token = newToken()
	}
	if _, err := tx.Exec(
		`INSERT INTO tokens(token, user_id, active, created_at) VALUES(?,?,1,?)`,
		token, userID, now); err != nil {
		return 0, "", 0, err
	}
	if sharerID > 0 {
		if _, err := tx.Exec(
			`INSERT INTO shares(sharer_user_id, target_token, share_type_id, share_level_id, active, created_at)
			 VALUES(?,?,?,?,1,?)`, sharerID, token, shareTypeID, shareLevelID, now); err != nil {
			return 0, "", 0, err
		}
	}
	return userID, token, collID, nil
}

// ---------- host 批量用户管理 ----------

var (
	errBatchEmpty    = errors.New("请选择至少一个用户")
	errBatchTooMany  = errors.New("单次最多操作 200 个用户")
	errBadUserID     = errors.New("用户 ID 不合法")
	errUserNotFound  = errors.New("部分用户不存在")
	errHostProtected = errors.New("不能操作 host 用户")
)

type batchUser struct {
	ID     int64
	Name   string
	RoleID int
	Active bool
}

func normalizeUserIDs(raw []int64) ([]int64, error) {
	if len(raw) == 0 {
		return nil, errBatchEmpty
	}
	if len(raw) > 200 {
		return nil, errBatchTooMany
	}
	seen := make(map[int64]struct{}, len(raw))
	out := make([]int64, 0, len(raw))
	for _, id := range raw {
		if id <= 0 {
			return nil, fmt.Errorf("%w: %d", errBadUserID, id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func idArgs(ids []int64) []interface{} {
	out := make([]interface{}, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func (a *App) loadBatchUsers(ids []int64) ([]batchUser, error) {
	q := `SELECT id, name, role_type_id, active FROM users WHERE id IN (` + sqlPlaceholders(len(ids)) + `)`
	rows, err := a.db.Query(q, idArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[int64]struct{}, len(ids))
	out := make([]batchUser, 0, len(ids))
	for rows.Next() {
		var u batchUser
		var active int
		if err := rows.Scan(&u.ID, &u.Name, &u.RoleID, &active); err != nil {
			return nil, err
		}
		if u.RoleID == RoleHost {
			return nil, errHostProtected
		}
		u.Active = active == 1
		seen[u.ID] = struct{}{}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(seen) != len(ids) {
		return nil, errUserNotFound
	}
	return out, nil
}

func (a *App) activeTokensForUsers(ids []int64) ([]string, error) {
	q := `SELECT token FROM tokens WHERE user_id IN (` + sqlPlaceholders(len(ids)) + `) AND active=1`
	rows, err := a.db.Query(q, idArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, rows.Err()
}

func writeBatchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errHostProtected):
		writeErr(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, errUserNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, errBatchEmpty), errors.Is(err, errBatchTooMany), errors.Is(err, errBadUserID):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		log.Printf("批量用户操作失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "server_error", "操作失败")
	}
}

func (a *App) handleAdminUserRole(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermManage) {
		writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可修改用户权限")
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
		RoleID  int     `json:"role_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
		return
	}
	ids, err := normalizeUserIDs(req.UserIDs)
	if err != nil {
		writeBatchError(w, err)
		return
	}
	if req.RoleID == RoleHost || !a.perm.ValidRole(req.RoleID) {
		writeErr(w, http.StatusBadRequest, "bad_request", "角色不合法（不能把用户改为 host）")
		return
	}
	users, err := a.loadBatchUsers(ids)
	if err != nil {
		writeBatchError(w, err)
		return
	}

	args := []interface{}{req.RoleID, nowText()}
	args = append(args, idArgs(ids)...)
	tx, err := a.db.Begin()
	if err != nil {
		writeBatchError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE users SET role_type_id=?, updated_at=? WHERE id IN (`+sqlPlaceholders(len(ids))+`)`,
		args...); err != nil {
		writeBatchError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeBatchError(w, err)
		return
	}

	roleName := a.perm.RoleName(req.RoleID)
	mask := a.perm.RoleMask(req.RoleID)
	for _, u := range users {
		a.hub.updateUserCaller(u.ID, req.RoleID, roleName, mask)
		a.hub.notifyUser(u.ID, "permissions_changed")
	}
	a.audit(r, c, "user.role", fmt.Sprintf("user_ids=%v role=%s", ids, roleName))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "updated": len(users), "role_id": req.RoleID, "role_name": roleName,
	})
}

func (a *App) handleAdminUserActive(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermManage) {
		writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可停用/恢复用户")
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
		Active  bool    `json:"active"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
		return
	}
	ids, err := normalizeUserIDs(req.UserIDs)
	if err != nil {
		writeBatchError(w, err)
		return
	}
	users, err := a.loadBatchUsers(ids)
	if err != nil {
		writeBatchError(w, err)
		return
	}

	var revokedTokens []string
	if !req.Active {
		revokedTokens, err = a.activeTokensForUsers(ids)
		if err != nil {
			writeBatchError(w, err)
			return
		}
	}

	tx, err := a.db.Begin()
	if err != nil {
		writeBatchError(w, err)
		return
	}
	defer tx.Rollback()
	activeValue := 0
	if req.Active {
		activeValue = 1
	}
	args := []interface{}{activeValue, nowText()}
	args = append(args, idArgs(ids)...)
	if _, err := tx.Exec(
		`UPDATE users SET active=?, updated_at=? WHERE id IN (`+sqlPlaceholders(len(ids))+`)`,
		args...); err != nil {
		writeBatchError(w, err)
		return
	}
	if !req.Active {
		tokenArgs := []interface{}{nowText()}
		tokenArgs = append(tokenArgs, idArgs(ids)...)
		if _, err := tx.Exec(
			`UPDATE tokens SET active=0, revoked_at=? WHERE user_id IN (`+sqlPlaceholders(len(ids))+`) AND active=1`,
			tokenArgs...); err != nil {
			writeBatchError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeBatchError(w, err)
		return
	}

	if !req.Active {
		for _, token := range revokedTokens {
			a.hub.kickToken(token, "session_revoked")
		}
	}
	action := "user.enable"
	if !req.Active {
		action = "user.disable"
	}
	a.audit(r, c, action, fmt.Sprintf("user_ids=%v active=%v", ids, req.Active))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "updated": len(users), "active": req.Active, "revoked": len(revokedTokens),
	})
}

func (a *App) handleAdminRevokeTokens(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermManage) {
		writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可撤销用户 token")
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
		return
	}
	ids, err := normalizeUserIDs(req.UserIDs)
	if err != nil {
		writeBatchError(w, err)
		return
	}
	users, err := a.loadBatchUsers(ids)
	if err != nil {
		writeBatchError(w, err)
		return
	}
	tokens, err := a.activeTokensForUsers(ids)
	if err != nil {
		writeBatchError(w, err)
		return
	}
	if len(tokens) > 0 {
		args := []interface{}{nowText()}
		args = append(args, idArgs(ids)...)
		tx, err := a.db.Begin()
		if err != nil {
			writeBatchError(w, err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec(
			`UPDATE tokens SET active=0, revoked_at=? WHERE user_id IN (`+sqlPlaceholders(len(ids))+`) AND active=1`,
			args...); err != nil {
			writeBatchError(w, err)
			return
		}
		if err := tx.Commit(); err != nil {
			writeBatchError(w, err)
			return
		}
		for _, token := range tokens {
			a.hub.kickToken(token, "session_revoked")
		}
	}
	a.audit(r, c, "token.revoke", fmt.Sprintf("user_ids=%v count=%d", ids, len(tokens)))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "users": len(users), "revoked": len(tokens),
	})
}

// ---------- /admin/users ----------

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request, c *Caller) {
	switch r.Method {
	case http.MethodGet:
		if !c.Has(PermManage) {
			writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可查看用户列表")
			return
		}
		a.listUsers(w)
	case http.MethodPost:
		if !c.Has(PermShare) {
			writeErr(w, http.StatusForbidden, "forbidden", "无生成用户权限")
			return
		}
		a.createUser(w, r, c)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的请求方法")
	}
}

func (a *App) listUsers(w http.ResponseWriter) {
	rows, err := a.db.Query(
		`SELECT u.id, u.name, u.role_type_id, rt.name, u.active, u.created_at,
		        (SELECT count(*) FROM tokens t WHERE t.user_id = u.id AND t.active = 1)
		 FROM users u JOIN role_types rt ON rt.id = u.role_type_id
		 ORDER BY u.id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "读取用户失败")
		return
	}
	defer rows.Close()
	out := make([]map[string]interface{}, 0)
	for rows.Next() {
		var (
			id, roleID, active, activeTokens int
			name, roleName, createdAt        string
		)
		if err := rows.Scan(&id, &name, &roleID, &roleName, &active, &createdAt, &activeTokens); err != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "读取用户失败")
			return
		}
		out = append(out, map[string]interface{}{
			"id": id, "name": name, "role_id": roleID, "role_name": roleName,
			"active": active == 1, "created_at": createdAt, "active_tokens": activeTokens,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": out})
}

func (a *App) createUser(w http.ResponseWriter, r *http.Request, c *Caller) {
	var req struct {
		Name         string `json:"name"`
		RoleID       int    `json:"role_id"`
		ShareTypeID  int    `json:"share_type_id"`
		ShareLevelID int    `json:"share_level_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "用户名称不能为空")
		return
	}
	if len([]rune(req.Name)) > 24 {
		writeErr(w, http.StatusBadRequest, "bad_request", "用户名称最多 24 个字符")
		return
	}
	if !a.perm.ValidRole(req.RoleID) || req.RoleID == RoleHost {
		writeErr(w, http.StatusBadRequest, "bad_request", "角色不合法（不能通过接口创建 host）")
		return
	}
	if c.RoleID != RoleHost && req.RoleID == RoleAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", "只有 host 能创建 admin")
		return
	}
	if req.ShareLevelID == 0 {
		req.ShareLevelID = 3
	}
	if !a.perm.ValidShareType(req.ShareTypeID) || !a.perm.ValidShareLevel(req.ShareLevelID) {
		writeErr(w, http.StatusBadRequest, "bad_request", "分享类型或分享等级不合法")
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "创建用户失败")
		return
	}
	defer tx.Rollback()
	uid, token, _, err := createUserTx(tx, req.Name, req.RoleID, "", c.UserID,
		req.ShareTypeID, req.ShareLevelID, "")
	if err != nil {
		log.Printf("创建用户失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "server_error", "创建用户失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "创建用户失败")
		return
	}

	a.audit(r, c, "user.create",
		fmt.Sprintf("user_id=%d role=%s share_type=%d share_level=%d",
			uid, a.perm.RoleName(req.RoleID), req.ShareTypeID, req.ShareLevelID))

	loginURL := a.loginURL(token)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":        uid,
			"name":      req.Name,
			"role_id":   req.RoleID,
			"role_name": a.perm.RoleName(req.RoleID),
		},
		"token":     token,
		"login_url": loginURL,
		"qr_url":    "/share/qr?url=" + url.QueryEscape(loginURL),
	})
}

// ---------- /admin/shares ----------

func (a *App) handleAdminShares(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	if !c.Has(PermShare) {
		writeErr(w, http.StatusForbidden, "forbidden", "无分享管理权限")
		return
	}
	query := `SELECT s.id, s.target_token, t.user_id, u.name, rt.name,
	                 s.share_type_id, st.name, s.share_level_id, sl.name,
	                 s.active, s.created_at, s.revoked_at
	          FROM shares s
	          JOIN tokens t ON t.token = s.target_token
	          JOIN users u ON u.id = t.user_id
	          JOIN role_types rt ON rt.id = u.role_type_id
	          JOIN share_types st ON st.id = s.share_type_id
	          JOIN share_levels sl ON sl.id = s.share_level_id`
	var rows *sql.Rows
	var err error
	if c.RoleID == RoleHost {
		rows, err = a.db.Query(query + ` ORDER BY s.id DESC LIMIT 500`)
	} else {
		rows, err = a.db.Query(query+` WHERE s.sharer_user_id = ? ORDER BY s.id DESC LIMIT 500`, c.UserID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "读取分享失败")
		return
	}
	defer rows.Close()

	out := make([]map[string]interface{}, 0)
	for rows.Next() {
		var (
			id, userID, shareTypeID, shareLevelID, active int
			targetToken, userName, roleName               string
			typeName, levelName, createdAt, revokedAt     string
		)
		if err := rows.Scan(&id, &targetToken, &userID, &userName, &roleName,
			&shareTypeID, &typeName, &shareLevelID, &levelName,
			&active, &createdAt, &revokedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "读取分享失败")
			return
		}
		out = append(out, map[string]interface{}{
			"id": id, "user_id": userID, "name": userName, "role_name": roleName,
			"token_hint": maskToken(targetToken), "share_type_id": shareTypeID,
			"share_type": typeName, "share_level_id": shareLevelID, "share_level": levelName,
			"active": active == 1, "created_at": createdAt, "revoked_at": revokedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"shares": out})
}

// ---------- /share/revoke ----------

func (a *App) handleRevoke(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermShare) {
		writeErr(w, http.StatusForbidden, "forbidden", "无撤销权限")
		return
	}
	var req struct {
		ShareID int64 `json:"share_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.ShareID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 share_id")
		return
	}
	var sharerID int64
	var targetToken string
	var active int
	err := a.db.QueryRow(
		`SELECT sharer_user_id, target_token, active FROM shares WHERE id=?`, req.ShareID).Scan(
		&sharerID, &targetToken, &active)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, "not_found", "分享不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "读取分享失败")
		return
	}
	if c.RoleID != RoleHost && sharerID != c.UserID {
		writeErr(w, http.StatusForbidden, "forbidden", "只能撤销自己创建的分享")
		return
	}
	if active == 1 {
		if _, err := a.db.Exec(
			`UPDATE shares SET active=0, revoked_at=? WHERE id=?`, nowText(), req.ShareID); err != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "撤销失败")
			return
		}
		a.hub.notifyToken(targetToken, "acl_changed")
	}
	a.audit(r, c, "share.revoke", fmt.Sprintf("share_id=%d target=%s", req.ShareID, maskToken(targetToken)))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- /admin/reissue ----------

func (a *App) handleReissue(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermManage) {
		writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可重签 token")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.UserID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 user_id")
		return
	}
	var roleID, active int
	if err := a.db.QueryRow(`SELECT role_type_id, active FROM users WHERE id=?`, req.UserID).Scan(&roleID, &active); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if roleID == RoleHost {
		writeErr(w, http.StatusBadRequest, "bad_request", "host token 请用 -host-token 重签")
		return
	}
	if active == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "用户已停用，请先恢复再重签")
		return
	}

	rows, err := a.db.Query(`SELECT token FROM tokens WHERE user_id=?`, req.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "读取 token 失败")
		return
	}
	var oldTokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, "server_error", "读取 token 失败")
			return
		}
		oldTokens = append(oldTokens, t)
	}
	rows.Close()

	newTk := newToken()
	tx, err := a.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "重签失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE tokens SET active=0, revoked_at=? WHERE user_id=? AND active=1`,
		nowText(), req.UserID); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "重签失败")
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO tokens(token, user_id, active, created_at) VALUES(?,?,1,?)`,
		newTk, req.UserID, nowText()); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "重签失败")
		return
	}
	for _, old := range oldTokens {
		if _, err := tx.Exec(`UPDATE shares SET target_token=? WHERE target_token=?`, newTk, old); err != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "重签失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "重签失败")
		return
	}
	for _, old := range oldTokens {
		a.hub.kickToken(old, "session_revoked")
	}
	a.audit(r, c, "token.reissue", fmt.Sprintf("user_id=%d", req.UserID))

	loginURL := a.loginURL(newTk)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": newTk, "login_url": loginURL,
		"qr_url": "/share/qr?url=" + url.QueryEscape(loginURL),
	})
}

// ---------- 新用户二维码 ----------

func (a *App) handleShareQR(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	target := r.URL.Query().Get("url")
	if target == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 url 参数")
		return
	}
	if len(target) > 2048 {
		writeErr(w, http.StatusBadRequest, "bad_request", "url 过长")
		return
	}
	png, err := qrcode.Encode(target, qrcode.Medium, 256)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "二维码生成失败")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(png); err != nil {
		log.Printf("写二维码失败: %v", err)
	}
}
