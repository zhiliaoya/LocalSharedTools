package main

import (
	"log"
	"net"
	"net/http"
)

// audit 记录写操作。detail 中不要放完整 token。
func (a *App) audit(r *http.Request, c *Caller, action, detail string) {
	if c == nil {
		a.auditRaw(r, 0, 0, action, detail)
		return
	}
	a.auditRaw(r, c.UserID, c.RoleID, action, detail)
}

func (a *App) auditRaw(r *http.Request, userID int64, roleID int, action, detail string) {
	ip := clientIP(r)
	if _, err := a.db.Exec(
		`INSERT INTO audit_log(at, actor_user_id, actor_role_id, action, detail, ip)
		 VALUES(?,?,?,?,?,?)`,
		nowText(), userID, roleID, action, detail, ip); err != nil {
		log.Printf("审计写入失败: %v", err)
	}
	log.Printf("审计 %s user=%d role=%d ip=%s %s", action, userID, roleID, ip, detail)
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
