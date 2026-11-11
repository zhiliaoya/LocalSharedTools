package main

import (
	"embed"
	"fmt"
	"html/template"
)

//go:embed web
var webFS embed.FS

// pageData 是服务端按角色组合页面时使用的数据。
// 页面模板只输出当前角色需要的 DOM 与脚本引用。
type pageData struct {
	Caps     Caps
	ShowRole bool
	RoleName string
	UserName string
	UserID   int64
	AdminJS  template.JS
	HostJS   template.JS
}

func (a *App) loadPages() error {
	appHTML, err := webFS.ReadFile("web/app.html")
	if err != nil {
		return fmt.Errorf("读取 web/app.html 失败: %w", err)
	}
	tmpl, err := template.New("app.html").Parse(string(appHTML))
	if err != nil {
		return fmt.Errorf("解析 web/app.html 失败: %w", err)
	}
	a.appTmpl = tmpl

	if a.adminJS, err = webFS.ReadFile("web/admin.js"); err != nil {
		return fmt.Errorf("读取 web/admin.js 失败: %w", err)
	}
	if a.hostJS, err = webFS.ReadFile("web/host.js"); err != nil {
		return fmt.Errorf("读取 web/host.js 失败: %w", err)
	}
	if a.noauthHTML, err = webFS.ReadFile("web/noauth.html"); err != nil {
		return fmt.Errorf("读取 web/noauth.html 失败: %w", err)
	}
	return nil
}
