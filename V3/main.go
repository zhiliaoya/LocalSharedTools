package main

import (
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const appVersion = "3.0"

// App 是 V3 的运行时容器。所有 handler 都是它的方法，便于测试隔离。
type App struct {
	cfg Config

	db   *sql.DB
	hub  *Hub
	perm *Perm

	appTmpl    *template.Template
	adminJS    []byte
	hostJS     []byte
	noauthHTML []byte

	hostname       string
	hostID         int64
	hostToken      string
	hostTokenFresh bool
	port           int
}

// ---------- 页面（服务端按角色组合） ----------

// handleApp 根据当前 token 的角色，实时拼装只属于该角色的主页 / 设置 DOM。
// 普通用户页面不会包含 admin / host 的 HTML，也不会引用它们的脚本。
func (a *App) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	c, _, err := a.resolveCaller(r)
	if err != nil || c == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// 链接里的 token 已在 resolveCaller 校验通过；写入 Cookie 后重定向，避免 token 留在地址栏。
	if r.URL.Query().Get("token") != "" {
		a.setAuthCookie(w, c.Token)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	a.setAuthCookie(w, c.Token)
	if a.appTmpl == nil {
		http.Error(w, "template not initialized", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := pageData{
		Caps:     CapsFromMask(c.PermMask),
		ShowRole: c.Has(PermShare) || c.Has(PermManage),
		RoleName: c.RoleName,
		UserName: c.UserName,
		UserID:   c.UserID,
	}
	if data.Caps.Share {
		data.AdminJS = template.JS(a.adminJS)
	}
	if data.Caps.Manage {
		data.HostJS = template.JS(a.hostJS)
	}
	if err := a.appTmpl.Execute(w, data); err != nil {
		log.Printf("渲染角色页面失败: %v", err)
	}
}

// handleLoginPage 不再提供 token 输入框：只接受链接 / 二维码里的 token。
// 有有效 token 就写 Cookie 并跳转主页；否则返回说明页。
func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, _, err := a.resolveCaller(r); err == nil && c != nil {
		a.setAuthCookie(w, c.Token)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(a.noauthHTML); err != nil {
		log.Printf("写登录说明页失败: %v", err)
	}
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "version": appVersion})
}

// ---------- 路由 ----------

// routes 注册 API 与角色页面；角色页面由服务端把对应增量脚本内联到 HTML。
// 静态公共资源由 initStatic 追加。
func (a *App) routes() *http.ServeMux {
	mux := a.apiMux()
	mux.HandleFunc("/login", a.handleLoginPage)
	mux.HandleFunc("/", a.handleApp)
	return mux
}

// apiMux 只注册 JSON / WebSocket API。
func (a *App) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/auth/login", a.handleLogin)
	mux.HandleFunc("/auth/logout", a.handleLogout)
	mux.Handle("/auth/me", a.auth(a.handleMe))
	mux.Handle("/profile", a.auth(a.handleProfile))
	mux.Handle("/history", a.auth(a.handleHistory))
	mux.Handle("/texts", a.auth(a.handleCreateText))
	mux.Handle("/upload", a.auth(a.handleUpload))
	mux.Handle("/download", a.auth(a.handleDownload))
	mux.HandleFunc("/ws", a.handleWS)
	mux.Handle("/share/qr", a.auth(a.handleShareQR))
	mux.Handle("/share/revoke", a.auth(a.handleRevoke))
	mux.Handle("/admin/users", a.auth(a.handleAdminUsers))
	mux.Handle("/admin/users/role", a.auth(a.handleAdminUserRole))
	mux.Handle("/admin/users/active", a.auth(a.handleAdminUserActive))
	mux.Handle("/admin/users/revoke-token", a.auth(a.handleAdminRevokeTokens))
	mux.Handle("/admin/shares", a.auth(a.handleAdminShares))
	mux.Handle("/admin/reissue", a.auth(a.handleReissue))
	return mux
}

// ---------- 网络工具 ----------

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

// openBrowser 用默认浏览器打开 URL（仅 Windows 有效）。
func openBrowser(target string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("cmd", "/c", "start", "", target).Start()
	}
}

// ---------- main ----------

func main() {
	var cfg Config
	flag.StringVar(&cfg.RootDir, "dir", "LocalShT/shared", "shared directory (auto-created if missing)")
	flag.StringVar(&cfg.DBPath, "db", "LocalShT/data/LocalShT.db", "sqlite db file (parent dir auto-created)")
	flag.IntVar(&cfg.Port, "port", 5050, "listen port")
	flag.IntVar(&cfg.History, "history", 200, "how many history rows to load")
	flag.BoolVar(&cfg.DualWrite, "dualwrite", true, "also write legacy messages table (V2 rollback bridge)")
	flag.IntVar(&cfg.BackupDays, "backup-days", 7, "keep daily VACUUM INTO snapshots for N days (0 disables)")
	flag.StringVar(&cfg.HostToken, "host-token", "", "explicit host token (recovery / first boot)")
	flag.Parse()

	if cfg.Port <= 0 || cfg.Port > 65535 {
		log.Fatalf("端口不合法: %d", cfg.Port)
	}
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}

	// 日志重定向必须先于 DB 打开，保证初始化失败也落文件。
	initLogging(filepath.Dir(cfg.DBPath))

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		log.Fatalf("解析共享目录失败: %v", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		log.Fatalf("创建共享目录失败: %v", err)
	}

	app, err := NewApp(cfg)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer app.Close()

	mux := app.routes()
	app.initStatic(mux)

	requestedPort := cfg.Port

	// 防呆：端口上已有「同一 exe」的旧实例时，打开浏览器后退出。
	if pid, same := sameExeOnPort(requestedPort); same {
		target := fmt.Sprintf("http://localhost:%d", requestedPort)
		log.Printf("检测到已有实例在端口 %d 运行（PID=%d），打开浏览器后退出", requestedPort, pid)
		openBrowser(target)
		return
	}

	ln, actualPort, err := listenWithFallback(requestedPort, 100)
	if err != nil {
		log.Fatal(err)
	}
	app.port = actualPort

	if actualPort != requestedPort {
		log.Printf("端口 %d 不可用，已自动切换到 %d", requestedPort, actualPort)
	}

	dbAbs, _ := filepath.Abs(cfg.DBPath)
	fmt.Printf("LocalShT V%s\n", appVersion)
	fmt.Printf("数据库: %s\n", dbAbs)
	fmt.Printf("共享目录: %s\n", absRoot)
	fmt.Printf("主机名: %s\n", app.hostname)
	fmt.Printf("本机访问: http://localhost:%d\n", actualPort)
	for _, ip := range localIPs() {
		fmt.Printf("局域网访问: http://%s:%d\n", ip, actualPort)
	}
	fmt.Printf("Host token 文件: %s\n", hostTokenPath(cfg.DBPath))
	if app.hostTokenFresh {
		fmt.Printf("Host 登录: http://localhost:%d/login?token=%s\n", actualPort, app.hostToken)
		fmt.Println("（首次生成的 host token 请妥善保存；丢失可用 -host-token 重签）")
	}
	fmt.Println("按 Ctrl+C 或关闭窗口结束（GUI 编译版请用任务管理器结束）")

	loginTarget := fmt.Sprintf("http://localhost:%d", actualPort)
	if app.hostTokenFresh {
		loginTarget += "/login?token=" + url.QueryEscape(app.hostToken)
	}
	openBrowser(loginTarget)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(server.Serve(ln))
}
