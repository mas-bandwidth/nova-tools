package friend

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

func newToolResultWatch(dir string) (toolResultWatch, error) {
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, errors.Join(err, unix.Close(dirFD))
	}
	unix.CloseOnExec(fd)
	change := unix.Kevent_t{Ident: uint64(dirFD), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE}
	_, err = unix.Kevent(fd, []unix.Kevent_t{change}, nil, nil)
	if err != nil {
		return nil, errors.Join(err, unix.Close(fd), unix.Close(dirFD))
	}
	return &toolResultNativeWatch{fd: fd, dirFD: dirFD}, nil
}

func (w *toolResultNativeWatch) wait(cancelFD int, deadline time.Time) (bool, error) {
	change := unix.Kevent_t{Ident: uint64(cancelFD), Filter: unix.EVFILT_READ, Flags: unix.EV_ADD | unix.EV_CLEAR}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		timeout := unix.NsecToTimespec(remaining.Nanoseconds())
		var events [2]unix.Kevent_t
		n, err := unix.Kevent(w.fd, []unix.Kevent_t{change}, events[:], &timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		changed := false
		for _, event := range events[:n] {
			if event.Flags&unix.EV_ERROR != 0 {
				return false, unix.Errno(event.Data)
			}
			if event.Ident == uint64(cancelFD) {
				return false, nil
			}
			if event.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME|unix.NOTE_REVOKE) != 0 {
				return false, errors.New("tool-result directory moved, disappeared or was revoked; pending events preserved; rearm on its real path")
			}
			changed = true
		}
		return changed, nil
	}
}
