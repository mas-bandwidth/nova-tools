//go:build slow

// The fold-lock test, whose cost is a real clock: the second fold has to wait the
// production `tokens.LockWait` out before it refuses, and the assertion is that it
// waited. Ten seconds of this package's 13 s.
//
// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (#516, Glenn's two-minute rule -- a package's
// tests answer in a minute). Nothing here is skipped or weakened; it runs nightly, whole.

package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestASecondFoldWaitsAndThenRefusesNamingTheHolder(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go")+"\n")
	release, err := tokens.TakeFoldLock(out, tokens.LockWait)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The second one waits its bounded time and refuses rather than writing beside the
	// first: two folds on one --out write one fixed temp name.
	start := time.Now()
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "fold.lock")
	wantContains(t, r.stderr, "pid ")
	if waited := time.Since(start); waited < 500*time.Millisecond {
		t.Errorf("the second fold refused after %s; it is supposed to wait for the first", waited)
	}
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
		t.Error("the refused fold wrote a day file")
	}
}
