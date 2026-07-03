package main

import (
	"os"
	"syscall"
	"unsafe"
)

// ttyWidth returns the terminal column count via TIOCGWINSZ, or 0 if unavailable.
func ttyWidth() int {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
