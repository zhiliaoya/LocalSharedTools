package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// staticProvider 描述前端静态资源（index.html 模板 + /static/ 文件服务）的来源。
type staticProvider struct {
	ReadIndex  func() ([]byte, error)
	FileServer http.Handler
	Desc       string
}

// resolveStatic 决定前端资源来源：
//   1) 编译时 -tags embedstatic → 使用内嵌 FS
//   2) 否则 → 从可执行文件/工作目录旁的 static 目录读取
func resolveStatic() (*staticProvider, error) {
	if fsys, ok := staticSource(); ok {
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			return nil, fmt.Errorf("内嵌 static/index.html 读取失败: %w", err)
		}
		return &staticProvider{
			ReadIndex:  func() ([]byte, error) { return index, nil },
			FileServer: http.FileServer(http.FS(fsys)),
			Desc:       "内嵌 static/（编译期 embedstatic）",
		}, nil
	}

	dir, err := findStaticDir()
	if err != nil {
		return nil, err
	}
	return &staticProvider{
		ReadIndex: func() ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, "index.html"))
		},
		FileServer: http.FileServer(http.Dir(dir)),
		Desc:       "磁盘 " + dir,
	}, nil
}

// mustInitStatic 解析前端来源、编译模板、注册 /static/ 路由。失败直接 log.Fatal。
func mustInitStatic(mux *http.ServeMux) {
	sp, err := resolveStatic()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("前端资源来源：%s", sp.Desc)

	index, err := sp.ReadIndex()
	if err != nil {
		log.Fatalf("读取 index.html 失败: %v", err)
	}
	tmpl = template.Must(template.New("index.html").Parse(string(index)))

	mux.Handle("/static/", http.StripPrefix("/static/", sp.FileServer))
}