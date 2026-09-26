package ci

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// The four items of the #2910 HOLD at 76811872, one test each.

// writeDocs writes the four docs PastedDocExamples scans under root, leaving
// out any whose name is in skip.
func writeDocs(t *testing.T, root string, body map[string]string, skip ...string) {
	t.Helper()
	for _, f := range []string{"README.md", "docs/USAGE.md", "docs/CLI.md", "docs/nova-swarm-quickstart.md"} {
		if slices.Contains(skip, f) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body[f]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Item 1: a named doc that is missing is an error naming it, never a silent
// skip that shrinks the scan to fewer docs.
func TestIssue2218MissingDocIsAnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeDocs(t, root, nil, "docs/nova-swarm-quickstart.md")
	_, err := PastedDocExamples(root)
	if err == nil || !strings.Contains(err.Error(), "nova-swarm-quickstart.md") {
		t.Fatalf("PastedDocExamples with docs/nova-swarm-quickstart.md missing returned err=%v; want an error naming the missing doc", err)
	}
}

// Item 2: a GOOS is a whole word on the Platform line, never a substring of
// another word ("js" in "json", "ios" in "ratios", "aix" in "plaix").
func TestIssue2218PlatformGOOSIsAWholeWord(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"Platform: darwin": {"darwin"},
		"Platform: recorded on macOS (darwin) — a Linux bench prints": {"linux", "darwin"},
		"Platform: the json ratios differ on this bench":              nil,
		"Platform: linux, darwin":                                     {"linux", "darwin"},
	}
	for line, want := range cases {
		if got := goosValues(line); !slices.Equal(got, want) {
			t.Errorf("goosValues(%q) = %v; want %v", line, got, want)
		}
	}
}

// Item 3: a compared_examples.txt entry is tied to ITS example: the test file
// lives in the example's tool's package, and the named test itself reads a
// doc the example is pasted in.
func TestIssue2218ComparedEntryIsTiedToItsExample(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := "package main\n\nfunc TestFoo(t *testing.T) {\n\traw, _ := os.ReadFile(filepath.Join(\"docs\", \"README.md\"))\n\tonboarding.Compare(onboarding.Step{Line: \"$ nova-foo go\"}, raw, nil)\n}\n\nfunc TestBar(t *testing.T) {\n\tos.ReadFile(\"CLI.md\")\n}\n"
	writeGo(t, root, "cmd/nova-foo/foo_test.go", src)
	entry := func(ex string) ComparedEntry {
		return ComparedEntry{File: "cmd/nova-foo/foo_test.go", Test: "TestFoo", Ex: ex}
	}
	if p := ComparedEntryProblem(root, entry("$ nova-foo go"), []string{"README.md"}); p != "" {
		t.Errorf("a test in the example's tool package whose body reads the example's doc: got %q, want no problem", p)
	}
	if p := ComparedEntryProblem(root, entry("$ nova-bar go"), []string{"README.md"}); p == "" {
		t.Error("an entry for `nova-bar` naming a test in cmd/nova-foo passed; want a problem (the test cannot run nova-bar's example)")
	}
	if p := ComparedEntryProblem(root, entry("$ nova-foo go"), []string{"docs/CLI.md"}); p == "" {
		t.Error("an entry pasted in docs/CLI.md naming TestFoo, whose body reads only README.md (CLI.md is read by TestBar), passed; want a problem")
	}
}

func writeGo(t *testing.T, root, rel, src string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rowan hold 3 at d3f2ddf5, item 2: the named test must reach the comparator
// and carry the example's own command text; a sibling comparator test in the
// same file no longer validates an arbitrary example. The control is the
// hold's own: `$ nova-wake awake --bus ./bus` named under the presence test,
// which never mentions awake, fails.
func TestIssue2218ComparedEntryNamesItsCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeDocs(t, root, map[string]string{
		"docs/CLI.md": "## nova-wake\n\n```\n$ nova-wake presence --bus ./bus\n$ nova-wake awake --bus ./bus\n```\n\n## nova-sprint\n\n### First run\n\n```text\n$ nova-sprint table --once\n! refused\n```\n",
	})
	writeGo(t, root, "cmd/nova-wake/presence_test.go", `package main

func TestPresence(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	cmdLine := exampleFrom(string(raw))
	for _, p := range onboarding.Compare(onboarding.Step{Line: cmdLine}, run(), nil) {
		t.Error(p)
	}
}

func exampleFrom(cli string) string {
	const marker = "$ nova-wake presence "
	return marker
}

func TestAwakeNoComparator(t *testing.T) {
	os.ReadFile("CLI.md")
	_ = "$ nova-wake awake --bus ./bus"
}
`)
	// production code names awake; it is never followed.
	writeGo(t, root, "cmd/nova-wake/main.go", "package main\n\nfunc run() { usage := \"nova-wake awake --bus ./bus\"; _ = usage }\n")
	writeGo(t, root, "cmd/nova-sprint/table_test.go", `package main

func TestFirstRun(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	runTranscript(t, string(raw))
}

func runTranscript(t *testing.T, doc string) {
	lines, _ := onboarding.FirstRun(doc, "nova-sprint")
	steps, _ := onboarding.Steps("nova-sprint", lines)
	onboarding.Execute(steps, runDocumented)
}
`)
	cli := []string{"docs/CLI.md"}
	for _, c := range []struct {
		entry ComparedEntry
		ok    bool
		why   string
	}{
		{ComparedEntry{"cmd/nova-wake/presence_test.go", "TestPresence", "$ nova-wake presence --bus ./bus"}, true, "the test's helper carries the presence command and the test reaches Compare"},
		{ComparedEntry{"cmd/nova-wake/presence_test.go", "TestPresence", "$ nova-wake awake --bus ./bus"}, false, "the hold's control: the presence test never names awake (main.go's banner is production code, not followed)"},
		{ComparedEntry{"cmd/nova-wake/presence_test.go", "TestAwakeNoComparator", "$ nova-wake awake --bus ./bus"}, false, "the test names awake but reaches no comparator"},
		{ComparedEntry{"cmd/nova-sprint/table_test.go", "TestFirstRun", "$ nova-sprint table --once"}, true, "the test executes the tool's First run transcript of the doc it reads, which holds the line"},
		{ComparedEntry{"cmd/nova-sprint/table_test.go", "TestFirstRun", "$ nova-sprint table --check"}, false, "the First run transcript it executes does not hold --check"},
	} {
		p := ComparedEntryProblem(root, c.entry, cli)
		if c.ok && p != "" {
			t.Errorf("%s under %s: got %q, want no problem (%s)", c.entry.Ex, c.entry.Test, p, c.why)
		}
		if !c.ok && p == "" {
			t.Errorf("%s under %s passed; want a problem (%s)", c.entry.Ex, c.entry.Test, c.why)
		}
	}
}

// rowan hold 3 at d3f2ddf5, item 1: the list is compared with its copy at the
// change's base, and a row the change adds fails. The control is the hold's
// own: a new doc example appended with the same row appended to the list.
func TestIssue2218AddedUnexecutedRowFails(t *testing.T) {
	t.Parallel()

	if got := AddedListRows("# c\n$ a\n$ b\n", "# c\n$ a\n\n$ nova-foo newverb --x 1\n"); !slices.Equal(got, []string{"$ nova-foo newverb --x 1"}) {
		t.Errorf("AddedListRows = %q; want the one appended row", got)
	}
	if got := AddedListRows("$ a\n$ b\n", "$ a\n"); len(got) != 0 {
		t.Errorf("AddedListRows on a shrink = %q; want none", got)
	}

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "dev")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "pr")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n$ nova-foo newverb --x 1\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n$ nova-foo newverb --x 1\n")
	git("commit", "-q", "-am", "adds an unexecuted example")

	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"pull_request":{"base":{"sha":"`+base+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]string{
		"pull_request event": {"GITHUB_EVENT_PATH": event},
		"local merge base":   {"GITHUB_BASE_REF": "dev"},
	} {
		got, err := ChangeBase(root, func(k string) string { return env[k] })
		if err != nil || got != base {
			t.Errorf("%s: ChangeBase = %q, %v; want %s", name, got, err, base)
			continue
		}
		baseList, present, err := ListAtCommit(root, got, UnexecutedListPath)
		if err != nil || !present {
			t.Fatalf("%s: ListAtCommit = present %v, %v; want the base's list", name, present, err)
		}
		head := loadAllowlist(t, filepath.Join(root, UnexecutedListPath), unexecutedListOptions)
		if added := AddedListRows(baseList, head.Text()); !slices.Equal(added, []string{"$ nova-foo newverb --x 1"}) {
			t.Errorf("%s: added rows = %q; want the appended row, which fails the class test", name, added)
		}
	}
	if _, present, err := ListAtCommit(root, base, "internal/ci/testdata/absent.txt"); err != nil || present {
		t.Errorf("ListAtCommit of a file the base lacks = present %v, %v; want introduced (false, nil)", present, err)
	}
	if !changeEventMustCompare("pull_request") || !changeEventMustCompare("merge_group") || changeEventMustCompare("schedule") {
		t.Error("changeEventMustCompare: a pull_request or merge_group run must compare with its base; a scheduled run need not")
	}
}

// rowan hold 3 at d3f2ddf5, item 3: `example:` entries may move to
// compared_examples.txt, and every help banner's example lines are counted,
// not only banners pasted in the four docs; an unlisted new help example
// fails the class test.
func TestIssue2218HelpBannersAreCounted(t *testing.T) {
	t.Parallel()

	list, err := allowlist.Parse("compared_examples.txt", "cmd/nova-work/main_test.go:TestReady example: nova-work ready --node a\n", comparedListOptions)
	if err != nil {
		t.Fatal(err)
	}
	got := parseCompared(t, "compared_examples.txt", list)
	if got["example: nova-work ready --node a"].test != "TestReady" {
		t.Errorf("parseCompared = %v; want the example: entry accepted", got)
	}

	root := t.TempDir()
	writeGo(t, root, "cmd/nova-foo/main.go", "package main\n\nconst usage = `usage: nova-foo run\n\nexample:\n  nova-foo run --x 1\n  nova-foo   new --y 2\n\nnova-foo prose is not an example\n`\n\nvar sub = \"usage: nova-foo sub\\n\" +\n\t\"\\nexample:\\n\" +\n\t\"  nova-foo sub --z\\n\"\n\nfunc splice(u string) string { return strings.Replace(u, \"\\nexample:\\n\", \"x\", 1) }\n")
	writeGo(t, root, "cmd/nova-foo/main_test.go", "package main\n\nconst testOnly = \"\\nexample:\\n  nova-foo test-only\\n\"\n")
	help, err := HelpBannerExamples(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example: nova-foo new --y 2", "example: nova-foo run --x 1", "example: nova-foo sub --z"}
	var keys []string
	for k := range help {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, want) {
		t.Errorf("HelpBannerExamples = %q; want %q (test files are not banners; a bare heading splice carries no lines)", keys, want)
	}
	allow := map[string]bool{"example: nova-foo run --x 1": true, "example: nova-foo sub --z": true}
	if u := unlistedExamples(keys, allow, nil); !slices.Equal(u, []string{"example: nova-foo new --y 2"}) {
		t.Errorf("unlistedExamples = %q; want the new help example, unlisted, to fail the class test", u)
	}

	writeGo(t, root, "cmd/nova-bad/main.go", "package main\n\nconst usage = \"usage\\n\\nexample:\\n\\n  nova-bad run\\n\"\n")
	if _, err := HelpBannerExamples(root); err == nil || !strings.Contains(err.Error(), "cmd/nova-bad/main.go") {
		t.Errorf("a banner whose example block opens with a blank line (no command under the heading): err = %v; want an error naming cmd/nova-bad/main.go", err)
	}
}

// Item 4: `example:` lines of a help banner pasted in a fenced block of a
// named doc are pasted examples too (the spec's "every $/example: line"),
// enumerated through HelpExampleLines.
func TestIssue2218HelpExampleBlocksInDocsAreCounted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeDocs(t, root, map[string]string{
		"docs/CLI.md": "```\nusage: nova-foo run\n\nexample:\n  nova-foo run --x 1\n  nova-foo run --y   2\n```\n\nnova-foo run --prose-not-an-example\n",
	})
	got, err := PastedDocExamples(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example: nova-foo run --x 1", "example: nova-foo run --y 2"} {
		if !slices.Contains(got, want) {
			t.Errorf("PastedDocExamples = %q; want it to include %q", got, want)
		}
	}
	if len(got) != 2 {
		t.Errorf("PastedDocExamples = %q; want exactly the two example: lines", got)
	}
}

// Emma's HOLD 7 at ac162b2f, item 2: a GOOS is matched whatever its case
// ("Linux" at docs/TESTS.md:165 and :702 names linux), and a Platform line in
// a `## nova-*` section that names no recognised GOOS is an error naming its
// line, never a line silently left out of the check.
func TestIssue2218PlatformGOOSIsCaseNormalized(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"Platform: Linux":                       {"linux"},
		"Platform: DARWIN, then a Linux bench":  {"linux", "darwin"},
		"Platform: FreeBSD":                     {"freebsd"},
		"Platform: the JSON Ratios differ here": nil,
	}
	for line, want := range cases {
		if got := goosValues(line); !slices.Equal(got, want) {
			t.Errorf("goosValues(%q) = %v; want %v", line, got, want)
		}
	}
}

func TestIssue2218PlatformLineWithNoGOOSFails(t *testing.T) {
	t.Parallel()

	md := "# Tests\n\nPlatform: prose outside a tool section is not checked\n\n## nova-foo\n\nPlatform: darwin\n\nPlatform: recorded on a machine whose wall probe fails\n\n## nova-bar\n\nPlatform: macOS\n"
	got, err := PlatformLinesFromTESTSmd(md)
	if err == nil {
		t.Fatalf("PlatformLinesFromTESTSmd returned %v and no error; want an error for the two Platform lines (lines 9 and 13) that name no recognised GOOS", got)
	}
	for _, want := range []string{"line 9", "line 13", "wall probe fails", "macOS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("PlatformLinesFromTESTSmd error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "line 3") {
		t.Errorf("PlatformLinesFromTESTSmd error %q names line 3, which is outside any `## nova-*` section", err)
	}

	ok := "## nova-foo\n\nPlatform: recorded on macOS (darwin); a Linux bench differs\n"
	got, err = PlatformLinesFromTESTSmd(ok)
	if err != nil || !slices.Equal(got, []string{"linux", "darwin"}) {
		t.Errorf("PlatformLinesFromTESTSmd(%q) = %v, %v; want [linux darwin], nil", ok, got, err)
	}
}

// The old section walker skipped every other `## nova-*` section (it cut the
// NEXT heading off as the separator), so the second of two adjacent tool
// sections was never read; each section is read.
func TestIssue2218EveryToolSectionIsRead(t *testing.T) {
	t.Parallel()

	md := "## nova-a\n\nPlatform: linux\n\n## nova-b\n\nPlatform: bogus\n\n## nova-c\n\nPlatform: darwin\n"
	got, err := PlatformLinesFromTESTSmd(md)
	if err == nil || !strings.Contains(err.Error(), "line 7") {
		t.Errorf("PlatformLinesFromTESTSmd = %v, %v; want an error naming line 7 (the Platform line of ## nova-b)", got, err)
	}
	if !slices.Equal(got, []string{"linux", "darwin"}) {
		t.Errorf("PlatformLinesFromTESTSmd platforms = %v; want [linux darwin] (nova-a and nova-c)", got)
	}
}

// Reading every section surfaced docs/TESTS.md:165's darwin, and ci.yml's
// darwin legs are declared in matrices (`os: [ubuntu-latest, macos-latest]`,
// `labels: '["self-hosted","macOS"]'`) behind a `runs-on: ${{ ... }}`
// expression, which the runs-on-only reader never saw: legs are read from the
// matrix values too, and a comment never declares a leg.
func TestIssue2218CILegsIncludeMatrixLegs(t *testing.T) {
	t.Parallel()

	yaml := "jobs:\n  a:\n    # a macOS leg is described here, in a comment only\n    strategy:\n      matrix:\n        os: [ubuntu-latest, macos-latest]\n    runs-on: ${{ matrix.os }}\n"
	if got := CILegsFromYAML(yaml); !got["linux"] || !got["darwin"] || len(got) != 2 {
		t.Errorf("CILegsFromYAML(matrix os list) = %v; want linux and darwin", got)
	}
	yaml = "jobs:\n  b:\n    strategy:\n      matrix:\n        leg:\n          - name: darwin\n            labels: '[\"self-hosted\",\"macOS\"]'\n    runs-on: ${{ fromJSON(matrix.leg.labels) }}\n"
	if got := CILegsFromYAML(yaml); !got["darwin"] || len(got) != 1 {
		t.Errorf("CILegsFromYAML(matrix labels) = %v; want darwin only", got)
	}
	yaml = "jobs:\n  c:\n    # runs-on: [self-hosted, macOS]\n    runs-on: [self-hosted, linux, x64, space]\n"
	if got := CILegsFromYAML(yaml); !got["linux"] || got["darwin"] {
		t.Errorf("CILegsFromYAML(commented macOS) = %v; want linux only", got)
	}
	legs := CILegsFromYAML(readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")))
	if !legs["linux"] || !legs["darwin"] {
		t.Errorf("CILegsFromYAML(.github/workflows/ci.yml) = %v; want linux and darwin (the studio and merge-group darwin legs)", legs)
	}
}

// Emma's HOLD 7 at f6491bd8, item 5: a banner's `example:` block is read
// whole, whatever tool leads a line. cmd/nova-redis's block has
// `nova-secrets exec ... -- nova-redis serve ...` as its second line, and the
// first-run reader (onboarding.ExampleLines) stops there, so the nova-redis
// lines under it went uncounted and their list rows read as stale. A line
// continued with ` \` is one command, counted by its first line.
func TestIssue2218HelpBannerLineLedByAnotherToolIsCounted(t *testing.T) {
	t.Parallel()

	banner := "usage: nova-foo serve\n\nexample:\n  nova-foo version\n  nova-secrets exec --only K -- nova-foo serve --port 1\n  nova-foo spill --ttl   10m \\\n           --value hi\n  nova-foo recall --name note\n\nnova-foo prose after the block is not an example\n"
	got, err := BannerExampleLines(banner)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"nova-foo version",
		"nova-secrets exec --only K -- nova-foo serve --port 1",
		"nova-foo spill --ttl 10m \\",
		"nova-foo recall --name note",
	}
	if !slices.Equal(got, want) {
		t.Errorf("BannerExampleLines = %q; want %q (every indented line of the block, a continuation folded into its command, the prose after the blank line left out)", got, want)
	}

	root := t.TempDir()
	writeGo(t, root, "cmd/nova-foo/main.go", "package main\n\nconst usage = "+strconv.Quote(banner)+"\n")
	help, err := HelpBannerExamples(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if help["example: "+w] != "cmd/nova-foo/main.go" {
			t.Errorf("HelpBannerExamples has no %q from cmd/nova-foo/main.go; got %v", "example: "+w, help)
		}
	}
	if len(help) != len(want) {
		t.Errorf("HelpBannerExamples = %v; want exactly the %d lines of the block", help, len(want))
	}

	// The real banner: every nova-redis line of the block is counted. Its
	// nova-secrets line moved into the banner's prose when serve took --dir
	// (#3879); the led-by-another-tool rule is the fixture above.
	repo, err := HelpBannerExamples(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"example: nova-redis spill --addr 127.0.0.1:6379 --owner rowan --name note --ttl 10m --value hi",
		"example: nova-redis recall --addr 127.0.0.1:6379 --owner rowan --name note",
	} {
		if repo[w] != "cmd/nova-redis/main.go" {
			t.Errorf("HelpBannerExamples(repo)[%q] = %q; want cmd/nova-redis/main.go", w, repo[w])
		}
	}
}
