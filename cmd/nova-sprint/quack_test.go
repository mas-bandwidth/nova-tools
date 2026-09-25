package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const quackBase = "0123456789abcdef0123456789abcdef01234567"

// quackFixture is a throwaway Redis with the library loaded, two registered
// benches and a local mirror so the card's repo probe never leaves the host.
// Nothing here dials a bench: the fake bench below writes the stage fields.
func quackFixture(t *testing.T) (*redis.Client, string) {
	t.Helper()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "benches", "b1", "b2").Err(); err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirror, "nova-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", mirror)
	return client, addr
}

// fakeBench replays the stage events of a quack sprint onto the card records,
// as the dealer, the bench wrapper, harvest and pr-to-read write them: once
// every probe card is pushed it stamps dealt_at +2 s, launched_at +3 s,
// ended_at +end s, harvested_at +50 s (with pr and head) and a first read
// task queued at +60 s, each from the card's own cut_at. A card in stall
// stops at ended: harvest never comes.
func fakeBench(t *testing.T, client *redis.Client, sprint string, labels []string, end int64, stall string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			pipe := client.Pipeline()
			cuts := make([]*redis.StringCmd, len(labels))
			for i, l := range labels {
				cuts[i] = pipe.HGet(ctx, "s:"+sprint+":card:"+l, "cut_at")
			}
			_, _ = pipe.Exec(ctx)
			ready := true
			for _, c := range cuts {
				ready = ready && c.Val() != ""
			}
			if !ready {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			for i, l := range labels {
				cut, _ := strconv.ParseInt(cuts[i].Val(), 10, 64)
				base := cut * 1000
				key := "s:" + sprint + ":card:" + l
				ms := func(s int64) string { return strconv.FormatInt(base+s*1000, 10) }
				client.HSet(ctx, key, "dealt_at", ms(2), "launched_at", ms(3), "ended_at", ms(end), "outcome", "DONE")
				if l == stall {
					continue
				}
				pr, head := strconv.Itoa(9000+i), strings.Repeat(strconv.Itoa(i%10), 40)
				client.HSet(ctx, key, "harvested_at", ms(50), "pr", pr, "head", head)
				id := "read-" + pr + "-" + head[:8]
				client.HSet(ctx, "s:"+sprint+":reads:mas-bandwidth/nova-tools#"+pr+"@"+head, "emma", id)
				client.HSet(ctx, "task:"+id, "created_at", time.UnixMilli(base+60000).UTC().Format(time.RFC3339))
			}
			return
		}
	}()
}

func quackLabels() []string {
	return []string{card.QuackLabel("b1", "pro"), card.QuackLabel("b1", "flash"), card.QuackLabel("b2", "pro"), card.QuackLabel("b2", "flash")}
}

func quackRows(out string) []string {
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, "b1 ") || strings.HasPrefix(l, "b2 ") {
			rows = append(rows, l)
		}
	}
	return rows
}

// TestQuack is the DONE-WHEN of nova-tools#3648: on a fixture store that
// replays stage events for two benches x two tiers with one flash card
// stalled at harvest, the verb prints four rows, the stalled one FAIL harvest
// after the timeout, and exits 1.
func TestQuack(t *testing.T) {
	client, addr := quackFixture(t)
	fakeBench(t, client, "quack-t1", quackLabels(), 40, card.QuackLabel("b2", "flash"))
	code, out, errOut := runSprint("quack", "--redis", addr, "--sprint", "quack-t1", "--benches", "b1,b2",
		"--tiers", "pro,flash", "--base-sha", quackBase, "--timeout", "1500ms", "--tick", "50ms")
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr: %s", code, out, errOut)
	}
	t.Logf("the probe table:\n%s", out)
	rows := quackRows(out)
	if len(rows) != 4 {
		t.Fatalf("%d rows, want 4:\n%s", len(rows), out)
	}
	for _, r := range rows {
		stalled := strings.HasPrefix(r, "b2 flash ")
		switch {
		case stalled && !strings.Contains(r, " FAIL harvest"):
			t.Fatalf("stalled row %q, want FAIL harvest", r)
		case stalled && !strings.Contains(r, "end=40 harvest=- read=-"):
			t.Fatalf("stalled row %q, want its stages to end and nothing after", r)
		case !stalled && !strings.HasSuffix(r, " PASS"):
			t.Fatalf("row %q, want PASS", r)
		case !stalled && !strings.Contains(r, "push=0 deal=2 launch=3 end=40 harvest=50 read=60"):
			t.Fatalf("row %q, want the replayed stage seconds", r)
		}
	}
	if !strings.Contains(out, "QUACK sprint=quack-t1 rows=4 pass=3 fail=1 timed_out=true") {
		t.Fatalf("no receipt:\n%s", out)
	}
	ctx := context.Background()
	for _, b := range []string{"b1", "b2"} {
		for _, tier := range []string{"pro", "flash"} {
			rec := client.HMGet(ctx, "s:quack-t1:card:"+card.QuackLabel(b, tier), "bench", "route").Val()
			if rec[0] != b || rec[1] != tier {
				t.Fatalf("card %s bench=%v route=%v, want pinned to its bench and tier", card.QuackLabel(b, tier), rec[0], rec[1])
			}
		}
	}
	if st, _ := client.HGet(ctx, "s:quack-t1", "status").Result(); st != "open" {
		t.Fatalf("sprint status %q, want open (a fresh sprint the dealer deals)", st)
	}
}

// TestQuackPassAndBars: every card through every stage exits 0 without
// waiting for the timeout; cfg:quack's bars override the defaults, a stage
// over its bar is FAIL <stage> over, a malformed bar is refused before any
// write, and a sprint that already exists is refused (fresh only).
func TestQuackPassAndBars(t *testing.T) {
	client, addr := quackFixture(t)
	ctx := context.Background()
	fakeBench(t, client, "quack-t2", quackLabels(), 40, "")
	code, out, errOut := runSprint("quack", "--redis", addr, "--sprint", "quack-t2", "--benches", "b1,b2",
		"--base-sha", quackBase, "--timeout", "20s", "--tick", "50ms")
	if code != 0 || len(quackRows(out)) != 4 || !strings.Contains(out, "pass=4 fail=0 timed_out=false") || !strings.Contains(out, "bars=default") {
		t.Fatalf("exit %d, want 0 and four PASS rows\nstdout:\n%s\nstderr: %s", code, out, errOut)
	}
	if code, out, _ := runSprint("quack", "--redis", addr, "--sprint", "quack-t2", "--benches", "b1", "--base-sha", quackBase); code != 1 || !strings.Contains(out, "why=sprint-not-fresh") {
		t.Fatalf("reused sprint: exit %d %q, want refused not fresh", code, out)
	}

	client.HSet(ctx, card.QuackBarsKey, "end", "30")
	fakeBench(t, client, "quack-t3", quackLabels(), 40, "")
	code, out, _ = runSprint("quack", "--redis", addr, "--sprint", "quack-t3", "--benches", "b1,b2",
		"--base-sha", quackBase, "--timeout", "20s", "--tick", "50ms")
	if code != 1 || strings.Count(out, "FAIL end over bar=30s") != 4 || !strings.Contains(out, "bars=cfg:quack") {
		t.Fatalf("end bar 30 s: exit %d, want 1 and four FAIL end rows\n%s", code, out)
	}

	client.HSet(ctx, card.QuackBarsKey, "read", "soon")
	code, out, _ = runSprint("quack", "--redis", addr, "--sprint", "quack-t4", "--benches", "b1", "--base-sha", quackBase)
	if code != 1 || !strings.Contains(out, "REFUSED quack") || client.Exists(ctx, "s:quack-t4").Val() != 0 {
		t.Fatalf("malformed bar: exit %d %q, want refused before the sprint is written", code, out)
	}
	if code, _, errOut := runSprint("quack", "--redis", addr, "--benches", "b1", "--tiers", "max", "--base-sha", quackBase); code != 2 || !strings.Contains(errOut, "not pro or flash") {
		t.Fatalf("tier max: exit %d %q, want usage", code, errOut)
	}
}
