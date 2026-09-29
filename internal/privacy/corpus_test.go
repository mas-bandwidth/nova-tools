package privacy_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

func rootSpec(t *testing.T, f *fixture, cfg string) privacy.Spec {
	t.Helper()
	file, err := privacy.ParseConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := file.Resolve(f.root, privacy.ConfigName)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Over the cap, the sample is the first max-docs in walk order, the same
// every run, and the warning says which ones.
func TestTheBackgroundCapTakesADeterministicSampleAndSaysSo(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	for i := 0; i < 6; i++ {
		f.write(t, fmt.Sprintf("notes/b/page-%d.md", i), fmt.Sprintf("word%d", i))
		f.write(t, fmt.Sprintf("notes/a/page-%d.md", i), "alphaword")
	}
	s := rootSpec(t, f, "source private/later.md\nbackground recursive *.md notes\nmax-docs 4\n")
	c := privacy.Load(s)
	if c.BackgroundDocs != 4 || c.Roots[0].Found != 12 || c.Roots[0].Read != 4 {
		t.Fatalf("docs %d root %+v, want 4 of 12", c.BackgroundDocs, c.Roots[0])
	}
	if c.BackgroundFreq["alphaword"] != 4 {
		t.Errorf("the sample is the first four in walk order, all under notes/a: %v", c.BackgroundFreq)
	}
	w := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"holds 12 matching documents", "max-docs is 4", "walk order", "same sample every run"} {
		if !strings.Contains(w, want) {
			t.Errorf("the warning lacks %q: %s", want, w)
		}
	}
	again := privacy.Load(s)
	if fmt.Sprint(again.BackgroundFreq) != fmt.Sprint(c.BackgroundFreq) {
		t.Error("two loads read two different samples")
	}
}

func TestAFlatRootDoesNotDescendAndARecursiveOneDoes(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	f.write(t, "journal/top.md", "topword")
	f.write(t, "journal/deeper/under.md", "underword")
	f.write(t, "journal/skip.txt", "textword")
	flat := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground flat *.md journal\n"))
	if flat.BackgroundDocs != 1 || flat.BackgroundFreq["underword"] != 0 || flat.BackgroundFreq["textword"] != 0 {
		t.Errorf("flat read %d docs: %v", flat.BackgroundDocs, flat.BackgroundFreq)
	}
	rec := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground recursive *.md journal\n"))
	if rec.BackgroundDocs != 2 || rec.BackgroundFreq["underword"] != 1 {
		t.Errorf("recursive read %d docs: %v", rec.BackgroundDocs, rec.BackgroundFreq)
	}
}

// A missing background root warns and does not refuse: it makes the screen
// flag more readily, never less.
func TestAMissingBackgroundRootWarnsAndDoesNotRefuse(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	s := f.withConfig(t, "background flat *.md absent\nbackground recursive *.md absent-too\n")
	r := privacy.Screen(s, harmless)
	if r.Outcome != privacy.UnprovenClean {
		t.Errorf("outcome %s, want UNPROVEN-CLEAN", r.Outcome)
	}
	w := strings.Join(r.Warnings, "\n")
	if !strings.Contains(w, "absent cannot be listed") || !strings.Contains(w, "absent-too cannot be walked") {
		t.Errorf("warnings %s", w)
	}
	if r.Roots[2].Err == nil || r.Roots[3].Err == nil {
		t.Errorf("the rows carry the error: %+v", r.Roots)
	}
}

func TestABackgroundDocumentOverItsBoundIsLeftOutByName(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	f.write(t, "journal/big.md", strings.Repeat("y", privacy.MaxDocBytes+1))
	f.write(t, "journal/small.md", "smallword")
	c := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground flat *.md journal\n"))
	if c.BackgroundDocs != 1 || !strings.Contains(strings.Join(c.Warnings, "\n"), "MaxDocBytes") {
		t.Errorf("docs %d warnings %v", c.BackgroundDocs, c.Warnings)
	}
}

func TestThePayloadIsBounded(t *testing.T) {
	t.Parallel()
	if _, err := privacy.ReadPayload(bytes.NewReader(make([]byte, privacy.MaxPayloadBytes)), "stdin"); err != nil {
		t.Errorf("a payload at the bound is read: %v", err)
	}
	_, err := privacy.ReadPayload(bytes.NewReader(make([]byte, privacy.MaxPayloadBytes+1)), "stdin")
	if !errors.Is(err, privacy.ErrTooLarge) || !strings.Contains(err.Error(), fmt.Sprint(privacy.MaxPayloadBytes)) {
		t.Errorf("err %v, want ErrTooLarge naming the bound", err)
	}
}

func TestReadBoundedRefusesADirectory(t *testing.T) {
	t.Parallel()
	if _, err := privacy.ReadBounded(t.TempDir(), 10); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("err %v", err)
	}
}
