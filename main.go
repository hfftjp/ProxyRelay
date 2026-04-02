package main

import (
	_ "embed"
	"fmt"
	"github.com/energye/systray"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32       = syscall.NewLazyDLL("kernel32.dll")
	modshell32        = syscall.NewLazyDLL("shell32.dll")
	procCreateMutex   = modkernel32.NewProc("CreateMutexW")
	procCloseHandle   = modkernel32.NewProc("CloseHandle")
	procShellExecuteW = modshell32.NewProc("ShellExecuteW")
)

const (
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

//go:embed icons/run_e.ico
var iconRunE []byte

//go:embed icons/stop.ico
var iconStop []byte

//go:embed icons/stop_e.ico
var iconStopE []byte

//go:embed icons/busy.ico
var iconBusy []byte

//go:embed icons/busy_e.ico
var iconBusyE []byte

// サービス状態・リスンポートなど
var (
	IsHttpdRun       bool
	CurrentHttpdPort int
	IsProxyRun       bool
	CurrentProxyPort int
	SkipTrayUpdate   bool
)

var (
	mStatus        *systray.MenuItem
	mStartAll      *systray.MenuItem
	mStopAll       *systray.MenuItem
	lastIconStatus int
)

// 起動処理
func main() {
	checkSingleInstance()
	isProcessing = false
	SkipTrayUpdate = false
	ErrorNotice = false
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

// エラー通知アイコン
func ResetErrorNotice() {
	ErrorNotice = false
	updateIcon()
}

// トレーアイコン更新
func updateIcon() {
	if IsProxyRun {
		if ErrorNotice {
			if lastIconStatus != 21 {
				systray.SetIcon(iconRunE)
				lastIconStatus = 21
			}
		} else {
			if lastIconStatus != 20 {
				systray.SetIcon(iconRun)
				lastIconStatus = 20
			}
		}
	} else {
		if ErrorNotice {
			if lastIconStatus != 11 {
				systray.SetIcon(iconStopE)
				lastIconStatus = 11
			}
		} else {
			if lastIconStatus != 10 {
				systray.SetIcon(iconStop)
				lastIconStatus = 10
			}
		}
	}
}

func setBusyIcon() {
	if ErrorNotice {
		if lastIconStatus != 31 {
			systray.SetIcon(iconBusyE)
			lastIconStatus = 31
		}
	} else {
		if lastIconStatus != 30 {
			systray.SetIcon(iconBusy)
			lastIconStatus = 30
		}
	}
}

// メイン動作＋タスクトレーメニューイベント
func onReady() {
	systray.SetTooltip(APP_NAME)
	updateIcon()
	if !startHttpd() {
		showMessage("ERROR", "SYS", "Httpd startup error")
		systray.Quit()
		time.Sleep(100 * time.Millisecond)
		os.Exit(1)
		return
	}
	systray.SetOnDClick(func(menu systray.IMenu) {
		openWebView()
	})
	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})
	mStatus = systray.AddMenuItem("", "")
	mStatus.SetIcon(iconStop)
	mStartAll = systray.AddMenuItem("開始", "")
	mStartAll.Click(func() {
		go PerformStartAll(true)
	})
	mStopAll = systray.AddMenuItem("停止", "")
	mStopAll.Click(func() {
		go PerformStopAll(true)
	})
	systray.AddSeparator()
	if GetStringSafe(cfg.Section("Hook").Key("PostStartBat")) != "" {
		title := toUTF8(GetStringSafe(cfg.Section("Hook").Key("PostStartTitle")))
		if len(title) > 0 {
			mHook := systray.AddMenuItem(truncateString(title, 10), "")
			mHook.Click(func() {
				go func() {
					if !runHookWithConfig("PostStart") {
						showLogMBox("ERROR", truncateString(title, 10))
					}
				}()
			})
		}
	}
	if GetStringSafe(cfg.Section("Hook").Key("PostStart2Bat")) != "" {
		title := toUTF8(GetStringSafe(cfg.Section("Hook").Key("PostStart2Title")))
		if len(title) > 0 {
			mHook2 := systray.AddMenuItem(truncateString(title, 10), "")
			mHook2.Click(func() {
				go func() {
					if !runHookWithConfig("PostStart2") {
						showLogMBox("ERROR", truncateString(title, 10))
					}
				}()
			})
		}
	}
	mSubMaintenance := systray.AddMenuItem("メンテナンス", "")
	mHttpdRestart := mSubMaintenance.AddSubMenuItem("Httpd再起動", "")
	mHttpdRestart.Click(func() {
		if !stopHttpd() {
			showLogMBox("ERROR", "Httpd再起動")
			return
		}
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if startHttpd() {
				return
			}
		}
		showLogMBox("ERROR", "Httpd再起動")
	})
	mProxyRestart := mSubMaintenance.AddSubMenuItem("Proxy再起動", "")
	mProxyRestart.Click(func() {
		if !IsProxyRun || showConfirmDialog("中継動作中ですが、再起動してもよろしいですか？") {
			if !stopProxyRelay() {
				showLogMBox("ERROR", "Proxy再起動")
				return
			}
			for i := 0; i < 10; i++ {
				time.Sleep(200 * time.Millisecond)
				if startProxyRelay() {
					return
				}
			}
			showLogMBox("ERROR", "Proxy再起動")
		}
	})
	mResetRegistry := mSubMaintenance.AddSubMenuItem("システム設定戻し", "")
	mResetRegistry.Click(func() {
		if !IsProxyRun || showConfirmDialog("中継動作中ですが、戻してもよろしいですか？") {
			if !setRegistryOff() {
				showLogMBox("ERROR", "システム設定戻し")
			}
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

	mResetErrorNotice := mSubMaintenance.AddSubMenuItem("エラー通知アイコンクリア", "")
	mResetErrorNotice.Click(func() {
		ResetErrorNotice()
	})

	systray.AddSeparator()
	mOpenBrowser := systray.AddMenuItem("管理画面を開く", "")
	mOpenBrowser.Click(func() {
		openWebView()
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
		PerformStartAll(true)
	}
	go monitorTraffic()
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

var isProcessing bool

// Proxy中継開始処理
func PerformStartAll(showmsgbox ...bool) {
	if isProcessing {
		return
	}
	isProcessing = true
	defer func() { isProcessing = false }()
	showflg := false
	if len(showmsgbox) > 0 {
		showflg = showmsgbox[0]
	}
	setBusyIcon()
	defer updateIcon()
	stopHook()
	if !runHookWithConfig("PreStart") {
		if showflg {
			showLogMBox("ERROR", "PreStart")
		}
		return
	}
	if !startProxyRelay() {
		if showflg {
			showLogMBox("ERROR", "StartProxy")
		}
		return
	}
	if !downloadOriginalPac() {
		if showflg {
			showLogMBox("ERROR", "DownloadPAC")
		}
		return
	}
	if !modifySavedPac() {
		if showflg {
			showLogMBox("ERROR", "ModifyPAC")
		}
		return
	}
	if !setRegistryOn() {
		if showflg {
			showLogMBox("ERROR", "OSSetting")
		}
		return
	}
	if !runHookWithConfig("PostStart") {
		if showflg {
			showLogMBox("ERROR", "PostStart")
		}
		return
	}
	if !runHookWithConfig("PostStart2") {
		if showflg {
			showLogMBox("ERROR", "PostStart2")
		}
	}
}

// Proxy中継終了処理
func PerformStopAll(showmsgbox ...bool) {
	if isProcessing {
		return
	}
	isProcessing = true
	defer func() { isProcessing = false }()
	showflg := false
	if len(showmsgbox) > 0 {
		showflg = showmsgbox[0]
	}
	setBusyIcon()
	defer updateIcon()
	stopHook()
	if !runHookWithConfig("PreStop") {
		if showflg {
			showLogMBox("ERROR", "PreStop")
		}
		return
	}
	if !setRegistryOff() {
		if showflg {
			showLogMBox("ERROR", "OSSetting")
		}
		return
	}
	if !stopProxyRelay() {
		if showflg {
			showLogMBox("ERROR", "StopProxy")
		}
		return
	}
	if !runHookWithConfig("PostStop") {
		if showflg {
			showLogMBox("ERROR", "PostStop")
		}
	}
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
	if hMutex != 0 {
		procCloseHandle.Call(hMutex)
	}
	logOutput("INFO", "SYS", "Stopped.")
}

var hMutex uintptr

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
	hMutex = ret
	if err != nil && err.(syscall.Errno) == ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(hMutex)
		showMessage("ERROR", "SYS", "Another instance is already running.")
		os.Exit(1)
	}
}
