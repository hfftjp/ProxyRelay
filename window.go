package main

import (
	"embed"
	"golang.org/x/sys/windows"
	"runtime"
	"time"
	"unsafe"
)

// ==========================================
const (
	// レイアウト
	NOTIFY_WIDTH    = 300 // ウィンドウ幅
	NOTIFY_MARGIN_X = 15  // 画面右からのマージン
	NOTIFY_MARGIN_Y = 15  // 画面下からのマージン
	NOTIFY_PADDING  = 8   // テキストの内側余白
	NOTIFY_SPACING  = 8   // 通知同士の間隔
	// フォント
	FONT_FAMILY = "Meiryo" // フォント種類
	FONT_SIZE   = 18       // フォントサイズ
	FONT_WEIGHT = 700      // 太さ (700=Bold)
	// 外観・背景
	WINDOW_ALPHA         = 220 // ウィンドウ全体の不透明度 (0-255)
	WINDOW_CORNER_RADIUS = 4   // 角の丸め具合 (px)
	// 背景色 0x00BBGGRR 形式
	BG_COLOR_DEFAULT = 0x00F0F0F0
)

// 背景画像の埋め込み設定
//
//go:embed icons/background.bmp
var assets embed.FS

const BG_IMAGE_NAME = "icons/background.bmp"

// 通知タイプの色の定義 (BGR形式)
const (
	NotifyNormal  = 0x00A2A2A2 // 灰
	NotifySuccess = 0x00B98029 // 青
	NotifyError   = 0x003C4CE7 // 赤
)

// ==========================================

// Win32 API 定数
const (
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_LAYERED    = 0x00080000
	WS_POPUP         = 0x80000000
	WS_VISIBLE       = 0x10000000
	WM_CLOSE         = 0x0010
	WM_DESTROY       = 0x0002
	LWA_ALPHA        = 0x00000002
	WM_PAINT         = 0x000F
	DT_LEFT          = 0x00000000
	DT_TOP           = 0x00000000
	DT_WORDBREAK     = 0x00000010
	DT_CALCRECT      = 0x00000400
	IDC_ARROW        = 32512
	SPI_GETWORKAREA  = 0x0030
	DIB_RGB_COLORS   = 0
	SRCCOPY          = 0x00CC0020
	HALFTONE         = 4
	GWL_USERDATA     = 0xFFFFFFEB
	QS_ALLINPUT      = 0x04FF
	WAIT_OBJECT_0    = 0
)

type rect struct {
	L, T, R, B int32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msgStruct struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var (
	modgdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClass             = moduser32.NewProc("RegisterClassExW")
	procCreateWindow              = moduser32.NewProc("CreateWindowExW")
	procSetLayered                = moduser32.NewProc("SetLayeredWindowAttributes")
	procPeekMessage               = moduser32.NewProc("PeekMessageW")
	procDispatch                  = moduser32.NewProc("DispatchMessageW")
	procTranslate                 = moduser32.NewProc("TranslateMessage")
	procPostMessage               = moduser32.NewProc("PostMessageW")
	procDefWindowProc             = moduser32.NewProc("DefWindowProcW")
	procGetDC                     = moduser32.NewProc("GetDC")
	procReleaseDC                 = moduser32.NewProc("ReleaseDC")
	procBeginPaint                = moduser32.NewProc("BeginPaint")
	procEndPaint                  = moduser32.NewProc("EndPaint")
	procDrawText                  = moduser32.NewProc("DrawTextW")
	procLoadCursor                = moduser32.NewProc("LoadCursorW")
	procMoveWindow                = moduser32.NewProc("MoveWindow")
	procGetWindowRect             = moduser32.NewProc("GetWindowRect")
	procGetClientRect             = moduser32.NewProc("GetClientRect")
	procSetWindowRgn              = moduser32.NewProc("SetWindowRgn")
	procSystemParametersInfoW     = moduser32.NewProc("SystemParametersInfoW")
	procDestroyWindow             = moduser32.NewProc("DestroyWindow")
	procSetTextColor              = modgdi32.NewProc("SetTextColor")
	procSetBkMode                 = modgdi32.NewProc("SetBkMode")
	procCreateFont                = modgdi32.NewProc("CreateFontW")
	procSelectObject              = modgdi32.NewProc("SelectObject")
	procDeleteObject              = modgdi32.NewProc("DeleteObject")
	procCreateRoundRectRgn        = modgdi32.NewProc("CreateRoundRectRgn")
	procCreateSolidBrush          = modgdi32.NewProc("CreateSolidBrush")
	procStretchDIBits             = modgdi32.NewProc("StretchDIBits")
	procSetStretchBltMode         = modgdi32.NewProc("SetStretchBltMode")
	procGetModuleHandleW          = modkernel32.NewProc("GetModuleHandleW")
	procSetWindowLongPtr          = moduser32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtr          = moduser32.NewProc("GetWindowLongPtrW")
	procMsgWaitForMultipleObjects = moduser32.NewProc("MsgWaitForMultipleObjects")
	procCreateEventW              = modkernel32.NewProc("CreateEventW")
	procSetEvent                  = modkernel32.NewProc("SetEvent")
	procResetEvent                = modkernel32.NewProc("ResetEvent")
)

type NotifyConfig struct {
	Title    string
	Duration time.Duration
	Width    int32
	Color    uint32
}

type winContext struct {
	title string
	color uint32
	font  uintptr
	bmp   []byte
}

var (
	activeWindows     []uintptr
	lastDisplayID     int64
	className         = windows.StringToUTF16Ptr("Notification")
	notifyEventHandle uintptr
)

// 初期化時呼び出し
func init() {
	go windowMain()
}

// 通知発生時呼び出し用
func triggerNotify() {
	if notifyEventHandle != 0 {
		procSetEvent.Call(notifyEventHandle)
	}
}

// 通知ウィンドウ初期生成、メッセージ待ちループ
func windowMain() {
	runtime.LockOSThread()
	inst, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, uintptr(IDC_ARROW))
	bgBrush, _, _ := procCreateSolidBrush.Call(uintptr(BG_COLOR_DEFAULT))
	bmpData, _ := assets.ReadFile(BG_IMAGE_NAME)
	fontName := windows.StringToUTF16Ptr(FONT_FAMILY)
	hFont, _, _ := procCreateFont.Call(uintptr(FONT_SIZE), 0, 0, 0, uintptr(FONT_WEIGHT), 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fontName)))
	notifyEventHandle, _, _ = procCreateEventW.Call(0, 1, 0, 0)
	wc := wndClassEx{
		WndProc: windows.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
			switch msg {
			case WM_PAINT:
				var ps struct {
					Hdc         uintptr
					FErase      int32
					RcPaint     rect
					FRestore    int32
					FIncUpdate  int32
					RgbReserved [32]byte
				}
				hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
				if hdc != 0 {
					ptr, _, _ := procGetWindowLongPtr.Call(hwnd, GWL_USERDATA)
					if ptr != 0 {
						ctx := (*winContext)(unsafe.Pointer(ptr))
						var r rect
						procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
						if len(ctx.bmp) > 54 {
							offBits := *(*uint32)(unsafe.Pointer(&ctx.bmp[10]))
							imgW := *(*int32)(unsafe.Pointer(&ctx.bmp[18]))
							imgH := *(*int32)(unsafe.Pointer(&ctx.bmp[22]))
							winW, winH := r.R, r.B
							var dstX, dstY, dstW, dstH int32
							dstW, dstH = winW, winH
							if winW > 0 && winH > 0 && imgW > 0 && imgH > 0 {
								winRatio, imgRatio := float64(winW)/float64(winH), float64(imgW)/float64(imgH)
								if imgRatio > winRatio {
									dstW, dstH = winW, int32(float64(winW)/imgRatio)
									dstY = (winH - dstH) / 2
								} else {
									dstH, dstW = winH, int32(float64(winH)*imgRatio)
									dstX = (winW - dstW) / 2
								}
							}
							procSetStretchBltMode.Call(hdc, uintptr(HALFTONE))
							procStretchDIBits.Call(
								hdc,
								uintptr(dstX), uintptr(dstY),
								uintptr(dstW), uintptr(dstH),
								0, 0,
								uintptr(imgW), uintptr(imgH),
								uintptr(unsafe.Pointer(&ctx.bmp[offBits])),
								uintptr(unsafe.Pointer(&ctx.bmp[14])),
								DIB_RGB_COLORS,
								SRCCOPY,
							)
						}
						r.L += NOTIFY_PADDING
						r.T += NOTIFY_PADDING
						r.R -= NOTIFY_PADDING
						r.B -= NOTIFY_PADDING
						oldFont, _, _ := procSelectObject.Call(hdc, ctx.font)
						procSetTextColor.Call(hdc, uintptr(ctx.color))
						procSetBkMode.Call(hdc, 1)
						tPtr := windows.StringToUTF16Ptr(ctx.title)
						procDrawText.Call(hdc, uintptr(unsafe.Pointer(tPtr)), ^uintptr(0), uintptr(unsafe.Pointer(&r)), DT_LEFT|DT_TOP|DT_WORDBREAK)
						procSelectObject.Call(hdc, oldFont)
					}
					procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
				}
				return 0
			case WM_CLOSE:
				for i, h := range activeWindows {
					if h == hwnd {
						activeWindows = append(activeWindows[:i], activeWindows[i+1:]...)
						break
					}
				}
				var workArea rect
				procSystemParametersInfoW.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&workArea)), 0)
				sw := workArea.R
				sh := workArea.B
				var currentYOffset int32 = 0
				for _, hwnd := range activeWindows {
					var r rect
					if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret != 0 {
						h := r.B - r.T
						w := r.R - r.L
						newX := sw - w - NOTIFY_MARGIN_X
						newY := sh - (currentYOffset + h) - NOTIFY_MARGIN_Y
						procMoveWindow.Call(hwnd, uintptr(newX), uintptr(newY), uintptr(w), uintptr(h), 1)
						currentYOffset += h + NOTIFY_SPACING
					}
				}
				procDestroyWindow.Call(hwnd)
				return 0
			case WM_DESTROY:
				return 0
			}
			ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
			return ret
		}),
		Instance:   inst,
		ClassName:  className,
		Background: bgBrush,
		Cursor:     cursor,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	procRegisterClass.Call(uintptr(unsafe.Pointer(&wc)))
	for {
		ret, _, _ := procMsgWaitForMultipleObjects.Call(
			1,
			uintptr(unsafe.Pointer(&notifyEventHandle)),
			0,
			0xFFFFFFFF,
			QS_ALLINPUT,
		)
		switch ret {
		case WAIT_OBJECT_0:
			procResetEvent.Call(notifyEventHandle)
			newItems := PullNotifications(lastDisplayID)
			for _, n := range newItems {
				var lvcolor uint32
				switch n.Type {
				case "error":
					lvcolor = NotifyError
				case "success":
					lvcolor = NotifySuccess
				default:
					lvcolor = NotifyNormal
				}
				config := NotifyConfig{
					Title:    n.Message,
					Duration: 3 * time.Second,
					Color:    lvcolor,
					Width:    NOTIFY_WIDTH,
				}
				var yOffset int32 = 0
				for _, hwnd := range activeWindows {
					var r rect
					if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret != 0 {
						yOffset += (r.B - r.T) + NOTIFY_SPACING
					}
				}
				ctx := &winContext{
					title: config.Title,
					color: config.Color,
					font:  hFont,
					bmp:   bmpData,
				}
				tmpHdc, _, _ := procGetDC.Call(0)
				oldFont, _, _ := procSelectObject.Call(tmpHdc, hFont)
				calcRect := rect{0, 0, config.Width - (NOTIFY_PADDING * 2), 0}
				textPtr := windows.StringToUTF16Ptr(config.Title)
				procDrawText.Call(tmpHdc, uintptr(unsafe.Pointer(textPtr)), ^uintptr(0), uintptr(unsafe.Pointer(&calcRect)), DT_CALCRECT|DT_WORDBREAK)
				procSelectObject.Call(tmpHdc, oldFont)
				procReleaseDC.Call(0, tmpHdc)
				finalHeight := calcRect.B + (NOTIFY_PADDING * 2)
				var workArea rect
				procSystemParametersInfoW.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&workArea)), 0)
				posX := workArea.R - config.Width - NOTIFY_MARGIN_X
				posY := workArea.B - finalHeight - yOffset - NOTIFY_MARGIN_Y
				hwnd, _, _ := procCreateWindow.Call(
					WS_EX_TOOLWINDOW|WS_EX_TOPMOST|WS_EX_LAYERED,
					uintptr(unsafe.Pointer(className)),
					uintptr(unsafe.Pointer(textPtr)),
					WS_POPUP|WS_VISIBLE,
					uintptr(posX), uintptr(posY),
					uintptr(config.Width), uintptr(finalHeight),
					0, 0, inst, 0,
				)
				if hwnd == 0 {
					lastDisplayID = n.ID
					continue
				}
				procSetWindowLongPtr.Call(hwnd, GWL_USERDATA, uintptr(unsafe.Pointer(ctx)))
				hRgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(config.Width), uintptr(finalHeight), uintptr(WINDOW_CORNER_RADIUS), uintptr(WINDOW_CORNER_RADIUS))
				procSetWindowRgn.Call(hwnd, hRgn, 1)
				activeWindows = append(activeWindows, hwnd)
				procSetLayered.Call(hwnd, 0, uintptr(WINDOW_ALPHA), LWA_ALPHA)
				time.AfterFunc(config.Duration, func() {
					procPostMessage.Call(hwnd, WM_CLOSE, 0, 0)
				})
				lastDisplayID = n.ID
			}
			var m msgStruct
			for {
				ret, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if ret == 0 {
					break
				}
				procTranslate.Call(uintptr(unsafe.Pointer(&m)))
				procDispatch.Call(uintptr(unsafe.Pointer(&m)))
			}
		case WAIT_OBJECT_0 + 1:
			var m msgStruct
			for {
				ret, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if ret == 0 {
					break
				}
				procTranslate.Call(uintptr(unsafe.Pointer(&m)))
				procDispatch.Call(uintptr(unsafe.Pointer(&m)))
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}
