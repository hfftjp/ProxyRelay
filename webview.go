package main

import (
	"fmt"
	"github.com/jchv/go-webview2"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	procLoadImage       = moduser32.NewProc("LoadImageW")
	procDestroyIcon     = moduser32.NewProc("DestroyIcon")
	procSendMessage     = moduser32.NewProc("SendMessageW")
	procFindWindow      = moduser32.NewProc("FindWindowW")
	procShowWindow      = moduser32.NewProc("ShowWindow")
	procSetForeground   = moduser32.NewProc("SetForegroundWindow")
	procGetModuleHandle = modkernel32.NewProc("GetModuleHandleW")
)

const (
	wmSetIcon   = 0x0080
	iconSmall   = 0
	iconBig     = 1
	idrMainIcon = 1
	SW_RESTORE  = 9
)

var (
	hIconSmall uintptr
	hIconBig   uintptr
)

// 管理画面の表示
func openWebView() {
	windowTitle := APP_NAME // index.htmlの<title>と同じにする
	if focusExistingWindow(windowTitle) {
		return
	}
	if !IsHttpdRun {
		notify("ERROR", "SYS", "WebUI startup error")
		if NotifyEnable == 0 {
			showMessage("ERROR", "SYS", "WebUI startup error")
		}
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/view", getCurrentHttpdPort())
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			Debug:     false,
			AutoFocus: true,
			WindowOptions: webview2.WindowOptions{
				Title:  windowTitle,
				Width:  800,
				Height: 600,
				// IconId: 1,
				Center: true,
			},
		})
		if w == nil {
			notify("ERROR", "SYS", "WebUI startup error")
			if NotifyEnable == 0 {
				showMessage("ERROR", "SYS", "WebUI startup error")
			}
			return
		}
		setWindowIcon(w.Window())
		w.SetSize(800, 600, webview2.HintFixed)
		w.Navigate(url)
		w.Run()
		destroyIcons()
		w.Destroy()
	}()
}

// 管理画面のWindowアイコン
func setWindowIcon(hwnd unsafe.Pointer) {
	if hIconSmall == 0 || hIconBig == 0 {
		handle, _, _ := procGetModuleHandle.Call(0)
		hIconSmall, _, _ = procLoadImage.Call(handle, uintptr(idrMainIcon), 1, 16, 16, 0)
		hIconBig, _, _ = procLoadImage.Call(handle, uintptr(idrMainIcon), 1, 32, 32, 0)
	}
	if hIconSmall != 0 {
		procSendMessage.Call(uintptr(hwnd), wmSetIcon, uintptr(iconSmall), hIconSmall)
	}
	if hIconBig != 0 {
		procSendMessage.Call(uintptr(hwnd), wmSetIcon, uintptr(iconBig), hIconBig)
	}
}

// アイコン破棄
func destroyIcons() {
	if hIconSmall != 0 {
		procDestroyIcon.Call(hIconSmall)
		hIconSmall = 0
	}
	if hIconBig != 0 {
		procDestroyIcon.Call(hIconBig)
		hIconBig = 0
	}
}

// 管理画面の2重起動防止（＋開いていれば最小化解除＋最前面へ）
func focusExistingWindow(title string) bool {
	tPtr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(tPtr)))
	if hwnd != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
		procSetForeground.Call(hwnd)
		return true
	}
	return false
}
