package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The tests docs/SPEC-WAKE.md demands, one per rule, named for the rule.
//
// Every clock here is INJECTED, which is what makes a sixteen-minute watch a
// millisecond of test and keeps this package under the two-minute rule. Every
// program the tool starts -- nova-bus, gh, git -- is a fake on PATH or a real
// git repository inside t.TempDir(), so nothing here reaches the network, the
// real bus or any directory a test did not make.

const exampleReports = "testdata/example-reports"

// at is the instant every injected clock starts from: the day the two lessons
// were paid for.
var at = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

type result struct {
	exit           int
	stdout, stderr string
	clock          *wake.Fake
}

func (r result) all() string { return r.stdout + r.stderr }

// wakeRun runs the binary in process, on a fake clock.
func wakeRun(t *testing.T, args ...string) result {
	t.Helper()
	return wakeRunAt(t, at, args...)
}

func wakeRunAt(t *testing.T, start time.Time, args ...string) result {
	t.Helper()
	clock := wake.NewFake(start)
	var out, errb bytes.Buffer
	exit := runWith(args, &out, &errb, clock)
	return result{exit: exit, stdout: out.String(), stderr: errb.String(), clock: clock}
}

// The fakes are Go programs rather than shell scripts because this repo's CI
// runs on Windows too -- and they are built ONCE for the package and copied
// into each test's own directory, because a `go build` per test was most of
// this package's wall clock and the two-minute rule is a rule. Nothing here
// leaves t.TempDir(): the build lands in the first caller's, and what survives
// it is the bytes.
var (
	fakeOnce  sync.Once
	fakeBins  map[string][]byte
	fakeBuilt error
)

func buildFakes(t *testing.T) {
	t.Helper()
	fakeOnce.Do(func() {
		dir := t.TempDir()
		cmd := exec.Command("go", "build", "-o", dir,
			"./testdata/fakebus", "./testdata/fakegh", "./testdata/fakenote")
		if raw, err := cmd.CombinedOutput(); err != nil {
			fakeBuilt = fmt.Errorf("building the fakes: %v\n%s", err, raw)
			return
		}
		fakeBins = map[string][]byte{}
		for _, f := range []struct{ name, built string }{
			{"nova-bus", "fakebus"}, {"gh", "fakegh"}, {"on-note", "fakenote"},
		} {
			built := filepath.Join(dir, f.built)
			if runtime.GOOS == "windows" {
				built += ".exe"
			}
			raw, err := os.ReadFile(built)
			if err != nil {
				fakeBuilt = err
				return
			}
			fakeBins[f.name] = raw
		}
	})
	if fakeBuilt != nil {
		t.Fatal(fakeBuilt)
	}
}

// install writes one of the built fakes into dir under the name the tool will
// start it by, and returns the path.
func install(t *testing.T, dir, name string) string {
	t.Helper()
	buildFakes(t)
	out := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	if err := os.WriteFile(out, fakeBins[name], 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

// fakes puts the fake nova-bus and gh at the front of PATH and hands back the
// directories they record into.
func fakes(t *testing.T) (busDir, ghDir string) {
	t.Helper()
	bin := t.TempDir()
	busDir, ghDir = t.TempDir(), t.TempDir()
	install(t, bin, "nova-bus")
	install(t, bin, "gh")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_WAKE_FAKE_BUS", busDir)
	t.Setenv("NOVA_WAKE_FAKE_GH", ghDir)
	return busDir, ghDir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// calls returns the fake's argument log, one line per invocation.
func calls(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(read(t, filepath.Join(dir, "calls")), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// advanced reports whether an invocation carried --advance as an ARGUMENT.
// A substring search is wrong here and was: t.TempDir() names its directory
// after the test, so a subtest called "without --advance-cursor" puts those
// nine characters into every path the fake was handed.
func advanced(call string) bool {
	for _, tok := range strings.Fields(call) {
		if tok == "--advance" {
			return true
		}
	}
	return false
}

func countLines(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// entryJSON writes one fake gh answer.
func entryJSON(t *testing.T, ghDir, number, state string, checks ...[2]string) {
	t.Helper()
	var parts []string
	for _, c := range checks {
		parts = append(parts, fmt.Sprintf(`{"__typename":"CheckRun","name":%q,"status":"COMPLETED","conclusion":%q}`, c[0], c[1]))
	}
	write(t, filepath.Join(ghDir, number+".json"),
		fmt.Sprintf(`{"state":%q,"statusCheckRollup":[%s]}`, state, strings.Join(parts, ",")))
}

// ---------------------------------------------------------------------------
// 1. Every wait has a written deadline and a default action.

func TestAWatchNamesItsDeadlineAndItsDefault(t *testing.T) {
	state := filepath.Join(t.TempDir(), "wake.state")
	base := []string{"watch", "--state", state, "--interval", "5s", "--reports", exampleReports}

	t.Run("no --max", func(t *testing.T) {
		r := wakeRun(t, append(append([]string{}, base...), "--on-deadline", "report")...)
		if r.exit != 2 {
			t.Fatalf("exit = %d, want 2; %s", r.exit, r.all())
		}
		if !strings.Contains(r.stderr, "--max is required") {
			t.Errorf("the refusal does not name --max:\n%s", r.stderr)
		}
		if !strings.Contains(r.stderr, "refusing to guess") {
			t.Errorf("a missing flag is still refusing to guess:\n%s", r.stderr)
		}
	})
	t.Run("no --on-deadline", func(t *testing.T) {
		r := wakeRun(t, append(append([]string{}, base...), "--max", "5s")...)
		if r.exit != 2 || !strings.Contains(r.stderr, "--on-deadline is required") {
			t.Errorf("exit = %d; the refusal does not name --on-deadline:\n%s", r.exit, r.stderr)
		}
	})
	t.Run("both missing, in one run", func(t *testing.T) {
		r := wakeRun(t, base...)
		if r.exit != 2 {
			t.Fatalf("exit = %d, want 2", r.exit)
		}
		for _, want := range []string{"--max is required", "--on-deadline is required"} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("one run must name every problem it can find; %q is missing from:\n%s", want, r.stderr)
			}
		}
	})
	t.Run("a watch that never changes ends at its deadline", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "wake.state")
		// Record the world first, so the second call has nothing to say.
		wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--interval", "5s", "--reports", exampleReports)
		r := wakeRun(t, "watch", "--state", state, "--max", "20m", "--on-deadline", "ask-glenn", "--interval", "5s", "--reports", exampleReports)
		if r.exit != 0 {
			t.Fatalf("exit = %d, want 0 (a deadline is not an error); %s", r.exit, r.all())
		}
		if n := countLines(r.stdout, "WAKE QUIET"); n != 1 {
			t.Fatalf("%d WAKE QUIET lines, want exactly 1:\n%s", n, r.stdout)
		}
		if !strings.Contains(r.stdout, "default=ask-glenn sources-failing=0: deadline, default taken") {
			t.Errorf("the verdict does not echo the default the caller named:\n%s", r.stdout)
		}
		if got := r.clock.Now().Sub(at); got != 20*time.Minute {
			t.Errorf("the watch ran %s, want exactly its --max of 20m: it never waits past --max", got)
		}
	})
	t.Run("--max over the ceiling", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", state, "--max", "61m", "--on-deadline", "x", "--interval", "5s", "--reports", exampleReports)
		if r.exit != 2 || !strings.Contains(r.stderr, "ceiling") {
			t.Errorf("a --max over 60m must be refused with the harness sentence; exit %d:\n%s", r.exit, r.stderr)
		}
	})
	t.Run("no source named", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--interval", "5s")
		if r.exit != 2 || !strings.Contains(r.stderr, "no source named") {
			t.Errorf("a watch with nothing to watch is a sleep with a longer name; exit %d:\n%s", r.exit, r.stderr)
		}
	})
}

// ---------------------------------------------------------------------------
// 3. Poll cadence matches the watched thing's rate.

func TestTheEntryIntervalIsTheRunLength(t *testing.T) {
	busDir, ghDir := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	entryJSON(t, ghDir, "942", "OPEN", [2]string{"build", "SUCCESS"})
	state := filepath.Join(t.TempDir(), "wake.state")
	bus := t.TempDir()

	r := wakeRun(t, "watch", "--state", state, "--max", "16m", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "8m",
		"--bus", bus, "--as", "Rowan", "--receipt-max-words", "40",
		"--entry", "mas-bandwidth/schema#942")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	polls := 0
	for _, c := range calls(t, busDir) {
		if strings.HasPrefix(c, "inbox ") {
			polls++
		}
	}
	if polls != 192 {
		t.Errorf("the bus was polled %d times over sixteen minutes at --interval 5s, want 192", polls)
	}
	if n := len(calls(t, ghDir)); n != 2 {
		t.Errorf("the entry was polled %d times over sixteen minutes at --entry-interval 8m, want 2 -- an 8-minute run deserves one check at 8 minutes, not eight at one", n)
	}

	t.Run("--interval missing", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--reports", exampleReports)
		if r.exit != 2 || !strings.Contains(r.stderr, "--interval is required") {
			t.Errorf("exit %d; the refusal does not name --interval:\n%s", r.exit, r.stderr)
		}
	})
	t.Run("--entry without --entry-interval", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x",
			"--interval", "5s", "--entry", "mas-bandwidth/schema#942")
		if r.exit != 2 || !strings.Contains(r.stderr, "--entry-interval is required") {
			t.Errorf("exit %d; the refusal does not name --entry-interval:\n%s", r.exit, r.stderr)
		}
	})
	t.Run("--interval below the floor", func(t *testing.T) {
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x",
			"--interval", "1s", "--reports", exampleReports)
		if r.exit != 2 || !strings.Contains(r.stderr, "floor") {
			t.Errorf("exit %d; a poll is a git fetch, and 5s is the floor:\n%s", r.exit, r.stderr)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. It finds itself by a file, never by pgrep.

func TestASecondWatcherOnOneStateFileRefusesOnOneLine(t *testing.T) {
	state := filepath.Join(t.TempDir(), "wake.state")
	release, holder, err := wake.LockState(state)
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatalf("the first watcher could not take the lock (held by %s)", holder)
	}
	defer release()

	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "x",
		"--interval", "5s", "--reports", exampleReports)
	if r.exit != 2 {
		t.Fatalf("exit = %d, want 2; %s", r.exit, r.all())
	}
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("the refusal is %d lines, want 1:\n%s", len(lines), r.stderr)
	}
	if !strings.HasPrefix(lines[0], "WAKE REFUSED: ") {
		t.Errorf("the refusal is not a WAKE REFUSED line:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, fmt.Sprint(os.Getpid())) {
		t.Errorf("the refusal does not name the holder's pid:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, wake.LockName(state)) {
		t.Errorf("the refusal does not name the lock file:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "state file of its own") {
		t.Errorf("the refusal does not say the fix:\n%s", r.stderr)
	}
}

// The source tripwire: rule 4 says the tool never lists processes, never reads
// its own command line back from the process table, and never uses /tmp or
// $TMPDIR. A loop that pgrepped its own command line on 2026-09-09 matched
// itself and never ended.
func TestTheSourceHoldsNoProcessScanAndNoTempDir(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "wake")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src := read(t, filepath.Join(dir, name))
			for _, banned := range []string{"pgrep", `"ps"`, "/proc", "os.TempDir", `"/tmp`, `--at `, `"stamp"`, `"now"`} {
				if strings.Contains(src, banned) {
					t.Errorf("%s/%s holds %q; rule 4 (its only files are --state, the temp file beside it and <state>.lock) and rule 9 (there is no flag that sets a stamp)", dir, name, banned)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 6. What woke you is named.

func TestWhatWokeYouIsNamed(t *testing.T) {
	busDir, ghDir := fakes(t)
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=abc123def456 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/note.md: the spec read is done\n"+
			"INBOX OK as=Rowan carrying=1 open=1 notes=1 receipts=0\n")
	entryJSON(t, ghDir, "942", "OPEN", [2]string{"race", "FAILURE"})
	reports := t.TempDir()
	write(t, filepath.Join(reports, "job", "RESULT.md"), "# a finding\n")
	state := filepath.Join(t.TempDir(), "wake.state")

	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s", "--baseline",
		"--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--entry", "mas-bandwidth/schema#942", "--reports", reports)
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	for _, want := range []string{
		"WAKE BUS id=abc123def456",
		"WAKE ENTRY mas-bandwidth/schema#942 state=OPEN fail=1",
		"failing=race",
		"WAKE REPORT path=",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("no change line carries %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(strings.ToLower(r.all()), "something changed") {
		t.Errorf("the words `something changed` appear in this tool's output; a wake that says only that the world moved sends the window back to look at all three places")
	}
}

// ---------------------------------------------------------------------------
// 7. Never filter the status line.

func TestEveryBusLineIsClassifiedAndCounted(t *testing.T) {
	busDir, _ := fakes(t)
	transcript := []string{
		"INBOX SCOPE mode=since cursor=abc changed=2 carrying=1",
		"INBOX NOTE id=n1 from=Ada addr=to at=2026-09-11T10:00:00Z path=from-ada/one.md: the first",
		"INBOX NOTE id=n2 from=Bo addr=cc at=2026-09-11T10:01:00Z path=from-bo/two.md: the second",
		"INBOX HEARD id=n3 from=Ada addr=to at=2026-09-11T10:02:00Z path=from-ada/three.md: heard",
		"INBOX REFUSED the bus is not a git checkout",
		"INBOX LEGACY before=2026-09-01 notes=4 unreadable=0",
		"INBOX OPEN carrying=1 heard=0",
		"INBOX CURSOR commit=abc carrying=1 pushed=false attempts=0",
		"INBOX OK as=Rowan carrying=1 open=1 notes=2 receipts=0",
		"INBOX FUTURE-TOKEN something a later nova-bus prints",
		"INBOX UNADDRESSED path=from-nobody/x.md: no reader",
		"fatal: not a git repository",
	}
	write(t, filepath.Join(busDir, "out"), strings.Join(transcript, "\n")+"\n")
	state := filepath.Join(t.TempDir(), "wake.state")

	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	var source string
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "WAKE SOURCE bus ") {
			source = line
		}
	}
	if source == "" {
		t.Fatalf("no WAKE SOURCE bus line:\n%s", r.stdout)
	}
	read, suppressed, relayed, standing := 0, 0, 0, 0
	for _, tok := range strings.Fields(source) {
		k, v, _ := strings.Cut(tok, "=")
		n := 0
		fmt.Sscanf(v, "%d", &n)
		switch k {
		case "read":
			read = n
		case "suppressed":
			suppressed = n
		case "relayed":
			relayed = n
		case "standing":
			standing = n
		}
	}
	if read != len(transcript) {
		t.Errorf("read=%d, want %d: a bus that printed nothing and a bus that printed twelve bookkeeping lines must not look the same", read, len(transcript))
	}
	if suppressed+relayed+standing != read {
		t.Errorf("the counts do not add up: %d + %d + %d != %d", suppressed, relayed, standing, read)
	}
	if !strings.Contains(r.stdout, "WAKE BUS LINE INBOX REFUSED the bus is not a git checkout") {
		t.Errorf("the INBOX REFUSED line was not relayed verbatim; an allow-list decides what is SUPPRESSED, never what is shown:\n%s", r.stdout)
	}
	for _, want := range []string{"INBOX FUTURE-TOKEN", "fatal: not a git repository", "INBOX UNADDRESSED"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("a line this tool cannot classify must be printed; %q is missing:\n%s", want, r.stdout)
		}
	}
}

// The other half of rule 7: shown every time, woken on once.
func TestAStandingBusLineIsShownEveryTimeAndWokenOnOnce(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX REFUSED the bus is not a git checkout\nINBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40"}

	first := wakeRun(t, args...)
	if !strings.Contains(first.stdout, "WAKE BUS LINE INBOX REFUSED") {
		t.Fatalf("the first sighting is a change and is relayed:\n%s", first.stdout)
	}
	if !strings.Contains(first.stdout, "WAKE CHANGE") {
		t.Fatalf("the first sighting must wake the window:\n%s", first.stdout)
	}
	second := wakeRun(t, args...)
	if !strings.Contains(second.stdout, "WAKE BUS STANDING INBOX REFUSED") {
		t.Errorf("a line that stands must be SHOWN on every poll -- dropping it is the false-quiet failure:\n%s", second.stdout)
	}
	if !strings.Contains(second.stdout, "WAKE QUIET") {
		t.Errorf("a line already woken on must not wake again: a bus refusing for an hour would otherwise wake the window every interval with one sentence it has already acted on:\n%s", second.stdout)
	}
}

// ---------------------------------------------------------------------------
// 8. The watcher's own failure is loud.

func TestThreeFailedPollsEndTheWatchLoudly(t *testing.T) {
	t.Run("three in a row in one call", func(t *testing.T) {
		busDir, _ := fakes(t)
		// A REFUSING bus, not a silent one. A fake that printed nothing
		// defended a path production cannot take: a real nova-bus that refuses
		// prints a line, the line relays, and a build that returned the call on
		// it never reached the streak at all.
		write(t, filepath.Join(busDir, "out"), "INBOX REFUSED the bus is not a git checkout\n")
		write(t, filepath.Join(busDir, "exit"), "1")
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "1m", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		if r.exit != 2 {
			t.Fatalf("exit = %d, want 2: a watcher that cannot see its source is not watching, and a WAKE QUIET from it would be a lie\n%s", r.exit, r.all())
		}
		if !strings.Contains(r.stdout, "WAKE BROKEN source=bus failures=3") {
			t.Errorf("the verdict is not WAKE BROKEN failures=3:\n%s", r.stdout)
		}
		if n := countLines(r.stderr, "WAKE POLL"); n != 3 {
			t.Errorf("%d WAKE POLL lines on stderr, want 3 (one per failed poll)", n)
		}
	})

	t.Run("two failures then a success", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "exit.1"), "1")
		write(t, filepath.Join(busDir, "exit.2"), "1")
		write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "20s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		if r.exit != 0 || !strings.Contains(r.stdout, "WAKE QUIET") {
			t.Fatalf("exit = %d; two failures then a success must run on to the deadline:\n%s", r.exit, r.all())
		}
		if n := countLines(r.stderr, "WAKE POLL"); n != 2 {
			t.Errorf("%d WAKE POLL lines, want 2", n)
		}
		if strings.Contains(read(t, state), "fail:bus") {
			t.Errorf("a success must clear the streak; fail:bus is still in the state:\n%s", read(t, state))
		}
	})

	t.Run("three failures over three calls, each with its own reason", func(t *testing.T) {
		busDir, _ := fakes(t)
		state := filepath.Join(t.TempDir(), "wake.state")
		args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40"}
		write(t, filepath.Join(busDir, "exit"), "1")
		var last result
		for i, reason := range []string{"one", "two", "three"} {
			write(t, filepath.Join(busDir, "out"), "INBOX REFUSED reason "+reason+"\n")
			// The clock moves BETWEEN calls, which is the gap a window spends
			// thinking; the streak has to survive it.
			last = wakeRunAt(t, at.Add(time.Duration(i)*time.Minute), args...)
		}
		if last.exit != 2 {
			t.Fatalf("the third call's exit = %d, want 2:\n%s", last.exit, last.all())
		}
		if !strings.Contains(last.stdout, "WAKE BROKEN source=bus failures=3") {
			t.Errorf("the streak did not span the three calls:\n%s", last.stdout)
		}
		if !strings.Contains(last.stdout, "since="+wake.Stamp(at)) {
			t.Errorf("since= is not the FIRST call's stamp, which may be a call or more ago:\n%s", last.stdout)
		}
	})
}

// An unreadable entry wakes under --final-only exactly as without it: a flag
// that asks for fewer wakes about arithmetic is not a flag that asks to sleep
// through a source that cannot be read.
func TestAnUnreadableEntryWakesUnderFinalOnly(t *testing.T) {
	_, ghDir := fakes(t)
	entryJSON(t, ghDir, "942", "OPEN", [2]string{"build", "SUCCESS"}, [2]string{"race", "SUCCESS"})
	write(t, filepath.Join(ghDir, "951.exit"), "1")
	write(t, filepath.Join(ghDir, "951.stderr"), "GraphQL: Could not resolve to a PullRequest")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s", "--final-only",
		"--entry", "mas-bandwidth/schema#942", "--entry", "mas-bandwidth/schema#951"}

	// Establish the world, then change nothing: the churn is recorded and the
	// unreadable entry is the news.
	wakeRun(t, args...)
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("a second poll of an unchanged world must be quiet:\n%s", r.all())
	}
	write(t, filepath.Join(ghDir, "951.stderr"), "GraphQL: a different failure entirely")
	r = wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE ENTRY mas-bandwidth/schema#951 unreadable:") {
		t.Errorf("one unreadable entry beside a readable one must wake the call under --final-only:\n%s", r.all())
	}
}

// ---------------------------------------------------------------------------
// 9. The tool stamps; a typed time is never trusted.

func TestTheToolStampsAndATypedTimeIsData(t *testing.T) {
	busDir, _ := fakes(t)
	// A note stamped two hours ahead of the clock, with a future date in its
	// subject as well. Both are data.
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=future1 from=Stella addr=to at=2026-09-11T14:00:00Z path=from-stella/n.md: due 2027-01-01, please read\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	if !strings.Contains(r.stdout, "WAKE at="+wake.Stamp(at)) {
		t.Errorf("the opening line's at= is not the injected clock's:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "at=2026-09-11T14:00:00Z") {
		t.Errorf("the note's own stamp is relayed as DATA, verbatim:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "WAKE at=2026-09-11T14:00") {
		t.Errorf("a time that arrived inside text was used as this tool's own clock:\n%s", r.stdout)
	}
}

// ---------------------------------------------------------------------------
// 11. Delivery is the printed line, not the state write.

func TestDeliveryIsThePrintedLine(t *testing.T) {
	_, ghDir := fakes(t)
	var entries []string
	for i := 0; i < 60; i++ {
		number := fmt.Sprint(900 + i)
		entryJSON(t, ghDir, number, "OPEN", [2]string{"build", "SUCCESS"})
		entries = append(entries, "--entry", "mas-bandwidth/schema#"+number)
	}
	state := filepath.Join(t.TempDir(), "wake.state")
	base := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--entry-interval", "5s"}

	// The cold first poll records the world and reports nothing.
	if r := wakeRun(t, append(append([]string{}, base...), entries...)...); !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("a cold first poll must record rather than report:\n%s", r.all())
	}
	// Now every one of them moves.
	for i := 0; i < 60; i++ {
		entryJSON(t, ghDir, fmt.Sprint(900+i), "OPEN", [2]string{"build", "FAILURE"})
	}
	r := wakeRun(t, append(append([]string{}, base...), entries...)...)
	if n := countLines(r.stdout, "WAKE ENTRY"); n != 40 {
		t.Errorf("%d WAKE ENTRY lines under the default --max-lines 40, want 40:\n%s", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "WAKE MORE kind=entry shown=40 total=60 n=20") {
		t.Errorf("the MORE line does not stand for the rest:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "pending=20") {
		t.Errorf("the verdict does not carry pending=20:\n%s", r.stdout)
	}

	// The next call prints exactly those twenty, first, and nothing newer.
	next := wakeRun(t, append(append([]string{}, base...), append(entries, "--max-lines", "0")...)...)
	if n := countLines(next.stdout, "WAKE ENTRY"); n != 20 {
		t.Errorf("the next call printed %d WAKE ENTRY lines, want exactly the 20 the cap elided:\n%s", n, next.stdout)
	}
	if !strings.Contains(next.stdout, "pending=0") {
		t.Errorf("the queue did not drain:\n%s", next.stdout)
	}
	third := wakeRun(t, append(append([]string{}, base...), entries...)...)
	if !strings.Contains(third.stdout, "WAKE QUIET") {
		t.Errorf("a third call over an unchanged world must be quiet:\n%s", third.all())
	}
}

// ---------------------------------------------------------------------------
// 12. New mail reaches the checkout through the advance -- and through nothing
// else this tool does.

func TestNewMailReachesTheCheckoutThroughTheAdvance(t *testing.T) {
	t.Run("without --advance-cursor nothing fetches, and it says so", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "10s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		for _, c := range calls(t, busDir) {
			if advanced(c) {
				t.Errorf("a run without --advance-cursor consumed something: %q", c)
			}
		}
		if n := countLines(r.stdout, "WAKE NOTE bus checkout is read as it stands"); n != 1 {
			t.Errorf("the note about not fetching is printed %d times, want once:\n%s", n, r.stdout)
		}
		if !strings.Contains(r.stdout, "head=") || !strings.Contains(r.stdout, "head-at=") {
			t.Errorf("the WAKE SOURCE bus line must carry head= and head-at=, so a reader can see the checkout stand still:\n%s", r.stdout)
		}
	})

	t.Run("--refresh fetches through wait and moves no cursor", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"), "INBOX OPEN carrying=3 heard=0\nINBOX OK as=Rowan carrying=3 open=3 notes=0 receipts=0\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
			"--refresh", "--remote", "origin", "--branch", "main")
		var sawWait, sawOpen bool
		for _, c := range calls(t, busDir) {
			if advanced(c) {
				t.Errorf("--refresh moved a cursor: %q", c)
			}
			if strings.HasPrefix(c, "wait ") && strings.Contains(c, "--timeout") {
				sawWait = true
			}
			if strings.Contains(c, "--open --open-max 3") {
				sawOpen = true
			}
		}
		if !sawWait {
			t.Errorf("--refresh must poll with `nova-bus wait --timeout`, which fetches and fast-forwards:\n%s", strings.Join(calls(t, busDir), "\n"))
		}
		if !sawOpen {
			t.Errorf("the first poll must read the carried list WHOLE, with --open-max equal to carrying= and never a constant:\n%s", strings.Join(calls(t, busDir), "\n"))
		}
		// "--timeout <t> ... where <t> is the TIME TO THE EARLIEST DUE SOURCE,
		// AT MOST --interval." A wait that blocked for --gh-timeout would spend
		// nine intervals in one poll, and the deadline would arrive inside a
		// call that was still waiting.
		for _, c := range calls(t, busDir) {
			if !strings.HasPrefix(c, "wait ") {
				continue
			}
			if !strings.Contains(c, "--timeout 5s") {
				t.Errorf("the wait budget is not the interval; --gh-timeout is the budget for a forge call, not for a poll: %q", c)
			}
		}
		// Rule 7 covers every line the bus source reads, and the enumerated
		// program shapes (docs/SPEC-WAKE.md:111-114) have no plain `inbox`
		// under --refresh: the carried count comes from the wait's own INBOX
		// OPEN line, so the extra read is neither run, nor unclassified.
		for _, c := range calls(t, busDir) {
			if strings.HasPrefix(c, "inbox ") && !strings.Contains(c, "--open") {
				t.Errorf("--refresh ran a plain inbox beside its wait: an extra process against the bus, and a read whose lines reached no count: %q", c)
			}
		}
	})

	t.Run("--refresh and --advance-cursor together", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
			"--refresh", "--advance-cursor", "--remote", "origin", "--branch", "main")
		if r.exit != 2 {
			t.Errorf("exit = %d, want 2: one fetch per poll, never two\n%s", r.exit, r.all())
		}
	})

	// "--advance-cursor is specified here and is not in the first build. Work
	// list item 3a ships it after item 3 is read and test 11 is green against
	// the PINNED BINARY; until then the flag is WAKE REFUSED: --advance-cursor
	// is not in this build; use --refresh, exit 2." That gate is not met while
	// the advancing tests run against a fake, so the flag is refused and item
	// 3a is a branch of its own, against the real nova-bus.
	t.Run("--advance-cursor is not in this build", func(t *testing.T) {
		fakes(t)
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
			"--advance-cursor", "--remote", "origin", "--branch", "main")
		if r.exit != 2 || !strings.Contains(r.stderr, "not in this build; use --refresh") {
			t.Errorf("exit = %d; %s", r.exit, r.stderr)
		}
	})

	t.Run("--advance-cursor without --as", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--receipt-max-words", "40",
			"--advance-cursor", "--remote", "origin", "--branch", "main")
		if r.exit != 2 {
			t.Errorf("exit = %d, want 2: the --as name IS the claim\n%s", r.exit, r.all())
		}
	})

	t.Run("a nova-bus of the wrong version", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "version"), "nova-bus v0.10.4 darwin/arm64 go1.27.1\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
			"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		if r.exit != 2 {
			t.Fatalf("exit = %d, want 2:\n%s", r.exit, r.all())
		}
		for _, want := range []string{"v0.10.4", wake.PinnedBusVersion} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("the refusal must name both versions; %q is missing:\n%s", want, r.stderr)
			}
		}
		if strings.Contains(r.stdout, "WAKE at=") {
			t.Errorf("the version is checked BEFORE the opening line:\n%s", r.stdout)
		}
	})
}

// "`cold=true` on the opening `WAKE` line says the run is IN THIS STATE, and
// `--baseline` turns it off for the caller who does want the world listed once
// -- that is what `quickstart` passes" (docs/SPEC-WAKE.md, The cold-start
// rule). A --baseline run lists the world, so it is not in that state, and a
// line that says it is describes the run it is not.
func TestTheOpeningLineSaysWhichFirstRunThisIs(t *testing.T) {
	reports := t.TempDir()
	write(t, filepath.Join(reports, "job", "RESULT.md"), "# a finding\n")
	base := []string{"--max", "5s", "--on-deadline", "report", "--interval", "5s", "--reports", reports}

	cold := wakeRun(t, append([]string{"watch", "--state", filepath.Join(t.TempDir(), "a.state")}, base...)...)
	if !strings.Contains(cold.stdout, "cold=true") {
		t.Errorf("a run that records the world and reports nothing must say cold=true:\n%s", cold.stdout)
	}
	if strings.Contains(cold.stdout, "WAKE REPORT") {
		t.Errorf("a cold run listed the world:\n%s", cold.stdout)
	}

	listed := wakeRun(t, append([]string{"watch", "--state", filepath.Join(t.TempDir(), "b.state"), "--baseline"}, base...)...)
	if !strings.Contains(listed.stdout, "WAKE REPORT") {
		t.Fatalf("--baseline must list the world once:\n%s", listed.stdout)
	}
	if !strings.Contains(listed.stdout, "cold=false") {
		t.Errorf("a --baseline run listed the world AND said cold=true; the field the spec uses to tell the two first-run shapes apart is then wrong for one of them, and the README ships it:\n%s", listed.stdout)
	}
}

// A bus that REFUSES every read printed its refusal (rule 7, rightly) and the
// call returned `WAKE CHANGE after=0s polls=1 bus=1`, exit 0 -- so a window
// looping on the second token of the last line spins with zero delay over a bus
// it cannot read, and the rule-8 streak can never reach three because the
// relayed line returns the call first. A run that LOOKED AT NOTHING must not
// print the same thing as a run that FOUND something.
func TestARefusingBusIsBrokenAndNotAChange(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX REFUSED Nobody is not on this bus's roster\n")
	write(t, filepath.Join(busDir, "exit"), "2\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "60s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Nobody", "--receipt-max-words", "40")
	last := lastLine(r.stdout)
	if !strings.HasPrefix(last, "WAKE BROKEN source=bus failures=3") {
		t.Errorf("the verdict over a bus that refused every read is %q; a watcher that cannot see its source is not watching, and CHANGE is what a caller reads as news", last)
	}
	if r.exit != 2 {
		t.Errorf("exit = %d, want 2: a watch whose source went away did not run to its deadline", r.exit)
	}
	// Rule 7 still holds: the sentence the bus said is on stdout.
	if !strings.Contains(r.stdout, "INBOX REFUSED Nobody is not on this bus's roster") {
		t.Errorf("the refusal was not relayed:\n%s", r.all())
	}
	if strings.Contains(r.stdout, "bus=1") {
		t.Errorf("a refusal was counted as a note that changed:\n%s", r.stdout)
	}
}

// A source that could not be read at all must not end a call as CALM. BROKEN
// arrives on the third consecutive failure, and a --max under three intervals
// would otherwise report a watch of nothing as a deadline reached.
func TestAQuietVerdictSaysHowManySourcesCouldNotBeRead(t *testing.T) {
	state := filepath.Join(t.TempDir(), "wake.state")
	missing := filepath.Join(t.TempDir(), "there-is-no-such-directory")
	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", missing)
	last := lastLine(r.stdout)
	if !strings.HasPrefix(last, "WAKE QUIET") {
		t.Fatalf("one failed poll is not yet BROKEN: %q", last)
	}
	if !strings.Contains(last, "sources-failing=1") {
		t.Errorf("the verdict reads as calm over a source that could not be read at all: %q", last)
	}
}

// The first question after a table misbehaves is which build each line is
// running (lesson 142).
func TestTheToolSaysWhichBuildItIs(t *testing.T) {
	r := wakeRun(t, "version")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	if !strings.HasPrefix(r.stdout, "nova-wake v") || countLines(r.stdout, "nova-wake ") != 1 {
		t.Errorf("version is not one line naming the build:\n%s", r.stdout)
	}
}

// Test 12's --refresh halves, which the first build left to the shape of the
// call and never asserted: "the first poll runs `inbox --open --open-max
// <carrying>` once and LISTS THE CARRIED NOTES BEFORE THE NEW ONE ... a `wait`
// that exits non-zero on one poll (the remote unreachable) is one `WAKE POLL`
// line, every queue record is intact afterwards, and the next successful poll
// relays the note".
func TestRefreshListsWhatIsOwedBeforeWhatIsNewAndSurvivesAFailedPoll(t *testing.T) {
	busDir, _ := fakes(t)
	// Poll 1 is the wait: it fails, the remote unreachable.
	write(t, filepath.Join(busDir, "out.1"), "INBOX FAIL could not reach origin\n")
	write(t, filepath.Join(busDir, "exit.1"), "1\n")
	// Poll 2 is the wait that works: one new note, and two notes carried.
	write(t, filepath.Join(busDir, "out.2"), strings.Join([]string{
		"INBOX NOTE id=new001 from=Stella addr=to at=2026-09-11T11:09:00Z path=from-stella/new.md: the new one",
		"INBOX OPEN carrying=2 heard=0",
	}, "\n")+"\n")
	// Poll 3 is the carried list, read whole with --open-max <carrying>.
	write(t, filepath.Join(busDir, "out.3"), strings.Join([]string{
		"INBOX NOTE id=old001 from=Johnny addr=to at=2026-09-11T10:00:00Z path=from-johnny/a.md: owed one",
		"INBOX NOTE id=old002 from=Emma addr=to at=2026-09-11T10:01:00Z path=from-emma/b.md: owed two",
	}, "\n")+"\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "20s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--refresh", "--remote", "origin", "--branch", "main")

	if n := countLines(r.stderr, "WAKE POLL bus"); n != 1 {
		t.Errorf("%d WAKE POLL lines for one unreachable remote, want 1; a failed poll is one line and the watch goes on:\n%s", n, r.stderr)
	}
	if r.exit != 0 {
		t.Fatalf("exit = %d; one failed poll is not BROKEN:\n%s", r.exit, r.all())
	}
	var order []string
	for _, line := range strings.Split(r.stdout, "\n") {
		if id, ok := strings.CutPrefix(line, "WAKE BUS id="); ok {
			order = append(order, strings.Fields(id)[0])
		}
	}
	if len(order) != 3 {
		t.Fatalf("relayed %v, want the two carried notes and the new one", order)
	}
	if order[0] != "old001" || order[1] != "old002" || order[2] != "new001" {
		t.Errorf("relayed in the order %v; a cold watcher lists what it is OWED before what is new", order)
	}
	var openCalls int
	for _, c := range calls(t, busDir) {
		if strings.Contains(c, "--open --open-max 2") {
			openCalls++
		}
		if advanced(c) {
			t.Errorf("--refresh moved a cursor: %q", c)
		}
	}
	if openCalls != 1 {
		t.Errorf("the carried list was read %d times with --open-max 2, want once per run, and the count is carrying= and never a constant:\n%s", openCalls, strings.Join(calls(t, busDir), "\n"))
	}
}

// Rule 11, step 3, verbatim: "delete each printed record and write
// `printed=<id>` FOR EACH LINE THAT REACHED STDOUT". A line counted as shown
// when its write failed is a delivery this tool never made, marked delivered
// permanently -- the silent loss rule 11 and The races exist to prevent.
//
// Test 11 demands it by name: "with an injected stdout that fails mid-write, no
// `printed=` mark is written for the failed line".
func TestAFailedWriteIsNotADelivery(t *testing.T) {
	reports := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		write(t, filepath.Join(reports, name, "RESULT.md"), "# a finding in "+name+"\n")
	}
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", reports, "--baseline", "--max-lines", "0"}

	// A stdout that takes the opening line and then fails, the way a closed
	// pipe does when the reader has gone.
	var errb bytes.Buffer
	out := &failingWriter{after: 1}
	exit := runWith(args, out, &errb, wake.NewFake(at))
	if exit != 0 && exit != 2 {
		t.Fatalf("exit = %d", exit)
	}
	if strings.Contains(read(t, state), "printed=") && !strings.Contains(read(t, state), "printed=-") {
		t.Errorf("a printed= mark was written for a line that never reached stdout:\n%s", read(t, state))
	}
	if n := wakeQueueRecords(t, state); n != 3 {
		t.Errorf("%d queue records after a failed write, want all 3 still pending: a record leaves the queue only by being printed", n)
	}

	// And the next call, over a stdout that works, prints every one of them.
	r := wakeRun(t, args...)
	if n := countLines(r.stdout, "WAKE REPORT"); n != 3 {
		t.Errorf("the next call printed %d of the 3 reports the failed write lost:\n%s", n, r.stdout)
	}
}

// failingWriter takes `after` writes and then fails every one, so a test can
// put a broken stdout under the loop without a pipe or a subprocess.
type failingWriter struct {
	after int
	n     int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > w.after {
		return 0, fmt.Errorf("the reader has gone")
	}
	return len(p), nil
}

// The refusal set is what a poll that could not be read produced, and it is
// about THAT POLL. A source that failed once and then answered must have its
// real change counted: a later poll's news is news.
func TestASourceThatRecoversHasItsChangeCounted(t *testing.T) {
	_, ghDir := fakes(t)
	// Unreadable on the first poll -- every entry, so the source itself fails.
	write(t, filepath.Join(ghDir, "1.exit"), "1\n")
	write(t, filepath.Join(ghDir, "1.stderr"), "gh: could not reach the forge\n")
	write(t, filepath.Join(ghDir, "1.json"), `{"state":"MERGED","statusCheckRollup":[]}`)
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "30s", "--on-deadline", "report",
		"--interval", "5s", "--entry", "mas-bandwidth/nova-tools#1", "--entry-interval", "5s"}

	clock := wake.NewFake(at)
	clock.OnSleep = func(time.Time) {
		// The forge comes back between the first poll and the second.
		os.Remove(filepath.Join(ghDir, "1.exit"))
	}
	var out, errb bytes.Buffer
	exit := runWith(args, &out, &errb, clock)
	if exit != 0 {
		t.Fatalf("exit = %d:\n%s%s", exit, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "WAKE ENTRY mas-bandwidth/nova-tools#1 state=MERGED") {
		t.Fatalf("the entry's real state never printed:\n%s", out.String())
	}
	last := lastLine(out.String())
	if !strings.HasPrefix(last, "WAKE CHANGE") || !strings.Contains(last, "entries=1") {
		t.Errorf("the verdict is %q; a source that failed once and then answered has its change counted -- the refusal is about the poll that failed, not about the key forever", last)
	}
}
