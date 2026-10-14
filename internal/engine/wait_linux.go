//go:build linux

package engine

import (
	"syscall"
	"unsafe"
)

// Wait without reaping: the supervisor's PID/PGID remains reserved until its
// entire process group has been killed. Only the caller then invokes Cmd.Wait.
func waitForExitWithoutReaping(pid int) error {
	var siginfo [16]uint64 // Linux siginfo_t is 128 bytes, naturally aligned.
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(pid), uintptr(unsafe.Pointer(&siginfo[0])), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}
