package desktop

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows notifications. The tray library owns the notification-area icon and has no call for
// balloons, so this asks the shell to show one on that same icon: Shell_NotifyIcon with NIF_INFO,
// addressed by the icon's window and id (both are fixed by the library: class "SystrayClass",
// id 100). Windows 10/11 show it as a toast.

var (
	shellDLL         = windows.NewLazySystemDLL("shell32.dll")
	userDLL          = windows.NewLazySystemDLL("user32.dll")
	pNotifyIcon      = shellDLL.NewProc("Shell_NotifyIconW")
	pFindWindowExW   = userDLL.NewProc("FindWindowExW")
	pGetWindowThread = userDLL.NewProc("GetWindowThreadProcessId")
)

const (
	trayClass  = "SystrayClass"
	trayIconID = 100
	nimModify  = 1
	nifInfo    = 0x10
	niifInfo   = 1
)

// notifyIconData mirrors NOTIFYICONDATAW (the version with the balloon icon).
type notifyIconData struct {
	Size             uint32
	Wnd              uintptr
	ID, Flags        uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State, StateMask uint32
	Info             [256]uint16
	Timeout          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         [16]byte
	BalloonIcon      uintptr
}

// trayWindow finds the tray icon's window among the windows of this process.
func trayWindow() uintptr {
	class, _ := windows.UTF16PtrFromString(trayClass)
	var after uintptr
	for {
		h, _, _ := pFindWindowExW.Call(0, after, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			return 0
		}
		var pid uint32
		pGetWindowThread.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(os.Getpid()) {
			return h
		}
		after = h
	}
}

func fill(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0) // keep room for the terminator
	}
	copy(dst, u)
}

// Notify shows a Windows notification with a title and a text. It fails when the tray icon
// does not exist (yet), in which case there is nowhere to show it.
func Notify(title, text string) error {
	hwnd := trayWindow()
	if hwnd == 0 {
		return errors.New("the tray icon does not exist")
	}
	nid := notifyIconData{Wnd: hwnd, ID: trayIconID, Flags: nifInfo, InfoFlags: niifInfo}
	nid.Size = uint32(unsafe.Sizeof(nid))
	fill(nid.InfoTitle[:], title)
	fill(nid.Info[:], text)
	if r, _, err := pNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return err
	}
	return nil
}
