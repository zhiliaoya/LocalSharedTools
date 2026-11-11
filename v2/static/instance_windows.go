package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

// pidOnPort 解析 netstat 输出，找监听指定端口的 PID
func pidOnPort(p int) (int, bool) {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return 0, false
	}
	needle := fmt.Sprintf(":%d ", p)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "LISTENING") || !strings.Contains(line, needle) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err == nil && pid > 0 {
			return pid, true
		}
	}
	return 0, false
}

// exePathOfPID 查询指定 PID 的可执行文件完整路径（Windows API）
func exePathOfPID(pid int) (string, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProc := kernel32.NewProc("OpenProcess")
	queryFull := kernel32.NewProc("QueryFullProcessImageNameW")
	closeHandle := kernel32.NewProc("CloseHandle")

	h, _, callErr := openProc.Call(PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(pid))
	if h == 0 {
		return "", fmt.Errorf("OpenProcess failed: %v", callErr)
	}
	defer closeHandle.Call(h)

	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	r, _, callErr := queryFull.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return "", fmt.Errorf("QueryFullProcessImageName failed: %v", callErr)
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

// sameExeOnPort：占用端口的进程是否就是当前 exe（同一路径）
func sameExeOnPort(p int) (int, bool) {
	pid, ok := pidOnPort(p)
	if !ok {
		return 0, false
	}
	other, err := exePathOfPID(pid)
	if err != nil {
		return pid, false
	}
	self, err := os.Executable()
	if err != nil {
		return pid, false
	}
	a, _ := filepath.Abs(other)
	b, _ := filepath.Abs(self)
	return pid, strings.EqualFold(a, b)
}