package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// blockedFixture writes three waiting tasks in stream s1 (c3, c1, c2 at 300,
// 100, 200) on nova-tools#11 and one (d1) on nova-tools#12 in s2.
func blockedFixture(t *testing.T, c *redis.Client) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	for i, x := range []struct {
		stream, id, on string
		at             int64
	}{{"s1", "c3", "nova-tools#11", 300}, {"s1", "c1", "nova-tools#11", 100}, {"s1", "c2", "nova-tools#11", 200}, {"s2", "d1", "nova-tools#12", 400}} {
		pipe.SAdd(ctx, "ws:names", x.stream)
		pipe.ZAddNX(ctx, "ws:order", redis.Z{Score: float64(i + 1), Member: x.stream})
		pipe.HSet(ctx, "task:"+x.id, "stream", x.stream, "state", "waiting", "created_at", fmt.Sprint(x.at), "blocked_on", x.on)
		pipe.ZAdd(ctx, ws.Key(x.stream, "waiting"), redis.Z{Score: float64(x.at), Member: x.id})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// blockedMirror is <root>/nova-tools.git with a dev branch whose one commit
// lands #12, in t.TempDir(): local git only.
func blockedMirror(t *testing.T) (root, sha string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "nova-tools.git")
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=dev", dir},
		{"-C", dir, "commit", "-q", "--allow-empty", "-m", "Merge #12 stream/x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(out))
}

// TestBlockedVerbs: list prints the waiting sets oldest first per stream;
// resolve releases what landed (#11 by the lander's record, #12 by the
// mirror's base) oldest first into ready at the tasks' own scores.
func TestBlockedVerbs(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr, c := wstest.Start(t)
	blockedFixture(t, c)
	root, sha := blockedMirror(t)

	code, stdout, stderr := runSprint("blocked", "list", "--redis", addr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || stderr != "" || len(lines) != 5 || !strings.HasPrefix(lines[4], "BLOCKED n=4 streams=2 ms=") {
		t.Fatalf("list: exit %d stderr %q\n%s", code, stderr, stdout)
	}
	row := regexp.MustCompile(`^1970-01-01T00:00:00Z (c1|c2|c3|d1) parent=nova-tools#1[12] age=\S+ stream=s[12]$`)
	for i, id := range []string{"c1", "c2", "c3", "d1"} {
		if !row.MatchString(lines[i]) || !strings.Contains(lines[i], " "+id+" ") {
			t.Fatalf("row %d %q, want %s", i, lines[i], id)
		}
	}
	if code, stdout, _ := runSprint("blocked", "list", "--stream", "s2", "--redis", addr); code != 0 || !strings.Contains(stdout, "BLOCKED n=1 streams=1") {
		t.Fatalf("list --stream: %d %s", code, stdout)
	}

	c.HSet(context.Background(), "pr:nova-tools:11", "state", "landed", "head", "0123456789abcdef")
	code, stdout, stderr = runSprint("blocked", "resolve", "--mirror-root", root, "--as", "t", "--redis", addr)
	want := "UNBLOCK s1 c1 parent=nova-tools#11 at 01234567\nUNBLOCK s1 c2 parent=nova-tools#11 at 01234567\n" +
		"UNBLOCK s1 c3 parent=nova-tools#11 at 01234567\nUNBLOCK s2 d1 parent=nova-tools#12 at " + sha[:8] + "\n" +
		"RESOLVED released=4 parked=0 waiting=0 unknown=0 refused=0 ms="
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, want) {
		t.Fatalf("resolve: exit %d stderr %q\n%s\nwant\n%s", code, stderr, stdout, want)
	}
	got, _ := c.ZRangeWithScores(context.Background(), ws.Key("s1", "ready"), 0, -1).Result()
	if len(got) != 3 || got[0].Member != "c1" || got[0].Score != 100 || got[2].Member != "c3" || got[2].Score != 300 {
		t.Fatalf("s1 ready %v", got)
	}
	if code, _, stderr := runSprint("blocked", "nope", "--redis", addr); code != 2 || !strings.Contains(stderr, "want list or resolve") {
		t.Fatalf("unknown subverb: %d %q", code, stderr)
	}
}

// TestBlockedResolveDutyIsRegistered: the reconciler runs the resolve every
// pass (no coordinator step): the registered blocked-resolve duty releases a
// waiting task once its parent lands, and its counts say so.
func TestBlockedResolveDutyIsRegistered(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr, c := wstest.Start(t)
	blockedFixture(t, c)
	root, _ := blockedMirror(t)
	t.Setenv("NOVA_MIRROR_ROOT", root)
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var duty reconcileDuty
	for _, b := range reconcileDuties {
		if b.Name == blockedDutyName {
			if duty, err = b.Build(st); err != nil {
				t.Fatal(err)
			}
		}
	}
	if duty == nil {
		t.Fatalf("no %s duty registered", blockedDutyName)
	}
	counts, err := duty.Run(ctx, nil)
	if err != nil || counts.Released != 1 || counts.Parked != 0 {
		t.Fatalf("first pass %+v %v, want d1 released by the mirror", counts, err)
	}
	if !strings.HasSuffix(counts.Line(), " released=1 parked=0") {
		t.Fatalf("DUTY line %q", counts.Line())
	}
	if counts, err := duty.Run(ctx, nil); err != nil || !counts.Zero() {
		t.Fatalf("idle pass %+v %v", counts, err)
	}
	c.HSet(ctx, "pr:nova-tools:11", "state", "merged")
	if counts, err := duty.Run(ctx, nil); err != nil || counts.Released != 3 {
		t.Fatalf("after the landed move %+v %v", counts, err)
	}
	if n, _ := c.ZCard(ctx, ws.Key("s1", "waiting")).Result(); n != 0 {
		t.Fatalf("s1 waiting %d", n)
	}
}
