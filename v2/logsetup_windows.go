//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	genericWrite   = 0x40000000
	openExisting   = 3
	fileShareRead  = 0x00000001
	fileShareWrite = 0x00000002
)

// openConsole 打开当前进程所属控制台（CONOUT$）。
// Windows GUI 子系统进程通常没有控制台，此时返回 ok=false。
func openConsole() (*os.File, bool) {
	name, err := syscall.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return nil, false
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createFile := kernel32.NewProc("CreateFileW")
	h, _, _ := createFile.Call(
		uintptr(unsafe.Pointer(name)),
		genericWrite,
		fileShareRead|fileShareWrite,
		0,
		openExisting,
		0,
		0,
	)
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return nil, false
	}
	return os.NewFile(h, "CONOUT$"), true
}

// clearCloexec：Windows 下 os/exec 对 *os.File 会自动 DuplicateHandle 给子进程，
// 无 CLOEXEC 概念，空实现即可。
func clearCloexec(f *os.File) {}