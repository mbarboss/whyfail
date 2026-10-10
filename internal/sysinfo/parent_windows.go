package sysinfo

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func parentName() (string, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", fmt.Errorf("sysinfo: process snapshot: %w", err)
	}
	defer syscall.CloseHandle(snap) //nolint:errcheck // read-only handle; nothing to recover on close

	ppid := uint32(os.Getppid()) //nolint:gosec // G115: Windows process IDs are DWORDs
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e)) //nolint:gosec // G103: the API requires the struct size
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if e.ProcessID == ppid {
			return syscall.UTF16ToString(e.ExeFile[:]), nil
		}
	}
	if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		return "", fmt.Errorf("sysinfo: parent process %d not found", ppid)
	}
	return "", fmt.Errorf("sysinfo: list processes: %w", err)
}
