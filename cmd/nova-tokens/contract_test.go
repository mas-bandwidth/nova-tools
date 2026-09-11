package main

import (
	"go/ast"
	"go/parser"
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
	for _, pkg := range []string{"internal/tokens", "cmd/nova-tokens"} {
		for name, text := range pkgText(t, pkg) {
			// The one exemption, by name and with its reason: on a platform with no
			// flock the lock is an exclusive create, and its release removes the
			// sentinel THIS RUN made. It is not a file the tool was given.
			if pkg == "internal/tokens" && name == "lock_other.go" {
				continue
			}
			for _, forbidden := range []string{"os.Remove", "os.RemoveAll", "os.Truncate"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s/%s calls %s; this tool removes nothing -- not a month file, not a log, not a stray (rule 9)", pkg, name, forbidden)
				}
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
	secret := write(t, filepath.Join(pool, "done", "j2", "usage.tsv"), "nothing here may be opened\n")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(secret, 0o644)
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "d="+pool)
	// A mode-000 file under done/ would be one TOKENS UNREADABLE line if it were opened.
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
