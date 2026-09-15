package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The repairs the two cold reads at fe9c0695 asked for, one test per finding.
// Everything here drives the real lane read over a real git repository inside
// this test's own temporary directory; where a bound can only be proved against
// a program that misbehaves, cmd/nova-wake/testdata/fakegit is a shim that
// wedges or answers synthetically and hands everything else to the real git.

// fakeGit puts the shim at the front of PATH and answers the directory it
// records into. The real git is looked up FIRST, so the shim can hand it work.
func fakeGit(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
	bin := t.TempDir()
	install(t, bin, "git")
	t.Setenv("NOVA_WAKE_REAL_GIT", real)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// ---------------------------------------------------------------------------
// Opus 1 and Fable F3: the fourth bound is a real deadline on a real process.

func TestAWedgedGitCannotHoldThePollOpen(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: caller, subject: "the answer"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	pidfile := filepath.Join(t.TempDir(), "wedged.pid")
	fakeGit(t)
	t.Setenv("NOVA_WAKE_FAKE_GIT_WEDGE", "--batch")
	t.Setenv("NOVA_WAKE_FAKE_GIT_PIDFILE", pidfile)

	started := time.Now()
	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
		"--as", caller, "--interval", "2s")
	elapsed := time.Since(started)
	// The number is loose on purpose: what this test proves is that the poll
	// ENDS, and against the code this repairs it did not -- the package timed
	// out at ten minutes with the child still sleeping. The read's own bound is
	// asserted tightly in internal/wake's TestAWedgedProcessIsKilledByTheWhole
	// ReadBound; here the clock also carries the probe's own git calls, each of
	// them through the shim and a real git under it.
	if elapsed > 20*time.Second {
		t.Errorf("the poll took %s; the whole read is bounded by --interval or 30s, whichever is smaller", elapsed)
	}
	line := probeLine(t, r.stdout)
	if strings.Contains(line, "correlation=complete") {
		t.Errorf("a read that could not open an object is never complete:\n%s", line)
	}
	raw := read(t, pidfile)
	if raw == "" {
		t.Fatalf("the wedged git never ran; the shim is not on PATH\n%s", r.all())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("the pidfile holds %q", raw)
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("pid %d is still running after the poll returned: a wedged git must be killed, not left behind", pid)
	}
}

// ---------------------------------------------------------------------------
// Opus 3: the one-item buffer is a bound on what is HELD, not only on what is
// counted.

func TestAHugeReceiptsAppendIsNeverHeldWhole(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	// A real commit that touches RECEIPTS, so the read reaches the diff; the
	// shim then answers that diff with a megabyte of appended lines.
	write(t, filepath.Join(busDir, "from-peer", "RECEIPTS"), "2026-09-13T10:00:00Z peer-000000000000\n")
	git(t, busDir, "add", "-A")
	commit(t, busDir, peer, "a receipt", at.Add(-time.Minute))
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	traceFile := filepath.Join(t.TempDir(), "trace")
	pidfile := filepath.Join(t.TempDir(), "diff")
	fakeGit(t)
	t.Setenv("NOVA_WAKE_FAKE_GIT_TRACE", traceFile)
	t.Setenv("NOVA_WAKE_FAKE_GIT_DIFF", "1200") // ~1.1 MiB of appended lines
	t.Setenv("NOVA_WAKE_FAKE_GIT_PIDFILE", pidfile)

	// A budget that fits two of those lines and no more.
	probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
		"--as", caller, "--correlate-bytes", "2048")
	fi, err := os.Stat(pidfile + ".bytes")
	raw := read(t, pidfile+".bytes")
	if raw == "" {
		t.Fatalf("the shim never answered a diff; it is not on PATH (size=%d err=%v raw=%q)",
			func() int64 { if fi != nil { return fi.Size() }; return -1 }(), err, raw)
	}
	written, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("the shim recorded %q", raw)
	}
	if written > 128<<10 {
		t.Errorf("the read took %d bytes of a 1.1 MiB receipts append; it is streamed a line at a time and stops when the budget is spent", written)
	}
	if written == 0 {
		t.Errorf("the read took nothing at all; the stream never started")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---------------------------------------------------------------------------
// Opus 4 and Fable F8: the answer's own id, in the spec's own sentence.

func TestTheAnswerNoteNamesTheAnsweringId(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	answerID := "peer-00000000beef"
	write(t, filepath.Join(busDir, "from-peer", "reply.md"),
		fmt.Sprintf("From: %s\nTo: %s\nDate: Sun Sep 13 10:00:00 UTC 2026\nId: %s\nRe: rowan-00000000000a\nSubject: here I am\n\nthe body\n",
			peer, caller, answerID))
	git(t, busDir, "add", "-A")
	commit(t, busDir, peer, "a reply", at.Add(-time.Minute))
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
	want := "WAKE NOTE probe peer: " + answerID + " answers ping rowan-00000000000a by its Re line"
	if !strings.Contains(r.stdout, want) {
		t.Errorf("the spec's sentence is not printed:\n  want %s\n  got\n%s", want, r.stdout)
	}
	if strings.Contains(r.stdout, "its its") {
		t.Errorf("the sentence says `by its its`:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "state=ANSWERED") {
		t.Errorf("a note from the line to the caller is an answer:\n%s", r.stdout)
	}
}

// ---------------------------------------------------------------------------
// Opus 5: a gap is named by its cause, and no caller is told to raise a budget
// against a cap that is not a budget.

func TestAGapIsNamedByItsCause(t *testing.T) {
	t.Run("an over-cap header at the default budget", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "c.md", from: peer, to: caller, noBlank: true})
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		if !strings.Contains(r.stdout, "header-cap:") {
			t.Errorf("the gap is named by its cause, the fixed header cap:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "no --correlate-bytes raise reads it") {
			t.Errorf("a fixed cap is not a budget, and the line must say so:\n%s", r.stdout)
		}
	})

	t.Run("an over-cap header at a budget that cannot take it", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "c.md", from: peer, to: caller, noBlank: true})
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
			"--as", caller, "--correlate-bytes", "2048")
		if !strings.Contains(r.stdout, "budget:") {
			t.Errorf("at a budget this item cannot fit, the gap is the budget's:\n%s", r.stdout)
		}
		if strings.Contains(r.stdout, "raise to at least that reads it") {
			t.Errorf("a raise lets this read ATTEMPT the header; it does not promise to read it:\n%s", r.stdout)
		}
	})

	t.Run("a header that ends in the cap and will not parse", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		write(t, filepath.Join(busDir, "from-peer", "bad.md"),
			"Branch: main\nFrom: peer\nTo: Rowan\nSubject: a key the bus does not know\n\nbody\n")
		git(t, busDir, "add", "-A")
		commit(t, busDir, peer, "an unparsable note", at.Add(-time.Minute))
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		if strings.Contains(r.stdout, "header-cap:") {
			t.Errorf("a header that ENDED inside the cap is not a header-cap gap:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "header:") {
			t.Errorf("an unparsable header is named as one:\n%s", r.stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// Fable F1: a failed read is never an empty lane, and an empty lane is the one
// thing that is complete negative evidence.

func TestAFailedReadIsNeverComplete(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: "Somebody-Else", subject: "not for the caller"})
	tree := treeOf(t, busDir, "from-peer")
	removeObject(t, busDir, tree)
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	first := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
	line := probeLine(t, first.stdout)
	if strings.Contains(line, "correlation=complete") {
		t.Errorf("a lane whose listing failed is not an empty lane:\n%s", line)
	}
	second := probeAt(t, at.Add(8*time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
	if strings.Contains(second.stdout, "UNAVAILABLE") {
		t.Errorf("no unavailability is declared on a read that could not have seen the answer:\n%s", second.stdout)
	}
	st, err := wake.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("probe:" + peer); !ok {
		t.Errorf("the ping was retired on a read that failed")
	}
}

// ---------------------------------------------------------------------------
// Fable F2: a path git would quote is read like any other.

func TestANotePathGitWouldQuoteIsStillRead(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "n-ünïcode-señal.md", from: peer, to: caller, subject: "here I am"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
	if r.exit != 0 || !strings.Contains(r.stdout, "state=ANSWERED") {
		t.Errorf("a note whose path git quotes is an answer like any other; exit %d\n%s", r.exit, r.all())
	}
	if strings.Contains(r.stdout, "gaps=1") {
		t.Errorf("a quoted path is not a missing object:\n%s", r.stdout)
	}
}

// ---------------------------------------------------------------------------
// Fable F4: remaining= counts items, and a RECEIPTS append is as many items as
// the lines it gained.

func TestRemainingCountsItemsAndNotPaths(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	write(t, filepath.Join(busDir, "from-peer", "a.md"),
		note{name: "a.md", from: peer, to: "Somebody-Else", subject: "one"}.text())
	var receipts strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&receipts, "2026-09-13T10:0%d:00Z peer-00000000000%d\n", i, i)
	}
	write(t, filepath.Join(busDir, "from-peer", "RECEIPTS"), receipts.String())
	git(t, busDir, "add", "-A")
	commit(t, busDir, peer, "one note and five receipts", at.Add(-time.Minute))
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
		"--as", caller, "--correlate-max", "1")
	line := probeLine(t, r.stdout)
	if !strings.Contains(line, "remaining=5") {
		t.Errorf("one note and five appended receipt lines are SIX items and two paths:\n%s", line)
	}
}

// ---------------------------------------------------------------------------
// Fable F6: a gap is retained once, and a tip commit that keeps failing cannot
// make gaps= climb.

func TestAStandingGapIsRetainedOnceAndNeverGrows(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: "Somebody-Else", subject: "one"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	// The commits list fine and ONE commit's own listing fails, which is the
	// shape the gap list can grow under: the tip is reached, the gap is
	// recorded, and the next poll resumes at the same position.
	fakeGit(t)
	t.Setenv("NOVA_WAKE_FAKE_GIT_FAIL", "diff-tree")
	for i := 0; i < 4; i++ {
		r := probeAt(t, at.Add(time.Duration(i+1)*time.Minute), "--bus", busDir, "--line", peer,
			"--state", state, "--as", caller)
		line := probeLine(t, r.stdout)
		if !strings.Contains(line, "gaps=1") {
			t.Fatalf("poll %d: a commit whose listing failed is one gap, retained once:\n%s", i, line)
		}
		if strings.Contains(line, "correlation=complete") {
			t.Fatalf("poll %d: a commit this read could not list is not covered:\n%s", i, line)
		}
	}
}

// ---------------------------------------------------------------------------
// Fable F7: an ANSWERED poll says what it MEASURED.

func TestAnAnswerInTheMiddleOfALaneIsNotComplete(t *testing.T) {
	busDir, anchor := deepLane(t, 40, 20)
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
	line := probeLine(t, r.stdout)
	if !strings.Contains(line, "state=ANSWERED") {
		t.Fatalf("the 20th note answers:\n%s", line)
	}
	if strings.Contains(line, "correlation=complete") {
		t.Errorf("an answer in the middle of a lane leaves what follows uncovered:\n%s", line)
	}
}

// ---------------------------------------------------------------------------
// Fable F10: a permanent gap never spends the item budget a lane still needs.

func TestAPermanentGapNeverStarvesTheLane(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: caller, noBlank: true},
		note{name: "b.md", from: peer, to: "Somebody-Else", subject: "two"},
		note{name: "c.md", from: peer, to: caller, subject: "the answer"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
	args := []string{"--bus", busDir, "--line", peer, "--state", state, "--as", caller, "--correlate-max", "1"}
	marks := map[string]bool{}
	answered := false
	for i := 0; i < 5; i++ {
		r := probeAt(t, at.Add(time.Duration(i+1)*time.Minute), args...)
		if strings.Contains(r.stdout, "state=ANSWERED") {
			answered = true
			break
		}
		mark := bookmarkOf(t, state)
		if marks[mark] {
			t.Fatalf("poll %d spent its one item on a gap no poll can resolve: %s", i, mark)
		}
		marks[mark] = true
	}
	if !answered {
		t.Errorf("the lane behind a permanent gap was never reached")
	}
}

// deepLane builds a lane of n notes in one commit, with the kth addressed to
// the caller, and answers the checkout and the anchor.
func deepLane(t *testing.T, n, answerAt int) (busDir, anchor string) {
	t.Helper()
	busDir, anchor = newLaneBus(t)
	var batch []note
	for i := 1; i <= n; i++ {
		to := "Somebody-Else"
		if i == answerAt {
			to = caller
		}
		batch = append(batch, note{name: fmt.Sprintf("n%04d.md", i), from: peer, to: to, subject: "note"})
	}
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute), batch...)
	return busDir, anchor
}

func treeOf(t *testing.T, dir, path string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD:"+path)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse tree: %v", err)
	}
	return strings.TrimSpace(string(raw))
}
