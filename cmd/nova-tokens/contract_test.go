package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The contract tests: the source-level tripwires the spec demands, then the refusals and
// the facts a run can pin that a reading of the code cannot.

func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// pkgText is every non-test .go file of a package under the repo root, its text by name.
func pkgText(t *testing.T, pkg string) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), pkg)
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range ents {
		if !e.IsDir() && isSource(e.Name()) {
			out[e.Name()] = testkit.ReadFile(t, filepath.Join(dir, e.Name()))
		}
	}
	require.NotEmpty(t, out, "no source files under %s; this tripwire was looking in the wrong place and would have passed by checking nothing", pkg)
	return out
}

// pkgFiles parses every non-test .go file of a package under the repo root.
func pkgFiles(t *testing.T, pkg string) map[string]*ast.File {
	t.Helper()
	fset, out := token.NewFileSet(), map[string]*ast.File{}
	for name, text := range pkgText(t, pkg) {
		f, err := parser.ParseFile(fset, name, text, parser.ParseComments)
		require.NoError(t, err)
		out[name] = f
	}
	return out
}

// rule9Emptiers is the tripwire's list of calls that can empty a file. It is a package
// variable and not a local so TestRule9EmptierListMatchesTheSpec below can pin it: a name
// quietly deleted from this list would otherwise take its tripwire with it and go green.
var rule9Emptiers = []string{"os.Remove", "os.RemoveAll", "os.Truncate", ".Truncate(", "os.Create(", "os.WriteFile(", "os.O_TRUNC", "syscall.Unlink("}

// Rule 9 and demanded test 8: this tool removes NOTHING. The prototype removed the old
// month files on every real run and noted it in a list capped at six.
//
// Rule 9 says "deletes, truncates or trims", and a tripwire that searches only for the three
// removal names is hollow for the middle word: `f.Truncate(0)` on the lock and `os.Create`
// on the scratch copy both truncate and both walked past it. Every call that can empty a
// file is searched for, in every package of the binary (binaryPackages, boundary_test.go),
// and the ones the tool is allowed are carved out here BY FILE, with the reason -- each one
// a file THIS RUN makes, never a file the tool was given.
func TestNothingInThisToolRemovesAFile(t *testing.T) {
	t.Parallel()

	allowed := map[string][]string{
		// The platform with no flock: the lock is an exclusive create and its release
		// removes the sentinel this run made.
		"internal/tokens/lock_other.go": {"os.Remove"},
		// The fold's own lock file, whose whole body this run wrote.
		"internal/tokens/lock.go": {".Truncate("},
		// The copy under --scratch, made from the live database this run and read there;
		// the live file is never opened for writing.
		"internal/tokens/opencode.go": {"os.Create("},
		// The temporary file atomicfile writes through before rename; on error or
		// cleanup, atomicfile removes the temporary file this run created.
		"internal/atomicfile/atomicfile.go": {"os.Remove"},
	}
	used := map[string]bool{}
	for _, pkg := range binaryPackages(t) {
		for name, text := range pkgText(t, pkg) {
			path := pkg + "/" + name
			for _, call := range rule9Emptiers {
				if strings.Contains(text, call) {
					used[path+" "+call] = true
					assert.Contains(t, allowed[path], call, "%s calls %s; this tool deletes, truncates and trims nothing -- not a month file, not a log, not a stray (rule 9). A file THIS RUN makes is carved out by name in this test and in the spec, or it is a bug", path, call)
				}
			}
		}
	}
	for path, calls := range allowed {
		for _, call := range calls {
			assert.True(t, used[path+" "+call], "%s no longer calls %s; drop the carve-out rather than leaving the tripwire open on that file (rule 9)", path, call)
		}
	}
}

// The meta-tripwire. TestNothingInThisToolRemovesAFile is only as wide as its list, and
// the list is source in a test file: deleting `os.O_TRUNC` from it deletes the check with
// it and every test still passes. So the list is read back out of rule 9's own sentence in
// docs/SPEC-TOKENS.md and compared both ways -- a name the spec demands and the list lacks
// is a hole, a name the list carries and the spec does not is drift. Neither side can be
// edited alone.
func TestRule9EmptierListMatchesTheSpec(t *testing.T) {
	t.Parallel()

	spec := testkit.ReadFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-TOKENS.md"))
	_, rule9, ok := strings.Cut(spec, "\n9. **One file per day")
	require.True(t, ok, "docs/SPEC-TOKENS.md has no rule 9 opening `9. **One file per day`; this test was reading the wrong place and would have passed by checking nothing")
	rule9, _, ok = strings.Cut(rule9, "\n10. ")
	require.True(t, ok, "docs/SPEC-TOKENS.md rule 9 has no rule 10 after it; this test could not bound rule 9's text")
	// The clause that names them runs from the marker to the `--` that closes it; a name
	// inside a parenthetical is prose about a name (`os.OpenFile`, the call the flag
	// empties) and not a name of its own.
	const marker = "can empty a file --"
	_, clause, ok := strings.Cut(strings.Join(strings.Fields(rule9), " "), marker)
	require.True(t, ok, "rule 9 no longer says %q before naming the calls that can empty a file; this test finds the list by that clause and could not find it", marker)
	var want []string
	depth := 0
walk:
	for _, tok := range regexp.MustCompile("`[^`]*`|[()]|--").FindAllString(clause, -1) {
		switch {
		case tok == "(":
			depth++
		case tok == ")":
			depth = max(depth-1, 0)
		case depth > 0:
		case tok == "--":
			break walk
		default:
			want = append(want, strings.Trim(tok, "`"))
		}
	}
	require.GreaterOrEqual(t, len(want), 5, "read only %d names out of rule 9's clause (%v); the clause's shape changed and this test would have passed by checking almost nothing", len(want), want)
	assert.ElementsMatch(t, want, rule9Emptiers, "rule 9 of docs/SPEC-TOKENS.md (listA) and rule9Emptiers in cmd/nova-tokens/contract_test.go (listB) name different calls that can empty a file; change both together -- never one alone")
}

// Rule 5 and demanded test 4's source half: ONE attribution rule, in one function, and no
// reader carrying a regexp of its own. The prototype had two tables in two scripts and
// they disagreed about three repos.
func TestOnlyRepoGoCarriesTheAttributionRule(t *testing.T) {
	t.Parallel()

	text := pkgText(t, "internal/tokens")
	for name, body := range text {
		if name != "repo.go" && name != "bus.go" { // bus.go's regexps are the note GRAMMAR, not a repo table
			assert.False(t, strings.Contains(body, "regexp.MustCompile") || strings.Contains(body, "regexp.Compile"), "internal/tokens/%s compiles a regexp; the repo table is the caller's file and the rule is one function in repo.go", name)
		}
	}
	for _, reader := range []string{"claude.go", "opencode.go", "swarm.go", "bus.go"} {
		assert.Contains(t, text[reader], "rules.Attribute", "internal/tokens/%s does not reach a repo name through the attribution function", reader)
	}
}

// Rule 15's source half: no reader writes a literal zero for a type it did not read. Every
// DeepSeek row and every Claude reasoning cell in the prototype said zero when nothing had
// measured them.
func TestNoReaderWritesAZeroForATypeItDidNotRead(t *testing.T) {
	t.Parallel()

	for name, body := range pkgText(t, "internal/tokens") {
		assert.NotContains(t, body, "Set(t, 0)", "internal/tokens/%s writes a literal 0 into a type cell; a type the source did not report is a dash (rule 15)", name)
	}
}

// Demanded test 15's first clause: "a source test asserts no function adds one type column
// into another". Every DeepSeek row in the prototype that said zero and every cell that
// carried its neighbour's number came from exactly this. A write of one type may only read
// THAT type: `c.Set(Input, c.n[Output])` is the shape this forbids. A type column is the
// index of an array a type column lives in (a map keyed by anything else is not one), and a
// type expression is one of the five type constants, such an index, or the argument of a
// Get, compared as source text without a type checker.
func TestNoFunctionAddsOneTypeColumnIntoAnother(t *testing.T) {
	t.Parallel()

	typeNames := map[string]bool{"Input": true, "Output": true, "CacheWrite": true, "CacheRead": true, "Reasoning": true}
	checked := 0
	for _, pkg := range []string{"internal/tokens", "cmd/nova-tokens"} {
		for name, src := range pkgText(t, pkg) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, name, src, 0)
			require.NoError(t, err)
			text := func(e ast.Expr) string {
				var b strings.Builder
				require.NoError(t, printer.Fprint(&b, fset, e))
				return b.String()
			}
			isCounts := func(e ast.Expr) bool {
				s := text(e)
				return strings.Contains(s, "Counts") || strings.HasSuffix(s, ".n") || strings.HasSuffix(s, ".has") || strings.HasSuffix(s, "Totals") || strings.HasSuffix(s, "Dashes")
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				onCounts := fn.Recv != nil && len(fn.Recv.List) == 1 && strings.Contains(text(fn.Recv.List[0].Type), "Counts")
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					var into, from ast.Expr
					switch v := n.(type) {
					case *ast.CallExpr:
						if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Set" && len(v.Args) == 2 && (onCounts || isCounts(sel.X)) {
							into, from = v.Args[0], v.Args[1]
						}
					case *ast.AssignStmt:
						if ix, ok := v.Lhs[0].(*ast.IndexExpr); ok && len(v.Lhs) == 1 && len(v.Rhs) == 1 && isCounts(ix.X) {
							into, from = ix.Index, v.Rhs[0]
						}
					}
					if into == nil {
						return true
					}
					checked++
					want := text(into)
					ast.Inspect(from, func(m ast.Node) bool {
						var got ast.Expr
						switch v := m.(type) {
						case *ast.Ident:
							if typeNames[v.Name] {
								got = v
							}
						case *ast.IndexExpr:
							got = v.Index
						case *ast.CallExpr:
							if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Get" && len(v.Args) == 1 {
								got = v.Args[0]
							}
						}
						if got != nil {
							assert.Equal(t, want, text(got), "%s/%s:%d writes the %s column from %s; the five types are kept apart and no function adds one type column into another (rule 15)",
								pkg, name, fset.Position(n.Pos()).Line, want, text(got))
						}
						return true
					})
					return true
				})
			}
		}
	}
	require.GreaterOrEqual(t, checked, 5, "%d type-column writes examined; this tripwire was looking at the wrong shape and would have passed by checking nothing", checked)
}

// The refusals, one row each: exit 2, the sentence, and nothing written under --out.
//   - --max -1 is a typo with two readings, on every verb that lists (0 lists all).
//   - The records namespace is not a verb. SPEC-TOKENS' verb list has none that writes
//     anywhere but the day file; a verb that publishes changes this test and the spec in the
//     same hand. The banner does not advertise it either: a usage block naming a verb the
//     tool refuses is a first run that fails on its own instructions.
//   - Rule 5's refusal half at the binary: a malformed rules line is refused naming the line.
func TestTheRefusalsExitTwoAndWriteNothing(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.transcript("a.jsonl", msg("m", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}))
	bad := testkit.WriteFile(t, filepath.Join(b.dir, "bad-repos.tsv"), "schema\t(^|/)schema($|/)\nthis line has no tab\n")
	fold := func(more ...string) []string {
		return append([]string{"fold", "--out", b.out, "--day", "2026-09-11", "--claude", "g=" + b.tr}, more...)
	}
	maxSays := []string{"--max", "0 lists all"}
	records := []string{`unknown verb "records"; did you mean report?`, "run: nova-tokens help"}
	assert.NotContains(t, usage, "records", "the usage banner names a records verb this tool refuses")
	t.Cleanup(func() {
		ents, err := os.ReadDir(b.out)
		require.NoError(t, err)
		assert.Empty(t, ents, "a refused verb wrote under --out")
	})
	for _, c := range []struct {
		name      string
		args, err []string
	}{
		{"max -1 is refused by check", []string{"check", "--out", b.out, "--max", "-1"}, maxSays},
		{"max -1 is refused by sum", []string{"sum", "--out", b.out, "--month", "2026-09", "--max", "-1"}, maxSays},
		{"max -1 is refused by fold", fold("--repos", b.repos, "--max", "-1"), maxSays},
		{"records is not a verb", []string{"records"}, records},
		{"records collect is not a verb", []string{"records", "collect", "--sources", "x", "--ledger", b.out, "--out", b.dir}, records},
		{"records publish is not a verb", []string{"records", "publish", "--batch", b.dir, "--ledger", b.out, "--remote", "origin", "--branch", "main"}, records},
		{"a malformed rules file is refused by line", fold("--repos", bad), []string{"line 2", "TOKENS REFUSED"}},
		{"a missing rules file says what it wants", fold("--repos", filepath.Join(b.dir, "nope.tsv")), []string{"it wants a file of"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			novaTokens.Do(t, c.args...).Exit(2).Err(c.err...)
		})
	}
}

// One run over one fixture, one row each: the transcripts written under the bench's tr, the
// run (a fold of them as claude:g when none is named), its exit, the stdout and stderr lines
// holding each key and its wants ("!" before a want: the line must not hold it), what a
// stream must not say, and what the day file holds (noDay: there is none).
func TestWhatOneRunPrintsAndWrites(t *testing.T) {
	t.Parallel()

	const t0 = "2026-09-11T10:00:00Z"
	in := func(n int) map[string]int { return map[string]int{"input_tokens": n} }
	cwd := func(line string) string {
		return strings.Replace(line, `{"type":"assistant",`, `{"type":"assistant","cwd":"/x/schema",`, 1)
	}
	fable := msg("m1", t0, "fable", in(100), "/x/schema/a.go")
	for _, c := range []struct {
		name           string
		files          map[string][]string
		run            func(b bench) testkit.Ran
		exit           int
		out, err       map[string][]string
		notOut, notErr []string
		day, notDay    []string
		noDay          bool
	}{
		// A transcript whose every line carries "cwd":"/x/schema" and whose --repos matches it
		// was `repos=1 unknown=100.0%`, because the paths came only from tool_use blocks. A
		// conversational session, a pure Task fan-out, or any turn before the first tool call
		// was unknown for the whole file -- and `unknown` is a bucket `check` is happy with.
		{name: "cwd is the lowest rung of the attribution ladder: a line with no path",
			files: map[string][]string{"a.jsonl": {cwd(msg("m1", t0, "f", in(100)))}},
			out:   map[string][]string{"TOKENS DAY": {"unknown=0.0%"}}, day: []string{"f\tschema\t100\t"}},
		{name: "cwd is the lowest rung: a tool path wins over it",
			files: map[string][]string{"a.jsonl": {cwd(msg("m2", t0, "f", in(5), "/x/serialize/a.go"))}},
			day:   []string{"f\tserialize\t5\t"}},
		// THE WHOLE LADDER, in order, because cwd is a rung the spec's written rule does not
		// yet have (the PR body proposes the sentence): a tool path; then for a line with no
		// path, the PREVIOUS repo before cwd; a line whose paths matched no rule is `other` --
		// a bucket, decided -- and is not reopened by cwd.
		{name: "cwd is the lowest rung: the path, the previous repo, other, then cwd",
			files: map[string][]string{"a.jsonl": {
				cwd(msg("p1", t0, "f", in(1), "/x/serialize/a.go")),
				cwd(msg("p2", "2026-09-11T10:01:00Z", "f", in(2))),
				cwd(msg("p3", "2026-09-11T10:02:00Z", "f", in(4), "/elsewhere/a.go")),
			}},
			day: []string{"f\tserialize\t3\t", "f\tother\t4\t"}, notDay: []string{"f\tschema\t"}},
		// noid= was a number on a green TOKENS SOURCE line and reached nothing else -- not the
		// exit code, not TOKENS NOTE. 100% of a file's usage can be dropped that way under
		// TOKENS OK (lesson 95: a number is not a sentence).
		{name: "messages with no id reach the remedy line",
			files: map[string][]string{"a.jsonl": {msg("", t0, "f", in(9000), "/x/schema/a.go"), msg("m1", "2026-09-11T10:00:01Z", "f", in(1), "/x/schema/a.go")}},
			out:   map[string][]string{"TOKENS SOURCE": {"noid=1"}, "TOKENS NOTE": {"no id", "claude:g", "!nothing was wrong"}}},
		// Demanded test 3's other half: a line that is not JSON is counted inside this file's
		// unreadable accounting, and the file continues; a synthetic model is not a message
		// (the harness talking to itself); and the other transcript shape, .output, is read
		// the same way: 10+5+7.
		{name: "a non-JSON line is counted and the file continues",
			files: map[string][]string{
				"a.jsonl": {
					msg("m1", t0, "f", in(10), "/x/schema/a.go"),
					"this line is not JSON at all",
					msg("m2", "2026-09-11T10:01:00Z", "f", in(5), "/x/schema/a.go"),
					msg("m3", "2026-09-11T10:02:00Z", "<synthetic>", in(999), "/x/schema/a.go"),
				},
				"child.output": {msg("m4", "2026-09-11T10:03:00Z", "f", in(7), "/x/schema/a.go")},
			},
			exit: 1, err: map[string][]string{"badline=1": nil}, out: map[string][]string{"files=2 unreadable=1": nil, "messages=3": nil},
			day: []string{"f\tschema\t22\t"}},
		// Rule 14's tripwire: nothing under done/ or failed/ is opened. The answer has to be
		// the same before and after `reclaim`, which is the whole reason the usage file is a
		// sidecar; an unreadable file under done/ would be one TOKENS UNREADABLE line.
		{name: "nothing under done or failed is opened",
			run: func(b bench) testkit.Ran {
				pool := filepath.Join(b.dir, "pool")
				swarmUsage(b.t, pool, "j1", swarmRow("j1", "1", "-", "m", "schema", t0, "1", "2", "3", "4", "5"))
				makeUnreadable(b.t, testkit.WriteFile(b.t, filepath.Join(pool, "done", "j2", "usage.tsv"), "nothing here may be opened\n"))
				return b.fold("--swarm", "d="+pool)
			},
			out: map[string][]string{"nousage=1": nil}, notOut: []string{"UNREADABLE"}, notErr: []string{"UNREADABLE"}},
		// The spec declares a collapse nothing pinned: "It does not detect overlap between
		// sources." Measured 2026-09-11 on the real bench: ~/.claude/projects/<session>/
		// subagents/agent-*.jsonl and /private/tmp/.../tasks/*.output are the SAME messages,
		// and declaring both reported 2,932,982,350 cache_read against the correct
		// 1,502,293,166 -- written=true, check OK, sum OK, and nothing anywhere said the day had
		// been doubled. The numbers still double, because that is what the spec says this tool
		// does; the run now says so, naming the two labels and the count.
		{name: "two sources over one tree: one of them alone",
			files: map[string][]string{"a.jsonl": {fable}},
			out:   map[string][]string{"TOKENS NOTE": {"nothing was wrong"}}, day: []string{"fable\tschema\t100\t"}},
		{name: "two sources over one tree are named in the remedy",
			files: map[string][]string{"a.jsonl": {fable}},
			run: func(b bench) testkit.Ran {
				two := filepath.Dir(testkit.WriteFile(b.t, filepath.Join(b.dir, "two", "a.jsonl"), fable+"\n"))
				return b.fold("--claude", "bench="+b.tr, "--claude", "copy="+two)
			},
			out: map[string][]string{"TOKENS NOTE": {"claude:bench", "claude:copy", "1 message ids", "TWICE"}}, day: []string{"fable\tschema\t200\t"}},
		// Rule 3 on the verb that skipped it: "The fold continues over the rest and writes
		// what it could compute, and the run exits 1, because a declared source is a claim
		// that the report covers it." cmdReport counted the unreadable and then returned 0
		// whenever any line printed, so a friend pasted a partial day onto the bus under
		// REPORT OK. Exit 1 still prints the body it could compute.
		{name: "a report with one unreadable source exits one",
			files: map[string][]string{"a.jsonl": {msg("m1", t0, "f", in(3), "/x/schema/a.go")}},
			run: func(b bench) testkit.Ran {
				bad := filepath.Join(b.dir, "bad")
				makeUnreadable(b.t, testkit.WriteFile(b.t, filepath.Join(bad, "x.jsonl"), "{}\n"))
				return novaTokens.Do(b.t, "report", "--who", "emma", "--day", "2026-09-11", "--repos", b.repos, "--claude", "g="+b.tr, "--claude", "b="+bad)
			},
			exit: 1, err: map[string][]string{"TOKENS UNREADABLE": nil}, out: map[string][]string{"2026-09-11\temma\tf\tschema\tinput\t3": nil}},
		// Rule 6: "the successor is validated whole -- header, Date:, every body line --
		// before it replaces anything." A note with one unparsed body line was not dead,
		// entered the superseded map, and its predecessor's numbers vanished behind a
		// correction nobody could read whole. Two tips now, so the lane-day is a conflict and
		// nothing folds for it.
		{name: "a half-read successor does not replace its predecessor",
			run: func(b bench) testkit.Ran {
				bus := busDir(b.t, filepath.Join(b.dir, "bus"), "emma")
				first := busNote(b.t, bus, "emma", "a.md", "emma-00000000000a", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t100\n")
				busNote(b.t, bus, "emma", "b.md", "emma-00000000000b", "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b supersedes="+first, busDate,
					"2026-09-11\temma\tg\tschema\tinput\t250\nthis line is prose\n")
				return novaTokens.Do(b.t, "fold", "--out", b.out, "--all", "--repos", b.repos, "--bus", bus)
			},
			exit: 1, err: map[string][]string{"TOKENS UNPARSED": nil, "TOKENS CONFLICT label=bus:emma day=2026-09-11": nil}, notOut: []string{"TOKENS SUPERSEDED"}, noDay: true},
		// Help says a dash is "NEVER 0 ... a zero meaning 'not measured' would sum into a
		// month claiming to be complete", and SUM PAIR printed reasoning=0 for a month whose
		// every row had a dash there. At the far edge (rule 15), a month with no day files has
		// no source that reported any type: the running total was printed instead, `input=0
		// ... reasoning=0`, the one "not measured" zero rule 15 forbids, in the one place the
		// tool had no row to learn it from.
		{name: "sum prints a dash where no row reported the type",
			files: map[string][]string{"a.jsonl": {msg("m1", t0, "f", in(100), "/x/schema/a.go")}},
			run: func(b bench) testkit.Ran {
				b.fold("--claude", "g="+b.tr).Exit(0)
				return novaTokens.Do(b.t, "sum", "--out", b.out, "--month", "2026-09")
			},
			out: map[string][]string{"SUM PAIR": {"input=100", "reasoning=-", "dashes=0,1,1,1,1"}, "SUM TOTAL": {"reasoning=-"}}},
		{name: "a month with no day files sums to dashes not zeros",
			run: func(b bench) testkit.Ran { return novaTokens.Do(b.t, "sum", "--out", b.out, "--month", "2026-09") },
			out: map[string][]string{"SUM TOTAL": {"input=-", "output=-", "cache_write=-", "cache_read=-", "reasoning=-"}}},
		// Rule 3 and the provider reader's comment stripping: a `#` is a comment only where a
		// comment can be. The reader dropped every line whose first byte is `#` before the CSV
		// reader saw anything, so a quoted field carrying a newline whose continuation line
		// begins with `#` had that line deleted out of the middle of its own record -- the
		// record then parsed from the wrong bytes, with the `line=` map off by as many lines
		// as were removed. The quoted field is the model, the only column of this export that
		// can carry text, and dropping its `# 2.5` line writes a model name it never carried.
		{name: "a quoted field whose continuation starts with a hash is not stripped",
			run: func(b bench) testkit.Ran {
				export := testkit.WriteFile(b.t, filepath.Join(b.dir, "google.csv"), "timestamp,model,input_tokens\n2026-09-11T10:00:00Z,\"gemini\n# 2.5\npro\",100\n")
				return novaTokens.Do(b.t, "fold", "--out", b.out, "--all", "--repos", b.repos, "--provider", "google:emma="+export)
			},
			notErr: []string{"UNREADABLE", "UNPARSED"}, out: map[string][]string{"TOKENS SOURCE": {"rows=1"}}, day: []string{"unattributed\t100\t", "2.5"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			for name, lines := range c.files {
				b.transcript(name, lines...)
			}
			run := c.run
			if run == nil {
				run = func(b bench) testkit.Ran { return b.fold("--claude", "g="+b.tr) }
			}
			r := run(b).Exit(c.exit).NotOut(c.notOut...).NotErr(c.notErr...)
			linesHold(t, r.Stdout, c.out, r)
			linesHold(t, r.Stderr, c.err, r)
			if c.noDay {
				assert.NoFileExists(t, filepath.Join(b.out, "2026-09-11.tsv"), r)
			}
			if len(c.day)+len(c.notDay) > 0 {
				day := b.day()
				for _, w := range c.day {
					assert.Contains(t, day, w, r)
				}
				for _, w := range c.notDay {
					assert.NotContains(t, day, w, r)
				}
			}
		})
	}
}

// The file-count half of rule 11's bound (bounded_test.go): the fold is one pass over each
// declared file. Serial: tokens.Opens() is process-wide, and every parallel fold adds to it.
func TestEachDeclaredFileIsOpenedOncePerRun(t *testing.T) {
	b := newBench(t)
	for i := range 12 {
		id := string(rune('a' + i))
		b.transcript(id+".jsonl", msg("m"+id, "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
	}
	before := tokens.Opens()
	b.fold("--claude", "g="+b.tr).Exit(0)
	assert.Equal(t, int64(12), tokens.Opens()-before, "source opens for 12 files; the fold is one pass over each declared file")
}
