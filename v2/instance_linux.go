package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sameExeOnPort：在非 Windows 平台，通过 /proc 检测占用端口的进程是否就是当前 exe。
// 检测失败时返回 (0, false)，不影响正常启动。
func sameExeOnPort(p int) (int, bool) {
	pid, ok := pidOnPortLinux(p)
	if !ok {
		return 0, false
	}
	self, err := os.Executable()
	if err != nil {
		return pid, false
	}
	self, _ = filepath.EvalSymlinks(self)
	self, _ = filepath.Abs(self)

	exeLink := fmt.Sprintf("/proc/%d/exe", pid)
	other, err := os.Readlink(exeLink)
	if err != nil {
		return pid, false
	}
	// Readlink 可能带 " (deleted)" 后缀
	other = strings.TrimSuffix(other, " (deleted)")
	other, _ = filepath.Abs(other)

	return pid, self == other
}

// pidOnPortLinux 通过 /proc/net/tcp{,6} 找出监听指定端口的 PID。
// 该实现依赖对 /proc/<pid>/fd 的读权限；容器内或不同用户时可能失败。
func pidOnPortLinux(port int) (int, bool) {
	portHex := fmt.Sprintf("%04X", port)

	var inodes []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[1:] { // 跳过表头
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}
			// fields[1] = local_address (hexIP:hexPort), fields[3] = state, fields[9] = inode
			if !strings.HasSuffix(fields[1], ":"+portHex) {
				continue
			}
			if fields[3] != "0A" { // 0A = LISTEN
				continue
			}
			inodes = append(inodes, fields[9])
		}
	}
	if len(inodes) == 0 {
		return 0, false
	}

	// 遍历 /proc/<pid>/fd，找 socket:[inode]
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // 非数字目录
		}
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			for _, inode := range inodes {
				if link == "socket:["+inode+"]" {
					return pid, true
				}
			}
		}
	}
	return 0, false
}