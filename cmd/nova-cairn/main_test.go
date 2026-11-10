package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// do runs nova-cairn in process and returns its exit code, stdout and stderr.
func do(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// assertRefusal holds a refusal to one line: nova-cairn <verb>: <why>; run: <next>,
// where next is a nova-cairn command or a command that looks at the file at fault.
func assertRefusal(t *testing.T, errs string, args []string) {
	t.Helper()
	assert.Regexp(t, `^nova-cairn[a-z -]*: .+; run: (nova-cairn|ls|sed|grep|cat) .+\n$`, errs, "%v", args)
	assert.Equal(t, 1, strings.Count(errs, "\n"), "one line: %v", args)
}

// TestSelfContained holds nova-cairn to the standard library, so it can be lifted out whole.
func TestSelfContained(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, name := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			ok := !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") ||
				(strings.HasSuffix(name, "_test.go") && strings.HasPrefix(path, "github.com/stretchr/testify"))
			assert.True(t, ok, "%s imports %s: nova-cairn imports only the standard library", name, path)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	code, out, _ := do(t, "", "open", "--store", store, "--session", "s1", "--source", "src", "--publish", "manual")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "\nnext: nova-cairn append --store "+store+" --session s1 --entry first ")

	code, out, _ = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "APPEND OK session=s1 entry=e1 source=src persisted=true published=false publish=manual duplicate=false")

	code, out, _ = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "duplicate=true")

	code, _, errs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "other words")
	assert.Equal(t, 1, code, "the same id with other words is a conflict")
	assert.Contains(t, errs, "APPEND FAILED session=s1 entry=e1: ")
	assert.Contains(t, errs, "run: nova-cairn receipt --store")

	code, _, _ = do(t, "a\nb", "append", "--store", store, "--session", "s1", "--entry", "e2", "--file", "-")
	require.Equal(t, 0, code)

	code, out, _ = do(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e2", "--text")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `bytes=3 source=src persisted=true published=false publish=manual text="a\nb"`)

	code, out, _ = do(t, "", "index", "--store", store)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "INDEX OK sessions=1 entries=2\n")
	assert.Contains(t, out, "INDEX SESSION session=s1 publish=manual opened=")
	assert.Equal(t, 2, strings.Count(out, "INDEX ENTRY session=s1 entry=e"))

	code, out, _ = do(t, "", "index", "--store", store, "--max", "1")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "INDEX MORE kind=entry shown=1 total=2")
}

// TestAppendOpensMissingSession: append to a session nobody opened opens it
// with publish=manual (or the --publish the line names), instead of refusing.
func TestAppendOpensMissingSession(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	require.NoError(t, os.MkdirAll(store, 0o755))
	code, _, errs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	assert.Equal(t, 2, code, "an append does not turn an empty directory into a store")
	assert.Contains(t, errs, "holds no store")
	code, _, errs = do(t, "", "open", "--store", store, "--session", "s0", "--publish", "manual")
	require.Equal(t, 0, code, errs)
	code, out, _ := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "APPEND OK session=s1 entry=e1")
	assert.Contains(t, out, "publish=manual")
	assert.Contains(t, out, "opened=true")

	code, _, errs = do(t, "", "append", "--store", filepath.Join(t.TempDir(), "nope"), "--session", "s1", "--entry", "e1", "--text", "x")
	assert.Equal(t, 2, code, "an append does not create a store")
	assert.Contains(t, errs, "does not exist")

	code, out, _ = do(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e1", "--text")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `text="the words"`)

	code, out, _ = do(t, "", "append", "--store", store, "--session", "s2", "--entry", "e1", "--text", "x", "--publish", "never")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "publish=never")

	code, out, _ = do(t, "", "open", "--store", store, "--session", "s1")
	require.Equal(t, 0, code, "re-opening without --publish keeps the recorded policy")
	assert.Contains(t, out, "publish=manual")
	assert.Contains(t, out, "reopened=true")
}

// TestOpenRemedies: help states the defaults, and an open under an unwritable
// parent is remedied by looking at the parent, not the store.
func TestOpenRemedies(t *testing.T) {
	t.Parallel()
	_, out, _ := do(t, "", "open", "-h")
	assert.Contains(t, out, "default never on open")
	for _, v := range []string{"search", "check"} {
		_, out, _ = do(t, "", "memory", v, "-h")
		assert.Contains(t, out, "default --channels bm25 --k 5")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	parent := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.MkdirAll(parent, 0o755))
	require.NoError(t, os.Chmod(parent, 0o500))
	defer os.Chmod(parent, 0o755)
	code, _, errs := do(t, "", "open", "--store", filepath.Join(parent, "store"), "--session", "s1", "--publish", "manual")
	assert.Equal(t, 2, code)
	assert.True(t, strings.HasSuffix(errs, "; run: ls -ld "+parent+"\n"), errs)
}

// TestAppendNextOnlyOnFirstEntry: the first entry of a session ends APPEND OK
// with the index command; later entries and duplicates do not repeat it.
func TestAppendNextOnlyOnFirstEntry(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	code, out, _ := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "words")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "\nnext: nova-cairn index --store "+store+"\n")
	code, out, _ = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e2", "--text", "more")
	require.Equal(t, 0, code)
	assert.NotContains(t, out, "next")
	code, out, _ = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "words")
	require.Equal(t, 0, code)
	assert.NotContains(t, out, "next")
}

// TestFilesOnDisk pins the formats on disk, so cairns already written keep reading.
func TestFilesOnDisk(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "s1", "--source", "src")
	do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "hello")
	entry, err := os.ReadFile(filepath.Join(store, "entries", "s1", "e1.json"))
	require.NoError(t, err)
	assert.Regexp(t, `^\{"session":"s1","id":"e1","stamp":"\S+","source":"src","publish":"never","text":"hello"\}$`, string(entry))
	log, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
	require.NoError(t, err)
	assert.Regexp(t, `^\{"entry":"","event":"open","publish":"never","session":"s1","source":"src","stamp":"\S+"\}\n\{"entry":"e1","event":"append",`, string(log))
	record, err := os.ReadFile(filepath.Join(store, "sessions", "s1.md"))
	require.NoError(t, err)
	assert.Regexp(t, `^# cairn s1\n\nOpened: \S+\nSource: src\nPublish: never\nENTRY e1 \S+\n$`, string(record))
}

func TestReopenAndRefusals(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	code, out, _ := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "reopened=true")
	code, _, errs := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "never")
	assert.Equal(t, 1, code, "a re-open naming another policy is a conflict")
	assert.Contains(t, errs, "OPEN FAILED: session \"s1\" is already open with publish=manual")

	for _, c := range [][]string{
		{"open", "--store", store, "--session", "a b", "--publish", "never"}, // not an id
		{"open", "--store", store, "--session", "s2", "--publish", "bogus"},  // not a policy
		{"open", "--store", store}, // missing flags
		{"append", "--store", store, "--session", "s1", "--entry", "e", "--text", "x", "--file", "-"},
		{"append", "--store", store, "--session", "s1", "--entry", "e"},
		{"append", "--store", store, "--session", "s1", "--entry", "e", "--text", ""},
		{"receipt", "--store", store, "--session", "s1", "--entry", "missing"},
		{"index", "--store", filepath.Join(store, "absent")},
		{"bogus"},
	} {
		code, out, errs := do(t, "", c...)
		assert.Equal(t, 2, code, "%v", c)
		assert.Empty(t, out, "%v", c)
		assertRefusal(t, errs, c)
	}
}

func TestValidID(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"s1", "a-b_c.d", "2026-11-05"} {
		assert.True(t, validID(ok), ok)
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a b", "a.", "nul", "COM1.txt", "a:b", strings.Repeat("x", 129)} {
		assert.False(t, validID(bad), bad)
	}
}

// TestFlatRecordReads: a hand-kept flat record is read, indexed and receipted, and never written.
func TestFlatRecordReads(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	flat := "# notes\n\n## 2026-10-01T10:00:00Z — a1\n\nfirst words\n\n## 2026-10-01T11:00:00Z — a2\n\nsecond\n"
	require.NoError(t, os.WriteFile(filepath.Join(store, "f1.md"), []byte(flat), 0o644))
	code, out, _ := do(t, "", "index", "--store", store)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "INDEX SESSION session=f1 publish=unknown opened=- entries=2")
	assert.Contains(t, out, "INDEX ENTRY session=f1 entry=a1 stamp=2026-10-01T10:00:00Z bytes=11 source=-")
	_, out, _ = do(t, "", "receipt", "--store", store, "--session", "f1", "--entry", "a2", "--text")
	assert.Contains(t, out, `publish=unknown text="second"`)
	code, _, errs := do(t, "", "append", "--store", store, "--session", "f1", "--entry", "a3", "--text", "x")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "flat record")
	got, _ := os.ReadFile(filepath.Join(store, "f1.md"))
	assert.Equal(t, flat, string(got))
}

func TestSelfTalk(t *testing.T) {
	t.Parallel()
	page := "I cannot ever get this right.\nI always break the build.\nNothing I do works.\n\n" +
		"On 2026-09-30 I am bad at estimating time.\n\nRULE: my central pathology gets a second reader.\n\nShe wrote: \"I always overpromise.\"\n"
	code, out, errs := do(t, page, "self-talk", "-")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `SELFTALK FAIL -:1: STANDING match="cannot ever": I cannot ever get this right.`)
	assert.Contains(t, errs, `SELFTALK FAIL -:2: TRAIT match="I always break": I always break the build.`)
	assert.Contains(t, errs, `SELFTALK FAIL -:3: FORECLOSURE match="Nothing I do works":`)
	assert.Equal(t, 3, strings.Count(errs, "SELFTALK FAIL"), "a dated claim, an instrument and a quotation are not findings")
	assert.Contains(t, out, "SELFTALK DATED n=1 files=1\n")
	assert.Contains(t, out, "SELFTALK FAIL files=1 claims=2 standing=1 installations=2 dated=1 shown=3\n")

	code, out, errs = do(t, "The tests passed on the first run.\n", "self-talk", "-")
	assert.Equal(t, 0, code)
	assert.Empty(t, errs)
	assert.Contains(t, out, "SELFTALK OK files=1 claims=0 standing=0 installations=0 dated=0\n")
}

func TestSelfTalkFlags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rules := filepath.Join(dir, "RULES.md")
	require.NoError(t, os.WriteFile(rules, []byte("I always break the build.\nI always overpromise.\nI always forget.\n"), 0o644))

	code, out, errs := do(t, "", "self-talk", "--rule-doc", "RULES.md", "--max", "1", rules)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "SELFTALK RULEDOC "+rules+": rule documents:")
	assert.Equal(t, 1, strings.Count(errs, "SELFTALK FAIL "), "--max 1 prints one finding")
	assert.Contains(t, errs, "SELFTALK MORE kind=installation shown=1 total=3 --max <n> raises the ceiling")

	code, out, _ = do(t, "", "self-talk", "--skip", "RULES.md", rules, "-")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SELFTALK SKIP "+rules+" (--skip)\n")

	_, out, _ = do(t, "", "self-talk", "--skip", "other.md", "--rule-doc", "none.md", rules)
	assert.Contains(t, out, "SELFTALK NOTE --skip other.md matched no file named on the line")
	assert.Contains(t, out, "SELFTALK NOTE --rule-doc none.md matched no file named on the line")

	for _, c := range [][]string{
		{"self-talk"},
		{"self-talk", filepath.Join(dir, "absent.md")},
		{"self-talk", "--skip", "a/b.md", rules},
		{"self-talk", "--max", "-1", rules},
	} {
		code, _, errs := do(t, "", c...)
		assert.Equal(t, 2, code, "%v", c)
		assertRefusal(t, errs, c)
	}
}

func TestSelfTalkInstallationShapes(t *testing.T) {
	t.Parallel()
	for shape, found := range map[string][]string{
		"FORECLOSURE":   {"I have no associative recall.", "There is no felt duration here.", "I will never be a good planner."},
		"RANKING":       {"Recall, my weakest instrument, failed again.", "I am the best reviewer here."},
		"VERDICT-IDIOM": {"Known as a proposition, dead as a practice.", "Confabulation is my central pathology."},
		"TRAIT":         {"I tend to overpromise.", "I never ask for help."},
	} {
		for _, s := range found {
			got, _ := installation(s)
			assert.Equal(t, shape, got, s)
		}
	}
	for _, passes := range []string{
		"I have no idea what you really are.", "The plan is my next step.", "I try my best on every page.",
		"Keep my central pathology in view.", "I want my central pathology to stay in view.",
		"RULE: my central pathology gets a second reader.", "I tended to overpromise that week.",
		"On 2026-09-30 my weakest instrument failed.",
	} {
		got, _ := installation(passes)
		assert.Empty(t, got, passes)
	}
}

// TestBannerListsEachVerbOnce: the banner is one usage line per verb, names
// where the long text lives, and carries none of the group notes.
func TestBannerListsEachVerbOnce(t *testing.T) {
	t.Parallel()
	code, out, _ := do(t, "", "-h")
	require.Equal(t, 0, code)
	assert.Equal(t, 11, strings.Count(out, "\n  nova-cairn "), out)
	assert.Contains(t, out, "nova-cairn help <verb>")
	assert.Contains(t, out, "cmd/nova-cairn/README.md")
	assert.NotContains(t, out, "Every write is synced")
}

func TestMemory(t *testing.T) {
	t.Parallel()
	c := "testdata/corpus"
	code, out, _ := do(t, "", "memory", "stats", "--root", c)
	assert.Equal(t, 0, code)
	assert.Regexp(t, `^STATS OK schema=nova-memory/2 files=6 entries=0 skipped=0 chunks=20 bytes=4866 vocab=380 avg-terms=39.3 build=\S+\nSTATS OK class=\.`, out)

	code, out, _ = do(t, "", "memory", "search", "--root", c, "--channels", "bm25", "--k", "2", "lantern", "brass")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `SEARCH OK hits=2 k=2 channels=bm25 files=6 entries=0 skipped=0 chunks=20: query="lantern brass"`)
	assert.Contains(t, out, "SEARCH CAL score=2.54 score-channel=bm25 probe=unrelated-control")
	assert.Contains(t, out, "SEARCH HIT rank=1 score=3.88 score-channel=bm25 class=notes name=- type=- root=testdata/corpus: notes/index-notes.md:8 ")
	assert.Contains(t, out, "SEARCH HIT rank=2 score=3.29 score-channel=bm25 class=notes name=lantern-care type=measured")
	assert.NotContains(t, out, "fused=")
	assert.NotContains(t, out, "SEARCH NOTE", "a search with hits needs no note")

	_, out, _ = do(t, "", "memory", "search", "--root", c, "--channels", "bm25", "--k", "2", "zzzzqq")
	assert.Contains(t, out, "SEARCH MISS every query term is out of vocabulary for this corpus")
	assert.Contains(t, out, "SEARCH NOTE lexical only", "a miss is where the note changes the next move")

	draft := "The lantern glazing needs clean cloths for brass and glass every week.\n\nToo short.\n"
	code, out, _ = do(t, draft, "memory", "check", "--root", c, "--channels", "bm25", "--k", "1", "-")
	assert.Equal(t, 0, code, "check never judges")
	assert.Contains(t, out, "MEMORY OK candidates=1 source=- k=1 channels=bm25 files=6 entries=0 skipped=0 chunks=20")
	assert.Contains(t, out, "MEMORY HIT cand=1 rank=1 ")
	assert.Contains(t, out, "notes/lantern.md:")
	assert.NotContains(t, out, "MEMORY NOTE", "no dated-log hit, so no note")

	code, out, _ = do(t, "zzzzqq wwwwww vvvvvv uuuuuu.\n", "memory", "check", "--root", c, "--channels", "bm25", "--k", "1", "-")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "MEMORY MISS")
	assert.Contains(t, out, "MEMORY NOTE lexical only", "a check miss explains itself like a search miss")
}

// TestSearchCalBelowExactHit: the unrelated control must score below an exact
// hit; shared function words alone once let it win (0.97 over 0.86).
func TestSearchCalBelowExactHit(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	do(t, "", "open", "--store", store, "--session", "today", "--publish", "never")
	do(t, "", "append", "--store", store, "--session", "today", "--entry", "lesson-1", "--text", "The bench needs NOVA_TEST_NO_HOST=1 for the unit tests.")
	code, out, _ := do(t, "", "memory", "search", "--root", store, "--channels", "bm25", "--k", "3", "bench", "unit", "tests")
	require.Equal(t, 0, code)
	score := func(kind string) float64 {
		m := regexp.MustCompile(kind + ` score=([0-9.]+)`).FindStringSubmatch(out)
		require.Len(t, m, 2, out)
		f, err := strconv.ParseFloat(m[1], 64)
		require.NoError(t, err)
		return f
	}
	assert.Less(t, score("SEARCH CAL"), score("SEARCH HIT rank=1"), out)
}

func TestMemoryRefusals(t *testing.T) {
	t.Parallel()
	c := "testdata/corpus"
	for _, args := range [][]string{
		{"memory"},
		{"memory", "stats"},
		{"memory", "search", "--root", c, "--channels", "bm25", "--k", "2"},
		{"memory", "search", "--root", c, "--channels", "trigram", "--k", "2", "x"},
		{"memory", "search", "--root", c, "--channels", "bm25", "--k", "0", "x"},
		{"memory", "stats", "--root", c, "--exclude", "*"},
		{"memory", "check", "--root", c, "--channels", "bm25", "--k", "1"},
		{"memory", "verify", "--root", c, "--links", "info"},
		{"memory", "verify", "--root", c, "--links", "maybe", "--coverage", "notes/*.md:notes/*.md"},
		{"memory", "verify", "--root", c, "--links", "gate", "--frontmatter", "nothing/*.md"},
	} {
		code, out, errs := do(t, "", args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, out, "%v", args)
		assertRefusal(t, errs, args)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	c := "testdata/corpus"
	code, out, errs := do(t, "", "memory", "verify", "--root", c, "--links", "info", "--coverage", "notes/*.md:notes/index-*.md",
		"--frontmatter", "notes/*.md", "--exempt", "index-")
	assert.Equal(t, 0, code)
	assert.Empty(t, errs)
	assert.Equal(t, "VERIFY CHECKED rule=coverage glob=notes/*.md:notes/index-*.md files=4 indexes=1\n"+
		"VERIFY CHECKED rule=frontmatter glob=notes/*.md files=3\n"+
		"VERIFY INFO wikilink: [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)\n"+
		"VERIFY OK gating=0 info=1 shown=1 coverage=0 frontmatter=0 links=info\n", out)

	code, _, errs = do(t, "", "memory", "verify", "--root", c, "--links", "gate", "--frontmatter", "log/*.md")
	assert.Equal(t, 1, code)
	assert.Equal(t, "VERIFY FAIL frontmatter log/1974-03-11.md: no name: in frontmatter\n"+
		"VERIFY FAIL wikilink [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)\n"+
		"VERIFY FAIL gating=2 shown=2 info=0 coverage=0 frontmatter=1 links=gate\n", errs)

	_, _, errs = do(t, "", "memory", "verify", "--root", c, "--links", "gate", "--fail-max", "1", "--frontmatter", "log/*.md")
	assert.NotContains(t, errs, "VERIFY MORE", "one finding per kind fits under --fail-max 1")
}

// TestVerifyCoverage uses a tree built in the test: an unlisted note, a dead link, a wikilink to a frontmatter name.
func TestVerifyCoverage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	write("m/listed.md", "---\nname: listed\n---\nbody\n")
	write("m/unlisted.md", "---\nname: unlisted-name\n---\nbody\n")
	write("m/index-a.md", "- [listed](listed.md)\n- [gone](gone.md)\n- see [[unlisted-name]] and `[[example]]`\n")
	code, _, errs := do(t, "", "memory", "verify", "--root", root, "--links", "gate", "--coverage", "m/*.md:m/index-*.md")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "VERIFY FAIL coverage m/unlisted.md: stem \"unlisted\" appears in no file matching m/index-*.md")
	assert.Contains(t, errs, "VERIFY FAIL backlink m/index-a.md links gone.md which does not exist (resolved m/gone.md)")
	assert.NotContains(t, errs, "wikilink", "a wikilink resolves to a frontmatter name, and a backticked one is not a link")
	assert.Contains(t, errs, "VERIFY FAIL gating=2 shown=2 info=0 coverage=2 frontmatter=0 links=gate")
}

// TestMemoryFindsStoreEntries: the words an AI appended are found by memory search,
// stats and check, and a hit names what receipt needs. No real clock: the store is
// written with a fixed time.
func TestMemoryFindsStoreEntries(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "cairn")
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	_, _, err := open(store, "s1", "", at, "manual")
	require.NoError(t, err)
	_, _, _, _, err = appendEntry(store, "s1", "e1", "The brass lantern illuminates the harbor.", "", at, "manual")
	require.NoError(t, err)
	_, _, _, _, err = appendEntry(store, "s1", "e2", "tide tables", "", at.Add(time.Second), "manual")
	require.NoError(t, err)

	code, out, _ := do(t, "", "memory", "search", "--root", store, "--channels", "bm25", "--k", "2", "brass", "lantern")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "SEARCH OK hits=1 k=2 channels=bm25 files=0 entries=2 skipped=0 chunks=2:")
	assert.Contains(t, out, "SEARCH HIT rank=1 ")
	assert.Contains(t, out, " store="+store+" session=s1 entry=e1 stamp=2026-11-05T12:00:00Z: entries/s1/e1.json:1 \"The brass lantern illuminates the harbor.\"")

	// The hit's fields are what receipt takes.
	code, out, _ = do(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e1", "--text")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `text="The brass lantern illuminates the harbor."`)

	// A short entry is still found; the session record's header is not a note.
	_, out, _ = do(t, "", "memory", "search", "--root", store, "--channels", "bm25", "--k", "2", "tide")
	assert.Contains(t, out, "entry=e2 stamp=2026-11-05T12:00:01Z")
	_, out, _ = do(t, "", "memory", "search", "--root", store, "--channels", "bm25", "--k", "2", "opened", "publish")
	assert.Contains(t, out, "SEARCH MISS")
	_, out, _ = do(t, "", "memory", "stats", "--root", store)
	assert.Contains(t, out, "STATS OK schema=nova-memory/2 files=0 entries=2 skipped=0 chunks=2 ")

	// Notes and a store together, and a draft checked against both.
	draft := "The lantern glazing and the brass need cloths every week.\n"
	code, out, _ = do(t, draft, "memory", "check", "--root", "testdata/corpus", "--root", store, "--channels", "bm25", "--k", "9", "-")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "files=6 entries=2 skipped=0 chunks=22")
	assert.Contains(t, out, "entry=e1 ")
	assert.Contains(t, out, "notes/lantern.md:")

	// verify reads notes only: a store with no notes has nothing to verify.
	code, _, errs := do(t, "", "memory", "verify", "--root", store, "--links", "gate")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no markdown notes found")
}

// TestMemorySkipsAJSONThatIsNotAnEntry: a .json under some entries/ directory that is
// not a store entry is left out with one warning and counted, never a refusal.
func TestMemorySkipsAJSONThatIsNotAnEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	write("notes/a.md", "The brass lantern needs a clean cloth.\n")
	write("web/entries/x/y.json", `{"name":"a package manifest"}`)
	write("web/entries/x/z.json", `not json`)
	code, out, errs := do(t, "", "memory", "stats", "--root", root)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "STATS OK schema=nova-memory/2 files=1 entries=0 skipped=2 chunks=1 ")
	assert.Equal(t, 1, strings.Count(errs, "\n"), "warned once")
	assert.Contains(t, errs, "STATS WARN skipped=2 first="+filepath.Join(root, "web", "entries", "x", "y.json")+":")

	code, out, errs = do(t, "", "memory", "search", "--root", root, "--exclude", "web", "--channels", "bm25", "--k", "1", "brass")
	assert.Equal(t, 0, code)
	assert.Empty(t, errs, "--exclude leaves them out quietly")
	assert.Contains(t, out, "files=1 entries=0 skipped=0 chunks=1")
}

// TestARuntimeFailureNamesTheNextCommand: a file memory cannot read is refused
// with the command that leaves it out; a missing store or self-talk file with the
// command that looks at it.
func TestARuntimeFailureNamesTheNextCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("The brass lantern needs a clean cloth.\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "big.md"), bytes.Repeat([]byte("word "), maxFile/5+1), 0o644))
	code, out, errs := do(t, "", "memory", "search", "--root", root, "--channels", "bm25", "--k", "1", "brass")
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Contains(t, errs, "big.md is over the")
	assert.True(t, strings.HasSuffix(errs, "; run: nova-cairn memory search --exclude big.md --root "+root+" --channels bm25 --k 1 brass\n"), errs)
	code, _, _ = do(t, "", "memory", "search", "--exclude", "big.md", "--root", root, "--channels", "bm25", "--k", "1", "brass")
	assert.Equal(t, 0, code, "the printed remedy runs")

	missing := filepath.Join(root, "absent")
	_, _, errs = do(t, "", "index", "--store", missing)
	assert.True(t, strings.HasSuffix(errs, "; run: nova-cairn open --store "+missing+" --session first --publish manual\n"), errs)
	_, _, errs = do(t, "", "self-talk", missing)
	assert.True(t, strings.HasSuffix(errs, "; run: ls -l "+missing+"\n"), errs)
	bad := filepath.Join(root, "bad.md")
	require.NoError(t, os.WriteFile(bad, []byte{0xff, '\n'}, 0o644))
	_, _, errs = do(t, "", "self-talk", bad)
	assert.True(t, strings.HasSuffix(errs, "; run: nova-cairn self-talk --skip bad.md "+bad+"\n"), errs)
	assertRefusal(t, errs, nil)
}

// TestEveryVerbHelpsAndRefusesUnknownFlags: -h, --help and help <verb> exit 0 with
// the verb's help on stdout; a flag the verb does not take is refused in one line.
func TestEveryVerbHelpsAndRefusesUnknownFlags(t *testing.T) {
	t.Parallel()
	for _, g := range groups {
		for _, v := range g.verbs {
			path := strings.Fields(v[0])
			for _, args := range [][]string{append(slices.Clone(path), "-h"), append(slices.Clone(path), "--help"), append([]string{"help"}, path...)} {
				code, out, errs := do(t, "", args...)
				assert.Equal(t, 0, code, "%v", args)
				assert.Contains(t, out, "nova-cairn "+v[0], "%v", args)
				assert.Empty(t, errs, "%v", args)
			}
			args := append(slices.Clone(path), "--no-such-flag")
			code, out, errs := do(t, "", args...)
			assert.Equal(t, 2, code, "%v", args)
			assert.Empty(t, out, "%v", args)
			assert.Regexp(t, `^nova-cairn `+v[0]+`: unknown flag --no-such-flag for `+v[0]+`; run: nova-cairn `+v[0]+` -h\n$`, errs)
		}
	}
	for _, ignored := range [][]string{
		{"index", "--store", "x", "--text", "y"},
		{"open", "--store", "x", "--session", "s", "--publish", "never", "--max", "3"},
		{"memory", "stats", "--root", "testdata/corpus", "--k", "3"},
		{"memory", "search", "--root", "testdata/corpus", "--channels", "bm25", "--k", "3", "--links", "gate", "x"},
		{"memory", "stats", "--root", "testdata/corpus", "stray"},
		{"version", "x"},
	} {
		code, out, errs := do(t, "", ignored...)
		assert.Equal(t, 2, code, "%v", ignored)
		assert.Empty(t, out, "%v", ignored)
		assertRefusal(t, errs, ignored)
	}
	code, _, errs := do(t, "", "no-such-verb")
	assert.Equal(t, 2, code)
	assert.Equal(t, "nova-cairn: unknown verb no-such-verb; the verbs are "+verbList+"; run: nova-cairn help\n", errs)
	code, _, errs = do(t, "", "--max", "x")
	assert.Equal(t, 2, code)
	assert.NotContains(t, errs, "flag provided")
}

// TestHealLogsTheHeadersPolicy: an open that crashed before its log line is healed
// with the policy its header recorded (as the model's Open does), and a re-open
// naming another policy is then a conflict.
func TestHealLogsTheHeadersPolicy(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(store, "sessions"), 0o755))
	header := "# cairn s1\n\nOpened: 2026-11-05T12:00:00Z\nSource: src\nPublish: never\n"
	require.NoError(t, os.WriteFile(filepath.Join(store, "sessions", "s1.md"), []byte(header), 0o644))
	code, _, errs := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "OPEN FAILED: session \"s1\" is already open with publish=never source=src")
	log, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(log), `"event":"open","publish":"never","session":"s1","source":"src"`)
	assert.Equal(t, 1, strings.Count(string(log), "\n"), "healed once")
	code, out, _ := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "never")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "source=src publish=never reopened=true")
}

// A session whose header is gone and whose open record is in the log gets the
// header back as the log has it, whatever a re-open names, and no second record.
func TestALostHeaderIsWrittenBackFromTheLog(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	code, _, _ := do(t, "", "open", "--store", store, "--session", "s1", "--source", "src", "--publish", "never")
	require.Equal(t, 0, code)
	path := filepath.Join(store, "sessions", "s1.md")
	header, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	code, _, errs := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "immediate")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "OPEN FAILED: session \"s1\" is already open with publish=never source=src")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(header), string(got), "the header is the log's open record")
	log, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(log), `"event":"open"`), "one open record")
	code, out, _ := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "never")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "source=src publish=never reopened=true")
}

func TestARetryNamingAnotherSourceIsAConflict(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "s1", "--source", "a", "--publish", "never")
	code, _, _ := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "w")
	require.Equal(t, 0, code)
	code, out, _ := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "w", "--source", "a")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "duplicate=true")
	code, _, errs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "w", "--source", "b")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "APPEND FAILED session=s1 entry=e1: entry \"e1\" already holds these words with source=a; --source b names another")
}

func TestALateFlagIsRefused(t *testing.T) {
	t.Parallel()
	code, out, _ := do(t, "", "memory", "search", "--root", "testdata/corpus", "lantern", "--k", "2")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `SEARCH OK hits=2 k=2 channels=bm25`)

	code, out, _ = do(t, "The tests passed on the first run.\n", "self-talk", "-", "--max", "1")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SELFTALK OK files=1")

	code, out, _ = do(t, "", "memory", "search", "--root", "testdata/corpus", "--", "-lantern")
	assert.Equal(t, 0, code, "after -- a word may start with -")
	assert.Contains(t, out, `k=5 channels=bm25`)
	assert.Contains(t, out, `query="-lantern"`)
}

// TestAppendRefusesBytesThatAreNotUTF8: the entry file is JSON, which would replace
// them, so they are refused and nothing is written; what is accepted round-trips exactly.
func TestAppendRefusesBytesThatAreNotUTF8(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "s1", "--publish", "never")
	bad := filepath.Join(store, "latin1.txt")
	require.NoError(t, os.WriteFile(bad, []byte("caf\xe9!"), 0o644))
	code, out, errs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--file", bad)
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-cairn append: text is not valid UTF-8 at byte 3; convert it first (iconv -f <its encoding> -t UTF-8); run: file "+bad+"\n", errs)
	code, _, errs = do(t, "ab\xff", "append", "--store", store, "--session", "s1", "--entry", "e1", "--file", "-")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "at byte 2;")
	code, _, errs = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "x", "--source", "p\xfe")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--source is not valid UTF-8 at byte 1")
	assert.False(t, validID("s\xff"))
	_, err := os.Stat(filepath.Join(store, "entries"))
	assert.True(t, os.IsNotExist(err), "nothing was written")

	words := "café — 日本   tab\tend\n"
	code, _, _ = do(t, words, "append", "--store", store, "--session", "s1", "--entry", "e2", "--file", "-")
	require.Equal(t, 0, code)
	_, out, _ = do(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e2", "--text")
	assert.Contains(t, out, "bytes="+strconv.Itoa(len(words))+" ")
	assert.Contains(t, out, "text="+strconv.Quote(words))
}

// TestAppendMissingFileNamesIt: a --file that does not exist is refused with ls -l
// of the path, quoted.
func TestAppendMissingFileNamesIt(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "ok", "--publish", "never")
	missing := filepath.Join(store, "no such file")
	code, _, errs := do(t, "", "append", "--store", store, "--session", "ok", "--entry", "e", "--file", missing)
	assert.Equal(t, 2, code)
	assert.True(t, strings.HasSuffix(errs, "; run: ls -l '"+missing+"'\n"), errs)
}

// TestMemoryReadsSymlinkedNotes: a note that is a symlink, and a --root that is a
// symlink, are read through the link; a broken linked note is refused by name.
func TestMemoryReadsSymlinkedNotes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "real"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real", "lantern.md"), []byte("The brass lantern needs a clean cloth.\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join("..", "real", "lantern.md"), filepath.Join(dir, "notes", "lantern.md")))
	require.NoError(t, os.Symlink("notes", filepath.Join(dir, "linkroot")))

	for _, root := range []string{filepath.Join(dir, "notes"), filepath.Join(dir, "linkroot")} {
		code, out, errs := do(t, "", "memory", "search", "--root", root, "--channels", "bm25", "--k", "1", "brass")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "SEARCH OK hits=1 k=1 channels=bm25 files=1 ", root)
		assert.Contains(t, out, ": lantern.md:1 ", root)
	}

	require.NoError(t, os.Symlink("gone.md", filepath.Join(dir, "notes", "broken.md")))
	code, _, errs := do(t, "", "memory", "search", "--root", filepath.Join(dir, "notes"), "--channels", "bm25", "--k", "1", "brass")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--exclude broken.md")
}

// TestVerifyGlobWithoutSlashMatchesAtAnyDepth: '*.md' checks nested notes too,
// and each rule says how many files it checked.
func TestVerifyGlobWithoutSlashMatchesAtAnyDepth(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("---\nname: a\n---\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "nameless.md"), []byte("body\n"), 0o644))
	code, out, errs := do(t, "", "memory", "verify", "--root", root, "--links", "gate", "--frontmatter", "*.md")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, errs, "VERIFY FAIL frontmatter sub/nameless.md: no name: in frontmatter\n")
	assert.Contains(t, out, "VERIFY CHECKED rule=frontmatter glob=*.md files=2\n")

	code, out, _ = do(t, "", "memory", "verify", "--root", root, "--links", "gate", "--frontmatter", "a.md", "--coverage", "*.md:a.md")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "VERIFY CHECKED rule=coverage glob=*.md:a.md files=2 indexes=1\n")
	assert.Contains(t, out, "VERIFY CHECKED rule=frontmatter glob=a.md files=1\n")
}

// TestSelfTalkScanningNothingIsRefused: a run whose every file is skipped scanned
// nothing, so it is refused, never a pass.
func TestSelfTalkScanningNothingIsRefused(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "a.md")
	require.NoError(t, os.WriteFile(f, []byte("I always break the build.\n"), 0o644))
	code, out, errs := do(t, "", "self-talk", "--skip", "a.md", f)
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assertRefusal(t, errs, nil)
	assert.Contains(t, errs, "run: nova-cairn self-talk "+f+"\n")
}

// TestIndexRefusesWhatItCannotRead: an unreadable sessions/ directory or a corrupt
// open record is refused by index as append refuses it, never listed as empty or unknown.
func TestIndexRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	code, _, errs := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	require.Equal(t, 0, code, errs)
	log := filepath.Join(store, "log.jsonl")
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	line := strings.TrimSpace(string(raw))
	require.NoError(t, os.WriteFile(log, []byte(line[:len(line)-10]+"\n"), 0o644))
	code, _, appendErrs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "x")
	assert.Equal(t, 2, code)
	code, out, errs := do(t, "", "index", "--store", store)
	assert.Equal(t, 2, code, out)
	assert.Empty(t, out)
	assert.Equal(t, strings.Replace(appendErrs, "nova-cairn append:", "nova-cairn index:", 1), errs)

	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	require.NoError(t, os.WriteFile(log, raw, 0o644))
	sessions := filepath.Join(store, "sessions")
	require.NoError(t, os.Chmod(sessions, 0o000))
	defer os.Chmod(sessions, 0o755)
	code, out, errs = do(t, "", "index", "--store", store)
	assert.Equal(t, 2, code, out)
	assert.Empty(t, out)
	assert.Contains(t, errs, "run: ls -ld "+sessions+"\n")
}

// TestMemoryReportsACorruptStoreEntry: an entry file of a store that does not decode
// is refused as corrupt with the repair, not skipped as some other .json.
func TestMemoryReportsACorruptStoreEntry(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "s")
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	_, _, err := open(store, "s1", "", at, "manual")
	require.NoError(t, err)
	_, _, _, _, err = appendEntry(store, "s1", "e1", "The brass lantern lights the harbor.", "", at, "manual")
	require.NoError(t, err)
	entry := filepath.Join(store, "entries", "s1", "e1.json")
	require.NoError(t, os.WriteFile(entry, []byte(`{"session":"s1","id":"e1","te`), 0o644))
	code, out, errs := do(t, "", "memory", "search", "--root", store, "--channels", "bm25", "--k", "1", "brass")
	assert.Equal(t, 2, code, out)
	assert.Empty(t, out)
	assert.Contains(t, errs, `stored entry "e1" is corrupt`)
	assert.True(t, strings.HasSuffix(errs, "; run: cat "+entry+"\n"), errs)
}

// TestAppendHealsAMissingLogLine: when the log line fails after the entry is kept,
// the refusal says the entry was kept and points at the log; the retry writes the line.
func TestAppendHealsAMissingLogLine(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes anything")
	}
	store := filepath.Join(t.TempDir(), "s")
	code, _, errs := do(t, "", "open", "--store", store, "--session", "s1", "--publish", "manual")
	require.Equal(t, 0, code, errs)
	log := filepath.Join(store, "log.jsonl")
	require.NoError(t, os.Chmod(log, 0o444))
	code, _, errs = do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `entry "e1" is kept`)
	assert.True(t, strings.HasSuffix(errs, "; run: ls -l "+log+"\n"), errs)

	require.NoError(t, os.Chmod(log, 0o644))
	code, out, errs := do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "duplicate=true")
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(raw), `"event":"append"`), string(raw))
	do(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "the words")
	raw, _ = os.ReadFile(log)
	assert.Equal(t, 1, strings.Count(string(raw), `"event":"append"`), "a second retry logs nothing more")
}

// TestMemoryRefusesAMalformedExclude: an --exclude that is not a valid glob is refused,
// never applied as a literal path.
func TestMemoryRefusesAMalformedExclude(t *testing.T) {
	t.Parallel()
	for _, verb := range [][]string{{"stats"}, {"search", "--channels", "bm25", "--k", "1", "brass"}} {
		args := append([]string{"memory", verb[0], "--root", "testdata/corpus", "--exclude", "["}, verb[1:]...)
		code, out, errs := do(t, "", args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, out)
		assertRefusal(t, errs, args)
		assert.Contains(t, errs, `--exclude "[" is not a valid glob`)
	}
}
