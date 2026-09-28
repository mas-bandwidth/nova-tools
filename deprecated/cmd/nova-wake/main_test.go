package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Unit checks use an injected clock and in-process sources. The companion
// functional files own child programs, real Git repositories, and sockets.
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

func countLines(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// Rule numbers follow docs/SPEC-WAKE.md, "Tests this spec demands".
//
// 1. Every wait has a written deadline and a default action.

func TestAWatchNamesItsDeadlineAndItsDefault(t *testing.T) {
	t.Parallel()

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

// 4. It finds itself by a file, never by pgrep.

func TestASecondWatcherOnOneStateFileRefusesOnOneLine(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
			// Rule 9, verbatim: "the source tripwire finds no flag named --at,
			// --stamp or --now". Both spellings of each: a flag is declared as
			// its name without the dashes and read as its name with them. `at`
			// alone is not banned -- a bus note's own at= field is read by that
			// name, and rule 9 is about a flag that SETS a stamp, never about
			// the word.
			for _, banned := range []string{"pgrep", `"ps"`, "/proc", "os.TempDir", `"/tmp`,
				`--at `, `--stamp`, `--now`, `"stamp"`, `"now"`} {
				if strings.Contains(src, banned) {
					t.Errorf("%s/%s holds %q; rule 4 (its only files are --state, the temp file beside it and <state>.lock) and rule 9 (there is no flag that sets a stamp)", dir, name, banned)
				}
			}
		}
	}
}

// "`cold=true` on the opening `WAKE` line says the run is IN THIS STATE, and
// `--baseline` turns it off for the caller who does want the world listed once
// -- that is what `quickstart` passes" (docs/SPEC-WAKE.md, The cold-start
// rule). A --baseline run lists the world, so it is not in that state, and a
// line that says it is describes the run it is not.
func TestTheOpeningLineSaysWhichFirstRunThisIs(t *testing.T) {
	t.Parallel()

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

// A source that could not be read at all must not end a call as CALM. BROKEN
// arrives on the third consecutive failure, and a --max under three intervals
// would otherwise report a watch of nothing as a deadline reached.
func TestAQuietVerdictSaysHowManySourcesCouldNotBeRead(t *testing.T) {
	t.Parallel()

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
// running (lesson 142). Since 2026-09-12 the answer is the build STAMP rather
// than a literal in the source -- the same stamp the nova-bus pin is derived
// from -- so an unstamped build answers `devel` and a released one answers its
// tag, and both are one line naming the build.
func TestTheToolSaysWhichBuildItIs(t *testing.T) {
	r := wakeRun(t, "version")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	if !strings.HasPrefix(r.stdout, "nova-wake "+buildVersion()+" ") || countLines(r.stdout, "nova-wake ") != 1 {
		t.Errorf("version is not one line naming the build:\n%s", r.stdout)
	}

	// A released build says the tag, and the tag is the pin: the version verb
	// and the nova-bus the tool accepts are one fact, so they cannot drift.
	stampRelease(t, "v0.12.0")
	r = wakeRun(t, "version")
	if !strings.HasPrefix(r.stdout, "nova-wake v0.12.0 ") {
		t.Errorf("a stamped build does not report its tag:\n%s", r.stdout)
	}
	if !wake.AcceptBus(Version(), "v0.12.0") {
		t.Error("the version this tool reports is not the nova-bus it accepts")
	}
}

// Test 11's demanded kills, verbatim: "the loop is killed, with an injected
// kill point, after the observed write, after the item lines, and after the
// printed= marks, and in each case THE NEXT CALL PRINTS EVERY LINE THE KILLED
// CALL HAD NOT MARKED AND NOTHING IT HAD".
//
// This race is closed in the safe direction deliberately: observed state is
// written first, item lines are printed second, printed= marks are written
// third, and a kill at any boundary leaves the entry pending. A window told the
// same news twice reads twice; a window told it never does not.
func TestAKillAtEachOrderBoundaryReplaysRatherThanLoses(t *testing.T) {
	for _, tc := range []struct {
		kill    string
		printed bool // did the killed call get its lines to stdout?
	}{
		{"after-observed", false},
		{"after-lines", true},
		{"after-marks", true},
	} {
		t.Run(tc.kill, func(t *testing.T) {
			reports := t.TempDir()
			for _, name := range []string{"a", "b", "c"} {
				write(t, filepath.Join(reports, name, "RESULT.md"), "# a finding in "+name+"\n")
			}
			state := filepath.Join(t.TempDir(), "wake.state")
			args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
				"--interval", "5s", "--reports", reports, "--baseline", "--max-lines", "0"}

			watchKillPoint = tc.kill
			killed := wakeRun(t, args...)
			watchKillPoint = ""
			// A process that died does not poll again: the killed call prints
			// each line ONCE or not at all, and never a second copy on a later
			// iteration of a loop that should have ended.
			want := 0
			if tc.printed {
				want = 3
			}
			if n := countLines(killed.stdout, "WAKE REPORT"); n != want {
				t.Fatalf("the killed call printed %d report lines, want %d: a kill is not a pause\n%s", n, want, killed.stdout)
			}

			next := wakeRun(t, args...)
			want = 3
			if tc.kill == "after-marks" {
				// The marks were written, so those three are delivered and the
				// next call prints nothing it had already shown.
				want = 0
			}
			if n := countLines(next.stdout, "WAKE REPORT"); n != want {
				t.Errorf("the next call printed %d report lines, want %d: every line the killed call had not marked, and nothing it had:\n%s", n, want, next.stdout)
			}
		})
	}
}
