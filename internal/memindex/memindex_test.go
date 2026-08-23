package memindex

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

// The grep failure modes this package exists to mechanize away, as fixtures.
// Each specimen hides a phrase from a naive search in a different way: a hard
// wrap with blockquote markers splits it across lines, emphasis markers split
// it mid-phrase, and a function-word difference defeats phrase matching
// entirely. The normalizer must recover the first two; term retrieval must
// recover the third, because the rare nouns carry the score and a preposition
// cannot zero a match.
const (
	wrapSpecimen   = "> the light is not the point. the point is that somebody is awake\n> and watching, and the light is only how that fact travels\n> to a ship."
	emphSpecimen   = "the rule the station runs on: treat every **instrument reading** as a measurement with a date on it."
	fnwordSpecimen = "the anemometer is reliable about the gusts and unreliable about the mean, three winters running."
)

func corpusFS() fstest.MapFS {
	return fstest.MapFS{
		"HANDBOOK.md":       {Data: []byte("# Handbook\n\n" + wrapSpecimen + "\n\nA second paragraph about the relief boat and the eighteen minutes it runs late.")},
		"CHARTER.md":        {Data: []byte("# Charter\n\n" + emphSpecimen + "\n\nWhat the station owes the coast, and what the coast owes the station.")},
		"notes/wind.md":     {Data: []byte("---\nname: wind-log\ntype: measured\n---\n\n" + fnwordSpecimen)},
		"notes/glass.md":    {Data: []byte("---\nname: glazing-care\ntype: reference\n---\n\nsalt haze etches the glazing unless it is washed in daylight with fresh water.\n\nnever bring the brass paste into the lightroom; it scratches optical glass permanently.")},
		"notes/index-a.md":  {Data: []byte("# Index\n\n- [wind-log](wind.md) — the anemometer\n- [glazing-care](glass.md) — the glazing\n")},
		"log/1974-03-11.md": {Data: []byte("# A day\n\nOnshore gale until dark; the diaphone ran for nine hours and the compressor held pressure throughout.")},
	}
}

func build(t *testing.T) *Corpus {
	t.Helper()
	c, err := Build(corpusFS(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

func TestNormalizeRecoversHiddenPhrases(t *testing.T) {
	cases := []struct{ name, src, phrase string }{
		{"wrap+blockquote", wrapSpecimen, "somebody is awake and watching"},
		{"emphasis", emphSpecimen, "every instrument reading as a measurement"},
	}
	for _, tc := range cases {
		if strings.Contains(strings.ToLower(tc.src), tc.phrase) {
			t.Fatalf("%s: the fixture already contains the phrase raw — the test would prove nothing", tc.name)
		}
		if !strings.Contains(Normalize(tc.src), tc.phrase) {
			t.Errorf("%s: Normalize did not recover %q from %q", tc.name, tc.phrase, tc.src)
		}
	}
}

func TestTruncateCutsOnARuneBoundary(t *testing.T) {
	// The port's source sliced bytes; a multi-byte rune straddling the cut
	// produced invalid UTF-8 inside a quoted receipt field.
	s := strings.Repeat("a", 9) + "é" + strings.Repeat("b", 40)
	for n := 5; n < 20; n++ {
		got := Truncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("Truncate(%q, %d) = %q, which is not valid UTF-8", s, n, got)
		}
	}
	if Truncate("short", 99) != "short" {
		t.Error("Truncate must not touch a string already within the limit")
	}
}

func TestBuildClassesAndFrontmatter(t *testing.T) {
	c := build(t)
	var wind *Chunk
	for i := range c.Chunks {
		if c.Chunks[i].File == "notes/wind.md" {
			wind = &c.Chunks[i]
		}
	}
	if wind == nil {
		t.Fatal("notes/wind.md produced no chunks")
	}
	if wind.Class != "notes" {
		t.Errorf("class = %q, want notes", wind.Class)
	}
	if wind.FMName != "wind-log" || wind.FMType != "measured" {
		t.Errorf("frontmatter = %q/%q, want wind-log/measured", wind.FMName, wind.FMType)
	}
	root := 0
	for _, ch := range c.Chunks {
		if ch.Class == "." {
			root++
		}
	}
	if root == 0 {
		t.Error("root files got no '.' class chunks — the corpus must classify itself")
	}
}

// A CRLF file must chunk exactly as its LF twin does. The blank-line split is
// on the literal "\n\n", so before line endings were normalized a CRLF file's
// blank lines ("\r\n\r\n") never split it: the whole file indexed as ONE
// chunk, para addresses collapsed, BM25 length normalization degraded, and
// MinTerms filtering effectively disappeared — silently, behind a green STATS
// line. This repo ships to other lines on other platforms.
func TestBuildChunkingIsLineEndingAgnostic(t *testing.T) {
	body := "---\nname: crlf-twin\ntype: measured\n---\n\n" +
		"the first paragraph names the compressor and the eleven minutes it needs from cold.\n\n" +
		"the second paragraph names the jetty and the eighteen minutes it runs late.\n\n" +
		"the third paragraph names the glazing and the salt haze that etches it.\n"
	lf, err := Build(fstest.MapFS{"twin.md": {Data: []byte(body)}}, nil)
	if err != nil {
		t.Fatalf("LF build: %v", err)
	}
	crlf, err := Build(fstest.MapFS{"twin.md": {Data: []byte(strings.ReplaceAll(body, "\n", "\r\n"))}}, nil)
	if err != nil {
		t.Fatalf("CRLF build: %v", err)
	}
	if len(lf.Chunks) != len(crlf.Chunks) {
		t.Fatalf("CRLF twin indexed as %d chunks, LF twin as %d — a CRLF corpus becomes one giant chunk per file",
			len(crlf.Chunks), len(lf.Chunks))
	}
	// Four: the frontmatter block is itself a paragraph over MinTerms, then
	// the three prose paragraphs. The number is pinned so a regression that
	// stops splitting shows up as one chunk here rather than as a quiet
	// ranking change.
	if len(lf.Chunks) != 4 {
		t.Fatalf("the fixture is meant to hold 4 indexable paragraphs, got %d", len(lf.Chunks))
	}
	for i := range lf.Chunks {
		if lf.Chunks[i].Text != crlf.Chunks[i].Text || lf.Chunks[i].Para != crlf.Chunks[i].Para {
			t.Errorf("chunk %d differs between twins:\n LF: %d %q\nCRLF: %d %q",
				i, lf.Chunks[i].Para, lf.Chunks[i].Text, crlf.Chunks[i].Para, crlf.Chunks[i].Text)
		}
	}
	// Frontmatter is read from the same normalized text, so a CRLF file's
	// name: reaches receipts too.
	if crlf.Chunks[0].FMName != "crlf-twin" {
		t.Errorf("CRLF frontmatter name = %q, want crlf-twin", crlf.Chunks[0].FMName)
	}
}

func TestBuildRefusesEmptyCorpus(t *testing.T) {
	if _, err := Build(fstest.MapFS{"a.txt": {Data: []byte("no markdown here")}}, nil); err == nil {
		t.Fatal("Build accepted a corpus with no markdown — the confident-zero engine this tool exists to remove")
	}
}

func TestBuildRefusesCorpusWithNoIndexableParagraph(t *testing.T) {
	if _, err := Build(fstest.MapFS{"a.md": {Data: []byte("# h\n\nok\n")}}, nil); err == nil {
		t.Fatal("Build accepted a corpus whose every paragraph is below MinTerms")
	}
}

func TestBuildHonoursExclude(t *testing.T) {
	c, err := Build(corpusFS(), func(p string) bool { return strings.HasPrefix(p, "notes") })
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, f := range c.Files {
		if strings.HasPrefix(f, "notes/") {
			t.Errorf("excluded path %s was indexed anyway", f)
		}
	}
}

func TestBM25FindsFunctionWordVariant(t *testing.T) {
	// A phrase grep for "reliable on the gusts" finds nothing against a file
	// saying "reliable about the gusts". Term retrieval must not care which
	// preposition was used.
	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "the anemometer is reliable on the gusts and unreliable on the mean", 3)
	if len(hits) == 0 || hits[0].File != "notes/wind.md" {
		t.Fatalf("function-word variant did not retrieve notes/wind.md first; hits=%+v", hits)
	}
}

func TestBM25FindsWrappedPhrase(t *testing.T) {
	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "somebody is awake and watching and the light is how that travels", 3)
	if len(hits) == 0 || hits[0].File != "HANDBOOK.md" {
		t.Fatalf("wrapped phrase did not retrieve HANDBOOK.md first; hits=%+v", hits)
	}
}

func TestRetrieveDeterministic(t *testing.T) {
	// Two independent builds, same queries, byte-identical formatted output.
	// Go randomizes map iteration; this is the test that proves no map order
	// leaks into a result.
	format := func(c *Corpus) string {
		var b strings.Builder
		for _, q := range []string{
			"instrument reading measurement", "diaphone compressor pressure",
			"salt haze glazing daylight", "somebody awake and watching",
		} {
			for _, h := range Retrieve(c, []Channel{NewBM25(c), NewTrigram(c)}, q, 5) {
				fmt.Fprintf(&b, "%s|%s|%d|%.9f|%.9f\n", q, h.File, h.Para, h.Fused, h.Native)
			}
		}
		return b.String()
	}
	a := format(build(t))
	bOut := format(build(t))
	if a != bOut {
		t.Fatalf("two builds produced different results:\n--- a ---\n%s--- b ---\n%s", a, bOut)
	}
}

func TestRetrieveNeverEmptyForInVocabularyQuery(t *testing.T) {
	// An unrelated but in-vocabulary query still returns the best k with
	// visible low scores — never a bare zero. Only a fully out-of-vocabulary
	// query may return nothing, and the CLI says so in words when it does.
	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "the boat and the coast and the water", 5)
	if len(hits) == 0 {
		t.Fatal("expected low-score hits for an in-vocabulary unrelated query, got none")
	}
}

func TestRetrieveEmptyForOutOfVocabularyQuery(t *testing.T) {
	c := build(t)
	if hits := Retrieve(c, []Channel{NewBM25(c)}, "zzqq xxvv wwjj", 5); len(hits) != 0 {
		t.Fatalf("out-of-vocabulary query returned hits: %+v", hits)
	}
}

func TestRetrieveRefusesNonPositiveK(t *testing.T) {
	c := build(t)
	for _, k := range []int{0, -1} {
		if hits := Retrieve(c, []Channel{NewBM25(c)}, "glazing", k); hits != nil {
			t.Errorf("k=%d returned %d hits; a non-positive budget is not unlimited", k, len(hits))
		}
	}
}

func TestSingleChannelOrderIsChannelOrder(t *testing.T) {
	c := build(t)
	raw := NewBM25(c).Query("brass paste scratches optical glass", 10)
	fused := Retrieve(c, []Channel{NewBM25(c)}, "brass paste scratches optical glass", 10)
	if len(raw) == 0 || len(fused) == 0 {
		t.Fatal("no results")
	}
	if c.Chunks[raw[0].Chunk].File != fused[0].File {
		t.Errorf("single-channel fusion reordered results: raw top %s, fused top %s",
			c.Chunks[raw[0].Chunk].File, fused[0].File)
	}
}

// The multi-channel receipt artifact. `native` used to be filled from the
// FIRST channel's deep list only, so a chunk that reached the fused top-k
// through the second channel alone carried Native=0 — and a reader comparing
// that 0.00 against the calibration band concludes the hit is weaker than
// unrelated control text, when the number is really an artifact of which
// channel surfaced it. Every hit must now carry a score some channel actually
// computed, and name that channel.
func TestNativeScoreComesFromTheChannelThatSurfacedTheChunk(t *testing.T) {
	c := build(t)
	// "anemometers" is out of vocabulary (the corpus says "anemometer"), so
	// bm25 cannot reach notes/wind.md at all; trigram can. "glazing" is in
	// vocabulary, so bm25 reaches notes/glass.md. One query, two channels,
	// two different surfacing channels.
	hits := Retrieve(c, []Channel{NewBM25(c), NewTrigram(c)}, "anemometers glazing", 10)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	byFile := map[string]FileHit{}
	for _, h := range hits {
		if h.NativeChan == "" {
			t.Errorf("%s carries no surfacing channel — its score=%.2f is unattributable", h.File, h.Native)
		}
		byFile[h.File] = h
	}
	wind, ok := byFile["notes/wind.md"]
	if !ok {
		t.Fatalf("the trigram-only file did not surface at all; hits=%+v", hits)
	}
	if wind.NativeChan != "trigram" {
		t.Errorf("notes/wind.md surfaced through %q, want trigram", wind.NativeChan)
	}
	if wind.Native <= 0 {
		t.Errorf("notes/wind.md printed a fabricated score %.4f — the artifact this test exists for", wind.Native)
	}
	glass, ok := byFile["notes/glass.md"]
	if !ok {
		t.Fatalf("the bm25 file did not surface at all; hits=%+v", hits)
	}
	if glass.NativeChan != "bm25" {
		t.Errorf("notes/glass.md surfaced through %q, want bm25 (the first channel that scored it)", glass.NativeChan)
	}
	// Naming the channels in the other order must move the attribution, never
	// silently keep the first-listed one.
	rev := Retrieve(c, []Channel{NewTrigram(c), NewBM25(c)}, "anemometers glazing", 10)
	for _, h := range rev {
		if h.File == "notes/glass.md" && h.NativeChan != "trigram" {
			t.Errorf("with trigram named first, notes/glass.md still reported %q", h.NativeChan)
		}
	}
}

// With one channel there is only one possible attribution, and every hit must
// carry it: single-channel output is exactly that channel's opinion.
func TestSingleChannelNativeIsThatChannel(t *testing.T) {
	c := build(t)
	for _, ch := range []Channel{NewBM25(c), NewTrigram(c)} {
		for _, h := range Retrieve(c, []Channel{ch}, "salt haze glazing daylight", 5) {
			if h.NativeChan != ch.Name() {
				t.Errorf("channel %s: %s reported score-channel %q", ch.Name(), h.File, h.NativeChan)
			}
		}
	}
}

func TestCoverage(t *testing.T) {
	fsys := corpusFS()
	// Clean case: both notes are named in index-a.md.
	fnds, err := Coverage(fsys, "notes/*.md", "notes/index-*.md")
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	for _, f := range fnds {
		t.Errorf("unexpected finding on a clean corpus: %s: %s", f.Kind, f.Detail)
	}
	// Prove it can fail, in both directions: an orphan note nothing points
	// at, and an index line pointing at nothing.
	fsys["notes/orphan.md"] = &fstest.MapFile{Data: []byte("---\nname: orphan\n---\n\nan undistilled lesson that no index names at all")}
	fsys["notes/index-a.md"] = &fstest.MapFile{Data: []byte("# Index\n\n- [wind-log](wind.md)\n- [glazing-care](glass.md)\n- [gone](gone.md)\n")}
	fnds, err = Coverage(fsys, "notes/*.md", "notes/index-*.md")
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	var haveOrphan, haveDangling bool
	for _, f := range fnds {
		if f.Kind == "coverage" && strings.Contains(f.Detail, "orphan") {
			haveOrphan = true
		}
		if f.Kind == "backlink" && strings.Contains(f.Detail, "gone.md") {
			haveDangling = true
		}
	}
	if !haveOrphan {
		t.Error("planted orphan not found — the check cannot fail, so its pass means nothing")
	}
	if !haveDangling {
		t.Error("planted dangling index link not found")
	}
}

// The planted fault the old regex waved through green. `\]\(([^)#?:]+\.md)\)`
// excluded '#' and '?' from the WHOLE target, so an anchored link never
// matched the pattern at all and was never checked — a dangling
// `](gone.md#top)` in a B-side index passed the coverage wall with exit 0.
// nova-check's own links check had cut the anchor before resolving since it
// was written; this is that behaviour, in the other binary.
func TestCoverageChecksAnchoredAndQueriedLinks(t *testing.T) {
	cases := []struct {
		name    string
		link    string
		wantBad bool
	}{
		{"a dangling anchored link", "[gone anchored](gone.md#top)", true},
		{"a dangling queried link", "[gone queried](gone.md?v=2)", true},
		{"a dangling link with anchor and query", "[gone both](gone.md?v=2#top)", true},
		{"a dangling link carrying a title", `[gone titled](gone.md "the page")`, true},
		{"a dangling angle-bracketed link", "[gone bracketed](<gone.md>)", true},
		{"a resolving anchored link", "[wind](wind.md#the-anemometer)", false},
		{"a resolving queried link", "[wind](wind.md?raw=1)", false},
		{"an http link that happens to end .md", "[remote](https://example.com/gone.md)", false},
		{"a protocol-relative link", "[remote](//example.com/gone.md)", false},
		{"a fragment-only link", "[here](#somewhere)", false},
		{"a root-absolute link", "[root](/gone.md)", false},
		{"a non-markdown target", "[image](gone.png)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := corpusFS()
			fsys["notes/index-a.md"] = &fstest.MapFile{
				Data: []byte("# Index\n\n- [wind-log](wind.md)\n- [glazing-care](glass.md)\n- " + tc.link + "\n"),
			}
			fnds, err := Coverage(fsys, "notes/*.md", "notes/index-*.md")
			if err != nil {
				t.Fatalf("Coverage: %v", err)
			}
			var bad bool
			for _, f := range fnds {
				if f.Kind == "backlink" {
					bad = true
				}
			}
			if bad != tc.wantBad {
				t.Errorf("backlink finding = %v, want %v; findings: %+v", bad, tc.wantBad, fnds)
			}
		})
	}
}

func TestCoverageRefusesEmptySide(t *testing.T) {
	if _, err := Coverage(corpusFS(), "nothing/*.md", "notes/index-*.md"); err == nil {
		t.Error("Coverage accepted an empty A side — a broken check reported as a pass")
	}
	if _, err := Coverage(corpusFS(), "notes/*.md", "nothing/index-*.md"); err == nil {
		t.Error("Coverage accepted an empty B side — a broken check reported as a pass")
	}
}

func TestWikilinks(t *testing.T) {
	fsys := corpusFS()
	fsys["notes/linked.md"] = &fstest.MapFile{Data: []byte("---\nname: linked\n---\n\nsee [[wind-log]] and the unwritten [[storm-glass]] page for the rest")}
	c, err := Build(fsys, nil)
	if err != nil {
		t.Fatal(err)
	}
	fnds, err := Wikilinks(fsys, c)
	if err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, f := range fnds {
		if strings.Contains(f.Detail, "storm-glass") {
			hit = true
		}
		if strings.Contains(f.Detail, "[[wind-log]]") {
			t.Errorf("a link that resolves through frontmatter was reported unresolved: %s", f.Detail)
		}
	}
	if !hit {
		t.Error("unresolved wikilink not reported — the check cannot fail")
	}
}

// The aliased and heading forms are the two commonest wikilink shapes, and
// the old regex excluded '|' and '#' from the body, so neither was ever
// SCANNED — a caller who chose --links=gate as a wall got the whole class
// waved through. Both arms matter: the dangling target must be reported and
// the resolving one must not, or the fix trades a hole for a false-positive
// gate.
func TestWikilinksScansAliasedAndHeadingForms(t *testing.T) {
	fsys := corpusFS()
	fsys["notes/linked.md"] = &fstest.MapFile{Data: []byte(
		"---\nname: linked\n---\n\n" +
			"see [[nowhere-page|the missing page]] and [[nowhere-sec#readings]] for what is not written,\n" +
			"beside [[wind-log|the anemometer log]] and [[glazing-care#the-brass]] which both resolve,\n" +
			"and [[#a-heading-on-this-page]] which names nothing in the corpus at all.\n")}
	c, err := Build(fsys, nil)
	if err != nil {
		t.Fatal(err)
	}
	fnds, err := Wikilinks(fsys, c)
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]bool{}
	for _, f := range fnds {
		reported[f.Detail] = true
	}
	has := func(stem string) bool {
		for d := range reported {
			if strings.Contains(d, "[["+stem+"]]") {
				return true
			}
		}
		return false
	}
	for _, stem := range []string{"nowhere-page", "nowhere-sec"} {
		if !has(stem) {
			t.Errorf("dangling wikilink [[%s]] not reported — the gate has a hole; findings: %+v", stem, fnds)
		}
	}
	for _, stem := range []string{"wind-log", "glazing-care"} {
		if has(stem) {
			t.Errorf("a wikilink that resolves was reported unresolved: [[%s]]; findings: %+v", stem, fnds)
		}
	}
	// A body that is only a heading is a same-page anchor, not a corpus
	// reference: reporting it would gate on something no corpus can satisfy.
	for d := range reported {
		if strings.Contains(d, "a-heading-on-this-page") {
			t.Errorf("a heading-only wikilink was reported as a corpus reference: %s", d)
		}
	}
}

func TestFrontmatterPresent(t *testing.T) {
	fnds, err := FrontmatterPresent(corpusFS(), "notes/wind.md", nil)
	if err != nil || len(fnds) != 0 {
		t.Fatalf("a file with frontmatter was flagged: %v %v", fnds, err)
	}
	fsys := corpusFS()
	fsys["notes/bare.md"] = &fstest.MapFile{Data: []byte("no frontmatter at all, just a paragraph of prose")}
	fnds, err = FrontmatterPresent(fsys, "notes/bare.md", nil)
	if err != nil || len(fnds) != 1 {
		t.Fatalf("a bare file was not flagged exactly once: %v %v", fnds, err)
	}
}

// The no-defaults law applied to scope: the tool this was ported from
// hardcoded the "index-" prefix as exempt, which is a guess about someone
// else's filenames. Nothing is exempt unless the caller says so, this run.
func TestFrontmatterExemptionIsTheCallersAndNeverADefault(t *testing.T) {
	fsys := corpusFS()
	fnds, err := FrontmatterPresent(fsys, "notes/index-a.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fnds) != 1 {
		t.Fatalf("index-a.md must be scanned when no exemption is stated, got %d findings — a default skip list has returned", len(fnds))
	}
	fnds, err = FrontmatterPresent(fsys, "notes/index-a.md", []string{"index-"})
	if err != nil || len(fnds) != 0 {
		t.Fatalf("the caller's stated exemption was not honoured: %v %v", fnds, err)
	}
}

func TestFrontmatterRefusesEmptyGlob(t *testing.T) {
	if _, err := FrontmatterPresent(corpusFS(), "nothing/*.md", nil); err == nil {
		t.Error("FrontmatterPresent accepted a glob matching nothing — a broken check reported as a pass")
	}
}

// TestFrontmatterToleratesCRLF pins a real defect rather than a fixture one. A
// checkout on Windows produces CRLF, and the fence check was a literal
// "---\n", so every entry in a CRLF corpus reported "no name: in frontmatter" —
// a line-ending fault presenting as a corpus fault, in a tool other people run
// against their own corpora. The repo's own fixtures are held to LF by
// .gitattributes, so only a test like this one can cover the user's file.
func TestFrontmatterToleratesCRLF(t *testing.T) {
	lf := "---\nname: lantern\ntype: reference\n---\n\nbody\n"
	crlf := "---\r\nname: lantern\r\ntype: reference\r\n---\r\n\r\nbody\r\n"
	wantName, wantType := frontmatter(lf)
	if wantName != "lantern" || wantType != "reference" {
		t.Fatalf("LF baseline broken: name=%q type=%q", wantName, wantType)
	}
	gotName, gotType := frontmatter(crlf)
	if gotName != wantName || gotType != wantType {
		t.Errorf("CRLF: name=%q type=%q, want %q/%q", gotName, gotType, wantName, wantType)
	}
}
