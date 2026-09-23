//go:build !unix

package lifecycle

import (
	"errors"
	"os"
	"time"
)

func lockFile(f *os.File) error {
	sent := f.Name() + ".held"
	deadline := time.Now().Add(10 * time.Second)
	for {
		h, err := os.OpenFile(sent, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_ = h.Close()
			return nil
		}
		if !time.Now().Before(deadline) {
			return err
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func unlockFile(f *os.File) {
	_ = os.Remove(f.Name() + ".held")
}
