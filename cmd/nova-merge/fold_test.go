package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 22, the fold's half: A RECORD FILE THAT DOES NOT DECODE IS NEVER SKIPPED.
// The unreadable file may be the hold or the newer red, so its entry is blocked for the
// pass whatever its other records say, and a file whose path names no entry stops the
// pass before any entry is read.

// putRecord writes a file straight into the lane branch at the remote, the way another
// machine's lane would, and returns nothing: what it proves is what the coordinator's
// next pull folds.
func (l *lab) putRecord(path, body string) {
	l.t.Helper()
	side := filepath.Join(l.dir, "side")
	if _, err := os.Stat(side); os.IsNotExist(err) {
		l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	}
	l.git(side, "fetch", "-q", "origin", "nova-merge/lane")
	l.git(side, "reset", "-q", "--hard", "FETCH_HEAD")
	full := filepath.Join(side, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.git(side, "add", "--", path)
	l.git(side, "-c", "user.name=other", "-c", "user.email=other@localhost", "commit", "-q", "-m", "a record from another machine")
	l.git(side, "push", "-q", "origin", "HEAD:refs/heads/nova-merge/lane")
}

func TestAMalformedRecordBlocksItsEntryAndIsNeverSkipped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	// Everything is in place for a merge. Now a truncated record arrives from another
	// machine, in this entry's own directory.
	truncated := `{"who":"stella","verdict":"ho`
	l.putRecord("reads/951/stella-"+oid[:12]+"-20260911T131500Z-abcdef.json", truncated)

	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a malformed record blocks its entry: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "FOLD REFUSED file=reads/951/stella-")
	contains(t, stderr, "MERGE BLOCKED entry=951 reason=malformed_record")
	absent(t, stdout, "MERGE OK")
	for _, p := range l.pushes() {
		if strings.HasPrefix(p, "refs/heads/main") {
			t.Errorf("nothing may be published while a record cannot be read: %s", p)
		}
	}
	// The file is preserved untouched: the fold never repairs anything.
	raw, err := os.ReadFile(filepath.Join(l.lane, "reads", "951", "stella-"+oid[:12]+"-20260911T131500Z-abcdef.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != truncated {
		t.Errorf("the file is byte-identical afterwards, got %q", raw)
	}
}

func TestARecordWhosePathNamesNoEntryStopsThePass(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	l.putRecord("reads/README.json", "not a record at all\n")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("exit %d, want 1\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN STOPPED reason=malformed_record file=reads/README.json")
	absent(t, stdout, "RUN ENTRY")
	absent(t, stdout, "RUN BASE")
}

// Demanded test 19 and 22: a read recorded on ANOTHER MACHINE -- a second lane on the
// same branch -- is one file in the branch, and the coordinator's next pass folds it.
func TestAReadFromAnotherMachineReachesTheCoordinatorsNextPass(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	// A reader's own lane, on the same branch: joined=true, and it creates nothing.
	readerLane := filepath.Join(l.dir, "emma-lane")
	exit, stdout, stderr := l.run("init", "--lane", readerLane, "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane")
	if exit != 0 {
		t.Fatalf("a second lane on the same branch: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "joined=true")
	exit, stdout, stderr = l.run("read", "--lane", readerLane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stdout, "pushed=true")
	// The coordinator's next pass says what it pulled, so a reader of the log can see
	// that a read recorded on another machine reached this pass.
	_, stdout, _ = l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "pulled=1")
	contains(t, stdout, "read=1a/0h")
	// And status shows it as a COUNT, never as a name.
	_, stdout, _ = l.run("status", "--lane", l.lane)
	contains(t, stdout, "reads=1a/0h")
	absent(t, stdout, "emma")
	// The list is behind --reads.
	_, stdout, _ = l.run("status", "--lane", l.lane, "--reads", "951")
	contains(t, stdout, "STATUS READ entry=951 who=emma")
	contains(t, stdout, "head="+merge.Short(oid))
	// Every lane is clean afterwards: a git status in a lane that is not mid-verb is
	// clean, on both machines.
	for _, lane := range []string{l.lane, readerLane} {
		if out := l.git(lane, "status", "--porcelain"); out != "" {
			t.Errorf("%s is not clean:\n%s", lane, out)
		}
	}
}

// Rule 22: `run` and `status` PULL the lane branch and then FOLD it, and "a verb that
// cannot take the lock within its --timeout exits 2 and says who holds it".
//
// The pull's error was assigned to `_`. A checkout-lock wait that ran out, or a fetch that
// failed, left the fold running over the local files and the pass deciding on the previous
// state -- so a hold or a newer red that reached the remote since the last pull was simply
// absent from the decision, silently. The fold is the only source of records other
// machines wrote; swallowing its failure is the failure rule 22 exists to close.
func TestAVerbThatCannotTakeTheCheckoutLockExitsTwoAndNamesTheHolder(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	release, err := merge.Lock(filepath.Join(l.lane, merge.CheckoutLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, verb := range []string{"run", "status"} {
		t.Run(verb, func(t *testing.T) {
			args := []string{verb, "--lane", l.lane, "--timeout", "1"}
			if verb == "run" {
				args = append(args, "--once")
			}
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("a verb that cannot take the lock exits 2: got %d\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, "REFUSED")
			contains(t, stderr, "pid=")
		})
	}
}

// `status` REPORTS and exits 0 on a pull it could not make, and says so.
//
// SPEC-MERGE, the verb sentences: "`status`, `dry-run` and `packet` report and exit 0
// whatever the lane holds. `run` is the verb that acts, and `run`'s exit code is about the
// pass, not about the lane" -- with the exit table making "could not run" 2 and "the verb
// ran and said NO" 1. A remote that cannot be reached made `status` exit 1, which is
// neither: the report is still a report, over the lane's own files, and what is owed is
// the NOTE that says the records another machine wrote are not in it. The lock half stays
// exit 2 under rule 22, which is TestAVerbThatCannotTakeTheCheckoutLockExitsTwoAndNamesTheHolder.
func TestAStatusWhosePullFailedStillReportsAndSaysWhatIsMissing(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	// The lane's origin now points at a repository that is not there, which is every
	// unreachable remote: no network, a revoked token, a deleted fork.
	l.git(l.lane, "remote", "set-url", "origin", filepath.Join(l.dir, "no-such-remote.git"))
	exit, stdout, stderr := l.run("status", "--lane", l.lane, "--timeout", "5")
	if exit != 0 {
		t.Fatalf("status reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "STATUS ENTRY kind=pr entry=951")
	contains(t, stderr, "STATUS NOTE")
	contains(t, stderr, "not pulled")
	// `run` is the verb that acts, and it still refuses: deciding on a state it knows is
	// stale is the failure rule 22 exists to close.
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once", "--timeout", "5")
	if exit != 1 {
		t.Fatalf("run on a lane that could not pull: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN REFUSED")
}

// Rule 23: `packet` TAKES NO LOCK.
//
// SPEC-MERGE, rule 23: the packet "is derived from the fold and the host, writes nothing
// and takes no lock." Its fold went through Records.Fold, which takes the checkout lock --
// so a reader asking for their own packet while the coordinator's pass held the checkout
// was answered with exit 2 and a lock refusal, which is the answer given to a writer.
func TestAPacketIsHandedOverWhileTheCheckoutIsHeld(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	release, err := merge.Lock(filepath.Join(l.lane, merge.CheckoutLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--pr", "951", "--who", "emma", "--timeout", "1")
	if exit != 0 {
		t.Fatalf("a packet is a report and takes no lock: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "PACKET ENTRY entry=951")
	contains(t, stdout, "head="+merge.Short(oid))
	absent(t, stderr, "REFUSED")
	// `run` still takes it, and still says who holds it: only the report is lock-free.
	exit, _, stderr = l.run("run", "--lane", l.lane, "--once", "--timeout", "1")
	if exit != 2 {
		t.Fatalf("a verb that acts still waits on the checkout lock: exit %d\n%s", exit, stderr)
	}
	contains(t, stderr, "pid=")
}

// Rule 22: A FOLD WHOSE STATE WRITE COULD NOT HAPPEN SAYS SO, and the pass does not run.
//
// merge.Update's error was assigned to `_`: a state lock another verb held made the fold
// a no-op that said nothing, so the records were pulled, the pass decided on the state as
// it was before them, and state.json kept a fold that never happened. The refusal now
// comes BEFORE the pass -- no RUN PASS line -- and names the holder, which is what rule 2
// asks of a verb that could not take a lock.
func TestAFoldWhoseStateWriteIsRefusedIsNotSilent(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", true)
	release, err := merge.Lock(filepath.Join(l.lane, merge.StateLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once", "--timeout", "1")
	if exit != 2 {
		t.Fatalf("a fold that could not write the state exits 2: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "REFUSED")
	contains(t, stderr, "pulled and folded")
	contains(t, stderr, "pid=")
	// The pass never ran: a fold that did not land is not a state to decide on.
	absent(t, stdout, "RUN PASS")
	absent(t, stdout, "RUN ENTRY")
}

// Read 4b, finding 1 -- rule 23 ("`packet` is derived from the fold and the host") read
// against demanded test 22 ("the fold refuses"): A PACKET NEVER HANDS OVER AN ENTRY WHOSE
// RECORDS THE FOLD REFUSED.
//
// `Packet` computed needs_read from the folded lists and ignored the fold's PROBLEMS
// entirely, so a truncated hold beside two valid approves vanished from the packet: the
// reader was handed the entry, with no note and none of the holds in that file, while
// `run` and `status` on the same lane blocked it as `malformed_record`. A report that
// authorises a read against a record the fold refuses is the silent half of the failure
// the fold's refusal exists to close.
func TestAPacketNeverOffersAnEntryWhoseRecordsTheFoldRefused(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	file := "reads/951/stella-" + oid[:12] + "-20260911T131500Z-abcdef.json"
	l.putRecord(file, `{"who":"stella","verdict":"ho`)
	// The coordinator's pass pulls the file into the checkout and blocks the entry.
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a malformed record blocks its entry: exit %d\n%s", exit, errb)
	}
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--pr", "951", "--who", "emma")
	if exit != 0 {
		t.Fatalf("packet reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	// The file is named, the entry is not handed over, and nothing is counted.
	contains(t, stderr, "FOLD REFUSED file="+file)
	contains(t, stderr, "PACKET NOTE entry=951")
	contains(t, stderr, "malformed_record")
	absent(t, stdout, "PACKET ENTRY")
	contains(t, stdout, "PACKET OK entries=0 holds=0")
}

// The other half of finding 1: a record whose path names no entry makes the SCOPE
// indeterminate, so there is no entry to block instead -- `run` stops before any entry
// line, and a packet hands nothing over either.
func TestAPacketStopsOnARecordWhosePathNamesNoEntry(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	l.putRecord("reads/README.json", "not a record at all\n")
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a record that names no entry stops the pass: exit %d\n%s", exit, errb)
	}
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--who", "emma", "--all")
	if exit != 0 {
		t.Fatalf("packet reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "PACKET STOPPED reason=malformed_record file=reads/README.json")
	absent(t, stdout, "PACKET ENTRY")
	contains(t, stdout, "PACKET OK entries=0 holds=0")
}

// Read 4b, finding 2: A FLUSH DRIVEN AGAINST A PACKET. Rule 22 puts every Git operation on
// a checkout under the checkout lock and rule 23 keeps the lock off `packet`, so the
// packet's fold reads a work tree the CAS loop is restoring bytes into. A record file is
// immutable and the branch only GROWS, so the invariant a reader depends on is that the
// holds a packet shows never go backwards -- a half-written file read once and dropped is
// exactly how they would.
func TestAPacketIsHandedOverCorrectlyWhileAFlushRuns(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "stella",
		"--head", oid, "--verdict", "hold", "--note", "the first finding"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	// A second line keeps recording holds: every one of those is a CAS loop resetting this
	// lane's checkout and writing record bytes back over it.
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 6; i++ {
			who := "reader" + itoa(i)
			if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", who,
				"--head", oid, "--verdict", "hold", "--note", "a finding by "+who); exit != 0 {
				done <- fmt.Errorf("read by %s: exit %d\n%s", who, exit, errb)
				return
			}
		}
		done <- nil
	}()
	holds := 0
	for i := 0; i < 12; i++ {
		exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--pr", "951", "--who", "emma", "--max", "0")
		if exit != 0 {
			t.Fatalf("packet reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		absent(t, stderr, "FOLD REFUSED")
		n := strings.Count(stdout, "PACKET HOLD ")
		if n < holds {
			t.Fatalf("a packet's holds never go backwards: %d after %d\n%s", n, holds, stdout)
		}
		holds = n
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// And the last word is the whole truth: seven holds, all of them.
	_, stdout, _ := l.run("packet", "--lane", l.lane, "--pr", "951", "--who", "emma", "--max", "0")
	if n := strings.Count(stdout, "PACKET HOLD "); n != 7 {
		t.Errorf("every hold recorded is in the packet, got %d\n%s", n, stdout)
	}
}

// Read 4b, finding 3 -- the OTHER half of rule 22's state write: A PASS WHOSE OWN VERDICTS
// COULD NOT BE WRITTEN SAYS SO. The fold's half is pinned above; this is the write after
// the pass, where the lane's file would otherwise keep the state from before it while the
// pass printed its verdicts as though they had landed.
//
// The lock is taken INSIDE the pass -- the fold has already happened, so the refusal can
// only be this write -- by a hand at the clone's first Git operation.
func TestAPassWhoseOwnStateWriteIsRefusedIsNotSilent(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", true)
	var release func()
	taken := false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(dir string, _ []string) {
		if taken || dir != filepath.Join(l.lane, merge.RepoDir) {
			return
		}
		taken = true
		rel, err := merge.Lock(filepath.Join(l.lane, merge.StateLock), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		release = rel
	}}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once", "--timeout", "1")
	if release != nil {
		release()
	}
	if !taken {
		t.Fatal("the hand never reached the clone: this test would have passed by proving nothing")
	}
	if exit != 2 {
		t.Fatalf("a pass whose verdicts could not be written exits 2: got %d\n%s\n%s", exit, stdout, stderr)
	}
	// The pass RAN -- which is what tells this apart from the fold's refusal -- and the
	// refusal is about the write that came after it, with the holder named.
	contains(t, stdout, "RUN PASS")
	contains(t, stderr, "RUN REFUSED: this pass ran and its verdicts could not be written to state.json")
	contains(t, stderr, "pid=")
}

// Read 4b, the delta read: A STATUS NEVER SKIPS A RECORD THE FOLD REFUSED.
//
// `Status` performed the fold and dropped every `Folded.Problems`: the entry came out
// BLOCKED from `plan`, but the FILE was never named, so a reader of `status` -- the one
// verb the remedy line on every `run` points at -- was handed a lane in which a record
// nobody could read had silently gone missing. `run`, `dry-run` and `packet` all announce
// it. The spec's rule is one sentence: a record file that does not decode is NEVER
// skipped.
func TestStatusNamesTheRecordsTheFoldRefused(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	file := "reads/951/stella-" + oid[:12] + "-20260911T131500Z-abcdef.json"
	l.putRecord(file, `{"who":"stella","verdict":"ho`)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a malformed record blocks its entry: exit %d\n%s", exit, errb)
	}
	exit, stdout, stderr := l.run("status", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("status reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "FOLD REFUSED file="+file)
	contains(t, stdout, "STATUS ENTRY kind=pr entry=951")
	contains(t, stdout, "state=BLOCKED")
	contains(t, stdout, "STATUS OK")
}

// The other half: a record whose path names no entry leaves the SCOPE indeterminate.
// `run` stops before any entry is read and `packet` hands nothing over; `status` placed
// every entry in a state computed from a fold it knew was incomplete and said nothing at
// all, because a problem with no entry matches no entry in `plan`.
func TestStatusStopsOnARecordWhosePathNamesNoEntry(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	l.putRecord("reads/README.json", "not a record at all\n")
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a record that names no entry stops the pass: exit %d\n%s", exit, errb)
	}
	exit, stdout, stderr := l.run("status", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("status reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "FOLD REFUSED file=reads/README.json")
	contains(t, stderr, "STATUS STOPPED reason=malformed_record file=reads/README.json")
	absent(t, stdout, "STATUS ENTRY")
	contains(t, stdout, "STATUS OK")
}

// `dry-run` stopped on the indeterminate one but never printed the FOLD REFUSED lines, so
// a survey of a lane with a truncated record in an entry's own directory named no file
// either: the entry read BLOCKED in the plan with nothing to point a hand at.
func TestADryRunNamesTheRecordsTheFoldRefused(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	file := "reads/951/stella-" + oid[:12] + "-20260911T131500Z-abcdef.json"
	l.putRecord(file, `{"who":"stella","verdict":"ho`)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a malformed record blocks its entry: exit %d\n%s", exit, errb)
	}
	exit, stdout, stderr := l.run("dry-run", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("dry-run reports and exits 0 whatever the lane holds: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "FOLD REFUSED file="+file)
	contains(t, stdout, "DRY OK")
}
