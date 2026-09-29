package privacy_test

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
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

// sampleKey is the documented sample order: FNV-1a 64 of the root as the
// configuration writes it, a slash, and the path relative to the root.
func sampleKey(root, rel string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(root + "/" + rel))
	return h.Sum64()
}

// Over a bound, the sample is the documents with the lowest hash of their
// relative path: it follows neither names nor dates, it is the same every
// run, and the output says how many were found, how many read, and why.
func TestTheSampleIsTheLowestPathHashesAndSaysSo(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	var rels []string
	for i := 0; i < 6; i++ {
		for _, dir := range []string{"a", "b"} {
			rel := fmt.Sprintf("%s/page-%d.md", dir, i)
			f.write(t, "notes/"+rel, fmt.Sprintf("word%s%d", dir, i))
			rels = append(rels, rel)
		}
	}
	sort.Slice(rels, func(i, j int) bool { return sampleKey("notes", rels[i]) < sampleKey("notes", rels[j]) })
	s := rootSpec(t, f, "source private/later.md\nbackground recursive *.md notes\nmax-docs 4\n")
	c := privacy.Load(s)
	if c.BackgroundDocs != 4 || c.BackgroundFound != 12 || c.Roots[0].Found != 12 || c.Roots[0].Read != 4 || c.SampleRule != privacy.SampleLowestHash {
		t.Fatalf("docs %d found %d rule %q root %+v, want 4 of 12 by the lowest hash", c.BackgroundDocs, c.BackgroundFound, c.SampleRule, c.Roots[0])
	}
	for i, rel := range rels {
		word := "word" + strings.TrimSuffix(strings.ReplaceAll(rel, "/page-", ""), ".md")
		if got, want := c.BackgroundFreq[word], i < 4; (got == 1) != want {
			t.Errorf("%s (rank %d): read=%v, want %v", rel, i, got == 1, want)
		}
	}
	w := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"found 12", "reads 4", "lowest hash of their relative path", "same every run"} {
		if !strings.Contains(w, want) {
			t.Errorf("the warning lacks %q: %s", want, w)
		}
	}
	again := privacy.Load(s)
	if fmt.Sprint(again.BackgroundFreq) != fmt.Sprint(c.BackgroundFreq) {
		t.Error("two loads read two different samples")
	}
}

// The byte bound takes documents in the same order until the next one would
// pass it.
func TestTheByteBoundTakesTheLowestHashesThatFit(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	var rels []string
	for i := 0; i < 10; i++ {
		base := fmt.Sprintf("p%d", i)
		f.write(t, "notes/"+base+".md", "word"+base+" "+strings.Repeat("x", 100-len(base)-5))
		rels = append(rels, base+".md")
	}
	sort.Slice(rels, func(i, j int) bool { return sampleKey("notes", rels[i]) < sampleKey("notes", rels[j]) })
	c := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground flat *.md notes\nmax-bytes 350\n"))
	if c.BackgroundDocs != 3 || c.BackgroundBytes != 300 || c.SampleRule != privacy.SampleLowestHash {
		t.Fatalf("docs %d bytes %d rule %q, want the three lowest that fit in 350 bytes", c.BackgroundDocs, c.BackgroundBytes, c.SampleRule)
	}
	for i, rel := range rels {
		word := "word" + strings.TrimSuffix(rel, ".md")
		if got, want := c.BackgroundFreq[word], i < 3; (got == 1) != want {
			t.Errorf("%s (rank %d): read=%v, want %v", rel, i, got == 1, want)
		}
	}
}

// Under both bounds, every document is read and the rule says so.
func TestUnderTheBoundsEveryDocumentIsRead(t *testing.T) {
	t.Parallel()
	c := privacy.Load(newFixture(t, true).spec)
	if c.SampleRule != privacy.SampleAll || c.BackgroundDocs != c.BackgroundFound {
		t.Errorf("rule %q docs %d found %d", c.SampleRule, c.BackgroundDocs, c.BackgroundFound)
	}
	if privacy.DefaultMaxDocs < 5000 || privacy.DefaultMaxBytes < 5000*8<<10 {
		t.Errorf("the defaults read 5,000 documents of ordinary size whole: max-docs %d max-bytes %d", privacy.DefaultMaxDocs, privacy.DefaultMaxBytes)
	}
}

// A document reachable from two roots is one document: the fixture's
// journal is under the recursive root and is a flat root of its own.
func TestADocumentUnderTwoRootsIsCountedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	for i := 0; i < 8; i++ {
		f.write(t, fmt.Sprintf("journal/page-%03d.md", i), "Ordinary prose and a journalword.\n")
	}
	c := privacy.Load(f.spec)
	if c.BackgroundDocs != 10 || c.Roots[0].Read != 10 || c.Roots[1].Found != 8 || c.Roots[1].Shared != 8 || c.Roots[1].Read != 0 {
		t.Errorf("docs %d roots %+v, want the journal counted once, under the first root", c.BackgroundDocs, c.Roots)
	}
	if n := c.BackgroundFreq["journalword"]; n != 8 {
		t.Errorf("a journal word is in %d documents, want 8", n)
	}
}

// The file pattern matches names case-insensitively.
func TestThePatternIgnoresCase(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	f.write(t, "journal/PAGE-0.MD", "upperword")
	f.write(t, "journal/page-1.Md", "mixedword")
	c := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground flat *.md journal\n"))
	if c.BackgroundDocs != 2 {
		t.Errorf("docs %d, want both names matched", c.BackgroundDocs)
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

// A recursive root that is a symlink to a directory is followed, once: the
// documents under it are read, and a second root reaching the same files
// through the link counts none of them again.
func TestARecursiveRootThatIsASymlinkIsFollowedOnce(t *testing.T) {
	t.Parallel()
	f := &fixture{root: t.TempDir()}
	f.write(t, "private/later.md", "## A secret (private)\n"+rareWords+"\n")
	f.write(t, "real/notes/a.md", "alphaword")
	f.write(t, "real/notes/b.md", "bravoword")
	if err := os.Symlink(f.path("real/notes"), f.path("linked")); err != nil {
		t.Fatal(err)
	}
	c := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground recursive *.md linked\nbackground recursive *.md real\n"))
	if c.BackgroundDocs != 2 || c.Roots[0].Read != 2 || c.Roots[1].Shared != 2 || c.Roots[1].Read != 0 {
		t.Errorf("docs %d roots %+v, want the two documents read once, through the link", c.BackgroundDocs, c.Roots)
	}
}

// A root that is a file is refused where it is named, and a Spec built in
// process with one reads nothing from it and says so.
func TestARootThatIsAFileIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "notes.md", "a single document")
	for _, cfg := range []string{"background recursive *.md notes.md\n", "background flat *.md notes.md\n"} {
		f.write(t, "file-root.conf", "source private/later.md\n"+cfg)
		if _, err := (privacy.Options{Config: f.path("file-root.conf")}).Spec(); err == nil || !strings.Contains(err.Error(), "notes.md is a file, not a directory") {
			t.Errorf("%q: err %v", cfg, err)
		}
	}
	if _, err := (privacy.Options{Sources: []string{f.path("private/later.md")}, Recursive: []string{f.path("notes.md")}}).Spec(); err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Errorf("by flag: err %v", err)
	}
	c := privacy.Load(rootSpec(t, f, "source private/later.md\nbackground recursive *.md notes.md\n"))
	if c.BackgroundDocs != 0 || c.Roots[0].Err == nil || !strings.Contains(strings.Join(c.Warnings, "\n"), "is a file") {
		t.Errorf("docs %d root %+v warnings %v", c.BackgroundDocs, c.Roots[0], c.Warnings)
	}
}
