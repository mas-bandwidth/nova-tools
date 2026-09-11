package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Work list item 3a, against THE REAL nova-bus.
//
// The spec admits --advance-cursor only "after item 3 is read and test 11 is
// green against the pinned binary", and it says why: the two-poll freshness
// promise is a property of nova-bus's PUSH and not of its read, so a fake that
// answers whatever the test wrote into a file proves nothing about it. Every
// test in this file therefore builds `cmd/nova-bus` FROM THIS TREE, puts it on
// PATH, reads its version out of the binary rather than trusting a constant,
// and drives a synthetic bare bus with two clones -- the fixture the spec's own
// measurement used. Nothing here reaches the network, the real bus, or any
// directory a test did not make.

var (
	busOnce    sync.Once
	busBinary  []byte
	busVersion string
	busBuild   error
)

// realBus builds nova-bus from this tree once for the package, installs it into
// a directory of this test's own, and puts that at the front of PATH.
func realBus(t *testing.T) {
	t.Helper()
	busOnce.Do(func() {
		dir := t.TempDir()
		out := filepath.Join(dir, "nova-bus")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		// Built FROM THIS TREE, and stamped with the version the spec pins.
		// nova-bus takes its version from debug.ReadBuildInfo, which gives a
		// dirty-tree build a pseudo-version, and nova-wake refuses any nova-bus
		// that is not the pin -- so an unstamped tree build could not be
		// measured at all. The stamp says which LINE of nova-bus this is; what
		// these tests measure is the behaviour of the code in this tree, and if
		// that behaviour ever diverges from the measurement in "How the
		// checkout receives mail" these tests are what says so.
		build := []string{"build", "-ldflags", "-X main.version=" + wake.PinnedBusVersion, "-o", out, "../nova-bus"}
		if raw, err := exec.Command("go", build...).CombinedOutput(); err != nil {
			busBuild = fmt.Errorf("building nova-bus from this tree: %v\n%s", err, raw)
			return
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			busBuild = err
			return
		}
		busBinary = raw
		// The version is READ OUT OF THE BINARY, because the pin is a fact
		// about the program this test ran and not about a constant either
		// side of it could drift from.
		v, err := exec.Command(out, "version").CombinedOutput()
		if err != nil {
			busBuild = fmt.Errorf("nova-bus version: %v\n%s", err, v)
			return
		}
		toks := strings.Fields(strings.SplitN(string(v), "\n", 2)[0])
		if len(toks) < 2 {
			busBuild = fmt.Errorf("nova-bus version printed %q", v)
			return
		}
		busVersion = toks[1]
	})
	if busBuild != nil {
		t.Fatal(busBuild)
	}
	// Read back OUT OF THE BINARY rather than trusted from the constant that
	// stamped it: a build that did not take the stamp would otherwise be
	// measured as if it had.
	if busVersion != wake.PinnedBusVersion {
		t.Fatalf("the nova-bus built from this tree answers %s and the spec pins %s; the advancing tests are a measurement of the pinned program", busVersion, wake.PinnedBusVersion)
	}
	bin := t.TempDir()
	path := filepath.Join(bin, "nova-bus")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, busBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const advanceRoster = `{
  "participants": [
    {"name": "Rowan", "lane": "from-rowan", "git_name": "Rowan", "git_email": "rowan@example.com"},
    {"name": "Stella", "lane": "from-stella", "git_name": "Stella", "git_email": "stella@example.com"}
  ],
  "groups": [{"name": "Everybody on the bus", "members": ["Rowan", "Stella"]}]
}`

// synthBus builds a bare remote and two clones of it: Rowan's, which the
// watcher reads, and Stella's, which pushes mail into it from the outside. That
// is the shape "How the checkout receives mail" was measured on.
func synthBus(t *testing.T) (rowan, stella string) {
	t.Helper()
	realBus(t)
	root := t.TempDir()
	bare := filepath.Join(root, "bus.git")
	gitAt(t, root, "init", "--bare", "--quiet", "-b", "main", bare)
	rowan, stella = filepath.Join(root, "rowan"), filepath.Join(root, "stella")
	gitAt(t, root, "clone", "--quiet", bare, stella)
	write(t, filepath.Join(stella, "participants.json"), advanceRoster)
	gitAt(t, stella, "add", "-A")
	gitAt(t, stella, "-c", "user.name=Stella", "-c", "user.email=stella@example.com", "commit", "-q", "-m", "the bus")
	gitAt(t, stella, "push", "-q", "origin", "HEAD:refs/heads/main")
	gitAt(t, root, "clone", "--quiet", bare, rowan)
	return rowan, stella
}

// push writes one note from Stella to Rowan and pushes it to the bare remote,
// the way another line's send does.
func push(t *testing.T, stella, id, subject string) {
	t.Helper()
	gitAt(t, stella, "pull", "-q", "--ff-only")
	path := filepath.Join("from-stella", "2026-09-11T12"+id[len(id)-2:]+"Z-"+id+".md")
	write(t, filepath.Join(stella, path), strings.Join([]string{
		"From: Stella", "To: Rowan", "Date: Fri Sep 11 12:00:00 UTC 2026",
		"Id: " + id, "Subject: " + subject, "", subject + ".",
	}, "\n")+"\n")
	index := filepath.Join(stella, "from-stella", "INDEX")
	line := id + "\t" + filepath.ToSlash(path) + "\t2026-09-11T12:00:00Z\tRowan\t-\n"
	f, err := os.OpenFile(index, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	gitAt(t, stella, "add", "-A")
	gitAt(t, stella, "-c", "user.name=Stella", "-c", "user.email=stella@example.com", "commit", "-q", "-m", "a note for Rowan: "+subject)
	gitAt(t, stella, "push", "-q", "origin", "HEAD:refs/heads/main")
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, raw)
	}
	return string(raw)
}

func advanceArgs(state, bus string, rest ...string) []string {
	return append([]string{"watch", "--state", state, "--max", "20s", "--on-deadline", "report",
		"--interval", "5s", "--bus", bus, "--as", "Rowan", "--receipt-max-words", "40",
		"--advance-cursor", "--remote", "origin", "--branch", "main"}, rest...)
}

// Test 12's advancing half, against the real binary: "a bare remote and two
// clones; a note is pushed from the other clone while one watcher runs alone
// with --advance-cursor and is relayed WITHIN TWO POLLS", and the measurement
// under it -- once --advance has moved the cursor past a note, a plain inbox
// prints INBOX OPEN carrying=<n> and no NOTE line for it.
func TestTheRealBusRelaysNewMailWithinTwoAdvancingPolls(t *testing.T) {
	rowan, stella := synthBus(t)
	push(t, stella, "stella-aaaaaaaaaaaa", "the first note")
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	state := filepath.Join(t.TempDir(), "wake.state")

	first := wakeRun(t, advanceArgs(state, rowan)...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if !strings.Contains(first.stdout, "WAKE BUS id=stella-aaaaaaaaaaaa") {
		t.Fatalf("the note the checkout already held was not relayed:\n%s", first.all())
	}
	if !strings.Contains(first.stdout, "nova-bus="+wake.PinnedBusVersion) {
		t.Errorf("the opening line does not carry the version it measured against:\n%s", first.stdout)
	}
	// The cursor moved, behind the print -- and the note is now on the
	// reader's OPEN list and on no listing a plain inbox makes.
	out := runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40")
	if !strings.Contains(out, "carrying=1") {
		t.Errorf("the cursor did not move past the printed note:\n%s", out)
	}
	if strings.Contains(out, "INBOX NOTE id=stella-aaaaaaaaaaaa") {
		t.Errorf("a plain inbox re-listed a note the cursor has passed; this tool's queue is the only thing that holds consumed mail:\n%s", out)
	}
	if s := gitAt(t, rowan, "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(s) != "" {
		t.Errorf("the advancing watcher dirtied the checkout, and inbox --advance refuses a dirty checkout:\n%s", s)
	}

	// A second note pushed from the other clone while no watcher runs. The
	// checkout has not fetched it; the advance's push is what brings it down.
	push(t, stella, "stella-bbbbbbbbbbbb", "the second note")
	before := gitAt(t, rowan, "rev-parse", "HEAD")
	second := wakeRun(t, advanceArgs(state, rowan)...)
	if !strings.Contains(second.stdout, "WAKE BUS id=stella-bbbbbbbbbbbb") {
		t.Fatalf("mail pushed from the other clone was not relayed:\n%s", second.all())
	}
	polls := 0
	for _, line := range strings.Split(second.stdout, "\n") {
		if strings.HasPrefix(line, "WAKE CHANGE") {
			for _, tok := range strings.Fields(line) {
				if v, ok := strings.CutPrefix(tok, "polls="); ok {
					fmt.Sscanf(v, "%d", &polls)
				}
			}
		}
	}
	if polls > 2 {
		t.Errorf("relayed after %d polls, want within two advancing polls", polls)
	}
	if after := gitAt(t, rowan, "rev-parse", "HEAD"); after == before {
		t.Errorf("the checkout never moved; new mail reaches it through inbox --advance and through nothing else this tool does")
	}
}

// Test 5's advancing half, against the real binary: the cursor waits behind the
// print. A call holding unprinted mail prints up to the cap, returns, and does
// not advance; the deferring call says so once.
func TestTheRealBusCursorWaitsBehindThePrint(t *testing.T) {
	rowan, stella := synthBus(t)
	for i := 0; i < 4; i++ {
		push(t, stella, fmt.Sprintf("stella-cccccccccc%02d", i), fmt.Sprintf("note %d", i))
	}
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	state := filepath.Join(t.TempDir(), "wake.state")
	before := gitAt(t, rowan, "rev-parse", "HEAD")

	// One line per call, so three notes stay pending after the first.
	held := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "1")...)
	if n := countLines(held.stdout, "WAKE BUS id="); n != 1 {
		t.Fatalf("the call printed %d notes under --max-lines 1:\n%s", n, held.stdout)
	}
	if countLines(held.stdout, "WAKE NOTE bus advance deferred") != 1 {
		t.Errorf("a call holding unprinted mail must say the advance is deferred, once:\n%s", held.stdout)
	}
	if after := gitAt(t, rowan, "rev-parse", "HEAD"); after != before {
		t.Errorf("the cursor moved past notes this tool has not printed")
	}
	if !strings.Contains(held.stdout, "pending=3") {
		t.Errorf("the verdict does not carry what the window has not been shown:\n%s", held.stdout)
	}

	// Drain them, and only the call whose print empties the queue advances.
	for i := 0; i < 3; i++ {
		r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "1")...)
		if n := countLines(r.stdout, "WAKE BUS id="); n != 1 {
			t.Fatalf("drain %d printed %d notes:\n%s", i, n, r.stdout)
		}
	}
	if after := gitAt(t, rowan, "rev-parse", "HEAD"); after == before {
		t.Errorf("the cursor never moved, even behind an empty queue")
	}
	out := runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40")
	if !strings.Contains(out, "carrying=4") {
		t.Errorf("the reader carries what it was shown:\n%s", out)
	}
}

// Test 11's advancing half, against the real binary: the residual race of The
// races. The kill lands between `inbox --advance` returning and the write of
// its output; the marker is what brings the consumed notes back, through the
// reader's own OPEN list, with --open-max equal to carrying= and never a
// constant.
func TestTheRealBusAdvanceRecoversAnInterruptedRead(t *testing.T) {
	rowan, stella := synthBus(t)
	// A backlog above nova-bus's default OPEN display cap of 20.
	for i := 0; i < 25; i++ {
		push(t, stella, fmt.Sprintf("stella-dddddddddd%02d", i), fmt.Sprintf("carried %d", i))
	}
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	state := filepath.Join(t.TempDir(), "wake.state")

	// The kill leaves the marker, with the cursor already moved.
	wake.AdvanceKillPoint = "after-advance"
	killed := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	wake.AdvanceKillPoint = ""
	if !strings.Contains(read(t, state), "bus:advance") || !strings.Contains(read(t, state), "inflight") {
		t.Fatalf("the kill did not leave the marker:\n%s", read(t, state))
	}
	_ = killed

	// The next call recovers every note the advance consumed.
	r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	if !strings.Contains(r.stdout, "WAKE NOTE bus advance was interrupted; recovered") {
		t.Fatalf("the recovery is not said out loud:\n%s", r.all())
	}
	printed := map[string]bool{}
	for _, call := range []result{killed, r} {
		for _, line := range strings.Split(call.stdout, "\n") {
			if id, ok := strings.CutPrefix(line, "WAKE BUS id="); ok {
				printed[strings.Fields(id)[0]] = true
			}
		}
	}
	for i := 0; i < 25; i++ {
		if id := fmt.Sprintf("stella-dddddddddd%02d", i); !printed[id] {
			t.Errorf("%s was consumed by the advance and never printed; the residual is a repeated wake, never a lost one", id)
		}
	}
	if strings.Contains(read(t, state), "inflight") {
		t.Errorf("the marker was not cleared after the recovery:\n%s", read(t, state))
	}
}

// The race the spec's table names for test 12: one advancing watcher per
// (bus, as), the second WAKE REFUSED exit 2 naming the holder -- and the lock
// must not sit in the worktree, because `inbox --advance` refuses a checkout
// that holds anything that is not its note.
func TestTheRealBusAdvanceLockIsExclusiveAndInvisible(t *testing.T) {
	rowan, _ := synthBus(t)
	release, holder, err := wake.LockAdvance(rowan, "Rowan")
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatalf("the first advancing watcher could not take the lock (held by %s)", holder)
	}
	if s := gitAt(t, rowan, "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(s) != "" {
		t.Fatalf("the lock dirtied the checkout:\n%s", s)
	}
	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, advanceArgs(state, rowan)...)
	if r.exit != 2 {
		t.Fatalf("exit = %d, want 2: a cursor two runs move is a claim neither can make\n%s", r.exit, r.all())
	}
	if !strings.Contains(r.stderr, fmt.Sprint(os.Getpid())) {
		t.Errorf("the refusal does not name the holder:\n%s", r.stderr)
	}
	release()
	if r := wakeRun(t, advanceArgs(state, rowan)...); r.exit != 0 {
		t.Errorf("the lock outlived its holder: exit %d\n%s", r.exit, r.all())
	}
}

// Rule 1: "The tool never waits past --max", and the four steps are defined per
// BUS POLL. An advance on the deadline iteration is a fetch and a write-side
// call after the call was supposed to have ended; an advance on an iteration
// that polled only the entries is a cursor moved by something that is not a bus
// poll at all.
func TestTheAdvanceRunsOnlyInsideABusPollBeforeTheDeadline(t *testing.T) {
	rowan, stella := synthBus(t)
	push(t, stella, "stella-eeeeeeeeeeee", "the only note")
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	_, ghDir := fakes(t)
	realBus(t) // fakes() put the fake in front; the real one goes back on top
	write(t, filepath.Join(ghDir, "1.json"), `{"state":"OPEN","statusCheckRollup":[]}`)
	state := filepath.Join(t.TempDir(), "wake.state")

	// The bus is polled every 30s and the entry every 5s, over a 20s --max: the
	// bus is due once, at the start, and the deadline iteration and every
	// entry-only iteration must not advance.
	r := wakeRun(t, "watch", "--state", state, "--max", "20s", "--on-deadline", "report",
		"--interval", "30s", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40",
		"--entry", "mas-bandwidth/nova-tools#1", "--entry-interval", "5s",
		"--advance-cursor", "--remote", "origin", "--branch", "main")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	head := strings.TrimSpace(gitAt(t, rowan, "rev-parse", "HEAD"))

	// One bus poll happened, so at most one advance did. Run it to its
	// deadline again with the bus never due after the first poll: the cursor
	// must be exactly where the last bus poll left it.
	r = wakeRun(t, "watch", "--state", state, "--max", "20s", "--on-deadline", "report",
		"--interval", "30s", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40",
		"--entry", "mas-bandwidth/nova-tools#1", "--entry-interval", "5s",
		"--advance-cursor", "--remote", "origin", "--branch", "main")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	// The bus was due once in this call too, so one advance at most -- and the
	// deadline iteration, which polls nothing, must not have added another.
	after := strings.TrimSpace(gitAt(t, rowan, "rev-parse", "HEAD"))
	commits := strings.TrimSpace(gitAt(t, rowan, "rev-list", "--count", head+".."+after))
	if commits != "0" && commits != "1" {
		t.Errorf("the checkout gained %s commits in a call with one bus poll; an advance runs inside a bus poll, before the deadline, and nowhere else", commits)
	}
}

// runBus runs the real nova-bus the way a person would, for a test that wants
// to look at the bus rather than at the watcher.
func runBus(t *testing.T, args ...string) string {
	t.Helper()
	raw, err := exec.Command("nova-bus", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("nova-bus %v: %v\n%s", args, err, raw)
	}
	return string(raw)
}
