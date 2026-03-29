package main

import (
	_ "embed"
	"fmt"
	"github.com/energye/systray"
	"github.com/natefinch/lumberjack"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	globalLogger      *lumberjack.Logger
	moduser32         = syscall.NewLazyDLL("user32.dll")
	modkernel32       = syscall.NewLazyDLL("kernel32.dll")
	modshell32        = syscall.NewLazyDLL("shell32.dll")
	procMessageBox    = moduser32.NewProc("MessageBoxW")
	procCreateMutex   = modkernel32.NewProc("CreateMutexW")
	procShellExecuteW = modshell32.NewProc("ShellExecuteW")
)

const (
	MB_OK                = 0x00000000
	MB_YESNO             = 0x00000004
	MB_ICONERROR         = 0x00000010
	MB_ICONQUESTION      = 0x00000020
	MB_ICONWARNING       = 0x00000030
	MB_ICONINFORMATION   = 0x00000040
	MB_DEFBUTTON2        = 0x00000100
	MB_TOPMOST           = 0x00040000
	IDYES                = 6
	ERROR_ALREADY_EXISTS = 183
)

const (
	CONF_DIR   = "./conf/"           // 設定ディレクトリ
	CONF_PATH  = "./conf/config.ini" // 設定ファイルパス
	DENY_PATH  = "./conf/deny.list"  // アクセス拒否リスト(任意、優先)
	ALLOW_PATH = "./conf/allow.list" // アクセス許可リスト(任意)
	APP_NAME   = "ProxyRelay"        // アプリ名
)

//go:embed icons/run.ico
var iconRun []byte

//go:embed icons/stop.ico
var iconStop []byte

//go:embed icons/busy.ico
var iconBusy []byte

// サービス状態・リスンポートなど
var (
	IsHttpdRun       bool
	CurrentHttpdPort int
	IsProxyRun       bool
	CurrentProxyPort int
	NotifyEnable     int
	LogLevel         int
	LogPath          string
	LogDir           string
	SkipTrayUpdate   bool
)

var (
	mStatus   *systray.MenuItem
	mStartAll *systray.MenuItem
	mStopAll  *systray.MenuItem
)

// 通知保持用
type AppNotification struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
}

var (
	notifyStack   []AppNotification
	stackMu       sync.RWMutex
	stackSize     = 1600
	notifyCounter int64
	retainPeriod  = 5 * time.Second
)

// 通知記録
func PushNotification(nType, message string) {
	newID := atomic.AddInt64(&notifyCounter, 1)
	if newID >= math.MaxInt64-2000 {
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
		defer stackMu.Unlock()
		now := time.Now()
		cutoff := -1
		for i, old := range notifyStack {
			if now.Sub(old.Timestamp) <= retainPeriod {
				cutoff = i
				break
			}
		}
		if cutoff > 0 {
			notifyStack = notifyStack[cutoff:]
		} else if cutoff == -1 && len(notifyStack) > 0 {
			notifyStack = []AppNotification{}
		}
		if len(notifyStack) >= stackSize {
			notifyStack = notifyStack[1:]
		}
		notifyStack = append(notifyStack, n)
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
	return results
}

// 起動処理
func main() {
	checkSingleInstance()
	SkipTrayUpdate = false
	if !LoadCfg() {
		os.Exit(1)
	}
	if !setupLogger() {
		showMessage("ERROR", "SYS", "Logger startup error")
		os.Exit(1)
	}
	logRotate()
	logOutput("LOG", "SYS", "Logger initialized")
	systray.Run(onReady, onExit)
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
	var lvstr string
	if NotifyEnable == 1 {
		if level == "ERROR" {
			lvstr = "error"
		} else {
			lvstr = "success"
		}
		PushNotification(lvstr, fmt.Sprintf("%s: %s", from, fmt.Sprintf(message, v...)))
	}
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
		uintptr(MB_OK|iconType|MB_TOPMOST),
	)
}

func showConfirmDialog(message string) bool {
	tPtr, _ := syscall.UTF16PtrFromString(APP_NAME)
	mPtr, _ := syscall.UTF16PtrFromString(message)
	ret, _, _ := procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(mPtr)),
		uintptr(unsafe.Pointer(tPtr)),
		uintptr(MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2|MB_TOPMOST),
	)
	return ret == IDYES
}

// トレーアイコン更新
func updateIcon() {
	systray.SetIcon(iconStop)
	if IsProxyRun {
		systray.SetIcon(iconRun)
	}
}

func setBusyIcon() {
	systray.SetIcon(iconBusy)
}

// SJIS文字列をUTF-8に変換する
func toUTF8(s string) string {
	return autoDecode([]byte(s))
}

// 指定した文字数でカットする
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// メイン動作＋タスクトレーメニューイベント
func onReady() {
	systray.SetTooltip(APP_NAME)
	updateIcon()
	if !startHttpd() {
		showMessage("ERROR", "SYS", "Httpd startup error")
		systray.Quit()
		return
	}
	systray.SetOnDClick(func(menu systray.IMenu) {
		OpenBrowser()
	})
	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})
	mStatus = systray.AddMenuItem("", "")
	mStatus.SetIcon(iconStop)
	mStartAll = systray.AddMenuItem("開始", "")
	mStartAll.Click(func() {
		go PerformStartAll()
	})
	mStopAll = systray.AddMenuItem("停止", "")
	mStopAll.Click(func() {
		go PerformStopAll()
	})
	systray.AddSeparator()
	if GetStringSafe(cfg.Section("Hook").Key("PostStartBat")) != "" {
		title := toUTF8(GetStringSafe(cfg.Section("Hook").Key("PostStartTitle")))
		if len(title) > 0 {
			mHook := systray.AddMenuItem(truncateString(title, 10), "")
			mHook.Click(func() {
				go runHookWithConfig("PostStart")
			})
		}
	}
	if GetStringSafe(cfg.Section("Hook").Key("PostStart2Bat")) != "" {
		title := toUTF8(GetStringSafe(cfg.Section("Hook").Key("PostStart2Title")))
		if len(title) > 0 {
			mHook2 := systray.AddMenuItem(truncateString(title, 10), "")
			mHook2.Click(func() {
				go runHookWithConfig("PostStart2")
			})
		}
	}
	mSubMaintenance := systray.AddMenuItem("メンテナンス", "")
	mHttpdRestart := mSubMaintenance.AddSubMenuItem("Httpd再起動", "")
	mHttpdRestart.Click(func() {
		stopHttpd()
		startHttpd()
	})
	mProxyRestart := mSubMaintenance.AddSubMenuItem("Proxy再起動", "")
	mProxyRestart.Click(func() {
		if !IsProxyRun || showConfirmDialog("中継動作中ですが、再起動してもよろしいですか？") {
			stopProxyRelay()
			startProxyRelay()
		}
	})
	mResetRegistry := mSubMaintenance.AddSubMenuItem("システム設定戻し", "")
	mResetRegistry.Click(func() {
		if !IsProxyRun || showConfirmDialog("中継動作中ですが、戻してもよろしいですか？") {
			setRegistryOff()
		}
	})
	mStopHook := mSubMaintenance.AddSubMenuItem("待機中BATの停止", "")
	mStopHook.Click(func() {
		if showConfirmDialog("動作中/待機中のHOOKを停止してもよろしいですか？") {
			go stopHook()
		}
	})
	mOpenConfDir := mSubMaintenance.AddSubMenuItem("設定フォルダを開く", "")
	mOpenConfDir.Click(func() {
		go func() {
			fullpath, err := filepath.Abs(CONF_DIR)
			if err != nil {
				return
			}
			if _, err := os.Stat(fullpath); os.IsNotExist(err) {
				return
			}
			_ = exec.Command("explorer", fullpath).Start()
		}()
	})
	systray.AddSeparator()
	mOpenBrowser := systray.AddMenuItem("管理画面を開く", "")
	mOpenBrowser.Click(func() {
		OpenBrowser()
	})
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("終了", "")
	mQuit.Click(func() {
		if !IsProxyRun || showConfirmDialog("中継動作中ですが、終了してもよろしいですか？") {
			systray.Quit()
		}
	})
	refreshMenu()
	if GetIntSafe(cfg.Section("General").Key("AutoStart"), 0) == 1 {
		PerformStartAll()
	}
	go monitorTraffic()
}

// 管理画面の表示
func OpenBrowser() {
	uPtr, _ := syscall.UTF16PtrFromString(fmt.Sprintf("http://127.0.0.1:%d/view", getCurrentHttpdPort()))
	vPtr, _ := syscall.UTF16PtrFromString("open")
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(vPtr)),
		uintptr(unsafe.Pointer(uPtr)),
		0,
		0,
		1,
	)
	if ret <= 32 {
		showMessage("ERROR", "SYS", "WebUI startup error: %d", ret)
	}
}

// トレーの状態更新
func refreshMenu() {
	var httpdStatus, proxyStatus string
	if IsProxyRun {
		mStartAll.Disable()
		mStopAll.Enable()
	} else {
		mStartAll.Enable()
		mStopAll.Disable()
	}
	if !IsHttpdRun || CurrentHttpdPort == 0 {
		httpdStatus = "-----"
	} else {
		httpdStatus = strconv.Itoa(CurrentHttpdPort)
	}
	if !IsProxyRun || CurrentProxyPort == 0 {
		proxyStatus = "-----"
		mStatus.SetIcon(iconStop)
	} else {
		proxyStatus = strconv.Itoa(CurrentProxyPort)
		mStatus.SetIcon(iconBusy)
	}
	mStatus.SetTitle(fmt.Sprintf("Proxy:%5s, Httpd:%5s", proxyStatus, httpdStatus))
	if !SkipTrayUpdate {
		systray.SetTooltip(fmt.Sprintf("%s\n%s", APP_NAME, fmt.Sprintf("Proxy:%5s, Httpd:%5s", proxyStatus, httpdStatus)))
	}
}

// Proxy中継開始処理
func PerformStartAll() {
	setBusyIcon()
	defer updateIcon()
	stopHook()
	if !runHookWithConfig("PreStart") {
		return
	}
	if !startProxyRelay() {
		return
	}
	if !downloadOriginalPac() {
		return
	}
	if !modifySavedPac() {
		return
	}
	if !setRegistryOn() {
		return
	}
	if !runHookWithConfig("PostStart") {
		return
	}
	runHookWithConfig("PostStart2")
}

// Proxy中継終了処理
func PerformStopAll() {
	setBusyIcon()
	defer updateIcon()
	stopHook()
	if !runHookWithConfig("PreStop") {
		return
	}
	if !setRegistryOff() {
		return
	}
	if !stopProxyRelay() {
		return
	}
	runHookWithConfig("PostStop")
	updateIcon()
}

// 停止処理(前後処理省略)
func onExit() {
	SkipTrayUpdate = true
	logOutput("INFO", "SYS", "Stopping...")
	if IsProxyRun {
		setRegistryOff()
		stopProxyRelay()
	}
	if IsHttpdRun {
		stopHttpd()
	}
	logOutput("INFO", "SYS", "Stopped.")
}

// 本体の2重起動防止
func checkSingleInstance() {
	mutexName, _ := syscall.UTF16PtrFromString("Local\\ProxyRelayUniqueMutexName")
	ret, _, err := procCreateMutex.Call(
		0,
		0,
		uintptr(unsafe.Pointer(mutexName)),
	)
	if ret == 0 {
		showMessage("ERROR", "SYS", "Fatal error creating mutex.")
		os.Exit(1)
	}
	if err != nil && err.(syscall.Errno) == ERROR_ALREADY_EXISTS {
		showMessage("ERROR", "SYS", "Another instance is already running.")
		os.Exit(1)
	}
}
