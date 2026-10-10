//go:build functional

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
)

// demoBackup makes a backup of a sprint made on a throwaway twin: every key a
// RESTORE line, xz -9, split into two parts, with the sum of the joined .xz
// beside them. hand is the hand backup of 2026-10-04 (a # header, the DUMP
// payload quoted as redis-cli prints it, REPLACE, <joined>.sha256 beside);
// else backup --out's (store.RestoreLine, SHA256SUMS beside). It returns the
// parts and the keys.
func demoBackup(t *testing.T, cards int, hand bool) ([]string, []string) {
	t.Helper()
	ctx := context.Background()
	twin := testredis.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: twin})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	coord := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": twin, "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	})
	t.Cleanup(coord.close)
	for _, args := range [][]string{
		{"init", "--members", "m1:4", "--readers", "reader-a,reader-b,reader-c"},
		{"add", "--stream", "demo", "--count", fmt.Sprint(cards), "--brief", passingBrief("demo-brief")},
	} {
		var out, errb bytes.Buffer
		require.Equal(t, 0, coord.run(args, &out, &errb), "%v: %s", args, errb.String())
	}
	keys, err := admin.Keys(ctx, "*").Result()
	require.NoError(t, err)
	sort.Strings(keys)
	var text bytes.Buffer
	if hand {
		fmt.Fprintf(&text, "# nova-sprint store backup: a test's twin\n# format: one key per line, sorted: RESTORE <key> <ttl ms, 0 = none> <DUMP payload, redis-cli quoted> REPLACE\n")
	}
	for _, k := range keys {
		dump, err := admin.Dump(ctx, k).Result()
		require.NoError(t, err)
		if hand {
			fmt.Fprintf(&text, "RESTORE %s 0 %s REPLACE\n", demoQuote(k), demoQuote(dump))
		} else {
			text.WriteString(store.RestoreLine(store.DumpKey{Key: k, Payload: []byte(dump)}))
		}
	}
	// the system xz, as the backup is made
	xzName := "xz"
	xzBin, err := exec.LookPath(xzName)
	require.NoError(t, err, "the backup is made with the system's xz")
	xz := exec.Command(xzBin, "-9", "-c")
	xz.Stdin = &text
	packed, err := xz.Output()
	require.NoError(t, err)
	dir := t.TempDir()
	stem := filepath.Join(dir, "sprint-store-test.redis.txt.xz")
	if !hand {
		stem = filepath.Join(dir, "sprint-epoch0.restore.txt.xz")
	}
	half := len(packed) / 2
	require.NoError(t, os.WriteFile(stem+".part-aa", packed[:half], 0o600))
	require.NoError(t, os.WriteFile(stem+".part-ab", packed[half:], 0o600))
	sum := sha256.Sum256(packed)
	line := hex.EncodeToString(sum[:]) + "  " + filepath.Base(stem) + "\n"
	if hand {
		require.NoError(t, os.WriteFile(stem+".sha256", []byte(line), 0o600))
	} else {
		text := sha256.Sum256(text.Bytes())
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(hex.EncodeToString(text[:])+"  sprint-epoch0.restore.txt\n"+line), 0o600))
	}
	return []string{stem + ".part-ab", stem + ".part-aa"}, keys
}

// demo load joins the parts in name order, checks the sum beside them, starts
// a Redis (here the one the test's helper starts, standing in for it), loads
// this build's function library, replays the dump and prints where against
// it with the address to point other verbs at; a second load is refused
// naming demo stop, and demo stop stops that Redis by its pid.
func TestDemoLoadReplaysABackupUnderThisBuildsLibrary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parts, keys := demoBackup(t, 3, true)
	root := filepath.Join(t.TempDir(), "demo")
	var srv *testredis.Server
	var dirs []string
	stubDemo(t, root, func(_ context.Context, _, dir string) (demoServer, error) {
		dirs = append(dirs, dir)
		srv = testredis.StartServer(t)
		return demoServer{Addr: srv.Addr(), PID: srv.PID()}, nil
	}, nil, nil)
	// the live store of the caller's environment is never opened: it does not answer
	a := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:1", "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	})
	t.Cleanup(a.close)
	var out, errb bytes.Buffer
	require.Equal(t, 0, a.run(append([]string{"demo", "load", "--dir", root}, parts...), &out, &errb), "%s\n%s", out.String(), errb.String())
	t.Log(out.String())
	require.Len(t, dirs, 1)
	addr := srv.Addr()
	assert.Contains(t, out.String(), fmt.Sprintf("DEMO LOADED parts=2 sha256=checked keys=%d", len(keys)))
	assert.Contains(t, out.String(), "DEMO UP --addr "+addr+": point a read verb at it with --redis "+addr)

	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	source, err := fn.Source()
	require.NoError(t, err)
	code, found, err := fn.Loaded(ctx, c)
	require.NoError(t, err)
	require.True(t, found, "the demo holds no %s library", fn.Library)
	assert.Equal(t, fn.Sum(source), fn.Sum(code), "the demo's library is not this build's")
	assert.Contains(t, out.String(), "library="+fn.Sum(source))
	got, err := c.Keys(ctx, "*").Result()
	require.NoError(t, err)
	sort.Strings(got)
	assert.Equal(t, keys, got)

	// where printed the dump's cards: its stream, the three cards ready
	assert.Regexp(t, `(?m)^demo +\| +0 +\| +3 +\| +0 +\|`, out.String(), "demo load's where does not show the dump's three cards")

	st, err := readDemoState(root)
	require.NoError(t, err)
	assert.Equal(t, demoState{Addr: addr, Port: st.Port, PID: srv.PID(), Dir: dirs[0], Parts: []string{parts[1], parts[0]}, Started: st.Started}, st)
	assert.Equal(t, addr, "127.0.0.1:"+st.Port)

	errb.Reset()
	assert.Equal(t, 1, a.run(append([]string{"demo", "load", "--dir", root}, parts...), &out, &errb))
	assert.Contains(t, errb.String(), "a demo is up at "+addr)
	assert.Contains(t, errb.String(), "run: nova-sprint demo stop --dir "+root)
	assert.Len(t, dirs, 1, "a second load started a Redis")

	out.Reset()
	require.Equal(t, 0, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb), "%s", errb.String())
	assert.Contains(t, out.String(), fmt.Sprintf("DEMO STOPPED addr=%s pid=%d removed=%s", addr, srv.PID(), dirs[0]))
	assert.Error(t, c.Ping(ctx).Err(), "the demo's Redis still answers after demo stop")
	assert.NoDirExists(t, dirs[0])
	assert.DirExists(t, root)
}

// demo stop of a demo that is up stops its Redis and removes only the
// directory it recorded: the sibling directories and files under the root stay.
func TestDemoStopStopsItsRedisAndRemovesOnlyItsDirectory(t *testing.T) {
	t.Parallel()
	parts, _ := demoBackup(t, 2, false)
	root := t.TempDir()
	other := filepath.Join(root, "redis-not-this-one")
	require.NoError(t, os.MkdirAll(other, 0o700))
	keep := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("x"), 0o600))
	var srv *testredis.Server
	stubDemo(t, root, func(context.Context, string, string) (demoServer, error) {
		srv = testredis.StartServer(t)
		return demoServer{Addr: srv.Addr(), PID: srv.PID()}, nil
	}, nil, nil)
	a := newApp(func(string) string { return "" })
	t.Cleanup(a.close)
	var out, errb bytes.Buffer
	require.Equal(t, 0, a.run(append([]string{"demo", "load", "--dir", root}, parts...), &out, &errb), "%s", errb.String())
	assert.Contains(t, out.String(), "sha256=checked", "the parts were not checked against SHA256SUMS")
	st, err := readDemoState(root)
	require.NoError(t, err)
	require.True(t, demoProcessAlive(srv.PID()))
	require.Equal(t, 0, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb), "%s", errb.String())
	c := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = c.Close() })
	assert.Error(t, c.Ping(context.Background()).Err(), "the demo's Redis still answers after demo stop")
	assert.NoDirExists(t, st.Dir)
	assert.DirExists(t, other)
	assert.FileExists(t, keep)
	assert.NoFileExists(t, filepath.Join(root, demoStateFile))
}

// The default starter starts redis-server itself on a loopback port in the
// demo's directory, and demo stop ends it.
func TestDemoLoadStartsItsOwnLoopbackRedis(t *testing.T) {
	t.Parallel()
	testredis.Program(t)
	parts, keys := demoBackup(t, 2, false)
	root := t.TempDir()
	a := newApp(func(string) string { return "" })
	t.Cleanup(a.close)
	var out, errb bytes.Buffer
	code := a.run(append([]string{"demo", "load", "--dir", root}, parts...), &out, &errb)
	st, err := readDemoState(root)
	if err == nil {
		t.Cleanup(func() {
			if demoProcessAlive(st.PID) {
				_ = killDemoRedis(st)
			}
		})
	}
	require.Equal(t, 0, code, "%s\n%s", out.String(), errb.String())
	require.NoError(t, err)
	assert.Contains(t, out.String(), fmt.Sprintf("keys=%d", len(keys)))
	assert.Equal(t, "127.0.0.1:"+st.Port, st.Addr)
	assert.FileExists(t, filepath.Join(st.Dir, "redis.log"))
	require.Equal(t, 0, a.run([]string{"demo", "stop", "--dir", root}, &out, &errb), "%s", errb.String())
	// the started server is this test's child: it is gone once reaped
	assert.Eventually(t, func() bool { return !demoProcessAlive(st.PID) }, demoStopWait, 20*time.Millisecond, "the demo's Redis is still running")
	assert.NoDirExists(t, st.Dir)
}
