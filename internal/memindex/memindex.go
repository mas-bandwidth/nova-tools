/*
Package memindex is the index half of nova-memory: membership in a markdown
corpus as a LOOKUP, never a scan.

WHY THIS EXISTS. A mind that keeps its memory as markdown answers "do I
already know this?" by re-reading everything it is. Consolidating n new
learnings against m existing memories is O(n*m), and m grows every day, so a
fixed daily budget buys a shrinking n — and the failure is silent: the self
just learns less while every step still looks like working. The requirement
this package serves is that the mind's budget per new learning be k receipts,
k constant: the index narrows m to k, the mind judges only the survivors, and
NOTHING here ever writes the corpus.

THREE DECISIONS A READER SHOULD KNOW, each measured before it was made:

 1. THE INDEX IS REBUILT IN MEMORY EVERY RUN. A full build over the corpus it
    was written for (548 markdown files, 11,823 paragraph chunks, ~11.5MB)
    measured 830ms on 2026-08-10 — so there is no persisted index, no
    staleness surface, and nothing that can drift from the tree. The repo is
    the store; this is a derivation that stops existing when the process
    exits. The escalation, if a build ever exceeds a couple of seconds:
    persist keyed by the tree's commit, then a real FTS engine. Named, not
    built.

 2. THE NORMALIZER RUNS BEFORE CHUNK TEXT IS STORED, because four grep
    failure modes were measured on that corpus (2026-07-31 / 2026-08-02):
    line wraps split phrases, emphasis markers split them, blockquote markers
    survive a naive unwrap, and function-word variation defeats phrase greps.
    Strip blockquote and emphasis characters FIRST, then collapse whitespace,
    then casefold. The order is the fix — collapsing first leaves the
    blockquote marker sitting inside the recovered phrase.

 3. EVERY RESULT ORDERING IS TOTAL. Go randomizes map iteration, and a
    hand-rolled retriever that iterates maps while scoring is
    nondeterministic by default. Ties break by chunk id after score, then by
    (path, para) after fusion; postings are sorted by chunk id; and the
    determinism test builds twice and requires identical bytes.

This package does no I/O beyond fs.FS reads handed in by the caller, so every
judgment is reachable by a test with no tempdir.
*/
package memindex

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SchemaVersion names the token space: tokenizer + normalizer + chunking
// rules together. It is printed by stats and would key any future persisted
// index, so a schema change can never silently mix token spaces —
// preprocessing drift is a failure that corrupts quietly.
const SchemaVersion = "nova-memory/1"

// MinTerms is the floor below which a paragraph is not worth indexing: one or
// two tokens is a heading fragment or a separator, and indexing them makes
// every rare-word query drown in stubs.
const MinTerms = 3

// Chunk is one indexed paragraph. Text is NORMALIZED (see Normalize); the raw
// bytes are not kept — receipts quote normalized text, and the file:para
// anchor is the stable address a judge needs to go read the original.
type Chunk struct {
	File  string // slash path relative to the corpus root
	Para  int    // ordinal among the file's indexable paragraphs
	Class string // top-level directory, "." for root files — the corpus classifies itself
	Text  string
	Terms map[string]int
	Len   int
	// Frontmatter, carried as receipt metadata when the file has any. The
	// tool surfaces whatever the corpus already writes; it invents nothing
	// and requires nothing.
	FMName string
	FMType string
}

// Corpus is the whole derived index. Build order is deterministic: files
// sorted by path, chunks in file order.
type Corpus struct {
	Chunks  []Chunk
	DF      map[string]int
	Post    map[string][]int32 // term -> chunk ids, ascending
	AvgLen  float64
	Files   []string
	Bytes   int64
	ByClass map[string]int // class -> chunk count
}

// Normalize strips blockquote markers and markdown emphasis BEFORE collapsing
// whitespace, then casefolds. See decision 2 in the package comment: the
// order is the whole fix.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch {
		case strings.ContainsRune("*_`>#|[]()", r):
			continue
		case unicode.IsSpace(r):
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		default:
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// Tokenize splits normalized text into terms: alphanumeric-or-apostrophe
// runs, length >= 2 after trimming apostrophes. No stemming and no stopwords:
// both were measured unnecessary on the corpus this was built for, and both
// are levers the eval harness can legitimize later on yours — never taste.
func Tokenize(s string) []string {
	s = Normalize(s)
	var out []string
	var cur strings.Builder
	flush := func() {
		t := strings.Trim(cur.String(), "'")
		if len(t) >= 2 {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// frontmatter reads a minimal frontmatter shape without a YAML dependency: a
// leading "---" fence, then "name:" and "type:" lines anywhere before the
// closing fence. Absent or malformed frontmatter returns empty strings —
// verify reports absence where a caller declares it required, and parsing
// here never fails a build.
func frontmatter(src string) (name, typ string) {
	// CRLF is tolerated because a checkout on Windows produces it, and a memory
	// file whose frontmatter fence ends "---\r\n" was parsed as having NO
	// frontmatter at all: every entry then reported "no name: in frontmatter",
	// which reads as a corpus fault rather than a line-ending one. Found by CI
	// on its first Windows run, in a tool other people are told to run against
	// their own corpora.
	var body string
	switch {
	case strings.HasPrefix(src, "---\n"):
		body = src[4:]
	case strings.HasPrefix(src, "---\r\n"):
		body = src[5:]
	default:
		return "", ""
	}
	end := strings.Index(body, "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(body[:end], "\n") {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, "name:"); ok && name == "" {
			name = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(trimmed, "type:"); ok && typ == "" {
			typ = strings.TrimSpace(v)
		}
	}
	return name, typ
}

// Truncate cuts s to at most n bytes on a rune boundary, appending an ellipsis
// when it cut. Receipts are quoted inside a machine-scannable line, and a
// mid-rune cut would put invalid UTF-8 into that line — the source this was
// ported from sliced bytes directly, which is a latent defect on any corpus
// holding a non-ASCII character near the cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Build derives the whole index from a filesystem of markdown. It refuses an
// empty corpus loudly: a query engine over nothing answers every membership
// question "no", which is the exact confident-zero failure this tool exists
// to remove.
//
// exclude, when non-nil, is asked about every directory and every markdown
// file by slash path relative to the root. It is the caller's scope, stated
// per run; nothing is excluded by default except .git, which is never a
// corpus.
func Build(fsys fs.FS, exclude func(p string) bool) (*Corpus, error) {
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (path.Base(p) == ".git" || (exclude != nil && exclude(p))) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".md") && (exclude == nil || !exclude(p)) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking corpus: %w", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no markdown files found — wrong root, or everything excluded")
	}

	c := &Corpus{DF: map[string]int{}, Post: map[string][]int32{}, ByClass: map[string]int{}, Files: files}
	var totalTerms int
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		c.Bytes += int64(len(raw)) // the bytes on disk, before any normalization
		// Line endings are normalized ONCE, here, before anything looks at the
		// text. The blank-line split is on the literal "\n\n", so a CRLF file's
		// blank lines ("\r\n\r\n") never split it: every such file indexed as
		// one giant chunk, collapsing its para addresses, degrading BM25 length
		// normalization, and quietly disabling MinTerms filtering — silently,
		// with a green STATS line. This repo ships to other lines on other
		// platforms, where CRLF markdown is ordinary.
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		fmName, fmType := frontmatter(text)
		class := "."
		if i := strings.IndexByte(f, '/'); i >= 0 {
			class = f[:i]
		}
		para := 0
		for _, p := range strings.Split(text, "\n\n") {
			terms := Tokenize(p)
			if len(terms) < MinTerms {
				continue
			}
			tf := make(map[string]int, len(terms))
			for _, t := range terms {
				tf[t]++
			}
			id := int32(len(c.Chunks))
			c.Chunks = append(c.Chunks, Chunk{
				File: f, Para: para, Class: class, Text: Normalize(p),
				Terms: tf, Len: len(terms), FMName: fmName, FMType: fmType,
			})
			for t := range tf {
				c.DF[t]++
				c.Post[t] = append(c.Post[t], id)
			}
			totalTerms += len(terms)
			para++
			c.ByClass[class]++
		}
	}
	if len(c.Chunks) == 0 {
		return nil, fmt.Errorf("corpus has %d markdown files but zero indexable paragraphs", len(files))
	}
	c.AvgLen = float64(totalTerms) / float64(len(c.Chunks))
	// Postings arrive sorted by construction (ids ascend), so no per-term sort
	// is needed — but the determinism of every result rests on that invariant,
	// so assert it cheaply rather than trusting the construction forever.
	for t, ids := range c.Post {
		if !sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] }) {
			return nil, fmt.Errorf("internal: postings for %q not sorted", t)
		}
	}
	return c, nil
}
