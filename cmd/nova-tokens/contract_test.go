package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// The contract tests: the exit codes, the ceilings, the source-level tripwires the spec
// demands, and the two facts a test can pin that a reading of the code cannot.

// ---------------------------------------------------------------- the source tripwires

// pkgFiles parses every non-test .go file of a package under the repo root.
func pkgFiles(t *testing.T, pkg string) map[string]*ast.File {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	ents, err := os.ReadDir(filepath.Join(root, pkg))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, pkg, e.Name()), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = f
	}
	if len(out) == 0 {
		t.Fatalf("no source files under %s; this tripwire was looking in the wrong place and would have passed by checking nothing", pkg)
	}
	return out
}

func pkgText(t *testing.T, pkg string) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(root, pkg))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, pkg, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}

// Rule 9 and demanded test 8: this tool removes NOTHING. The prototype removed the old
// month files on every real run and noted it in a list capped at six.
func TestNothingInThisToolRemovesAFile(t *testing.T) {
	// Rule 9 says "deletes, truncates or trims", and a tripwire that searches only for the
	// three removal names is hollow for the middle word: `f.Truncate(0)` on the lock and
	// `os.Create` on the scratch copy both truncate and both walked past it. Every call
	// that can empty a file is searched for, and the four the tool is allowed are carved
	// out here BY FILE, with the reason -- each one a file THIS RUN makes, never a file the
	// tool was given.
	emptiers := []string{"os.Remove", "os.RemoveAll", "os.Truncate", ".Truncate(", "os.Create(", "os.WriteFile("}
	allowed := map[string][]string{
		// The platform with no flock: the lock is an exclusive create and its release
		// removes the sentinel this run made.
		"internal/tokens/lock_other.go": {"os.Remove"},
		// The fold's own lock file, whose whole body this run wrote.
		"internal/tokens/lock.go": {".Truncate("},
		// The copy under --scratch, made from the live database this run and read there;
		// the live file is never opened for writing.
		"internal/tokens/opencode.go": {"os.Create("},
		// The fixed `<day>.tsv.tmp` a day is written through before the one rename, the
		// name `check` steps over by rule 9's own last sentence.
		"internal/tokens/dayfile.go": {"os.WriteFile("},
		// The same, for the report `report` writes.
		"cmd/nova-tokens/main.go": {"os.WriteFile("},
	}
	used := map[string]bool{}
	for _, pkg := range []string{"internal/tokens", "cmd/nova-tokens"} {
		for name, text := range pkgText(t, pkg) {
			path := pkg + "/" + name
			for _, forbidden := range emptiers {
				if !strings.Contains(text, forbidden) {
					continue
				}
				ok := false
				for _, a := range allowed[path] {
					if a == forbidden {
						ok, used[path+" "+forbidden] = true, true
					}
				}
				if !ok {
					t.Errorf("%s calls %s; this tool deletes, truncates and trims nothing -- not a month file, not a log, not a stray (rule 9). A file THIS RUN makes is carved out by name in this test and in the spec, or it is a bug", path, forbidden)
				}
			}
		}
	}
	// A carve-out nothing uses any more is a hole left open in the tripwire.
	for path, names := range allowed {
		for _, n := range names {
			if !used[path+" "+n] {
				t.Errorf("%s no longer calls %s; drop the carve-out rather than leaving the tripwire open on that file (rule 9)", path, n)
			}
		}
	}
}

// Rule 16 and demanded tests 6 and 16: no network, and exactly one subprocess.
func TestTheOnlySubprocessIsSqlite3AndThereIsNoNetwork(t *testing.T) {
	files := pkgFiles(t, "internal/tokens")
	for name, f := range files {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "net" || strings.HasPrefix(path, "net/") {
				t.Errorf("internal/tokens/%s imports %q; this tool talks to no network", name, path)
			}
			if path == "os/exec" && name != "opencode.go" {
				t.Errorf("internal/tokens/%s imports os/exec; the one subprocess is sqlite3, and it lives in opencode.go (rule 19)", name)
			}
		}
	}
	text := pkgText(t, "internal/tokens")
	for name, body := range text {
		if name == "opencode.go" {
			continue
		}
		if strings.Contains(body, "exec.Command") {
			t.Errorf("internal/tokens/%s starts a subprocess; there is one, and it is sqlite3", name)
		}
	}
	// And no git at all: an earlier draft ran `git log` to order competing notes, and
	// that order is now in the notes themselves.
	for name, body := range text {
		if strings.Contains(body, `"git"`) {
			t.Errorf("internal/tokens/%s names git; the tool runs none (rule 16)", name)
		}
	}
}

// Rule 5 and demanded test 4's source half: ONE attribution rule, in one function, and no
// reader carrying a regexp of its own. The prototype had two tables in two scripts and
// they disagreed about three repos.
func TestOnlyRepoGoCarriesTheAttributionRule(t *testing.T) {
	for name, body := range pkgText(t, "internal/tokens") {
		if name == "repo.go" || name == "bus.go" {
			continue // bus.go's regexps are the note GRAMMAR, not a repo table
		}
		if strings.Contains(body, "regexp.MustCompile") || strings.Contains(body, "regexp.Compile") {
			t.Errorf("internal/tokens/%s compiles a regexp; the repo table is the caller's file and the rule is one function in repo.go", name)
		}
	}
	// Every reader reaches a repo name through that one function.
	for _, reader := range []string{"claude.go", "opencode.go", "swarm.go", "bus.go"} {
		body := pkgText(t, "internal/tokens")[reader]
		if !strings.Contains(body, "rules.Attribute") {
			t.Errorf("internal/tokens/%s does not call the attribution function", reader)
		}
	}
}

// Rule 15's source half: nothing folds one type into another, and no reader writes a
// literal zero for a type it did not read. Every DeepSeek row and every Claude reasoning
// cell in the prototype said zero when nothing had measured them.
func TestNoReaderWritesAZeroForATypeItDidNotRead(t *testing.T) {
	for name, body := range pkgText(t, "internal/tokens") {
		if !strings.Contains(body, "Counts.Set") && !strings.Contains(body, ".Set(t,") && !strings.Contains(body, "Set(t, v)") {
			continue
		}
		if strings.Contains(body, "Set(t, 0)") || strings.Contains(body, "Counts.Set(t, 0)") {
			t.Errorf("internal/tokens/%s writes a literal 0 into a type cell; a type the source did not report is a dash (rule 15)", name)
		}
	}
}

// Rule 14's tripwire: nothing under done/ or failed/ is opened. The answer has to be the
// same before and after `reclaim`, which is the whole reason the usage file is a sidecar.
func TestNothingUnderDoneOrFailedIsOpened(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRow("j1", "1", "-", "m", "schema", "2026-09-11T10:00:00Z", "1", "2", "3", "4", "5"))
	makeUnreadable(t, write(t, filepath.Join(pool, "done", "j2", "usage.tsv"), "nothing here may be opened\n"))
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "d="+pool)
	// An unreadable file under done/ would be one TOKENS UNREADABLE line if it were opened.
	wantExit(t, r, 0)
	wantNotContains(t, r.all(), "UNREADABLE")
	wantContains(t, r.stdout, "nousage=1")
}

// ---------------------------------------------------------------- the lock (demanded test 8)

func TestASecondFoldWaitsAndThenRefusesNamingTheHolder(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go")+"\n")
	release, err := tokens.TakeFoldLock(out, tokens.LockWait)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The second one waits its bounded time and refuses rather than writing beside the
	// first: two folds on one --out write one fixed temp name.
	start := time.Now()
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "fold.lock")
	wantContains(t, r.stderr, "pid ")
	if waited := time.Since(start); waited < 500*time.Millisecond {
		t.Errorf("the second fold refused after %s; it is supposed to wait for the first", waited)
	}
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
		t.Error("the refused fold wrote a day file")
	}
}

// ---------------------------------------------------------------- one pass over each file

func TestEachDeclaredFileIsOpenedOncePerRun(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	for i := range 12 {
		write(t, filepath.Join(tr, string(rune('a'+i))+".jsonl"),
			msg("m"+string(rune('a'+i)), "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go")+"\n")
	}
	before := tokens.Opens()
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr), 0)
	if got := tokens.Opens() - before; got != 12 {
		t.Errorf("%d source opens for 12 files; the fold is one pass over each declared file", got)
	}
}

// ---------------------------------------------------------------- the ceilings

func TestMaxZeroPrintsAllAndMaxNegativeIsRefused(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	for _, verb := range [][]string{
		{"check", "--out", out, "--max", "-1"},
		{"sum", "--out", out, "--month", "2026-09", "--max", "-1"},
	} {
		r := invoke(t, verb...)
		wantExit(t, r, 2)
		wantContains(t, r.stderr, "--max")
		wantContains(t, r.stderr, "0 for all")
	}
}

// ---------------------------------------------------------------- overlapping sources

// TestTwoSourcesOverThatOneTreeAreNamed pins the collapse the spec declares and nothing
// pinned: "It does not detect overlap between sources." Measured 2026-09-11 on the real
// bench: ~/.claude/projects/<session>/subagents/agent-*.jsonl and
// /private/tmp/.../tasks/*.output are the SAME messages, and declaring both reported
// 2,932,982,350 cache_read against the correct 1,502,293,166 -- written=true, check OK,
// sum OK, and nothing anywhere said the day had been doubled. The numbers still double,
// because that is what the spec says this tool does; the run now says so.
func TestTwoSourcesOverOneTreeAreNamedInTheRemedy(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	one := mkdir(t, filepath.Join(dir, "one"))
	two := mkdir(t, filepath.Join(dir, "two"))
	line := msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go") + "\n"
	write(t, filepath.Join(one, "a.jsonl"), line)
	write(t, filepath.Join(two, "a.jsonl"), line)

	single := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+one)
	wantExit(t, single, 0)
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "fable\tschema\t100\t")
	wantContains(t, lineWith(single.stdout, "TOKENS NOTE"), "nothing was wrong")

	out2 := mkdir(t, filepath.Join(dir, "out2"))
	both := invoke(t, "fold", "--out", out2, "--day", "2026-09-11", "--repos", repos,
		"--claude", "bench="+one, "--claude", "copy="+two)
	wantExit(t, both, 0)
	// The day IS doubled -- the spec says the fold does not de-duplicate across sources --
	// and the remedy names the two labels and the count.
	wantContains(t, read(t, filepath.Join(out2, "2026-09-11.tsv")), "fable\tschema\t200\t")
	note := lineWith(both.stdout, "TOKENS NOTE")
	wantContains(t, note, "claude:bench")
	wantContains(t, note, "claude:copy")
	wantContains(t, note, "1 message ids")
	wantContains(t, note, "TWICE")
}

// ---------------------------------------------------------------- the bus, read whole

// TestReportWithOneUnreadableSourceExitsOne pins rule 3 on the verb that skipped it:
// "The fold continues over the rest and writes what it could compute, and the run exits 1,
// because a declared source is a claim that the report covers it." cmdReport counted the
// unreadable and then returned 0 whenever any line printed, so a friend pasted a partial
// day onto the bus under REPORT OK.
func TestReportWithOneUnreadableSourceExitsOne(t *testing.T) {
	dir := t.TempDir()
	repos := reposFile(t, dir)
	good := mkdir(t, filepath.Join(dir, "good"))
	write(t, filepath.Join(good, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 3}, "/x/schema/a.go")+"\n")
	bad := mkdir(t, filepath.Join(dir, "bad"))
	makeUnreadable(t, write(t, filepath.Join(bad, "x.jsonl"), "{}\n"))

	r := invoke(t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", repos,
		"--claude", "g="+good, "--claude", "b="+bad)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS UNREADABLE")
	// Exit 1 still writes: the body printed, so the friend can see what it could compute.
	wantContains(t, r.stdout, "2026-09-11\temma\tf\tschema\tinput\t3")
}

// TestAHalfReadSuccessorDoesNotReplaceItsPredecessor pins rule 6: "the successor is
// validated whole -- header, Date:, every body line -- before it replaces anything." A
// note with one unparsed body line was not dead, entered the superseded map, and its
// predecessor's numbers vanished behind a correction nobody could read whole.
func TestAHalfReadSuccessorDoesNotReplaceItsPredecessor(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	first := busNote(t, bus, "emma", "a.md", "emma-00000000000a", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tg\tschema\tinput\t100\n")
	busNote(t, bus, "emma", "b.md", "emma-00000000000b",
		"tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate,
		"2026-09-11\temma\tg\tschema\tinput\t250\nthis line is prose\n")

	r := invoke(t, "fold", "--out", out, "--all", "--repos", repos, "--bus", bus)
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "TOKENS UNPARSED")
	wantNotContains(t, r.stdout, "TOKENS SUPERSEDED")
	// Two tips now, so the lane-day is a conflict and nothing folds for it: the half-read
	// correction never quietly became the day.
	wantContains(t, r.stderr, "TOKENS CONFLICT label=bus:emma day=2026-09-11")
	if _, err := os.Stat(filepath.Join(out, "2026-09-11.tsv")); err == nil {
		t.Error("a day folded from a successor that did not parse whole")
	}
}

// ---------------------------------------------------------------- the footguns a new line hit

// TestCwdIsTheLowestRungOfTheAttributionLadder: a transcript whose every line carries
// "cwd":"/x/schema" and whose --repos matches it was `repos=1 unknown=100.0%`, because the
// paths came only from tool_use blocks. A conversational session, a pure Task fan-out, or
// any turn before the first tool call was unknown for the whole file -- and `unknown` is a
// bucket `check` is happy with.
func TestCwdIsTheLowestRungOfTheAttributionLadder(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	line := `{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","cwd":"/x/schema","message":{"id":"m1","model":"f","usage":{"input_tokens":100}}}`
	write(t, filepath.Join(tr, "a.jsonl"), line+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 0)
	wantContains(t, read(t, filepath.Join(out, "2026-09-11.tsv")), "f\tschema\t100\t")
	wantContains(t, lineWith(r.stdout, "TOKENS DAY"), "unknown=0.0%")

	// A tool path still wins over cwd: cwd is the LOWEST rung, not a new first one.
	tr2 := mkdir(t, filepath.Join(dir, "tr2"))
	write(t, filepath.Join(tr2, "a.jsonl"),
		`{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","cwd":"/x/schema","message":{"id":"m2","model":"f","usage":{"input_tokens":5},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x/serialize/a.go"}}]}}`+"\n")
	out2 := mkdir(t, filepath.Join(dir, "out2"))
	wantExit(t, invoke(t, "fold", "--out", out2, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr2), 0)
	wantContains(t, read(t, filepath.Join(out2, "2026-09-11.tsv")), "f\tserialize\t5\t")

	// THE WHOLE LADDER, in order, because cwd is a rung the spec's written rule does not
	// yet have (the PR body proposes the sentence). Below the tool paths: a line with no
	// path at all takes the PREVIOUS repo before it takes cwd, and a line whose paths
	// matched no rule is `other` -- a bucket, decided -- and is not reopened by cwd.
	tr3 := mkdir(t, filepath.Join(dir, "tr3"))
	write(t, filepath.Join(tr3, "a.jsonl"), strings.Join([]string{
		// serialize from a tool path, with a cwd that says schema: the path wins.
		`{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","cwd":"/x/schema","message":{"id":"p1","model":"f","usage":{"input_tokens":1},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x/serialize/a.go"}}]}}`,
		// no path at all: the previous repo (serialize) is the rung above cwd (schema).
		`{"type":"assistant","timestamp":"2026-09-11T10:01:00Z","cwd":"/x/schema","message":{"id":"p2","model":"f","usage":{"input_tokens":2}}}`,
		// a path that matches no rule is `other`, and cwd does not reopen it.
		`{"type":"assistant","timestamp":"2026-09-11T10:02:00Z","cwd":"/x/schema","message":{"id":"p3","model":"f","usage":{"input_tokens":4},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/elsewhere/a.go"}}]}}`,
	}, "\n")+"\n")
	out3 := mkdir(t, filepath.Join(dir, "out3"))
	wantExit(t, invoke(t, "fold", "--out", out3, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr3), 0)
	day := read(t, filepath.Join(out3, "2026-09-11.tsv"))
	wantContains(t, day, "f\tserialize\t3\t") // 1 + 2: the path, then the previous repo
	wantContains(t, day, "f\tother\t4\t")
	if strings.Contains(day, "f\tschema\t") {
		t.Errorf("cwd took a rung above the previous repo or above `other`:\n%s", day)
	}
}

// TestMessagesWithNoIDReachTheRemedyLine: noid= was a number on a green TOKENS SOURCE line
// and reached nothing else -- not the exit code, not TOKENS NOTE. 100% of a file's usage
// can be dropped that way under TOKENS OK (lesson 95: a number is not a sentence).
func TestMessagesWithNoIDReachTheRemedyLine(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 9000}, "/x/schema/a.go"),
		msg("m1", "2026-09-11T10:00:01Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
	}, "\n")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 0)
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "noid=1")
	note := lineWith(r.stdout, "TOKENS NOTE")
	wantContains(t, note, "no id")
	wantContains(t, note, "claude:g")
	wantNotContains(t, note, "nothing was wrong")
}

// TestSumPrintsADashWhereNoRowReportedTheType: help says a dash is "NEVER 0 ... a zero
// meaning 'not measured' would sum into a month claiming to be complete", and SUM PAIR
// printed reasoning=0 for a month whose every row had a dash there.
func TestSumPrintsADashWhereNoRowReportedTheType(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr), 0)

	s := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, s, 0)
	pair := lineWith(s.stdout, "SUM PAIR")
	wantContains(t, pair, "input=100")
	wantContains(t, pair, "reasoning=-")
	wantContains(t, pair, "dashes=0,1,1,1,1")
	wantContains(t, lineWith(s.stdout, "SUM TOTAL"), "reasoning=-")
}

// Rule 15 at the far edge: a month with no day files has no source that reported any
// type, so its five cells are dashes. The running total was printed instead -- `input=0
// output=0 cache_write=0 cache_read=0 reasoning=0` -- the one "not measured" zero rule 15
// forbids, in the one place the tool had no row to learn it from.
func TestAMonthWithNoDayFilesSumsToDashesNotZeros(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	r := invoke(t, "sum", "--out", out, "--month", "2026-09")
	total := lineWith(r.stdout, "SUM TOTAL")
	if total == "" {
		t.Fatalf("no SUM TOTAL line over an empty month:\n%s%s", r.stdout, r.stderr)
	}
	for _, want := range []string{"input=-", "output=-", "cache_write=-", "cache_read=-", "reasoning=-"} {
		if !strings.Contains(total, want) {
			t.Errorf("SUM TOTAL over a month with no day files is %q; want %s -- a type no source reported is a dash, never 0 (rule 15)", total, want)
		}
	}
}

// Rule 3 and the provider reader's comment stripping: a `#` is a comment only where a
// comment can be. The reader dropped every line whose first byte is `#` before the CSV
// reader saw anything, so a quoted field carrying a newline whose continuation line begins
// with `#` had that line deleted out of the middle of its own record -- the record then
// parsed from the wrong bytes, with the `line=` map off by as many lines as were removed.
func TestAQuotedFieldWhoseContinuationStartsWithAHashIsNotStripped(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	// The quoted field is the model, the only column of this export that can carry text;
	// its second line begins with `#` and is NOT a comment.
	export := write(t, filepath.Join(dir, "google.csv"), strings.Join([]string{
		"timestamp,model,input_tokens",
		`2026-09-11T10:00:00Z,"gemini`,
		"# 2.5",
		`pro",100`,
		"",
	}, "\n"))
	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--provider", "google:emma="+export)
	wantExit(t, r, 0)
	wantNotContains(t, r.stderr, "UNREADABLE")
	wantNotContains(t, r.stderr, "UNPARSED")
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	wantContains(t, day, "unattributed\t100\t")
	// The stripped line was part of the model's own text: dropping it writes a model name
	// the export never carried.
	wantContains(t, day, "2.5")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "rows=1")
}

// typeNames are the five type constants. A type expression is one of them, the index of a
// counts array, or the argument of a Get.
var typeNames = map[string]bool{"Input": true, "Output": true, "CacheWrite": true, "CacheRead": true, "Reasoning": true}

// typeExprs is every type expression inside an expression, rendered back to source text so
// two of them can be compared without a type checker.
func typeExprs(fset *token.FileSet, e ast.Expr) []string {
	var out []string
	add := func(x ast.Expr) {
		var b strings.Builder
		if err := printer.Fprint(&b, fset, x); err == nil {
			out = append(out, b.String())
		}
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			if typeNames[v.Name] {
				out = append(out, v.Name)
			}
		case *ast.IndexExpr:
			add(v.Index)
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Get" && len(v.Args) == 1 {
				add(v.Args[0])
			}
		}
		return true
	})
	return out
}

// Demanded test 15's first clause, which nothing pinned: "a source test asserts no function
// adds one type column into another". Every DeepSeek row in the prototype that said zero
// and every cell that carried its neighbour's number came from exactly this. A write of
// one type may only read THAT type: `c.Set(Input, c.n[Output])` is the shape this forbids.
// countsArrays are the arrays a type column lives in; an index into one of them is a type
// column and a map keyed by anything else is not.
func isCountsTarget(text string) bool {
	return strings.Contains(text, "Counts") || strings.HasSuffix(text, ".n") || strings.HasSuffix(text, ".has") ||
		strings.HasSuffix(text, "Totals") || strings.HasSuffix(text, "Dashes")
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return ""
	}
	return b.String()
}

func TestNoFunctionAddsOneTypeColumnIntoAnother(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, pkg := range []string{"internal/tokens", "cmd/nova-tokens"} {
		ents, err := os.ReadDir(filepath.Join(root, pkg))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(root, pkg, e.Name()), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				onCounts := false
				if fn.Recv != nil && len(fn.Recv.List) == 1 {
					onCounts = strings.Contains(exprText(fset, fn.Recv.List[0].Type), "Counts")
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					var into, from ast.Expr
					switch v := n.(type) {
					case *ast.CallExpr:
						sel, ok := v.Fun.(*ast.SelectorExpr)
						if !ok || sel.Sel.Name != "Set" || len(v.Args) != 2 {
							return true
						}
						if !onCounts && !isCountsTarget(exprText(fset, sel.X)) {
							return true
						}
						into, from = v.Args[0], v.Args[1]
					case *ast.AssignStmt:
						if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
							return true
						}
						ix, ok := v.Lhs[0].(*ast.IndexExpr)
						if !ok || !isCountsTarget(exprText(fset, ix.X)) {
							return true
						}
						into, from = ix.Index, v.Rhs[0]
					default:
						return true
					}
					checked++
					want := exprText(fset, into)
					for _, got := range typeExprs(fset, from) {
						if got != want {
							t.Errorf("%s/%s:%d writes the %s column from %s; the five types are kept apart and no function adds one type column into another (rule 15)",
								pkg, e.Name(), fset.Position(n.Pos()).Line, want, got)
						}
					}
					return true
				})
			}
		}
	}
	if checked < 5 {
		t.Fatalf("%d type-column writes examined; this tripwire was looking at the wrong shape and would have passed by checking nothing", checked)
	}
}
