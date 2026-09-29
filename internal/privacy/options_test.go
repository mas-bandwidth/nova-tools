package privacy_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

func TestOptionsNamingNothingAreRefused(t *testing.T) {
	t.Parallel()
	if _, err := (privacy.Options{}).Spec(); !errors.Is(err, privacy.ErrNoCorpus) {
		t.Errorf("err %v, want ErrNoCorpus", err)
	}
}

func TestOptionsResolveTheRootConfiguration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	s, err := privacy.Options{Root: f.root}.Spec()
	if err != nil {
		t.Fatal(err)
	}
	if s.Config != f.root+string(filepath.Separator)+privacy.ConfigName || len(s.Sources) != 2 || len(s.Roots) != 2 {
		t.Errorf("spec %+v", s)
	}
	if s.Sources[0].Path != f.path("private/later.md") || s.Sources[0].FromFlag {
		t.Errorf("source %+v", s.Sources[0])
	}
}

func TestOptionsRefuseEachBrokenConfiguration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	empty := t.TempDir()
	f.write(t, "nosource.conf", "background flat *.md journal\n")
	f.write(t, "bad.conf", "source a.md\nnonsense here\n")
	f.write(t, "big.conf", strings.Repeat("#", privacy.MaxConfigBytes+1))
	for name, c := range map[string]struct {
		o    privacy.Options
		want string
	}{
		"root and config":      {privacy.Options{Root: f.root, Config: f.path(privacy.ConfigName)}, "give one"},
		"no file under root":   {privacy.Options{Root: empty}, "no configuration at"},
		"named file missing":   {privacy.Options{Config: filepath.Join(empty, "x.conf")}, "no configuration at"},
		"no source":            {privacy.Options{Config: f.path("nosource.conf")}, "declares no source"},
		"malformed":            {privacy.Options{Config: f.path("bad.conf")}, "line 2:"},
		"over its bound":       {privacy.Options{Config: f.path("big.conf")}, "MaxConfigBytes"},
		"bad pattern":          {privacy.Options{Root: f.root, Pattern: "["}, "--pattern"},
		"negative max-docs":    {privacy.Options{Root: f.root, MaxDocs: -1}, "--max-docs"},
		"negative max-bytes":   {privacy.Options{Root: f.root, MaxBytes: -1}, "--max-bytes"},
		"a directory for file": {privacy.Options{Config: empty}, "is a directory"},
	} {
		if _, err := c.o.Spec(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestOptionFlagsReplaceTheConfiguration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	s, err := privacy.Options{
		Root:     f.root,
		Sources:  []string{"elsewhere.md"},
		Flat:     []string{"j"},
		Marker:   "[hush]",
		MaxDocs:  7,
		MaxBytes: 4096,
	}.Spec()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sources) != 1 || s.Sources[0].Path != "elsewhere.md" || !s.Sources[0].FromFlag {
		t.Errorf("sources %+v", s.Sources)
	}
	if len(s.Roots) != 1 || s.Roots[0].Dir != "j" || s.Roots[0].Recursive || s.Roots[0].Pattern != privacy.DefaultPattern {
		t.Errorf("roots %+v", s.Roots)
	}
	if s.Rules.Marker != "[hush]" || s.MaxDocs != 7 || s.MaxBytes != 4096 {
		t.Errorf("marker %q max-docs %d max-bytes %d", s.Rules.Marker, s.MaxDocs, s.MaxBytes)
	}
}

// Sources named by option need no configuration when no root is named.
func TestSourcesByOptionNeedNoConfiguration(t *testing.T) {
	t.Parallel()
	s, err := privacy.Options{Sources: []string{"a.md"}}.Spec()
	if err != nil || s.Config != "" || len(s.Sources) != 1 {
		t.Errorf("spec %+v err %v", s, err)
	}
}

// A root named explicitly must hold its configuration, even beside --source:
// a misspelt root would otherwise drop the refuse shapes, the marker, the stop
// words and the background without a word.
func TestANamedRootWithNoConfigurationIsRefusedEvenWithSources(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "typo")
	_, err := privacy.Options{Root: missing, Sources: []string{"a.md"}}.Spec()
	if err == nil || !strings.Contains(err.Error(), "no configuration at") || !strings.Contains(err.Error(), "typo") {
		t.Errorf("err %v, want a refusal naming the root's configuration", err)
	}
}

// Two sources that are one file would double every count in it, and a term
// at the rarity bound would fall out of the fingerprints. They are refused at
// load, by name, however the path is spelled.
func TestSourcesThatAreOneFileAreRefusedByName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	if err := os.Symlink(f.path("private/later.md"), f.path("private/linked.md")); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]string{
		"the same line twice": "source private/later.md\nsource private/later.md\n",
		"a second spelling":   "source private/later.md\nsource private/./later.md\n",
		"a symlink":           "source private/later.md\nsource private/linked.md\n",
	} {
		f.write(t, name+".conf", cfg)
		_, err := privacy.Options{Config: f.path(name + ".conf")}.Spec()
		if err == nil || !strings.Contains(err.Error(), "same file") || !strings.Contains(err.Error(), "private/later.md") {
			t.Errorf("%s: err %v, want a refusal naming both sources", name, err)
		}
	}
	_, err := privacy.Options{Sources: []string{f.path("private/later.md"), f.path("private/linked.md")}}.Spec()
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Errorf("by flag: err %v", err)
	}
}

// A Spec built in process with one file twice is caught by Load as well: the
// second row is unreadable, so the screen does not clear.
func TestLoadRefusesOneFileDeclaredTwice(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	spec := f.spec
	spec.Sources = append(spec.Sources, privacy.SourceSpec{Path: f.path("private/./later.md"), Display: "again.md"})
	r := privacy.Screen(spec, harmless)
	if r.Outcome != privacy.CorpusUnreadable || !strings.Contains(r.Reason, "again.md") || !strings.Contains(r.Reason, "same file as private/later.md") {
		t.Errorf("outcome %s reason %q", r.Outcome, r.Reason)
	}
}
