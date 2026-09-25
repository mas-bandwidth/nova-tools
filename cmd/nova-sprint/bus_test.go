package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

func busFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c
}

func runBusVerb(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(append([]string{"bus"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

var postedRE = regexp.MustCompile(`^POSTED to=(\w+) id=(\d+-\d+)\n$`)

// TestBusVerbsPostReadReplyLs is the verbs end to end: post prints POSTED
// with the id, read prints the header, the indented body and one READ
// receipt and acks, reply goes back to the sender with re set, ls counts.
func TestBusVerbsPostReadReplyLs(t *testing.T) {
	addr, c := busFixture(t)
	code, out, errOut := runBusVerb("post", "--redis", addr, "--as", "emma", "--to", "rowan", "--kind", "done",
		"--subject", "PR up", "--body", "#3865 opened\nCI green", "--ref", "nova-tools#3865")
	m := postedRE.FindStringSubmatch(out)
	if code != 0 || m == nil || m[1] != "rowan" || errOut != "" {
		t.Fatalf("post: %d %q %q", code, out, errOut)
	}
	id := m[2]

	code, out, _ = runBusVerb("ls", "--redis", addr)
	if code != 0 || !strings.Contains(out, "bus:rowan len=1 unread=1\n") || !strings.HasSuffix(out, "BUS inboxes=6 unread=1\n") {
		t.Fatalf("ls: %d %q", code, out)
	}

	code, out, _ = runBusVerb("pending", "--redis", addr, "--as", "rowan")
	if code != 0 || out != "PENDING as=rowan n=0\n" {
		t.Fatalf("pending: %d %q", code, out)
	}

	code, out, errOut = runBusVerb("read", "--redis", addr, "--as", "rowan")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 0 || len(lines) != 4 || !strings.HasPrefix(lines[0], id+" ") ||
		!strings.HasSuffix(lines[0], ` emma done "PR up" ref=nova-tools#3865`) ||
		lines[1] != "  #3865 opened" || lines[2] != "  CI green" || lines[3] != "READ as=rowan n=1" {
		t.Fatalf("read: %d %q %q", code, out, errOut)
	}
	if code, out, _ = runBusVerb("read", "--redis", addr, "--as", "rowan"); code != 0 || out != "READ as=rowan n=0\n" {
		t.Fatalf("second read: %d %q", code, out)
	}

	body := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(body, []byte("merged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runBusVerb("reply", "--redis", addr, "--as", "rowan", "--re", id, "--body-file", body)
	m = postedRE.FindStringSubmatch(out)
	if code != 0 || m == nil || m[1] != "emma" {
		t.Fatalf("reply: %d %q %q", code, out, errOut)
	}
	got := c.XRange(context.Background(), "bus:emma", m[2], m[2]).Val()
	if len(got) != 1 || got[0].Values["re"] != id || got[0].Values["kind"] != "answer" || got[0].Values["subject"] != "re: PR up" || got[0].Values["from"] != "rowan" {
		t.Fatalf("reply entry %+v", got)
	}
}

// TestBusVerbRefusals: refusals exit 1 with one REFUSED line naming the
// remedy and write nothing; usage exits 2.
func TestBusVerbRefusals(t *testing.T) {
	addr, c := busFixture(t)
	cases := []struct {
		args   []string
		code   int
		substr string
	}{
		{[]string{"post", "--redis", addr, "--as", "emma", "--to", "rowan", "--kind", "done", "--subject", "s", "--body", "SCORE who=emma 10/10"}, 1, "remedy: record the read with nova-sprint read post"},
		{[]string{"post", "--redis", addr, "--as", "emma", "--to", "rowan", "--kind", "note", "--subject", "s", "--body", strings.Repeat("y", 16385)}, 1, "remedy: put it in a task record or a PR"},
		{[]string{"post", "--redis", addr, "--as", "emma", "--to", "bob", "--kind", "note", "--subject", "s", "--body", "x"}, 1, "--to bob is not an inbox"},
		{[]string{"reply", "--redis", addr, "--as", "rowan", "--re", "1-1", "--body", "x"}, 1, "is in neither bus:rowan nor bus:all"},
		{[]string{"post", "--redis", addr, "--as", "emma", "--to", "rowan", "--kind", "note", "--subject", "s"}, 2, "--body-file"},
		{[]string{"post", "--redis", addr, "--as", "emma", "--to", "rowan", "--kind", "note", "--subject", "s", "--body", "x", "--body-file", "f"}, 2, "--body-file"},
		{[]string{"reply", "--redis", addr, "--as", "rowan", "--re", "1-1", "--to", "emma", "--body", "x"}, 2, "no --to"},
		{[]string{"read", "--redis", addr}, 2, "--as"},
		{[]string{"tail", "--redis", addr, "--as", "rowan", "--all"}, 2, "belong to bus read"},
		{[]string{"shout"}, 2, "unknown subverb shout"},
	}
	for _, tc := range cases {
		code, out, errOut := runBusVerb(tc.args...)
		if code != tc.code || out != "" || !strings.Contains(errOut, tc.substr) || strings.Count(errOut, "\n") != 1 {
			t.Errorf("bus %s: %d %q %q; want %d naming %q", tc.args[0], code, out, errOut, tc.code, tc.substr)
		}
	}
	if n := c.Exists(context.Background(), "bus:rowan", "bus:emma", "bus:sent:emma").Val(); n != 0 {
		t.Fatalf("refusals wrote %d bus keys", n)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestBusTailPrintsWithinOneSecond is the DONE-WHEN arm: `bus tail --as
// rowan` prints a posted entry, one line with its body, as it arrives, and a
// signal ends it. The waits class asserts the event, not the clock: the
// latency is logged, the wake-up on the XADD itself is
// internal/nsprint/bus's TestBusTailDeliversAsItArrives, and a signal is seen
// within one blocking read, busTailBlock, at most one second.
func TestBusTailPrintsWithinOneSecond(t *testing.T) {
	if busTailBlock > time.Second {
		t.Fatalf("busTailBlock is %v; a signal would wait longer than one second", busTailBlock)
	}
	addr, _ := busFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut lockedBuf
	done := make(chan int, 1)
	go func() { done <- runBusTail(ctx, []string{"--redis", addr, "--as", "rowan"}, &out, &errOut) }()
	code, posted, _ := runBusVerb("post", "--redis", addr, "--as", "stella", "--to", "rowan", "--kind", "question", "--subject", "base?", "--body", "dev or main")
	m := postedRE.FindStringSubmatch(posted)
	if code != 0 || m == nil {
		t.Fatalf("post: %d %q", code, posted)
	}
	at := time.Now()
	giveUp := at.Add(busTestWait())
	for !strings.Contains(out.String(), m[2]) {
		if time.Now().After(giveUp) {
			t.Fatalf("tail printed %q; want %s", out.String(), m[2])
		}
		time.Sleep(5 * time.Millisecond) // wall-ok: polling a condition in a test
	}
	t.Logf("BUS TAIL printed_ms=%.2f", float64(time.Since(at).Microseconds())/1000)
	line := out.String()
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, ` stella question "base?" body="dev or main"`+"\n") {
		t.Fatalf("tail line %q", line)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 || errOut.String() != "" {
			t.Fatalf("tail exit %d %q", code, errOut.String())
		}
	case <-time.After(busTestWait()):
		t.Fatal("tail did not stop on its signal")
	}
}

// busTestWait is a test's give-up, never a product bound: NOVA_TEST_WAIT, else 30 s.
func busTestWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}
