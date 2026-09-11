package main

// Rule 3 and its two companions: the fold. This is where the tool's one load-bearing
// property is pinned — THE SAME EVENTS DERIVE THE SAME OWNER, in every clone and in both
// backends — and where the id is proven a draw rather than a derivation.
//
// A "clone" here is a second directory holding the same card files, and a union merge is
// the two files' lines in one order or the other. That is exactly what git hands two lines
// that appended to one card file, and the point of the test is that the ORDER OF THE LINES
// IN THE FILE CHANGES NOTHING.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/board"
)

// --------------------------------------------------------------------------------- 3

func TestTheBoardIsAFoldOverCardFilesWithNoLock(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add(plain("rowan", "the first thing owed")...)
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != id+".board" {
		t.Fatalf("add wrote %v, want exactly %s.board", names(entries), id)
	}

	// Twenty cards, taken and closed from two clones at once. All of it lands and no lock
	// file is left behind, because the tool holds no lock: no file has two writers.
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, b.add(plain("rowan", "thing "+string(rune('a'+i))+" is owed")...))
	}
	var wg sync.WaitGroup
	for i, card := range ids {
		wg.Add(1)
		go func(i int, card string) {
			defer wg.Done()
			who := "ada"
			if i%2 == 1 {
				who = "bo"
			}
			if exit, _, stderr := b.atTime(b.now.Add(time.Duration(i)*time.Second), b.board("take", "--as", who, "--card", card, "--stale", "10m")...); exit != 0 {
				t.Errorf("take %s: exit %d %s", card, exit, stderr)
			}
		}(i, card)
	}
	wg.Wait()
	entries, err = os.ReadDir(b.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 21 {
		t.Errorf("the board holds %d files, want 21 card files and no index and no lock: %v", len(entries), names(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".board") {
			t.Errorf("the board directory holds %q; the tool holds no lock because it needs none", e.Name())
		}
	}
	_, stdout, _ := b.run(b.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "cards=21") || !strings.Contains(stdout, "open=21") {
		t.Errorf("twenty concurrent takes did not all land:\n%s", stdout)
	}
}

// TWO LINES TAKING ONE CARD FROM TWO CLONES. Both appends land, the union keeps both
// `taken` lines, and the owner is the take that sorts later by (at, as, id) — the same
// owner from the merge in either textual order, and the same owner from the other backend.
func TestOneCardTakenByTwoClonesFoldsToOneOwnerEverywhere(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "the one card two lines both reach for")...)
	one := b.now.Add(time.Minute)

	// Ada's clone and Bo's clone, each a copy of the board, each taking at ONE second.
	ada := clone(t, b.dir)
	bo := clone(t, b.dir)
	for dir, who := range map[string]string{ada: "Ada", bo: "Bo"} {
		if exit, _, stderr := b.atTime(one, "take", "--dir", dir, "--as", who, "--card", id, "--stale", "10m"); exit != 0 {
			t.Fatalf("take by %s: exit %d %s", who, exit, stderr)
		}
	}
	adaLine := lastLine(t, filepath.Join(ada, id+".board"))
	boLine := lastLine(t, filepath.Join(bo, id+".board"))
	if adaLine == boLine {
		t.Fatal("two takes produced one line; each event draws its own ev=")
	}
	if !strings.Contains(adaLine, "after="+id) || !strings.Contains(boLine, "after="+id) {
		t.Fatalf("two concurrent takes must name the same predecessor:\n%s\n%s", adaLine, boLine)
	}

	base := readFile(t, filepath.Join(b.dir, id+".board"))
	forward := merged(t, b, id, base+adaLine+"\n"+boLine+"\n")
	backward := merged(t, b, id, base+boLine+"\n"+adaLine+"\n")
	if stripSource(forward) != stripSource(backward) {
		t.Errorf("two clones that merged the same appends in opposite orders derive different boards:\n%s\n%s", forward, backward)
	}
	// The documented key is (at, as, id, verb, bytes): at one second, the take whose as=
	// sorts LATER is the owner. That winner is arbitrary and REPRODUCIBLE, which is the
	// property the accepted residual race needs.
	if !strings.Contains(forward, "owner=Bo") {
		t.Errorf("the owner at one second must be Bo, whose as= sorts later:\n%s", forward)
	}
	if !strings.Contains(forward, "conflicts=1") {
		t.Errorf("the tie the fold chose over is not counted:\n%s", forward)
	}

	// THE SAME EVENTS THROUGH THE OTHER BACKEND name the same owner. Nothing in the format
	// belongs to a backend.
	lines := strings.Split(strings.TrimSpace(base+adaLine+"\n"+boLine), "\n")[1:] // drop the version line
	viaIssue := issueListing(t, b, lines, "list", "--stale", "10m", "--list", "--open")
	if !strings.Contains(viaIssue, "owner=Bo") || !strings.Contains(viaIssue, "conflicts=1") {
		t.Errorf("the issue backend names a different owner from the same events:\n%s", viaIssue)
	}
	if stripSource(viaIssue) != stripSource(forward) {
		t.Errorf("the two backends printed different listings from identical histories:\n--- dir ---\n%s\n--- issue ---\n%s", forward, viaIssue)
	}
}

// CAUSAL ORDER BEATS THE KEY. At one second Bo takes the card; Ada SEES that take and
// retakes over it, naming it in after=. The owner is Ada, and there is no conflict: a
// clock behind is a clock, not a cause.
func TestCausalOrderBeatsTheTotalKey(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "a card taken twice, the second knowing the first")...)
	one := b.now.Add(time.Minute)
	if exit, _, stderr := b.atTime(one, b.board("take", "--as", "Bo", "--card", id, "--stale", "10m")...); exit != 0 {
		t.Fatalf("Bo's take: exit %d %s", exit, stderr)
	}
	if exit, _, stderr := b.atTime(one, b.board("take", "--as", "Ada", "--card", id, "--stale", "10m", "--anyway")...); exit != 0 {
		t.Fatalf("Ada's take: exit %d %s", exit, stderr)
	}
	lines := eventLines(t, filepath.Join(b.dir, id+".board"))
	if !strings.Contains(lines[2], "after="+evOf(lines[1])) {
		t.Fatalf("Ada's take does not name the take she saw:\n%s", strings.Join(lines, "\n"))
	}
	_, stdout, _ := b.atTime(one, b.board("list", "--stale", "10m", "--list", "--open")...)
	if !strings.Contains(stdout, "owner=Ada") {
		t.Errorf("the owner must be Ada, who saw Bo's take:\n%s", stdout)
	}
	if !strings.Contains(stdout, "conflicts=0") {
		t.Errorf("a take that names the other take is not concurrent:\n%s", stdout)
	}
	if !strings.Contains(stdout, "override=true") && !strings.Contains(lines[2], "override=true") {
		t.Errorf("the --anyway is not recorded in the event:\n%s", lines[2])
	}
	// The same events in the opposite textual order derive the same owner.
	swapped := readFile(t, filepath.Join(b.dir, id+".board"))
	rows := strings.Split(strings.TrimRight(swapped, "\n"), "\n")
	rows[1], rows[2] = rows[2], rows[1]
	if got := merged(t, b, id, strings.Join(rows, "\n")+"\n"); !strings.Contains(got, "owner=Ada") {
		t.Errorf("the merge order changed the owner:\n%s", got)
	}
	// A CLOSE WHOSE CLOCK IS A SECOND BEHIND THE TAKE IT NAMES still folds after it.
	behind := "closed " + id + " ev=aaaaaaaaaaaa after=" + evOf(lines[2]) + " as=Ada at=" +
		one.Add(-time.Second).UTC().Format(time.RFC3339) + " override=false: it was already done"
	if got := merged(t, b, id, swapped+behind+"\n"); !strings.Contains(got, "state=CLOSED") {
		t.Errorf("a close a second behind the take it names did not close the card:\n%s", got)
	}
}

// An event whose after= names a missing id, one naming an event of ANOTHER card, and two
// naming each other are each QUARANTINED: not folded, counted, named once, never guessed at.
func TestABrokenPredecessorIsQuarantinedAndCounted(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "a card somebody appends nonsense to")...)
	other := b.add(plain("rowan", "another card entirely")...)
	if exit, _, stderr := b.run(b.board("take", "--as", "bo", "--card", other, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take: exit %d %s", exit, stderr)
	}
	otherEv := evOf(eventLines(t, filepath.Join(b.dir, other+".board"))[1])
	base := readFile(t, filepath.Join(b.dir, id+".board"))
	stamp := b.now.Add(time.Minute).UTC().Format(time.RFC3339)

	for _, tc := range []struct {
		name  string
		extra []string
		want  int
	}{
		{"a missing predecessor", []string{"taken " + id + " ev=111111111111 after=999999999999 as=zz at=" + stamp + " override=false"}, 1},
		{"an event of another card", []string{"taken " + id + " ev=111111111111 after=" + otherEv + " as=zz at=" + stamp + " override=false"}, 1},
		{"two naming each other", []string{
			"taken " + id + " ev=111111111111 after=222222222222 as=zz at=" + stamp + " override=false",
			"taken " + id + " ev=222222222222 after=111111111111 as=zz at=" + stamp + " override=false",
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := merged(t, b, id, base+strings.Join(tc.extra, "\n")+"\n")
			for _, want := range []string{"conflict=true", "quarantined=" + itoa(tc.want), "owner=rowan"} {
				if !strings.Contains(got, want) {
					t.Errorf("want %q in:\n%s", want, got)
				}
			}
			if !strings.Contains(got, "BOARD NOTE quarantined events=") {
				t.Errorf("a quarantined event is not named once:\n%s", got)
			}
			if strings.Contains(got, "owner=zz") {
				t.Errorf("a quarantined take was folded:\n%s", got)
			}
		})
	}
}

// -------------------------------------------------------------- the id, and O_EXCL

// THE ID IS A DRAW AND CREATION IS EXCLUSIVE. Two rows filed under one name with one text
// on two legs at one second, from two clones, produce two cards with two distinct ids and
// neither is refused.
func TestTheIdIsRandomAndCreationIsExclusive(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	one := clone(t, b.dir)
	two := clone(t, b.dir)
	same := []string{"--as", "rowan", "--text", "every field is written", "--by", "4h", "--default", "rowan probes it"}
	var got []string
	for _, dir := range []string{one, two} {
		leg := "cpp"
		if dir == two {
			leg = "go"
		}
		args := append([]string{"add", "--dir", dir}, same...)
		args = append(args, "--thing", "every-field", "--leg", leg)
		exit, stdout, stderr := b.run(args...)
		if exit != 0 {
			t.Fatalf("exit %d: %s", exit, stderr)
		}
		got = append(got, field(stdout, "id="))
	}
	if got[0] == got[1] {
		t.Fatalf("two concurrent adds drew one id %s; a creation identity may depend on nothing two writers can both observe before either writes", got[0])
	}
	for _, id := range got {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Errorf("id %q is not thirty-two lower-case hex", id)
		}
	}
	// The ids come from the injected source and equal NOTHING computed from the fields:
	// the same fields through a source at a different point draw a different id.
	third := newBench(t)
	third.rnd.n = 500 // a different point in the source, not a different set of fields
	args := append([]string{"add", "--dir", third.dir}, same...)
	_, stdout, _ := third.run(append(args, "--thing", "every-field", "--leg", "cpp")...)
	if field(stdout, "id=") == got[0] {
		t.Error("two adds with the same fields drew the same id; the id is a draw, not a derivation")
	}

	// AN ID THAT ALREADY EXISTS IS A REFUSAL AND NEVER AN OVERWRITE. With a random id that
	// is a hand-made file, a copied one, or a broken random source.
	hand := filepath.Join(b.dir, "0000000000000000000000000000000f.board")
	if err := os.WriteFile(hand, []byte("BOARD v1\n# a file somebody placed by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, hand)
	exit, _, stderr := b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "a thing", "--by", "4h",
		"--default", "d", "--id", "0000000000000000000000000000000f")
	if exit != 1 || !strings.Contains(stderr, "ADD REFUSED") {
		t.Errorf("an add over an existing id: exit %d, stderr %q", exit, stderr)
	}
	if readFile(t, hand) != before {
		t.Error("the existing file's bytes were changed")
	}

	// AN ADD WHOSE TEXT MATCHES AN OPEN CARD prints a note on stderr and FILES ANYWAY. It
	// never refuses on the hash and never merges on it: a silent deduplication is what
	// The races forbids.
	dup := newBench(t)
	first := dup.add(plain("rowan", "the very same words")...)
	exit, stdout, stderr = dup.run(dup.board("add", "--as", "emma", "--text", "the very same words", "--by", "4h", "--default", "d")...)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "ADD NOTE hash=") || !strings.Contains(stderr, "matches open card "+first) {
		t.Errorf("no hash note on a duplicate filing: %q", stderr)
	}
	if field(stdout, "id=") == first {
		t.Error("the duplicate was merged into the first card")
	}
	_, stdout, _ = dup.run(dup.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "cards=2") {
		t.Errorf("two lines noticing one thing owe it twice until one closes `duplicate of`:\n%s", stdout)
	}
}

// A RETRY AFTER AN UNCERTAIN APPEND REUSES THE ID IT DREW.
func TestAnIdRetryIsTheSameFilingOrARefusal(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "a filing whose outcome nobody saw")...)
	// The same add, run again with --id and the same fields: existed=true, nothing written.
	exit, stdout, stderr := b.run(append(b.board("add", "--id", id), plain("rowan", "a filing whose outcome nobody saw")...)...)
	if exit != 0 || !strings.Contains(stdout, "existed=true") {
		t.Errorf("an identical retry: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "id="+id) {
		t.Errorf("the retry drew a new id: %q", stdout)
	}
	if n := len(eventLines(t, filepath.Join(b.dir, id+".board"))); n != 1 {
		t.Errorf("the retry appended: the file holds %d events, want 1", n)
	}
	// The same --id with a different --text is a refusal, and nothing is written.
	exit, _, stderr = b.run(append(b.board("add", "--id", id), plain("rowan", "a different filing entirely")...)...)
	if exit != 1 || !strings.Contains(stderr, "different fields") {
		t.Errorf("a differing retry: exit %d, stderr %q", exit, stderr)
	}
	if n := len(eventLines(t, filepath.Join(b.dir, id+".board"))); n != 1 {
		t.Errorf("a refused retry wrote: the file holds %d events, want 1", n)
	}
	// TWO CARD LINES WITH ONE ID AND DIFFERENT FIELDS are a conflict in the fold, and
	// neither silently wins.
	file := filepath.Join(b.dir, id+".board")
	base := readFile(t, file)
	forged := strings.Replace(strings.TrimRight(base, "\n"), "\n", "\n", 1) + "\n" +
		strings.Replace(eventLines(t, file)[0], "as=rowan", "as=emma", 1) + "\n"
	got := merged(t, b, id, forged)
	if !strings.Contains(got, "conflict=true") || !strings.Contains(got, "conflicts=1") {
		t.Errorf("a differing payload under one identity is a conflict:\n%s", got)
	}
}

// ------------------------------------------------------------------- the two backends

// Both backends, driven end to end through a FAKE gh on PATH — the recorded thread, never
// the network. An overridden close shows override=true from both, with no git metadata and
// no comment author read: the read side derives from as= and override= and nothing else.
func TestTheTwoBackendsRenderIdenticalListings(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "a card closed over somebody's live take")...)
	if exit, _, stderr := b.atTime(b.now.Add(time.Minute), b.board("take", "--as", "ada", "--card", id, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take: exit %d %s", exit, stderr)
	}
	exit, _, stderr := b.atTime(b.now.Add(2*time.Minute), b.board("close", "--as", "bo", "--card", id, "--stale", "10m", "--how", "it landed under another number", "--anyway")...)
	if exit != 0 {
		t.Fatalf("close --anyway: exit %d %s", exit, stderr)
	}
	// Both listings are taken at ONE instant, because age= is a derivation from the clock
	// and a difference there would be a difference in the test rather than in the tool.
	_, viaDir, _ := b.atTime(b.now.Add(5*time.Minute), b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(viaDir, "BOARD CLOSE") || !strings.Contains(viaDir, "override=true") {
		t.Fatalf("the dir backend does not show the override:\n%s", viaDir)
	}

	lines := eventLines(t, filepath.Join(b.dir, id+".board"))
	viaIssue := issueListing(t, b, lines, "list", "--stale", "10m", "--list")
	if stripSource(viaIssue) != stripSource(viaDir) {
		t.Errorf("identical histories printed different listings:\n--- dir ---\n%s\n--- issue ---\n%s", viaDir, viaIssue)
	}
	if !strings.Contains(viaIssue, "BOARD CLOSE id="+id) || !strings.Contains(viaIssue, "by=bo") {
		t.Errorf("the close's actor is not derived from as=:\n%s", viaIssue)
	}
}

// A whole add/take/close cycle through the issue backend, so that the gh argv is asserted:
// an event's text goes in on STDIN and is never an argument.
func TestTheIssueBackendWritesThroughStdinAndReadsTheWholeThread(t *testing.T) {
	b := newBench(t)
	store, argv := fakeGH(t)
	exit, stdout, stderr := b.run("add", "--issue", "mas-bandwidth/schema#876", "--as", "rowan",
		"--text", "a card filed through the issue backend", "--by", "4h", "--default", "rowan files it")
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "backend=issue durable=true") {
		t.Errorf("an add --issue is durable when the command returns: %q", stdout)
	}
	id := field(stdout, "id=")
	for i := 0; i < 45; i++ {
		if exit, _, stderr := b.run("add", "--issue", "mas-bandwidth/schema#876", "--as", "emma",
			"--text", "filler card "+itoa(i), "--by", "4h", "--default", "emma files it"); exit != 0 {
			t.Fatalf("exit %d: %s", exit, stderr)
		}
	}
	// THE READ IS THE WHOLE LOG: the prototype's forty-comment window would have lost the
	// first card, and a vanished card makes the count fall without any work being done.
	_, stdout, _ = b.run("list", "--issue", "mas-bandwidth/schema#876", "--stale", "10m", "--list", "--max", "0")
	if !strings.Contains(stdout, "cards=46") {
		t.Errorf("the read is not the whole thread:\n%s", stdout)
	}
	if !strings.Contains(stdout, id) {
		t.Error("the card filed forty-six events ago vanished from the listing")
	}
	// Nothing a filer wrote ever reached gh's command line.
	for _, line := range strings.Split(readFile(t, argv), "\n") {
		if strings.Contains(line, "a card filed through the issue backend") {
			t.Errorf("an event's text went on gh's command line: %q", line)
		}
	}
	if !strings.Contains(readFile(t, store), "card "+id) {
		t.Error("the store does not hold the card event")
	}
}

// ------------------------------------------------------------------------------ helpers

// clone copies a board directory, which is what a second checkout of the repository is.
func clone(t *testing.T, dir string) string {
	t.Helper()
	out := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, e.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// merged writes one card file's bytes into a fresh board and lists it: the union merge,
// in whatever order the caller chose.
func merged(t *testing.T, b *bench, id, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, id+".board"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stdout, stderr := b.atTime(b.now.Add(5*time.Minute), "list", "--dir", dir, "--stale", "10m", "--list", "--max", "0")
	if stderr != "" {
		t.Fatalf("listing the merge: %s", stderr)
	}
	return stdout
}

// issueListing puts the same event lines through the ISSUE backend, with the fake gh on
// PATH, and returns what the tool printed.
func issueListing(t *testing.T, b *bench, lines []string, args ...string) string {
	t.Helper()
	store, _ := fakeGH(t)
	if err := os.WriteFile(store, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	full := append([]string{args[0], "--issue", "mas-bandwidth/schema#876"}, args[1:]...)
	_, stdout, stderr := b.atTime(b.now.Add(5*time.Minute), full...)
	if stderr != "" {
		t.Fatalf("listing through the issue backend: %s", stderr)
	}
	return stdout
}

// fakeGH builds the recorded gh, POINTS THE BACKEND AT IT BY PATH, and returns the
// thread's store and the argv log. Nothing here reaches the network.
//
// THE EXECUTABLE SUFFIX IS PART OF THE NAME. A fake built as plain "gh" is not a program
// Windows will run; the lookup passed over it, found the runner's real gh, and two tests
// talked to github.com from CI until they were refused for a missing token. So the suffix
// comes from runtime.GOOS, and the backend is handed the absolute path rather than asked
// to look anything up.
//
// Belt and braces, in three layers, because a test that silently reaches the real gh is a
// test that proves nothing and says so to nobody:
//
//  1. the backend is INJECTED with this binary's path, so no lookup happens at all;
//  2. this directory is still FIRST on PATH, for any lookup a child process might do;
//  3. a real gh reached from here would fail locally instead of talking to github.com —
//     the host is under the reserved .invalid TLD, which resolves nowhere, and the config
//     directory is an empty one, so no stored credential is found; and
//  4. the TRIPWIRE: the fake records every argv it is called with, and a run that leaves
//     that log empty means some OTHER gh served the call, which fails the test by name.
func fakeGH(t *testing.T) (store, argv string) {
	t.Helper()
	bin := t.TempDir()
	prog := filepath.Join(bin, "gh")
	if runtime.GOOS == "windows" {
		prog += ".exe"
	}
	build := exec.Command("go", "build", "-o", prog, "./testdata/fakegh/main.go")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fake gh: %v\n%s", err, out)
	}
	store = filepath.Join(t.TempDir(), "thread.txt")
	argv = filepath.Join(filepath.Dir(store), "argv.txt")
	t.Cleanup(board.SetGHBinary(prog))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_BOARD_FAKE_GH_STORE", store)
	t.Setenv("NOVA_BOARD_FAKE_GH_ARGV", argv)
	t.Setenv("GH_HOST", "nova-board-tests.invalid")
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	t.Setenv("GH_TOKEN", "the-recorded-gh-is-the-only-gh-these-tests-may-run")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(argv); err != nil || strings.TrimSpace(string(raw)) == "" {
			t.Errorf("the recorded gh at %s was never called; some other gh served this test's calls, and a test that reaches the real one touches the network", prog)
		}
	})
	return store, argv
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// eventLines is a card file's events, without its version line.
func eventLines(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readFile(t, path), "\n") {
		if strings.TrimSpace(line) != "" && strings.TrimSpace(line) != "BOARD v1" {
			out = append(out, line)
		}
	}
	return out
}

func lastLine(t *testing.T, path string) string {
	t.Helper()
	lines := eventLines(t, path)
	return lines[len(lines)-1]
}

func evOf(line string) string {
	for _, tok := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(tok, "ev="); ok {
			return v
		}
	}
	return ""
}

// stripSource drops the source= field, which is the one thing two backends may differ on.
func stripSource(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		var toks []string
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "source=") || strings.HasPrefix(tok, "backend=") {
				continue
			}
			toks = append(toks, tok)
		}
		kept = append(kept, strings.Join(toks, " "))
	}
	return strings.Join(kept, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// stuck is a BROKEN RANDOM SOURCE: it returns the same bytes on every draw, which is the
// third way the spec names for an id to already exist — "a hand-made file, a copied one,
// or a broken random source". A tool that trusted its draw would file the second card
// over the first.
type stuck struct{ b byte }

func (s stuck) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = s.b
	}
	return len(p), nil
}

// with drives the binary at this bench's clock with another random source.
func (b *bench) with(rnd io.Reader, args ...string) (int, string, string) {
	b.t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, b.now, rnd)
	return exit, out.String(), errb.String()
}

// AN ID THAT ALREADY EXISTS IS REFUSED IN BOTH BACKENDS, and the drawn id is looked up
// too. Spec, Creation is exclusive against hand-made files: "under `--dir` the card file
// is created with `O_EXCL` and never truncated; under `--issue` the board is re-read
// immediately before the append and the id looked for. An id that already exists — which
// with a random id means a hand-made file, a copied one, or a broken random source — is
// `ADD REFUSED: id <id> exists; nothing written` at exit 1". Work list 1 asks for it by
// the same case: "two ids drawn from a source that returns the same bytes twice are
// refused by `O_EXCL`, never overwritten". A second filing that folded into the first
// card would be the silent deduplication The races forbids, and the count falling for a
// reason other than work.
func TestABrokenRandomSourceIsRefusedInBothBackends(t *testing.T) {
	t.Run("dir", func(t *testing.T) {
		b := newBench(t)
		exit, stdout, stderr := b.with(stuck{0xab}, b.board("add", "--as", "rowan", "--text", "the first filing",
			"--by", "4h", "--default", "d")...)
		if exit != 0 {
			t.Fatalf("the first add: exit %d %s", exit, stderr)
		}
		id := field(stdout, "id=")
		exit, _, stderr = b.with(stuck{0xab}, b.board("add", "--as", "rowan", "--text", "the second filing",
			"--by", "4h", "--default", "d")...)
		if exit != 1 || !strings.Contains(stderr, "ADD REFUSED: id "+id+" exists; nothing written") {
			t.Errorf("a second draw of one id: exit %d, stderr %q", exit, stderr)
		}
		if n := len(eventLines(t, filepath.Join(b.dir, id+".board"))); n != 1 {
			t.Errorf("the refused add wrote: the card file holds %d events, want 1", n)
		}
		_, listing, _ := b.run(b.board("list", "--stale", "10m")...)
		if !strings.Contains(listing, "cards=1") {
			t.Errorf("two filings under one id did not stay one card and one refusal:\n%s", listing)
		}
	})
	t.Run("issue", func(t *testing.T) {
		b := newBench(t)
		store, _ := fakeGH(t)
		issue := []string{"add", "--issue", "mas-bandwidth/schema#876", "--as", "rowan", "--by", "4h", "--default", "d"}
		exit, stdout, stderr := b.with(stuck{0xcd}, append(issue, "--text", "the first filing")...)
		if exit != 0 {
			t.Fatalf("the first add: exit %d %s", exit, stderr)
		}
		id := field(stdout, "id=")
		exit, _, stderr = b.with(stuck{0xcd}, append(issue, "--text", "the second filing")...)
		if exit != 1 || !strings.Contains(stderr, "ADD REFUSED: id "+id+" exists; nothing written") {
			t.Errorf("a second draw of one id under --issue: exit %d, stderr %q", exit, stderr)
		}
		if n := strings.Count(readFile(t, store), "card "+id); n != 1 {
			t.Errorf("the thread holds %d card lines under one id, want 1: nothing is written when the id exists", n)
		}
		_, listing, _ := b.run("list", "--issue", "mas-bandwidth/schema#876", "--stale", "10m")
		if !strings.Contains(listing, "cards=1") || !strings.Contains(listing, "conflicts=0") {
			t.Errorf("two filings folded into one card with nothing said:\n%s", listing)
		}
	})
}

// TWO PROCESSES UNDER --issue DRAW TWO IDS AND NEITHER IS REFUSED. Spec, Tests this spec
// demands 3: "two rows filed under one name with one text on two legs at one injected
// second ... from two clones under `--dir` and from two processes under `--issue`, produce
// two cards with two distinct thirty-two-hex ids and neither is refused". The exclusivity
// above may not cost the ordinary concurrent filing: a board that refused one of two real
// filings would be the count failing to climb while a review was arriving.
func TestTwoProcessesUnderIssueBothFile(t *testing.T) {
	b := newBench(t)
	fakeGH(t)
	var wg sync.WaitGroup
	ids := make([]string, 2)
	exits := make([]int, 2)
	for n, leg := range []string{"cpp", "go"} {
		wg.Add(1)
		go func(n int, leg string) {
			defer wg.Done()
			exit, stdout, _ := b.run("add", "--issue", "mas-bandwidth/schema#876", "--as", "rowan",
				"--text", "every field is written", "--by", "4h", "--default", "rowan probes it",
				"--thing", "every-field", "--leg", leg)
			exits[n], ids[n] = exit, field(stdout, "id=")
		}(n, leg)
	}
	wg.Wait()
	for n := range ids {
		if exits[n] != 0 {
			t.Errorf("the add from process %d was refused: exit %d", n, exits[n])
		}
		if len(ids[n]) != 32 || strings.Trim(ids[n], "0123456789abcdef") != "" {
			t.Errorf("id %q is not thirty-two lower-case hex", ids[n])
		}
	}
	if ids[0] == ids[1] {
		t.Fatalf("two processes drew one id %s", ids[0])
	}
	_, listing, _ := b.run("list", "--issue", "mas-bandwidth/schema#876", "--stale", "10m")
	if !strings.Contains(listing, "cards=2") {
		t.Errorf("two filings from two processes are two cards:\n%s", listing)
	}
}

// CONCURRENT EVENTS ARE COUNTED, NOT HIDDEN. Spec, Tests this spec demands 3: "two takes
// from two clones with the same `after=` fold to one owner by the total order, `BOARD CARD
// conflicts=1`, `BOARD OK conflicts=1`, identically from both merge orders and both
// backends; a take whose `after=` names the other take is not a conflict and
// `conflicts=0`" — and "every `taken`, `closed`, `landed` and `probed` line carries a
// distinct twelve-hex `ev=` from the injected source and an `after=` equal to the newest
// `ev=` in the writer's fold or the card id"; "`list --list --owner Bo` prints only `Bo`'s
// open cards and `BOARD OK` still counts the whole board".
//
// The migration fixture clause of this test belongs to work list 10, which is not in this
// PR and is named owed in its body; it is the one clause here that is not yet pinned.
func TestConcurrentEventsAreCountedNotHidden(t *testing.T) {
	b := newBench(t)
	id := b.add(plain("rowan", "the one card two lines both reach for")...)
	one := b.now.Add(time.Minute)
	ada := clone(t, b.dir)
	bo := clone(t, b.dir)
	for dir, who := range map[string]string{ada: "Ada", bo: "Bo"} {
		if exit, _, stderr := b.atTime(one, "take", "--dir", dir, "--as", who, "--card", id, "--stale", "10m"); exit != 0 {
			t.Fatalf("take by %s: exit %d %s", who, exit, stderr)
		}
	}
	adaLine := lastLine(t, filepath.Join(ada, id+".board"))
	boLine := lastLine(t, filepath.Join(bo, id+".board"))
	base := readFile(t, filepath.Join(b.dir, id+".board"))
	forward := merged(t, b, id, base+adaLine+"\n"+boLine+"\n")
	backward := merged(t, b, id, base+boLine+"\n"+adaLine+"\n")
	lines := strings.Split(strings.TrimSpace(base+adaLine+"\n"+boLine), "\n")[1:]
	viaIssue := issueListing(t, b, lines, "list", "--stale", "10m", "--list")
	for name, got := range map[string]string{"the forward merge": forward, "the backward merge": backward, "the issue backend": viaIssue} {
		if !strings.Contains(got, "owner=Bo") {
			t.Errorf("%s names another owner; the take whose as= sorts later at one second is the owner:\n%s", name, got)
		}
		if count(got, "BOARD CARD") != 1 || !strings.Contains(got, "conflicts=1 conflict=") {
			t.Errorf("%s does not carry conflicts=1 on the card:\n%s", name, got)
		}
		if !strings.Contains(got, "BOARD OK cards=1") || !strings.Contains(got, "conflicts=1 quarantined=0") {
			t.Errorf("%s does not count the pair the fold chose over on BOARD OK:\n%s", name, got)
		}
	}
	if stripSource(forward) != stripSource(backward) || stripSource(forward) != stripSource(viaIssue) {
		t.Errorf("the same events printed differently:\n%s\n%s\n%s", forward, backward, viaIssue)
	}
	// The merge lands in this bench's own board, so the rest of the test reads what two
	// clones that pushed would have.
	if err := os.WriteFile(filepath.Join(b.dir, id+".board"), []byte(base+adaLine+"\n"+boLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A TAKE THAT NAMES THE OTHER TAKE IS NOT A CONFLICT: the writer saw it.
	seen := b.add(plain("rowan", "a card taken twice, the second knowing the first")...)
	for _, who := range []string{"Bo", "Ada"} {
		args := b.board("take", "--as", who, "--card", seen, "--stale", "10m")
		if who == "Ada" {
			args = append(args, "--anyway")
		}
		if exit, _, stderr := b.atTime(one, args...); exit != 0 {
			t.Fatalf("take by %s: exit %d %s", who, exit, stderr)
		}
	}
	_, listing, _ := b.atTime(one, b.board("list", "--stale", "10m", "--list", "--open")...)
	if !strings.Contains(listing, "conflicts=0") || !strings.Contains(listing, "owner=Ada") {
		t.Errorf("a take that names the take it saw is not concurrent:\n%s", listing)
	}

	// EVERY LATER EVENT CARRIES A DISTINCT TWELVE-HEX ev= AND AN after= THE WRITER HELD.
	four := b.add(plain("rowan", "a card that gets one of every later verb")...)
	row := b.add(append(plain("rowan", "a row of the owed ledger"), "--thing", "every-field", "--leg", "cpp")...)
	if exit, _, stderr := b.run(b.board("take", "--as", "rowan", "--card", four, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take: exit %d %s", exit, stderr)
	}
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", four, "--stale", "10m", "--how", "it is done")...); exit != 0 {
		t.Fatalf("close: exit %d %s", exit, stderr)
	}
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", row, "--stale", "10m", "--probed", "./reports/probe.txt")...); exit != 0 {
		t.Fatalf("probe: exit %d %s", exit, stderr)
	}
	landedID := b.add(plain("rowan", "a card that lands under a number")...)
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", landedID, "--stale", "10m", "--landed", "mas-bandwidth/nova-tools#9")...); exit != 0 {
		t.Fatalf("land: exit %d %s", exit, stderr)
	}
	evs := map[string]string{}
	verbs := map[string]bool{}
	for _, card := range []string{four, row, landedID} {
		held := map[string]bool{card: true}
		for _, line := range eventLines(t, filepath.Join(b.dir, card+".board"))[1:] {
			verb := strings.Fields(line)[0]
			verbs[verb] = true
			ev := evOf(line)
			if len(ev) != 12 || strings.Trim(ev, "0123456789abcdef") != "" {
				t.Errorf("%s carries ev=%q, want twelve lower-case hex", verb, ev)
			}
			if where, seen := evs[ev]; seen {
				t.Errorf("%s reuses the ev= of %s: %s", verb, where, ev)
			}
			evs[ev] = verb
			after := field(line, "after=")
			if !held[after] {
				t.Errorf("%s names after=%s, which its own fold did not hold: %s", verb, after, line)
			}
			held[ev] = true
		}
	}
	for _, verb := range []string{"taken", "closed", "probed", "landed"} {
		if !verbs[verb] {
			t.Errorf("no %s line was written; the clause covers all four later verbs", verb)
		}
	}

	// `list --list --owner Bo` PRINTS ONLY Bo's OPEN CARDS and BOARD OK still counts the
	// whole board: the listing is capped and filtered, the counting never is.
	bosDone := b.add(plain("rowan", "a card Bo took and finished")...)
	for _, verb := range [][]string{
		b.board("take", "--as", "Bo", "--card", bosDone, "--stale", "10m"),
		b.board("close", "--as", "Bo", "--card", bosDone, "--stale", "10m", "--how", "Bo finished it"),
	} {
		if exit, _, stderr := b.run(verb...); exit != 0 {
			t.Fatalf("%v: exit %d %s", verb[0], exit, stderr)
		}
	}
	_, mine, _ := b.atTime(one, b.board("list", "--stale", "10m", "--list", "--owner", "Bo")...)
	if strings.Contains(mine, bosDone) || strings.Contains(mine, "state=CLOSED") {
		t.Errorf("one line's own batch holds a card that is no longer owed:\n%s", mine)
	}
	if count(mine, "BOARD CARD") != 1 || !strings.Contains(mine, "owner=Bo") {
		t.Errorf("--list --owner Bo printed %d cards, want Bo's one:\n%s", count(mine, "BOARD CARD"), mine)
	}
	if !strings.Contains(mine, "BOARD OK cards=6") {
		t.Errorf("the filtered listing changed the board's counts:\n%s", mine)
	}
}

// A WRITER NEVER NAMES AN after= ITS OWN FOLD DOES NOT HOLD. Spec, Tests this spec demands
// 3: "`take --anyway` against a fold that lacks the named `after=` is refused", and The
// fold order: "take, close, land and probe refuse to append an `after=` that their own
// fold does not hold". The CLI cannot reach that refusal, and this test says why: after=
// is DERIVED from the writer's own fold by Card.After, which walks the folded events and
// falls back to the card id, so Card.Holds is true of it by construction — including when
// the card's newest event is quarantined, which is the case the refusal was written for.
// The refusal stays as the guard on that invariant; the invariant is what is pinned here.
func TestATakeNeverNamesAnAfterTheFoldDoesNotHold(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add(plain("rowan", "a card whose newest event is quarantined")...)
	file := filepath.Join(b.dir, id+".board")
	good := readFile(t, file)
	stamp := b.now.Add(time.Minute).UTC().Format(time.RFC3339)
	broken := "taken " + id + " ev=111111111111 after=999999999999 as=zz at=" + stamp + " override=false\n"
	if err := os.WriteFile(file, []byte(good+broken), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := b.atTime(b.now.Add(2*time.Minute), b.board("take", "--as", "ada", "--card", id, "--stale", "10m", "--anyway")...)
	if exit != 0 {
		t.Fatalf("a take over a quarantined event: exit %d %s", exit, stderr)
	}
	if !strings.Contains(stdout, "TAKE OK") {
		t.Errorf("the take did not run: %q", stdout)
	}
	after := field(lastLine(t, file), "after=")
	if after != id {
		t.Errorf("after=%s, want the card id: a quarantined event is not in the fold, so it is never named", after)
	}
	_, listing, _ := b.atTime(b.now.Add(2*time.Minute), b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(listing, "owner=ada") || !strings.Contains(listing, "quarantined=1") {
		t.Errorf("the fold did not keep the take and the quarantine apart:\n%s", listing)
	}
}
