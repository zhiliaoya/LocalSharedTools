package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ---------- WebSocket Hub ----------

type Hub struct {
	app     *App
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

type wsClient struct {
	conn      *websocket.Conn
	caller    *Caller
	send      chan []byte
	hub       *Hub
	closeOnce sync.Once
}

func NewHub(app *App) *Hub {
	return &Hub{app: app, clients: make(map[*wsClient]struct{})}
}

func (h *Hub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *Hub) enqueue(c *wsClient, payload []byte) {
	select {
	case c.send <- payload:
	default:
	}
}

func viewContainsID(v MessageView, id int64) bool {
	if v.ID == id {
		return true
	}
	for _, f := range v.Files {
		if f.ID == id {
			return true
		}
	}
	return false
}

// broadcastRecord 把新记录按接收者可见性推给在线客户端；不可见不推送。
func (h *Hub) broadcastRecord(rec *Record) {
	rows, err := h.app.loadRecent(50)
	if err != nil {
		log.Printf("广播加载历史失败: %v", err)
		return
	}
	// 持读锁发送：readPump 的 remove 要拿写锁，避免往已关闭的 send 通道写。
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		viewer := h.app.perm.Viewer(c.caller)
		visible := make([]Record, 0, len(rows))
		for _, r := range rows {
			if viewer.Can(r.UserID, PermView) {
				visible = append(visible, r)
			}
		}
		if len(visible) == 0 {
			continue
		}
		views := toViews(visible, viewer)
		if len(views) == 0 {
			continue
		}
		last := views[len(views)-1]
		if !viewContainsID(last, rec.ID) {
			// 新记录对当前客户端不可见；不要用旧行打扰它。
			continue
		}
		payload, err := json.Marshal(wsEvent{Type: "message_upsert", Row: &last})
		if err != nil {
			continue
		}
		h.enqueue(c, payload)
	}
}

// notifyToken 给某个 token 的所有连接推事件（撤销分享后的 acl_changed）。
func (h *Hub) notifyToken(token, event string) {
	payload, err := json.Marshal(wsEvent{Type: event})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.caller.Token == token {
			h.enqueue(c, payload)
		}
	}
}

// notifyUser 给某个用户的所有在线连接推事件。
func (h *Hub) notifyUser(userID int64, event string) {
	payload, err := json.Marshal(wsEvent{Type: event})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.caller.UserID == userID {
			h.enqueue(c, payload)
		}
	}
}

// updateUserCaller 在角色变更后同步在线连接缓存的权限，避免旧权限继续广播。
func (h *Hub) updateUserCaller(userID int64, roleID int, roleName string, permMask uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.caller.UserID == userID {
			c.caller.RoleID = roleID
			c.caller.RoleName = roleName
			c.caller.PermMask = permMask
		}
	}
}

// kickToken 通知并关闭某个 token 的所有连接（重签后的 session_revoked）。
func (h *Hub) kickToken(token, event string) {
	payload, err := json.Marshal(wsEvent{Type: event})
	if err != nil {
		return
	}
	var conns []*websocket.Conn
	h.mu.RLock()
	for c := range h.clients {
		if c.caller.Token != token {
			continue
		}
		h.enqueue(c, payload)
		conns = append(conns, c.conn)
	}
	h.mu.RUnlock()
	for _, conn := range conns {
		time.AfterFunc(300*time.Millisecond, func() { conn.Close() })
	}
}

// ---------- 事件 ----------

type wsEvent struct {
	Type string        `json:"type"` // history | message_upsert | acl_changed | session_revoked
	Rows []MessageView `json:"rows,omitempty"`
	Row  *MessageView  `json:"row,omitempty"`
}

// ---------- 连接 ----------

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // 非浏览器客户端
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

func (a *App) handleWS(w http.ResponseWriter, r *http.Request) {
	c, _, err := a.resolveCaller(r)
	if err != nil || c == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "WS 缺少或无效 token")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{
		conn: conn, caller: c,
		send: make(chan []byte, 64), hub: a.hub,
	}
	a.hub.add(client)

	limit := a.cfg.History
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.loadRecent(limit)
	if err == nil {
		viewer := a.perm.Viewer(c)
		visible := make([]Record, 0, len(rows))
		for _, rec := range rows {
			if viewer.Can(rec.UserID, PermView) {
				visible = append(visible, rec)
			}
		}
		if payload, err := json.Marshal(wsEvent{Type: "history", Rows: toViews(visible, viewer)}); err == nil {
			select {
			case client.send <- payload:
			default:
			}
		}
	} else {
		log.Printf("WS 历史加载失败: %v", err)
	}

	go client.writePump()
	go client.readPump()
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) readPump() {
	defer func() {
		c.hub.remove(c)
		c.closeOnce.Do(func() { close(c.send) })
		c.conn.Close()
	}()
	c.conn.SetReadLimit(64 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
