//go:build linux

package main

import (
	"os"
	"syscall"
)

// openConsole：Linux 下用 /dev/tty 判断是否有控制终端。
// cron/daemon 场景打开失败 → 无控制台。
func openConsole() (*os.File, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, false
	}
	return tty, true
}

// clearCloexec 清除 fd 的 O_CLOEXEC。
// Go 运行时的 os.Pipe() 在 Linux 上默认设置 FD_CLOEXEC，
// 不清掉的话，exec 出的子进程（如 netstat）继承不到写端句柄，stdout 会丢。
func clearCloexec(f *os.File) {
	if f == nil {
		return
	}
	fd := f.Fd()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
	if errno != 0 {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, flags&^uintptr(syscall.FD_CLOEXEC))
}