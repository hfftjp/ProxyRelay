package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

// Windows API定数
const (
	FILE_ATTRIBUTE_TEMPORARY = 0x00000100
	FILE_SHARE_DELETE        = 0x00000004
	CP_932                   = 932
	CP_UTF8                  = 65001
)

var (
	procMultiByteToWideChar = modkernel32.NewProc("MultiByteToWideChar")
	procWideCharToMultiByte = modkernel32.NewProc("WideCharToMultiByte")
)

// 文字コード(UTF-8/SJIS)を自動判別してUTF-8文字列を返す
func autoDecode(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}
	res1, _, _ := procMultiByteToWideChar.Call(uintptr(CP_932), 0, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	if res1 == 0 {
		return string(data)
	}
	utf16 := make([]uint16, res1)
	procMultiByteToWideChar.Call(uintptr(CP_932), 0, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&utf16[0])), res1)
	res2, _, _ := procWideCharToMultiByte.Call(uintptr(CP_UTF8), 0, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)), 0, 0, 0, 0)
	if res2 == 0 {
		return string(data)
	}
	res := make([]byte, res2)
	procWideCharToMultiByte.Call(uintptr(CP_UTF8), 0, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)), uintptr(unsafe.Pointer(&res[0])), res2, 0, 0)
	return string(res)
}

var (
	hookMu         sync.Mutex
	hookCancelFunc context.CancelFunc
	hookDone       chan struct{}
)

// BAT実行
func runHookWithConfig(prefix string) bool {
	if !ReloadCfg() {
		return false
	}
	sec := cfg.Section("Hook")
	batFile := GetStringSafe(sec.Key(prefix + "Bat"))
	if batFile == "" {
		return true
	}
	hookMu.Lock()
	if hookCancelFunc != nil {
		hookMu.Unlock()
		logOutput("ERROR", prefix, "Aborted: Another hook is already running")
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	hookCancelFunc = cancel
	hookDone = make(chan struct{})
	hookMu.Unlock()
	defer func() {
		hookMu.Lock()
		if hookDone != nil {
			close(hookDone)
			hookDone = nil
		}
		hookCancelFunc = nil
		hookMu.Unlock()
	}()
	notify("INFO", prefix, "Processing")
	path := filepath.Join(CONF_DIR, batFile)
	wCond := GetIntSafe(sec.Key(prefix + "WaitNot"))
	wPort := GetStringSafe(sec.Key(prefix + "WaitPort"))
	wProc := GetStringSafe(sec.Key(prefix + "WaitProc"))
	tout := GetIntSafe(sec.Key(prefix+"Timeout"), 30)
	interval := GetIntSafe(cfg.Section("Hook").Key("HookInterval"), 1000)
	tmpPath := filepath.Join(LogDir, fmt.Sprintf("proxyrelay_hook_out_%d.log", time.Now().UnixNano()))
	ptrPath, _ := syscall.UTF16PtrFromString(tmpPath)
	h, err := syscall.CreateFile(ptrPath, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|FILE_SHARE_DELETE,
		nil, syscall.CREATE_ALWAYS, FILE_ATTRIBUTE_TEMPORARY, 0)
	if err != nil {
		logOutput("ERROR", prefix, "Failed to create temp file: %v", err)
		return false
	}
	tmpFile := os.NewFile(uintptr(h), tmpPath)
	defer os.Remove(tmpPath)
	cmd := exec.Command("cmd.exe", "/c", path)
	cmd.Stdout = tmpFile
	cmd.Stderr = tmpFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	runErr := cmd.Run()
	tmpFile.Close()
	output, readErr := os.ReadFile(tmpPath)
	if readErr == nil && len(output) > 0 {
		outStr := strings.TrimSpace(autoDecode(output))
		lines := regexp.MustCompile(`\r?\n`).Split(outStr, -1)
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				logOutput("LOG", prefix, line)
			}
		}
	}
	os.Remove(tmpPath)
	if runErr != nil {
		notify("ERROR", prefix, "Aborted")
		return false
	}
	if wPort == "" && wProc == "" {
		notify("INFO", prefix, "Done")
		return true
	}
	logOutput("LOG", prefix, "Waiting for proc/port ")
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			notify("ERROR", prefix, "Cancelled")
			return false
		default:
			if time.Since(start) > time.Duration(tout)*time.Second {
				notify("ERROR", prefix, "Timeout")
				return false
			}
			isExist := checkCondition(wPort, wProc)
			if (wCond == 0 && isExist) || (wCond == 1 && !isExist) {
				notify("INFO", prefix, "Done")
				return true
			}
			time.Sleep(time.Duration(interval) * time.Millisecond)
		}
	}
}

// BAT実行内判定待ちループの外部からの中断(中断するまで待機)
func stopHook() {
	hookMu.Lock()
	cancel := hookCancelFunc
	done := hookDone
	hookMu.Unlock()
	if cancel != nil && done != nil {
		cancel()
		<-done
	}
}

// BAT実行内判定ロジック(Port/Proc)(1111/tcp 形式, app.exe:N 形式)
func checkCondition(wPort, wProc string) bool {
	if wPort != "" {
		re := regexp.MustCompile(`(\d+)/(tcp|udp)`)
		m := re.FindStringSubmatch(wPort)
		if len(m) == 3 {
			port, proto := m[1], m[2]
			cmdStr := fmt.Sprintf("netstat -an | findstr LISTENING | findstr :%s", port)
			if proto == "udp" {
				cmdStr = fmt.Sprintf("netstat -an | findstr UDP | findstr :%s", port)
			}
			c := exec.Command("cmd.exe", "/c", cmdStr)
			c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if err := c.Run(); err == nil {
				return true
			}
		}
	}
	if wProc != "" {
		parts := strings.Split(wProc, ":")
		if len(parts) == 2 {
			name, numStr := parts[0], parts[1]
			num, _ := strconv.Atoi(numStr)
			c := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/FO", "CSV", "/NH")
			c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, _ := c.Output()
			if bytes.Count(out, []byte(name)) >= num {
				return true
			}
		}
	}
	return false
}
