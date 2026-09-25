package harvest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
)

// TestHarvestTwoBenchesRecordAndBody (#2932): two benches harvest in parallel
// under two racing workers without a double push; the PR body carries the
// card's BASE, base-sha, STREAM and DONE-WHEN lines; the PR record
// pr:<repo>:<n> is written by the harvest; and a second pass opens no second
// PR, pushes nothing and reads nothing from GitHub. The origin is a local
// bare repository and the push goes through the fake ssh of the zsh control.
func TestHarvestTwoBenchesRecordAndBody(t *testing.T) {
	t.Parallel()

	for _, bin := range []string{"bash", "git", "perl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s unavailable", bin)
		}
	}
	c := startRedis(t)
	forge := newZshForge()
	alpha := newZshBenchOn(t, c, forge, "alpha", "card-alpha")
	bravo := newZshBenchOn(t, c, forge, "bravo", "card-bravo")
	benches := []string{alpha.bench, bravo.bench}
	ctx := context.Background()

	// One fake ssh (one log) for both benches: the remote command line names
	// the branch, so the log says which bench pushed what.
	run := func(instance string) []harvest.BenchResult {
		return harvest.Run(ctx, alpha.st, harvest.Options{
			Sprint: alpha.sprint, Benches: benches, Clock: time.Minute, Instance: instance,
			Forge: forge, Pusher: harvest.SSHPusher{SSH: alpha.ssh, Remote: originOf},
		})
	}
	pushes := func(branch string) int {
		log, _ := os.ReadFile(alpha.sshLog)
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
			if strings.Contains(line, branch) {
				n++
			}
		}
		return n
	}

	t.Run("two-benches-parallel-no-double-push", func(t *testing.T) {
		// Two workers race over both benches: each bench's lease admits one,
		// so every card is pushed once and harvested once, whichever wins.
		var wg sync.WaitGroup
		results := make([][]harvest.BenchResult, 2)
		for i, instance := range []string{"worker-1", "worker-2"} {
			wg.Add(1)
			go func(i int, instance string) {
				defer wg.Done()
				results[i] = run(instance)
			}(i, instance)
		}
		wg.Wait()
		harvested := 0
		for _, res := range results {
			for _, r := range res {
				if r.Err != nil && !strings.Contains(r.Err.Error(), harvest.ErrLeaseHeld.Error()) {
					t.Fatalf("%s: %v", r.Bench, r.Err)
				}
				if len(r.Failed) != 0 {
					t.Fatalf("%s failed: %+v", r.Bench, r.Failed)
				}
				harvested += len(r.Cards)
			}
		}
		if harvested != 2 || forge.creates != 2 {
			t.Fatalf("harvested %d cards, %d creates; want 2 and 2 across both workers", harvested, forge.creates)
		}
		if forge.reads != 0 {
			t.Fatalf("GitHub read %d times; the head comes from the create reply and then the record", forge.reads)
		}
		for _, z := range []*zshBench{alpha, bravo} {
			z.harvested(2) // reads the forge once itself, as its own check
			if n := pushes(z.branch); n != 1 {
				t.Fatalf("%s pushed %d times, want once", z.branch, n)
			}
		}
	})

	t.Run("body-carries-stream-and-done-when", func(t *testing.T) {
		for _, z := range []*zshBench{alpha, bravo} {
			// The typed body starts with BASE: (#3712): read it as lines.
			body := "\n" + forge.bodies[z.branch]
			for _, want := range []string{
				"\nBASE: dev\n", "\nbase-sha: 09fbedc9\n", "\nSTREAM: nova-sprint\n",
				"\nDONE-WHEN: the branch " + z.branch + " is on origin and one PR names it\n",
				"\npushed_sha: " + z.sha + "\n",
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("PR body for %s lacks %q:\n%s", z.branch, strings.TrimSpace(want), body)
				}
			}
		}
	})

	t.Run("second-pass-opens-nothing", func(t *testing.T) {
		before := map[string]string{}
		for _, z := range []*zshBench{alpha, bravo} {
			before[z.label] = fmt.Sprint(z.card())
			n := z.card()["pr"]
			before["rec:"+z.label] = fmt.Sprint(c.HGetAll(ctx, "pr:nova-tools:"+n).Val())
		}
		logLen := c.XLen(ctx, "s:"+alpha.sprint+":log").Val()
		lookups, reads := forge.lookups, forge.reads
		for _, r := range run("worker-3") {
			if r.Err != nil || len(r.Cards)+len(r.Failed) != 0 {
				t.Fatalf("second pass %s = %+v; want n=0", r.Bench, r)
			}
		}
		if forge.creates != 2 || forge.lookups != lookups || forge.reads != reads {
			t.Fatalf("second pass: creates %d lookups %d reads %d; want no GitHub at all", forge.creates, forge.lookups-lookups, forge.reads-reads)
		}
		if c.XLen(ctx, "s:"+alpha.sprint+":log").Val() != logLen {
			t.Fatal("second pass appended to s:<S>:log")
		}
		for _, z := range []*zshBench{alpha, bravo} {
			if fmt.Sprint(z.card()) != before[z.label] {
				t.Fatalf("second pass wrote to %s", z.label)
			}
			n := z.card()["pr"]
			if fmt.Sprint(c.HGetAll(ctx, "pr:nova-tools:"+n).Val()) != before["rec:"+z.label] {
				t.Fatalf("second pass rewrote pr:nova-tools:%s", n)
			}
			if pushes(z.branch) != 1 {
				t.Fatalf("second pass pushed %s again", z.branch)
			}
		}
	})

	t.Run("record-refuses-a-head-that-is-not-pushed-sha", func(t *testing.T) {
		// ns_harvest_pr writes the record only from a head equal to
		// pushed_sha; another head is HEAD| and nothing is written.
		other := "card-other-head"
		key := "s:" + alpha.sprint + ":card:" + other
		branch := "nova/" + alpha.sprint + "/" + other + "-a1"
		c.HSet(ctx, key, "state", "ended", "outcome", "DONE", "bench", alpha.bench, "attempt", "1",
			"repo", "nova-tools", "pushed_sha", alpha.sha, "harvest_step", harvest.StepPushed, "base", "dev", "base_sha", "09fbedc9")
		if r := c.FCall(ctx, harvest.FunctionLease, nil, alpha.bench, "holder", "tok", 60000).Val(); r != "TAKEN" {
			t.Fatalf("lease = %v", r)
		}
		wrong := strings.Repeat("e", 40)
		if r := c.FCall(ctx, harvest.FunctionPR, nil, alpha.sprint, alpha.bench, "holder", "tok", other, "nova-tools", branch, "77", wrong).Val(); r != "HEAD|"+alpha.sha {
			t.Fatalf("ns_harvest_pr with head %s = %v, want HEAD|%s", wrong, r, alpha.sha)
		}
		if c.Exists(ctx, harvest.RecordKey("nova-tools", 77)).Val() != 0 || c.HGet(ctx, "s:"+alpha.sprint+":idem", "pr:nova-tools:"+branch).Val() != "" {
			t.Fatal("a refused head wrote the record or the idem key")
		}
		if r := c.FCall(ctx, harvest.FunctionPR, nil, alpha.sprint, alpha.bench, "holder", "tok", other, "nova-tools", branch, "77", alpha.sha).Val(); r != "PR|77" {
			t.Fatalf("ns_harvest_pr with the pushed_sha = %v, want PR|77", r)
		}
		rec := c.HGetAll(ctx, harvest.RecordKey("nova-tools", 77)).Val()
		if rec["head"] != alpha.sha || rec["label"] != other || rec["branch"] != branch || rec["state"] != "open" {
			t.Fatalf("record = %v", rec)
		}
		// The card model's double link: the card record carries pr and head
		// from the same call, and the record carries the card (label, sprint).
		if h := c.HGetAll(ctx, key).Val(); h["pr"] != "77" || h["head"] != alpha.sha || h["harvest_step"] != harvest.StepPublished {
			t.Fatalf("card after ns_harvest_pr = %v; want pr 77, head %s, published", h, alpha.sha)
		}
		if rec["sprint"] != alpha.sprint {
			t.Fatalf("record sprint %q, want %s", rec["sprint"], alpha.sprint)
		}
		at := rec["at"]
		if r := c.FCall(ctx, harvest.FunctionPR, nil, alpha.sprint, alpha.bench, "holder", "tok", other, "nova-tools", branch, "77", alpha.sha).Val(); r != "PR|77" {
			t.Fatalf("ns_harvest_pr again = %v", r)
		}
		if c.HGet(ctx, harvest.RecordKey("nova-tools", 77), "at").Val() != at {
			t.Fatal("a repeat ns_harvest_pr rewrote the record")
		}
		c.Del(ctx, "lease:harvest:"+alpha.bench)
	})
}

// TestHarvestRecordKeyDropsTheOwner is #3740: a card's repo is owner/name
// (mas-bandwidth/nova-tools), and ns_harvest_pr wrote the PR record under
// pr:mas-bandwidth/nova-tools:<n>, where read post (prkey, pr:<name>:<n>)
// never looks. The record is pr:nova-tools:<n>, the owner form is never
// written, ns_harvest_due reads the head back from the bare key, and the idem
// field pr:<repo>:<branch> (another record) keeps the card's repo.
func TestHarvestRecordKeyDropsTheOwner(t *testing.T) {
	t.Parallel()

	c := startRedis(t)
	ctx := context.Background()
	const (
		bench = "owner-bench"
		label = "card-owner"
		repo  = "mas-bandwidth/nova-tools"
		n     = 3726
	)
	head := sha(label)
	branch := "nova/" + sprint + "/" + label + "-a1"
	key := "s:" + sprint + ":card:" + label
	if err := c.HSet(ctx, key, "kind", "model", "repo", repo, "base", "dev", "base_sha", "09fbedc9",
		"state", "ended", "outcome", "DONE", "reason", "done", "bench", bench, "attempt", "1",
		"identity", sprint+"/"+label+"/09fbedc9/"+bench+"/1", "pushed_sha", head,
		"harvest_step", harvest.StepPushed, "stream", "nova-sprint").Err(); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "s:"+sprint+":idx:card:ended", label)
	c.SAdd(ctx, "s:"+sprint+":bench:"+bench+":ended", label)
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")

	if got, want := harvest.RecordKey(repo, n), "pr:nova-tools:3726"; got != want {
		t.Fatalf("RecordKey(%s, %d) = %s, want %s", repo, n, got, want)
	}
	if r := c.FCall(ctx, harvest.FunctionLease, nil, bench, "holder", "tok", 60000).Val(); r != "TAKEN" {
		t.Fatalf("lease = %v", r)
	}
	if r := c.FCall(ctx, harvest.FunctionPR, nil, sprint, bench, "holder", "tok", label, repo, branch, fmt.Sprint(n), head).Val(); r != "PR|3726" {
		t.Fatalf("ns_harvest_pr = %v, want PR|3726", r)
	}
	if rec := c.HGetAll(ctx, "pr:nova-tools:3726").Val(); rec["head"] != head || rec["label"] != label || rec["state"] != "open" {
		t.Fatalf("pr:nova-tools:3726 = %v; want head %s, label %s, open", rec, head, label)
	}
	if c.Exists(ctx, "pr:mas-bandwidth/nova-tools:3726").Val() != 0 {
		t.Fatal("ns_harvest_pr wrote the owner-form key pr:mas-bandwidth/nova-tools:3726")
	}
	if got := c.HGet(ctx, "s:"+sprint+":idem", "pr:"+repo+":"+branch).Val(); got != "3726" {
		t.Fatalf("idem pr:%s:%s = %q, want 3726 (the idem field keeps the card repo)", repo, branch, got)
	}
	// ns_harvest_due: OK host user, then one row of 14 whose 9th field is the
	// idem PR and whose 14th is rec_head, read from the bare record key.
	due, err := c.FCall(ctx, harvest.FunctionDue, nil, sprint, bench, 16).StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 3+14 || due[0] != "OK" || due[3] != label || due[3+8] != "3726" || due[3+13] != head {
		t.Fatalf("ns_harvest_due = %q; want one row, idem PR 3726, rec_head %s from pr:nova-tools:3726", due, head)
	}
}
