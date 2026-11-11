package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ---------- 视图模型 ----------

type FileItem struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	Filesize    int64  `json:"filesize"`
	SizeText    string `json:"size_text"`
	Ext         string `json:"ext,omitempty"`
	PreviewKind string `json:"preview_kind,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

type MessageView struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Kind      string `json:"kind"` // text | file
	Content   string `json:"content"`
	Filename  string `json:"filename,omitempty"`
	Filesize  int64  `json:"filesize,omitempty"`
	Ext       string `json:"ext,omitempty"`
	CreatedAt string `json:"created_at"`
	Ts        int64  `json:"ts"`

	Username    string `json:"username"`
	IsSelf      bool   `json:"is_self"`
	DisplayName string `json:"display_name"`
	AvatarText  string `json:"avatar_text"`
	SizeText    string `json:"size_text,omitempty"`
	TimeLabel   string `json:"time_label"`
	ShowDivider bool   `json:"show_divider"`
	CanDownload bool   `json:"can_download"`

	// 文件组
	FileCount int        `json:"file_count,omitempty"`
	Files     []FileItem `json:"files,omitempty"`

	// 兼容单文件快捷字段（= files[0]）
	PreviewKind string `json:"preview_kind,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// ---------- 展示辅助 ----------

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

func recordExt(r *Record) string {
	ext := r.Ext
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(r.Content), "."))
	}
	return ext
}

func toFileItem(r *Record, canDownload bool) FileItem {
	ext := recordExt(r)
	pk := previewKind(ext)
	it := FileItem{
		ID:       r.ID,
		Filename: r.Content,
		Filesize: r.Filesize,
		SizeText: humanSize(r.Filesize),
		Ext:      ext,
	}
	if canDownload {
		it.PreviewKind = pk
		it.DownloadURL = fmt.Sprintf("/download?msg=%d", r.ID)
		if pk != "" {
			it.PreviewURL = fmt.Sprintf("/download?msg=%d&inline=1", r.ID)
		}
	}
	return it
}

// toViews 把有序记录按「相邻同人文件 ≤60s」合组成 MessageView 列表。
// 传入的 rows 必须已按查看者权限过滤；DOWNLOad 能力逐行判定，无权者拿不到直链。
func toViews(rows []Record, viewer *Viewer) []MessageView {
	out := make([]MessageView, 0, len(rows))
	const fiveMin = int64(5 * 60 * 1000)
	var lastTs int64

	var viewerID int64
	if viewer != nil && viewer.caller != nil {
		viewerID = viewer.caller.UserID
	}

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
		r := &rows[i]

		isSelf := viewerID != 0 && r.UserID == viewerID
		dispName := "匿名"
		if isSelf {
			dispName = "我"
		} else if strings.TrimSpace(r.Username) != "" {
			dispName = r.Username
		}
		avatar := avatarText(dispName)
		canDownload := viewer != nil && viewer.Can(r.UserID, PermDownload)

		if r.FileTypeID != FileTypeText {
			if curGroup != nil &&
				curGroup.UserID == r.UserID &&
				r.Ts-curGroup.Ts <= fileGroupWindowMs {
				curGroupFiles = append(curGroupFiles, toFileItem(r, canDownload))
				lastTs = r.Ts
				continue
			}
			flushGroup()
			curGroup = &MessageView{
				ID: r.ID, UserID: r.UserID, Kind: "file",
				Username: r.Username, IsSelf: isSelf, DisplayName: dispName, AvatarText: avatar,
				CreatedAt: r.CreatedAt, Ts: r.Ts,
				TimeLabel:   timeLabel(r.Ts),
				ShowDivider: (lastTs == 0) || (r.Ts-lastTs > fiveMin),
				CanDownload: canDownload,
			}
			curGroupFiles = []FileItem{toFileItem(r, canDownload)}
			lastTs = r.Ts
			continue
		}

		flushGroup()
		v := MessageView{
			ID: r.ID, UserID: r.UserID, Kind: "text", Content: r.Content,
			Username: r.Username, IsSelf: isSelf, DisplayName: dispName, AvatarText: avatar,
			CreatedAt: r.CreatedAt, Ts: r.Ts,
			TimeLabel:   timeLabel(r.Ts),
			ShowDivider: (lastTs == 0) || (r.Ts-lastTs > fiveMin),
			CanDownload: canDownload,
		}
		lastTs = r.Ts
		out = append(out, v)
	}
	flushGroup()
	return out
}

func toView(r *Record, viewer *Viewer) MessageView {
	vs := toViews([]Record{*r}, viewer)
	if len(vs) == 0 {
		return MessageView{}
	}
	return vs[0]
}
