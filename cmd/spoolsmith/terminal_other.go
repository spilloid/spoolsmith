//go:build !windows

package main

import "os"

// isTerminal reports whether file is an interactive terminal. /dev/null is a
// character device too, so the file mode alone would call `< /dev/null` or
// `> /dev/null` interactive; it is ruled out explicitly. Windows asks the
// console instead (terminal_windows.go).
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}
