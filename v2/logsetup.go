package main

import (
	"log"
	"os"
	"path/filepath"
)

// logFile 保存日志文件句柄。进程生命周期内不主动关闭。
var logFile *os.File

// initLogging 统一初始化日志，三种分支：
//   1) 无控制台     → 全部写文件（GUI 模式不崩）
//   2) 有控制台     → 接管 os.Stdout/os.Stderr，管道内容双写「原控制台 + 日志文件」
//   3) 管道创建失败 → 降级为全部写文件（容错）
//
// dbDir 用于定位日志文件目录（与原先一致：db 同目录下 LocalShT.log）。
func initLogging(dbDir string) {
	if dbDir == "" || dbDir == "." {
		dbDir = "."
	}
	logPath := filepath.Join(dbDir, "LocalShT.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// 连日志文件都打不开，只能退回 stderr（尽力而为）
		log.SetOutput(os.Stderr)
		return
	}
	logFile = lf

	// 分支 1：无控制台
	if !detectConsole() {
		os.Stdout = lf
		os.Stderr = lf
		log.SetOutput(lf)
		log.Printf("===== 启动（无控制台，日志仅写文件）=====")
		return
	}

	// 有控制台：先保存原句柄，作为「原控制台」回写目标
	oldStdout := os.Stdout
	oldStderr := os.Stderr

	stdoutR, stdoutW, err1 := os.Pipe()
	stderrR, stderrW, err2 := os.Pipe()
	if err1 != nil || err2 != nil {
		// 分支 3：管道失败 → 关闭已创建的一端，降级为全部写文件
		for _, f := range []*os.File{stdoutR, stdoutW, stderrR, stderrW} {
			if f != nil {
				f.Close()
			}
		}
		os.Stdout = lf
		os.Stderr = lf
		log.SetOutput(lf)
		log.Printf("===== 启动（管道创建失败，日志仅写文件）=====")
		return
	}

	// 关键：清掉 CLOEXEC，让 os/exec 启动的子进程能继承写端（Linux 上必须）
	clearCloexec(stdoutW)
	clearCloexec(stderrW)

	// 分支 2：接管 stdout/stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW
	// log 包指向 os.Stderr（管道），从而天然走 pump 双写；
	// fmt.Printf 走 os.Stdout，同理。
	log.SetOutput(os.Stderr)

	go pump(stdoutR, oldStdout, lf)
	go pump(stderrR, oldStderr, lf)

	log.Printf("===== 启动（控制台 + 文件双写）=====")
}

// detectConsole 判断当前是否有可写的控制台/终端。
func detectConsole() bool {
	c, ok := openConsole()
	if ok {
		c.Close()
	}
	return ok
}

// pump 从管道读取，同时写到「原控制台」和日志文件。
// 注意：直接 os.File.Write，避免再次触发 os.Stdout/os.Stderr 重定向形成环。
// lf 与 log 包自身写入可能交错，但日志行一般不跨 4KB 单次 syscall，可接受。
func pump(r, console, file *os.File) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if console != nil {
				_, _ = console.Write(buf[:n])
			}
			_, _ = file.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}