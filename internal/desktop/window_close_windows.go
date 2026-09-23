package desktop

import "syscall"

const wmClose = 0x0010

var (
	closeHandler   func() (swallow bool)
	origWindowProc uintptr
)

// InterceptClose subclasses hwnd so that onClose decides, for every WM_CLOSE it gets, whether to
// let it proceed as usual (destroying the window) or swallow it. It exists so the desktop app can
// hide the window instead of tearing the whole WebView2 instance down when "close to tray" is on:
// restoring it later is then an instant show of what is already there, not a full recreate (which
// is also what used to flash blank-white-then-themed at the wrong place before settling). Same
// subclassing trick as WatchNotificationClicks, on the app's own window instead of the tray's.
func InterceptClose(hwnd uintptr, onClose func() (swallow bool)) {
	closeHandler = onClose
	orig, _, _ := pGetWindowLongPtrW.Call(hwnd, uintptr(gwlpWndProc))
	origWindowProc = orig
	pSetWindowLongPtrW.Call(hwnd, uintptr(gwlpWndProc), syscall.NewCallback(closeWndProcSubclass))
}

func closeWndProcSubclass(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == wmClose && closeHandler != nil && closeHandler() {
		return 0
	}
	r, _, _ := pCallWindowProcW.Call(origWindowProc, hwnd, uintptr(msg), wParam, lParam)
	return r
}
