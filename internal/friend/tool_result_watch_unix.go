//go:build linux || darwin

package friend

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type toolResultNativeWatch struct {
	fd, dirFD int
}

func (w *toolResultNativeWatch) Close() error {
	err := unix.Close(w.fd)
	if w.dirFD >= 0 {
		err = errors.Join(err, unix.Close(w.dirFD))
	}
	return err
}

func (w *toolResultNativeWatch) Wait(ctx context.Context, timeout time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if timeout <= 0 {
		return false, nil
	}
	// This anonymous pipe only cancels our own blocking kernel call. It is
	// neither an application IPC endpoint nor a transport for event payloads.
	read, write, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer read.Close()  // ignored: the observer has no durable writes
	defer write.Close() // ignored: cancellation is already joined below
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			_, err := write.Write([]byte{1})
			done <- err
		case <-stop:
			done <- nil
		}
	}()
	changed, err := w.wait(int(read.Fd()), time.Now().Add(timeout))
	close(stop)
	cancelErr := <-done
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return changed, errors.Join(err, cancelErr)
}
