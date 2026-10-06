package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// demoQuote quotes a value as redis-cli --no-raw prints it (sdscatrepr), the
// way the store backup writes each DUMP payload.
func demoQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\a':
			b.WriteString(`\a`)
		case c == '\b':
			b.WriteString(`\b`)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// stubDemo gives the demo under root the test's processes, for this test
// alone: the hooks are keyed by the root, a directory of the test's own.
func stubDemo(t *testing.T, root string, start func(ctx context.Context, bin, dir string) (demoServer, error), alive func(int) bool, kill func(demoState) error) {
	t.Helper()
	root, err := filepath.Abs(root)
	require.NoError(t, err)
	demoHooksMu.Lock()
	demoHooks[root] = demoProcs{start: start, alive: alive, kill: kill}
	demoHooksMu.Unlock()
	t.Cleanup(func() {
		demoHooksMu.Lock()
		delete(demoHooks, root)
		demoHooksMu.Unlock()
	})
}

// A line of the dump is read as redis-cli reads it: every byte of a payload,
// quoted as redis-cli prints it, comes back as it was.
func TestDemoSplitArgsReadsWhatRedisCliPrints(t *testing.T) {
	t.Parallel()
	payload := string([]byte{0x00, 0x1b, '"', '\\', '\n', '\r', '\t', '\a', '\b', 0xff, 'a', ' ', 'z', 0x7f})
	line := "RESTORE " + demoQuote("table:work:15:cell:a b") + " 0 " + demoQuote(payload) + " REPLACE"
	got, err := demoSplitArgs(line)
	require.NoError(t, err)
	assert.Equal(t, []string{"RESTORE", "table:work:15:cell:a b", "0", payload, "REPLACE"}, got)

	got, err = demoSplitArgs(`  RESTORE cap:log 0 'it\'s' REPLACE  `)
	require.NoError(t, err)
	assert.Equal(t, []string{"RESTORE", "cap:log", "0", "it's", "REPLACE"}, got)

	for _, bad := range []string{`RESTORE k 0 "open`, `RESTORE k 0 "x"y`, `RESTORE k 0 'open`} {
		_, err := demoSplitArgs(bad)
		assert.Error(t, err, "%q was read", bad)
	}
}

// The parts are joined in name order whatever order they are given in, and
// parts of two files, or a part given twice, are refused.
func TestDemoPartsJoinInNameOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stem := filepath.Join(dir, "s.redis.txt.xz")
	for _, p := range []string{".part-aa", ".part-ab", ".part-ac"} {
		require.NoError(t, os.WriteFile(stem+p, []byte(p), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "t.xz.part-aa"), nil, 0o600))
	parts, joined, err := demoParts([]string{stem + ".part-ac", stem + ".part-aa", stem + ".part-ab"})
	require.NoError(t, err)
	assert.Equal(t, []string{stem + ".part-aa", stem + ".part-ab", stem + ".part-ac"}, parts)
	assert.Equal(t, stem, joined)

	_, _, err = demoParts([]string{stem + ".part-aa", filepath.Join(dir, "t.xz.part-aa")})
	assert.ErrorContains(t, err, "two files")
	_, _, err = demoParts([]string{stem + ".part-aa", stem + ".part-aa"})
	assert.ErrorContains(t, err, "given twice")
}

// A backup that does not match the sum file beside it starts nothing: no
// Redis, no directory, no state.
func TestDemoLoadRefusesADamagedBackupAndStartsNothing(t *testing.T) {
	t.Parallel()
	dir, root := t.TempDir(), filepath.Join(t.TempDir(), "demo")
	stem := filepath.Join(dir, "s.redis.txt.xz")
	require.NoError(t, os.WriteFile(stem+".part-aa", []byte("one"), 0o600))
	require.NoError(t, os.WriteFile(stem+".part-ab", []byte("two"), 0o600))
	sum := sha256.Sum256([]byte("onetwo"))
	require.NoError(t, os.WriteFile(stem+".sha256", []byte(hex.EncodeToString(sum[:])+"  s.redis.txt.xz\n"), 0o600))
	started := 0
	stubDemo(t, root, func(context.Context, string, string) (demoServer, error) {
		started++
		return demoServer{}, fmt.Errorf("not in a unit test")
	}, nil, nil)

	// a damaged part: the sum does not match
	require.NoError(t, os.WriteFile(stem+".part-ab", []byte("twx"), 0o600))
	a := newApp(func(string) string { return "" })
	var out, errb bytes.Buffer
	assert.Equal(t, 1, a.run([]string{"demo", "load", "--dir", root, stem + ".part-ab", stem + ".part-aa"}, &out, &errb))
	assert.Contains(t, errb.String(), "the backup is damaged or a part is missing, and nothing was started")
	// a --sha256 given is checked the same
	errb.Reset()
	assert.Equal(t, 1, a.run([]string{"demo", "load", "--dir", root, "--sha256", strings.Repeat("0", 64), stem + ".part-aa"}, &out, &errb))
	assert.Contains(t, errb.String(), "nothing was started")
	assert.Zero(t, started, "a Redis was started for a damaged backup")
	_, err := os.Stat(root)
	assert.ErrorIs(t, err, os.ErrNotExist, "a damaged backup left the demo's root")
}

// demo stop removes the directory its state file records, by that literal
// path, and the state file; never a sibling, never the root, and never a
// directory demo load does not make.
func TestDemoStopRemovesOnlyTheRecordedDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mine := filepath.Join(root, "redis-123")
	other := filepath.Join(root, "redis-other")
	for _, d := range []string{mine, other} {
		require.NoError(t, os.MkdirAll(filepath.Join(d, "sub"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(d, "sub", "f"), []byte("x"), 0o600))
	}
	keep := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("x"), 0o600))
	killed := 0
	// the recorded pid is gone: nothing is signalled
	stubDemo(t, root, nil, func(int) bool { return false }, func(demoState) error { killed++; return nil })

	outside := t.TempDir()
	for _, bad := range []string{outside, root, filepath.Join(root, "keep.txt"), filepath.Join(root, "redis-123", ".."), "redis-123"} {
		require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:1", PID: 4242, Dir: bad}))
		a := newApp(func(string) string { return "" })
		var out, errb bytes.Buffer
		assert.Equal(t, 1, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb), "a recorded directory %q was taken", bad)
		assert.Contains(t, errb.String(), "nothing was stopped or removed")
		assert.DirExists(t, outside)
		assert.DirExists(t, mine)
	}

	require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:1", PID: 4242, Dir: mine}))
	a := newApp(func(string) string { return "" })
	var out, errb bytes.Buffer
	require.Equal(t, 0, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb), "%s", errb.String())
	assert.Contains(t, out.String(), "DEMO STOPPED addr=127.0.0.1:1 pid=4242 removed="+mine)
	assert.NoDirExists(t, mine)
	assert.FileExists(t, filepath.Join(other, "sub", "f"))
	assert.FileExists(t, keep)
	assert.NoFileExists(t, filepath.Join(root, demoStateFile))
	assert.Zero(t, killed, "a pid that is gone was signalled")

	// a second stop finds no demo
	errb.Reset()
	assert.Equal(t, 1, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb))
	assert.Contains(t, errb.String(), "no demo is up")
}

// A recorded pid that is alive and is not the Redis at the recorded address
// is never signalled, and nothing is removed.
func TestDemoStopNeverSignalsAPidThatIsNotTheDemo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mine := filepath.Join(root, "redis-1")
	require.NoError(t, os.MkdirAll(mine, 0o700))
	killed := 0
	stubDemo(t, root, nil, func(int) bool { return true }, func(demoState) error { killed++; return nil })
	// nothing listens on port 1: the pid cannot be shown to be the demo's
	require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:1", PID: os.Getpid(), Dir: mine}))
	a := newApp(func(string) string { return "" })
	var out, errb bytes.Buffer
	assert.Equal(t, 1, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb))
	assert.Contains(t, errb.String(), "is not the demo's Redis")
	assert.Zero(t, killed)
	assert.DirExists(t, mine)
	assert.FileExists(t, filepath.Join(root, demoStateFile))
}

// The sum beside the parts is the hand backup's <joined>.sha256, else the
// line of backup --out's SHA256SUMS naming the joined file; with neither the
// load says unchecked.
func TestDemoSumBesideIsTheSumFileOrSHA256SUMS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stem := filepath.Join(dir, "sprint-epoch0.restore.txt.xz")
	want, from, err := demoSumBeside(stem)
	require.NoError(t, err)
	assert.Empty(t, want)
	assert.Empty(t, from)

	xzSum, textSum := strings.Repeat("a", 64), strings.Repeat("b", 64)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(textSum+"  sprint-epoch0.restore.txt\n"+xzSum+"  sprint-epoch0.restore.txt.xz\n"), 0o600))
	want, from, err = demoSumBeside(stem)
	require.NoError(t, err)
	assert.Equal(t, xzSum, want, "the sum of the text was taken for the xz's")
	assert.Equal(t, filepath.Join(dir, "SHA256SUMS"), from)

	side := strings.Repeat("c", 64)
	require.NoError(t, os.WriteFile(stem+".sha256", []byte(side+"  sprint-epoch0.restore.txt.xz\n"), 0o600))
	want, from, err = demoSumBeside(stem)
	require.NoError(t, err)
	assert.Equal(t, side, want)
	assert.Equal(t, stem+".sha256", from)
}

// The demo opens no store but the one it starts: a --redis is refused, and
// nothing is started.
func TestDemoTakesNoRedis(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "demo")
	stubDemo(t, root, func(context.Context, string, string) (demoServer, error) {
		t.Error("a Redis was started")
		return demoServer{}, fmt.Errorf("not in a unit test")
	}, nil, nil)
	a := newApp(func(string) string { return "" })
	var out, errb bytes.Buffer
	assert.Equal(t, 2, a.run([]string{"demo", "load", "--dir", root, "--redis", "127.0.0.1:6379", "x.xz.part-aa"}, &out, &errb))
	assert.Contains(t, errb.String(), "takes no --redis")
	errb.Reset()
	assert.Equal(t, 2, a.run([]string{"demo", "stop", "--dir", root, "--redis", "127.0.0.1:6379"}, &out, &errb))
	assert.Contains(t, errb.String(), "takes no --redis")
	assert.NoDirExists(t, root)
}
