package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// isTerminal reports whether file is an interactive console. NUL is a
// character device too, so the file mode alone would call `< NUL` or
// `> NUL` interactive; only a real console answers GetConsoleMode.
func isTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}
