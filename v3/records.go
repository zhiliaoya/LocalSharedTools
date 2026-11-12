package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 相邻同人文件合组的时间窗口
const fileGroupWindowMs = int64(60 * 1000)

// ---------- 数据模型 ----------

type Record struct {
	ID           int64
	UserID       int64
	Username     string
	FileTypeID   int
	Content      string
	Filesize     int64
	Ext          string
	CollectionID int64
	CreatedAt    string
	Ts           int64
}

func fileTypeID(ext string) int {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico", "avif":
		return FileTypeImage
	case "mp4", "webm", "ogv", "mov", "m4v":
		return FileTypeVideo
	case "mp3", "wav", "flac", "aac", "ogg", "m4a":
		return FileTypeAudio
	case "pdf":
		return FileTypePDF
	}
	return FileTypeOther
}

// ---------- 数据访问 ----------

func (a *App) defaultCollection(userID int64) (int64, error) {
	var id int64
	err := a.db.QueryRow(
		`SELECT id FROM collections WHERE user_id=? AND is_default=1`, userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := a.db.Exec(
		`INSERT INTO collections(user_id, name, is_default, created_at) VALUES(?,?,1,?)`,
		userID, "默认集合", nowText())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) insertRecord(c *Caller, r *Record) (int64, error) {
	collID, err := a.defaultCollection(c.UserID)
	if err != nil {
		return 0, err
	}
	r.CollectionID = collID
	r.UserID = c.UserID
	if r.Username == "" {
		r.Username = c.UserName
	}

	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO records
		 (user_id, username, file_type_id, content, filesize, ext, collection_id, created_at, ts)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		r.UserID, r.Username, r.FileTypeID, r.Content, r.Filesize, r.Ext, r.CollectionID, r.CreatedAt, r.Ts)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.ID = id

	if a.cfg.DualWrite {
		kind := "text"
		filename := ""
		content := r.Content
		if r.FileTypeID != FileTypeText {
			kind = "file"
			filename = r.Content
			content = ""
		}
		clientID := c.ClientID
		if clientID == "" {
			clientID = c.Token
		}
		msgRes, err := tx.Exec(
			`INSERT INTO messages(client_id, username, kind, content, filename, filesize, ext, created_at, ts)
			 VALUES(?,?,?,?,?,?,?,?,?)`,
			clientID, r.Username, kind, content, filename, r.Filesize, r.Ext, r.CreatedAt, r.Ts)
		if err != nil {
			return 0, fmt.Errorf("双写 messages 失败: %w", err)
		}
		msgID, err := msgRes.LastInsertId()
		if err != nil {
			return 0, err
		}
		// 记录双写来源 id，保证重启时 migrateLegacy 不会把同一条消息再迁一遍。
		if _, err := tx.Exec(`UPDATE records SET legacy_message_id=? WHERE id=?`, msgID, id); err != nil {
			return 0, fmt.Errorf("写入 legacy_message_id 失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (a *App) loadRecent(limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.db.Query(
		`SELECT r.id, r.user_id, COALESCE(NULLIF(r.username,''), u.name), r.file_type_id,
		        r.content, r.filesize, r.ext, r.collection_id, r.created_at, r.ts
		 FROM records r JOIN users u ON u.id = r.user_id
		 ORDER BY r.ts DESC, r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.FileTypeID, &r.Content,
			&r.Filesize, &r.Ext, &r.CollectionID, &r.CreatedAt, &r.Ts); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

func (a *App) getRecord(id int64) (*Record, error) {
	var r Record
	err := a.db.QueryRow(
		`SELECT r.id, r.user_id, COALESCE(NULLIF(r.username,''), u.name), r.file_type_id,
		        r.content, r.filesize, r.ext, r.collection_id, r.created_at, r.ts
		 FROM records r JOIN users u ON u.id = r.user_id WHERE r.id = ?`, id).Scan(
		&r.ID, &r.UserID, &r.Username, &r.FileTypeID, &r.Content,
		&r.Filesize, &r.Ext, &r.CollectionID, &r.CreatedAt, &r.Ts)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ---------- /history ----------

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	if !c.Has(PermView) {
		writeErr(w, http.StatusForbidden, "forbidden", "无查看权限")
		return
	}
	limit := a.cfg.History
	if limit <= 0 {
		limit = 200
	}
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	rows, err := a.loadRecent(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "读取历史失败")
		return
	}
	viewer := a.perm.Viewer(c)
	visible := make([]Record, 0, len(rows))
	for _, rec := range rows {
		if viewer.Can(rec.UserID, PermView) {
			visible = append(visible, rec)
		}
	}
	resp := a.authPayload(c)
	resp["rows"] = toViews(visible, viewer)
	resp["limit"] = limit
	writeJSON(w, http.StatusOK, resp)
}

// ---------- /texts ----------

func (a *App) handleCreateText(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermSend) {
		writeErr(w, http.StatusForbidden, "forbidden", "当前角色不能发送文本")
		return
	}

	var content string
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Content string `json:"content"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON")
			return
		}
		content = body.Content
	} else {
		if err := r.ParseForm(); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "表单解析失败")
			return
		}
		content = r.PostFormValue("content")
	}
	if strings.TrimSpace(content) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "内容不能为空")
		return
	}
	if len(content) > 64*1024 {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "文本过长")
		return
	}

	now := time.Now()
	rec := &Record{
		UserID: c.UserID, Username: c.UserName, FileTypeID: FileTypeText,
		Content: content, CreatedAt: now.Format("2006-01-02 15:04:05"), Ts: now.UnixMilli(),
	}
	id, err := a.insertRecord(c, rec)
	if err != nil {
		log.Printf("写入文本失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "server_error", "发送失败")
		return
	}
	rec.ID = id

	a.hub.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, toView(rec, a.perm.Viewer(c)))
}

// ---------- /upload ----------

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if !c.Has(PermUpload) {
		writeErr(w, http.StatusForbidden, "forbidden", "当前角色不能上传文件")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "上传解析失败: "+err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 file 字段")
		return
	}

	absRoot, err := filepath.Abs(a.cfg.RootDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "共享目录无效")
		return
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "无法创建共享目录")
		return
	}

	var added []*Record
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "读取上传文件失败")
			return
		}
		dst := uniquePath(absRoot, filepath.Base(fh.Filename))
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			f.Close()
			writeErr(w, http.StatusInternalServerError, "server_error", "写入文件失败")
			return
		}
		written, copyErr := io.Copy(out, f)
		out.Close()
		f.Close()
		if copyErr != nil {
			writeErr(w, http.StatusInternalServerError, "server_error", "写入文件失败")
			return
		}

		now := time.Now()
		baseName := filepath.Base(dst)
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))
		rec := &Record{
			UserID: c.UserID, Username: c.UserName, FileTypeID: fileTypeID(ext),
			Content: baseName, Filesize: written, Ext: ext,
			CreatedAt: now.Format("2006-01-02 15:04:05"), Ts: now.UnixMilli(),
		}
		id, err := a.insertRecord(c, rec)
		if err != nil {
			log.Printf("写入文件记录失败: %v", err)
			writeErr(w, http.StatusInternalServerError, "server_error", "记录文件失败")
			return
		}
		rec.ID = id
		added = append(added, rec)
	}

	for _, rec := range added {
		a.hub.broadcastRecord(rec)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "count": len(added)})
}

// ---------- /download ----------

var safeInlineExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true,
	"bmp": true, "ico": true, "avif": true,
	"mp4": true, "webm": true, "ogv": true, "mov": true, "m4v": true,
	"pdf": true,
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request, c *Caller) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	absRoot, err := filepath.Abs(a.cfg.RootDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "共享目录无效")
		return
	}

	var (
		full     string
		filename string
	)
	if idStr := r.URL.Query().Get("msg"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "msg 参数不合法")
			return
		}
		rec, err := a.getRecord(id)
		if err != nil || rec.FileTypeID == FileTypeText || rec.Content == "" {
			writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
			return
		}
		if !a.perm.Viewer(c).Can(rec.UserID, PermDownload) {
			writeErr(w, http.StatusForbidden, "forbidden", "无权下载该文件")
			return
		}
		full = filepath.Join(absRoot, filepath.Base(rec.Content))
		filename = filepath.Base(rec.Content)
	} else if p := r.URL.Query().Get("file"); p != "" {
		if !c.Has(PermManage) {
			writeErr(w, http.StatusForbidden, "forbidden", "仅 host 可通过 file 参数下载")
			return
		}
		full = filepath.Join(absRoot, filepath.FromSlash(p))
		filename = filepath.Base(full)
	} else {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 msg 或 file 参数")
		return
	}

	absFull, _ := filepath.Abs(full)
	if !withinRoot(absRoot, absFull) {
		writeErr(w, http.StatusForbidden, "forbidden", "路径越界")
		return
	}
	info, err := os.Stat(absFull)
	if err != nil || info.IsDir() {
		writeErr(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}

	f, err := os.Open(absFull)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "打开文件失败")
		return
	}
	defer f.Close()

	inline := r.URL.Query().Get("inline") == "1"
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	disposition := "attachment"
	if inline && safeInlineExt[ext] {
		disposition = "inline"
	}
	mimeType := mime.TypeByExtension("." + ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("下载中断: %v", err)
	}
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

func uniquePath(dir, name string) string {
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return dst
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext))
}
