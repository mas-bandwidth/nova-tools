package pulse

// Johnny's review of `nova-pulse launch`/`harvest` at c732de08, one test per issue, each
// built from that issue's own receipt (nova-tools #1818-#1825; #1826 is the security twin
// of #1824 and #1825 measured here too).
//
//	#1818  harvest --id ignored the pulse table launch wrote
//	#1819  rule 15's relaunch invoked launch without --slots/--deadline
//	#1820  launch queued a 3-field row; relaunch read a 5-field one and printed POOL EMPTY
//	#1821  --max was on the usage line, parsed, and ignored
//	#1822  a 120 s native.log age took a slot the lock files said was free
//	#1823  harvest scored line 1 by equality; the batch gather scores it by prefix
//	#1824  harvest pushed to whatever REPO/BRANCH the worker wrote in RESULT.md
//	#1825  launch interpolated --root into a --then string nova-swarm runs with sh -c

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// #1818. The receipt: launch writes <root>/cards/<id>/cards.tsv, harvest --id reads
// <root>/cards.tsv, and the chained `--then` harvest of a successful pulse refuses with
// "run: nova-pulse cut" -- the wrong door, because launch had already written the table.
func TestHarvestIDFoldsThePulseTableLaunchWrote(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "view", Exit: 1},
		{Arg: 2, Equals: "create", Stdout: "https://forge.invalid/owner/repo/pull/7"},
	}})

	// Exactly what launch leaves behind: the admitted table under cards/<id>/, the job
	// with its RESULT.md, and NO <root>/cards.tsv.
	id := "20260919T174653Z-pulse-0be2be"
	card := filepath.Join(root, "cardsrc", "i1.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("RESULT i1 sha=000000000000\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitted := pulseCardsPath(root, id)
	if err := os.MkdirAll(filepath.Dir(admitted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admitted, []byte("i1\t1\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, "1", "jobs", "i1")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT i1 sha=000000000000\nDONE\nBRANCH rowan/review-i1\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cards.tsv")); err == nil {
		t.Fatal("this fixture must have no <root>/cards.tsv: that is the point")
	}

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})

	if strings.Contains(errb.String(), "HARVEST REFUSED") {
		t.Fatalf("harvest refused the pulse launch admitted: %s", errb.String())
	}
	if !strings.Contains(out.String(), "done=1") || !strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("the pulse table was not folded (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}

// #1818's other half: when neither table nor a job directory is there, the refusal names the
// path launch would have written, not `nova-pulse cut`.
func TestHarvestRefusalNamesThePulseTable(t *testing.T) {
	root := t.TempDir()
	fakePATH(t)
	var out, errb bytes.Buffer
	if code := Harvest(HarvestInput{ID: "p-nope", Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb}); code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if !strings.Contains(errb.String(), filepath.Join("cards", "p-nope", "cards.tsv")) {
		t.Fatalf("the refusal does not name the pulse table: %s", errb.String())
	}
}

// #1821. The receipt: `--max 1` with three rows admitted all three (`n=3`).
func TestLaunchHonoursMax(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 3)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 3, Deadline: "120", Max: 1, Now: theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	if !strings.Contains(out, " n=1 ") {
		t.Fatalf("--max 1 admitted more than one card: %q", out)
	}
	if !strings.Contains(errb, "PULSE NOTE max=1") {
		t.Fatalf("a truncated pulse must say so: %q", errb)
	}
	admitted := pulseCardsPath(root, pulseID(t, out))
	body, err := os.ReadFile(admitted)
	if err != nil {
		t.Fatalf("the admitted table: %v", err)
	}
	if rows := len(nonempty(string(body))); rows != 1 {
		t.Fatalf("the batch was handed %d cards under --max 1:\n%s", rows, body)
	}
	_ = argvLog
}

// --max 0 still means all, the repo's own convention.
func TestLaunchMaxZeroMeansAll(t *testing.T) {
	root := t.TempDir()
	fakeSwarm(t, filepath.Join(root, "argv.log"))
	cards, _ := writeCards(t, root, 3)
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 3, Deadline: "120", Max: 0, Now: theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; %s", code, errb)
	}
	if !strings.Contains(out, " n=3 ") {
		t.Fatalf("--max 0 must admit all three: %q", out)
	}
}

// #1822. The receipt: slot 1 with NO lock file and a fresh native.log refused the pulse;
// backdating the log's mtime, and nothing else, let it through.
func TestLaunchSlotFreeFromLockFilesOnly(t *testing.T) {
	root := t.TempDir()
	fakeSwarm(t, filepath.Join(root, "argv.log"))
	cards, _ := writeCards(t, root, 1)

	pool := filepath.Join(root, "pool", "1")
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pool, "native.log"), []byte("live\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 1, Deadline: "120",
		Now: func() time.Time { return time.Now().UTC() },
	})
	if code != 0 {
		t.Fatalf("a fresh native.log took a slot no lock file holds (SPEC-PULSE rule 8: never a log age); exit=%d stderr=%s", code, errb)
	}
	if !strings.Contains(out, "free-before=1") {
		t.Fatalf("the slot must be free from its lock files: %q", out)
	}
}

// The other direction, so the fix is not just "count everything free": a lock file that is
// not state=free still holds its slot, log or no log.
func TestLaunchLockFileStillHoldsItsSlot(t *testing.T) {
	root := t.TempDir()
	fakeSwarm(t, filepath.Join(root, "argv.log"))
	cards, _ := writeCards(t, root, 1)

	slots := filepath.Join(root, "pool", "slots")
	if err := os.MkdirAll(slots, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slots, "1.json"), []byte(`{"state":"live"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 1, Deadline: "120", Now: theHour,
	})
	if code != 2 || !strings.Contains(errb, "UNDER-SLOTS") {
		t.Fatalf("a live lock must still hold its slot; exit=%d stderr=%q", code, errb)
	}
}

// #1823. The receipt: contract `RESULT cardp sha=bbb`, RESULT.md line 1 `RESULT cardp
// sha=bbb extra from worker`. The batch gather scores that done (SPEC-SWARM:1482-1491);
// harvest scored it mismatch=1 and pushed nothing.
func TestHarvestScoresLine1ByPrefixLikeTheGather(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/8")

	addCard(t, root, "cardp", "1", "flash", "RESULT cardp sha=bbb",
		"RESULT cardp sha=bbb extra from worker\nDONE\nBRANCH bp\nREPO owner/repo\n")

	out, errb := runHarvest(t, root)
	if !strings.Contains(out, "done=1") || !strings.Contains(out, "pushed=1") {
		t.Fatalf("a line 1 that BEGINS with the contract is this card and is done:\n%s\n%s", out, errb)
	}
	if !strings.Contains(out, "mismatch=0") {
		t.Fatalf("want mismatch=0:\n%s", out)
	}
}

// And the prefix rule does not become "anything": a line 1 that is not the contract's
// prefix is still a mismatch, and the contract is not allowed to be empty.
func TestHarvestStillRefusesAWrongLine1(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/9")

	addCard(t, root, "cardq", "1", "flash", "RESULT cardq sha=bbb",
		"RESULT cardq sha=ccc\nDONE\nBRANCH bq\nREPO owner/repo\n")

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "mismatch=1") || !strings.Contains(out, "pushed=0") {
		t.Fatalf("a different sha is still a mismatch:\n%s", out)
	}
}

// #1824. The receipt, verbatim: a planted RESULT.md naming `example/exfil` was pushed to
// `example/exfil` and a draft PR was opened on it. The card is for owner/repo.
func TestHarvestRefusesAPushToARepoTheCardDoesNotName(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/example/exfil/pull/99")

	addCard(t, root, "i1", "1", "flash", "RESULT i1 sha=000000000000",
		"RESULT i1 sha=000000000000\nDONE\nBRANCH rowan/review-i1\nREPO example/exfil\n")

	out, errb := runHarvest(t, root)
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("a worker's REPO line chose the remote:\n%s\n%s", out, errb)
	}
	if !strings.Contains(out, "refused=1") {
		t.Fatalf("want refused=1:\n%s", out)
	}
	if !strings.Contains(errb, "example/exfil") || !strings.Contains(errb, "owner/repo") {
		t.Fatalf("the refusal must name both the claim and the card's own repo: %q", errb)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.Contains(l, "pr create") {
			t.Fatalf("nothing may reach a remote: %s", l)
		}
	}
}

// The same card with the repo it really belongs to still pushes: the guard is a check, not
// a wall.
func TestHarvestPushesWhenTheRepoIsTheCardsOwn(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/10")

	addCard(t, root, "i2", "1", "flash", "RESULT i2 sha=000000000000",
		"RESULT i2 sha=000000000000\nDONE\nBRANCH rowan/review-i2\nREPO owner/repo\n")

	out, errb := runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") {
		t.Fatalf("the card's own repo must still push:\n%s\n%s", out, errb)
	}
}

// #1825. The receipt: a root path with a space in it produced a --then string that `sh -c`
// splits in two, so harvest's --root became the first half. A `$`, `;` or backtick would
// have run something else. The test runs the string through a real shell.
func TestLaunchThenSurvivesAShellAndAPathWithASpace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("--then is run with sh -c; the round trip needs a POSIX shell")
	}
	base := t.TempDir()
	root := filepath.Join(base, "M space", "a;b $x", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	argvLog := filepath.Join(base, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 1, Deadline: "30", Now: theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; %s", code, errb)
	}

	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	_, then, ok := strings.Cut(line, "--then ")
	if !ok {
		t.Fatalf("no --then in the batch argv: %q", line)
	}

	// What nova-swarm does with that string: sh -c. Swapping the verb for a printf lets
	// a real shell do the word splitting and hand back the words it produced -- which is
	// the only thing under test here. The flags and the values are untouched.
	words, ok := strings.CutPrefix(then, "nova-pulse harvest ")
	if !ok {
		t.Fatalf("--then is not the harvest call: %q", then)
	}
	out, err := exec.Command("sh", "-c", `printf '%s\n' `+words).Output()
	if err != nil {
		t.Fatalf("sh could not run the --then fields: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(got) != 4 {
		t.Fatalf("sh -c split the --then string into %d words, want 4 (--id <id> --root <root>): %q", len(got), got)
	}
	if got[0] != "--id" || got[2] != "--root" {
		t.Fatalf("the harvest call is not --id <id> --root <root>: %q", got)
	}
	if got[3] != root {
		t.Fatalf("--root reached harvest as %q, want %q (sh -c split the path)", got[3], root)
	}
	// Nothing in the path was executed, and nothing was expanded: `$x` and `;` are
	// literal characters in a directory name, not shell.
	if _, err := os.Stat(filepath.Join(base, "x")); err == nil {
		t.Fatal("a metacharacter in the root path ran a command")
	}
}

// #1819. The receipt: harvest folded a card, then ran `nova-pulse launch --cards … --root …
// --queue` with no --slots and no --deadline, and launch refused it twice over. The width
// and the deadline are not guessed: the pulse being harvested recorded them.
func TestRelaunchPassesTheWidthAndDeadlineThePulseRanWith(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/11")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE OK id=p2 n=1 free-before=1 queued=0 batches=1 deadline=300",
	}})
	writePulseTable(t, root, "p1", 1, 6, "900")
	addCard(t, root, "done", "1", "flash", "RESULT done sha=ddd", "RESULT done sha=ddd\nDONE\nBRANCH bd\nREPO owner/repo\n")
	queueCard(t, root, "q1")

	out, errb := runHarvest(t, root)
	if strings.Contains(errb, "launch failed") {
		t.Fatalf("the relaunch was refused by launch: %s", errb)
	}
	line := ""
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "nova-pulse launch") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no relaunch in the argv log:\n%s\n%s", out, errb)
	}
	for _, want := range []string{"--slots 6", "--deadline 900", "--queue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the relaunch argv lacks %q: %s", want, line)
		}
	}
}

// #1819's other half: a pulse table that records neither -- every row written before this
// change -- makes harvest SAY it is not pulsing again, rather than invoke a launch that can
// only refuse.
func TestRelaunchSaysSoWhenThePulseShapeIsUnrecorded(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/12")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog})
	// The two-field row of every pulse before #1819.
	if err := os.WriteFile(filepath.Join(root, "pulses", "p1.tsv"), []byte("pulse-p1\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addCard(t, root, "done", "1", "flash", "RESULT done sha=ddd", "RESULT done sha=ddd\nDONE\nBRANCH bd\nREPO owner/repo\n")
	queueCard(t, root, "q1")

	_, errb := runHarvest(t, root)
	if !strings.Contains(errb, "PULSE NOTE relaunch skipped") {
		t.Fatalf("harvest must say it is not pulsing again: %q", errb)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "nova-pulse launch") {
			t.Fatalf("a launch that can only refuse must not be invoked: %s", l)
		}
	}
}

// #1820. The receipt: launch queued a card as `label<TAB>model<TAB>card`, relaunch read it
// with a five-field candidate reader that skips any row under four fields, and harvest
// printed `PULSE POOL EMPTY` over the top of a card that was waiting.
func TestRelaunchAdmitsTheQueueLaunchActuallyWrote(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/13")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE OK id=p2 n=1 free-before=1 queued=0 batches=1 deadline=300",
	}})
	addCard(t, root, "done", "1", "flash", "RESULT done sha=ddd", "RESULT done sha=ddd\nDONE\nBRANCH bd\nREPO owner/repo\n")
	card := queueCard(t, root, "g2")

	out, errb := runHarvest(t, root)
	if strings.Contains(out, "PULSE POOL EMPTY") {
		t.Fatalf("the pool is not empty: a queued card is waiting\n%s\n%s", out, errb)
	}
	raw, err := os.ReadFile(filepath.Join(root, "cards.tsv"))
	if err != nil {
		t.Fatalf("the relaunch must cut the queued card into cards.tsv: %v", err)
	}
	rows := nonempty(string(raw))
	if len(rows) == 0 || !strings.Contains(rows[0], card) {
		t.Fatalf("the queued card is not the relaunch's FIRST row (queue first, SPEC-PULSE rule 15): %q", rows)
	}
	// The queue was taken, not deleted: its rows are in the table being launched and the
	// file they came from is parked beside it.
	if _, err := os.Stat(filepath.Join(root, "queue.tsv")); err == nil {
		t.Fatal("the queue was handed to launch and must not still be pending")
	}
	taken, err := filepath.Glob(filepath.Join(root, "queue.tsv.taken-*"))
	if err != nil || len(taken) != 1 {
		t.Fatalf("the taken queue must be parked, never deleted: %v %v", taken, err)
	}
}

// #1818, the sharp half. Folding the right TABLE is not the same as folding the right
// CARDS: when harvest could not find a table it fell through to the bare-swarm-root
// discovery, which takes each RESULT.md's OWN line 1 as the contract -- so line 1 always
// matches and the card's real contract is never checked at all. With the pulse table read,
// a RESULT.md whose line 1 is not the card's contract is a mismatch, as it must be.
func TestHarvestIDChecksThePulseTablesOwnContract(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "view", Exit: 1},
		{Arg: 2, Equals: "create", Stdout: "https://forge.invalid/owner/repo/pull/14"},
	}})

	id := "20260919T174653Z-pulse-wrong"
	card := filepath.Join(root, "cardsrc", "i9.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("RESULT i9 sha=cafebabe0000\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitted := pulseCardsPath(root, id)
	if err := os.MkdirAll(filepath.Dir(admitted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admitted, []byte("i9\t1\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, "1", "jobs", "i9")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	// A RESULT.md for a DIFFERENT revision than the card asked for.
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT i9 sha=0000deadbeef\nDONE\nBRANCH rowan/i9\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})

	if !strings.Contains(out.String(), "mismatch=1") || !strings.Contains(out.String(), "pushed=0") {
		t.Fatalf("the card's own contract, from the pulse table, was not the one checked:\n%s\n%s", out.String(), errb.String())
	}
}
