// lease.go: one renderer per output (#3045; #2756 6.1 and 6.7). The renderer
// that writes a table file holds lease:table:<out>, taken with AcquireLock's
// SET NX PX and kept alive by the tick's own pipeline (SprintConfig.LockKey is
// LeaseKey(out)); a second renderer on the same output is refused with the
// holder named. WriteAtomic writes the file through a temp file in the same
// directory and a rename, so a reader never sees half a table.
package table

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
)

// LeaseKey is the renderer lease of one output file: lease:table:<out>, with
// out made absolute and clean so two spellings of one path are one lease.
func LeaseKey(out string) string {
	if abs, err := filepath.Abs(out); err == nil {
		out = abs
	}
	return "lease:table:" + filepath.Clean(out)
}

// LeaseHeld is the refusal of a second renderer: Key is held by Holder.
type LeaseHeld struct {
	Key, Holder string
}

func (e *LeaseHeld) Error() string {
	return fmt.Sprintf("REFUSED: %s is held by %s; one renderer per output", e.Key, e.Holder)
}

// AcquireRenderer takes the lease of out for token, or returns *LeaseHeld
// naming the renderer that holds it.
func AcquireRenderer(ctx context.Context, client redis.UniversalClient, out, token string, ttl time.Duration) error {
	key := LeaseKey(out)
	holder, err := AcquireLock(ctx, client, key, token, ttl)
	if err != nil {
		return fmt.Errorf("renderer lease %s: %w", key, err)
	}
	if holder != token {
		return &LeaseHeld{Key: key, Holder: holder}
	}
	return nil
}

// ReleaseRenderer gives the lease of out back, only while token holds it.
func ReleaseRenderer(ctx context.Context, client redis.UniversalClient, out, token string) error {
	return ReleaseLock(ctx, client, LeaseKey(out), token)
}

// WriteAtomic writes body to path: a fresh temp file in path's own directory
// (so the rename is atomic and two writers never share one), synced, then
// renamed over path. A reader sees the old table or the new one, never part
// of either; the temp file is removed on any failure.
func WriteAtomic(path, body string) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("--out: %w", err)
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		return fail(err)
	}
	if err := f.Chmod(0o644); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	return nil
}
