//go:build slow

// The fold-lock test, whose cost is a real clock: the second fold has to wait the
// production `tokens.LockWait` out before it refuses, and the assertion is that it
// waited. Ten seconds of this package's 13 s.
//
// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (a package's tests answer in a minute, two at
// most). Nothing here is skipped or weakened; it runs nightly, whole.

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The second fold on one --out waits its bounded time and refuses, naming the holder, rather
// than writing beside the first: two folds on one --out write one fixed temp name.
func TestASecondFoldWaitsAndThenRefusesNamingTheHolder(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
	release, err := tokens.TakeFoldLock(b.out, tokens.LockWait)
	require.NoError(t, err)
	defer release()

	start := time.Now()
	b.fold("--claude", "g="+b.tr).Exit(2).Err("fold.lock", "pid ")
	// fixed-waits-allowlist.txt names this measurement as slow_test.go:38; keep it there.
	waited := time.Since(start)
	assert.GreaterOrEqual(t, waited, 500*time.Millisecond, "the second fold refused after %s; it is supposed to wait for the first", waited)
	assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "the refused fold wrote a day file")
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

// SLOW: 1.0 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it
// out. The fold goes on over the other sources and writes the day; --timeout 0 is refused.
func TestRule19ASubprocessPastTheTimeoutIsUnreadableAndTheFoldGoesOn(t *testing.T) {
	b := newBench(t)
	db := testkit.WriteFile(t, filepath.Join(b.dir, "opencode.db"), "SQLite format 3\x00\n")
	fakeSqlite3OnPath(t, fakeSleepMode)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 3}, "/x/schema/a.go"))
	b.fold("--opencode", "bench="+db, "--scratch", testkit.Mkdir(t, filepath.Join(b.dir, "scratch")), "--claude", "g="+b.tr, "--timeout", "1").
		Exit(1).Err("timeout after 1s")
	assert.FileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), "the fold did not continue over the other sources")
	b.fold("--claude", "g="+b.tr, "--timeout", "0").Exit(2)
}
