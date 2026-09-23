// sprint_replay_test.go REPLAYS THE DAY. It loads the fixes sprint of 2026-09-22 --
// testdata/sprint-fixes-2026-09-22.tsv, the task set behind the numbers Rowan posted by hand
// on nova-tools #2593 -- through the verbs, and asserts the verbs print what the hand
// printed:
//
//	14/23 61% -> ~4h     before the hand-over (critical lane johnny: A5 -> #2550 -> #2548)
//	14/23 61% -> ~2h     after it (rowan-34593b5bb7a9: "yes -> wall ~2 h, no -> ~4 h")
//
// and that a refill tops each present friend's queue to four, ownership first then age, with
// nothing at all going to the friend whose heartbeat is not up.
//
// It is the DONE-WHEN sentence of #2593 as a test: "today's fixes sprint replays from the
// store to the numbers Rowan posted by hand". The wall arithmetic is the point -- the sum of
// the open work is nearly eight hours and was never the answer.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

const replaySprint = "fixes-2026-09-22"

// replayRow is one line of the fixture.
type replayRow struct {
	ID, Kind, Ref, Owner, Paths, DependsOn, LeasedAt, DoneAt, Evidence string
	Est                                                                string
}

func loadReplayRows(t *testing.T) []replayRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sprint-fixes-2026-09-22.tsv"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var out []replayRow
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 10 {
			t.Fatalf("fixture line has %d fields, wants 10: %q", len(f), line)
		}
		out = append(out, replayRow{
			ID: f[0], Kind: f[1], Ref: f[2], Owner: f[3], Est: f[4],
			Paths: f[5], DependsOn: f[6], LeasedAt: f[7], DoneAt: f[8], Evidence: f[9],
		})
	}
	return out
}

// replayHarness is the store and the flags every call in this file shares.
type replayHarness struct {
	t     *testing.T
	store *sprint.FakeStore
	deps  sprintDeps
	now   time.Time
}

func newReplayHarness(t *testing.T) *replayHarness {
	t.Helper()
	store := sprint.NewFakeStore()
	h := &replayHarness{
		t:     t,
		store: store,
		now:   time.Date(2026, 9, 22, 16, 32, 0, 0, time.UTC),
		deps: sprintDeps{
			open: func(addr, user, password string) (sprint.Store, error) { return store, nil },
			// No primary record is consulted in the replay: every close below carries the
			// record it came from, exactly as the day did.
			records: nil,
			cards: func(addr, user, password string) (sprint.Cards, error) {
				return &sprint.FakeCards{}, nil
			},
			getenv: func(string) string { return "" },
		},
	}
	return h
}

// run calls one sprint verb and returns its stdout, failing on a non-zero exit.
func (h *replayHarness) run(args ...string) string {
	h.t.Helper()
	var out, errOut bytes.Buffer
	args = append(args, "--store", "store.invalid:6380")
	if code := runSprint(args, &out, &errOut, h.now, h.deps); code != 0 {
		h.t.Fatalf("nova-pulse sprint %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out.String(), errOut.String())
	}
	return out.String()
}

// load opens the sprint, adds every task in fixture order (each one a minute later than the
// last, which is what the age ordering is read from) and closes the fourteen that closed,
// each with the primary record that closed it.
func (h *replayHarness) load(rows []replayRow) {
	h.t.Helper()
	h.run("open", "--name", replaySprint, "--goal", "every fix, read and decision of the fixes day, friends and swarm in one x/y")
	added := h.now.Add(-8 * time.Hour)
	for _, r := range rows {
		args := []string{"add", "--name", replaySprint, "--id", r.ID, "--kind", r.Kind, "--ref", r.Ref, "--owner", r.Owner, "--est", r.Est}
		if r.Paths != "-" {
			args = append(args, "--paths", r.Paths)
		}
		if r.DependsOn != "-" {
			args = append(args, "--depends-on", r.DependsOn)
		}
		if r.LeasedAt != "-" {
			args = append(args, "--leased-at", r.LeasedAt)
		}
		var out, errOut bytes.Buffer
		full := append(append([]string{}, args...), "--store", "store.invalid:6380")
		if code := runSprint(full, &out, &errOut, added, h.deps); code != 0 {
			h.t.Fatalf("adding %s exited %d: %s", r.ID, code, errOut.String())
		}
		added = added.Add(time.Minute)
	}
	for _, r := range rows {
		if r.DoneAt == "-" {
			continue
		}
		h.run("close", "--name", replaySprint, "--task", r.ID, "--evidence", r.Evidence, "--at", r.DoneAt)
	}
}

// presence is the measured heartbeat: three friends up, the coordinator up, Freddy with no
// window at all, and the benches pushing their rows.
func (h *replayHarness) presence() {
	for _, name := range []string{"johnny", "stella", "emma", "rowan", "hulk", "vision", "space", "hetzner", "superman", "batman"} {
		h.store.SetPresent(name, true)
	}
	h.store.SetPresent("freddy", false)
}

func machinesFixture() string { return filepath.Join("testdata", "sprint-machines-2026-09-22.tsv") }

const replayFriends = "johnny,stella,emma,rowan,freddy"

func TestSprintReplayPrintsTheDaysNumbers(t *testing.T) {
	h := newReplayHarness(t)
	h.load(loadReplayRows(t))
	h.presence()

	// 1. The line, as Glenn asked for it at 12:32 PM: one active sprint, so no name, no
	// C/O/W, just the fraction and the WALL.
	got := strings.TrimSpace(h.run("status"))
	if want := "14/23 61% -> ~4h"; got != want {
		t.Fatalf("sprint status printed %q, wants %q", got, want)
	}

	// 2. --verbose names the critical lane and what is splittable on it: the two tasks
	// Johnny held that share no file with anything else of his.
	verbose := h.run("status", "--verbose")
	for _, want := range []string{
		"C=14 O=9 W=0",
		"critical johnny",
		"splittable tools-2550",
		"splittable tools-2548",
		"Open tools-2550 mas-bandwidth/nova-tools#2550 owner=johnny route=- est=~1.5h kind=fix depends=-",
	} {
		if !strings.Contains(verbose, want) {
			t.Fatalf("sprint status --verbose has no %q:\n%s", want, verbose)
		}
	}

	// 3. The question asked before the hand-over: what would the wall be if those two
	// moved? This writes nothing -- it is the number put in front of the owner.
	wall := h.run("wall", "--move", "tools-2550,tools-2548")
	if !strings.Contains(wall, "WALL ~4h work ~7.8h critical johnny") {
		t.Fatalf("sprint wall did not print the four hour wall over the day's open work:\n%s", wall)
	}
	if !strings.Contains(wall, "MOVING tools-2550,tools-2548 -> wall ~2h (from ~4h)") {
		t.Fatalf("sprint wall did not offer the two hour wall:\n%s", wall)
	}

	// 4. Johnny said yes (bus rowan-34593b5bb7a9), so the typing moves and he stays the
	// required reader. Each hand-over goes to the emptiest lane that can take it.
	for _, id := range []string{"tools-2550", "tools-2548"} {
		out := h.run("route", "--task", id, "--hand-over", "--apply", "--machines", machinesFixture(), "--friends", replayFriends)
		if !strings.Contains(out, "reader=johnny") {
			t.Fatalf("the hand-over of %s did not keep johnny as the required reader: %s", id, out)
		}
		if !strings.Contains(out, "ROUTE "+id+" -> bench:") {
			t.Fatalf("the hand-over of %s did not reach a bench: %s", id, out)
		}
	}

	// 5. The same line again: the same work, half the wall.
	got = strings.TrimSpace(h.run("status"))
	if want := "14/23 61% -> ~2h"; got != want {
		t.Fatalf("after the hand-over sprint status printed %q, wants %q", got, want)
	}
}

// TestSprintReplayRefillTopsEachFriendToFour is the 1:12 PM half of the day: "When somebody
// completes work, you must make sure they have more work to do. a queue if you will." The
// read debt on the day's open pull requests is placed on exactly one friend each, and every
// present friend's queue is topped to four, ownership first then age. Freddy has never had a
// window up, so nothing is routed to him -- that is the whole of "never route to an away
// friend", and it costs one measured key, not a judgement.
func TestSprintReplayRefillTopsEachFriendToFour(t *testing.T) {
	h := newReplayHarness(t)
	h.load(loadReplayRows(t))
	h.presence()
	for _, id := range []string{"tools-2550", "tools-2548"} {
		h.run("route", "--task", id, "--hand-over", "--apply", "--machines", machinesFixture(), "--friends", replayFriends)
	}

	// The read debt: every open pull request of the day with zero typed lines, one task
	// each, owned by the friend who owns that package.
	debt := []struct{ id, ref, owner string }{
		{"read-2604", "mas-bandwidth/nova-tools#2604", "johnny"},
		{"read-2605", "mas-bandwidth/nova-tools#2605", "johnny"},
		{"read-2606", "mas-bandwidth/nova-tools#2606", "johnny"},
		{"read-2609", "mas-bandwidth/nova-tools#2609", "stella"},
		{"read-2611", "mas-bandwidth/nova-tools#2611", "stella"},
		{"read-2612", "mas-bandwidth/nova-tools#2612", "emma"},
		{"read-2613", "mas-bandwidth/nova-tools#2613", "emma"},
	}
	at := h.now.Add(-time.Hour)
	for _, d := range debt {
		var out, errOut bytes.Buffer
		args := []string{"add", "--name", replaySprint, "--id", d.id, "--kind", "read", "--ref", d.ref, "--owner", d.owner, "--store", "store.invalid:6380"}
		if code := runSprint(args, &out, &errOut, at, h.deps); code != 0 {
			t.Fatalf("adding %s exited %d: %s", d.id, code, errOut.String())
		}
		at = at.Add(time.Minute)
	}

	out := h.run("refill", "--machines", machinesFixture(), "--friends", replayFriends)
	if strings.Contains(out, "freddy") {
		t.Fatalf("a task was routed to a friend whose heartbeat is not up:\n%s", out)
	}
	for _, friend := range []string{"johnny", "stella", "emma"} {
		if want := "REFILL " + friend + " +4 depth 0 -> 4"; !strings.Contains(out, want) {
			t.Fatalf("refill did not top %s to four (%q):\n%s", friend, want, out)
		}
		if got := len(h.store.Queue("q:" + friend)); got != 4 {
			t.Fatalf("%s's queue holds %d tasks after the refill, wants 4", friend, got)
		}
	}

	// Ownership first, then age: Johnny's own A5 is the oldest thing he owns, so it is at
	// the top of his queue, and the read debt follows in the order it arrived.
	var ids []string
	for _, task := range h.store.Queue("q:johnny") {
		ids = append(ids, task.ID)
	}
	want := []string{"a5-2544", "read-2604", "read-2605", "read-2606"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("johnny's queue is %v, wants %v (ownership first, then age)", ids, want)
	}

	// A read is a friend's typed line: no read may be placed on a bench, however empty.
	for _, bench := range []string{"hulk", "vision", "space", "hetzner", "superman", "batman"} {
		for _, task := range h.store.Queue("q:" + bench) {
			if task.Kind == sprint.KindRead {
				t.Fatalf("read %s was placed on the bench %s", task.ID, bench)
			}
		}
	}
}

// TestSprintReplayCalibrationHasTodaysError is the estimator's half: fourteen closed tasks,
// each with the actual measured from its lease to the record that closed it. The numbers are
// today's and they are not flattering, which is the point of printing them.
func TestSprintReplayCalibrationHasTodaysError(t *testing.T) {
	h := newReplayHarness(t)
	h.load(loadReplayRows(t))
	out := h.run("calibration")
	for _, want := range []string{"KIND fix", "KIND read", "OWNER johnny", "SUGGEST fix"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sprint calibration has no %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "error=") {
		t.Fatalf("sprint calibration printed no error distribution:\n%s", out)
	}
}
