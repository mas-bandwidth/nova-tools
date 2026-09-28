//go:build functional

package capacity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
)

// TestWorkerPauseResumeShow (#4308): worker pause sets paused 1 on a
// bench's or a friend's desired hash in one FCALL with a cap:log receipt,
// resume clears it, a repeat writes nothing, and worker show reads every
// worker's record (slots, paused, tiers, kinds, machine) or the one named;
// a name neither registry holds is UNKNOWN.
func TestWorkerPauseResumeShow(t *testing.T) {
	t.Parallel()

	st, c := redisControl(t)
	ctx := context.Background()
	if _, err := capacity.SetMachine(ctx, st, "m", 64, 64, 128, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetBenchWith(ctx, st, "b", "m", 8, "test", "", capacity.DesiredOpts{Tiers: "frontier,pro"}); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetFriendWith(ctx, st, "f", "m", 4, "test", "", capacity.DesiredOpts{Kinds: "read"}); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "consumers", "bench:z") // enrolled, no desired hash: shown with empty fields

	receipts := func() int64 { return c.XLen(ctx, capacity.LogKey).Val() }
	before := receipts()
	for _, k := range []struct{ kind, name string }{{"bench", "b"}, {"friend", "f"}} {
		word, changed, err := capacity.PauseWorker(ctx, st, k.kind, k.name, true, "rowan", "")
		if err != nil || word != "PAUSED" || !changed {
			t.Fatalf("pause %s:%s = %s %v %v", k.kind, k.name, word, changed, err)
		}
		if got := c.HGet(ctx, capacity.DesiredKey(k.kind, k.name), "paused").Val(); got != "1" {
			t.Fatalf("%s:%s paused=%q after pause", k.kind, k.name, got)
		}
		// a repeat is the same word and writes nothing
		word, changed, err = capacity.PauseWorker(ctx, st, k.kind, k.name, true, "rowan", "")
		if err != nil || word != "PAUSED" || changed {
			t.Fatalf("repeat pause %s:%s = %s %v %v", k.kind, k.name, word, changed, err)
		}
	}
	if n := receipts() - before; n != 2 {
		t.Fatalf("cap:log grew by %d, want one receipt per pause", n)
	}
	if got := c.HGet(ctx, "bench:b:desired", "slots").Val(); got != "8" {
		t.Fatalf("pause changed slots to %q", got)
	}

	rows, err := capacity.ShowWorkers(ctx, st, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, r.Line())
	}
	want := "WORKER bench:b slots=8 paused=1 tiers=frontier,pro kinds=- machine=m\n" +
		"WORKER bench:z slots=- paused=0 tiers=- kinds=- machine=-\n" +
		"WORKER friend:f slots=4 paused=1 tiers=- kinds=read machine=m"
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("show:\n%s\nwant:\n%s", got, want)
	}

	word, changed, err := capacity.PauseWorker(ctx, st, "bench", "b", false, "rowan", "")
	if err != nil || word != "RESUMED" || !changed {
		t.Fatalf("resume = %s %v %v", word, changed, err)
	}
	one, err := capacity.ShowWorkers(ctx, st, "bench", "b")
	if err != nil || len(one) != 1 || one[0].Paused || one[0].Line() != "WORKER bench:b slots=8 paused=0 tiers=frontier,pro kinds=- machine=m" {
		t.Fatalf("show one after resume: %+v %v", one, err)
	}

	var unknown *capacity.UnknownWorker
	if _, _, err := capacity.PauseWorker(ctx, st, "bench", "nope", true, "rowan", ""); !errors.As(err, &unknown) || unknown.ID != "bench:nope" {
		t.Fatalf("pause of an unregistered bench: %v", err)
	}
	if _, err := capacity.ShowWorkers(ctx, st, "friend", "nope"); !errors.As(err, &unknown) || unknown.ID != "friend:nope" {
		t.Fatalf("show of an unregistered friend: %v", err)
	}
	if c.Exists(ctx, "bench:nope:desired").Val() != 0 {
		t.Fatal("a refused pause wrote a desired hash")
	}
}
