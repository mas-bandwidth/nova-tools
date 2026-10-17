package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEntries reads a START value: entries split on commas outside parentheses, each its
// first word (backticks trimmed, cleaned as a PATHS entry) and, in parentheses after it,
// what the card does there; `(read)` in any case marks a file only read, anything else in
// the parentheses does not; an unbalanced close never swallows the rest.
func TestStartEntriesSplitOnCommasOutsideParentheses(t *testing.T) {
	t.Parallel()
	assert.Nil(t, startEntries(""))
	assert.Nil(t, startEntries(" , ,"))
	assert.Equal(t, []startEntry{{"a.go", false}, {"b.go", true}, {"pkg/x", false}},
		startEntries("a.go, b.go (read), pkg/x (change the verb, then test)"))
	assert.Equal(t, []startEntry{{"a.go", true}}, startEntries("`a.go` (Read)"))
	assert.Equal(t, []startEntry{{"a", false}}, startEntries("./a/"))
	assert.Equal(t, []startEntry{{"a.go", false}}, startEntries("a.go (read only)"))
	assert.Equal(t, []startEntry{{"a.go", false}}, startEntries("a.go (READ, then write)"))
	assert.Equal(t, []startEntry{{"a.go)", false}, {"b.go", false}}, startEntries("a.go), b.go"))
	assert.Equal(t, []startEntry{{"a.go", true}, {"b.go", false}}, startEntries("a.go (read) , b.go"))
	assert.Equal(t, []startEntry{{"a.go", false}}, startEntries("a.go the first file"), "the first word is the path")
}

// taskPaths is every path-shaped word of the task text not marked `(read)` after it, its
// trailing stop cut and cleaned; a bare word is no path.
func TestTaskPathsSkipsReadMarkedAndBareWords(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"pkg/x/a.go", "cmd/y/main.go", "a/b.go", "docs/C.md"},
		taskPaths("Change pkg/x/a.go and read docs/SPEC.md (read). Also cmd/y/main.go. And ./a/b.go, `docs/B.md` (read), \"docs/C.md\"."))
	assert.Nil(t, taskPaths("run nova-sprint add with the --flag"))
	assert.Nil(t, taskPaths(""))
	assert.Equal(t, []string{"a/b"}, taskPaths("a/b (reads it)"), "only the exact marker exempts a path")
	assert.Nil(t, taskPaths("a/b.(read)"), "a stop between the path and the marker still marks it")
}

// taskParagraph is THE TASK paragraph: from its line (1-based) to the next paragraph head
// (STEP n, or CAPITAL WORDS then a stop, colon or parenthesis); "" when the brief has none.
func TestTaskParagraphEndsAtTheNextHead(t *testing.T) {
	t.Parallel()
	line, task := taskParagraph([]byte("card\nPATHS: a.go\n\nTHE TASK. Do x.\nmore here\n\nSTEP 1. y\nnever\n"))
	assert.Equal(t, 4, line)
	assert.Equal(t, " Do x.\nmore here\n", task)
	_, task = taskParagraph([]byte("THE TASK: Do x.\nlower case line\nRULES. no\n"))
	assert.Equal(t, ": Do x.\nlower case line", task)
	_, task = taskParagraph([]byte("THE TASK Do x.\nCOORDINATOR NOTE (x): y\n"))
	assert.Equal(t, " Do x.", task)
	_, task = taskParagraph([]byte("THE TASK. Do x.\nA line Of prose.\nAB. two caps is no head\n"))
	assert.Equal(t, " Do x.\nA line Of prose.\nAB. two caps is no head\n", task)
	line, task = taskParagraph([]byte("card\nno task here\n"))
	assert.Equal(t, 0, line)
	assert.Equal(t, "", task)
	assert.Equal(t, " Do x.", strings.TrimRight(taskText(t, "THE TASK. Do x."), "\n"))
}

func taskText(t *testing.T, brief string) string {
	t.Helper()
	_, task := taskParagraph([]byte(brief))
	return task
}

// NamesAFinishPR: a PR: header line, or THE TASK saying the work starts from the PR's
// branch; a PR: line below the header block is not the header's.
func TestNamesAFinishPR(t *testing.T) {
	t.Parallel()
	assert.True(t, NamesAFinishPR([]byte("card\nPR: owner/repo#1\nPATHS: a.go\n")))
	assert.True(t, NamesAFinishPR([]byte("card\nPATHS: a.go\n\nTHE TASK. Start from the PR's branch and finish it.\n")))
	assert.False(t, NamesAFinishPR([]byte("card\nPATHS: a.go\n\nTHE TASK. Open a PR.\n")))
	assert.False(t, NamesAFinishPR([]byte("card\nPATHS: a.go\n\nprose\nPR: owner/repo#1\n")))
}

// coversDir says scope reaches into a directory: the directory itself, one above it, or a
// file or glob inside it; a sibling with the same prefix letters does not.
func TestCoversDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scope []string
		dir   string
		want  bool
	}{
		{[]string{"pkg/x/*.go"}, "pkg/x", true},
		{[]string{"pkg"}, "pkg/x", true},
		{[]string{"pkg/x/y/a.go"}, "pkg/x", true},
		{[]string{"./pkg/x/"}, "pkg/x", true},
		{[]string{"pkg/x"}, "pkg/x", true},
		{[]string{"pkg/**"}, "pkg/x", true},
		{[]string{"pkg/xy"}, "pkg/x", false},
		{[]string{"pkg/x"}, "pkg/xy", false},
		{[]string{"docs/*.md"}, "pkg/x", false},
		{nil, "pkg/x", false},
	} {
		assert.Equal(t, tc.want, coversDir(tc.scope, tc.dir), "%v %s", tc.scope, tc.dir)
	}
}

// coversTests says scope covers a _test.go file of the package in dir ("" the root): the
// package's tests by glob or directory, or one test file of that directory named.
func TestCoversTests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scope []string
		dir   string
		want  bool
	}{
		{[]string{"pkg/x/*_test.go"}, "pkg/x", true},
		{[]string{"pkg/x/foo_test.go"}, "pkg/x", true},
		{[]string{"pkg/x/*.go"}, "pkg/x", true},
		{[]string{"pkg/x"}, "pkg/x", true},
		{[]string{"pkg/x/"}, "pkg/x", true},
		{[]string{"pkg/**"}, "pkg/x", true},
		{[]string{"pkg/y/foo_test.go"}, "pkg/x", false},
		{[]string{"pkg/x/y/foo_test.go"}, "pkg/x", false},
		{[]string{"pkg/x/foo.go"}, "pkg/x", false},
		{[]string{"a_test.go"}, "", true},
		{[]string{"*_test.go"}, "", true},
		{[]string{"pkg/x/a_test.go"}, "", false},
		{nil, "pkg/x", false},
	} {
		assert.Equal(t, tc.want, coversTests(tc.scope, tc.dir), "%v %q", tc.scope, tc.dir)
	}
}

// codePackages is every directory whose non-test Go code scope covers: with a tree, the
// files it names plus the literal NEW files; with no tree, read off the entries alone,
// where a `dir/*.go` glob names its directory.
func TestCodePackagesWithAndWithoutATree(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"pkg/a", "pkg/b"}, codePackages(nil, []string{"pkg/a/x.go", "pkg/a/y_test.go", "pkg/b/*.go", "docs/x.md", "./pkg/a/z.go"}, nil))
	assert.Nil(t, codePackages(nil, []string{"pkg/a/y_test.go", "docs/x.md", "pkg/c"}, []string{"pkg/d/n.go"}), "with no tree NEW is not read")
	files := []string{"pkg/a/x.go", "pkg/a/x_test.go", "pkg/c/z.go", "docs/x.md", "pkg/a/sub/s.go"}
	assert.Equal(t, []string{"pkg/a", "pkg/d"}, codePackages(files, []string{"pkg/a/*.go"}, []string{"pkg/d/n.go", "pkg/e/*.go", "pkg/f/n_test.go"}))
	assert.Equal(t, []string{"pkg/a", "pkg/c", "pkg/a/sub"}, codePackages(files, []string{"pkg/"}, nil), "in the tree's order")
	assert.Nil(t, codePackages(files, []string{"docs/"}, nil))
	assert.Nil(t, codePackages(files, nil, nil))
}

// verbTools is each cmd/<tool> whose Go code scope covers, when the task names a verb or a
// flag; none when it names neither, a glob for the tool name is skipped, a test file is
// not code.
func TestVerbToolsNeedsATaskThatNamesAVerbOrFlag(t *testing.T) {
	t.Parallel()
	scope := []string{"cmd/nova-x/main.go", "cmd/nova-y/*.go", "cmd/nova-z/a_test.go", "cmd/nova-w", "pkg/x/a.go", "cmd/*/main.go", "cmd/nova-v/sub/x.go", "cmd/nova-u/", "cmd/nova-t/**", "cmd/nova-s/README.md", "cmd/nova-x/other.go"}
	assert.Equal(t, []string{"nova-x", "nova-y", "nova-w", "nova-v", "nova-u", "nova-t"}, verbTools(scope, "add a --force flag"))
	assert.Equal(t, []string{"nova-x", "nova-y", "nova-w", "nova-v", "nova-u", "nova-t"}, verbTools(scope, "the verb changes"))
	assert.Equal(t, []string{"nova-x", "nova-y", "nova-w", "nova-v", "nova-u", "nova-t"}, verbTools(scope, "pass `--open`"))
	assert.Nil(t, verbTools(scope, "fix the bug in x--y"), "a -- inside a word names no flag")
	assert.Nil(t, verbTools(scope, "fix the bug"))
	assert.Nil(t, verbTools(scope, ""))
	assert.Nil(t, verbTools([]string{"pkg/x/a.go", "cmd/nova-z/a_test.go", "cmd/*/main.go"}, "the --flag"))
	assert.Nil(t, verbTools(nil, "the --flag"))
}

// toolSpec is docs/SPEC-<TOOL>.md without the nova- prefix, else with it, "" when the tree
// holds neither; with no tree the first spelling.
func TestToolSpec(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "docs/SPEC-X.md", toolSpec("nova-x", nil, false))
	assert.Equal(t, "docs/SPEC-FOO.md", toolSpec("foo", nil, false))
	assert.Equal(t, "docs/SPEC-X.md", toolSpec("nova-x", []string{"docs/SPEC-X.md", "docs/SPEC-NOVA-X.md"}, true))
	assert.Equal(t, "docs/SPEC-NOVA-X.md", toolSpec("nova-x", []string{"docs/SPEC-NOVA-X.md"}, true))
	assert.Equal(t, "", toolSpec("nova-x", []string{"docs/SPEC-Y.md"}, true))
	assert.Equal(t, "", toolSpec("nova-x", nil, true))
}

// liveBase is sprint/<name>, the trunk (dev, main, master) or a base the store lists;
// temporaryBase is tmp/, temp/, wip/, scratch/ in any case, or a name ending -tmp or -wip.
func TestLiveAndTemporaryBases(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"sprint/a", "sprint/", "dev", "main", "master"} {
		assert.True(t, liveBase(ref, nil), ref)
	}
	for _, ref := range []string{"", "feature/x", "sprint", "Dev", "trunk", "tmp/x"} {
		assert.False(t, liveBase(ref, nil), ref)
	}
	assert.True(t, liveBase("feature/x", []string{"a", "feature/x"}))
	assert.False(t, liveBase("feature/y", []string{"a", "feature/x"}))
	for _, ref := range []string{"tmp/x", "Temp/x", "WIP/x", "scratch/x", "feat-tmp", "x-WIP", "tmp", "wip"} {
		assert.True(t, temporaryBase(ref), ref)
	}
	for _, ref := range []string{"", "sprint/tmp", "tmpx/y", "x-tmp-y", "wipe/x", "scratchy", "main"} {
		assert.False(t, temporaryBase(ref), ref)
	}
}

// heavyLine is line 1 with its tier heavy: the tier word replaced where there is one, an
// empty tier filled, and appended where line 1 names none.
func TestHeavyLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "card x tier: heavy", heavyLine("card x tier: pro"))
	assert.Equal(t, "card x tier: heavy", heavyLine("card x tier:"))
	assert.Equal(t, "card x tier: heavy", heavyLine("card x tier:   "))
	assert.Equal(t, "card x tier: heavy", heavyLine("card x"))
	assert.Equal(t, "card x tier: heavy", heavyLine("card x   "))
	assert.Equal(t, "card tier: heavy more", heavyLine("card tier: flash more"))
	assert.Equal(t, " tier: heavy", heavyLine(""))
	assert.Equal(t, "frontier tier: heavy", heavyLine("frontier"), "the word alone is no tier")
}

// goPkgArg is ./-relative; orElse, uniq and notIn are the list helpers the fix lines use.
func TestListHelpers(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "./a", goPkgArg("./a"))
	assert.Equal(t, ".", goPkgArg("."))
	assert.Equal(t, "./a/b", goPkgArg("a/b"))
	assert.Equal(t, "./", goPkgArg(""))
	assert.Equal(t, "x", orElse("x", "y"))
	assert.Equal(t, "y", orElse("", "y"))
	assert.Equal(t, []string{"a", "b"}, uniq([]string{"a", "b", "a", "b", "a"}))
	assert.Nil(t, uniq(nil))
	assert.Equal(t, []string{"b"}, notIn([]string{"a", "b"}, []string{"./a/"}))
	assert.Equal(t, []string{"a", "b"}, notIn([]string{"a", "b"}, nil))
	assert.Nil(t, notIn(nil, []string{"a"}))
	assert.Nil(t, notIn([]string{"a"}, []string{"a", "a"}))
	assert.Equal(t, []string{"a", "b", "c"}, headerList("a, b c,none,-,,"))
	assert.Nil(t, headerList(""))
	assert.Nil(t, headerList("none"))
	assert.Equal(t, "a/b", cleanEntry("./a/b/"))
	assert.Equal(t, ".", cleanEntry("./"), "the slash goes first, so ./ is .")
}

// answeredByNew says a NEW entry names the PATHS entry: the same path cleaned, or one glob
// matching the other.
func TestAnsweredByNew(t *testing.T) {
	t.Parallel()
	assert.True(t, answeredByNew("a/b.go", []string{"a/*.go"}))
	assert.True(t, answeredByNew("a/*.go", []string{"a/b.go"}))
	assert.True(t, answeredByNew("a/b.go", []string{"c.go", "./a/b.go"}))
	assert.True(t, answeredByNew("a/b/c.go", []string{"a/**"}))
	assert.False(t, answeredByNew("a/b.go", []string{"c.go"}))
	assert.False(t, answeredByNew("a/b.go", nil))
	assert.False(t, answeredByNew("a/b.go", []string{"a"}), "a directory above is no NEW answer")
}

// entriesMissing is every entry that names nothing in the tree: a file, a directory above
// one, or a glob matching one; a literal new test file is never missing.
func TestEntriesMissing(t *testing.T) {
	t.Parallel()
	files := []string{"a/b.go", "a/c.go", "d/e.go", "a/sub/f.go"}
	assert.Equal(t, []string{"z.go", "q/*.go", "a/b", "d/..."},
		entriesMissing(files, []string{"a/b.go", "a", "a/*.go", "./a/sub/", "z.go", "new_test.go", "a/test_x.py", "q/*.go", "a/b", "a/b.go/", "a/**", "d/..."}),
		"a trailing slash is cleaned off a file; the tree spelling `...` is not read here, only by Covers")
	assert.Nil(t, entriesMissing(files, nil))
	assert.Equal(t, []string{"a"}, entriesMissing(nil, []string{"a", "x_test.go"}))
	assert.Equal(t, []string{"*_test.go/x"}, entriesMissing(files, []string{"*_test.go/x"}), "a glob is never a new test file")
}

// classTestPackage is internal/ci or a package under it, in any spelling a TEST line
// uses; ledgerClassTest is that with KIND: ledger, the kind in any case.
func TestLedgerClassTest(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"internal/ci", "./internal/ci", "./internal/ci/", "internal/ci/sub", " internal/ci "} {
		assert.True(t, classTestPackage(p), "%q", p)
		assert.True(t, ledgerClassTest("ledger", p), "%q", p)
		assert.True(t, ledgerClassTest(" Ledger ", p), "%q", p)
		assert.False(t, ledgerClassTest("fix", p), "%q", p)
		assert.False(t, ledgerClassTest("", p), "%q", p)
	}
	for _, p := range []string{"", "internal/cix", "internal", "pkg/x", "internal/ci-x", "./internal"} {
		assert.False(t, classTestPackage(p), "%q", p)
		assert.False(t, ledgerClassTest("ledger", p), "%q", p)
	}
	assert.Equal(t, "ledger", LedgerKind)
}

// BriefRemedy answers from the brief table first, then the card base table, and "" for a
// check neither knows; every brief remedy names what to write.
func TestBriefRemedy(t *testing.T) {
	t.Parallel()
	for check, want := range BriefBaseRemedies {
		assert.Equal(t, want, BriefRemedy(check), check)
		assert.NotEmpty(t, want, check)
	}
	assert.NotEmpty(t, BriefRemedy("paths-at-base"), "answered from CardBaseRemedies")
	assert.Equal(t, CardBaseRemedies["paths-at-base"], BriefRemedy("paths-at-base"))
	assert.Equal(t, "", BriefRemedy("no-such-check"))
	assert.Contains(t, BriefRemedy("paths-read-marker"), PathsReadRemedy)
}

// The new-model-is-heavy-or-above rule: a card whose NEW names a new .tla module
// writes a model, and is tier heavy or above, so heavy and frontier lint clean
// and pro and flash are refused naming the rule; a card that only edits models,
// .cfg files or the ledgers under tla/ is any tier (the owner, 2026-10-17: "I
// think models at heavy is fine, and touching models can be pro now").
func TestATLACardIsHeavyOrAbove(t *testing.T) {
	t.Parallel()
	lint := func(line1, header string) []CardHeaderFinding {
		// the model gate (model-gate-named) is named, so only the tier rule speaks
		fs, _ := LintBrief([]byte(line1+"\n"+header+"\nGATE-NEEDS: linux, tla\n\nTHE TASK. the model\n\nSTEP 0. Run the model: go run ./tools/tlacheck groups --root . --stale.\n"), BriefBase{})
		return fs
	}
	newModel := "PATHS: tla/Bar.tla,tla/Bar.cfg\nNEW: tla/Bar.tla,tla/Bar.cfg"
	assert.Empty(t, lint("card x tier: heavy", newModel), "heavy card with NEW tla/Bar.tla passes: heavy is the rule's floor")
	assert.Empty(t, lint("card x tier: frontier", newModel), "frontier is above heavy and lints clean")
	assert.Empty(t, lint("card x tier: pro", "PATHS: tla/Foo.tla"), "pro card editing tla/Foo.tla passes")
	assert.Empty(t, lint("card x tier: pro", "PATHS: tla/Foo.tla,tla/Foo.cfg,tla/COVERAGE.tsv,tla/RUNS.tsv\nNEW: tla/Foo2.cfg"), "a new .cfg for an existing model is no new model")
	for _, below := range []string{"pro", "flash"} {
		fs := lint("card x tier: "+below, newModel)
		require.Equal(t, []string{"new-model-is-heavy-or-above"}, checksOf(fs), "%s card with NEW tla/Bar.tla is refused", below)
		assert.Contains(t, fs[0].Excerpt, "not heavy or frontier", below)
		assert.Contains(t, fs[0].Excerpt, "tla/Bar.tla", below)
		assert.Contains(t, BriefRemedy("new-model-is-heavy-or-above"), "heavy", below)
	}
	for _, old := range []string{"tla-is-heavy-or-above", "tla-is-frontier"} {
		assert.Equal(t, BriefRemedy("new-model-is-heavy-or-above"), BriefRemedy(old), "the old token %s is an alias for one release", old)
	}
}

// LintBrief without a tree: the checks that need none still hold the brief (pr-carry,
// tier-set, tla-is-heavy-or-above, who-serves-tier, base-is-live, paths-cover-named off START,
// paths-cover-test, donewhen-test-name's parse), the ones that need it draw MISSING when a
// base was named and not handed over, and nothing when the brief names no base; the fix
// lines answer the PATHS, NEW and SHARED findings and line 1.
func TestLintBriefWithoutATree(t *testing.T) {
	t.Parallel()
	brief := func(line1, header, task string) []byte {
		return []byte(line1 + "\n" + header + "\n\nTHE TASK. " + task + "\n")
	}
	checks := func(fs []CardHeaderFinding) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Check)
		}
		return out
	}

	t.Run("no PATHS line is no card brief", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card tier: -", "KIND: fix", "x"), BriefBase{})
		assert.Empty(t, fs, "not even tier-set")
		assert.Nil(t, fix)
	})

	t.Run("no base named: the tree checks do not run and draw no MISSING", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card tier: pro", "PATHS: pkg/a/x.go\nTEST: pkg/a TestX\nSTART: pkg/a/x.go, docs/D.md (read), pkg/b, cmd/t/main.go", "x"), BriefBase{})
		require.Equal(t, []string{"paths-cover-named", "paths-cover-test"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, `START names "pkg/b/*.go", "cmd/t/main.go" to change`)
		assert.Contains(t, fs[1].Excerpt, "TEST TestX is in pkg/a, and PATHS covers none of its _test files")
		assert.Equal(t, BriefFix{"PATHS: pkg/a/x.go,pkg/b/*.go,cmd/t/main.go,pkg/a/*_test.go"}, fix)
		fs, _ = LintBrief(brief("card tier: pro", "PATHS: pkg/a/*.go\nTEST: pkg/a TestX", "x"), BriefBase{})
		assert.Empty(t, fs, "a *.go glob covers the package's _test files")
	})

	t.Run("a base named and not handed over draws MISSING on each tree check", func(t *testing.T) {
		t.Parallel()
		fs, _ := LintBrief(brief("card tier: pro", "REPO: o/r\nBASE: sprint/s\nPATHS: pkg/a/*.go\nTEST: pkg/a TestX", "x"), BriefBase{Missing: "the clone is cold"})
		assert.Equal(t, []string{"paths-at-base", "donewhen-test-name", "paths-cover-named", "paths-cover-testdata", "paths-cover-ledgers", "base-is-live"}, checks(fs))
		for _, f := range fs {
			assert.Contains(t, f.Excerpt, "MISSING: the clone is cold", f.Check)
		}
	})

	t.Run("a base named with no repository handed over says so", func(t *testing.T) {
		t.Parallel()
		fs, _ := LintBrief(brief("card tier: pro", "REPO: o/r\nBASE: sprint/s\nPATHS: docs/x.md", "x"), BriefBase{})
		require.Len(t, fs, 2)
		assert.Equal(t, "paths-at-base", fs[0].Check)
		assert.Contains(t, fs[0].Excerpt, "MISSING: no repository at the BASE tip was handed over, so the brief was not read against sprint/s")
		assert.Equal(t, "paths-cover-named", fs[1].Check)
	})

	t.Run("tier-set and new-model-is-heavy-or-above fix line 1", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card x tier: -", "PATHS: tla/Model.tla\nNEW: tla/Model.tla\nGATE-NEEDS: linux, tla", "x\n\nSTEP 0. Run the model: go run ./tools/tlacheck groups --root . --stale."), BriefBase{})
		assert.Equal(t, []string{"tier-set", "new-model-is-heavy-or-above"}, checks(fs))
		assert.Equal(t, 1, fs[0].Line)
		assert.Contains(t, fs[1].Excerpt, "line 1 names tier -, not heavy or frontier")
		assert.Equal(t, BriefFix{"card x tier: heavy"}, fix)
		fs, fix = LintBrief(brief("card x", "PATHS: pkg/a/*.go,tla/\nNEW: tla/*.tla\nGATE-NEEDS: linux, tla", "x\n\nSTEP 0. Run the model: go run ./tools/tlacheck groups --root . --stale."), BriefBase{})
		assert.Equal(t, []string{"tier-set", "new-model-is-heavy-or-above"}, checks(fs))
		assert.Equal(t, BriefFix{"card x tier: heavy"}, fix)
		fs, fix = LintBrief(brief("card x tier: frontier", "PATHS: tla/\nNEW: tla/Bar.tla\nGATE-NEEDS: linux, tla", "x\n\nSTEP 0. Run the model: go run ./tools/tlacheck groups --root . --stale."), BriefBase{})
		assert.Empty(t, fs)
		assert.Nil(t, fix)
		fs, _ = LintBrief(brief("card x tier: heavy", "PATHS: tla/\nNEW: tla/Bar.tla\nGATE-NEEDS: linux, tla", "x\n\nSTEP 0. Run the model: go run ./tools/tlacheck groups --root . --stale."), BriefBase{})
		assert.Empty(t, fs, "heavy is the rule's floor")
		fs, _ = LintBrief(brief("card x tier: pro", "PATHS: tla/", "x"), BriefBase{})
		assert.Empty(t, fs, "editing the models in tla/ is any tier")
		fs, _ = LintBrief(brief("card x tier: pro", "PATHS: pkg/tla/x.go", "x"), BriefBase{})
		assert.Empty(t, fs, "tla inside another path is not the model")
	})

	t.Run("who-serves-tier", func(t *testing.T) {
		t.Parallel()
		fs, _ := LintBrief(brief("card tier: pro", "PATHS: docs/x.md\nWHO: friend w1", "x"), BriefBase{})
		assert.Equal(t, []string{"who-serves-tier"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "MISSING: the friends table was not read")
		friends := map[string][]string{"w1": {"flash", "pro"}, "w2": {"frontier"}}
		fs, _ = LintBrief(brief("card tier: pro", "PATHS: docs/x.md\nWHO: friend w1", "x"), BriefBase{Friends: friends})
		assert.Empty(t, fs)
		fs, _ = LintBrief(brief("card tier: heavy", "PATHS: docs/x.md\nWHO: only friend w1", "x"), BriefBase{Friends: friends})
		assert.Equal(t, []string{"who-serves-tier"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "WHO names friend w1, who serves flash,pro, and the card is tier heavy")
		fs, _ = LintBrief(brief("card tier: heavy", "PATHS: docs/x.md\nWHO: friend w9", "x"), BriefBase{Friends: friends})
		assert.Empty(t, fs, "a friend the table does not know is not held to it")
		fs, _ = LintBrief(brief("card tier: heavy", "PATHS: docs/x.md\nWHO: friend", "x"), BriefBase{Friends: friends})
		assert.Equal(t, []string{"who-serves-tier"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "no friend of the table serves tier heavy")
		fs, _ = LintBrief(brief("card tier: frontier", "PATHS: docs/x.md\nWHO: friend", "x"), BriefBase{Friends: friends})
		assert.Empty(t, fs)
		fs, _ = LintBrief(brief("card tier: heavy", "PATHS: docs/x.md\nWHO: fleet", "x"), BriefBase{Friends: friends})
		assert.Empty(t, fs, "an unreadable WHO is another check's")
		fs, _ = LintBrief(brief("card tier: -", "PATHS: docs/x.md\nWHO: friend", "x"), BriefBase{Friends: friends})
		assert.Equal(t, []string{"tier-set"}, checks(fs), "no tier: nothing to serve")
	})

	t.Run("base-is-live", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			base string
			bb   BriefBase
			want string
		}{
			{"sprint/s", BriefBase{Gone: true}, "BASE sprint/s is no branch of origin o/r: deleted, or never pushed"},
			{"sprint/s", BriefBase{Missing: "cold"}, "MISSING: cold"},
			{"tmp/s", BriefBase{Gone: true}, "is no branch of origin"},
			{"tmp/s", BriefBase{}, "BASE tmp/s is a temporary branch; a card is cut on a sprint base"},
			{"feature/s", BriefBase{}, "BASE feature/s is no sprint base: not sprint/<name>, not the trunk, and not a base the sprint's cards use (none)"},
			{"feature/s", BriefBase{Listed: []string{"a", "b"}}, "not a base the sprint's cards use (a, b)"},
			{"feature/s", BriefBase{Listed: []string{"feature/s"}}, ""},
			{"dev", BriefBase{}, ""},
			{"sprint/s", BriefBase{}, ""},
		} {
			fs, _ := LintBrief(brief("card tier: pro", "REPO: o/r\nBASE: "+tc.base+"\nPATHS: docs/x.md", "x"), tc.bb)
			got := findingsFor(fs, "base-is-live")
			if tc.want == "" {
				assert.Empty(t, got, tc.base)
				continue
			}
			require.Len(t, got, 1, tc.base)
			assert.Equal(t, 3, got[0].Line)
			assert.Contains(t, got[0].Excerpt, tc.want, tc.base)
		}
		fs, _ := LintBrief(brief("card tier: pro", "BASE: tmp/s\nPATHS: docs/x.md", "x"), BriefBase{})
		assert.Empty(t, findingsFor(fs, "base-is-live"), "no REPO: no base to hold")
	})

	t.Run("the TEST line is parsed even with no tree", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card tier: pro", "PATHS: pkg/a/*.go\nTEST: pkg/a testX", "x"), BriefBase{})
		assert.Equal(t, []string{"donewhen-test-name"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "is not `[-tags <tags>] <package> <TestName>`")
		assert.Nil(t, fix, "a refused TEST line asks for no _test coverage")
		fs, _ = LintBrief(brief("card tier: pro", "PATHS: pkg/a/*.go\nTEST: none docs only", "x"), BriefBase{})
		assert.Empty(t, fs)
		fs, fix = LintBrief(brief("card tier: pro", "PATHS: pkg/a/*.go\nTEST: . TestRoot", "x"), BriefBase{})
		assert.Equal(t, []string{"paths-cover-test"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "TEST TestRoot is in ., and PATHS covers none")
		assert.Equal(t, BriefFix{"PATHS: pkg/a/*.go,*_test.go"}, fix)
	})

	t.Run("pr-carry holds a brief with no PATHS too", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card tier: pro", "PR: o/r#1", "finish it"), BriefBase{})
		assert.Equal(t, []string{"pr-carry"}, checks(fs))
		assert.Equal(t, 2, fs[0].Line)
		assert.Nil(t, fix)
		fs, _ = LintBrief(brief("card tier: pro", "PR: o/r#1\nCARRY: head="+strings.Repeat("ab", 20)+" branch=pr-b\nPATHS: docs/x.md", "finish it"), BriefBase{})
		assert.Empty(t, findingsFor(fs, "pr-carry"))
	})

	t.Run("paths-cover-docs from the entries alone", func(t *testing.T) {
		t.Parallel()
		fs, fix := LintBrief(brief("card tier: pro", "PATHS: cmd/nova-q/*.go\nSHARED: docs/CLI.md", "add a --flag to the verb"), BriefBase{})
		assert.Equal(t, []string{"paths-cover-docs"}, checks(fs))
		assert.Contains(t, fs[0].Excerpt, "the card changes a verb or flag of nova-q, and")
		assert.Equal(t, BriefFix{"PATHS: cmd/nova-q/*.go,docs/CLI.md,docs/SPEC-Q.md", "SHARED: docs/CLI.md,docs/SPEC-Q.md"}, fix)
		fs, fix = LintBrief(brief("card tier: pro", "PATHS: cmd/nova-q/*.go,docs/CLI.md,docs/SPEC-Q.md\nSHARED: docs/CLI.md,docs/SPEC-Q.md", "add a --flag"), BriefBase{})
		assert.Empty(t, fs)
		assert.Nil(t, fix)
	})

	t.Run("fix lines never repeat an entry", func(t *testing.T) {
		t.Parallel()
		_, fix := LintBrief(brief("card tier: pro", "PATHS: pkg/a/*.go\nNEW: none\nSTART: pkg/b/x.go, pkg/b/x.go, pkg/b/y.go", "x"), BriefBase{})
		assert.Equal(t, BriefFix{"PATHS: pkg/a/*.go,pkg/b/x.go,pkg/b/y.go"}, fix)
	})
}
