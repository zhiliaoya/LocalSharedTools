package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	qrcode "github.com/skip2/go-qrcode"
	_ "modernc.org/sqlite"
)

var (
	rootDir = flag.String("dir", "LocalShT/shared", "shared directory (auto-created if missing)")
	dbPath  = flag.String("db", "LocalShT/data/LocalShT.db", "sqlite db file (parent dir auto-created)")
	port    = flag.Int("port", 5050, "listen port")
	history = flag.Int("history", 200, "how many history rows to load")
)

var (
	tmpl             *template.Template // 见 staticfs.go 中 parseIndexTemplate
	hub              *Hub
	database         *sql.DB
	hostname         string
	actualPortGlobal int
)

// 相邻同人文件合组的时间窗口
const fileGroupWindowMs = int64(60 * 1000)

// ---------- 数据模型 ----------

type Message struct {
	ID        int64  `json:"id"`
	ClientID  string `json:"client_id"`
	Username  string `json:"username"`
	Kind      string `json:"kind"` // text | file
	Content   string `json:"content"`
	Filename  string `json:"filename,omitempty"`
	Filesize  int64  `json:"filesize,omitempty"`
	Ext       string `json:"ext,omitempty"`
	CreatedAt string `json:"created_at"`
	Ts        int64  `json:"ts"`
}

// ---------- 视图模型 ----------

type FileItem struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	Filesize    int64  `json:"filesize"`
	SizeText    string `json:"size_text"`
	Ext         string `json:"ext,omitempty"`
	PreviewKind string `json:"preview_kind,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	DownloadURL string `json:"download_url"`
}

type MessageView struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	Filename  string `json:"filename,omitempty"`
	Filesize  int64  `json:"filesize,omitempty"`
	Ext       string `json:"ext,omitempty"`
	CreatedAt string `json:"created_at"`
	Ts        int64  `json:"ts"`

	ClientID    string `json:"client_id"`
	Username    string `json:"username"`
	IsSelf      bool   `json:"is_self"`
	DisplayName string `json:"display_name"`
	AvatarText  string `json:"avatar_text"`
	SizeText    string `json:"size_text,omitempty"`
	TimeLabel   string `json:"time_label"`
	ShowDivider bool   `json:"show_divider"`

	// 文件组
	FileCount int        `json:"file_count,omitempty"`
	Files     []FileItem `json:"files,omitempty"`

	// 兼容单文件快捷字段（= files[0]）
	PreviewKind string `json:"preview_kind,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// ---------- 服务端业务判断 ----------

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n)
	i := -1
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func previewKind(ext string) string {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico", "avif":
		return "image"
	case "mp4", "webm", "ogv", "mov", "m4v":
		return "video"
	case "pdf":
		return "pdf"
	}
	return ""
}

func timeLabel(ts int64) string {
	t := time.UnixMilli(ts)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return fmt.Sprintf("%d月%d日 %s", int(t.Month()), t.Day(), t.Format("15:04"))
}

func avatarText(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	r := []rune(name)
	return strings.ToUpper(string(r[0]))
}

func toFileItem(m *Message) FileItem {
	ext := m.Ext
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(m.Filename), "."))
	}
	pk := previewKind(ext)
	it := FileItem{
		ID:          m.ID,
		Filename:    m.Filename,
		Filesize:    m.Filesize,
		SizeText:    humanSize(m.Filesize),
		Ext:         ext,
		PreviewKind: pk,
		DownloadURL: fmt.Sprintf("/download?msg=%d", m.ID),
	}
	if pk != "" {
		it.PreviewURL = fmt.Sprintf("/download?msg=%d&inline=1", m.ID)
	}
	return it
}

// toViews：把有序消息序列按"相邻同人文件 ≤60s"合组成 MessageView 列表
func toViews(rows []Message, viewerCID string) []MessageView {
	out := make([]MessageView, 0, len(rows))
	const fiveMin = int64(5 * 60 * 1000)
	var lastTs int64

	var curGroup *MessageView
	var curGroupFiles []FileItem
	flushGroup := func() {
		if curGroup == nil {
			return
		}
		curGroup.Files = curGroupFiles
		curGroup.FileCount = len(curGroupFiles)
		if len(curGroupFiles) > 0 {
			first := curGroupFiles[0]
			curGroup.Filename = first.Filename
			curGroup.Filesize = first.Filesize
			curGroup.SizeText = first.SizeText
			curGroup.Ext = first.Ext
			curGroup.PreviewKind = first.PreviewKind
			curGroup.PreviewURL = first.PreviewURL
			curGroup.DownloadURL = first.DownloadURL
		}
		out = append(out, *curGroup)
		curGroup = nil
		curGroupFiles = nil
	}

	for i := range rows {
		m := &rows[i]

		isSelf := viewerCID != "" && m.ClientID == viewerCID
		dispName := "匿名"
		if isSelf {
			dispName = "我"
		} else if m.Username != "" {
			dispName = m.Username
		}
		avatar := avatarText(dispName)

		if m.Kind == "file" {
			if curGroup != nil &&
				curGroup.ClientID == m.ClientID &&
				m.Ts-curGroup.Ts <= fileGroupWindowMs {
				curGroupFiles = append(curGroupFiles, toFileItem(m))
				lastTs = m.Ts
				continue
			}
			flushGroup()
			curGroup = &MessageView{
				ID: m.ID, Kind: "file", ClientID: m.ClientID, Username: m.Username,
				IsSelf: isSelf, DisplayName: dispName, AvatarText: avatar,
				CreatedAt: m.CreatedAt, Ts: m.Ts,
				TimeLabel:   timeLabel(m.Ts),
				ShowDivider: (lastTs == 0) || (m.Ts-lastTs > fiveMin),
			}
			curGroupFiles = []FileItem{toFileItem(m)}
			lastTs = m.Ts
			continue
		}

		flushGroup()
		v := MessageView{
			ID: m.ID, Kind: m.Kind, Content: m.Content, ClientID: m.ClientID,
			Username: m.Username, IsSelf: isSelf, DisplayName: dispName, AvatarText: avatar,
			CreatedAt: m.CreatedAt, Ts: m.Ts,
			TimeLabel:   timeLabel(m.Ts),
			ShowDivider: (lastTs == 0) || (m.Ts-lastTs > fiveMin),
		}
		lastTs = m.Ts
		out = append(out, v)
	}
	flushGroup()
	return out
}

func toView(m *Message, viewerCID string) MessageView {
	vs := toViews([]Message{*m}, viewerCID)
	if len(vs) == 0 {
		return MessageView{}
	}
	return vs[0]
}

// ---------- 数据库 ----------

func initDB(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	schema := `
CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	client_id  TEXT    NOT NULL DEFAULT '',
	username   TEXT    NOT NULL,
	kind       TEXT    NOT NULL DEFAULT 'text',
	content    TEXT    NOT NULL DEFAULT '',
	filename   TEXT    NOT NULL DEFAULT '',
	filesize   INTEGER NOT NULL DEFAULT 0,
	ext        TEXT    NOT NULL DEFAULT '',
	created_at TEXT    NOT NULL,
	ts         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages(ts);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	if hasTable(db, "texts") && !hasTable(db, "messages") {
		_, _ = db.Exec(`INSERT INTO messages(client_id, username, kind, content, created_at, ts)
			SELECT client_id, username, 'text', content, created_at, ts FROM texts`)
	}
	ensureColumn(db, "messages", "kind", "TEXT NOT NULL DEFAULT 'text'")
	ensureColumn(db, "messages", "content", "TEXT NOT NULL DEFAULT ''")
	ensureColumn(db, "messages", "filename", "TEXT NOT NULL DEFAULT ''")
	ensureColumn(db, "messages", "filesize", "INTEGER NOT NULL DEFAULT 0")
	ensureColumn(db, "messages", "ext", "TEXT NOT NULL DEFAULT ''")

	return db, nil
}

func hasTable(db *sql.DB, name string) bool {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return err == nil && n > 0
}

func hasColumn(db *sql.DB, table, col string) bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

func ensureColumn(db *sql.DB, table, col, def string) {
	if !hasColumn(db, table, col) {
		_, _ = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def))
	}
}

func insertMessage(m *Message) (int64, error) {
	res, err := database.Exec(
		`INSERT INTO messages(client_id, username, kind, content, filename, filesize, ext, created_at, ts)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ClientID, m.Username, m.Kind, m.Content, m.Filename, m.Filesize, m.Ext, m.CreatedAt, m.Ts,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func loadRecent(limit int) ([]Message, error) {
	rows, err := database.Query(
		`SELECT id, client_id, username, kind, content, filename, filesize, ext, created_at, ts
		 FROM messages ORDER BY ts DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ClientID, &m.Username, &m.Kind, &m.Content,
			&m.Filename, &m.Filesize, &m.Ext, &m.CreatedAt, &m.Ts); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

func getMessage(id int64) (*Message, error) {
	var m Message
	err := database.QueryRow(
		`SELECT id, client_id, username, kind, content, filename, filesize, ext, created_at, ts
		 FROM messages WHERE id=?`, id,
	).Scan(&m.ID, &m.ClientID, &m.Username, &m.Kind, &m.Content,
		&m.Filename, &m.Filesize, &m.Ext, &m.CreatedAt, &m.Ts)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------- WebSocket Hub ----------

type Hub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

type wsClient struct {
	conn      *websocket.Conn
	id        string
	send      chan []byte
	hub       *Hub
	closeOnce sync.Once
}

func NewHub() *Hub { return &Hub{clients: make(map[*wsClient]struct{})} }

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

func (h *Hub) broadcastMessage(m *Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		recent, err := loadRecent(50)
		if err != nil {
			v := toView(m, c.id)
			payload, err2 := json.Marshal(wsEvent{Type: "message_upsert", Row: &v})
			if err2 == nil {
				select {
				case c.send <- payload:
				default:
				}
			}
			continue
		}
		views := toViews(recent, c.id)
		if len(views) == 0 {
			continue
		}
		last := views[len(views)-1]
		payload, err2 := json.Marshal(wsEvent{Type: "message_upsert", Row: &last})
		if err2 != nil {
			continue
		}
		select {
		case c.send <- payload:
		default:
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); c.conn.Close() }()
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	clientID := r.URL.Query().Get("cid")
	if clientID == "" {
		clientID = "anon"
	}
	c := &wsClient{conn: conn, id: clientID, send: make(chan []byte, 64), hub: hub}
	hub.add(c)

	rows, _ := loadRecent(*history)
	views := toViews(rows, clientID)
	if payload, err := json.Marshal(wsEvent{Type: "history", Rows: views}); err == nil {
		select {
		case c.send <- payload:
		default:
		}
	}

	go c.writePump()
	go c.readPump()
}

// ---------- 事件 ----------

type wsEvent struct {
	Type string        `json:"type"` // history | message_upsert
	Rows []MessageView `json:"rows,omitempty"`
	Row  *MessageView  `json:"row,omitempty"`
}

// ---------- 工具 ----------

func withinRoot(absRoot, absPath string) bool {
	if absPath == absRoot {
		return true
	}
	return len(absPath) > len(absRoot) &&
		absPath[:len(absRoot)] == absRoot &&
		os.IsPathSeparator(absPath[len(absRoot)])
}

// findStaticDir 磁盘模式：在可执行文件目录、工作目录下寻找 static 目录。
func findStaticDir() (string, error) {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "static"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "static"))
	}
	for _, d := range cands {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d, nil
		}
	}
	return "", fmt.Errorf("static dir not found, tried: %v", cands)
}

func uniquePath(dir, name string) string {
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return dst
	}
	ext := filepath.Ext(name)
	base := name[:len(name)-len(ext)]
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext))
}

// ---------- 页面 ----------

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, map[string]interface{}{
		"Hostname": hostname,
	}); err != nil {
		log.Printf("template: %v", err)
	}
}

// GET /history?cid=xxx&limit=200
func historyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cid := r.URL.Query().Get("cid")
	limit := *history
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	rows, err := loadRecent(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"rows": toViews(rows, cid),
	})
}

// ---------- 文本消息 ----------

func createTextHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clientID := r.PostFormValue("cid")
	username := r.PostFormValue("username")
	content := r.PostFormValue("content")
	if clientID == "" || content == "" {
		http.Error(w, "cid and content are required", http.StatusBadRequest)
		return
	}
	if username == "" {
		username = "anonymous"
	}

	now := time.Now()
	m := &Message{
		ClientID: clientID, Username: username, Kind: "text",
		Content: content, CreatedAt: now.Format("2006-01-02 15:04:05"), Ts: now.UnixMilli(),
	}
	id, err := insertMessage(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.ID = id

	hub.broadcastMessage(m)

	v := toView(m, clientID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---------- 文件上传 ----------

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["file"]
	clientID := r.FormValue("cid")
	username := r.FormValue("username")
	if clientID == "" {
		clientID = "anon"
	}
	if username == "" {
		username = "anonymous"
	}

	absRoot, _ := filepath.Abs(*rootDir)
	var added []*Message

	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dst := uniquePath(absRoot, filepath.Base(fh.Filename))
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			f.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written, err := io.Copy(out, f)
		out.Close()
		f.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		now := time.Now()
		baseName := filepath.Base(dst)
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))
		m := &Message{
			ClientID: clientID, Username: username, Kind: "file",
			Filename: baseName, Filesize: written, Ext: ext,
			CreatedAt: now.Format("2006-01-02 15:04:05"), Ts: now.UnixMilli(),
		}
		id, err := insertMessage(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		m.ID = id
		added = append(added, m)
	}

	for _, m := range added {
		hub.broadcastMessage(m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "count": len(added)})
}

// ---------- 文件下载 ----------

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	absRoot, _ := filepath.Abs(*rootDir)

	var full, filename string
	var size int64

	if idStr := r.URL.Query().Get("msg"); idStr != "" {
		var id int64
		fmt.Sscanf(idStr, "%d", &id)
		m, err := getMessage(id)
		if err != nil || m.Kind != "file" || m.Filename == "" {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		full = filepath.Join(absRoot, filepath.Base(m.Filename))
		filename = m.Filename
	} else if p := r.URL.Query().Get("file"); p != "" {
		full = filepath.Join(absRoot, filepath.FromSlash(p))
		filename = filepath.Base(full)
	} else {
		http.Error(w, "missing msg or file", http.StatusBadRequest)
		return
	}

	absFull, _ := filepath.Abs(full)
	if !withinRoot(absRoot, absFull) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := os.Stat(absFull)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	size = info.Size()

	f, err := os.Open(absFull)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	inline := r.URL.Query().Get("inline") == "1"
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	safeInline := map[string]bool{
		"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true,
		"bmp": true, "ico": true, "avif": true,
		"mp4": true, "webm": true, "ogv": true, "mov": true, "m4v": true,
		"pdf": true,
	}
	disposition := "attachment"
	if inline && safeInline[ext] {
		disposition = "inline"
	}

	mimeType := mime.TypeByExtension("." + ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("%s; filename=%q", disposition, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	io.Copy(w, f)
}

// ---------- 端口 ----------

func localIPs() []string {
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	return ips
}

func portInUse(p int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func listenWithFallback(startPort, maxTries int) (net.Listener, int, error) {
	for p := startPort; p < startPort+maxTries && p <= 65535; p++ {
		if portInUse(p) {
			log.Printf("端口 %d 已被占用，尝试下一个...", p)
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			return ln, p, nil
		}
		log.Printf("端口 %d 绑定失败: %v", p, err)
	}
	return nil, 0, fmt.Errorf("在 %d~%d 范围内没有可用端口", startPort, startPort+maxTries-1)
}

// ---------- 分享地址 / 二维码 ----------

func shareInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := actualPortGlobal
	if p == 0 {
		p = *port
	}

	ips := localIPs()
	url := ""
	if len(ips) > 0 {
		url = fmt.Sprintf("http://%s:%d", ips[0], p)
	} else {
		url = fmt.Sprintf("http://localhost:%d", p)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url":  url,
		"ips":  ips,
		"port": p,
	})
}

func shareQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := r.URL.Query().Get("url")
	if target == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}

	png, err := qrcode.Encode(target, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to generate qr: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// ---------- main ----------

// openBrowser 用默认浏览器打开 URL（仅 Windows 有效）
func openBrowser(url string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("cmd", "/c", "start", "", url).Start()
	}
}

func main() {
	flag.Parse()

	hostname, _ = os.Hostname()
	if hostname == "" {
		hostname = "unknown-host"
	}

	// 先确保数据库所在目录（默认 data/）存在，否则后面的日志文件打不开
	if dir := filepath.Dir(*dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}

	// ---- 日志重定向：无控制台/双写/降级 由 initLogging 统一处理 ----
	initLogging(filepath.Dir(*dbPath))

	absRoot, err := filepath.Abs(*rootDir)
	if err != nil {
		log.Fatalf("解析共享目录失败: %v", err)
	}
	if err := os.MkdirAll(absRoot, 0755); err != nil {
		log.Fatalf("创建共享目录失败: %v", err)
	}

	database, err = initDB(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer database.Close()

	hub = NewHub()

	mux := http.NewServeMux()
	mustInitStatic(mux) // 解析 index.html 模板 + 注册 /static/

	mux.HandleFunc("/ws", wsHandler)
	mux.HandleFunc("/history", historyHandler)
	mux.HandleFunc("/texts", createTextHandler)
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/download", downloadHandler)
	mux.HandleFunc("/share/info", shareInfoHandler)
	mux.HandleFunc("/share/qr", shareQRHandler)
	mux.HandleFunc("/", indexHandler)

	requestedPort := *port

	// ---- 防呆：检测初始端口上是否已有「同一 exe」的旧实例 ----
	// 策略 A：不杀旧实例，直接打开浏览器指向它，然后退出
	if pid, same := sameExeOnPort(requestedPort); same {
		url := fmt.Sprintf("http://localhost:%d", requestedPort)
		log.Printf("检测到已有实例在端口 %d 运行（PID=%d），打开浏览器后退出", requestedPort, pid)
		openBrowser(url)
		return
	}

	ln, actualPort, err := listenWithFallback(requestedPort, 100)
	if err != nil {
		log.Fatal(err)
	}
	actualPortGlobal = actualPort

	if actualPort != requestedPort {
		log.Printf("端口 %d 不可用，已自动切换到 %d", requestedPort, actualPort)
	}

	dbAbs, _ := filepath.Abs(*dbPath)
	fmt.Printf("数据库: %s\n", dbAbs)
	fmt.Printf("共享目录: %s\n", absRoot)
	fmt.Printf("主机名: %s\n", hostname)
	fmt.Printf("本机访问: http://localhost:%d\n", actualPort)
	for _, ip := range localIPs() {
		fmt.Printf("局域网访问: http://%s:%d\n", ip, actualPort)
	}
	fmt.Println("按 Ctrl+C 或关闭窗口结束（GUI 编译版请用任务管理器结束）")

	// ---- 自动打开默认浏览器（用真实监听端口）----
	openBrowser(fmt.Sprintf("http://localhost:%d", actualPort))

	log.Fatal(http.Serve(ln, mux))
}