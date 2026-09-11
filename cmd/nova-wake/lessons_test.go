package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The two lessons of 2026-09-11, pinned by name (work list item 9). A spec
// whose lessons are not pinned by a test is a spec that will buy them again.

// TestNoFalseWakeOnReload is the second lesson: a state value round-trips or it
// is not state. The state for one watched entry was stored as a tab-joined
// string whose last field was often empty; reloading it dropped the trailing
// empty field, so the reloaded value never equalled the freshly computed one,
// every poll reported a change, and the watcher woke the window every interval,
// forever, with nothing to say. A false wake is worse than a missed one,
// because the window learns to stop reading.
//
// It is written over ALL FOUR kinds at once rather than over the entry alone,
// because the failure was in the codec and the codec is shared.
func TestNoFalseWakeOnReload(t *testing.T) {
	busDir, ghDir := fakes(t)
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=n1 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/one.md: a note with a trailing empty subject\n"+
			"INBOX OK as=Rowan carrying=1 open=1 notes=1 receipts=0\n")
	// An entry whose value ends in an EMPTY field: no failing checks, which is
	// the common case and was the one the tab-joined value lost.
	entryJSON(t, ghDir, "942", "OPEN", [2]string{"build", "SUCCESS"})
	reports := t.TempDir()
	write(t, filepath.Join(reports, "job", "RESULT.md"), "# a finding\n")
	bus := newBusCheckout(t)
	commitAs(t, bus, "Johnny", at.Add(-time.Minute))
	state := filepath.Join(t.TempDir(), "wake.state")

	args := []string{"watch", "--state", state, "--max", "20s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s",
		"--bus", bus, "--as", "Rowan", "--receipt-max-words", "40",
		"--entry", "mas-bandwidth/schema#942", "--reports", reports, "--line", "Johnny"}

	first := wakeRun(t, args...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if !strings.Contains(first.stdout, "WAKE CHANGE") {
		t.Fatalf("the first call must report the note and the line:\n%s", first.stdout)
	}

	// Nothing has moved. The state is reloaded from disk, every value is
	// computed again, and every comparison must be equal BYTE FOR BYTE.
	second := wakeRun(t, args...)
	if second.exit != 0 {
		t.Fatalf("exit = %d; %s", second.exit, second.all())
	}
	if !strings.Contains(second.stdout, "WAKE QUIET") {
		t.Fatalf("THE FALSE WAKE OF 2026-09-11: a poll over an unchanged world reported a change after a reload.\n%s", second.all())
	}
	for _, token := range []string{"WAKE BUS ", "WAKE ENTRY ", "WAKE REPORT ", "WAKE LINE "} {
		if strings.Contains(second.stdout, token) {
			t.Errorf("a %sline was printed over an unchanged world:\n%s", token, second.stdout)
		}
	}
	// And a third, because a value that survives one reload and not two is the
	// same bug one poll further away.
	third := wakeRun(t, args...)
	if !strings.Contains(third.stdout, "WAKE QUIET") {
		t.Errorf("the third call was not quiet either:\n%s", third.all())
	}
}

// TestBlocksRatherThanTicks is the first lesson: a watcher BLOCKS, it does not
// tick. One call, one return, one turn per change. The clock lives inside the
// tool, where a poll costs a subprocess and not a turn -- the window that
// bought this ran a five-minute sleep tick, and every tick was a model turn on
// the most expensive model in the fleet, spent learning that the world was
// exactly as it had been left.
func TestBlocksRatherThanTicks(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "20m", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d, want 0 (the deadline is not an error); %s", r.exit, r.all())
	}

	// ONE return, ONE verdict line. Not 240 returns of "nothing yet".
	verdicts := 0
	for _, line := range strings.Split(r.stdout, "\n") {
		toks := strings.Fields(line)
		if len(toks) >= 2 && toks[0] == "WAKE" {
			switch toks[1] {
			case "CHANGE", "QUIET", "BROKEN":
				verdicts++
			}
		}
	}
	if verdicts != 1 {
		t.Errorf("%d verdict lines, want exactly 1: one call, one return, one turn per change\n%s", verdicts, r.stdout)
	}
	if got := r.clock.Now().Sub(at); got != 20*time.Minute {
		t.Errorf("the call returned after %s, want its whole --max of 20m", got)
	}

	// The polling happened INSIDE the call: 240 polls of the bus at --interval
	// 5s over twenty minutes, all of them subprocesses and none of them a turn.
	polls := 0
	for _, c := range calls(t, busDir) {
		if strings.HasPrefix(c, "inbox ") {
			polls++
		}
	}
	if polls != 240 {
		t.Errorf("the bus was polled %d times inside the one call, want 240", polls)
	}
	if !strings.Contains(r.stdout, "WAKE at=") {
		t.Errorf("the opening line must print BEFORE anything is waited on: a tool call that prints nothing for twenty minutes and then prints everything is, while it runs, indistinguishable from one that has hung\n%s", r.stdout)
	}
}

// ---------------------------------------------------------------------------
// 2. Ten minutes silent is offline.

func TestTenMinutesSilentIsOfflineOnce(t *testing.T) {
	fakes(t)
	bus := newBusCheckout(t)
	commitAs(t, bus, "Emma", at.Add(-time.Minute))
	commitAs(t, bus, "Johnny", at.Add(-11*time.Minute))
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", bus, "--as", "Rowan", "--receipt-max-words", "40",
		"--line", "Johnny", "--line", "Emma"}

	first := wakeRun(t, args...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if n := countLines(first.stdout, "WAKE LINE name=Johnny state=OFFLINE"); n != 1 {
		t.Fatalf("%d OFFLINE lines for Johnny, want 1:\n%s", n, first.stdout)
	}
	// Nine minutes -- or one -- is not offline, and a healthy line on a COLD
	// first sighting is not news either: no sentence of this spec makes the
	// first sighting of a line that is fine a change, and a cold watch that
	// printed one WAKE LINE per online line and returned at once is the
	// "listing of everything that exists" the cold-start rule forbids.
	if strings.Contains(first.stdout, "state=BACK") {
		t.Errorf("a cold first run listed a healthy line as BACK:\n%s", first.stdout)
	}
	if !strings.Contains(first.stdout, "silent=11m0s") {
		t.Errorf("the OFFLINE line must carry how long the silence is:\n%s", first.stdout)
	}

	second := wakeRun(t, args...)
	if strings.Contains(second.stdout, "WAKE LINE") {
		t.Errorf("an OFFLINE line must not repeat while nothing changes:\n%s", second.stdout)
	}
	if !strings.Contains(second.stdout, "WAKE QUIET") {
		t.Errorf("a repeated OFFLINE is not a change:\n%s", second.all())
	}

	// A new sign from the line is a change once, state=BACK.
	commitAs(t, bus, "Johnny", at.Add(-30*time.Second))
	third := wakeRun(t, args...)
	if n := countLines(third.stdout, "WAKE LINE name=Johnny state=BACK"); n != 1 {
		t.Errorf("%d BACK lines for Johnny, want 1:\n%s", n, third.stdout)
	}
	fourth := wakeRun(t, args...)
	if strings.Contains(fourth.stdout, "WAKE LINE name=Johnny") {
		t.Errorf("BACK is reported once as well:\n%s", fourth.stdout)
	}

	t.Run("--line without --bus", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", filepath.Join(t.TempDir(), "s"), "--max", "5s",
			"--on-deadline", "x", "--interval", "5s", "--reports", exampleReports, "--line", "Johnny")
		if r.exit != 2 || !strings.Contains(r.stderr, "--line needs --bus") {
			t.Errorf("exit %d; a line's last sign is a commit on the bus checkout:\n%s", r.exit, r.stderr)
		}
	})
}

// newBusCheckout makes a git repository inside this test's own temporary
// directory. It is a REAL repository because what rule 2 reads is a real `git
// log`, and a fake git would be a test of the fake.
func newBusCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

func commitAs(t *testing.T, dir, name string, when time.Time) {
	t.Helper()
	stamp := when.UTC().Format(time.RFC3339)
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.name="+name, "-c", "user.email="+strings.ToLower(name)+"@mas-bandwidth.com",
		"commit", "-q", "--allow-empty", "-m", "a sign from "+name)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("committing as %s: %v\n%s", name, err, out)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// ---------------------------------------------------------------------------
// 5. Wake output is bounded and never carries a body.

func TestWakeOutputIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	busDir, ghDir := fakes(t)

	// 200 notes, each with a subject a body would dwarf.
	var transcript []string
	for i := 0; i < 200; i++ {
		transcript = append(transcript, fmt.Sprintf(
			"INBOX NOTE id=n%03d from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/n%03d.md: subject %03d", i, i, i))
	}
	transcript = append(transcript, "INBOX OK as=Rowan carrying=200 open=200 notes=200 receipts=0")
	write(t, filepath.Join(busDir, "out"), strings.Join(transcript, "\n")+"\n")

	// 50 entries.
	var entries []string
	for i := 0; i < 50; i++ {
		number := fmt.Sprint(800 + i)
		entryJSON(t, ghDir, number, "OPEN", [2]string{"race", "FAILURE"})
		entries = append(entries, "--entry", "mas-bandwidth/schema#"+number)
	}

	// 100 report files.
	reports := t.TempDir()
	for i := 0; i < 100; i++ {
		write(t, filepath.Join(reports, fmt.Sprintf("job-%03d", i), "RESULT.md"),
			"# a finding\n\nthe body of a report is never printed, and this sentence is the proof\n")
	}

	// 20 lines, all of them silent.
	bus := newBusCheckout(t)
	for i := 0; i < 20; i++ {
		commitAs(t, bus, fmt.Sprintf("line-%02d", i), at.Add(-time.Hour))
	}
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "--line", fmt.Sprintf("line-%02d", i))
	}

	state := filepath.Join(t.TempDir(), "wake.state")
	args := append([]string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s", "--baseline",
		"--bus", bus, "--as", "Rowan", "--receipt-max-words", "40",
		"--reports", reports}, entries...)
	args = append(args, lines...)

	const maxLines = 40
	r := wakeRun(t, args...)
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	got := len(strings.Split(strings.TrimSuffix(r.all(), "\n"), "\n"))
	if want := 4*maxLines + 10; got > want {
		t.Errorf("%d lines on stdout plus stderr at the largest plausible state, the promise is at most %d\n%s", got, want, r.all())
	}
	if bytes := len(r.all()); bytes > 64*1024 {
		t.Errorf("%d bytes of output; the promise is a bound in bytes as well as lines", bytes)
	}
	// The cap is PER KIND: a flat cap over a concatenated stream means the
	// loud kind eats the quiet one, and forty churning entries must not hide
	// one note. The three kinds over the ceiling each say what they did not
	// print; the twenty lines are under it and print whole, with no MORE line
	// at all, because a MORE line saying total equals shown carries nothing.
	for _, kind := range []string{"bus", "entry", "report"} {
		if !strings.Contains(r.stdout, "WAKE MORE kind="+kind+" shown=40") {
			t.Errorf("no MORE line for kind=%s:\n%s", kind, r.stdout)
		}
	}
	if n := countLines(r.stdout, "WAKE LINE "); n != 20 {
		t.Errorf("%d WAKE LINE lines, want all 20: they are under the ceiling and the quiet kind is not eaten by the loud one", n)
	}
	if strings.Contains(r.stdout, "WAKE MORE kind=line") {
		t.Errorf("a MORE line was printed for a kind that elided nothing:\n%s", r.stdout)
	}
	if strings.Contains(r.all(), "the body of a report is never printed") {
		t.Error("a report's CONTENTS reached the output; a report is its path and its size, never its contents")
	}

	// The bound holds on EVERY poll of the call and not only the first: a bus
	// refusing for twelve polls still prints at most --max-lines bus lines on
	// each of them.
	t.Run("a bus refusing for twelve polls", func(t *testing.T) {
		busDir, _ := fakes(t)
		var refusals []string
		for i := 0; i < 60; i++ {
			refusals = append(refusals, fmt.Sprintf("INBOX REFUSED reason %02d", i))
		}
		write(t, filepath.Join(busDir, "out"), strings.Join(refusals, "\n")+"\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		args := []string{"watch", "--state", state, "--max", "60s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40"}
		// The first call wakes on them and prints what the cap allows.
		wakeRun(t, args...)
		// Every later call sees them STANDING, on every poll, capped on each.
		r := wakeRun(t, args...)
		polls := 12
		if n := countLines(r.stdout, "WAKE BUS STANDING"); n > polls*maxLines {
			t.Errorf("%d standing lines over %d polls, at most %d per poll", n, polls, maxLines)
		}
		if n := countLines(r.stdout, "WAKE MORE kind=bus"); n == 0 {
			t.Errorf("a standing listing past the cap must still say what it did not print:\n%s", r.stdout)
		}
	})
}

// The known limit, pinned as behaviour rather than left to be discovered:
// mtime:size cannot see a rewrite that preserves both.
func TestATouchedReportWithTheSameSizeAndMtimeDoesNotWake(t *testing.T) {
	reports := t.TempDir()
	path := filepath.Join(reports, "job", "RESULT.md")
	write(t, path, "# a finding\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", reports}
	wakeRun(t, args...) // the cold poll records it

	// A rewrite of the same length, with the mtime put back.
	write(t, path, "# a FINDING\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("this is the named limit and it is behaviour, not an accident:\n%s", r.all())
	}

	// The same limit, in the shape that catches a compared identity carrying
	// one field more than mtime:size. "ab\n" and "a\nb" are the same length and
	// a different number of lines, and the spec says THIS rewrite must not
	// wake: a content digest "would close it and costs a read of every watched
	// file on every poll; it is a v2 item behind a flag".
	write(t, path, "ab\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	wakeRun(t, args...) // whatever that rewrite was, it is recorded now
	write(t, path, "a\nb")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	r = wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("a rewrite that preserved mtime and size woke the window on its LINE COUNT; the state value of a report file is mtime:size, and the limit above is pinned as behaviour:\n%s", r.all())
	}

	// And the other half: a real modification DOES wake, and its line still
	// carries lines= so the window can tell a stub from a finding.
	write(t, path, "# a finding, and a second line\n")
	r = wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE REPORT path=") || !strings.Contains(r.stdout, "modified") {
		t.Errorf("a modified report must wake:\n%s", r.all())
	}
	if !strings.Contains(r.stdout, "lines=1") {
		t.Errorf("the WAKE REPORT line must still carry the line count, which is display and not identity:\n%s", r.stdout)
	}
}

// A file that DISAPPEARS is not a change and its key is kept: a job directory
// being rebuilt is not news, and the window does not want to be woken by an rm.
func TestAVanishedReportIsNotAChange(t *testing.T) {
	reports := t.TempDir()
	path := filepath.Join(reports, "job", "RESULT.md")
	write(t, path, "# a finding\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", reports}
	wakeRun(t, args...)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Errorf("an rm woke the window:\n%s", r.all())
	}
	if !strings.Contains(read(t, state), "report:") {
		t.Errorf("the key was dropped; a job directory being rebuilt must not be new again:\n%s", read(t, state))
	}
}

// --final-only suppresses the WAKE, not the STATE.
func TestFinalOnlySuppressesTheWakeAndNotTheState(t *testing.T) {
	_, ghDir := fakes(t)
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s", "--final-only",
		"--entry", "mas-bandwidth/schema#942"}
	churn := func(pending, pass int) {
		var parts []string
		for i := 0; i < pending; i++ {
			parts = append(parts, fmt.Sprintf(`{"__typename":"CheckRun","name":"c%d","status":"IN_PROGRESS"}`, i))
		}
		for i := 0; i < pass; i++ {
			parts = append(parts, fmt.Sprintf(`{"__typename":"CheckRun","name":"p%d","status":"COMPLETED","conclusion":"SUCCESS"}`, i))
		}
		write(t, filepath.Join(ghDir, "942.json"),
			fmt.Sprintf(`{"state":"OPEN","statusCheckRollup":[%s]}`, strings.Join(parts, ",")))
	}
	churn(11, 1)
	wakeRun(t, args...)
	churn(9, 3)
	if r := wakeRun(t, args...); !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("churn woke the window under --final-only:\n%s", r.all())
	}
	churn(6, 6)
	if r := wakeRun(t, args...); !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("churn woke the window under --final-only:\n%s", r.all())
	}
	// pending=0 with at least one check is FINAL, green or red.
	churn(0, 12)
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE ENTRY mas-bandwidth/schema#942 state=OPEN fail=0 pending=0 pass=12 final=true") {
		t.Fatalf("a green final entry must wake:\n%s", r.all())
	}
	// The churn was stored all along, so it is never re-reported as news.
	if strings.Contains(r.stdout, "pending=9") || strings.Contains(r.stdout, "pending=6") {
		t.Errorf("the suppressed churn was re-reported later:\n%s", r.stdout)
	}

	t.Run("an entry with no checks is never final by the count rule", func(t *testing.T) {
		_, ghDir := fakes(t)
		write(t, filepath.Join(ghDir, "951.json"), `{"state":"OPEN","statusCheckRollup":[]}`)
		state := filepath.Join(t.TempDir(), "wake.state")
		args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--entry-interval", "5s", "--final-only", "--baseline",
			"--entry", "mas-bandwidth/schema#951"}
		r := wakeRun(t, args...)
		if strings.Contains(r.stdout, "WAKE ENTRY") {
			t.Errorf("pending=0 pass=0 fail=0 is an entry whose checks have not been created yet; calling that green is the one arithmetic mistake here that would merge something red:\n%s", r.stdout)
		}
		if !wake.IsFinal(wake.Compose("MERGED", "0", "0", "0", "")) {
			t.Error("MERGED is final: it is the change most likely to end the window's wait")
		}
	})
}

// Test 5's other named half, through the binary: "a backlog of 3,000 notes
// leaves 3,000 queue records after the first call with none evicted". The queue
// is never evicted and never truncated -- pending is unbounded in the STATE and
// bounded in the OUTPUT, never the other way round -- and the file's size is
// the honest cost of a window that has not read its mail. internal/wake's own
// test proves the LRU at LRUMax+50; this one proves the promise at the number
// the spec names, against the tool.
func TestABacklogOfThreeThousandNotesIsThreeThousandQueueRecords(t *testing.T) {
	busDir, _ := fakes(t)
	var lines []string
	for i := 0; i < 3000; i++ {
		lines = append(lines, fmt.Sprintf(
			"INBOX NOTE id=n%04d from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/n%04d.md: note %d", i, i, i))
	}
	write(t, filepath.Join(busDir, "out"), strings.Join(lines, "\n")+"\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--max-lines", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.stderr)
	}
	if n := countLines(r.stdout, "WAKE BUS id="); n != 40 {
		t.Errorf("the first call printed %d bus lines, want --max-lines 40: the bound holds at the largest plausible state", n)
	}
	kept, notes := 0, 0
	for _, line := range strings.Split(read(t, state), "\n") {
		switch {
		case strings.HasPrefix(line, "queue:next|"):
		case strings.HasPrefix(line, "queue:"):
			kept++
		case strings.HasPrefix(line, "bus:note:"):
			notes++
		}
	}
	// All 3,000 are accounted for: forty printed and deleted, 2,960 still
	// pending, and not one evicted.
	if kept != 2960 {
		t.Errorf("%d queue records after the first call, want 2960 -- 3000 less the 40 that printed; a pending record leaves the state only by being printed", kept)
	}
	if notes != 3000 {
		t.Errorf("%d bus:note: entries, want 3000: a bus:note: a queue record names is not a candidate for eviction whatever its age", notes)
	}
	if !strings.Contains(r.stdout, "pending=2960") {
		t.Errorf("the verdict must carry what the window has not been shown:\n%s", lastLine(r.stdout))
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// Test 5's advancing half, and test 11's: the bound holds over consumed mail,
// and the cursor waits behind the print.
//
// "200 notes ... with --advance-cursor on, where the bus prints --max-lines
// notes and one WAKE MORE kind=bus ... n=160, the state holds 160 queue
// records, the cursor has not moved, four further calls drain them in order and
// the cursor moves on the fifth, after its print, with nova-bus asserted to
// have received --advance exactly once; each of the first four calls prints
// WAKE NOTE bus advance deferred once".
func TestTheCursorWaitsBehindThePrint(t *testing.T) {
	busDir, _ := fakes(t)
	var notes []string
	for i := 0; i < 200; i++ {
		notes = append(notes, fmt.Sprintf(
			"INBOX NOTE id=n%03d from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/n%03d.md: note %d", i, i, i))
	}
	write(t, filepath.Join(busDir, "out"), strings.Join(notes, "\n")+"\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--advance-cursor", "--remote", "origin", "--branch", "main", "--max-lines", "40"}

	first := wakeRun(t, args...)
	if n := countLines(first.stdout, "WAKE BUS id="); n != 40 {
		t.Fatalf("the first call printed %d notes, want --max-lines 40:\n%s", n, lastLine(first.stdout))
	}
	if !strings.Contains(first.stdout, "WAKE MORE kind=bus") || !strings.Contains(first.stdout, "n=160") {
		t.Errorf("the elided notes are not counted:\n%s", first.stdout)
	}
	if n := wakeQueueRecords(t, state); n != 160 {
		t.Errorf("%d queue records, want 160: mail consumed is mail spooled, and the queue is never evicted", n)
	}

	// Four further calls drain them, and none of the first four advances.
	seen := 40
	for call := 2; call <= 5; call++ {
		r := wakeRun(t, args...)
		if n := countLines(r.stdout, "WAKE BUS id="); n != 40 {
			t.Fatalf("call %d printed %d notes, want 40:\n%s", call, n, lastLine(r.stdout))
		}
		seen += 40
		if call < 5 && countLines(r.stdout, "WAKE NOTE bus advance deferred") != 1 {
			t.Errorf("call %d does not say the advance is deferred:\n%s", call, r.stdout)
		}
		if call < 5 && advances(t, busDir) != 0 {
			t.Fatalf("the cursor moved on call %d, with %d notes still unprinted", call, 200-seen)
		}
	}
	if seen != 200 {
		t.Fatalf("drained %d of 200", seen)
	}
	// The fifth call drained the queue and only then advanced.
	if got := advances(t, busDir); got != 1 {
		t.Errorf("nova-bus received --advance %d times, want exactly once -- after the print that emptied the queue", got)
	}
}

// Test 11's advancing half: the residual race of The races, closed by the
// recovery and not by a refusal.
//
// "The loop is killed with an injected kill point between `nova-bus inbox
// --advance` returning and the write of its output; the state then holds
// `bus:advance=inflight`; the next call is asserted to run `inbox --open
// --open-max 25`, `25` being the `carrying=` it read AND NOT A CONSTANT, prints
// the `WAKE NOTE ... recovered 25 notes from OPEN` line, and the following
// calls print all 25 WAKE BUS lines."
func TestTheAdvanceRecoversAnInterruptedRead(t *testing.T) {
	busDir, _ := fakes(t)
	// Poll 1: nothing owed, so the call advances behind an empty queue.
	write(t, filepath.Join(busDir, "out.1"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--advance-cursor", "--remote", "origin", "--branch", "main", "--max-lines", "0"}

	// The kill lands between the advance returning and the write of its output.
	// A backlog ABOVE nova-bus's default OPEN display cap of 20, so a fixed
	// --open-max would recover a fixed number.
	wake.AdvanceKillPoint = "after-advance"
	wakeRun(t, args...)
	wake.AdvanceKillPoint = ""
	if !strings.Contains(read(t, state), "bus:advance") || !strings.Contains(read(t, state), "inflight") {
		t.Fatalf("the kill did not leave the marker:\n%s", read(t, state))
	}

	// What the cursor passed is on the reader's own OPEN list and on no other:
	// a plain inbox prints carrying= and no NOTE line for it.
	var carried []string
	for i := 0; i < 25; i++ {
		carried = append(carried, fmt.Sprintf(
			"INBOX NOTE id=c%02d from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/c%02d.md: carried %d", i, i, i))
	}
	// The fake's poll counter: run one read the inbox (1) and advanced (2), so
	// the recovery's plain inbox is the third call and its --open read the
	// fourth. `version` does not count.
	write(t, filepath.Join(busDir, "out"), "INBOX OPEN carrying=25 heard=0\n")
	write(t, filepath.Join(busDir, "out.3"), "INBOX OPEN carrying=25 heard=0\n")
	write(t, filepath.Join(busDir, "out.4"), strings.Join(carried, "\n")+"\n")

	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE NOTE bus advance was interrupted; recovered 25 notes from OPEN") {
		t.Errorf("the recovery is not said out loud:\n%s", r.all())
	}
	if n := countLines(r.stdout, "WAKE BUS id=c"); n != 25 {
		t.Errorf("%d of the 25 consumed notes were printed; the residual is a repeated wake, never a lost one:\n%s", n, lastLine(r.stdout))
	}
	var sawOpen bool
	for _, c := range calls(t, busDir) {
		if strings.Contains(c, "--open --open-max 25") {
			sawOpen = true
		}
		if strings.Contains(c, "--open-max 20") {
			t.Errorf("the recovery used a constant: nova-bus caps the listed OPEN at --open-max, and a fixed number recovers a fixed number: %q", c)
		}
	}
	if !sawOpen {
		t.Errorf("the carried list was not read whole with --open-max equal to carrying=:\n%s", strings.Join(calls(t, busDir), "\n"))
	}
	if strings.Contains(read(t, state), "inflight") {
		t.Errorf("the marker was not cleared after the recovery:\n%s", read(t, state))
	}
	// Rule 7 covers the recovery's own reads: "every line the bus source reads
	// is classified as suppressed, relayed or standing, and every line is
	// counted", and read equals the sum of the other three. A recovery that
	// read the bus and counted none of it leaves that sum short.
	var source string
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "WAKE SOURCE bus ") {
			source = line
		}
	}
	if source == "" {
		t.Fatalf("no WAKE SOURCE line:\n%s", r.stdout)
	}
	n := map[string]int{}
	for _, tok := range strings.Fields(source) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		if i, err := strconv.Atoi(v); err == nil {
			n[k] = i
		}
	}
	if n["read"] != n["suppressed"]+n["relayed"]+n["standing"] {
		t.Errorf("the four counts do not add up: %s", source)
	}
	// Every line of this call: the recovery's own plain inbox (one INBOX OPEN),
	// the carried list it recovered (25 notes), the poll after it (one INBOX
	// OPEN) and the advance behind the drained queue (one more). A recovery
	// that read the bus and counted none of it leaves read at 27.
	if n["read"] != 28 || n["suppressed"] != 3 {
		t.Errorf("read=%d suppressed=%d; the recovery's own read is a read, and rule 7 says every line of it is counted: %s",
			n["read"], n["suppressed"], source)
	}
}

// wakeQueueRecords counts the delivery queue in a state file, without counting
// its counter.
func wakeQueueRecords(t *testing.T, state string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(read(t, state), "\n") {
		if strings.HasPrefix(line, "queue:") && !strings.HasPrefix(line, "queue:next|") {
			n++
		}
	}
	return n
}

// advances counts the invocations of the fake nova-bus that carried --advance.
func advances(t *testing.T, busDir string) int {
	t.Helper()
	n := 0
	for _, c := range calls(t, busDir) {
		if advanced(c) {
			n++
		}
	}
	return n
}
