package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMigration(t *testing.T) {
	cfg := testConfig(t)
	seedLegacyMessages(t, cfg.DBPath, []legacyMessage{
		{ClientID: "cid-a", Username: "张三", Kind: "text", Content: "hello", Ts: 1000},
		{ClientID: "cid-a", Username: "张三", Kind: "file", Filename: "pic.png", Filesize: 123, Ext: "png", Ts: 2000},
		{ClientID: "cid-b", Username: "李四", Kind: "text", Content: "hi", Ts: 3000},
	})

	app := newTestAppWith(t, cfg)

	if n := getCount(t, app, `SELECT count(*) FROM records`); n != 3 {
		t.Fatalf("records = %d, want 3", n)
	}
	if n := getCount(t, app, `SELECT count(*) FROM users`); n != 3 {
		t.Fatalf("users = %d, want 3 (host + 2 legacy)", n)
	}
	if n := getCount(t, app, `SELECT count(*) FROM tokens`); n != 3 {
		t.Fatalf("tokens = %d, want 3", n)
	}
	if n := getCount(t, app, `SELECT count(*) FROM collections`); n != 3 {
		t.Fatalf("collections = %d, want 3", n)
	}
	if n := getCount(t, app, `SELECT count(*) FROM shares WHERE active=1`); n != 2 {
		t.Fatalf("legacy shares = %d, want 2", n)
	}

	var ft int
	if err := app.db.QueryRow(`SELECT file_type_id FROM records WHERE legacy_message_id=2`).Scan(&ft); err != nil {
		t.Fatalf("query file type: %v", err)
	}
	if ft != FileTypeImage {
		t.Fatalf("file_type_id = %d, want %d", ft, FileTypeImage)
	}
	var content string
	if err := app.db.QueryRow(`SELECT content FROM records WHERE legacy_message_id=2`).Scan(&content); err != nil {
		t.Fatalf("query content: %v", err)
	}
	if content != "pic.png" {
		t.Fatalf("content = %q, want pic.png", content)
	}

	// 重复启动不重复迁移。
	if err := app.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	app2, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer app2.Close()
	if n := getCount(t, app2, `SELECT count(*) FROM records`); n != 3 {
		t.Fatalf("after restart records = %d, want 3", n)
	}
	if n := getCount(t, app2, `SELECT count(*) FROM users`); n != 3 {
		t.Fatalf("after restart users = %d, want 3", n)
	}
}

func TestDualWriteToggle(t *testing.T) {
	cfg := testConfig(t)
	cfg.DualWrite = true
	app := newTestAppWith(t, cfg)
	host := hostCaller(t, app)
	insertTestRecord(t, app, host, FileTypeText, "双写开启", 0, "")

	if n := getCount(t, app, `SELECT count(*) FROM messages`); n != 1 {
		t.Fatalf("messages = %d, want 1", n)
	}
	if n := getCount(t, app, `SELECT count(*) FROM records`); n != 1 {
		t.Fatalf("records = %d, want 1", n)
	}
	_ = app.Close()

	cfg.DualWrite = false
	app2, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer app2.Close()
	host2 := hostCaller(t, app2)
	insertTestRecord(t, app2, host2, FileTypeText, "双写关闭", 0, "")

	if n := getCount(t, app2, `SELECT count(*) FROM messages`); n != 1 {
		t.Fatalf("messages after dualwrite off = %d, want 1", n)
	}
	if n := getCount(t, app2, `SELECT count(*) FROM records`); n != 2 {
		t.Fatalf("records = %d, want 2", n)
	}
}

func TestBackupAndSchemaVersion(t *testing.T) {
	cfg := testConfig(t)
	cfg.BackupDays = 1
	app := newTestAppWith(t, cfg)

	var version int
	if err := app.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(cfg.DBPath), "backup", "LocalShT-*.db"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("daily backup missing: %v %v", matches, err)
	}
}

func TestPreMigrationBackup(t *testing.T) {
	cfg := testConfig(t)
	seedLegacyMessages(t, cfg.DBPath, []legacyMessage{
		{ClientID: "cid-x", Username: "x", Kind: "text", Content: "old", Ts: 1000},
	})
	_ = newTestAppWith(t, cfg)
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(cfg.DBPath), "backup", "pre-migrate-*.db"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("pre-migrate backup missing: %v %v", matches, err)
	}
	info, err := os.Stat(matches[0])
	if err != nil || info.Size() == 0 {
		t.Fatalf("pre-migrate backup empty: %v %v", info, err)
	}
}
