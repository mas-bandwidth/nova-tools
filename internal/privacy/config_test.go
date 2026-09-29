package privacy_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

func TestEveryKeywordParses(t *testing.T) {
	t.Parallel()
	f, err := privacy.ParseConfig(strings.Join([]string{
		"# comment",
		"",
		"source private/later.md",
		"source\tnotes with blanks/a.md",
		"background recursive *.md .",
		"background flat *.txt journal dir",
		"marker [secret]",
		"entry *",
		"entry ##",
		"stop lantern harbour",
		"refuse home-path /home/[a-z]+ x",
		"warn tool \\bacme-[a-z]+",
		"allow acme-docs",
		"max-docs 12",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Sources, []string{"private/later.md", "notes with blanks/a.md"}) {
		t.Errorf("sources %q", f.Sources)
	}
	want := []privacy.Root{{Dir: ".", Recursive: true, Pattern: "*.md"}, {Dir: "journal dir", Recursive: false, Pattern: "*.txt"}}
	if !reflect.DeepEqual(f.Roots, want) {
		t.Errorf("roots %+v", f.Roots)
	}
	if f.Marker != "[secret]" || !reflect.DeepEqual(f.EntryTokens, []string{"*", "##"}) || f.MaxDocs != 12 {
		t.Errorf("marker %q entries %q max-docs %d", f.Marker, f.EntryTokens, f.MaxDocs)
	}
	if !reflect.DeepEqual(f.Stop, []string{"lantern", "harbour"}) || !reflect.DeepEqual(f.Allow, []string{"acme-docs"}) {
		t.Errorf("stop %q allow %q", f.Stop, f.Allow)
	}
	if len(f.Refuse) != 1 || f.Refuse[0].Class != "home-path" || f.Refuse[0].RE.String() != "/home/[a-z]+ x" {
		t.Errorf("refuse %+v", f.Refuse)
	}
	if len(f.Warn) != 1 || f.Warn[0].Class != "tool" {
		t.Errorf("warn %+v", f.Warn)
	}
}

// One run names every malformed line, each with its number.
func TestEveryMalformedLineIsReported(t *testing.T) {
	t.Parallel()
	_, err := privacy.ParseConfig(strings.Join([]string{
		"source",                     // 1
		"background sideways *.md x", // 2
		"background flat [ x",        // 3
		"marker (private)",           // 4
		"marker (secret)",            // 5
		"entry a b",                  // 6
		"stop",                       // 7
		"refuse onlyaclass",          // 8
		"warn broken (unclosed",      // 9
		"allow",                      // 10
		"max-docs zero",              // 11
		"sources private/later.md",   // 12
		"source private/later.md",    // 13, fine
		"background recursive *.md",  // 14
		"max-docs 0",                 // 15
	}, "\n"))
	if err == nil {
		t.Fatal("malformed configuration accepted")
	}
	for _, n := range []string{"line 1:", "line 2:", "line 3:", "line 5:", "line 6:", "line 7:", "line 8:", "line 9:", "line 10:", "line 11:", "line 12:", "line 14:", "line 15:"} {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("%q is not reported in:\n%v", n, err)
		}
	}
	for _, n := range []string{"line 4:", "line 13:"} {
		if strings.Contains(err.Error(), n) {
			t.Errorf("%q is well formed and was reported:\n%v", n, err)
		}
	}
	if !strings.Contains(err.Error(), "line 4 already names one") {
		t.Errorf("the second marker names the first: %v", err)
	}
}

func TestRelativePathsResolveAgainstTheConfigurationDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	abs := filepath.Join(base, "elsewhere", "b.md")
	f, err := privacy.ParseConfig("source private/a.md\nsource " + abs + "\nbackground flat *.md journal\n")
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.Resolve(base, "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if s.Sources[0].Path != filepath.Join(base, "private", "a.md") || s.Sources[0].Display != "private/a.md" {
		t.Errorf("relative source %+v", s.Sources[0])
	}
	if s.Sources[1].Path != abs {
		t.Errorf("absolute source %+v", s.Sources[1])
	}
	if s.Roots[0].Dir != filepath.Join(base, "journal") || s.Roots[0].Display != "journal" || s.Roots[0].Mode() != "flat" {
		t.Errorf("root %+v", s.Roots[0])
	}
	if s.MaxDocs != privacy.DefaultMaxDocs || s.Config != "cfg" || s.Rules.Marker != privacy.DefaultMarker {
		t.Errorf("defaults: max-docs %d config %q marker %q", s.MaxDocs, s.Config, s.Rules.Marker)
	}
}

func TestABadEntryTokenFailsToResolve(t *testing.T) {
	t.Parallel()
	f := privacy.File{EntryTokens: []string{"a b"}}
	if _, err := f.Resolve(t.TempDir(), "cfg"); err == nil {
		t.Error("an entry token with a blank resolved")
	}
}
