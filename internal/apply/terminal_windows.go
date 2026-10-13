package apply

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OpenTerminal opens the console's input and output buffers. It fails when
// whyfail has no console.
func OpenTerminal() (Terminal, error) {
	in, err := openConsole("CONIN$")
	if err != nil {
		return Terminal{}, err
	}
	out, err := openConsole("CONOUT$")
	if err != nil {
		return Terminal{}, errors.Join(err, in.Close())
	}
	return Terminal{In: in, Out: out, Close: func() error { return errors.Join(in.Close(), out.Close()) }}, nil
}

// openConsole uses CreateFile instead of os.OpenFile so that os.NewFile sees
// a console handle and reads and writes it as UTF-16.
func openConsole(name string) (*os.File, error) {
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("apply: open %s: %w", name, err)
	}
	h, err := syscall.CreateFile(path,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("apply: open %s: %w", name, err)
	}
	return os.NewFile(uintptr(h), name), nil
}
