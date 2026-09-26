//go:build functional

package launch

// Rowan's failure-guidance audit (2026-09-26). startDetachedArgsEnv
// (spawn_unix.go:52-64) starts the card wrapper with Stdout and Stderr nil,
// so everything the wrapper prints after LAUNCHED -- including nova-card's
// keepRefusal line when Redis will not take the refusal -- goes to
// /dev/null, and the launch returns no path where it could be read.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRowanAuditDetachedWrapperOutputIsKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "fake-nova-card")
	done := filepath.Join(dir, "done")
	body := "#!/bin/sh\nread line\necho LAUNCHED >&3\necho 'nova-card: could not record refusal: dial tcp 127.0.0.1:1: connection refused' >&2\ntouch " + done + "\nexit 7\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	pid, ack, err := startDetachedArgsEnv(wrapper, []string{wrapper, "audit/c/1"}, "audit c 1 tok", time.Now().Add(5*time.Second), nil)
	if err != nil || ack != "LAUNCHED" {
		t.Fatalf("launch pid=%d ack=%q err=%v", pid, ack, err)
	}
	for i := 0; i < 250; i++ {
		if _, err := os.Stat(done); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Nothing in the launch's return, and nothing the launcher set up, holds
	// the wrapper's stderr: look for any file under dir that does.
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != wrapper {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "could not record refusal") {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatalf("the detached wrapper's stderr (a refusal it could not record) and exit 7 are kept nowhere; launch returned only pid=%d ack=%q", pid, ack)
	}
}
