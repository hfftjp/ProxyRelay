package main

import (
	"crypto/rand"
	"fmt"
	"golang.org/x/sys/windows"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"unsafe"
)

var (
	procEnumWindows         = moduser32.NewProc("EnumWindows")
	procGetWindowTextW      = moduser32.NewProc("GetWindowTextW")
	procSetForegroundWindow = moduser32.NewProc("SetForegroundWindow")
	procShowWindow          = moduser32.NewProc("ShowWindow")
	procIsIconic            = moduser32.NewProc("IsIconic")
)

const (
	SW_RESTORE = 9
)

var (
	currentSessionID   string
	oneTimeLaunchKey   string
	sessionMu          sync.Mutex
	isLaunchPathActive bool
	currentLaunchPath  string
)

// 管理画面の表示
func openWebView() {
	windowTitle := APP_NAME + " Management"
	if focusExistingBrowser(windowTitle) {
		return
	}
	_, launchPath := RegisterLaunchKey()
	url := fmt.Sprintf("http://%s:%d%s", getToaddr(CurrentHttpdAddr), CurrentHttpdPort, launchPath)
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}
func RegisterLaunchKey() (string, string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	oneTimeLaunchKey = generateUUID()
	currentLaunchPath = "/" + generateUUID()
	isLaunchPathActive = true
	return oneTimeLaunchKey, currentLaunchPath
}
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x%x%x%x", b[0:4], b[4:8], b[8:12], b[12:16])
}

// 管理画面起動用中継
func HandleLauncher(w http.ResponseWriter, r *http.Request) {
	sessionMu.Lock()
	active := isLaunchPathActive
	key := oneTimeLaunchKey
	isLaunchPathActive = false
	sessionMu.Unlock()
	if !active {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	targetURL := "/index.html"
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
	<html>
	<head>
		<script>
			function launch() {
				window.history.replaceState({}, "", "/");
				document.forms[0].submit();
			}
		</script>
	</head>
	<body onload="launch()">
		<form method="POST" action="%s">
			<input type="hidden" name="launch_key" value="%s">
		</form>
	</body>
	</html>`, targetURL, key)
}

// 管理画面の2重起動防止
type findWindowContext struct {
	title  string
	handle windows.Handle
}

func focusExistingBrowser(title string) bool {
	ctx := findWindowContext{title: title}
	procEnumWindows.Call(windows.NewCallback(enumWindowsCallback), uintptr(unsafe.Pointer(&ctx)))
	if ctx.handle != 0 {
		ret, _, _ := procIsIconic.Call(uintptr(ctx.handle))
		if ret != 0 {
			procShowWindow.Call(uintptr(ctx.handle), SW_RESTORE)
		}
		procSetForegroundWindow.Call(uintptr(ctx.handle))
		return true
	}
	return false
}
func enumWindowsCallback(hwnd windows.Handle, lParam uintptr) uintptr {
	ctx := (*findWindowContext)(unsafe.Pointer(lParam))
	b := make([]uint16, 512)
	ret, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if ret == 0 {
		return 1
	}
	title := windows.UTF16ToString(b)
	if strings.Contains(title, ctx.title) {
		ctx.handle = hwnd
		return 0
	}
	return 1
}
func ExchangeSession(key string) (string, bool) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if oneTimeLaunchKey == "" || key != oneTimeLaunchKey {
		return "", false
	}
	oneTimeLaunchKey = ""
	currentSessionID = generateUUID()
	return currentSessionID, true
}
func IsValidSession(token string) bool {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return token != "" && token == currentSessionID
}
