package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The findings of one scenario run over a copy of a real bus, each closed and
// each pinned here from both sides: the failure does not happen, and the behaviour it was
// protecting still does.

// draftFrom is one note's whole text, so that two benches sending in the same second get
// two different ids -- the id is a hash over the note, and these differ in the body.
func draftFrom(who, subject, body string) string {
	return "From: " + who + "\nTo: Bo\nSubject: " + subject + "\n\n" + body + "\n"
}

// ---------------------------------------------------------------- 1. the wedged line

// THE WEDGE. Five lines sent three notes each with `--attempts 3`; six landed, and the
// three lines that lost the race were then stuck, because their own unpushed commit made
// the next `send` refuse with "commits the tool did not make". Here: a send loses its push,
// and the NEXT send runs and carries it.
func TestASendThatLostItsPushDoesNotWedgeTheNextOne(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	other := cloneOf(t, bare)

	// Somebody else lands first, so a one-attempt push cannot.
	writeFile(t, other, "from-bo/2026-09-08T0000Z-theirs-999999999999.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 00:00:00 UTC 2026\nId: bo-999999999999\nSubject: Theirs\n\nA note from the other bench.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "bo: theirs")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	lost := invoke(t, draftFrom("Ada", "The one that lost", "This push cannot land."),
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "1").
		mustCode(t, 1).
		mustContain(t, "stderr", "was NOT pushed").
		mustContain(t, "stderr", "git pull --rebase && git push")
	if strings.Contains(lost.stdout, "SEND OK") {
		t.Fatalf("a lost push reported success: %s", lost.stdout)
	}

	// THE NEXT RUN. This is where the tool refused to run at all.
	invoke(t, draftFrom("Ada", "The one after it", "This one must run."),
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-")

	// Both of Ada's notes are on the bus: the second carried the first.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if n := strings.Count(files, "from-ada/2026-09-09T1234Z-the-one-"); n != 2 {
		t.Fatalf("%d of Ada's two notes reached the bus:\n%s", n, files)
	}

	// The other way: a commit a PERSON made is still refused, because a push publishes the
	// branch and that one would ride along under a note's name.
	writeFile(t, checkout, "notes-to-self.txt", "half a thought\n")
	gitIn(t, checkout, "add", "notes-to-self.txt")
	gitIn(t, checkout, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit", "-q", "-m", "wip")
	invoke(t, draftFrom("Ada", "After somebody elses work", "Should be refused."),
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND REFUSED: ").
		mustContain(t, "stderr", "1 of which the tool did not make").
		mustContain(t, "stderr", "git pull --rebase && git push")
}

// --attempts has a default, and it is the number the scenario measured. A send that names
// none is not a refusal.
func TestTheRetryBudgetHasAMeasuredDefault(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=ada-")
	if defaultAttempts < 25 {
		t.Fatalf("the default budget is %d; five lines sending at once consumed nine attempts at the peak and three landed under a budget of three", defaultAttempts)
	}
}

// ---------------------------------------------------------------- 2. no conflict wedges a line

// TWO BENCHES OF ONE LANE, RACING. This is the shape that wedged a bench: both append to
// the lane's INDEX in the same commit as their note, so the rebase conflicts, and a person's
// own `git pull --rebase` then landed in a half-done rebase with `UU from-<lane>/INDEX`.
// Twenty sends, ten rounds of two, and every one of them lands.
func TestTwoClonesOfOneLaneRacingTenRoundsAllLand(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	second := cloneOf(t, bare)

	const rounds = 10
	benches := []string{checkout, second}
	for round := range rounds {
		var wg sync.WaitGroup
		results := make([]result, len(benches))
		for i, dir := range benches {
			wg.Add(1)
			go func(i int, dir string) {
				defer wg.Done()
				subject := fmt.Sprintf("Round %d from bench %d", round, i)
				body := fmt.Sprintf("Bench %d, round %d, sent at once with the other.", i, round)
				results[i] = invoke(t, draftFrom("Ada", subject, body),
					"send", "--bus", dir, "--stdin", "--remote", "origin", "--branch", "main")
			}(i, dir)
		}
		wg.Wait()
		for i, r := range results {
			if r.code != 0 {
				t.Fatalf("round %d, bench %d: exit %d\nstdout: %s\nstderr: %s", round, i, r.code, r.stdout, r.stderr)
			}
		}
	}

	// 20/20 on the bus.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if n := strings.Count(files, "from-ada/2026-09-09T1234Z-round-"); n != rounds*2 {
		t.Fatalf("%d of %d notes reached the bus:\n%s", n, rounds*2, files)
	}
	// And the catalogue names every one of them: the union kept both sides' lines every
	// time, rather than one bench's replacing the other's.
	index := gitIn(t, bare, "show", "main:from-ada/INDEX")
	if n := strings.Count(index, "from-ada/2026-09-09T1234Z-round-"); n != rounds*2 {
		t.Fatalf("the catalogue holds %d of %d lines:\n%s", n, rounds*2, index)
	}
	if strings.Contains(index, "<<<") {
		t.Fatalf("conflict markers reached the catalogue:\n%s", index)
	}

	// BOTH TREES ARE CLEAN AND NEITHER IS MID-REBASE, which is the whole of "no conflict
	// wedges a line": a bench a person has to rescue is a bench that lost.
	for i, dir := range benches {
		if branch := strings.TrimSpace(gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
			t.Fatalf("bench %d is on %q, not a branch a person can work from", i, branch)
		}
		if out := gitIn(t, dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
			t.Fatalf("bench %d was left dirty:\n%s", i, out)
		}
		gd := strings.TrimSpace(gitIn(t, dir, "rev-parse", "--absolute-git-dir"))
		for _, name := range []string{"rebase-merge", "rebase-apply"} {
			if _, err := os.Stat(filepath.Join(gd, name)); !os.IsNotExist(err) {
				t.Fatalf("bench %d was left in a rebase (%s)", i, name)
			}
		}
	}
	// The bus itself is still valid.
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// And the union rule is on the bus, so a person's own `git pull --rebase` gets the
	// same settlement this tool gave itself.
	attrs := gitIn(t, bare, "show", "main:"+bus.AttributesName)
	for _, want := range []string{"from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		if !strings.Contains(attrs, want) {
			t.Fatalf("%s does not carry %q:\n%s", bus.AttributesName, want, attrs)
		}
	}
}

// ---------------------------------------------------------------- 3. addressed to nobody

// 22 notes on the real bus were in no inbox and were not UNREADABLE either: they parsed,
// and their To line named nobody. Nothing ever told anyone they were there.
func TestANoteAddressedToNobodyIsNamed(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	const toNobody = "from-bo/2026-09-08T0100Z-team.md"
	writeFile(t, checkout, toNobody,
		"From: Bo\nTo: Team\nDate: Tue Sep  8 01:00:00 UTC 2026\nSubject: The wire task\n\nA note addressed to a name no roster holds.\n")
	commitAs(t, checkout, "Bo", "bo: a note to nobody")

	// EVERY reader sees it on a full read, because it is a fact about the bus.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX UNADDRESSED path="+toNobody+": ").
		mustContain(t, "stdout", `"Team"`).
		mustContain(t, "stdout", "unaddressed=1")

	// AND ITS OWN SENDER SEES IT ON EVERY RUN, whatever the mode, because that is the one
	// person who can fix it. Bo reads incrementally from a cursor.
	invoke(t, "", advance(checkout, "Bo")...).mustCode(t, 0)
	writeFile(t, checkout, "from-bo/2026-09-08T0200Z-also-team.md",
		"From: Bo\nTo: Team\nDate: Tue Sep  8 02:00:00 UTC 2026\nSubject: Also the wire task\n\nAnother one to nobody.\n")
	commitAs(t, checkout, "Bo", "bo: another to nobody")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Bo", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX UNADDRESSED path=from-bo/2026-09-08T0200Z-also-team.md: ")

	// The other way: an ordinary note reaches somebody and is NOT reported as unaddressed,
	// and neither is one that reaches somebody as well as nobody.
	writeFile(t, checkout, "from-bo/2026-09-08T0300Z-partly.md",
		"From: Bo\nTo: Ada, Team\nDate: Tue Sep  8 03:00:00 UTC 2026\nSubject: Partly addressed\n\nThis one reaches Ada.\n")
	commitAs(t, checkout, "Bo", "bo: partly addressed")
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full").mustCode(t, 0)
	if strings.Contains(r.stdout, "2026-09-08T0300Z-partly.md: ") && strings.Contains(r.stdout, "INBOX UNADDRESSED path=from-bo/2026-09-08T0300Z-partly.md") {
		t.Fatalf("a note that reaches Ada was reported as reaching nobody:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "INBOX NOTE id=- from=Bo addr=to at=2026-09-08T03:00:00Z path=from-bo/2026-09-08T0300Z-partly.md") {
		t.Fatalf("the partly-addressed note is not in Ada's inbox:\n%s", r.stdout)
	}
}

// ---------------------------------------------------------------- 5. the output, read by a person

// The verb whose whole job is to tell a person how to spell a To line was printing names
// nobody could paste: oneline.Field escapes every space, so "Ada Vale" came out
// `Ada\x20Claude` and `send` refuses that. The names are quoted now, and the proof is
// that what comes out of `names` goes into a draft the tool accepts.
func TestNamesPrintsSomethingASendWillAccept(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", "names", "--bus", checkout).mustCode(t, 0)
	if strings.Contains(r.stdout, `\x20`) {
		t.Fatalf("a name came out with its spaces escaped, which nobody can paste into a To line:\n%s", r.stdout)
	}
	// Lift an alias straight out of the output, quotes and all, and send to it.
	const want = `aliases="Ada Vale"`
	if !strings.Contains(r.stdout, want) {
		t.Fatalf("the aliases are not quoted as %s:\n%s", want, r.stdout)
	}
	invoke(t, "From: Bo\nTo: Ada Vale\nSubject: Pasted from names\n\nThe spelling came out of the names verb.\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=bo-")
	// The one-line guarantee still holds: every line of the output is one line.
	for _, line := range strings.Split(strings.TrimRight(r.stdout, "\n"), "\n") {
		if !strings.HasPrefix(line, "NAMES ") {
			t.Fatalf("a value broke the output into a line that is not an event: %q", line)
		}
	}
}

// A rebase conflict this tool will not settle used to arrive as git's whole transcript
// inlined into the reason and escaped: forty lines of git as one line of \x0d\x0a. The
// actionable line is one line; the transcript follows it, as git wrote it.
func TestARebaseConflictPrintsOneActionableLineAndTheTranscriptRaw(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	second := cloneOf(t, checkoutRemote(t, checkout))

	// ONE NOTE PATH FROM TWO BENCHES, with different bytes in it. The id is a hash over the
	// note's RESOLVED recipients, so "Bo" and "Bo Codex" are one recipient and these
	// two drafts are assigned one id -- and the To line itself is the author's own words and
	// is round-tripped verbatim, so the two files differ. Same path, different content, from
	// two benches that cannot see each other: which of the two is the note is a person's
	// decision and this tool will not make it.
	const subject = "The very same note"
	const body = "Written twice in one second by two benches."
	mine := "From: Ada\nTo: Bo\nSubject: " + subject + "\n\n" + body + "\n"
	theirs := "From: Ada\nTo: Bo Codex\nSubject: " + subject + "\n\n" + body + "\n"
	invoke(t, mine, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	r := invoke(t, theirs, "send", "--bus", second, "--stdin", "--remote", "origin", "--branch", "main").mustCode(t, 1)

	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "SEND FAIL from-ada/") {
		t.Fatalf("the first line of stderr is not the event line:\n%s", r.stderr)
	}
	for _, want := range []string{"conflicted", "was NOT pushed", "git pull --rebase"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("the actionable line does not say %q:\n%s", want, lines[0])
		}
	}
	// The event line is ONE line and carries no transcript.
	if strings.Contains(lines[0], `\x0d`) || strings.Contains(lines[0], `\x0a`) {
		t.Fatalf("git's transcript was escaped into the event line:\n%s", lines[0])
	}
	// The transcript is under it, unescaped, and is git's own words.
	rest := strings.Join(lines[1:], "\n")
	if !strings.Contains(rest, "CONFLICT") {
		t.Fatalf("git's own transcript is not on stderr under the event line:\n%s", r.stderr)
	}
	if len(lines) < 2 {
		t.Fatalf("the transcript is one line, so it was folded after all:\n%s", r.stderr)
	}
}

// A --bus that is a subdirectory of a bigger repository refused with "participants.json:
// no such file" -- true, and not the caller's mistake. The root check runs first, so the
// sentence that fixes the invocation is the one seen.
func TestASubdirectoryBusIsRefusedByItsRootAndNotByItsRoster(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	nested := filepath.Join(checkout, "docs", "bus")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"check", "--bus", nested, "--as", "Ada"},
		{"inbox", "--bus", nested, "--as", "Ada", "--receipt-max-words", "40"},
	} {
		r := invoke(t, "", args...).mustCode(t, 2).
			mustContain(t, "stderr", "is not its root").
			mustContain(t, "stderr", "would report an empty change set over unread notes")
		if strings.Contains(r.stderr, "participants.json") {
			t.Fatalf("%s: the roster refusal fired first, so the good sentence is not the one seen:\n%s", args[0], r.stderr)
		}
	}
	// The other way: a bus that IS a root and has no roster is refused for the roster,
	// which is then the true reason.
	bareRoot := t.TempDir()
	gitIn(t, bareRoot, "init", "--quiet", "-b", "main")
	invoke(t, "", "check", "--bus", bareRoot, "--as", "Ada").
		mustCode(t, 2).mustContain(t, "stderr", "participants.json")
}

// The two counts said different numbers on two lines with nothing saying why: `carrying=`
// is the whole open list and `open=` is what still waits on you, and they differ by exactly
// the heard notes. Both are now on the OK line, under the names they are printed under
// elsewhere, beside the decomposition that makes them add up.
func TestTheCarryingAndOpenCountsSayWhatTheyCount(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main").mustCode(t, 0)
	// A cursor first, so the read below is the incremental one a person actually runs.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	// Two notes on the list, one of them heard: carrying counts it, open does not.
	for _, want := range []string{
		"INBOX SCOPE mode=since",
		"INBOX OPEN carrying=2 heard=1",
		"INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, r.stdout)
		}
	}
	// All three lines agree about `carrying=`, which is the whole point: one number under
	// one name, and `open=` beside it saying what it leaves out.
	scope := field(t, r.stdout, "INBOX SCOPE mode=since cursor=")
	_ = scope
	for _, line := range strings.Split(r.stdout, "\n") {
		if !strings.Contains(line, "carrying=") {
			continue
		}
		if got := field(t, line, "carrying="); got != "2" {
			t.Fatalf("a line says carrying=%s where the open list holds 2: %q", got, line)
		}
	}
}

// ---------------------------------------------------------------- 6, 7. the guards

// The subprocess budget is a flag, and a value that is not a budget is a bad invocation
// rather than a run with no budget at all.
//
// NOT PARALLEL. `--git-timeout 5` does not set a budget for this run: it calls
// bus.SetGitTimeout, which writes a package-level atomic that EVERY git call in the process
// reads. Run beside the dozen other git tests this package now runs at once, it would cut
// their budget to five seconds under them -- a timeout they would report as the tool
// hanging, on whichever test happened to be slowest on a loaded runner, and never here.
// It is the same class as the NoteParses and checkoutLockWait tests: process-global state,
// so it runs alone.
func TestGitTimeoutIsAFlagAndIsChecked(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "check", "--bus", checkout, "--full", "--git-timeout", "0").
		mustCode(t, 2).mustContain(t, "stderr", "--git-timeout is a whole number of seconds and at least 1")
	invoke(t, "", "check", "--bus", checkout, "--full", "--git-timeout", "5").
		mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
}

// TWO CONCURRENT INVOCATIONS on one checkout. The second waits and then refuses with a
// sentence, rather than writing the same OPEN list from underneath the first.
func TestASecondInvocationOnOneCheckoutRefuses(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	// The binary waits ten seconds; a test that waited ten seconds would assert the same
	// thing and take ten seconds to do it.
	real := checkoutLockWait
	checkoutLockWait = 200 * time.Millisecond
	t.Cleanup(func() { checkoutLockWait = real })

	held := make(chan struct{})
	done := make(chan struct{})
	var first error
	go func() {
		defer close(done)
		release, err := bus.LockCheckout(checkout, time.Second)
		first = err
		close(held)
		if err != nil {
			return
		}
		time.Sleep(600 * time.Millisecond)
		release()
	}()
	<-held
	if first != nil {
		t.Fatalf("the first run could not take the lock: %v", first)
	}
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND REFUSED: ").
		mustContain(t, "stderr", "another nova-bus is already running on this checkout").
		mustContain(t, "stderr", "run this again when that one has finished")
	<-done

	// The other way: with nothing holding it, the same send runs.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=ada-")
}

// ---------------------------------------------------------------- helpers

// cloneOf is a second checkout of one bus: the other bench, which cannot see this one.
func cloneOf(t *testing.T, bare string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bench")
	gitIn(t, filepath.Dir(dir), "clone", "--quiet", bare, dir)
	gitIn(t, dir, "checkout", "-q", "-B", "main")
	return dir
}

// checkoutRemote is the bare repository a checkout pushes to, for a test that was handed
// the checkout and needs the other end of it.
func checkoutRemote(t *testing.T, checkout string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, checkout, "remote", "get-url", "origin"))
}
