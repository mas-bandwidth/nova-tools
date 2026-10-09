package friend

import (
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func newToolResultWatch(dir string) (toolResultWatch, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	_, err = unix.InotifyAddWatch(fd, dir, unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_UNMOUNT|unix.IN_ONLYDIR|unix.IN_DONT_FOLLOW)
	if err != nil {
		closeErr := unix.Close(fd)
		return nil, errors.Join(err, closeErr)
	}
	return &toolResultNativeWatch{fd: fd, dirFD: -1}, nil
}

func (w *toolResultNativeWatch) wait(cancelFD int, deadline time.Time) (bool, error) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}, {Fd: int32(cancelFD), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		if fds[1].Revents != 0 {
			return false, nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return false, errors.New("tool-result directory observer closed; pending events preserved; rearm the root wait")
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			return false, nil
		}
		var raw [4096]byte
		n, err := unix.Read(w.fd, raw[:])
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return false, err
		}
		changed, err := toolResultInotifyChanges(raw[:n])
		if changed || err != nil {
			return changed, err
		}
	}
}

func toolResultInotifyChanges(raw []byte) (bool, error) {
	changed := false
	for len(raw) >= unix.SizeofInotifyEvent {
		mask := binary.NativeEndian.Uint32(raw[4:8])
		nameLen := binary.NativeEndian.Uint32(raw[12:16])
		if mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_UNMOUNT|unix.IN_IGNORED) != 0 {
			return false, errors.New("tool-result directory moved, disappeared or unmounted; pending events preserved; rearm on its real path")
		}
		if uint64(nameLen)+unix.SizeofInotifyEvent > uint64(len(raw)) {
			return false, errors.New("tool-result directory notification is truncated; rearm the root wait")
		}
		name := strings.TrimRight(string(raw[unix.SizeofInotifyEvent:unix.SizeofInotifyEvent+int(nameLen)]), "\x00")
		// Ignore lock-file close writes and temporary publication filenames:
		// rescanning takes the lock itself and must not feed its own observer.
		// IN_Q_OVERFLOW has no filename: durable rescan, not event loss.
		changed = changed || mask&unix.IN_Q_OVERFLOW != 0 || name == "binding.json" || strings.HasSuffix(name, ".event.json") || strings.HasSuffix(name, ".pending.json")
		raw = raw[unix.SizeofInotifyEvent+int(nameLen):]
	}
	if len(raw) != 0 {
		return false, errors.New("tool-result directory notification is malformed; rearm the root wait")
	}
	return changed, nil
}
