package main

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFiles embed.FS

const schemaVersion = 1

// Config 是服务启动参数。测试直接构造。
type Config struct {
	RootDir    string
	DBPath     string
	Port       int
	History    int
	DualWrite  bool
	BackupDays int
	HostToken  string
	HostName   string
}

func nowText() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func openDB(cfg Config) (*sql.DB, error) {
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}
	// V2 的串行写模型：单连接避免 SQLite 写冲突。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("启用外键失败: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	return db, nil
}

func migrateSchema(db *sql.DB, cfg Config) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于程序支持的 %d，请使用更新版本的程序", version, schemaVersion)
	}
	if version < schemaVersion && hasAnyTable(db) {
		// 迁移前快照，失败不阻断（没有写权限时仍允许启动）。
		if err := preMigrateBackup(db, cfg.DBPath); err != nil {
			log.Printf("迁移前快照失败（继续启动）: %v", err)
		}
	}

	raw, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("读取 schema.sql 失败: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(string(raw)); err != nil {
		return fmt.Errorf("执行 schema.sql 失败: %w", err)
	}
	if err := seedDictionaries(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func seedDictionaries(tx *sql.Tx) error {
	for _, r := range roleSeeds {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO role_types(id, name, perm_mask, description) VALUES(?,?,?,?)`,
			r.ID, r.Name, r.PermMask, r.Description); err != nil {
			return err
		}
	}
	for _, s := range shareTypeSeeds {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO share_types(id, name, description) VALUES(?,?,?)`,
			s.ID, s.Name, s.Description); err != nil {
			return err
		}
	}
	for _, s := range shareLevelSeeds {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO share_levels(id, name, perm_mask, description) VALUES(?,?,?,?)`,
			s.ID, s.Name, s.PermMask, s.Description); err != nil {
			return err
		}
	}
	for _, f := range fileTypeSeeds {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO file_types(id, name, description) VALUES(?,?,?)`,
			f.ID, f.Name, f.Description); err != nil {
			return err
		}
	}
	return nil
}

func hasAnyTable(db *sql.DB) bool {
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return err == nil && n > 0
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
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def)); err != nil {
			log.Printf("补列 %s.%s 失败: %v", table, col, err)
		}
	}
}

// ---------- 旧 messages 表（V2 兼容 / 双写） ----------

const legacyMessagesSchema = `
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

func ensureLegacyColumns(db *sql.DB) {
	ensureColumn(db, "messages", "kind", "TEXT NOT NULL DEFAULT 'text'")
	ensureColumn(db, "messages", "content", "TEXT NOT NULL DEFAULT ''")
	ensureColumn(db, "messages", "filename", "TEXT NOT NULL DEFAULT ''")
	ensureColumn(db, "messages", "filesize", "INTEGER NOT NULL DEFAULT 0")
	ensureColumn(db, "messages", "ext", "TEXT NOT NULL DEFAULT ''")
}

func ensureLegacyMessages(db *sql.DB) error {
	if _, err := db.Exec(legacyMessagesSchema); err != nil {
		return err
	}
	ensureLegacyColumns(db)
	return nil
}

// prepareLegacyStore 处理 V1 texts → V2 messages 的旧迁移，并按需创建/补齐双写表。
func prepareLegacyStore(db *sql.DB, cfg Config) error {
	if hasTable(db, "texts") && !hasTable(db, "messages") {
		if err := ensureLegacyMessages(db); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO messages(client_id, username, kind, content, created_at, ts)
			SELECT client_id, username, 'text', content, created_at, ts FROM texts`); err != nil {
			return fmt.Errorf("迁移 texts → messages 失败: %w", err)
		}
	}
	if cfg.DualWrite {
		return ensureLegacyMessages(db)
	}
	// 不双写时，旧表已存在则只补列（只读保留），不主动创建空表。
	if hasTable(db, "messages") {
		ensureLegacyColumns(db)
	}
	return nil
}

// ---------- 备份 ----------

func vacuumInto(db *sql.DB, path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	q := "VACUUM INTO '" + strings.ReplaceAll(path, "'", "''") + "'"
	_, err := db.Exec(q)
	return err
}

func preMigrateBackup(db *sql.DB, dbPath string) error {
	dir := filepath.Join(filepath.Dir(dbPath), "backup")
	name := "pre-migrate-" + time.Now().Format("20060102-150405")
	path := filepath.Join(dir, name+".db")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.db", name, i))
	}
	if err := vacuumInto(db, path); err != nil {
		return err
	}
	log.Printf("迁移前快照: %s", path)
	return nil
}

func backupDaily(db *sql.DB, cfg Config) error {
	if cfg.BackupDays <= 0 {
		return nil
	}
	dir := filepath.Join(filepath.Dir(cfg.DBPath), "backup")
	path := filepath.Join(dir, "LocalShT-"+time.Now().Format("20060102")+".db")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := vacuumInto(db, path); err != nil {
		return fmt.Errorf("创建日快照失败: %w", err)
	}
	log.Printf("日快照: %s", path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -cfg.BackupDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "LocalShT-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("清理旧快照失败 %s: %v", name, err)
		}
	}
	return nil
}

func pruneAudit(db *sql.DB) error {
	_, err := db.Exec(
		`DELETE FROM audit_log WHERE id NOT IN (
			SELECT id FROM audit_log ORDER BY id DESC LIMIT 5000
		)`)
	return err
}

// ---------- host 引导 ----------

func hostTokenPath(dbPath string) string {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		dir = "."
	}
	return filepath.Join(dir, "host_token.txt")
}

func readHostToken(dbPath string) string {
	b, err := os.ReadFile(hostTokenPath(dbPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeHostToken(dbPath, token string) (string, error) {
	path := hostTokenPath(dbPath)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return path, err
	}
	return path, nil
}

func (a *App) activeTokenForUser(userID int64) string {
	var token string
	err := a.db.QueryRow(
		`SELECT token FROM tokens WHERE user_id=? AND active=1 ORDER BY created_at LIMIT 1`,
		userID).Scan(&token)
	if err != nil {
		return ""
	}
	return token
}

// bootstrapHost 确保恰好一个 host 用户和一个可用 token，并落盘 host_token.txt。
func (a *App) bootstrapHost() (string, bool, error) {
	var hostID int64
	err := a.db.QueryRow(`SELECT id FROM users WHERE role_type_id=0 ORDER BY id LIMIT 1`).Scan(&hostID)

	if err == sql.ErrNoRows {
		desired := strings.TrimSpace(a.cfg.HostToken)
		tx, err := a.db.Begin()
		if err != nil {
			return "", false, err
		}
		defer tx.Rollback()
		name := a.cfg.HostName
		if name == "" {
			name = a.hostname
		}
		uid, token, _, err := createUserTx(tx, name, RoleHost, "", 0, 0, 0, desired)
		if err != nil {
			return "", false, err
		}
		if err := tx.Commit(); err != nil {
			return "", false, err
		}
		a.hostID = uid
		if _, err := writeHostToken(a.cfg.DBPath, token); err != nil {
			log.Printf("写入 host token 文件失败: %v", err)
		}
		a.auditRaw(nil, uid, RoleHost, "host.bootstrap", "首次启动创建 host")
		log.Printf("已创建 host 用户，token 文件: %s", hostTokenPath(a.cfg.DBPath))
		return token, true, nil
	}
	if err != nil {
		return "", false, err
	}
	a.hostID = hostID

	desired := strings.TrimSpace(a.cfg.HostToken)
	if desired != "" {
		var ownerID int64
		err := a.db.QueryRow(`SELECT user_id FROM tokens WHERE token=?`, desired).Scan(&ownerID)
		switch {
		case err == sql.ErrNoRows:
			// 指定 token 不存在：给 host 换用该 token。
			tx, err := a.db.Begin()
			if err != nil {
				return "", false, err
			}
			defer tx.Rollback()
			if _, err := tx.Exec(
				`UPDATE tokens SET active=0, revoked_at=? WHERE user_id=? AND active=1`,
				nowText(), hostID); err != nil {
				return "", false, err
			}
			if _, err := tx.Exec(
				`INSERT INTO tokens(token, user_id, active, created_at) VALUES(?,?,1,?)`,
				desired, hostID, nowText()); err != nil {
				return "", false, err
			}
			if err := tx.Commit(); err != nil {
				return "", false, err
			}
			if _, err := writeHostToken(a.cfg.DBPath, desired); err != nil {
				log.Printf("写入 host token 文件失败: %v", err)
			}
			a.auditRaw(nil, hostID, RoleHost, "token.reissue.host", "按 -host-token 重签")
			log.Printf("host token 已按 -host-token 重签，文件: %s", hostTokenPath(a.cfg.DBPath))
			return desired, true, nil
		case err != nil:
			return "", false, err
		case ownerID != hostID:
			return "", false, fmt.Errorf("-host-token 已被其他用户占用")
		default:
			// 该 token 可能被停用，确保它是 host 唯一的活跃 token。
			if _, err := a.db.Exec(
				`UPDATE tokens SET active=0, revoked_at=? WHERE user_id=? AND token<>? AND active=1`,
				nowText(), hostID, desired); err != nil {
				return "", false, err
			}
			if _, err := a.db.Exec(
				`UPDATE tokens SET active=1, revoked_at='' WHERE token=?`, desired); err != nil {
				return "", false, err
			}
			if _, err := writeHostToken(a.cfg.DBPath, desired); err != nil {
				log.Printf("写入 host token 文件失败: %v", err)
			}
			return desired, false, nil
		}
	}

	// 未指定：优先 token 文件，其次库里最早的活跃 host token，最后新签。
	token := readHostToken(a.cfg.DBPath)
	var ownerID int64
	if token != "" {
		err := a.db.QueryRow(
			`SELECT user_id FROM tokens WHERE token=? AND active=1`, token).Scan(&ownerID)
		if err == nil && ownerID == hostID {
			return token, false, nil
		}
	}
	token = a.activeTokenForUser(hostID)
	if token != "" {
		if _, err := writeHostToken(a.cfg.DBPath, token); err != nil {
			log.Printf("写入 host token 文件失败: %v", err)
		}
		return token, false, nil
	}

	// 没有任何活跃 token：新签一个。
	tx, err := a.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	token = newToken()
	if _, err := tx.Exec(
		`INSERT INTO tokens(token, user_id, active, created_at) VALUES(?,?,1,?)`,
		token, hostID, nowText()); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	if _, err := writeHostToken(a.cfg.DBPath, token); err != nil {
		log.Printf("写入 host token 文件失败: %v", err)
	}
	return token, true, nil
}

// ---------- V2 messages → records 迁移 ----------

type legacyMessage struct {
	ID        int64
	ClientID  string
	Username  string
	Kind      string
	Content   string
	Filename  string
	Filesize  int64
	Ext       string
	CreatedAt string
	Ts        int64
}

// migrateLegacy 把 messages 增量迁入 records / users / tokens / collections / shares。
// 幂等：以 records.legacy_message_id 为键，只处理 id 大于最大已迁移 id 的行。
func migrateLegacy(db *sql.DB, hostID int64) error {
	if !hasTable(db, "messages") {
		return nil
	}

	var maxLegacy sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(legacy_message_id) FROM records`).Scan(&maxLegacy); err != nil {
		return err
	}
	fromID := int64(0)
	if maxLegacy.Valid {
		fromID = maxLegacy.Int64
	}

	rows, err := db.Query(
		`SELECT id, client_id, username, kind, content, filename, filesize, ext, created_at, ts
		 FROM messages WHERE id > ? ORDER BY id`, fromID)
	if err != nil {
		return err
	}
	var list []legacyMessage
	for rows.Next() {
		var m legacyMessage
		if err := rows.Scan(&m.ID, &m.ClientID, &m.Username, &m.Kind, &m.Content,
			&m.Filename, &m.Filesize, &m.Ext, &m.CreatedAt, &m.Ts); err != nil {
			rows.Close()
			return err
		}
		list = append(list, m)
	}
	rows.Close()
	if len(list) == 0 {
		return nil
	}

	// 已有迁移用户：client_id -> user_id / collection_id
	users := make(map[string]int64)
	collections := make(map[int64]int64)
	urows, err := db.Query(`SELECT id, client_id FROM users WHERE client_id <> ''`)
	if err != nil {
		return err
	}
	for urows.Next() {
		var uid int64
		var cid string
		if err := urows.Scan(&uid, &cid); err != nil {
			urows.Close()
			return err
		}
		users[cid] = uid
	}
	urows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	collectionOf := func(uid int64) (int64, error) {
		if id, ok := collections[uid]; ok {
			return id, nil
		}
		var id int64
		err := tx.QueryRow(
			`SELECT id FROM collections WHERE user_id=? AND is_default=1`, uid).Scan(&id)
		if err != nil {
			return 0, err
		}
		collections[uid] = id
		return id, nil
	}

	inserted := 0
	for _, m := range list {
		cid := strings.TrimSpace(m.ClientID)
		if cid == "" {
			cid = "anon"
		}
		uid, ok := users[cid]
		if !ok {
			name := strings.TrimSpace(m.Username)
			if name == "" {
				name = cid
			}
			var token string
			var collID int64
			uid, token, collID, err = createUserTx(tx, name, RoleLV2, cid, hostID, ShareTypeAll, 3, "")
			if err != nil {
				return fmt.Errorf("迁移用户 %s 失败: %w", cid, err)
			}
			users[cid] = uid
			collections[uid] = collID
			_ = token
		}

		collID, err := collectionOf(uid)
		if err != nil {
			return fmt.Errorf("读取默认集合失败(user=%d): %w", uid, err)
		}

		ft := FileTypeText
		content := m.Content
		if m.Kind == "file" {
			ft = fileTypeID(m.Ext)
			content = m.Filename
		}
		createdAt := m.CreatedAt
		if createdAt == "" {
			createdAt = nowText()
		}
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO records
			 (user_id, username, file_type_id, content, filesize, ext, collection_id, created_at, ts, legacy_message_id)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			uid, m.Username, ft, content, m.Filesize, m.Ext, collID, createdAt, m.Ts, m.ID)
		if err != nil {
			return fmt.Errorf("迁移记录 %d 失败: %w", m.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("V2 messages 迁移完成：新增 %d 条记录（起始 id=%d）", inserted, fromID+1)
	return nil
}

// ---------- App 装配 ----------

func NewApp(cfg Config) (*App, error) {
	a := &App{cfg: cfg}
	a.hostname = cfg.HostName
	if a.hostname == "" {
		a.hostname, _ = os.Hostname()
	}
	if a.hostname == "" {
		a.hostname = "unknown-host"
	}

	db, err := openDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	a.db = db

	if err := migrateSchema(db, cfg); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库结构失败: %w", err)
	}
	perm, err := NewPerm(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("载入权限字典失败: %w", err)
	}
	a.perm = perm

	if err := a.loadPages(); err != nil {
		db.Close()
		return nil, err
	}

	if err := prepareLegacyStore(db, cfg); err != nil {
		db.Close()
		return nil, fmt.Errorf("准备旧表失败: %w", err)
	}

	hostToken, fresh, err := a.bootstrapHost()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 host 失败: %w", err)
	}
	a.hostToken = hostToken
	a.hostTokenFresh = fresh

	if err := migrateLegacy(db, a.hostID); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 V2 数据失败: %w", err)
	}

	if err := backupDaily(db, cfg); err != nil {
		log.Printf("日快照失败（继续启动）: %v", err)
	}
	if err := pruneAudit(db); err != nil {
		log.Printf("清理审计日志失败: %v", err)
	}

	a.hub = NewHub(a)
	return a, nil
}

func (a *App) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	return a.db.Close()
}
