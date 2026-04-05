package main

import (
	"fmt"
	"github.com/natefinch/lumberjack"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	globalLogger   *lumberjack.Logger
	moduser32      = syscall.NewLazyDLL("user32.dll")
	procMessageBox = moduser32.NewProc("MessageBoxW")
)

const (
	MB_OK                   = 0x00000000
	MB_YESNO                = 0x00000004
	MB_ICONERROR            = 0x00000010
	MB_ICONQUESTION         = 0x00000020
	MB_ICONWARNING          = 0x00000030
	MB_ICONINFORMATION      = 0x00000040
	MB_DEFBUTTON2           = 0x00000100
	MB_TOPMOST              = 0x00040000
	MB_SETFOREGROUND        = 0x00010000
	MB_SERVICE_NOTIFICATION = 0x00200000
	IDYES                   = 6
)

// 通知保持用
type AppNotification struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
}

var (
	NotifyEnable  int
	notifyStack   []AppNotification
	stackMu       sync.RWMutex
	stackSize     = 1600
	notifyCounter int64
	retainPeriod  = 5 * time.Second
	ErrorNotice   bool
	LogLevel      int
	LogPath       string
	LogDir        string
)

// 通知記録
func PushNotification(nType, message string) {
	newID := atomic.AddInt64(&notifyCounter, 1)
	if newID >= math.MaxInt64-5000 {
		atomic.StoreInt64(&notifyCounter, 0)
	}
	n := AppNotification{
		ID:        newID,
		Timestamp: time.Now(),
		Type:      nType,
		Message:   message,
	}
	go func() {
		stackMu.Lock()
		now := time.Now()
		dropCount := 0
		for _, old := range notifyStack {
			if now.Sub(old.Timestamp) > retainPeriod {
				dropCount++
			} else {
				break
			}
		}
		if dropCount == len(notifyStack) {
			notifyStack = []AppNotification{}
		} else if dropCount > 0 {
			notifyStack = notifyStack[dropCount:]
		}
		if len(notifyStack) >= stackSize {
			notifyStack = notifyStack[1:]
		}
		notifyStack = append(notifyStack, n)
		stackMu.Unlock()
		triggerNotify()
	}()
}

// 通知読取
func PullNotifications(lastID int64) []AppNotification {
	stackMu.RLock()
	defer stackMu.RUnlock()
	var results []AppNotification
	now := time.Now()
	for _, n := range notifyStack {
		isFresh := now.Sub(n.Timestamp) <= retainPeriod
		isNew := n.ID > lastID || (lastID > (math.MaxInt64-3000) && n.ID < 3000)
		if isFresh && isNew {
			results = append(results, n)
		}
	}
	ResetErrorNotice()
	return results
}

// ロガー初期化
func setupLogger() bool {
	LogDir = GetStringSafe(cfg.Section("Log").Key("LogDir"), "./log/")
	lFile := GetStringSafe(cfg.Section("Log").Key("LogFileName"), "proxyrelay.log")
	LogPath = filepath.Join(LogDir, lFile)
	if err := os.MkdirAll(LogDir, os.ModePerm); err != nil {
		return false
	}
	globalLogger = &lumberjack.Logger{
		Filename:   LogPath,
		MaxSize:    GetIntSafe(cfg.Section("Log").Key("LogMaxSize"), 1),
		MaxBackups: GetIntSafe(cfg.Section("Log").Key("LogMaxBackups"), 6),
		LocalTime:  true,
	}
	log.SetOutput(globalLogger)
	log.SetFlags(log.Ltime)
	NotifyEnable = GetIntSafe(cfg.Section("General").Key("NotifyEnable"), 1)
	LogLevel = GetIntSafe(cfg.Section("Proxy").Key("LogLevel"), 0)
	return true
}

// ログローテ
func logRotate() bool {
	if globalLogger == nil {
		logOutput("ERROR", "SYS", "Logger is not initialized")
		return false
	}
	err := globalLogger.Rotate()
	if err != nil {
		logOutput("ERROR", "SYS", "Log rotate error: %v", err)
		return false
	}
	return true
}

// ログ出力
func logOutput(level, from, message string, v ...interface{}) {
	log.Println(fmt.Sprintf("[%s] %s: ", level, from) + fmt.Sprintf(message, v...))
}

// ロギング・通知
func notify(level, from, message string, v ...interface{}) {
	logOutput(level, from, message, v...)
	if level == "ERROR" {
		ErrorNotice = true
	}
	updateIcon()
	var (
		lvstr string
	)
	switch level {
	case "ERROR":
		lvstr = "error"
	case "INFO":
		lvstr = "success"
	default:
		lvstr = "success"
	}
	PushNotification(lvstr, fmt.Sprintf("%s: %s", from, fmt.Sprintf(message, v...)))
}

// メッセージボックス表示
func showMessage(level, from, message string, v ...interface{}) {
	var iconType uintptr
	switch level {
	case "ERROR":
		iconType = MB_ICONERROR
	case "WARNING":
		iconType = MB_ICONWARNING
	default:
		iconType = MB_ICONINFORMATION
	}
	tPtr, _ := syscall.UTF16PtrFromString(APP_NAME)
	mPtr, _ := syscall.UTF16PtrFromString(from + ": " + fmt.Sprintf(message, v...))
	procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(mPtr)),
		uintptr(unsafe.Pointer(tPtr)),
		uintptr(MB_OK|iconType|MB_TOPMOST|MB_SETFOREGROUND),
	)
}

func showLogMBox(level, from string) {
	if NotifyEnable == 0 {
		go showMessage(level, from, "エラーが発生しました。ログを参照してください")
	}
}

func showConfirmDialog(message string) bool {
	tPtr, _ := syscall.UTF16PtrFromString(APP_NAME)
	mPtr, _ := syscall.UTF16PtrFromString(message)
	ret, _, _ := procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(mPtr)),
		uintptr(unsafe.Pointer(tPtr)),
		uintptr(MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2|MB_TOPMOST|MB_SETFOREGROUND),
	)
	return ret == IDYES
}
