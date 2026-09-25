package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/presence"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
	"github.com/redis/go-redis/v9"
)

// nova-tools #3876: watch --store blocks on ev:github for the --pr it names.
// The fact that a pull request moved arrives on the stream the webhook
// receiver writes, so the watch runs no gh and no git fetch at all.

// syncBuf is a buffer the watch goroutine writes while the test reads it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// storeFixture is a throwaway miniredis, a client on it, and a PATH holding
// only a recording gh and a recording git: any gh or git the watch starts is
// a line in the returned calls directory.
func storeFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	t.Setenv(presence.PasswordEnv, "")
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	bin, callDir := t.TempDir(), t.TempDir()
	for _, name := range []string{"gh", "git"} {
		out := filepath.Join(bin, name)
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		if err := testbin.Place(fakePaths["gh"], out); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("NOVA_WAKE_FAKE_GH", callDir)
	return mr, rdb, callDir
}

func xaddEvent(t *testing.T, rdb *redis.Client, repo, number, kind, action string) {
	t.Helper()
	err := rdb.XAdd(context.Background(), &redis.XAddArgs{Stream: ghevent.Stream, Values: []string{
		"repo", repo, "kind", kind, "number", number, "head", "abc123", "action", action,
		"at", "2026-09-25T13:00:00Z", "sender", "rowan-claude", "comment_id", "",
	}}).Err()
	if err != nil {
		t.Fatal(err)
	}
}

// storeWait is the give-up bound for anything this file waits on:
// NOVA_TEST_WAIT when set, thirty seconds otherwise. It is never the product's
// bound; the event is what each test asserts.
func storeWait(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("NOVA_TEST_WAIT=%q: %v", v, err)
		}
		return d
	}
	return 30 * time.Second
}

// waitFor polls out until it holds want, up to storeWait.
func waitFor(t *testing.T, out *syncBuf, want string) {
	t.Helper()
	for end := time.Now().Add(storeWait(t)); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if strings.Contains(out.String(), want) {
			return
		}
	}
	t.Fatalf("never saw %q in:\n%s", want, out.String())
}

// longBlock makes one XREAD BLOCK outlast every wait in the test, so a watch
// that returns did so because Redis served it an entry: nothing polled.
func longBlock(t *testing.T) {
	t.Helper()
	was := storeBlockSlice
	storeBlockSlice = time.Hour
	t.Cleanup(func() { storeBlockSlice = was })
}

// TestWatchStoreWakesOnEvGithubWithNoGhAndNoGit is the DONE-WHEN: a watch
// blocked on one PR, one matching ev:github entry XADDed, and the watch returns
// on that entry -- its one blocking read served by the XADD, with no poll and
// no cadence in between -- having started zero gh and zero git.
func TestWatchStoreWakesOnEvGithubWithNoGhAndNoGit(t *testing.T) {
	mr, rdb, callDir := storeFixture(t)
	longBlock(t)
	// An entry already on the stream before the watch starts is history, not
	// news: the cold cursor is the stream's tip.
	xaddEvent(t, rdb, "mas-bandwidth/nova-tools", "3876", "pull_request", "opened")
	state := filepath.Join(t.TempDir(), "wake.state")

	var out, errb syncBuf
	done := make(chan int, 1)
	go func() {
		done <- runWith([]string{"watch", "--store", mr.Addr(), "--state", state, "--max", "20m",
			"--on-deadline", "report", "--pr", "mas-bandwidth/nova-tools#3876"}, &out, &errb, wake.NewFake(at))
	}()
	waitFor(t, &out, "WAKE at=")
	// A different PR on the same repo, and the same number on another repo,
	// are not this watch's news.
	xaddEvent(t, rdb, "mas-bandwidth/nova-tools", "3877", "pull_request", "synchronize")
	xaddEvent(t, rdb, "mas-bandwidth/rowan-tools", "3876", "pull_request", "synchronize")
	start := time.Now()
	xaddEvent(t, rdb, "mas-bandwidth/nova-tools", "3876", "issue_comment", "created")
	var code int
	select {
	case code = <-done:
	case <-time.After(storeWait(t)):
		t.Fatalf("watch did not return on the matching XADD:\n%s%s", out.String(), errb.String())
	}
	t.Logf("XADD to return: %s", time.Since(start))
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s%s", code, out.String(), errb.String())
	}
	got := out.String()
	if n := countLines(got, "WAKE EVENT "); n != 1 {
		t.Fatalf("want exactly one WAKE EVENT line, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "WAKE EVENT pr=mas-bandwidth/nova-tools#3876 kind=issue_comment action=created") {
		t.Fatalf("the event line does not name the matching entry:\n%s", got)
	}
	// after=0s on the injected clock: no block ran out (one that did moves the
	// hands an hour), so every read was ended by an XADD -- at most one per
	// entry added, never a cadence.
	if !regexp.MustCompile(`(?m)^WAKE CHANGE after=0s polls=[123] prs=1 `).MatchString(got) {
		t.Fatalf("want the verdict from reads the XADDs ended, no poll cycle:\n%s", got)
	}
	if c := calls(t, callDir); len(c) != 0 {
		t.Fatalf("watch --store started gh or git %d times, want zero: %q", len(c), c)
	}
}

// TestWatchStoreCursorSpansCalls: the cursor lives in --state, so an entry
// added between two calls wakes the second one at once rather than being lost
// behind a fresh tip; and an empty stream ends QUIET at the deadline.
func TestWatchStoreCursorSpansCalls(t *testing.T) {
	mr, rdb, callDir := storeFixture(t)
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--store", mr.Addr(), "--state", state, "--max", "1s",
		"--on-deadline", "report", "--pr", "mas-bandwidth/nova-tools#3876"}

	var out, errb bytes.Buffer
	if code := runWith(args, &out, &errb, wake.NewFake(at)); code != 0 || !strings.Contains(out.String(), "WAKE QUIET ") {
		t.Fatalf("an empty stream must end QUIET at the deadline, exit 0; got %d:\n%s%s", code, out.String(), errb.String())
	}
	xaddEvent(t, rdb, "mas-bandwidth/nova-tools", "3876", "pull_request", "closed")
	out.Reset()
	errb.Reset()
	longBlock(t)
	if code := runWith(args, &out, &errb, wake.NewFake(at)); code != 0 || !strings.Contains(out.String(), "action=closed") ||
		!strings.Contains(out.String(), "WAKE CHANGE after=0s polls=1 ") {
		t.Fatalf("the entry added between calls must wake the next one on its first read; got %d:\n%s%s", code, out.String(), errb.String())
	}
	if c := calls(t, callDir); len(c) != 0 {
		t.Fatalf("watch --store started gh or git: %q", c)
	}
	raw, _ := os.ReadFile(state)
	if !strings.Contains(string(raw), storeCursorKey) {
		t.Fatalf("the state holds no %s cursor:\n%s", storeCursorKey, raw)
	}
}

// TestWatchStoreRefusesPollingSources: --store blocks on the stream, so a flag
// that names a polled source or a poll cadence is refused, not ignored.
func TestWatchStoreRefusesPollingSources(t *testing.T) {
	mr, _, _ := storeFixture(t)
	base := []string{"watch", "--store", mr.Addr(), "--state", filepath.Join(t.TempDir(), "s"),
		"--max", "1s", "--on-deadline", "report"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no pr", nil, "--pr"},
		{"forge interval", []string{"--pr", "a/b#1", "--forge-interval", "30s"}, "--forge-interval"},
		{"interval", []string{"--pr", "a/b#1", "--interval", "5s"}, "--interval"},
		{"entry", []string{"--pr", "a/b#1", "--entry", "a/b#2"}, "--entry"},
		{"bad pr", []string{"--pr", "nope"}, "--pr nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := runWith(append(append([]string{}, base...), tc.args...), &out, &errb, wake.Real{})
			if code != 2 || !strings.Contains(errb.String(), tc.want) {
				t.Fatalf("want exit 2 naming %q, got %d:\n%s%s", tc.want, code, out.String(), errb.String())
			}
		})
	}
}
