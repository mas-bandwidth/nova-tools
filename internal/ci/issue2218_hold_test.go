package ci

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
// lives in the example's tool's package, and the named test function itself
// reads a doc the example is pasted in.
func TestIssue2218ComparedEntryIsTiedToItsExample(t *testing.T) {
	root := t.TempDir()
	src := "package main\n\nfunc TestFoo(t *testing.T) {\n\traw, _ := os.ReadFile(filepath.Join(\"docs\", \"README.md\"))\n\tonboarding.Compare(raw)\n}\n\nfunc TestBar(t *testing.T) {\n\tos.ReadFile(\"CLI.md\")\n}\n"
	path := filepath.Join(root, "cmd", "nova-foo", "foo_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := func(ex string) comparedExample {
		return comparedExample{file: "cmd/nova-foo/foo_test.go", test: "TestFoo", line: 1, ex: ex}
	}
	if p := comparatorTestProblem(root, entry("$ nova-foo go"), []string{"README.md"}); p != "" {
		t.Errorf("a test in the example's tool package whose body reads the example's doc: got %q, want no problem", p)
	}
	if p := comparatorTestProblem(root, entry("$ nova-bar go"), []string{"README.md"}); p == "" {
		t.Error("an entry for `nova-bar` naming a test in cmd/nova-foo passed; want a problem (the test cannot run nova-bar's example)")
	}
	if p := comparatorTestProblem(root, entry("$ nova-foo go"), []string{"docs/CLI.md"}); p == "" {
		t.Error("an entry pasted in docs/CLI.md naming TestFoo, whose body reads only README.md (CLI.md is read by TestBar), passed; want a problem")
	}
}

// Item 4: `example:` lines of a help banner pasted in a fenced block of a
// named doc are pasted examples too (the spec's "every $/example: line"),
// enumerated through HelpExampleLines.
func TestIssue2218HelpExampleBlocksInDocsAreCounted(t *testing.T) {
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
