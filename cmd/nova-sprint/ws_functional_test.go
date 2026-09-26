//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// receiptMS is the ms=<n> field of a receipt line.
func receiptMS(line string) string {
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, "ms=") {
			return strings.TrimPrefix(f, "ms=")
		}
	}
	return "?"
}

// TestWSVerbsOnAThousandTasks is the #3659/#3660 DONE-WHEN for the verbs: on
// 1,000 tasks across 10 streams every ws, scope and stream verb exits 0 with
// its one receipt line, no command runs a second on the server (SLOWLOG armed
// at one second: the rule is the server's clock, not a test wall bound), and
// the invariants hold after each. The receipts' ms are logged.
func TestWSVerbsOnAThousandTasks(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	cpDir := t.TempDir()
	t.Setenv("NOVA_SPRINT_CHECKPOINT_DIR", cpDir)
	t.Setenv("FRIEND_QUEUE_SPRINT", "")
	addr, c := wstest.Start(t)
	ids := wstest.Fixture(t, c, 1000, 10)
	ctx := context.Background()
	if err := c.ConfigSet(ctx, "slowlog-log-slower-than", "1000000").Err(); err != nil {
		t.Fatal(err)
	}
	c.SlowLogReset(ctx)
	s := wstest.StreamName
	idsFile := filepath.Join(t.TempDir(), "ids")
	if err := os.WriteFile(idsFile, []byte("t00001\nt00011 # a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(t.TempDir(), "cp.tsv")
	for _, tc := range []struct {
		args  []string
		want  string
		lines int
	}{
		{[]string{"ws", "counts"}, `COUNTS sprint=- streams=10 waiting=400 ready=200 working=150 review=0 merging=100 landed=100 parked=0 total=950 done=100/950 pct=10 left=850 eta="?" `, 1},
		{[]string{"stream", "ls"}, "STREAMS n=10 ", 11},
		{[]string{"scope", "keep", "--streams", s(0) + "|" + s(1)}, "KEPT streams=2 parked_streams=8 parked=480 checkpoint=" + cpDir + "/ws-", 1},
		{[]string{"scope", "ls"}, "SCOPE streams=10 kept=2 parked=8 partial=0 ", 11},
		{[]string{"scope", "unpark", "--stream", s(2)}, `UNPARKED stream="s2: work" unparked=60 `, 1},
		{[]string{"scope", "park", "--stream", s(1), "--ids", "@" + idsFile, "--checkpoint", cp}, `PARKED stream="s1: work" parked=2 same=0 checkpoint=` + cp + " rows=1000 ", 1},
		{[]string{"scope", "park", "--stream", s(0)}, `PARKED stream="s0: work" parked=60 `, 1},
		{[]string{"stream", "rename", s(4), "swarm: cards"}, `RENAMED from="s4: work" to="swarm: cards" members=100 `, 1},
		{[]string{"stream", "order", "swarm: cards", s(9)}, `ORDERED streams=10 first="swarm: cards" `, 1},
		// 1,002 rows: the rename registered "swarm: cards" through the one
		// move and stream order registered s9, each creating its sentinel
		// (#4318); the other fixture streams were written straight into the
		// keys and have none until task fsck --repair.
		{[]string{"ws", "checkpoint", "--out", cp}, "CHECKPOINT path=" + cp + " streams=10 rows=1002 ", 1},
	} {
		args := append(append([]string{}, tc.args...), "--redis", addr)
		if tc.args[1] == "rename" || tc.args[1] == "order" {
			args = append([]string{tc.args[0], tc.args[1], "--redis", addr}, tc.args[2:]...)
		}
		code, stdout, stderr := runSprint(args...)
		lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
		last := lines[len(lines)-1]
		if code != 0 || stderr != "" || len(lines) != tc.lines || !strings.HasPrefix(last, tc.want) {
			t.Fatalf("%v: exit %d, %d lines, receipt %q, stderr %q; want %q", tc.args, code, len(lines), last, stderr, tc.want)
		}
		t.Logf("%-16s ms=%s", strings.Join(tc.args[:2], " "), receiptMS(last))
		if err := ws.Check(ctx, c, ids); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
	logs, _ := c.SlowLogGet(ctx, 16).Result()
	for _, l := range logs {
		t.Errorf("over one second on the server: %v took %v", l.Args, l.Duration)
	}
	if n, _ := filepath.Glob(filepath.Join(cpDir, "ws-*.tsv")); len(n) != 2 {
		t.Fatalf("default checkpoints %v, want one each for scope keep and scope park", n)
	}
	if got, _ := c.Get(ctx, ws.CheckpointKey).Result(); !strings.Contains(got, "path="+cp+" rows=1002") {
		t.Fatalf("ws:checkpoint %q", got)
	}
}

func TestWSVerbRefusals(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_SPRINT_CHECKPOINT_DIR", t.TempDir())
	addr, c := wstest.Start(t)
	wstest.Fixture(t, c, 100, 10)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"scope", "keep", "--redis", addr, "--streams", "nope|" + wstest.StreamName(0)}, 1, "REFUSED unknown stream nope"},
		{[]string{"scope", "unpark", "--redis", addr, "--stream", "nope"}, 1, "REFUSED unknown stream nope"},
		{[]string{"stream", "rename", "--redis", addr, wstest.StreamName(0), wstest.StreamName(1)}, 1, "REFUSED stream s1: work exists"},
		{[]string{"stream", "order", "--redis", addr, "nope"}, 1, "REFUSED unknown stream nope"},
		{[]string{"scope", "park", "--redis", addr, "--stream", wstest.StreamName(1), "--ids", "t00000"}, 1, "REFUSED 1 of 1 ids are not in stream"},
	} {
		code, stdout, stderr := runSprint(tc.args...)
		if code != tc.code || !strings.HasPrefix(stdout, tc.want) || strings.Count(stdout, "\n") != 1 {
			t.Errorf("%v: exit %d stdout %q stderr %q; want %d %q", tc.args, code, stdout, stderr, tc.code, tc.want)
		}
	}
	if st, _ := c.HGet(context.Background(), "task:t00000", "state").Result(); st != "waiting" {
		t.Fatalf("a refused park moved t00000 to %s", st)
	}
}

// TestWSShowOrder (#4318): `ws show --order` and `stream order --show` print
// every stream's cards in order with their edges and the sentinel last, then
// one receipt; --stream narrows to one stream and refuses an unknown name
// with the names it has; ws show without --order names the remedy.
func TestWSShowOrder(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	push := func(id, stream, on string) {
		t.Helper()
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: stream, Kind: "build",
			Title: "STREAM: " + stream + " | " + id, By: "test", DependsOn: on}); err != nil {
			t.Fatal(err)
		}
	}
	push("A", "swarm: cards", "")
	push("B", "swarm: cards", "A")
	push("C", "ci", "swarm-cards:sentinel")
	want := `STREAM 1 "swarm: cards" cards=3 live=2 landed=0 sentinel=waiting
  waiting A
  waiting B <- A(waiting)
  waiting swarm-cards:sentinel <- every other card of the stream (live 2)
STREAM 2 "ci" cards=2 live=1 landed=0 sentinel=waiting
  waiting C <- swarm-cards:sentinel(waiting)
  waiting ci:sentinel <- every other card of the stream (live 1)
SHOW streams=2 cards=5 edges=5 ms=`
	for _, args := range [][]string{{"ws", "show", "--redis", addr, "--order"}, {"stream", "order", "--redis", addr, "--show"}} {
		code, stdout, stderr := runSprint(args...)
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, want) {
			t.Fatalf("%v: exit %d\n%s%s\nwant\n%s", args, code, stdout, stderr, want)
		}
	}
	code, stdout, _ := runSprint("ws", "show", "--redis", addr, "--order", "--stream", "ci")
	if code != 0 || !strings.HasPrefix(stdout, "STREAM 2 \"ci\" cards=2") || !strings.Contains(stdout, "SHOW streams=1 cards=2 edges=2 ms=") {
		t.Fatalf("--stream ci: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = runSprint("ws", "show", "--redis", addr, "--order", "--stream", "nope")
	if code != 1 || !strings.HasPrefix(stdout, `REFUSED no stream "nope"; the index has "swarm: cards" "ci": nova-sprint stream ls --redis <addr> ms=`) {
		t.Fatalf("--stream nope: exit %d %q", code, stdout)
	}
	code, _, stderr := runSprint("ws", "show", "--redis", addr)
	if code != 2 || !strings.Contains(stderr, "want --order: ws show --redis <addr> --order [--stream <s>]") {
		t.Fatalf("no --order: exit %d %q", code, stderr)
	}
	code, _, stderr = runSprint("stream", "order", "--redis", addr, "--show", "ci")
	if code != 2 || !strings.Contains(stderr, "--show lists the streams; it ranks nothing") {
		t.Fatalf("--show with names: exit %d %q", code, stderr)
	}
}
