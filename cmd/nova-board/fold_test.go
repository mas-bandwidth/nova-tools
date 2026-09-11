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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// --------------------------------------------------------------------------------- 3

func TestTheBoardIsAFoldOverCardFilesWithNoLock(t *testing.T) {
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
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is built and put on PATH; the exe suffix makes this a different test")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is built and put on PATH")
	}
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

// fakeGH builds the recorded gh, puts it on PATH, and returns the thread's store and the
// argv log. Nothing here reaches the network.
func fakeGH(t *testing.T) (store, argv string) {
	t.Helper()
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "gh"), "./testdata/fakegh/main.go")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fake gh: %v\n%s", err, out)
	}
	store = filepath.Join(t.TempDir(), "thread.txt")
	argv = filepath.Join(filepath.Dir(store), "argv.txt")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_BOARD_FAKE_GH_STORE", store)
	t.Setenv("NOVA_BOARD_FAKE_GH_ARGV", argv)
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
