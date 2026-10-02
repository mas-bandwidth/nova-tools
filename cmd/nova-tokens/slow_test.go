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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

func TestASecondFoldWaitsAndThenRefusesNamingTheHolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go")+"\n")
	release, err := tokens.TakeFoldLock(out, tokens.LockWait)
	require.False(t, err != nil, err)
	defer release()
	// The second one waits its bounded time and refuses rather than writing beside the
	// first: two folds on one --out write one fixed temp name.
	start := time.Now()
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "fold.lock")
	wantContains(t, r.stderr, "pid ")
	{
		waited := time.Since(start)
		assert.False(t, waited < 500*time.Millisecond, "the second fold refused after %s; it is supposed to wait for the first", waited)
	}
	{
		_, err := os.Stat(filepath.Join(out, "2026-09-11.tsv"))
		assert.False(t, err == nil, "the refused fold wrote a day file")
	}
}

// fakeSleepMode is the sqlite3 that answers nothing and outlives any timeout a test would
// set: the subprocess rule 19 is about. It waits on the wall clock, so it lives here.
const (
	fakeSleepMode = "sleep"
	fakeSleep     = 30 * time.Second
)

func init() {
	fakeModes[fakeSleepMode] = func() int {
		time.Sleep(fakeSleep)
		return 0
	}
}

// fakeSqlite3Sleeping puts the sleeping sqlite3 on PATH.
func fakeSqlite3Sleeping(t *testing.T) {
	t.Helper()
	fakeSqlite3OnPath(t, fakeSleepMode)
}

// SLOW: 1.0 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
func TestRule19ASubprocessPastTheTimeoutIsUnreadableAndTheFoldGoesOn(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	scratch := mkdir(t, filepath.Join(dir, "scratch"))
	db := write(t, filepath.Join(dir, "opencode.db"), "SQLite format 3\x00\n")
	fakeSqlite3Sleeping(t)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 3}, "/x/schema/a.go")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir),
		"--opencode", "bench="+db, "--scratch", scratch, "--claude", "g="+tr, "--timeout", "1")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "timeout after 1s")
	{
		_, err := os.Stat(filepath.Join(out, "2026-09-11.tsv"))
		assert.False(t, err != nil, "the fold did not continue over the other sources")
	}
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr, "--timeout", "0"), 2)
}
