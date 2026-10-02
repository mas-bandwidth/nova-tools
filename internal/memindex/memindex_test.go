package memindex

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "Build")
	return c
}

func TestNormalizeRecoversHiddenPhrases(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, src, phrase string }{
		{"wrap+blockquote", wrapSpecimen, "somebody is awake and watching"},
		{"emphasis", emphSpecimen, "every instrument reading as a measurement"},
	}
	for _, tc := range cases {
		require.NotContains(t, strings.ToLower(tc.src), tc.phrase, "%s: the fixture already contains the phrase raw — the test would prove nothing", tc.name)
		assert.Contains(t, Normalize(tc.src), tc.phrase, "%s: Normalize did not recover %q from %q", tc.name, tc.phrase, tc.src)
	}
}

func TestTruncateCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	// The port's source sliced bytes; a multi-byte rune straddling the cut
	// produced invalid UTF-8 inside a quoted receipt field.
	s := strings.Repeat("a", 9) + "é" + strings.Repeat("b", 40)
	for n := 5; n < 20; n++ {
		got := Truncate(s, n)
		require.True(t, utf8.ValidString(got), "Truncate(%q, %d) = %q, which is not valid UTF-8", s, n, got)
	}
	assert.Equal(t, "short", Truncate("short", 99), "Truncate must not touch a string already within the limit")
}

func TestBuildClassesAndFrontmatter(t *testing.T) {
	t.Parallel()

	c := build(t)
	var wind *Chunk
	for i := range c.Chunks {
		if c.Chunks[i].File == "notes/wind.md" {
			wind = &c.Chunks[i]
		}
	}
	require.NotNil(t, wind, "notes/wind.md produced no chunks")
	assert.Equal(t, "notes", wind.Class, "class = %q, want notes", wind.Class)
	assert.Equal(t, [2]string{"wind-log", "measured"}, [2]string{wind.FMName, wind.FMType}, "frontmatter = %q/%q, want wind-log/measured", wind.FMName, wind.FMType)
	root := 0
	for _, ch := range c.Chunks {
		if ch.Class == "." {
			root++
		}
	}
	assert.NotZero(t, root, "root files got no '.' class chunks — the corpus must classify itself")
}

// A CRLF file must chunk exactly as its LF twin does. The blank-line split is
// on the literal "\n\n", so before line endings were normalized a CRLF file's
// blank lines ("\r\n\r\n") never split it: the whole file indexed as ONE
// chunk, para addresses collapsed, BM25 length normalization degraded, and
// MinTerms filtering effectively disappeared — silently, behind a green STATS
// line. This repo ships to other lines on other platforms.
func TestBuildChunkingIsLineEndingAgnostic(t *testing.T) {
	t.Parallel()

	body := "---\nname: crlf-twin\ntype: measured\n---\n\n" +
		"the first paragraph names the compressor and the eleven minutes it needs from cold.\n\n" +
		"the second paragraph names the jetty and the eighteen minutes it runs late.\n\n" +
		"the third paragraph names the glazing and the salt haze that etches it.\n"
	lf, err := Build(fstest.MapFS{"twin.md": {Data: []byte(body)}}, nil)
	require.NoError(t, err, "LF build")
	// Three: the prose paragraphs. The frontmatter block is metadata, never a
	// paragraph. The number is pinned so a regression that stops splitting
	// shows up as one chunk here rather than as a quiet ranking change.
	require.Len(t, lf.Chunks, 3, "the fixture is meant to hold 3 indexable paragraphs, got %d", len(lf.Chunks))
	// The lone-CR twin is not a hypothetical: it is what classic-Mac-era
	// tooling and a few exporters still emit, and it is what a CRLF fix that
	// only replaces "\r\n" leaves behind untouched.
	for _, tw := range []struct{ name, ending string }{
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tw.name, func(t *testing.T) {
			twin, err := Build(fstest.MapFS{"twin.md": {Data: []byte(strings.ReplaceAll(body, "\n", tw.ending))}}, nil)
			require.NoError(t, err, "%s build", tw.name)
			require.Len(t, twin.Chunks, len(lf.Chunks), "%s twin indexed as %d chunks, LF twin as %d — that corpus becomes one giant chunk per file",
				tw.name, len(twin.Chunks), len(lf.Chunks))
			for i := range lf.Chunks {
				assert.Equal(t, [2]any{lf.Chunks[i].Text, lf.Chunks[i].Para}, [2]any{twin.Chunks[i].Text, twin.Chunks[i].Para},
					"chunk %d differs between twins:\n  LF: %d %q\n%4s: %d %q",
					i, lf.Chunks[i].Para, lf.Chunks[i].Text, tw.name, twin.Chunks[i].Para, twin.Chunks[i].Text)
			}
			// Frontmatter is read from the same normalized text, so the twin's
			// name: reaches receipts too.
			assert.Equal(t, "crlf-twin", twin.Chunks[0].FMName, "%s frontmatter name = %q, want crlf-twin", tw.name, twin.Chunks[0].FMName)
		})
	}
}

func TestBuildRefusesEmptyCorpus(t *testing.T) {
	t.Parallel()

	_, err := Build(fstest.MapFS{"a.txt": {Data: []byte("no markdown here")}}, nil)
	require.Error(t, err, "Build accepted a corpus with no markdown — the confident-zero engine this tool exists to remove")
}

func TestBuildRefusesCorpusWithNoIndexableParagraph(t *testing.T) {
	t.Parallel()

	_, err := Build(fstest.MapFS{"a.md": {Data: []byte("# h\n\nok\n")}}, nil)
	require.Error(t, err, "Build accepted a corpus whose every paragraph is below MinTerms")
}

func TestBuildHonoursExclude(t *testing.T) {
	t.Parallel()

	c, err := Build(corpusFS(), func(p string) bool { return strings.HasPrefix(p, "notes") })
	require.NoError(t, err, "Build")
	for _, f := range c.Files {
		assert.False(t, strings.HasPrefix(f, "notes/"), "excluded path %s was indexed anyway", f)
	}
}

func TestBM25FindsFunctionWordVariant(t *testing.T) {
	t.Parallel()

	// A phrase grep for "reliable on the gusts" finds nothing against a file
	// saying "reliable about the gusts". Term retrieval must not care which
	// preposition was used.
	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "the anemometer is reliable on the gusts and unreliable on the mean", 3)
	require.NotEmpty(t, hits, "function-word variant did not retrieve notes/wind.md first; hits=%+v", hits)
	require.Equal(t, "notes/wind.md", hits[0].File, "function-word variant did not retrieve notes/wind.md first; hits=%+v", hits)
}

func TestBM25FindsWrappedPhrase(t *testing.T) {
	t.Parallel()

	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "somebody is awake and watching and the light is how that travels", 3)
	require.NotEmpty(t, hits, "wrapped phrase did not retrieve HANDBOOK.md first; hits=%+v", hits)
	require.Equal(t, "HANDBOOK.md", hits[0].File, "wrapped phrase did not retrieve HANDBOOK.md first; hits=%+v", hits)
}

func TestRetrieveDeterministic(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, a, bOut, "two builds produced different results")
}

func TestRetrieveNeverEmptyForInVocabularyQuery(t *testing.T) {
	t.Parallel()

	// An unrelated but in-vocabulary query still returns the best k with
	// visible low scores — never a bare zero. Only a fully out-of-vocabulary
	// query may return nothing, and the CLI says so in words when it does.
	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "the boat and the coast and the water", 5)
	require.NotEmpty(t, hits, "expected low-score hits for an in-vocabulary unrelated query, got none")
}

func TestRetrieveEmptyForOutOfVocabularyQuery(t *testing.T) {
	t.Parallel()

	c := build(t)
	hits := Retrieve(c, []Channel{NewBM25(c)}, "zzqq xxvv wwjj", 5)
	require.Empty(t, hits, "out-of-vocabulary query returned hits: %+v", hits)
}

// crowdingFS holds one file owning far more matching paragraphs than any
// fixed per-channel chunk headroom, and one other file matching the same
// query. The documented contract is top-k FILES, so k=2 has to reach both.
func crowdingFS() fstest.MapFS {
	var long strings.Builder
	long.WriteString("---\nname: a-long\n---\n")
	for i := 0; i < 100; i++ {
		long.WriteString("\nquasar nebula comet\n")
	}
	return fstest.MapFS{
		"a-long.md": {Data: []byte(long.String())},
		"b-relevant.md": {Data: []byte("---\nname: b-relevant\n---\n\n" +
			"quasar nebula comet astronomy telescope spectrum planet orbital gravity observation\n")},
	}
}

// The unit of the retrieval limit is FILES, not chunks. Each channel was
// asked for max(50, k*10) chunks and only then aggregated per file, so a
// document owning more matching paragraphs than that cap filled every slot
// and every other matching file was truncated away BEFORE it could be
// counted — --k 2 returning one file, silently, with a green receipt line.
func TestRetrieveDoesNotLetOneLongFileCrowdOutOthers(t *testing.T) {
	t.Parallel()

	c, err := Build(crowdingFS(), nil)
	require.NoError(t, err, "Build")
	for _, chans := range [][]Channel{
		{NewBM25(c)},
		{NewBM25(c), NewTrigram(c)},
	} {
		hits := Retrieve(c, chans, "quasar nebula comet", 2)
		var files []string
		for _, h := range hits {
			files = append(files, h.File)
		}
		if !assert.Len(t, hits, 2, "%d channel(s): asked for 2 files, got %d: %v — one long file exhausted the headroom",
			len(chans), len(hits), files) {
			continue
		}
		var haveLong, haveOther bool
		for _, f := range files {
			switch f {
			case "a-long.md":
				haveLong = true
			case "b-relevant.md":
				haveOther = true
			}
		}
		assert.True(t, haveLong && haveOther, "%d channel(s): top-2 files were %v, want both a-long.md and b-relevant.md", len(chans), files)
	}
}

func TestRetrieveRefusesNonPositiveK(t *testing.T) {
	t.Parallel()

	c := build(t)
	for _, k := range []int{0, -1} {
		hits := Retrieve(c, []Channel{NewBM25(c)}, "glazing", k)
		assert.Nil(t, hits, "k=%d returned %d hits; a non-positive budget is not unlimited", k, len(hits))
	}
}

func TestSingleChannelOrderIsChannelOrder(t *testing.T) {
	t.Parallel()

	c := build(t)
	raw := NewBM25(c).Query("brass paste scratches optical glass", 10)
	fused := Retrieve(c, []Channel{NewBM25(c)}, "brass paste scratches optical glass", 10)
	require.NotEmpty(t, raw, "no results")
	require.NotEmpty(t, fused, "no results")
	assert.Equal(t, c.Chunks[raw[0].Chunk].File, fused[0].File, "single-channel fusion reordered results: raw top %s, fused top %s",
		c.Chunks[raw[0].Chunk].File, fused[0].File)
}

// The multi-channel receipt artifact. `native` used to be filled from the
// FIRST channel's deep list only, so a chunk that reached the fused top-k
// through the second channel alone carried Native=0 — and a reader comparing
// that 0.00 against the calibration band concludes the hit is weaker than
// unrelated control text, when the number is really an artifact of which
// channel surfaced it. Every hit must now carry a score some channel actually
// computed, and name that channel.
func TestNativeScoreComesFromTheChannelThatSurfacedTheChunk(t *testing.T) {
	t.Parallel()

	c := build(t)
	// "anemometers" is out of vocabulary (the corpus says "anemometer"), so
	// bm25 cannot reach notes/wind.md at all; trigram can. "glazing" is in
	// vocabulary, so bm25 reaches notes/glass.md. One query, two channels,
	// two different surfacing channels.
	hits := Retrieve(c, []Channel{NewBM25(c), NewTrigram(c)}, "anemometers glazing", 10)
	require.NotEmpty(t, hits, "no hits")
	byFile := map[string]FileHit{}
	for _, h := range hits {
		assert.NotEmpty(t, h.NativeChan, "%s carries no surfacing channel — its score=%.2f is unattributable", h.File, h.Native)
		byFile[h.File] = h
	}
	wind, ok := byFile["notes/wind.md"]
	require.True(t, ok, "the trigram-only file did not surface at all; hits=%+v", hits)
	assert.Equal(t, "trigram", wind.NativeChan, "notes/wind.md surfaced through %q, want trigram", wind.NativeChan)
	assert.Greater(t, wind.Native, 0.0, "notes/wind.md printed a fabricated score %.4f — the artifact this test exists for", wind.Native)
	glass, ok := byFile["notes/glass.md"]
	require.True(t, ok, "the bm25 file did not surface at all; hits=%+v", hits)
	assert.Equal(t, "bm25", glass.NativeChan, "notes/glass.md surfaced through %q, want bm25 (the first channel that scored it)", glass.NativeChan)
	// Naming the channels in the other order must move the attribution, never
	// silently keep the first-listed one.
	rev := Retrieve(c, []Channel{NewTrigram(c), NewBM25(c)}, "anemometers glazing", 10)
	for _, h := range rev {
		if h.File == "notes/glass.md" {
			assert.Equal(t, "trigram", h.NativeChan, "with trigram named first, notes/glass.md still reported %q", h.NativeChan)
		}
	}
}

// With one channel there is only one possible attribution, and every hit must
// carry it: single-channel output is exactly that channel's opinion.
func TestSingleChannelNativeIsThatChannel(t *testing.T) {
	t.Parallel()

	c := build(t)
	for _, ch := range []Channel{NewBM25(c), NewTrigram(c)} {
		for _, h := range Retrieve(c, []Channel{ch}, "salt haze glazing daylight", 5) {
			assert.Equal(t, ch.Name(), h.NativeChan, "channel %s: %s reported score-channel %q", ch.Name(), h.File, h.NativeChan)
		}
	}
}

func TestCoverage(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	// Clean case: both notes are named in index-a.md.
	fnds, err := Coverage(fsys, "notes/*.md", "notes/index-*.md")
	require.NoError(t, err, "Coverage")
	for _, f := range fnds {
		t.Errorf("unexpected finding on a clean corpus: %s: %s", f.Kind, f.Detail)
	}
	// Prove it can fail, in both directions: an orphan note nothing points
	// at, and an index line pointing at nothing.
	fsys["notes/orphan.md"] = &fstest.MapFile{Data: []byte("---\nname: orphan\n---\n\nan undistilled lesson that no index names at all")}
	fsys["notes/index-a.md"] = &fstest.MapFile{Data: []byte("# Index\n\n- [wind-log](wind.md)\n- [glazing-care](glass.md)\n- [gone](gone.md)\n")}
	fnds, err = Coverage(fsys, "notes/*.md", "notes/index-*.md")
	require.NoError(t, err, "Coverage")
	var haveOrphan, haveDangling bool
	for _, f := range fnds {
		if f.Kind == "coverage" && strings.Contains(f.Detail, "orphan") {
			haveOrphan = true
		}
		if f.Kind == "backlink" && strings.Contains(f.Detail, "gone.md") {
			haveDangling = true
		}
	}
	assert.True(t, haveOrphan, "planted orphan not found — the check cannot fail, so its pass means nothing")
	assert.True(t, haveDangling, "planted dangling index link not found")
}

// The planted fault the old regex waved through green. `\]\(([^)#?:]+\.md)\)`
// excluded '#' and '?' from the WHOLE target, so an anchored link never
// matched the pattern at all and was never checked — a dangling
// `](gone.md#top)` in a B-side index passed the coverage wall with exit 0.
// nova-check's own links check had cut the anchor before resolving since it
// was written; this is that behaviour, in the other binary.
func TestCoverageChecksAnchoredAndQueriedLinks(t *testing.T) {
	t.Parallel()

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
			require.NoError(t, err, "Coverage")
			var bad bool
			for _, f := range fnds {
				if f.Kind == "backlink" {
					bad = true
				}
			}
			assert.Equal(t, tc.wantBad, bad, "backlink finding = %v, want %v; findings: %+v", bad, tc.wantBad, fnds)
		})
	}
}

// TestCoverageCollidingStemIsNotCoverage plants a collision: a note whose
// stem is a PREFIX of another note's stem, where only the longer one is
// indexed. Testing membership with a bare substring over the concatenated
// index makes foobar.md's entry supply foo.md's coverage, and the orphan
// passes silently — the one direction a loss check must never fail in, since
// its whole value is the exit code on a real loss. The correctly indexed
// notes stay in the fixture as the control: tightening the match must not
// start reporting files that are genuinely named.
func TestCoverageCollidingStemIsNotCoverage(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	fsys["notes/foo.md"] = &fstest.MapFile{Data: []byte("---\nname: foo\n---\n\na lesson no index names at all")}
	fsys["notes/foobar.md"] = &fstest.MapFile{Data: []byte("---\nname: foobar\n---\n\na lesson the index does name")}
	fsys["notes/index-a.md"] = &fstest.MapFile{Data: []byte(
		"# Index\n\n- [wind-log](wind.md) — the anemometer\n- [glazing-care](glass.md) — the glazing\n- [foobar](foobar.md) — the longer stem\n")}

	fnds, err := Coverage(fsys, "notes/*.md", "notes/index-*.md")
	require.NoError(t, err, "Coverage")
	var haveFoo bool
	for _, f := range fnds {
		if f.Kind == "coverage" && strings.Contains(f.Detail, "notes/foo.md") {
			haveFoo = true
			continue
		}
		t.Errorf("unexpected finding on a genuinely indexed file: %s: %s", f.Kind, f.Detail)
	}
	assert.True(t, haveFoo, "notes/foo.md passed coverage on foobar.md's index entry — a substring is not membership")
}

func TestCoverageRefusesEmptySide(t *testing.T) {
	t.Parallel()

	_, err := Coverage(corpusFS(), "nothing/*.md", "notes/index-*.md")
	assert.Error(t, err, "Coverage accepted an empty A side — a broken check reported as a pass")
	_, err = Coverage(corpusFS(), "notes/*.md", "nothing/index-*.md")
	assert.Error(t, err, "Coverage accepted an empty B side — a broken check reported as a pass")
}

func TestWikilinks(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	fsys["notes/linked.md"] = &fstest.MapFile{Data: []byte("---\nname: linked\n---\n\nsee [[wind-log]] and the unwritten [[storm-glass]] page for the rest")}
	c, err := Build(fsys, nil)
	require.NoError(t, err)
	fnds, err := Wikilinks(fsys, c)
	require.NoError(t, err)
	var hit bool
	for _, f := range fnds {
		if strings.Contains(f.Detail, "storm-glass") {
			hit = true
		}
		assert.NotContains(t, f.Detail, "[[wind-log]]", "a link that resolves through frontmatter was reported unresolved: %s", f.Detail)
	}
	assert.True(t, hit, "unresolved wikilink not reported — the check cannot fail")
}

// The aliased and heading forms are the two commonest wikilink shapes, and
// the old regex excluded '|' and '#' from the body, so neither was ever
// SCANNED — a caller who chose --links=gate as a wall got the whole class
// waved through. Both arms matter: the dangling target must be reported and
// the resolving one must not, or the fix trades a hole for a false-positive
// gate.
func TestWikilinksScansAliasedAndHeadingForms(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	fsys["notes/linked.md"] = &fstest.MapFile{Data: []byte(
		"---\nname: linked\n---\n\n" +
			"see [[nowhere-page|the missing page]] and [[nowhere-sec#readings]] for what is not written,\n" +
			"beside [[wind-log|the anemometer log]] and [[glazing-care#the-brass]] which both resolve,\n" +
			"and [[#a-heading-on-this-page]] which names nothing in the corpus at all.\n")}
	c, err := Build(fsys, nil)
	require.NoError(t, err)
	fnds, err := Wikilinks(fsys, c)
	require.NoError(t, err)
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
		assert.True(t, has(stem), "dangling wikilink [[%s]] not reported — the gate has a hole; findings: %+v", stem, fnds)
	}
	for _, stem := range []string{"wind-log", "glazing-care"} {
		assert.False(t, has(stem), "a wikilink that resolves was reported unresolved: [[%s]]; findings: %+v", stem, fnds)
	}
	// A body that is only a heading is a same-page anchor, not a corpus
	// reference: reporting it would gate on something no corpus can satisfy.
	for d := range reported {
		assert.NotContains(t, d, "a-heading-on-this-page", "a heading-only wikilink was reported as a corpus reference: %s", d)
	}
}

func TestFrontmatterPresent(t *testing.T) {
	t.Parallel()

	fnds, err := FrontmatterPresent(corpusFS(), "notes/wind.md", nil)
	require.NoError(t, err, "a file with frontmatter was flagged: %v %v", fnds, err)
	require.Empty(t, fnds, "a file with frontmatter was flagged: %v %v", fnds, err)
	fsys := corpusFS()
	fsys["notes/bare.md"] = &fstest.MapFile{Data: []byte("no frontmatter at all, just a paragraph of prose")}
	fnds, err = FrontmatterPresent(fsys, "notes/bare.md", nil)
	require.NoError(t, err, "a bare file was not flagged exactly once: %v %v", fnds, err)
	require.Len(t, fnds, 1, "a bare file was not flagged exactly once: %v %v", fnds, err)
}

// The no-defaults law applied to scope: the tool this was ported from
// hardcoded the "index-" prefix as exempt, which is a guess about someone
// else's filenames. Nothing is exempt unless the caller says so, this run.
func TestFrontmatterExemptionIsTheCallersAndNeverADefault(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	fnds, err := FrontmatterPresent(fsys, "notes/index-a.md", nil)
	require.NoError(t, err)
	require.Len(t, fnds, 1, "index-a.md must be scanned when no exemption is stated, got %d findings — a default skip list has returned", len(fnds))
	fnds, err = FrontmatterPresent(fsys, "notes/index-a.md", []string{"index-"})
	require.NoError(t, err, "the caller's stated exemption was not honoured: %v %v", fnds, err)
	require.Empty(t, fnds, "the caller's stated exemption was not honoured: %v %v", fnds, err)
}

func TestFrontmatterRefusesEmptyGlob(t *testing.T) {
	t.Parallel()

	_, err := FrontmatterPresent(corpusFS(), "nothing/*.md", nil)
	assert.Error(t, err, "FrontmatterPresent accepted a glob matching nothing — a broken check reported as a pass")
}

// TestFrontmatterToleratesCRLF pins a real defect rather than a fixture one. A
// checkout on Windows produces CRLF, and the fence check was a literal
// "---\n", so every entry in a CRLF corpus reported "no name: in frontmatter" —
// a line-ending fault presenting as a corpus fault, in a tool other people run
// against their own corpora. The repo's own fixtures are held to LF by
// .gitattributes, so only a test like this one can cover the user's file.
func TestFrontmatterToleratesCRLF(t *testing.T) {
	t.Parallel()

	lf := "---\nname: lantern\ntype: reference\n---\n\nbody\n"
	wantName, wantType := frontmatter(lf)
	require.Equal(t, "lantern", wantName, "LF baseline broken: name=%q type=%q", wantName, wantType)
	require.Equal(t, "reference", wantType, "LF baseline broken: name=%q type=%q", wantName, wantType)
	for _, tw := range []struct{ name, ending string }{
		{"CRLF", "\r\n"},
		{"CR", "\r"}, // the twin a "\r\n" replacement leaves untouched
	} {
		gotName, gotType := frontmatter(strings.ReplaceAll(lf, "\n", tw.ending))
		assert.Equal(t, [2]string{wantName, wantType}, [2]string{gotName, gotType}, "%s: name=%q type=%q, want %q/%q", tw.name, gotName, gotType, wantName, wantType)
	}
}

// A wikilink inside inline code or a fenced block is a QUOTED SPECIMEN — prose
// ABOUT wikilinks — not a reference to a file. Measured on the corpus this tool
// was built for, 2026-08-31: of 44 citation-shaped occurrences, 20 sat inside
// backticks, in sentences like "every dangling `[[wikilink]]` in this repo".
//
// It matters more here than a tidiness fix sounds, because --links can be run as
// a GATE: a false positive is then not clutter, it is a wall in front of a
// correct document. And where it only reports, dilution still trains the skip —
// a list that is half specimens is a list a reader stops reading, which is how a
// real dangling pointer hides in plain sight.
//
// The genuinely dangling link in the same fixture is the NEGATIVE CONTROL:
// without it, this test would pass against a Wikilinks that reported nothing.
func TestWikilinksIgnoresQuotedSpecimens(t *testing.T) {
	t.Parallel()

	fsys := corpusFS()
	fsys["notes/specimens.md"] = &fstest.MapFile{Data: []byte(
		"---\nname: specimens\n---\n\n" +
			"prose about `[[quoted-specimen]]` and a real dangling [[genuinely-missing]] one\n\n" +
			"```\nfenced [[fenced-specimen]] here\n```\n")}
	c, err := Build(fsys, nil)
	require.NoError(t, err)
	fnds, err := Wikilinks(fsys, c)
	require.NoError(t, err)
	var sawReal bool
	for _, f := range fnds {
		assert.NotContains(t, f.Detail, "quoted-specimen", "inline-code specimen reported as a citation: %s", f.Detail)
		assert.NotContains(t, f.Detail, "fenced-specimen", "fenced-block specimen reported as a citation: %s", f.Detail)
		if strings.Contains(f.Detail, "genuinely-missing") {
			sawReal = true
		}
	}
	assert.True(t, sawReal, "negative control failed: a genuinely dangling wikilink was not reported")
}

// The masking in maskCode can fail in TWO directions, and only one of them is
// survivable. Reporting a specimen is noise; HIDING a real dangling link is the
// checker failing at its one job. The first implementation did the second — it
// paired backtick triples positionally across the whole file — so every hazard
// below is a reproduction kept as a permanent regression.
//
// Every case plants [[really-missing]] AFTER the hazard and asserts it survives.
func TestMaskingNeverHidesARealLink(t *testing.T) {
	t.Parallel()

	hazards := map[string]string{
		"lone fence run in prose":    "A fence opens with ``` in markdown.\n\nSee [[really-missing]].\n\n```\ncode\n```\n",
		"stray backticks in prose":   "it`s a shame [[really-missing]] but it`s fine\n",
		"link before unclosed fence": "See [[really-missing]].\n\n```\nnever closed\n",
		"link adjacent to code":      "the `flag` and then [[really-missing]] after it\n",
		"tilde fence then prose":     "~~~\nfenced\n~~~\n\nSee [[really-missing]].\n",
	}
	for name, body := range hazards {
		t.Run(name, func(t *testing.T) {
			fsys := corpusFS()
			fsys["notes/hazard.md"] = &fstest.MapFile{Data: []byte("---\nname: hazard\n---\n\n" + body)}
			c, err := Build(fsys, nil)
			require.NoError(t, err)
			fnds, err := Wikilinks(fsys, c)
			require.NoError(t, err)
			for _, f := range fnds {
				if strings.Contains(f.Detail, "really-missing") {
					return
				}
			}
			t.Errorf("masking HID a real dangling link (%s)", name)
		})
	}
}

// fixedChannel is a test double that returns a predetermined ranking, so a
// fusion test can know each chunk's per-channel rank without depending on the
// scoring details of any real channel.
type fixedChannel struct {
	name string
	res  []Scored
}

func (f fixedChannel) Name() string                      { return f.name }
func (f fixedChannel) Query(text string, k int) []Scored { return f.res }

// Pin the channel geometry BM25 smoothing, trigram Jaccard and reciprocal-rank
// fusion dictate. These contracts live in docs/SPEC.md but had no direct tests.
func TestIssue2306(t *testing.T) {
	t.Parallel()

	t.Run("BM25IdfNeverNegative", func(t *testing.T) {
		// A term that appears in every document has DF == len(Chunks). The
		// classic idf log(N/n) would be zero here; Lucene smoothing must stay
		// strictly positive, because a corpus of related notes is full of
		// common terms and a zero or negative idf scrambles rankings.
		c, err := Build(fstest.MapFS{
			"a.md": {Data: []byte("alpha beta gamma")},
			"b.md": {Data: []byte("alpha delta epsilon")},
		}, nil)
		require.NoError(t, err, "Build")
		got := NewBM25(c).idf("alpha")
		assert.Greater(t, got, 0.0, "idf for a term in every chunk = %v, want > 0", got)
	})

	t.Run("BM25Constants", func(t *testing.T) {
		c, err := Build(fstest.MapFS{
			"a.md": {Data: []byte("alpha beta gamma")},
		}, nil)
		require.NoError(t, err, "Build")
		bm := NewBM25(c)
		assert.Equal(t, 1.2, bm.K1, "K1 = %v, want 1.2", bm.K1)
		assert.Equal(t, 0.75, bm.B, "B = %v, want 0.75", bm.B)
	})

	t.Run("TrigramJaccardIsBounded", func(t *testing.T) {
		c := build(t)
		trig := NewTrigram(c)
		got := trig.Query("anemometers", len(c.Chunks))
		var foundWind bool
		for _, s := range got {
			assert.False(t, s.Score < 0 || s.Score > 1, "trigram score %v out of [0,1] for chunk %d", s.Score, s.Chunk)
			if c.Chunks[s.Chunk].File == "notes/wind.md" {
				foundWind = true
			}
		}
		assert.True(t, foundWind, "morphology variant \"anemometers\" did not reach notes/wind.md; got %+v", got)
	})

	t.Run("FusionIsReciprocalRankOnly", func(t *testing.T) {
		c, err := Build(fstest.MapFS{
			"a.md": {Data: []byte("alpha beta gamma")},
			"b.md": {Data: []byte("delta epsilon zeta")},
		}, nil)
		require.NoError(t, err, "Build")
		chA := fixedChannel{name: "A", res: []Scored{
			{Chunk: 0, Rank: 0, Score: 10},
			{Chunk: 1, Rank: 1, Score: 5},
		}}
		chB := fixedChannel{name: "B", res: []Scored{
			{Chunk: 1, Rank: 0, Score: 7},
			{Chunk: 0, Rank: 1, Score: 3},
		}}
		hits := Retrieve(c, []Channel{chA, chB}, "irrelevant", 2)
		byFile := map[string]float64{}
		for _, h := range hits {
			byFile[h.File] = h.Fused
		}
		want := 1.0/60.0 + 1.0/61.0
		const eps = 1e-12
		for _, f := range []string{"a.md", "b.md"} {
			got, ok := byFile[f]
			require.True(t, ok, "missing hit for %s", f)
			assert.InDelta(t, want, got, eps, "%s fused = %v, want %v (reciprocal-rank sum)", f, got, want)
		}
	})
}

// The other direction, kept beside it so neither can be "fixed" by breaking the
// other: these SHOULD be masked, and a checker that reports them is the diluted
// one this change exists to repair.
func TestMaskingDoesSilenceQuotedSpecimens(t *testing.T) {
	t.Parallel()

	quiet := map[string]string{
		"inline specimen": "every dangling `[[quiet-one]]` in this repo\n",
		"fenced specimen": "```\nsee [[quiet-one]] here\n```\n",
		"tilde fenced":    "~~~\nsee [[quiet-one]] here\n~~~\n",
		"indented fence":  "  ```\n  see [[quiet-one]] here\n  ```\n",
		"double backtick": "a ``[[quiet-one]]`` span is a specimen too\n",
	}
	for name, body := range quiet {
		t.Run(name, func(t *testing.T) {
			fsys := corpusFS()
			fsys["notes/quiet.md"] = &fstest.MapFile{Data: []byte("---\nname: quiet\n---\n\n" + body)}
			c, err := Build(fsys, nil)
			require.NoError(t, err)
			fnds, err := Wikilinks(fsys, c)
			require.NoError(t, err)
			for _, f := range fnds {
				assert.NotContains(t, f.Detail, "quiet-one", "quoted specimen reported as a citation (%s): %s", name, f.Detail)
			}
		})
	}
}

func TestIssue2305(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		".git/HEAD.md":       {Data: []byte("ref: refs/heads/main\n")},
		".git/config.md":     {Data: []byte("this git config pretends to be a markdown file\n")},
		".git/objects/md.md": {Data: []byte("a nested markdown inside git objects\n")},
		"real.md":            {Data: []byte("---\nname: real-document\n---\n\nA real document that should be indexed into three separate chunks for the index.\n\nThe second paragraph is about the light and how it travels across water at night.\n\nThe third paragraph is about the diaphone and its nine hours of running.\n")},
	}

	c, err := Build(fsys, nil)
	require.NoError(t, err, "Build")

	for _, ch := range c.Chunks {
		assert.False(t, strings.HasPrefix(ch.File, ".git/"), "chunk from .git/ reached the index: %s", ch.File)
	}

	for _, f := range c.Files {
		assert.False(t, strings.HasPrefix(f, ".git/"), ".git/ file listed in Files: %s", f)
	}

	_, ok := c.ByClass[".git"]
	assert.False(t, ok, ".git class appeared in ByClass -- git content reached the index")
}

// TestBuildSkipsNestedGitDirectory pins issue #2305's second half: the
// .git skip is by basename at any depth, not only at the corpus root.
// A root-only check (p == ".git") passes TestIssue2305 but fails here.
func TestBuildSkipsNestedGitDirectory(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"sub/.git/file.md":       {Data: []byte("a markdown file inside a nested git directory\n")},
		"sub/.git/objects/md.md": {Data: []byte("a nested markdown inside nested git objects\n")},
		"a/b/.git/HEAD.md":       {Data: []byte("ref: refs/heads/main\n")},
		"sub/real.md":            {Data: []byte("---\nname: nested-real\n---\n\nA real document beside the nested git directory that should reach the index.\n")},
	}

	c, err := Build(fsys, nil)
	require.NoError(t, err, "Build")

	for _, ch := range c.Chunks {
		assert.NotContains(t, "/"+ch.File, "/.git/", "chunk from a nested .git/ reached the index: %s", ch.File)
	}
	sawReal := false
	for _, f := range c.Files {
		assert.NotContains(t, "/"+f, "/.git/", "nested .git/ file listed in Files: %s", f)
		if f == "sub/real.md" {
			sawReal = true
		}
	}
	assert.True(t, sawReal, "sub/real.md missing from Files %v: the skip must drop only .git, not its parent", c.Files)
}

// Frontmatter is receipt metadata, never body text: a query on a frontmatter key or value
// matches no YAML, the snippet quotes none, the file still carries its name and type, and
// the body keeps the line number it has in the file (the rater's case: frontmatter with
// no blank line before the body, and the one with a blank line).
func TestFrontmatterIsMetadataNeverBodyText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, src string
		line      int
	}{
		{"no blank line after the fence", "---\nname: deploy-note\ntype: project\n---\nDeploy runs nightly on the bench machine.\n", 5},
		{"a blank line after the fence", "---\nname: deploy-note\ntype: project\n---\n\nDeploy runs nightly on the bench machine.\n", 6},
		{"CRLF line endings", "---\r\nname: deploy-note\r\ntype: project\r\n---\r\nDeploy runs nightly on the bench machine.\r\n", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Build(fstest.MapFS{
				"b.md":     {Data: []byte(tc.src)},
				"other.md": {Data: []byte("The relief boat runs late on a project of its own.\n")},
			}, nil)
			require.NoError(t, err)
			for _, ch := range c.Chunks {
				assert.NotContains(t, ch.Original, "---", "a chunk quotes the frontmatter fence: %q", ch.Original)
				assert.NotContains(t, ch.Original, "name:", "a chunk quotes the frontmatter: %q", ch.Original)
				if ch.File == "b.md" {
					assert.Equal(t, tc.line, ch.Line)
					assert.Equal(t, "deploy-note", ch.FMName)
					assert.Equal(t, "project", ch.FMType)
				}
			}
			for _, h := range Retrieve(c, []Channel{NewBM25(c)}, "type project", 3) {
				assert.NotEqual(t, "b.md", h.File, "a query on the frontmatter's key and value matched b.md's YAML")
			}
		})
	}
}
