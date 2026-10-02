//go:build slow

// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (#516, the two-minute rule -- a package's
// tests answer in a minute). Nothing here is skipped or weakened; it runs nightly, whole.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// SLOW: 1.0 s on bench-tier at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// TestNativeRunKillsAtDeadline: a child that sleeps past the wall is killed by it, and the
// run records a non-zero exit rather than hanging.
func TestNativeRunKillsAtDeadline(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	start := time.Now()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("FAKE-SLEEP 60\n"), slotDir: slot, root: root, deadline: time.Second, noWall: true,
	}, &errOut)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("a deadline kill is not a refusal, got exit %d:\n%s", code, errOut.String())
	}
	if res.rc == 0 {
		t.Fatalf("the deadline killed the child, and the run records a non-zero exit")
	}
	if elapsed > 30*time.Second {
		t.Fatalf("the deadline should cut the run short, but it took %v", elapsed)
	}
}

// SLOW: 25.2 s on bench-tier at dev 64b9bec48, over the five-second line.
// TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline: rule 13d, "no sample starts while
// one is unanswered", and "a usage reader that never returns does not move the deadline, and
// the run still ends inside the bound the deadline's own test holds (issue #779)".
//
// THE READER THAT NEVER RETURNS is a `sqlite3` on PATH that sleeps past every bound. The
// sampler gives one read 5 seconds and abandons it; the card's deadline is 3 seconds and is
// the thing under test, so a sampler that could hold the ending open would show here as a
// run that outlived its own deadline.
func TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	// A database must EXIST for the reader to be run at all: an absent one is an absence
	// and never a read.
	db := filepath.Join(slot, "data", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader("CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	// A `sqlite3` on PATH that never answers, ahead of the real one.
	slow := t.TempDir()
	stall := filepath.Join(slow, swarm.SQLiteBinary)
	if err := testbin.WriteExecutable(stall, []byte("#!/bin/sh\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))

	card := filepath.Join(root, "card.md")
	if err := os.WriteFile(card, []byte("a card\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "50000"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "3s"
		}
	}
	// THE EVENT, NOT THE CLOCK. The reader sleeps ten minutes and the card's deadline is
	// three seconds. What is asserted is that the run RETURNS and that its DEADLINE is what
	// ended the card: `rc=-1` with NO `stopped=` field, which is the deadline's own shape and
	// not a sampler's. A sampler that could hold the ending open would not reach either
	// assertion at all -- the go test timeout is this repo's bound on a hang, and it is a
	// better one than a number written here, which is why the repo refuses the number.
	var stdout, stderr bytes.Buffer
	run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	line := nativeOKLine(t, stdout.String())
	if got := fieldOf(line, "rc"); got != "-1" {
		t.Fatalf("the DEADLINE ended this card, so the line prints rc=-1; got %q:\n%s", got, line)
	}
	if got := fieldOf(line, "stopped"); got != "" {
		t.Fatalf("a reader that never answers is not three FAILED reads while the card still had time; the deadline ended it and the line carries no stopped= field, got %q:\n%s", got, line)
	}
}

// SLOW: 11.1 s on bench-tier at dev 64b9bec48, over the five-second line.
// TestNativeThreeFailedReadsEndTheCardUnverifiable, and two then an answer end nothing.
//
// A READ THAT FAILS IS NOT A SOURCE THAT REPORTED NOTHING: the first ends a card, because a
// numeric budget the tool has stopped being able to see is a budget the caller believes is
// enforced and is not; the second leaves the budget unable to fire and the deadline to end
// the job.
func TestNativeThreeFailedReadsEndTheCardUnverifiable(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		failures int // how many refusals the reader gives before it answers
		wantRC   int
		wantStop string
		wantEnd  string
	}{
		{"three_in_a_row_ends_it", 1000, 1, "unverifiable", swarm.EndUnverifiable},
		{"twice_then_an_answer_ends_nothing", 2, 0, "", swarm.EndDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			// A DATABASE MUST EXIST for a read to be attempted at all: an absent one is an
			// absence, and rule 13d keeps the two apart.
			db := filepath.Join(slot, "data", "opencode", "opencode.db")
			if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("a database\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A reader that refuses its first `failures` calls and answers after that,
			// counting in a file of its own so the count survives across processes.
			dir := t.TempDir()
			counter := filepath.Join(dir, "calls")
			script := "#!/bin/sh\n" +
				"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
				"n=$((n+1)); echo $n > " + counter + "\n" +
				"if [ \"$n\" -le " + strconv.Itoa(tc.failures) + " ]; then echo 'Error: file is not a database' >&2; exit 1; fi\n" +
				"exit 0\n"
			if err := testbin.WriteExecutable(filepath.Join(dir, swarm.SQLiteBinary), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			card := filepath.Join(root, "card.md")
			if err := os.WriteFile(card, []byte("a card\nFAKE-SLEEP 8\nFAKE-FINDINGS 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(budgetNativeArgs(t, bin, card, slot, root, "100000"), "--usage-interval", "1s")
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = "60s"
				}
			}
			var stdout, stderr strings.Builder
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != tc.wantRC {
				t.Fatalf("this card exits %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantRC, rc, stdout.String(), stderr.String())
			}
			if got := fieldOf(nativeOKLine(t, stdout.String()), "stopped"); got != tc.wantStop {
				t.Errorf("stopped= is %q, want %q:\n%s", got, tc.wantStop, stdout.String())
			}
			head, rows := usageRows(t, filepath.Join(slot, "jobs", "lbl"))
			if got := cell(t, head, rows[len(rows)-1], "end"); got != tc.wantEnd {
				t.Errorf("the last row carries end=%s, got %q", tc.wantEnd, got)
			}
		})
	}
}
