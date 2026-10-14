//go:build darwin

package engine

import "syscall"

// kqueue observes process exit without reaping the child. Keeping the group
// leader unreaped prevents its PID/PGID from being reused before group cleanup.
func waitForExitWithoutReaping(pid int) error {
	kq, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer syscall.Close(kq)
	event := syscall.Kevent_t{Ident: uint64(pid), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ENABLE | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}
	events := make([]syscall.Kevent_t, 1)
	changes := []syscall.Kevent_t{event}
	for {
		n, err := syscall.Kevent(kq, changes, events, nil)
		if err == syscall.EINTR {
			changes = nil
			continue
		}
		if err != nil {
			return err
		}
		if n > 0 {
			if events[0].Flags&syscall.EV_ERROR != 0 {
				return syscall.Errno(events[0].Data)
			}
			return nil
		}
		changes = nil
	}
}
