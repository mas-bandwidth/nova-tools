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
	"time"

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
		// Built FROM THIS TREE, and stamped with the version THIS nova-wake
		// answers -- which is the pin, since 2026-09-12: one tag ships both
		// programs and the release stamps both with it. nova-bus takes its
		// version from debug.ReadBuildInfo, which gives a dirty-tree build a
		// pseudo-version, so without the stamp the two halves of one tree could
		// answer differently and the pair could not be measured at all. What
		// these tests measure is the behaviour of the code in this tree, and if
		// that behaviour ever diverges from the measurement in "How the
		// checkout receives mail" these tests are what says so.
		stamp := buildVersion()
		build := []string{"build", "-ldflags", "-X main.version=" + stamp, "-o", out, "../nova-bus"}
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
		// Read back OUT OF THE BINARY rather than trusted from the flag that
		// stamped it: a build that did not take the stamp would otherwise be
		// measured as if it had.
		if busVersion != stamp {
			busBuild = fmt.Errorf("the nova-bus built from this tree answers %s and this nova-wake is %s; the advancing tests are a measurement of the pair that ships", busVersion, stamp)
		}
	})
	if busBuild != nil {
		t.Fatal(busBuild)
	}
	bin := t.TempDir()
	real := filepath.Join(bin, "nova-bus-real")
	if runtime.GOOS == "windows" {
		real += ".exe"
	}
	if err := os.WriteFile(real, busBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	// A recording wrapper stands in front of it, because one of test 11's
	// demands is about an ARGUMENT -- "--open-max 25, 25 being the carrying= it
	// read and not a constant" -- and a test that drives the real binary cannot
	// otherwise see what it was handed. The wrapper runs the real nova-bus, so
	// the same test proves the argument and the behaviour.
	shim := filepath.Join(bin, "nova-bus")
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	if raw, err := exec.Command("go", "build", "-o", shim, "./testdata/recordbus").CombinedOutput(); err != nil {
		t.Fatalf("building the recording nova-bus: %v\n%s", err, raw)
	}
	t.Setenv("NOVA_WAKE_REAL_BUS", real)
	t.Setenv("NOVA_WAKE_BUS_CALLS", filepath.Join(t.TempDir(), "bus-calls"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// busCalls is every nova-bus invocation this test has made, argv per line.
func busCalls(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(read(t, os.Getenv("NOVA_WAKE_BUS_CALLS")), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
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
	// No background maintenance in a directory the test framework is about to
	// remove: git's auto-gc is a second process writing into .git after the
	// test has stopped looking, and RemoveAll races it.
	for _, repo := range []string{bare, rowan, stella} {
		gitAt(t, repo, "config", "gc.auto", "0")
		gitAt(t, repo, "config", "maintenance.auto", "false")
	}
	return rowan, stella
}

// push writes one note from Stella to Rowan and pushes it to the bare remote,
// the way another line's send does.
//
// THE NOTE IS DATED FROM THE CLOCK, not from the day this file was written. The
// first advance on a lane refuses when the notes it would carry are dated before
// today UTC -- nova-bus's own guard against a first read printing a whole bus's
// history -- so a constant date is a fixture that holds only until the next
// midnight UTC, and after it the real-bus tests here fail on a refusal that is
// nova-bus behaving exactly as it should. The minute field of the filename stays
// the id's last two characters: it was never a time, only what keeps two notes
// in the same hour apart.
func push(t *testing.T, stella, id, subject string) {
	t.Helper()
	gitAt(t, stella, "pull", "-q", "--ff-only")
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join("from-stella", now.Format("2006-01-02T15")+id[len(id)-2:]+"Z-"+id+".md")
	write(t, filepath.Join(stella, path), strings.Join([]string{
		"From: Stella", "To: Rowan", "Date: " + now.Format(time.UnixDate),
		"Id: " + id, "Subject: " + subject, "", subject + ".",
	}, "\n")+"\n")
	index := filepath.Join(stella, "from-stella", "INDEX")
	line := id + "\t" + filepath.ToSlash(path) + "\t" + now.Format(time.RFC3339) + "\tRowan\t-\n"
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
	if !strings.Contains(first.stdout, "nova-bus="+busVersion) {
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
// races, which is the whole reason the marker exists.
//
// "A kill between `nova-bus inbox --advance` returning and the write of its
// output would lose this tool's record of ANY NOTE THAT CALL LISTED FIRST, and
// a plain inbox does not re-list a note the cursor has passed, so nothing would
// bring it back on its own."
//
// So the fixture leaves exactly that: 25 notes on the REMOTE that the checkout
// has not pulled. The advance's push fetches them, `inbox --advance` lists them
// first, the kill loses the listing, and the cursor is already past them -- they
// are on the reader's OPEN list and on no listing a plain inbox makes. Only the
// recovery reaches them.
func TestTheRealBusAdvanceRecoversAnInterruptedRead(t *testing.T) {
	rowan, stella := synthBus(t)
	// A backlog above nova-bus's default OPEN display cap of 20.
	for i := 0; i < 25; i++ {
		push(t, stella, fmt.Sprintf("stella-dddddddddd%02d", i), fmt.Sprintf("carried %d", i))
	}
	// Deliberately NOT pulled into rowan: these notes reach the checkout
	// through the fetch inside the advance's push, and the kill lands after it.
	state := filepath.Join(t.TempDir(), "wake.state")

	// First, the kill itself: it leaves the marker, which is what brings the
	// consumed notes back.
	wake.AdvanceKillPoint = "after-advance"
	killed := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	wake.AdvanceKillPoint = ""
	if n := countLines(killed.stdout, "WAKE BUS id="); n != 0 {
		t.Fatalf("the killed call printed %d notes; the kill must land before its output is written:\n%s", n, killed.stdout)
	}
	if !strings.Contains(read(t, state), "bus:advance") || !strings.Contains(read(t, state), "inflight") {
		t.Fatalf("the kill did not leave the marker:\n%s", read(t, state))
	}

	// Now the state the race actually leaves: the cursor PAST notes this tool
	// has no record of, with the marker set. Measured on v0.10.3, one
	// `inbox --advance` moves the cursor to the head it read at its start, so
	// the notes its own push fetched are still listed by the next plain inbox
	// -- the two-poll property -- and the residual needs the cursor a poll
	// further on. A person's second advance puts it there, which is exactly the
	// state a kill in the gap leaves behind: carried notes, unprinted, on no
	// listing a plain inbox makes.
	runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main")
	plain := runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40")
	if strings.Contains(plain, "INBOX NOTE id=stella-dddddddddd") {
		t.Fatalf("the cursor is not past the notes, so there is nothing only the recovery can reach:\n%s", plain)
	}
	if !strings.Contains(plain, "carrying=25") {
		t.Fatalf("the reader does not carry the 25 the advance consumed:\n%s", plain)
	}
	// A fresh state with the marker and nothing else: no note here has ever
	// been printed, so every one the recovery misses is a note lost.
	state = filepath.Join(t.TempDir(), "recovered.state")
	write(t, state, "bus:advance|inflight%7C2026-09-11T12:00:00Z%7C-\n")

	before := len(busCalls(t))
	r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	if !strings.Contains(r.stdout, "WAKE NOTE bus advance was interrupted; recovered 25 notes from OPEN") {
		t.Fatalf("the recovery did not reach the 25 notes the kill lost:\n%s", r.all())
	}
	printed := map[string]string{}
	for _, line := range strings.Split(r.stdout, "\n") {
		if !strings.HasPrefix(line, "WAKE BUS id=") {
			continue
		}
		f := strings.Fields(line)
		id := strings.TrimPrefix(f[0]+" "+f[2], "WAKE BUS id=")
		printed[strings.TrimPrefix(f[2], "id=")] = line
		_ = id
	}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("stella-dddddddddd%02d", i)
		line, ok := printed[id]
		if !ok {
			t.Errorf("%s was consumed by the advance and never printed; the residual is a repeated wake, never a lost one", id)
			continue
		}
		// Rule 6: what woke you is named, and a note is named by its id AND ITS
		// COMMIT SHA. A recovered note is a change line like any other.
		if strings.Contains(line, "commit=-") {
			t.Errorf("the recovered note carries no commit sha: %s", line)
		}
	}
	if strings.Contains(read(t, state), "inflight") {
		t.Errorf("the marker was not cleared after the recovery:\n%s", read(t, state))
	}

	assertOpenMax(t, busCalls(t)[before:], 25)
}

// assertOpenMax is "--open-max <n>, <n> being the carrying= it read AND NOT A
// CONSTANT". One fixture cannot say that -- a constant equal to that fixture's
// count passes it -- so the caller runs this over two recoveries carrying
// DIFFERENT numbers, and only a tool that reads the count passes both.
func assertOpenMax(t *testing.T, calls []string, want int) {
	t.Helper()
	opens := 0
	for _, c := range calls {
		if !strings.Contains(c, "--open-max") {
			continue
		}
		opens++
		if !strings.Contains(c, fmt.Sprintf("--open --open-max %d", want)) {
			t.Errorf("the recovery asked for a number that is not the carrying= it read (want %d): %q", want, c)
		}
	}
	if opens == 0 {
		t.Errorf("the recovery never read the carried list:\n%s", strings.Join(calls, "\n"))
	}
}

// The other half of "never a constant": a second recovery, carrying a different
// number. A hardcoded --open-max of ANY value fails one of the two.
func TestTheRecoveryReadsTheCountAndNeverAConstant(t *testing.T) {
	for _, carried := range []int{7, 23} {
		t.Run(fmt.Sprintf("carrying %d", carried), func(t *testing.T) {
			rowan, stella := synthBus(t)
			for i := 0; i < carried; i++ {
				push(t, stella, fmt.Sprintf("stella-eeeeeeeeee%02d", i), fmt.Sprintf("carried %d", i))
			}
			gitAt(t, rowan, "pull", "-q", "--ff-only")
			// Two advances by hand put the cursor past every one of them, which
			// is the state a kill in the gap leaves: carried, unprinted, on no
			// listing a plain inbox makes.
			for i := 0; i < 2; i++ {
				runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40",
					"--advance", "--remote", "origin", "--branch", "main")
			}
			plain := runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40")
			if !strings.Contains(plain, fmt.Sprintf("carrying=%d", carried)) {
				t.Fatalf("the fixture does not carry %d:\n%s", carried, plain)
			}
			state := filepath.Join(t.TempDir(), "recovered.state")
			write(t, state, "bus:advance|inflight%7C2026-09-11T12:00:00Z%7C-\n")
			before := len(busCalls(t))
			r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
			if n := countLines(r.stdout, "WAKE BUS id="); n != carried {
				t.Errorf("the recovery reached %d of %d carried notes:\n%s", n, carried, lastLine(r.stdout))
			}
			assertOpenMax(t, busCalls(t)[before:], carried)
			// And the lines the recovery read are counted, whatever nova-bus
			// answered: read equals suppressed+relayed+standing (rule 7).
			var source string
			for _, line := range strings.Split(r.stdout, "\n") {
				if strings.HasPrefix(line, "WAKE SOURCE bus ") {
					source = line
				}
			}
			n := map[string]int{}
			for _, tok := range strings.Fields(source) {
				k, v, ok := strings.Cut(tok, "=")
				if !ok {
					continue
				}
				var i int
				if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
					n[k] = i
				}
			}
			if n["read"] != n["suppressed"]+n["relayed"]+n["standing"] || n["read"] == 0 {
				t.Errorf("the recovery's own reads are not counted: %s", source)
			}
		})
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
