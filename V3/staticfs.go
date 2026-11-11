package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// staticProvider 描述 /static/ 公共资源（app.css / app.js）的来源。
// 角色页面与 admin/host 增量脚本在 web/ 中编译期内嵌，不通过 /static/ 暴露。
type staticProvider struct {
	FileServer http.Handler
	Desc       string
}

// resolveStatic 决定公共静态资源来源：
//  1. 编译时 -tags embedstatic → 使用内嵌 FS
//  2. 否则 → 从可执行文件/工作目录旁的 static 目录读取
func resolveStatic() (*staticProvider, error) {
	if fsys, ok := staticSource(); ok {
		return &staticProvider{
			FileServer: http.FileServer(http.FS(fsys)),
			Desc:       "内嵌 static/（编译期 embedstatic）",
		}, nil
	}

	dir, err := findStaticDir()
	if err != nil {
		return nil, err
	}
	return &staticProvider{
		FileServer: http.FileServer(http.Dir(dir)),
		Desc:       "磁盘 " + dir,
	}, nil
}

// initStatic 注册 /static/。角色页面由 routes() 处理。
func (a *App) initStatic(mux *http.ServeMux) {
	sp, err := resolveStatic()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("前端资源来源：%s", sp.Desc)
	// 旧版本可能把 index.html / login.html 留在 static/ 下；这里显式屏蔽，避免旧页面被继续访问。
	for _, name := range []string{"/static/index.html", "/static/login.html"} {
		mux.HandleFunc(name, func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	mux.Handle("/static/", http.StripPrefix("/static/", sp.FileServer))
}

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
