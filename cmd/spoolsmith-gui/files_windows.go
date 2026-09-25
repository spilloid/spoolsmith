//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Printer files reach the app every way Windows offers: double-clicked in
// Explorer (the .ssb association), passed on the command line, dropped onto
// the window, and pasted. They all end in the same place -- openPrinterFiles
// -- which opens the apply sheet for one printer or the member list for a
// set. Nothing here changes Windows; the sheet's single confirmation still
// guards every change.

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procSetPropW             = user32.NewProc("SetPropW")
	procGetPropW             = user32.NewProc("GetPropW")
	procAllowSetForeground   = user32.NewProc("AllowSetForegroundWindow")
	procSendMessageTimeout   = user32.NewProc("SendMessageTimeoutW")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	procSHChangeNotify       = shell32.NewProc("SHChangeNotify")
	windowPropName, _        = windows.UTF16PtrFromString("SpoolSmith.MainWindow")
	previousMainWindowProc   uintptr
	copyDataCallback         uintptr
	mainApp                  *app
	cfPreferredDropEffect, _ = registerClipboardFormat("Preferred DropEffect")
)

const copyDataOpenFiles = 0x53534231 // "SSB1"

// openPrinterFiles opens what was handed to the app. One printer file or set
// is reviewed; with several, the first is opened and the rest are named so
// nothing is silently dropped.
func (a *app) openPrinterFiles(paths []string, how string) {
	if len(paths) == 0 {
		return
	}
	if a.mutationBusy {
		walk.MsgBox(a.mw, "Please wait", "Finish the current printer operation before opening another printer file.", walk.MsgBoxIconInformation)
		return
	}
	candidates, rejected := printerFileCandidates(paths)
	if len(candidates) == 0 {
		showErr(a.mw, "Open printer file", fmt.Errorf("%s: only printer files (.ssb) and printer sets (.zip) can be opened here", strings.Join(rejected, ", ")))
		return
	}
	if len(candidates) > 1 || len(rejected) > 0 {
		var skipped []string
		for _, path := range candidates[1:] {
			skipped = append(skipped, filepath.Base(path))
		}
		skipped = append(skipped, rejected...)
		walk.MsgBox(a.mw, "Open printer file", fmt.Sprintf("Opening %s first. Open the others one at a time, so each printer gets its own review:\r\n\r\n%s",
			filepath.Base(candidates[0]), strings.Join(skipped, "\r\n")), walk.MsgBoxIconInformation)
	}
	path := candidates[0]
	a.log("gui", "open file", []string{how, path}, "success", nil, time.Now())
	if isSet, err := bundle.IsSet(path); err != nil {
		showErr(a.mw, "Open printer file", fmt.Errorf("%s", friendlyOperationError(err.Error())))
		return
	} else if isSet {
		a.showPrinterSet(path)
		return
	}
	a.reviewBundle(path)
}

// --- Drop, paste and copy ----------------------------------------------------

func (a *app) enableFileDrop() {
	a.mw.DropFiles().Attach(func(paths []string) {
		bringToFront(a.mw.Handle())
		a.openPrinterFiles(paths, "drop")
	})
	// An elevated window does not receive drops or hand-overs from ordinary
	// windows unless it says so; neither message can do more than open the
	// sheet, which still asks for confirmation.
	for _, message := range []uint32{win.WM_DROPFILES, win.WM_COPYDATA, 0x0049 /* WM_COPYGLOBALDATA */} {
		win.ChangeWindowMessageFilterEx(a.mw.Handle(), message, 1 /* MSGFLT_ALLOW */, nil)
	}

	paste := walk.NewAction()
	paste.SetShortcut(walk.Shortcut{Modifiers: walk.ModControl, Key: walk.KeyV})
	paste.Triggered().Attach(a.onPaste)
	a.mw.ShortcutActions().Add(paste)
}

// onPaste opens printer files copied in Explorer. Anywhere else a paste
// means text, so it is passed on to the focused box untouched.
func (a *app) onPaste() {
	paths := clipboardFiles(a.mw.Handle())
	if len(paths) == 0 {
		if focus := win.GetFocus(); focus != 0 {
			win.SendMessage(focus, win.WM_PASTE, 0, 0)
		}
		return
	}
	a.openPrinterFiles(paths, "paste")
}

// onCopyToClipboard writes the selected printers to printer files and puts
// the files on the clipboard, ready to paste into Explorer, a share or a
// chat. Each printer is copied exactly as Copy does it, driver preferred.
func (a *app) onCopyToClipboard() {
	queues := copyableOnly(a.selectedQueues())
	if len(queues) == 0 || a.clipBusy || a.mutationBusy {
		if selected := a.selectedQueues(); len(selected) > 0 && len(queues) == 0 {
			a.queueDetail.SetText("Can't copy " + selected[0].PrinterName + ": " + selected[0].CopyBlockedReason())
		}
		return
	}
	folder := filepath.Join(clipboardRoot(), clipboardFolderName(time.Now()))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		showErr(a.mw, "Copy printers", err)
		return
	}
	a.clipBusy = true
	a.updateQueueActions()
	start := time.Now()
	go func() {
		var written []string
		var failures []string
		for i, queue := range queues {
			name := queue.PrinterName
			a.mw.Synchronize(func() {
				a.queueStatus.SetText(fmt.Sprintf("Copying %s (%d of %d)...", name, i+1, len(queues)))
			})
			path := filepath.Join(folder, uniqueClipboardName(written, bundleFileName(name)))
			_, err := bundle.Create(context.Background(), a.env, a.collector(), bundle.CreateOptions{
				QueueName: name, Path: path, CreatedBy: "SpoolSmith desktop " + versionString(), SourceHost: hostName(),
			})
			a.log("gui", "copy to clipboard", []string{name, path}, statusOf(err), err, start)
			if err != nil {
				failures = append(failures, name+": "+err.Error())
				continue
			}
			written = append(written, path)
		}
		a.mw.Synchronize(func() {
			a.clipBusy = false
			var err error
			if len(written) > 0 {
				err = setClipboardFiles(a.mw.Handle(), written)
			}
			switch {
			case err != nil:
				a.queueStatus.SetText("Couldn't put the printer files on the clipboard.")
				showErr(a.mw, "Copy printers", err)
			case len(written) == 0:
				a.queueStatus.SetText("Nothing was copied.")
			default:
				a.queueStatus.SetText(fmt.Sprintf("Copied %s. Paste into a folder, a share or a chat.", countPrinters(len(written))))
			}
			if len(failures) > 0 {
				showErr(a.mw, "Copy printers", fmt.Errorf("%s", friendlyOperationError(strings.Join(failures, "\r\n"))))
			}
			a.updateQueueActions()
		})
	}()
}

func uniqueClipboardName(taken []string, name string) string {
	stem, ext := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
	candidate := name
	for n := 2; ; n++ {
		clash := false
		for _, path := range taken {
			if strings.EqualFold(filepath.Base(path), candidate) {
				clash = true
				break
			}
		}
		if !clash {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
	}
}

// cleanClipboardFolders removes copies made more than a day ago.
func cleanClipboardFolders() {
	entries, err := os.ReadDir(clipboardRoot())
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && entry.IsDir() && staleClipboardFolder(entry.Name(), info.ModTime(), now) {
			os.RemoveAll(filepath.Join(clipboardRoot(), entry.Name()))
		}
	}
}

// DROPFILES header for CF_HDROP, followed by a double-NUL-terminated list of
// UTF-16 paths.
type dropFiles struct {
	pFiles uint32
	pt     win.POINT
	fNC    int32
	fWide  int32
}

func setClipboardFiles(owner win.HWND, paths []string) error {
	var list []uint16
	for _, path := range paths {
		encoded, err := windows.UTF16FromString(path)
		if err != nil {
			return err
		}
		list = append(list, encoded...)
	}
	list = append(list, 0)
	header := dropFiles{pFiles: uint32(unsafe.Sizeof(dropFiles{})), fWide: 1}
	size := uintptr(header.pFiles) + uintptr(len(list))*2

	return withClipboard(owner, func() error {
		if !win.EmptyClipboard() {
			return lastError("EmptyClipboard")
		}
		hMem := win.GlobalAlloc(win.GMEM_MOVEABLE, size)
		if hMem == 0 {
			return lastError("GlobalAlloc")
		}
		p := win.GlobalLock(hMem)
		if p == nil {
			win.GlobalFree(hMem)
			return lastError("GlobalLock")
		}
		*(*dropFiles)(p) = header
		copy(unsafe.Slice((*uint16)(unsafe.Add(p, header.pFiles)), len(list)), list)
		win.GlobalUnlock(hMem)
		if win.SetClipboardData(win.CF_HDROP, win.HANDLE(hMem)) == 0 {
			win.GlobalFree(hMem)
			return lastError("SetClipboardData")
		}
		// Tell Explorer this is a copy, not a cut.
		if cfPreferredDropEffect != 0 {
			if effect := win.GlobalAlloc(win.GMEM_MOVEABLE, 4); effect != 0 {
				if q := win.GlobalLock(effect); q != nil {
					*(*uint32)(q) = 1 // DROPEFFECT_COPY
					win.GlobalUnlock(effect)
					if win.SetClipboardData(cfPreferredDropEffect, win.HANDLE(effect)) == 0 {
						win.GlobalFree(effect)
					}
				}
			}
		}
		return nil
	})
}

func clipboardFiles(owner win.HWND) []string {
	if !win.IsClipboardFormatAvailable(win.CF_HDROP) {
		return nil
	}
	var paths []string
	_ = withClipboard(owner, func() error {
		handle := win.GetClipboardData(win.CF_HDROP)
		if handle == 0 {
			return nil
		}
		drop := win.HDROP(handle)
		count := win.DragQueryFile(drop, 0xFFFFFFFF, nil, 0)
		for i := uint(0); i < uint(count); i++ {
			buf := make([]uint16, win.DragQueryFile(drop, i, nil, 0)+1)
			if win.DragQueryFile(drop, i, &buf[0], uint(len(buf))) > 0 {
				paths = append(paths, windows.UTF16ToString(buf))
			}
		}
		return nil
	})
	return paths
}

func withClipboard(owner win.HWND, f func() error) error {
	var opened bool
	for attempt := 0; attempt < 10 && !opened; attempt++ {
		if opened = win.OpenClipboard(owner); !opened {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !opened {
		return lastError("OpenClipboard")
	}
	defer win.CloseClipboard()
	return f()
}

func registerClipboardFormat(name string) (uint32, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	r, _, callErr := user32.NewProc("RegisterClipboardFormatW").Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return 0, callErr
	}
	return uint32(r), nil
}

func lastError(what string) error {
	if err := windows.GetLastError(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return errors.New(what + " failed")
}

// --- Administrator relaunch -------------------------------------------------

// relaunchElevated starts an elevated copy of SpoolSmith on the same review,
// then closes this one. Windows' own consent prompt comes first; declining it
// leaves this window exactly as it was. The elevated copy prepares the
// preview again from the operation alone and asks for the usual single
// confirmation of the plan it shows -- nothing reviewed here is carried over
// as approved.
func (a *app) relaunchElevated(op operation) {
	exe, err := os.Executable()
	if err != nil {
		showErr(a.mw, "Continue as administrator", err)
		return
	}
	arg, err := encodeReview(op)
	if err != nil {
		showErr(a.mw, "Continue as administrator", err)
		return
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(quoteArg(arg))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	// ShellExecute with "runas" returns once the consent prompt is answered and
	// the elevated process has started.
	if err := windows.ShellExecute(windows.Handle(a.mw.Handle()), verb, file, params, dir, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			a.setHint("Administrator permission wasn't given, so nothing changed. Use the shield button to try again.")
			return
		}
		showErr(a.mw, "Continue as administrator", err)
		return
	}
	a.log("gui", "relaunch elevated", []string{string(op.Kind), op.PrinterName}, "success", nil, time.Now())
	a.mw.Close()
}

// --- One window --------------------------------------------------------------

// forwardToRunningInstance hands files to a SpoolSmith window that is already
// open, so double-clicking a second printer file does not open a second app.
// It reports whether the hand-over happened.
func forwardToRunningInstance(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	target := findMainWindow()
	if target == 0 {
		return false
	}
	payload, err := windows.UTF16FromString(strings.Join(paths, "\n"))
	if err != nil {
		return false
	}
	var pid uint32
	windows.GetWindowThreadProcessId(windows.HWND(target), &pid)
	procAllowSetForeground.Call(uintptr(pid))
	data := copyDataStruct{
		dwData: copyDataOpenFiles,
		cbData: uint32(len(payload) * 2),
		lpData: uintptr(unsafe.Pointer(&payload[0])),
	}
	var result uintptr
	sent, _, _ := procSendMessageTimeout.Call(uintptr(target), win.WM_COPYDATA, 0, uintptr(unsafe.Pointer(&data)), 0x0002 /* SMTO_ABORTIFHUNG */, 5000, uintptr(unsafe.Pointer(&result)))
	return sent != 0 && result == 1
}

func findMainWindow() win.HWND {
	var found win.HWND
	callback := syscall.NewCallback(func(hwnd win.HWND, _ uintptr) uintptr {
		if r, _, _ := procGetPropW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(windowPropName))); r != 0 {
			found = hwnd
			return 0
		}
		return 1
	})
	windows.EnumWindows(callback, nil)
	return found
}

// listenForHandOver marks this window as SpoolSmith's and accepts files that
// a second launch forwards to it.
func (a *app) listenForHandOver() {
	mainApp = a
	hwnd := a.mw.Handle()
	procSetPropW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(windowPropName)), 1)
	copyDataCallback = syscall.NewCallback(mainWindowProc)
	previousMainWindowProc = win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, copyDataCallback)
}

func mainWindowProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == win.WM_COPYDATA && lParam != 0 {
		// lParam and lpData are pointers Windows owns for the duration of
		// this message; reinterpreting the integer in place keeps vet quiet
		// about a conversion that is valid here.
		data := *(**copyDataStruct)(unsafe.Pointer(&lParam))
		if data.dwData == copyDataOpenFiles && data.cbData > 0 && data.cbData%2 == 0 && data.cbData < 1<<20 {
			text := windows.UTF16ToString(unsafe.Slice(*(**uint16)(unsafe.Pointer(&data.lpData)), data.cbData/2))
			paths := strings.Split(text, "\n")
			// Open after this message returns: the sender is waiting, and the
			// sheet may show dialogs of its own.
			mainApp.mw.Synchronize(func() {
				bringToFront(hwnd)
				mainApp.openPrinterFiles(paths, "open")
			})
			return 1
		}
	}
	return win.CallWindowProc(previousMainWindowProc, hwnd, msg, wParam, lParam)
}

func bringToFront(hwnd win.HWND) {
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
}

// --- .ssb association ---------------------------------------------------------

// associationState reports whether .ssb files open with this copy of
// SpoolSmith, with another copy, or with nothing SpoolSmith registered.
type associationState int

const (
	associationNone associationState = iota
	associationThis
	associationOther
)

func currentAssociation(exe string) associationState {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+printerFileProgID+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return associationNone
	}
	defer key.Close()
	command, _, err := key.GetStringValue("")
	if err != nil {
		return associationNone
	}
	ext, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+printerFileExt, registry.QUERY_VALUE)
	if err != nil {
		return associationNone
	}
	defer ext.Close()
	if progID, _, err := ext.GetStringValue(""); err != nil || progID != printerFileProgID {
		return associationNone
	}
	if strings.EqualFold(filepath.Clean(associationTarget(command)), filepath.Clean(exe)) {
		return associationThis
	}
	return associationOther
}

// registerAssociation makes .ssb files open with this exe, for this user only.
func registerAssociation(exe string) error {
	set := func(path, name, value string) error {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer key.Close()
		return key.SetStringValue(name, value)
	}
	for _, value := range []struct{ path, name, value string }{
		{printerFileExt, "", printerFileProgID},
		{printerFileExt + `\OpenWithProgids`, printerFileProgID, ""},
		{printerFileProgID, "", "SpoolSmith printer file"},
		{printerFileProgID + `\DefaultIcon`, "", `"` + exe + `",0`},
		{printerFileProgID + `\shell\open`, "FriendlyAppName", "SpoolSmith"},
		{printerFileProgID + `\shell\open\command`, "", associationCommand(exe)},
	} {
		if err := set(value.path, value.name, value.value); err != nil {
			return err
		}
	}
	notifyAssociationChanged()
	return nil
}

// unregisterAssociation removes only what registerAssociation added.
func unregisterAssociation() error {
	for _, path := range []string{
		printerFileProgID + `\shell\open\command`, printerFileProgID + `\shell\open`, printerFileProgID + `\shell`,
		printerFileProgID + `\DefaultIcon`, printerFileProgID, printerFileExt + `\OpenWithProgids`,
	} {
		if err := registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+printerFileExt, registry.QUERY_VALUE|registry.SET_VALUE); err == nil {
		if progID, _, err := key.GetStringValue(""); err == nil && progID == printerFileProgID {
			key.DeleteValue("")
		}
		key.Close()
		registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+printerFileExt)
	}
	notifyAssociationChanged()
	return nil
}

func notifyAssociationChanged() {
	const shcneAssocChanged, shcnfIDList = 0x08000000, 0x0000
	procSHChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
}

// Whether the operator has already answered the one-time offer is kept
// beside the association itself, per user.
const settingsKey = `Software\SpoolSmith`

func associationOfferAnswered() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, settingsKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetIntegerValue("AssociationOffered")
	return err == nil
}

func rememberAssociationOffer() {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, settingsKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	key.SetDWordValue("AssociationOffered", 1)
}

// toggleAssociation is the More menu's "Open .ssb files with SpoolSmith".
func (a *app) toggleAssociation() {
	exe, err := os.Executable()
	if err != nil {
		showErr(a.mw, "Printer files", err)
		return
	}
	rememberAssociationOffer()
	a.hideAssociationOffer()
	if currentAssociation(exe) == associationThis {
		if err := unregisterAssociation(); err != nil {
			showErr(a.mw, "Printer files", err)
		}
		return
	}
	if err := registerAssociation(exe); err != nil {
		showErr(a.mw, "Printer files", err)
		return
	}
	walk.MsgBox(a.mw, "Printer files", "Printer files (.ssb) now open in SpoolSmith when you double-click them.\r\n\r\nOnly your Windows account is changed. Turn it off from More at any time.", walk.MsgBoxIconInformation)
}

// copyDataStruct is COPYDATASTRUCT.
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}
