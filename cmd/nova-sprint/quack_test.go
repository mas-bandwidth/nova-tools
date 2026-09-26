//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const quackBase = "0123456789abcdef0123456789abcdef01234567"

// quackFixture is a throwaway Redis with the library loaded and the sprint
// open; nothing here dials a bench or a mirror (every cut names --base-sha).
func quackFixture(t *testing.T, sprint string) (*redis.Client, string) {
	t.Helper()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.HSet(ctx, "s:"+sprint, "status", "open")
	return client, addr
}

// TestQuackCut is the DONE-WHEN of nova-tools#4307's first half: one verb
// sets the stop, pushes N primaries into the stream's waiting set from the
// template (tiers round-robin, the card's REPO and base-sha on the record)
// and prints the CUT line naming the stop; a second cut skips every id it
// finds with a SKIPPED line and keeps the stop; a sprint nobody opened and a
// repo with no mirror and no --base-sha are refused before anything is
// written.
func TestQuackCut(t *testing.T) {
	t.Parallel()

	const S = "quack-t1"
	client, addr := quackFixture(t, S)
	ctx := context.Background()
	code, out, errOut := runSprint("quack", "cut", "--redis", addr, "--n", "5", "--repo", "mas-bandwidth/quack", "--stream", "quack",
		"--sprint", S, "--tiers", "flash,pro", "--base-sha", quackBase, "--ref", "mas-bandwidth/nova-tools#4232", "--actor", "rowan")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr: %s", code, out, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	for _, want := range []string{"CUT n=5 stream=quack sprint=quack-t1 repo=mas-bandwidth/quack tiers=flash,pro pushed=5 skipped=0 refused=0 base-sha=" + quackBase[:12] + " pitstop=set",
		` lift="nova-sprint quack run --sprint quack-t1" ms=`} {
		if !strings.Contains(last, want) {
			t.Fatalf("CUT line %q lacks %q", last, want)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("a clean cut prints one line, got:\n%s", out)
	}
	if stop, err := pitstop.Read(ctx, client, S); err != nil || !stop.Set || stop.By != "rowan" || !strings.Contains(stop.Why, "cutting 5 quack cards") {
		t.Fatalf("pit stop %+v %v, want set by rowan for the cut", stop, err)
	}
	ids, err := client.ZRange(ctx, "ws:quack:waiting", 0, -1).Result()
	if err != nil || strings.Join(ids, ",") != "quack-001,quack-002,quack-003,quack-004,quack-005" {
		t.Fatalf("ws:quack:waiting=%v %v", ids, err)
	}
	for i, id := range ids {
		tier := []string{"flash", "pro"}[i%2]
		rec := client.HGetAll(ctx, "task:"+id).Val()
		for k, want := range map[string]string{"where": "waiting", "stream": "quack", "sprint": S, "route": tier, "repo": "mas-bandwidth/quack",
			"base": "dev", "base_sha": quackBase, "paths": "docs/fixtures/quack-" + S + "-" + id + ".txt", "title": "quack " + id + " " + tier,
			"ref": "mas-bandwidth/nova-tools#4232", "kind": "fix", "task": id, "source": "quack", "priority": "100"} {
			if rec[k] != want {
				t.Fatalf("task:%s %s=%q, want %q", id, k, rec[k], want)
			}
		}
		if !strings.Contains(rec["body"], "DONE-WHEN: the file docs/fixtures/quack-"+S+"-"+id+".txt exists") || !strings.Contains(rec["done_when"], "\"quack "+S+" "+id+"\"") {
			t.Fatalf("task:%s body or done_when lacks the fixture line: %q", id, rec["done_when"])
		}
	}

	// The same cut again: every id exists, each is SKIPPED, the stop is held.
	code, out, _ = runSprint("quack", "cut", "--redis", addr, "--n", "6", "--repo", "mas-bandwidth/quack", "--stream", "quack",
		"--sprint", S, "--base-sha", quackBase, "--actor", "rowan")
	if code != 0 || strings.Count(out, "SKIPPED id=quack-00") != 5 || !strings.Contains(out, "SKIPPED id=quack-003 why=exists\n") {
		t.Fatalf("second cut: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "CUT n=6 stream=quack sprint=quack-t1 repo=mas-bandwidth/quack tiers=flash,pro pushed=1 skipped=5 refused=0 base-sha="+quackBase[:12]+" pitstop=held") {
		t.Fatalf("second CUT line:\n%s", out)
	}
	if n := client.ZCard(ctx, "ws:quack:waiting").Val(); n != 6 {
		t.Fatalf("waiting=%d, want 6", n)
	}

	// Refusals write nothing.
	code, out, _ = runSprint("quack", "cut", "--redis", addr, "--n", "2", "--repo", "mas-bandwidth/quack", "--stream", "quack",
		"--sprint", "quack-nobody-opened", "--base-sha", quackBase, "--actor", "rowan")
	if code != 1 || !strings.HasPrefix(out, "CUT REFUSED sprint=quack-nobody-opened why=sprint-unknown remedy=") || client.Exists(ctx, "task:quack-001").Val() != 1 || client.Exists(ctx, "s:quack-nobody-opened:pitstop").Val() != 0 {
		t.Fatalf("unknown sprint: exit %d %q", code, out)
	}
	code, out, _ = runSprint("quack", "cut", "--redis", addr, "--n", "2", "--repo", "example/no-such-mirror-4307", "--stream", "quack2",
		"--sprint", S, "--actor", "rowan")
	if code != 1 || !strings.HasPrefix(out, "CUT REFUSED sprint=quack-t1 repo=example/no-such-mirror-4307 why=") || !strings.Contains(out, "remedy=\"pass --base-sha <sha40>, or nova-sprint mirror refresh") || client.Exists(ctx, "ws:quack2:waiting").Val() != 0 {
		t.Fatalf("no mirror, no --base-sha: exit %d %q", code, out)
	}
	for _, args := range [][]string{
		{"--redis", addr, "--n", "0", "--repo", "o/r", "--stream", "q", "--sprint", S, "--base-sha", quackBase, "--actor", "a"},
		{"--redis", addr, "--n", "2", "--repo", "o/r", "--stream", "q", "--sprint", S, "--base-sha", quackBase, "--actor", "a", "--tiers", "max"},
		{"--redis", addr, "--n", "2", "--repo", "norepo", "--stream", "q", "--sprint", S, "--base-sha", quackBase, "--actor", "a"},
		{"--redis", addr, "--n", "2", "--repo", "o/r", "--stream", "q", "--sprint", "Bad Name", "--base-sha", quackBase, "--actor", "a"},
		{"--redis", addr, "--n", "2", "--repo", "o/r", "--stream", "q", "--sprint", S, "--base-sha", quackBase, "--actor", "a", "extra"},
	} {
		if code, _, errOut := runSprint(append([]string{"quack", "cut"}, args...)...); code != 2 || !strings.HasPrefix(errOut, "nova-sprint quack cut: ") {
			t.Fatalf("%v: exit %d %q, want usage", args, code, errOut)
		}
	}
	if code, _, errOut := runSprint("quack", "probe"); code != 2 || !strings.Contains(errOut, "want cut or run") {
		t.Fatalf("unknown subverb: %d %q", code, errOut)
	}
}

// TestQuackRun is the second half: the benches named by --slots get their
// slots through the capacity path (a bench with no recorded machine is
// refused naming the capacity verb), the pit stop is lifted, and a run with
// no stop to lift says so.
func TestQuackRun(t *testing.T) {
	t.Parallel()

	const S = "quack-t2"
	client, addr := quackFixture(t, S)
	ctx := context.Background()
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)
	client.SAdd(ctx, "benches", "hetzner", "hulk")
	client.HSet(ctx, "bench:hetzner:desired", "slots", 0, "machine", "m")
	client.HSet(ctx, "bench:hulk:desired", "slots", 0, "machine", "m")
	if _, err := pitstop.Set(ctx, client, S, "rowan", "quack cut: cutting 2 quack cards into quack", false, ""); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runSprint("quack", "run", "--redis", addr, "--sprint", S, "--slots", "hulk=16,hetzner=8,ghost=2", "--actor", "rowan")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (ghost refused)\n%s%s", code, out, errOut)
	}
	for _, want := range []string{
		"SLOTS REFUSED bench=ghost slots=2 why=no-machine remedy=\"nova-sprint capacity bench --as rowan --machine <m> ghost 2\"\n",
		"SLOTS SET bench=hetzner machine=m slots=8 desired=8/40\n",
		"SLOTS SET bench=hulk machine=m slots=16 desired=24/40\n",
		"PITSTOP CLEAR sprint=quack-t2 by=rowan at=",
		" was_by=rowan was_why=\"quack cut: cutting 2 quack cards into quack\"\n",
		"QUACK RUN sprint=quack-t2 benches=2 refused=1 pitstop=lifted ms=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if stop, _ := pitstop.Read(ctx, client, S); stop.Set {
		t.Fatalf("stop still set: %+v", stop)
	}
	if got := client.HGet(ctx, "bench:hulk:desired", "slots").Val(); got != "16" {
		t.Fatalf("hulk slots=%q", got)
	}

	code, out, _ = runSprint("quack", "run", "--redis", addr, "--sprint", S, "--slots", "hulk=16", "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "SLOTS SAME bench=hulk machine=m slots=16") || !strings.Contains(out, "PITSTOP NONE sprint=quack-t2\n") || !strings.Contains(out, "QUACK RUN sprint=quack-t2 benches=1 refused=0 pitstop=none") {
		t.Fatalf("second run: exit %d\n%s", code, out)
	}
	for _, args := range [][]string{
		{"--redis", addr, "--actor", "a"},
		{"--redis", addr, "--sprint", S, "--actor", "a", "--slots", "hulk"},
		{"--redis", addr, "--sprint", S, "--actor", "a", "--slots", "hulk=-1"},
		{"--redis", addr, "--sprint", S, "--actor", "a", "--slots", "hulk=1,hulk=2"},
	} {
		if code, _, errOut := runSprint(append([]string{"quack", "run"}, args...)...); code != 2 || !strings.HasPrefix(errOut, "nova-sprint quack run: ") {
			t.Fatalf("%v: exit %d %q, want usage", args, code, errOut)
		}
	}
}
