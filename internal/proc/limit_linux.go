package proc

import (
	"syscall"
	"unsafe"
)

// setDataLimit sets RLIMIT_DATA of the process pid with prlimit(2).
func setDataLimit(pid int, max uint64) error {
	lim := syscall.Rlimit{Cur: max, Max: max}
	// #nosec G103 -- the system call reads the Rlimit value.
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(pid), syscall.RLIMIT_DATA,
		uintptr(unsafe.Pointer(&lim)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
