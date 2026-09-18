package pulse

// SPEC-PULSE.md, "Rate and convergence" rule 4, replay 43 `gated-card-launches-on-merge`:
// a card carrying `AFTER: PR7 merged` is gated while the fixture forge reports PR 7 open,
// and goes out on the first tick after the fixture reports it merged, with no other input.
//
// Until 2026-09-18 the rule had no code and no test on either road into a bench. The fill
// road launched the card while the PR did not exist; the manager wrote the line and never
// took it off. Both halves are here.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gateForge is a PRSource a test teaches: PR number to state. A PR it does not know is an
// error, which is the forge answering "unread" and a gate that stays shut.
type gateForge struct {
	state map[int]string
	asked int
}

func (f *gateForge) View(repo string, pr int) (PRView, error) {
	f.asked++
	state, ok := f.state[pr]
	if !ok {
		return PRView{}, fmt.Errorf("no pull request %d", pr)
	}
	return PRView{Number: pr, State: state}, nil
}

// TestGatedCardLaunchesOnMerge is replay 43. One ready card gated on PR 7 and one ungated
// beside it: the first tick launches only the ungated one and says which PR the other
// waits on; the forge then reports PR 7 merged and the second tick launches it, with the
// gate line taken off the card as it goes.
func TestGatedCardLaunchesOnMerge(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1 the gated one\nAFTER: PR7 merged\nSOURCE: repo owner/name#1\n")
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2 the free one\n")
	forge := &gateForge{state: map[int]string{7: "OPEN"}}
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)

	run := func() (string, string, *laneLauncher) {
		l := &laneLauncher{}
		var out, errb bytes.Buffer
		code := Fill(FillInput{
			Ready: ready, Launched: launched, Machines: machines, Repo: "owner/name",
			Benches: []string{"bench-a"}, Once: true,
			Stdout: &out, Stderr: &errb,
			Capacity: laneCap{"bench-a": 10}, Launcher: l, Forge: forge,
		})
		if code != 0 {
			t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
		}
		return out.String(), errb.String(), l
	}

	out, _, l := run()
	if len(l.calls) != 1 || !strings.HasSuffix(l.calls[0], "card-002.md") {
		t.Fatalf("tick 1 launched %q, want only card-002.md: a card gated on an OPEN pull request went out", l.calls)
	}
	if !strings.Contains(out, "FILL GATED card=card-001.md after=PR7 state=OPEN") {
		t.Fatalf("tick 1 did not name the gate it held: %q", out)
	}
	if !strings.Contains(out, "gated=1") {
		t.Fatalf("the FILL line does not count the gated card: %q", out)
	}
	if _, err := os.Stat(filepath.Join(ready, "card-001.md")); err != nil {
		t.Fatalf("the gated card left --ready: %v", err)
	}

	// The one thing that changes: the forge.
	forge.state[7] = "MERGED"
	out, _, l = run()
	if len(l.calls) != 1 || !strings.HasSuffix(l.calls[0], "card-001.md") {
		t.Fatalf("tick 2 launched %q, want card-001.md: the merge did not release the gate", l.calls)
	}
	if strings.Contains(out, "FILL GATED") {
		t.Fatalf("tick 2 still held the card: %q", out)
	}
	body, err := os.ReadFile(filepath.Join(launched, "card-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "AFTER:") {
		t.Fatalf("the spent gate is still on the launched card: %q", body)
	}
}

// TestGateAsksTheForgeOncePerPullRequest: nine cards behind one PR is one forge call, not
// nine. A tick that asks the forge per card is a tick that costs a rate limit to say the
// same word nine times.
func TestGateAsksTheForgeOncePerPullRequest(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 9; i++ {
		writeCard(t, ready, fmt.Sprintf("card-00%d.md", i), "RESULT: CARD\nAFTER: PR1348 merged\n")
	}
	forge := &gateForge{state: map[int]string{1348: "OPEN"}}
	var out, errb bytes.Buffer
	Fill(FillInput{
		Ready: ready, Launched: launched, Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches: []string{"bench-a"}, Once: true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 30}, Launcher: &laneLauncher{}, Forge: forge,
	})
	if forge.asked != 1 {
		t.Fatalf("the forge was asked %d times about one pull request, want 1", forge.asked)
	}
	if !strings.Contains(out.String(), "gated=9") {
		t.Fatalf("the FILL line does not count all nine: %q", out.String())
	}
}

// TestGateWithNoForgeHoldsTheCard: a fill with no forge cannot tell whether the PR landed,
// and a gate whose answer is unknown stays shut. The alternative -- launching on a guess --
// is the edge this rule exists to close.
func TestGateWithNoForgeHoldsTheCard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD\nAFTER: PR9999 merged\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	Fill(FillInput{
		Ready: ready, Launched: launched, Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches: []string{"bench-a"}, Once: true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: l,
	})
	if len(l.calls) != 0 {
		t.Fatalf("a card gated on PR9999 launched with no forge to ask: %q", l.calls)
	}
	if !strings.Contains(out.String(), "state=no-forge") {
		t.Fatalf("the held card does not say the forge was never asked: %q", out.String())
	}
}

// TestFillDryRunChangesNothing: `--once` still moved the card out of --ready and launched
// it, so there was no way to see what a tick would do (the manager dogfood, edge 5). A dry
// run counts the same cards and changes nothing -- no move, no marker, no launcher, no lock.
func TestFillDryRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2\nLANE: pulse\n")
	writeCard(t, ready, "card-003.md", "RESULT: CARD-3\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Queue: dir,
		Lanes:    laneFile(t, dir, "pulse\tinternal/pulse/"),
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"}, Once: true, DryRun: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("a dry run launched %q", l.calls)
	}
	if got := len(readyCards(ready)); got != 3 {
		t.Fatalf("a dry run moved %d of 3 cards out of --ready", 3-got)
	}
	if !strings.Contains(out.String(), "dry-run=yes") {
		t.Fatalf("the FILL line does not say it changed nothing: %q", out.String())
	}
	// It counts what it WOULD do, lane rule and all: two launches and one held.
	if !strings.Contains(out.String(), "bench-a:launched=2,failed=0") {
		t.Fatalf("a dry run does not count what the tick would place: %q", out.String())
	}
	if !strings.Contains(out.String(), "FILL HELD card=card-002.md lane=pulse live=card-001.md") {
		t.Fatalf("a dry run lost the lane rule: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, QueueLockName)); !os.IsNotExist(err) {
		t.Fatalf("a dry run took the queue's lock")
	}
}
