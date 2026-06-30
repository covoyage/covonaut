//go:build linux

package terminal

import (
	"syscall"
	"unsafe"
)

// Linux termios layout (asm-generic/termbits.h).
//
// struct termios {
//   tcflag_t c_iflag;    // uint32
//   tcflag_t c_oflag;    // uint32
//   tcflag_t c_cflag;    // uint32
//   tcflag_t c_lflag;    // uint32
//   cc_t     c_line;     // uint8
//   cc_t     c_cc[NCCS]; // NCCS = 32, cc_t = uint8
//   speed_t  c_ispeed;   // uint32
//   speed_t  c_ospeed;   // uint32
// };

const nccs = 32

type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [nccs]uint8
	Ispeed uint32
	Ospeed uint32
}

const (
	ioctlTcgets     = 0x5401 // TCGETS
	ioctlTcsets     = 0x5402 // TCSETS
	ioctlWinsizeGet = 0x5413 // TIOCGWINSZ

	iIGNBRK = 0x1
	iBRKINT = 0x2
	iPARMRK = 0x8
	iISTRIP = 0x20
	iINLCR  = 0x40
	iIGNCR  = 0x80
	iICRNL  = 0x100
	iIXON   = 0x400

	oOPOST = 0x1

	lECHO   = 0x8
	lECHONL = 0x40
	lICANON = 0x2
	lISIG   = 0x1
	lIEXTEN = 0x8000

	cCSIZE  = 0x30
	cPARENB = 0x100
	cCS8    = 0x30

	vmin  = 6
	vtime = 5
)

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func getTermios(fd uintptr) (termios, error) {
	var t termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTcgets, uintptr(unsafe.Pointer(&t)))
	if errno != 0 {
		return t, errno
	}
	return t, nil
}

func setTermios(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTcsets, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

func makeRaw(orig termios) termios {
	t := orig
	t.Iflag &^= iIGNBRK | iBRKINT | iPARMRK | iISTRIP | iINLCR | iIGNCR | iICRNL | iIXON
	t.Oflag &^= oOPOST
	t.Lflag &^= lECHO | lECHONL | lICANON | lISIG | lIEXTEN
	t.Cflag &^= cCSIZE | cPARENB
	t.Cflag |= cCS8
	t.Cc[vmin] = 0
	t.Cc[vtime] = 1
	return t
}

func getWinsize(fd uintptr) (cols, rows int64, err error) {
	var w winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlWinsizeGet, uintptr(unsafe.Pointer(&w)))
	if errno != 0 {
		return 0, 0, errno
	}
	return int64(w.Col), int64(w.Row), nil
}
