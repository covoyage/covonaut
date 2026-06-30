//go:build darwin

package terminal

import (
	"syscall"
	"unsafe"
)

// macOS termios layout (sys/termios.h).
//
// struct termios {
//   tcflag_t c_iflag;    // uint64
//   tcflag_t c_oflag;    // uint64
//   tcflag_t c_cflag;    // uint64
//   tcflag_t c_lflag;    // uint64
//   cc_t     c_cc[NCCS]; // NCCS = 20, cc_t = uint8
//   speed_t  c_ispeed;   // uint64 (Go auto-pads 4 bytes before this)
//   speed_t  c_ospeed;   // uint64
// };

const nccs = 20

type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [nccs]uint8
	Ispeed uint64
	Ospeed uint64
}

const (
	ioctlTcgets     = 0x40487413 // TIOCGETA
	ioctlTcsets     = 0x80487414 // TIOCSETA
	ioctlWinsizeGet = 0x40087468 // TIOCGWINSZ

	iIGNBRK = 0x1
	iBRKINT = 0x2
	iPARMRK = 0x8
	iISTRIP = 0x20
	iINLCR  = 0x40
	iIGNCR  = 0x80
	iICRNL  = 0x100
	iIXON   = 0x200

	oOPOST = 0x1

	lECHO   = 0x8
	lECHONL = 0x10
	lICANON = 0x100
	lISIG   = 0x80
	lIEXTEN = 0x400

	cCSIZE  = 0x300
	cPARENB = 0x1000
	cCS8    = 0x300

	vmin  = 16
	vtime = 17
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
	t.Cc[vtime] = 1 // 100ms read timeout so the read loop can check stopRead
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
