//go:build unix

package friend

import (
	"os"
	"syscall"
)

func withLaneLock(path string, action func() (string, error)) (string, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return action()
}
