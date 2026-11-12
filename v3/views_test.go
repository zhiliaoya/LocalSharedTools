package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToViewsGroupingAndDownloadGate(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	base := int64(1700000000000)
	rows := []Record{
		{ID: 1, UserID: host.UserID, Username: host.UserName, FileTypeID: FileTypeText, Content: "hello", Ts: base, CreatedAt: nowText()},
		{ID: 2, UserID: host.UserID, Username: host.UserName, FileTypeID: FileTypeImage, Content: "a.png", Ext: "png", Filesize: 10, Ts: base + 1000, CreatedAt: nowText()},
		{ID: 3, UserID: host.UserID, Username: host.UserName, FileTypeID: FileTypeImage, Content: "b.png", Ext: "png", Filesize: 20, Ts: base + 2000, CreatedAt: nowText()},
		{ID: 4, UserID: host.UserID, Username: host.UserName, FileTypeID: FileTypeText, Content: "later", Ts: base + 400000, CreatedAt: nowText()},
	}

	views := toViews(rows, app.perm.Viewer(host))
	if len(views) != 3 {
		t.Fatalf("views = %d, want 3", len(views))
	}
	if views[0].Kind != "text" || views[0].Content != "hello" {
		t.Fatalf("first view = %+v", views[0])
	}
	if views[1].Kind != "file" || views[1].FileCount != 2 || len(views[1].Files) != 2 {
		t.Fatalf("group view = %+v", views[1])
	}
	if views[1].Files[0].DownloadURL == "" || views[1].Files[0].PreviewURL == "" {
		t.Fatalf("host should get download/preview urls: %+v", views[1].Files[0])
	}
	if !views[2].ShowDivider {
		t.Fatal("later view should start a new divider")
	}

	// lv1 只有 VIEW，没有 DOWNLOAD：文件可见但没有直链。
	_, token := makeUser(t, app, "lv1", RoleLV1, host.UserID, ShareTypeSpecific, 1)
	lv1 := callerFor(t, app, token)
	views = toViews(rows, app.perm.Viewer(lv1))
	if len(views) != 3 {
		t.Fatalf("lv1 views = %d", len(views))
	}
	if views[1].Files[0].DownloadURL != "" || views[1].Files[0].PreviewURL != "" {
		t.Fatalf("lv1 should not get urls: %+v", views[1].Files[0])
	}
	if views[1].CanDownload {
		t.Fatal("lv1 CanDownload should be false")
	}
}

func TestViewsSelfAndDivider(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	rec := Record{ID: 9, UserID: host.UserID, Username: "ignored", FileTypeID: FileTypeText, Content: "x", Ts: 1000}
	v := toView(&rec, app.perm.Viewer(host))
	if !v.IsSelf || v.DisplayName != "我" {
		t.Fatalf("self view = %+v", v)
	}
	if !v.ShowDivider {
		t.Fatal("first view should show divider")
	}
}

func TestWithinRootAndFileType(t *testing.T) {
	root := t.TempDir()
	if !withinRoot(root, filepath.Join(root, "a.txt")) {
		t.Fatal("child should be within root")
	}
	if withinRoot(root, filepath.Join(root, "..", "escape.txt")) {
		t.Fatal("parent path should be rejected")
	}
	if withinRoot(root, root+"-sibling") {
		t.Fatal("sibling path should be rejected")
	}
	if fileTypeID("png") != FileTypeImage || fileTypeID("mp4") != FileTypeVideo ||
		fileTypeID("pdf") != FileTypePDF || fileTypeID("exe") != FileTypeOther {
		t.Fatal("file type mapping mismatch")
	}
}

func TestUploadUniquePath(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := uniquePath(dir, "a.txt")
	if second == first {
		t.Fatal("uniquePath returned existing path")
	}
	if filepath.Base(second) != "a(1).txt" {
		t.Fatalf("unique name = %s", filepath.Base(second))
	}
}
