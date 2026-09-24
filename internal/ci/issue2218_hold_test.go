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
		"Platform: recorded on macOS (darwin) — a Linux bench prints": {"darwin"},
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
