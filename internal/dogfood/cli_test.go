package dogfood

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCLIReadsTheVerbsAReferenceDeclares(t *testing.T) {
	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	if err != nil {
		t.Fatalf("ParseCLI: %v", err)
	}
	got := make([]string, 0, len(verbs))
	for _, v := range verbs {
		got = append(got, v.Key())
	}
	want := []string{
		"nova-example quickstart",
		"nova-example links",
		"nova-example help",
		"nova-example version",
		"nova-fixture lift quarantine",
		"nova-fixture lift lockdown",
		"nova-fixture path",
		"nova-example status",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("verbs:\n got %q\nwant %q", got, want)
	}
}

// The document's order is the ledger's order, and the line number is how a
// reader finds the declaration the row came from.
func TestParseCLIKeepsTheDocumentsOrderAndTheDeclaringLine(t *testing.T) {
	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	if err != nil {
		t.Fatalf("ParseCLI: %v", err)
	}
	if verbs[0].Verb != "quickstart" {
		t.Fatalf("first verb = %q, want quickstart", verbs[0].Verb)
	}
	for i := 1; i < len(verbs); i++ {
		if verbs[i].Line <= 0 {
			t.Fatalf("%s carries no line number", verbs[i].Key())
		}
	}
	// `links` is declared twice; the row is the first declaration.
	for _, v := range verbs {
		if v.Key() == "nova-example links" && v.Line != 10 {
			t.Fatalf("nova-example links declared at line %d, want the FIRST declaration (10)", v.Line)
		}
	}
}

func TestParseCLIRefusesTheShapesThatAreNotDeclarations(t *testing.T) {
	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	if err != nil {
		t.Fatalf("ParseCLI: %v", err)
	}
	for _, v := range verbs {
		switch v.Key() {
		case "nova-example ghost":
			t.Fatal("prose that names a verb declared it")
		case "nova-example transcript-only":
			t.Fatal("a `$ ` transcript line declared a verb")
		case "nova-fixture lift lockdown REFUSED", "nova-fixture lift":
			t.Fatalf("the description after a flagless synopsis leaked into the verb: %q", v.Verb)
		}
		if strings.HasPrefix(v.Verb, "-") || strings.Contains(v.Verb, "<") {
			t.Fatalf("a flag or a placeholder became a verb: %q", v.Verb)
		}
	}
}

func TestParseCLIRefusesAReferenceWithNoVerbs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte("# Command reference\n\nNo verbs here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCLI(path); err == nil {
		t.Fatal("a reference declaring no verbs was accepted; a ledger over nothing says OK about nothing")
	}
}

func TestParseCLIRefusesAMissingReference(t *testing.T) {
	if _, err := ParseCLI(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Fatal("a missing reference was accepted")
	}
}

// The real reference is the one the gate will run on, so the parser is held to
// it here — loosely, on shape and not on a count that every doc change breaks.
func TestParseCLIReadsThisRepositorysOwnReference(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "CLI.md")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("docs/CLI.md not present: %v", err)
	}
	verbs, err := ParseCLI(path)
	if err != nil {
		t.Fatalf("ParseCLI(docs/CLI.md): %v", err)
	}
	if len(verbs) < 40 {
		t.Fatalf("docs/CLI.md parsed to %d verbs; the reference declares many more", len(verbs))
	}
	want := map[string]bool{
		"nova-check links":          false,
		"nova-check corpus":         false,
		"nova-swarm batch":          false,
		"nova-bus send":             false,
		"nova-fuse lift quarantine": false,
		"nova-review packet":        false,
		"nova-version snapshot":     false,
		"nova-memory quickstart":    false,
	}
	tools := map[string]bool{}
	for _, v := range verbs {
		tools[v.Tool] = true
		if _, ok := want[v.Key()]; ok {
			want[v.Key()] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("docs/CLI.md declares %q and the parser missed it", key)
		}
	}
	if len(tools) < 10 {
		t.Fatalf("found verbs for %d tools, want at least 10", len(tools))
	}
}
