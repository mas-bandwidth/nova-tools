//go:build slow

// The join and delivery tests whose cost is real work rather than a wait: child
// processes killed at each write boundary, a real bus, and a retry clock. Five tests
// were 70 s of this package's 86 s.
//
// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (#516, Glenn's two-minute rule -- a package's
// tests answer in a minute). Nothing here is skipped or weakened; it runs nightly, whole.

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingBeforeDispatchAndQuietRetry(t *testing.T) {
	for _, mode := range []string{"uncertain", "hang"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeBusPath(t)
			p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
			statePath := filepath.Join(t.TempDir(), "s.json")
			args := []string{"report", "--file", p, "--send", "--snapshot", statePath, "--as", "fixture", "--to", "integrator", "--bus", t.TempDir(), "--remote", "origin", "--branch", "main", "--timeout", "5s"}
			if mode == "hang" {
				// A bus that never answers is now bounded by the BUDGET, not by
				// the version probe's --timeout: that was the repair. So the
				// fixture names a budget, and the hang is cut off by the same
				// allowance a real delivery gets. Ten seconds: enough that a real
				// prepare and the start of a send fit, far less than the fixture's
				// thirty-second sleep.
				args = append(args, "--budget", "10s")
			}
			t.Setenv("NOVA_UPDATE_BUS_MODE", mode)
			code, _, errout := run(t, Environment{}, args...)
			if code != 1 {
				t.Fatal(code)
			}
			need(t, errout, "sent=uncertain")
			s, e := readSnapshot(statePath)
			if e != nil || len(s.Pending) != 1 || len(s.Delivered) != 0 {
				t.Fatal(s, e)
			}
			var id string
			for _, pending := range s.Pending {
				id = pending.ID
				if id == "" {
					t.Fatal("no ID retained")
				}
			}
			t.Setenv("NOVA_UPDATE_BUS_MODE", "ok")
			code, out, errout := run(t, Environment{}, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, errout)
			}
			need(t, out, "REPORT SENT", "id\\x3d"+id)
			nprep, nsend := calls(t, log)
			if nprep != 1 || nsend != 2 {
				t.Fatal(nprep, nsend)
			}
			code, out, errout = run(t, Environment{}, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, errout)
			}
			need(t, out, "nothing sent", "sent=no")
			np, ns := calls(t, log)
			if np != nprep || ns != nsend {
				t.Fatal("unchanged confirmed run invoked bus")
			}
		})
	}
}

// The join at its plainest: a real prepare, a real push, and the two deciding
// facts -- one note plus one INDEX row on the remote, and zero bus invocations
// on the next run of a report that has not changed.
func TestJoinRealBusPublishesOneNoteAndOneIndexRow(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	code, out, errs := r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	id := r.deliveredID(t)
	note := r.bus.exactlyOneContribution(t, id)
	if !strings.Contains(out, "REPORT SENT") {
		t.Fatalf("no REPORT SENT line: %s", out)
	}
	before := git(t, r.bus.bare, "rev-parse", "main")
	code, out, errs = r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "nothing sent") {
		t.Fatalf("a confirmed unchanged report was sent again: %s", out)
	}
	if after := git(t, r.bus.bare, "rev-parse", "main"); after != before {
		t.Fatal("a confirmed unchanged report moved the remote")
	}
	if n, _ := r.bus.published(t); len(n) != 1 || n[0] != note {
		t.Fatalf("the note changed under an unchanged report: %v", n)
	}
}

// ------------------------------------------------------------------- the cases
// A refused push must not cost the report its identity. The remote refuses every
// push while the hook is installed; the caller keeps the prepared artifact; the
// hook comes off and the SAME snapshot is sent again, and exactly one note with
// the original identity lands.
func TestJoinRefusedPushRecoversToOneNoteWithTheSameID(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	restore, how := r.bus.refusePushes(t)
	t.Logf("pushes are refused by %s", how)
	started := time.Now()
	code, out, errs := r.send(t, r.bin)
	refusedIn := time.Since(started)
	if code != 1 {
		t.Fatalf("a refused push was not reported as a failure: %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(errs+out, "sent=uncertain") {
		t.Fatalf("a refused push was not reported as uncertain:\n%s\n%s", out, errs)
	}
	id := r.pendingID(t)
	restore()
	if notes, index := r.bus.published(t); len(notes) != 0 || len(index) != 0 {
		t.Fatalf("a refused push published something: %v %v", notes, index)
	}
	// The reporter hands the bus finite --attempts and --git-timeout out of its
	// own remaining budget, so a remote that refuses every push costs seconds
	// rather than the bus's implicit twenty-five tries. The witness is printed
	// because the seconds are the point.
	t.Logf("a remote refusing every push was reported uncertain in %s", refusedIn.Round(time.Millisecond))
	if refusedIn > 30*time.Second {
		t.Fatalf("a refused push took %s, which is not a bound anybody chose", refusedIn)
	}
	code, out, errs = r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if got := r.deliveredID(t); got != id {
		t.Fatalf("the retry delivered %q, not the prepared %q", got, id)
	}
	r.bus.exactlyOneContribution(t, id)
}

// A child killed at each of the named write boundaries, for real, with SIGKILL.
// The saved artifact has to recover the SAME identity, and each final remote has
// to hold one complete contribution -- one note and one INDEX row -- no matter
// which boundary the death landed on.
func TestJoinChildDeathAtWriteBoundariesRecoversOneContribution(t *testing.T) {
	for _, boundary := range []string{killBeforeNote, killAfterNote, killAfterIndex, killAfterAttrs, killAfterCmt} {
		t.Run(boundary, func(t *testing.T) {
			missed := 0
			for attempt := 1; attempt <= stagingAttempts; attempt++ {
				if stagedADeath(t, boundary, attempt) {
					return
				}
				missed++
				t.Logf("%s: the bus finished before the kill landed (miss %d of %d allowed); staging it again", boundary, missed, stagingAttempts)
			}
			t.Skipf("%s: no kill landed on a live bus in %d staged attempts, so this boundary is UNPROVEN in this run rather than green", boundary, stagingAttempts)
		})
	}
}

// A lost confirmation needs no signal at all: the bus is allowed to finish and
// its answer is thrown away, which is what a reporter that dies between the push
// and reading the answer leaves behind. Nothing here is platform-specific, so
// this case runs on Windows too.
func TestJoinLostConfirmationRecoversWithoutASecondNote(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	wrap, record := r.wrapperOnPath(t, killLostResult)
	code, out, errs := r.send(t, wrap)
	if code != 1 {
		t.Fatalf("a lost answer was not reported as a failure: %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(errs+out, "sent=uncertain") {
		t.Fatalf("a lost answer was not reported as uncertain:\n%s\n%s", out, errs)
	}
	id := r.pendingID(t)
	if b, err := os.ReadFile(record); err != nil {
		t.Fatalf("the wrapper left no record: %v", err)
	} else if !strings.Contains(string(b), "boundary="+killLostResult) {
		t.Fatalf("the wrapper staged something else: %s", b)
	}
	notes, _ := r.bus.published(t)
	if len(notes) != 1 {
		t.Fatalf("the bus was allowed to finish, so the note should be published: %v", notes)
	}
	head := git(t, r.bus.bare, "rev-parse", "main")
	clearWrapper(t)
	code, out, errs = r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("recovery failed: %d\n%s\n%s\nthe bus, asked directly: %s", code, out, errs, r.busAskedDirectly(t))
	}
	if got := r.deliveredID(t); got != id {
		t.Fatalf("recovery confirmed %q, not the pending %q", got, id)
	}
	r.bus.exactlyOneContribution(t, id)
	if after := git(t, r.bus.bare, "rev-parse", "main"); after != head {
		t.Fatalf("recovery published a second time: %s became %s", head, after)
	}
	if !strings.Contains(out, "already-published") {
		t.Fatalf("recovery republished instead of finding its own note: %s", out)
	}
}
