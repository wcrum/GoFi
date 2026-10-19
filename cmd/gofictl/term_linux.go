//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// terminalSize returns the terminal's columns and rows, or 0, 0 when f is
// not a terminal.
func terminalSize(f *os.File) (int, int) {
	var ws struct {
		Rows, Cols, X, Y uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Cols), int(ws.Rows)
}

// rawInput switches f to unbuffered, no-echo input so single key presses
// arrive immediately (signals such as Ctrl-C still work). It returns a
// restore function, or nil when the terminal could not be changed, in which
// case the caller falls back to line input.
func rawInput(f *os.File) func() {
	var saved syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&saved))); errno != 0 {
		return nil
	}
	raw := saved
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&saved)))
	}
}
