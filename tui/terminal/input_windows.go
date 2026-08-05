//go:build windows

package terminal

import (
	"os"

	"golang.org/x/sys/windows"
)

func duplicateInput(file *os.File) (*os.File, error) {
	process := windows.CurrentProcess()
	var handle windows.Handle
	err := windows.DuplicateHandle(process, windows.Handle(file.Fd()), process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), file.Name()+"-read"), nil
}

func cancelRead(file *os.File) error {
	return windows.CancelIoEx(windows.Handle(file.Fd()), nil)
}
