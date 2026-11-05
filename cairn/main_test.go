package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	assert.Regexp(t, `^OPEN OK session=s1 store=\S+ source=src publish=manual stamp=\S+\n$`, out)

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

// TestFilesOnDisk pins the formats on disk, so cairns already written keep reading.
func TestFilesOnDisk(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	do(t, "", "open", "--store", store, "--session", "s1", "--source", "src", "--publish", "never")
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
		{"append", "--store", store, "--session", "nope", "--entry", "e", "--text", "x"},
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
		assert.Contains(t, errs, "REFUSED", "%v", c)
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

	code, out, _ = do(t, "", "self-talk", "--skip", "RULES.md", rules)
	assert.Equal(t, 0, code, "an all-skipped run is not a green")
	assert.Contains(t, out, "SELFTALK SKIP "+rules+" (--skip)\n")
	assert.Contains(t, out, "SELFTALK SKIP files=0 skipped=1 reason=all-skipped")

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
		assert.Contains(t, errs, "REFUSED")
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

func TestMemory(t *testing.T) {
	t.Parallel()
	c := "testdata/corpus"
	code, out, _ := do(t, "", "memory", "stats", "--root", c)
	assert.Equal(t, 0, code)
	assert.Regexp(t, `^STATS OK schema=nova-memory/2 files=6 chunks=20 bytes=4866 vocab=380 avg-terms=39.3 build=\S+\nSTATS OK class=\.`, out)

	code, out, _ = do(t, "", "memory", "search", "--root", c, "--channels", "bm25", "--k", "2", "lantern", "brass")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `SEARCH OK hits=2 k=2 channels=bm25 files=6 chunks=20: query="lantern brass"`)
	assert.Contains(t, out, "SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control")
	assert.Contains(t, out, "SEARCH HIT rank=1 score=3.88 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=testdata/corpus: notes/index-notes.md:8 ")
	assert.Contains(t, out, "SEARCH HIT rank=2 score=3.29 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured")

	_, out, _ = do(t, "", "memory", "search", "--root", c, "--channels", "bm25", "--k", "2", "zzzzqq")
	assert.Contains(t, out, "SEARCH MISS every query term is out of vocabulary for this corpus")

	draft := "The lantern glazing needs clean cloths for brass and glass every week.\n\nToo short.\n"
	code, out, _ = do(t, draft, "memory", "check", "--root", c, "--channels", "bm25", "--k", "1", "-")
	assert.Equal(t, 0, code, "check never judges")
	assert.Contains(t, out, "MEMORY OK candidates=1 source=- k=1 channels=bm25 files=6 chunks=20")
	assert.Contains(t, out, "MEMORY HIT cand=1 rank=1 ")
	assert.Contains(t, out, "notes/lantern.md:")
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
		assert.Contains(t, errs, "REFUSED", "%v", args)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	c := "testdata/corpus"
	code, out, errs := do(t, "", "memory", "verify", "--root", c, "--links", "info", "--coverage", "notes/*.md:notes/index-*.md",
		"--frontmatter", "notes/*.md", "--exempt", "index-")
	assert.Equal(t, 0, code)
	assert.Empty(t, errs)
	assert.Equal(t, "VERIFY INFO wikilink: [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)\n"+
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
