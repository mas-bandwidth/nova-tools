package pulse

// The `process-gone` controls, ported from rowan-tools/tests/wait-for.bats (nova-tools
// #2546). The bats file encodes the two self-match bugs measured on 2026-09-22 -- the
// descendant with an identical argv, and the ancestor shell that carries the pattern -- and
// each of them is a case here, asserted over a process table the test writes so neither
// depends on whether this machine's pgrep is procps or BSD.
//
// The bats file could only assert them by spawning shells and reading the platform's pgrep.
// That is why one bug passed on darwin and failed on Linux and the other did the reverse.
// Here the table IS the input, so both platforms' shapes are asserted on every platform, and
// the last test in this file spawns a real process so the production reader is not left
// untested behind the seam.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// table builds a process table from rows of "pid ppid argv...".
func table(rows ...string) []WaitProc { return parseWaitProcs(strings.Join(rows, "\n")) }

// TestProcessGoneCountsAnUnrelatedMatchAsLive is the positive control: a process nobody
// here is related to, whose argv[1] is the marker, is live and the wait does not hold.
func TestProcessGoneCountsAnUnrelatedMatchAsLive(t *testing.T) {
	t.Parallel()
	procs := table(
		"100 1 /bin/bash /tmp/waitfor-probe-7",
		"200 1 /usr/sbin/cupsd",
	)
	live := WaitLiveMatches(procs, "waitfor-probe-7", 999)
	if len(live) != 1 || live[0].Pid != 100 {
		t.Fatalf("an unrelated matching process must count as live, got %v", live)
	}
}

// TestProcessGoneHoldsWhenNothingMatches is the other half of the same control.
func TestProcessGoneHoldsWhenNothingMatches(t *testing.T) {
	t.Parallel()
	procs := table("200 1 /usr/sbin/cupsd", "201 1 /usr/bin/ssh-agent")
	if live := WaitLiveMatches(procs, "waitfor-probe-7", 999); len(live) != 0 {
		t.Fatalf("nothing names the pattern, so nothing is live; got %v", live)
	}
}

// TestProcessGoneIgnoresItsOwnDescendantWithAnIdenticalArgv is BUG ONE, the bats case
// "gone returns once that shell is dead": bash forks an identical copy of the running
// script for each pipeline stage -- same argv, different pid, a CHILD of the waiter -- and
// bin/wait-for counted that copy live, so `gone` could never return for any pattern naming
// the thing it waited on. Measured on darwin, where it hung until 2026-09-22.
func TestProcessGoneIgnoresItsOwnDescendantWithAnIdenticalArgv(t *testing.T) {
	t.Parallel()
	const self = 500
	procs := table(
		"1 0 /sbin/launchd",
		fmt.Sprintf("%d 1 /usr/local/bin/nova-pulse wait --until process-gone harvest-priority", self),
		// the pipeline copy: same argv, a child of the waiter
		fmt.Sprintf("501 %d /usr/local/bin/harvest-priority", self),
		// and a grandchild, because a pipeline inside a function is two levels down
		"502 501 /usr/local/bin/harvest-priority",
	)
	if live := WaitLiveMatches(procs, "harvest-priority", self); len(live) != 0 {
		t.Fatalf("a descendant of the waiter is the waiter's own copy and is never live; got %v", live)
	}
	// The positive control for the same table: the same argv under a DIFFERENT tree is live.
	withStranger := append(procs, table("600 1 /usr/local/bin/harvest-priority")...)
	if live := WaitLiveMatches(withStranger, "harvest-priority", self); len(live) != 1 || live[0].Pid != 600 {
		t.Fatalf("the rule must still see a real one: got %v", live)
	}
}

// TestProcessGoneIgnoresTheCallerShellCarryingThePattern is BUG TWO, the bats case "a
// pattern matching only the caller's own shell is NOT live": the shell one level up carries
// the pattern in its own argv, and procps `pgrep -f` on Linux matches an ancestor of the
// pgrep process where the BSD pgrep on darwin does not. It is the self-match that killed
// the coordinator's shell four times on 2026-09-21.
func TestProcessGoneIgnoresTheCallerShellCarryingThePattern(t *testing.T) {
	t.Parallel()
	const self = 700
	procs := table(
		"1 0 /sbin/launchd",
		// the caller: `bash /tmp/caller harvest-priority`, so argv[1] is the caller path
		// and the pattern is argv[2]; and one level up again, a shell whose argv[1] IS
		// a script path holding the pattern.
		"698 1 /bin/bash /tmp/run-harvest-priority.sh",
		"699 698 /bin/bash /tmp/caller harvest-priority",
		fmt.Sprintf("%d 699 /usr/local/bin/nova-pulse wait --until process-gone harvest-priority", self),
	)
	if live := WaitLiveMatches(procs, "harvest-priority", self); len(live) != 0 {
		t.Fatalf("an ancestor of the waiter carries the pattern because it INVOKED the waiter; got %v", live)
	}
}

// oldWholeLineMatch is the pgrep -f shape this verb replaces: a substring match against the
// WHOLE command line, not just argv[0] and argv[1]. It exists only as the control below, to
// prove a row is being kept out by WaitLiveMatches and not merely absent from the fixture by
// construction.
func oldWholeLineMatch(p WaitProc, pat string) bool {
	return strings.Contains(strings.Join(p.Args, " "), pat)
}

// TestProcessGoneOldWholeLineMatchWouldHaveCountedDescendantAndAncestor is Johnny's HOLD on
// PR #2616 (comment 5780888670): the two tests above did not actually show either bug,
// because the descendant's argv was a different binary rather than a COPY of the waiter's
// own argv, and the ancestor's pattern sat in argv[1] as a script name rather than a later
// argument, which WaitMatchesPattern never reads. Here the descendant carries the waiter's
// own argv verbatim -- the shape bash gives a forked pipeline copy -- and the ancestor's
// pattern is argv[3], past where WaitMatchesPattern reads. A whole-line match, the pgrep -f
// shape bin/wait-for used, counts both rows live; WaitLiveMatches must count neither.
func TestProcessGoneOldWholeLineMatchWouldHaveCountedDescendantAndAncestor(t *testing.T) {
	t.Parallel()
	const self = 700
	const waiterArgv = "/usr/local/bin/nova-pulse wait --until process-gone harvest-priority"
	procs := table(
		"1 0 /sbin/launchd",
		// the ancestor: one level up, a shell invoking the waiter via `-c`, so the
		// pattern lands in argv[3] -- past argv[1] -- never in the script-name position.
		"650 1 /bin/bash -c wait-for-helper harvest-priority",
		fmt.Sprintf("%d 650 %s", self, waiterArgv),
		// the descendant: NOT a different binary -- the waiter's own argv, copied whole,
		// exactly as a forked pipeline stage carries it.
		fmt.Sprintf("701 %d %s", self, waiterArgv),
	)

	var ancestor, descendant WaitProc
	for _, p := range procs {
		switch p.Pid {
		case 650:
			ancestor = p
		case 701:
			descendant = p
		}
	}
	if !oldWholeLineMatch(ancestor, "harvest-priority") {
		t.Fatalf("fixture bug: the ancestor's later argument must contain the pattern on the whole line: %v", ancestor)
	}
	if !oldWholeLineMatch(descendant, "harvest-priority") {
		t.Fatalf("fixture bug: the descendant's copied argv must contain the pattern on the whole line: %v", descendant)
	}

	live := WaitLiveMatches(procs, "harvest-priority", self)
	for _, p := range live {
		if p.Pid == ancestor.Pid {
			t.Fatalf("WaitLiveMatches counted the ancestor whose pattern sits past argv[1]; a whole-line match would have too: %v", live)
		}
		if p.Pid == descendant.Pid {
			t.Fatalf("WaitLiveMatches counted the descendant carrying the waiter's own argv; a whole-line match would have too: %v", live)
		}
	}
}

// TestProcessGoneNeverMatchesAnArgumentPastArgv1 is the rule that makes both bugs
// structurally impossible rather than only excluded: the pattern is read from argv[0] and
// argv[1] and nowhere else, so a process that merely NAMES the pattern in a later argument
// -- every waiter ever written, including this one -- is not a match at all.
func TestProcessGoneNeverMatchesAnArgumentPastArgv1(t *testing.T) {
	t.Parallel()
	procs := table(
		// a stranger's shell that happens to mention the pattern in argument three
		"800 1 /bin/bash -c pkill -f harvest-priority",
		"801 1 /usr/bin/tail -f /var/log/harvest-priority.log",
	)
	if live := WaitLiveMatches(procs, "harvest-priority", 999); len(live) != 0 {
		t.Fatalf("argv[2] onward is not matched; got %v", live)
	}
	// argv[0] and argv[1] are, whole and by basename.
	for _, row := range []string{
		"810 1 /usr/local/bin/harvest-priority --once",
		"811 1 /bin/bash /usr/local/bin/harvest-priority",
		"812 1 harvest-priority",
	} {
		if live := WaitLiveMatches(table(row), "harvest-priority", 999); len(live) != 1 {
			t.Fatalf("argv[0]/argv[1] must match, row %q gave %v", row, live)
		}
	}
	// A path pattern matches the path form; a basename pattern matches both.
	if live := WaitLiveMatches(table("820 1 /bin/bash /home/x/bin/harvest-priority"), "bin/harvest-priority", 999); len(live) != 1 {
		t.Fatal("a pattern with a slash must match the path it names")
	}
}

// TestProcessGoneExcludesTheWaiterItself: self is never live, whatever its argv says.
func TestProcessGoneExcludesTheWaiterItself(t *testing.T) {
	t.Parallel()
	procs := table("900 1 /usr/local/bin/harvest-priority")
	if live := WaitLiveMatches(procs, "harvest-priority", 900); len(live) != 0 {
		t.Fatalf("the waiter is not something to wait for; got %v", live)
	}
}

// TestProcessGoneSurvivesACycleInTheSnapshot: a table read while processes exit can name a
// reused pid as its own ancestor. The walk is bounded and must answer rather than spin.
func TestProcessGoneSurvivesACycleInTheSnapshot(t *testing.T) {
	t.Parallel()
	procs := table("10 11 /usr/local/bin/harvest-priority", "11 10 /bin/bash")
	done := make(chan int, 1)
	go func() { done <- len(WaitLiveMatches(procs, "harvest-priority", 11)) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a cycle in the snapshot made the ancestor walk spin")
	}
}

// TestProcessGoneAgainstARealProcess is the integration control for the production reader:
// the bats file's positive-and-negative pair, run once against a real process table. It is
// the ONLY test here that spawns anything, and it exists because everything above is
// asserted behind the ReadWaitProcs seam.
//
// THE TOPOLOGY IS THE BATS FILE'S, not this test process's. In the bats control the probe
// and wait-for are SIBLINGS -- both children of the bats shell -- and that is what a real
// wait looks like: a coordinator's shell starts the thing, then starts the waiter. A Go
// test that spawned the probe and then passed its own pid as self would be asking about its
// own child, which rule 2 correctly excludes, and the whole test would assert nothing. So a
// second child is spawned to stand in for the waiter, and ITS pid is the self here.
func TestProcessGoneAgainstARealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe is a shell script and ps -axo is not a windows command; the fleet runs no windows bench")
	}
	marker := fmt.Sprintf("waitfor-probe-%d-%d", os.Getpid(), time.Now().UnixNano())
	dir := t.TempDir()
	prog := filepath.Join(dir, marker)
	// A script, and a script with TWO commands in it. Two reasons, both measured
	// 2026-09-22: `bash -c "sleep 30"` execs the last command, so the process that survives
	// is `sleep 30` and the marker is gone from its argv -- the bats file's own note -- and
	// bash applies the same last-command exec to a script FILE, so a one-line script does
	// exactly what the -c form does. The trailing `exit 0` is what keeps the bash alive
	// carrying the marker as its argv[1], which is the shape this test needs to see. The
	// bats control this is ported from carried only the first half of that note, and its
	// one-line script is why it passed on darwin and failed on the Linux runner.
	if err := os.WriteFile(prog, []byte("#!/usr/bin/env bash\nsleep 30\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The stand-in waiter: a sibling of the probe, carrying the marker only where a real
	// waiter carries it -- as a later argument, the shape that used to hang.
	standIn := filepath.Join(dir, "stand-in-waiter")
	if err := os.WriteFile(standIn, []byte("#!/usr/bin/env bash\nsleep 30\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	probe := start(t, "/bin/bash", prog)
	waiter := start(t, "/bin/bash", standIn, "--until", "process-gone", marker)
	self := waiter.Process.Pid

	// Negative: while it runs, the pattern is live. The marker is argv[1] of that bash.
	if !eventually(func() bool {
		procs, err := ReadWaitProcs()
		if err != nil {
			t.Fatalf("ReadWaitProcs on a real machine: %v", err)
		}
		return len(WaitLiveMatches(procs, marker, self)) > 0
	}) {
		t.Fatal("a live process carrying the marker as argv[1] was never seen; this test would pass vacuously")
	}
	// ...and the stand-in waiter, which carries the marker as argv[4], is not itself the
	// match: that is the self-match bin/wait-for had.
	procs, err := ReadWaitProcs()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range WaitLiveMatches(procs, marker, self) {
		if p.Pid == self {
			t.Fatal("the waiter matched itself on its own --until argument")
		}
	}

	// Positive: once the probe is dead, the pattern holds.
	_ = probe.Process.Kill()
	_, _ = probe.Process.Wait()
	if !eventually(func() bool {
		procs, err := ReadWaitProcs()
		if err != nil {
			t.Fatal(err)
		}
		return len(WaitLiveMatches(procs, marker, self)) == 0
	}) {
		t.Fatal("the process is dead and process-gone still reports it live")
	}
}

// start spawns one process and kills it when the test ends.
func start(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// eventually polls a condition for ten seconds. The process table is a snapshot of a
// machine, so a spawn or an exit is seen a few milliseconds after it happens.
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
