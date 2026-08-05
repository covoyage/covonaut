//go:build !windows

package terminal

import (
	"os"
	"syscall"
)

func duplicateInput(file *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), file.Name()+"-read"), nil
}

func cancelRead(*os.File) error { return nil }
