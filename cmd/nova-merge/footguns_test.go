package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"strings"
	"testing"
)

// The footguns a new line walked into on a first run, each with the line that would have
// saved them. Lessons 30, 96 and 110: a tool that could not look says so; lesson 15: a
// remedy is a command a person can run.

// FG-1: a failed host or git call became base_state=UNKNOWN and exit 0 with no other line.
// A new line reads STATUS OK over a lane the tool cannot see.
func TestStatusSaysWhenItCouldNotReadTheBase(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.Err = errors.New("gh: could not resolve host github.invalid")
	exit, stdout, stderr := l.run("status", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("status REPORTS and exits 0 whatever the lane holds: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "base_state=UNKNOWN")
	// The NOTE names WHAT could not be read, so UNKNOWN is a sentence and not a shrug.
	contains(t, stderr, "STATUS NOTE")
	contains(t, stderr, "could not be read")
	contains(t, stderr, "could not resolve host")
}

// FG-4: `packet --pr N` for an entry that IS in the lane printed `PACKET OK entries=0
// holds=0`, exit 0, no reason -- and `--all` silently meant "entries with needs_read=yes".
// A coordinator sees one entry of three and concludes the rest need no read.
func TestPacketSaysWhyAnEntryWasSkipped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// One entry that needs a read, one that does not.
	setupPR(t, l, 951, "feature-a", "a.txt", false) // needs_read=no
	oid := l.branch("feature-b", "b.txt", "b\n", "b")
	l.host.PRs[952] = merge.PR{Number: 952, Author: "pat", Base: "main", HeadRef: "feature-b",
		HeadOID: oid, Mergeable: "MERGEABLE"}
	l.host.SetChecks(oid, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "952", "--needs-read"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	// Asked for by name, and skipped: the reason is printed.
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--who", "emma", "--pr", "951")
	if exit != 0 {
		t.Fatalf("packet reports: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "PACKET OK entries=0")
	contains(t, stderr, "PACKET NOTE entry=951")
	contains(t, stderr, "needs_read=no")
	// And --all says how many of the lane it passed over.
	_, stdout, stderr = l.run("packet", "--lane", l.lane, "--who", "emma", "--all")
	contains(t, stdout, "PACKET ENTRY entry=952")
	contains(t, stderr, "PACKET NOTE entry=951")
}

// FG-6: after STOP OK, `status` printed a normal lane and `dry-run` printed stopped=0.
// Only run read the file, so the one signal that says "start nothing new" was invisible to
// every verb a person checks first.
func TestAStoppedLaneSaysSoOnEveryVerb(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("stop", "--lane", l.lane); exit != 0 {
		t.Fatalf("stop: %s", errb)
	}
	for _, args := range [][]string{
		{"status", "--lane", l.lane},
		{"dry-run", "--lane", l.lane},
		{"add", "--lane", l.lane, "--pr", "952"},
		{"packet", "--lane", l.lane, "--who", "emma", "--all"},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, stdout, stderr := l.run(args...)
			if !strings.Contains(stdout+stderr, "a stop file is present in this lane") {
				t.Errorf("%s says nothing about the stop file:\n%s\n%s", args[0], stdout, stderr)
			}
		})
	}
}

// FG-8: an unreachable host was exit 1 -- the code for "the tool ran and said no" -- with
// the whole gh body on one line and a RUN NOTE about a RED entry that did not exist. A
// tool that could not look is exit 2, and its remedy names the call that failed.
func TestAnUnreachableHostIsExitTwoWithABoundedLine(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.Err = errors.New("gh: HTTP 404: Not Found (https://api.github.com/repos/o/n/commits/main/check-runs)\n" +
		strings.Repeat("a very long body the host returned, which is not a line of this tool's grammar. ", 40))
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 2 {
		t.Fatalf("a tool that could not look is exit 2, not 1: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN REFUSED")
	for _, line := range strings.Split(stdout+stderr, "\n") {
		if len(line) > 1200 {
			t.Errorf("a line of %d bytes: the host's body is DATA and it is capped, not pasted whole\n%s", len(line), line[:200])
		}
	}
	// The one remedy is about the host, not about a RED entry the lane does not have.
	absent(t, stdout, "a RED entry needs its failing job read")
}

// FG-9: two remedies were not commands. `gate it with --head <sha> --base-sha <sha>
// --merge <sha> --from <lane>/repo` has no verb, and no --from flag exists on anything.
func TestEveryRemedyIsACommandAPersonCanRun(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "RUN NOTE ") {
			continue
		}
		note := strings.TrimPrefix(line, "RUN NOTE ")
		if !strings.HasPrefix(note, "nova-merge ") {
			t.Errorf("a remedy is a command a person can run, and this one has no verb:\n%s", note)
		}
		if strings.Contains(note, "--from ") {
			t.Errorf("the remedy names --from, which is a flag on no verb of this tool:\n%s", note)
		}
	}
}

// FG-2: `read` recorded and PUSHED an immutable approve for an entry not in the lane, and
// for a head that is not a commit, and said nothing about either. A typo'd entry records an
// approve nothing will ever fold, and the record cannot be taken back.
//
// It is a NOTE and not a refusal because rule 22 says the lane's order is the
// coordinator's and is NOT shared: a reader on another machine has a lane listing none of
// the entries and records for them anyway.
func TestAReadNamesAnEntryThisLaneDoesNotHold(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--branch", "nosuch", "--who", "emma",
		"--head", strings.Repeat("a", 40), "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK")
	contains(t, stderr, "READ NOTE entry=nosuch")
	contains(t, stderr, "does not list it")
}

// And a head the lane's clone does not hold is NAMED. It may simply not be fetched yet, so
// it is a note and not a wall -- but "READ OK ... pushed=true" for forty zeros said
// nothing at all.
func TestAReadNamesAHeadNoCloneHolds(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", strings.Repeat("0", 40), "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stderr, "READ NOTE")
	contains(t, stderr, "no commit with this sha")
}

// FG-5: `add --pr N` defaults needs_read=no -- the direction that merges with zero reads --
// as a field and not a warning, and the number was checked against nothing.
func TestAddSaysWhatNeedsReadNoMeansAndChecksTheNumber(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	_, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", "952")
	contains(t, stdout, "ADD OK")
	contains(t, stderr, "ADD NOTE")
	contains(t, stderr, "needs_read=no")
	contains(t, stderr, "--needs-read")
	// A number no host could ever answer for is refused rather than queued forever, AND
	// THE REFUSAL IS THE ONE FOR THAT MISTAKE: a flag nobody typed is a missing argument,
	// a number typed and wrong is a wrong number, and the two sentences were behind one
	// `*pr < 1` where the second could never print. Asserting only "--pr" could not tell.
	for _, typed := range []string{"0", "-3"} {
		exit, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", typed)
		if exit != 2 {
			t.Fatalf("a pull request number is positive: --pr %s exit %d\n%s\n%s", typed, exit, stdout, stderr)
		}
		contains(t, stderr, "which is positive, got "+typed)
		absent(t, stderr, "refusing to guess")
	}
	// And a --pr nobody typed is the missing-argument sentence, not the number one.
	exit, stdout, stderr := l.run("add", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("add with no --pr: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "refusing to guess")
	absent(t, stderr, "which is positive")
}

// FG-7: `GATE OK ... in_lane=true` means "the ENTRY is in the lane", not "the merge object
// is in the lane's clone" -- which is the phrase the tool's own help uses for the
// predicate. A green gate for a merge commit held by neither the clone nor the remote
// printed in_lane=true newest=true: a green receipt for a useless record.
func TestAGateNamesAMergeObjectNoCloneHolds(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	ghost := strings.Repeat("b", 40)
	exit, stdout, stderr := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", ghost, "--verdict", "green", "--summary", l.summary("ghost"))
	if exit != 0 {
		t.Fatalf("gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "GATE OK entry=951")
	contains(t, stderr, "GATE NOTE")
	contains(t, stderr, "no commit with this sha")
	contains(t, stderr, "will not satisfy")
}

// FG-3: a refused `init` left a half-built lane and poisoned every retry. checkout() ran
// git init, wrote .gitignore, committed and pushed BEFORE merge.Init wrote state.json, and
// cleaned nothing up -- so the second attempt failed with "On branch nova-merge/lane", a
// refusal about nothing, and the directory was permanently un-initializable.
func TestARefusedInitLeavesNothingBehindAndRetriesTheSame(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.urlFor = func(string) string { return filepath.Join(l.dir, "no-such-repository.git") }
	lane := filepath.Join(l.dir, "fresh")
	first := ""
	for i := 0; i < 2; i++ {
		exit, stdout, stderr := l.run("init", "--lane", lane, "--repo", "o/n", "--base", "main",
			"--lane-branch", "nova-merge/lane")
		if exit == 0 {
			t.Fatalf("this fixture points init at a repository that is not there: exit 0\n%s", stdout)
		}
		contains(t, stderr, "INIT REFUSED")
		if i == 0 {
			first = stderr
			// Nothing half-built: the directory init made is gone.
			if _, err := os.Stat(lane); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(lane)
				t.Fatalf("a refused init left %v behind in %s", entries, lane)
			}
			continue
		}
		// THE SAME REFUSAL, the second time: a retry that says something different is a
		// retry that is refusing about the wreckage rather than about the cause.
		if stderr != first {
			t.Errorf("the retry gives a different refusal, so the first one left state behind:\nfirst: %s\nthen:  %s", first, stderr)
		}
	}
}

// FG-10: nothing could be exercised without a live GitHub repository -- the URL was
// hardcoded with no flag and no override -- so a new line could not rehearse, and git's own
// config was the only way in, which means THE ENVIRONMENT could move where a lane pushes.
func TestInitTakesAnExplicitRemoteSoALaneCanBeRehearsed(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// A URL that could never be reached if the flag were ignored.
	l.urlFor = func(string) string { return filepath.Join(l.dir, "no-such-repository.git") }
	lane := filepath.Join(l.dir, "rehearsal")
	exit, stdout, stderr := l.run("init", "--lane", lane, "--repo", "o/n", "--base", "main",
		"--lane-branch", "nova-merge/lane", "--remote", l.remote)
	if exit != 0 {
		t.Fatalf("--remote is what a rehearsal needs: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "INIT OK")
	if got := l.git(lane, "remote", "get-url", "origin"); got != l.remote {
		t.Errorf("the lane's origin is %s, and --remote said %s", got, l.remote)
	}
	if got := l.git(filepath.Join(lane, merge.RepoDir), "remote", "get-url", "origin"); got != l.remote {
		t.Errorf("the lane's clone points at %s, and --remote said %s", got, l.remote)
	}
	// And --remote is refused off the verb that owns it, like every other init flag.
	exit, _, stderr = l.run("status", "--lane", lane, "--remote", l.remote)
	if exit != 2 {
		t.Fatalf("--remote on a verb that is not init is exit 2: got %d\n%s", exit, stderr)
	}
}
