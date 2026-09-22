package pulse

// The bench harvest's two events (nova-tools #2563 item 1): `harvested` when the branch is
// pushed and `pr` when a pull request is OPENED. They ride the same seams every other test
// in this file uses -- a fake bench shell, a fake forge and the fake git on PATH -- plus an
// events.FakeStream in place of the store, so no test here opens a connection.
//
// The test that matters most is the LAST one: a store that errors on every entry leaves the
// harvest's exit code, its receipt lines, its push and its pull request exactly as they were.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// deadlineStore is a FakeStream that also records the DEADLINE of the context each Emit was
// handed. It exists for one assertion, and that assertion is the bug it was written against:
// a five-second deadline taken once at the top of a verb that runs for minutes has expired
// before the first entry, and an emit that can never fail a harvest fails silently.
type deadlineStore struct {
	*events.FakeStream
	deadlines []time.Time
	hadNone   int
}

func (d *deadlineStore) Emit(ctx context.Context, e events.Event) (string, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.deadlines = append(d.deadlines, dl)
	} else {
		d.hadNone++
	}
	return d.FakeStream.Emit(ctx, e)
}

// benchEventFixture is one finished job on a bench, with a store attached. It returns the
// input, the fake stream and the forge so a case can assert on all three.
func benchEventFixture(t *testing.T, fake *events.FakeStream, log *strings.Builder) (HarvestInput, *events.FakeStream, *fakeForge) {
	t.Helper()
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc",
			"DONE",
			"BRANCH rowan/card-9601",
			"REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	var sink strings.Builder
	if log == nil {
		log = &sink
	}
	in.Events = events.OpenWriter(context.Background(), events.WriterOptions{
		Log: log,
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return fake, nil },
	})
	return in, fake, forge
}

// entries reads the whole fake stream back as typed events.
func streamEntries(t *testing.T, fake *events.FakeStream) []events.Event {
	t.Helper()
	raw, err := fake.Range(context.Background(), "-", 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]events.Event, 0, len(raw))
	for _, r := range raw {
		e, err := events.FromFields(r.Fields)
		if err != nil {
			t.Fatalf("an entry this verb wrote does not read back: %v (%v)", err, r.Fields)
		}
		out = append(out, e)
	}
	return out
}

// TestHarvestBenchEmitsHarvestedAndPR: one finished job writes exactly two entries, in the
// order the work happened -- the push first, then the pull request -- and both carry the
// label, the bench and the FULL head, which is what the lander and the reads join on.
func TestHarvestBenchEmitsHarvestedAndPR(t *testing.T) {
	in, fake, forge := benchEventFixture(t, events.NewFakeStream(), nil)
	code, out, errb := runBenchHarvest(t, in)
	in.Events.Close()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	got := streamEntries(t, fake)
	if len(got) != 2 {
		t.Fatalf("one finished job wrote %d entries, want harvested then pr: %+v", len(got), got)
	}
	if got[0].Kind != events.Harvested {
		t.Errorf("the first entry is %q, want %q -- the push is the durable fact and is announced first", got[0].Kind, events.Harvested)
	}
	if got[1].Kind != events.PullReq {
		t.Errorf("the second entry is %q, want %q", got[1].Kind, events.PullReq)
	}
	for _, e := range got {
		if e.Label != "card-9601" {
			t.Errorf("entry %q carries label %q", e.Kind, e.Label)
		}
		if e.Bench != "hulk" {
			t.Errorf("entry %q carries bench %q", e.Kind, e.Bench)
		}
		if e.Head == "" {
			t.Errorf("entry %q carries no head; the fold joins landings on an exact head", e.Kind)
		}
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}
	if got[1].PR != "77" {
		t.Errorf("the pr entry names PR %q, want the number the forge returned", got[1].PR)
	}
	if fake.StreamName() != "cards:done" || in.Events.StreamName() != "cards:done" {
		t.Errorf("the harvest wrote to %q through a writer naming %q, want cards:done for both", fake.StreamName(), in.Events.StreamName())
	}
}

// TestHarvestBenchDoesNotEmitPRForOneThatAlreadyExisted: this verb runs again and again over
// the same bench. FindPR answering with a number means an EARLIER pass opened -- and already
// announced -- that pull request, so a second `pr` entry would make one card's PR several in
// the fold. `harvested` is not emitted either: nothing is pushed for a job already marked.
func TestHarvestBenchDoesNotEmitPRForOneThatAlreadyExisted(t *testing.T) {
	in, fake, forge := benchEventFixture(t, events.NewFakeStream(), nil)
	forge.find = func(string, string) (int, error) { return 1234, nil }
	code, out, errb := runBenchHarvest(t, in)
	in.Events.Close()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	got := streamEntries(t, fake)
	if len(got) != 1 || got[0].Kind != events.Harvested {
		t.Fatalf("a pass that found an existing PR wrote %+v; want the push's entry alone", got)
	}
	if len(forge.opened) != 0 {
		t.Fatalf("the verb opened a PR that already existed")
	}
}

// TestHarvestBenchEmitsNothingWhenNothingIsPushed: a job with no commits past its base is
// marked harvested on the bench and pushed nowhere. No branch reached the forge, so the
// stream says nothing -- an entry here would put work in the fold that does not exist.
func TestHarvestBenchEmitsNothingWhenNothingIsPushed(t *testing.T) {
	fake := events.NewFakeStream()
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, map[string]string{"origin/dev..refs/harvest/rowan/card-9601": "0"})
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc", "DONE", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Events = events.OpenWriter(context.Background(), events.WriterOptions{
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return fake, nil },
	})
	code, out, errb := runBenchHarvest(t, in)
	in.Events.Close()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "HARVEST NO-COMMIT") {
		t.Fatalf("the fixture did not reach the no-commit path:\n%s", out)
	}
	if fake.Len() != 0 {
		t.Fatalf("a job that pushed nothing wrote %d entries", fake.Len())
	}
}

// TestHarvestBenchSurvivesAStoreThatErrors IS THE CONTRACT. The store refuses every entry.
// The branch is still pushed, the pull request is still opened, the receipt lines are
// byte-for-byte what they are without a store, and the exit code is 0 -- the harvest is not
// RED because a measurement failed. The only difference is one line per refused entry.
func TestHarvestBenchSurvivesAStoreThatErrors(t *testing.T) {
	fake := events.NewFakeStream()
	fake.FailEmit = errors.New("LOADING Redis is loading the dataset in memory")
	var log strings.Builder
	in, _, forge := benchEventFixture(t, fake, &log)
	code, out, errb := runBenchHarvest(t, in)
	in.Events.Close()

	if code != 0 {
		t.Fatalf("a store that errors made the harvest exit %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "HARVEST JOB bench=hulk label=card-9601 branch=rowan/card-9601") {
		t.Errorf("the job's receipt line did not survive the failed emits:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST BENCH OK") {
		t.Errorf("the harvest is not OK though every fact it records is true:\n%s", out)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1: a store that is down must not stop a pull request", len(forge.opened))
	}
	if fake.Len() != 0 {
		t.Fatalf("a store that errors kept %d entries", fake.Len())
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("two refused entries wrote %d lines, want one each:\n%s", len(lines), log.String())
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "EVENT SKIPPED label=card-9601 ") {
			t.Errorf("a skip line does not name its entry: %q", l)
		}
	}
}

// TestHarvestBenchBoundsEachEntrySeparately: every entry is written under its OWN deadline.
//
// RED WITHOUT THE FIX. The first cut took one `context.WithTimeout(..., 5s)` at the top of
// harvestBench and handed it to both sends. A bench harvest fetches, pushes and opens pull
// requests for every finished job and runs for minutes, so on any real pass that context was
// long expired and EVERY entry was refused `context deadline exceeded` -- silently, because
// an emit never fails a harvest. The assertion is mechanical and needs no clock: two entries
// written under one shared context carry the SAME absolute deadline, and two entries each
// taking their own carry different ones.
func TestHarvestBenchBoundsEachEntrySeparately(t *testing.T) {
	store := &deadlineStore{FakeStream: events.NewFakeStream()}
	in, _, _ := benchEventFixture(t, store.FakeStream, nil)
	// Re-open the writer over the recording store rather than the bare fake.
	in.Events = events.OpenWriter(context.Background(), events.WriterOptions{
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return store, nil },
	})
	code, out, errb := runBenchHarvest(t, in)
	in.Events.Close()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	if store.hadNone > 0 {
		t.Fatalf("%d entries were written under a context with NO deadline; a store that stops answering must cost seconds", store.hadNone)
	}
	if len(store.deadlines) != 2 {
		t.Fatalf("recorded %d deadlines, want one per entry", len(store.deadlines))
	}
	if store.deadlines[0].Equal(store.deadlines[1]) {
		t.Fatal("both entries were written under ONE shared deadline; a bound taken once per harvest expires before the first push and refuses every entry silently")
	}
	for i, dl := range store.deadlines {
		if !dl.After(time.Now()) {
			t.Errorf("entry %d was written under a deadline already in the past", i)
		}
	}
}

// TestHarvestBenchWithNoStoreIsUnchanged: the writer is nil, which is what every harvest
// that names no store and has no password runs with. Nothing guards the Send call sites, so
// this asserts the nil receiver really is a no-op on the live path and not only in a unit
// test of the writer.
func TestHarvestBenchWithNoStoreIsUnchanged(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc", "DONE", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Events = nil
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	if len(forge.opened) != 1 || !strings.Contains(out, "HARVEST BENCH OK") {
		t.Fatalf("a harvest with no store did not behave as it always has:\n%s", out)
	}
}
