package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// memory searches the notes already written and checks a draft against them. The index is rebuilt
// every run and never written; ties break by chunk id, so the same corpus gives the same bytes.

const (
	schema   = "nova-memory/2" // the token scheme: normalizer + tokenizer + chunking
	minTerms = 3               // a paragraph of fewer terms is a heading fragment
	maxFile  = 8 << 20         // a file over this is refused by name
	probe    = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"
	noteLex  = "lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic"
)

// chunk is one indexed paragraph: of a note, or of a store entry's words.
type chunk struct {
	file, class, root, text, fmName, fmType string
	line, n                                 int
	terms                                   map[string]int
	store                                   string     // an entry's store directory
	entry                                   *entryFile // nil for a note
}

type corpus struct {
	chunks  []chunk
	df      map[string]int
	post    map[string][]int
	avg     float64
	files   []string // the notes indexed
	entries int      // the store entries indexed
	skipped []string // .json files under an entries/ directory that are not store entries
	bytes   int
	total   int // terms in all chunks
	byClass map[string]int
}

// normalize drops blockquote and emphasis marks BEFORE collapsing whitespace
// (the order is the fix: a marker left inside a wrapped phrase breaks it), then casefolds.
func normalize(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case strings.ContainsRune("*_`>#|[]()", r):
		case unicode.IsSpace(r):
			if !space {
				b.WriteByte(' ')
			}
			space = true
		default:
			b.WriteRune(unicode.ToLower(r))
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}

// tokenize: alphanumeric-or-apostrophe runs of two or more bytes. No stemming, no stopwords.
func tokenize(s string) []string {
	return strings.FieldsFunc(normalize(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' })
}

var stopwords = map[string]bool{"the": true, "a": true, "an": true, "and": true, "or": true, "of": true, "to": true, "in": true, "on": true, "for": true, "with": true, "as": true, "at": true, "by": true, "from": true, "is": true, "are": true, "was": true, "were": true, "be": true, "been": true, "it": true, "its": true, "this": true, "that": true, "these": true, "those": true, "before": true, "after": true, "into": true, "over": true, "under": true, "per": true, "via": true}

func terms(s string) []string {
	var out []string
	for _, t := range tokenize(s) {
		if t = strings.Trim(t, "'"); len(t) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

func lf(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n") }

var yamlLine = regexp.MustCompile(`^(\s*|\s*#.*|[A-Za-z_][A-Za-z0-9_.-]*\s*:.*|[ \t].*|-( .*)?)$`)

// frontmatter reads the leading "---" block: its name: and type: values, and
// the offset where the block's closing line ends (0 when there is no block).
// A block with a line that is not YAML-shaped is a thematic break, not frontmatter.
func frontmatter(src string) (name, typ string, end int) {
	if !strings.HasPrefix(src, "---\n") {
		return
	}
	body := src[4:]
	close := -1
	for i := 0; ; {
		j := strings.Index(body[i:], "\n---")
		if j < 0 {
			return
		}
		i += j
		if i+4 == len(body) || body[i+4] == '\n' {
			close = i
			break
		}
		i++
	}
	yaml := true
	for _, l := range strings.Split(body[:close], "\n") {
		yaml = yaml && yamlLine.MatchString(l)
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "name:"); ok && name == "" {
			name = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "type:"); ok && typ == "" {
			typ = strings.TrimSpace(v)
		}
	}
	if !yaml {
		return name, typ, 0
	}
	end = 4 + close + len("\n---")
	if nl := strings.IndexByte(src[end:], '\n'); nl >= 0 {
		end += nl
	} else {
		end = len(src)
	}
	return
}

// truncate cuts s to n bytes on a rune boundary, adding an ellipsis when it cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

type roots struct {
	root, exclude multi
}

func (r *roots) excluded(p string) bool {
	for _, e := range r.exclude {
		if ok, _ := path.Match(e, p); ok || p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
	}
	return false
}

// load reads the markdown notes under root for verify: their slash paths,
// sorted, their texts with line endings made \n, and their size in bytes.
func (r *roots) load(root string) (files []string, texts map[string]string, size int, err error) {
	if files, _, err = r.walk(root); err == nil && len(files) == 0 {
		err = fmt.Errorf("no markdown notes found — wrong root, or everything excluded")
	}
	if err != nil {
		return nil, nil, 0, err
	}
	texts = map[string]string{}
	for _, f := range files {
		raw, err := readFile(root, f)
		if err != nil {
			return nil, nil, 0, &fileError{root, f, err}
		}
		size += len(raw)
		texts[f] = lf(raw)
	}
	return files, texts, size, nil
}

// walk lists what memory reads under root, as sorted slash paths: the markdown notes, and the
// entry files of any store in the tree. It skips .git, excluded paths and symlinks.
func (r *roots) walk(root string) (notes, entries []string, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if err != nil {
			if rel == "." {
				return err
			}
			return &fileError{root, rel, err}
		}
		switch {
		case rel != "." && ((d.IsDir() && d.Name() == ".git") || r.excluded(rel)):
			if d.IsDir() {
				return filepath.SkipDir
			}
		case d.IsDir() || !d.Type().IsRegular():
		case strings.HasSuffix(rel, ".md"):
			if !storeRecord(root, rel) {
				notes = append(notes, rel)
			}
		case entryRel(rel) != nil:
			entries = append(entries, rel)
		}
		return nil
	})
	slices.Sort(notes)
	slices.Sort(entries)
	if err == nil && len(notes)+len(entries) == 0 {
		err = fmt.Errorf("no markdown notes and no store entries found — wrong root, or everything excluded")
	}
	return notes, entries, err
}

// entryRel splits a store entry's path, <prefix>entries/<session>/<id>.json,
// into prefix, session and id; nil when rel is not one.
func entryRel(rel string) []string {
	parts := strings.Split(rel, "/")
	n := len(parts)
	if n < 3 || parts[n-3] != "entries" {
		return nil
	}
	id, ok := strings.CutSuffix(parts[n-1], ".json")
	if !ok || !validID(id) || !validID(parts[n-2]) {
		return nil
	}
	return []string{strings.Join(parts[:n-3], "/"), parts[n-2], id}
}

// storeRecord reports a store's readable record, <prefix>sessions/<session>.md
// beside an entries/ directory: its words are in the entries, so it is not a note.
func storeRecord(root, rel string) bool {
	dir, _ := path.Split(rel)
	prefix, ok := strings.CutSuffix(dir, "sessions/")
	if !ok {
		return false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(prefix), "entries"))
	return err == nil && info.IsDir()
}

// fileError is a file under a root that could not be read; the remedy excludes it.
type fileError struct {
	root, rel string
	err       error
}

func (e *fileError) Error() string { return e.err.Error() }

func readFile(root, rel string) (string, error) {
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if len(raw) > maxFile {
		err = fmt.Errorf("%s is over the %d-byte file cap; exclude it", rel, maxFile)
	}
	return string(raw), err
}

// add indexes the paragraphs of one text that hold at least min terms.
func (c *corpus) add(ch chunk, text string, min int) {
	line := 1
	for _, p := range strings.Split(text, "\n\n") {
		start := line + len(p) - len(strings.TrimLeft(p, "\n"))
		line += strings.Count(p, "\n") + 2
		ts := terms(p)
		if len(ts) < min {
			continue
		}
		tf := map[string]int{}
		for _, t := range ts {
			tf[t]++
		}
		id := len(c.chunks)
		ch.text, ch.line, ch.n, ch.terms = strings.Trim(p, "\n"), start, len(ts), tf
		c.chunks = append(c.chunks, ch)
		for t := range tf {
			c.df[t]++
			c.post[t] = append(c.post[t], id)
		}
		c.byClass[ch.class]++
		c.total += len(ts)
	}
}

// build indexes every root: each note's paragraphs of at least minTerms terms,
// and every paragraph of each store entry's words, however short.
func (r *roots) build() (*corpus, error) {
	c := &corpus{df: map[string]int{}, post: map[string][]int{}, byClass: map[string]int{}}
	for _, root := range r.root {
		notes, entries, err := r.walk(root)
		for _, f := range append(slices.Clone(notes), entries...) {
			if err != nil {
				break
			}
			var raw string
			if raw, err = readFile(root, f); err != nil {
				err = &fileError{root, f, err}
				break
			}
			ch := chunk{file: f, class: ".", root: root}
			if i := strings.IndexByte(f, '/'); i >= 0 {
				ch.class = f[:i]
			}
			if e := entryRel(f); e != nil {
				var ef entryFile
				if json.Unmarshal([]byte(raw), &ef) != nil || ef.Session != e[1] || ef.ID != e[2] {
					c.skipped = append(c.skipped, filepath.Join(root, filepath.FromSlash(f))) // some other .json, not a store entry
					continue
				}
				c.bytes += len(raw)
				ch.store, ch.entry = filepath.Join(root, filepath.FromSlash(e[0])), &ef
				c.entries++
				c.add(ch, lf(ef.Text), 1)
				continue
			}
			c.bytes += len(raw)
			c.files = append(c.files, f)
			text := lf(raw)
			name, typ, end := frontmatter(text)
			ch.fmName, ch.fmType = name, typ
			c.add(ch, strings.Repeat("\n", strings.Count(text[:end], "\n"))+text[end:], minTerms) // keep line numbers
		}
		if err != nil {
			return nil, fmt.Errorf("building the index over %s: %w", root, err)
		}
	}
	if len(c.chunks) == 0 {
		return nil, fmt.Errorf("the corpus holds no indexable paragraph (a note's needs at least %d terms); point --root at notes or a store with words in it", minTerms)
	}
	c.avg = float64(c.total) / float64(len(c.chunks))
	return c, nil
}

type hit struct {
	chunk int
	rank  int // 0-based rank among scored chunks
	score float64
}

// query ranks the chunks by BM25, best first (ties by chunk id).
func (c *corpus) query(text string) []hit {
	scores, seen := map[int]float64{}, map[string]bool{}
	q, weighted := terms(text), slices.ContainsFunc(terms(text), func(t string) bool { return !stopwords[t] })
	for _, t := range q {
		if seen[t] || (weighted && stopwords[t]) {
			continue
		}
		seen[t] = true
		n, N := float64(c.df[t]), float64(len(c.chunks))
		idf := math.Log(1 + (N-n+0.5)/(n+0.5))
		for _, id := range c.post[t] {
			tf := float64(c.chunks[id].terms[t])
			scores[id] += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*float64(c.chunks[id].n)/c.avg))
		}
	}
	hits := make([]hit, 0, len(scores))
	for id, s := range scores {
		hits = append(hits, hit{id, 0, s})
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), a.chunk-b.chunk)
	})
	for i := range hits {
		hits[i].rank = i
	}
	return hits
}

// retrieve is the k best files for text; a tail scoring 0.00 is dropped unless every hit does.
func (c *corpus) retrieve(text string, k int) []hit {
	var out []hit
	seen := map[string]bool{}
	for _, h := range c.query(text) {
		key := c.chunks[h.chunk].root + "\x00" + c.chunks[h.chunk].file
		if !seen[key] && len(out) < k {
			seen[key] = true
			out = append(out, h)
		}
	}
	for last := len(out); last > 0; last-- {
		if math.Round(out[last-1].score*100) > 0 {
			return out[:last]
		}
	}
	return out
}

// printHits prints one HIT line per receipt, or one MISS line when there is none.
func (c *corpus) printHits(w io.Writer, head, prefix string, hits []hit) {
	if len(hits) == 0 {
		fmt.Fprintf(w, "%s MISS %severy query term is out of vocabulary for this corpus\n", head, prefix)
	}
	for i, h := range hits {
		ch := c.chunks[h.chunk]
		where := ""
		if e := ch.entry; e != nil { // what receipt needs to fetch the entry
			where = fmt.Sprintf(" store=%s session=%s entry=%s stamp=%s", dash(ch.store), dash(e.Session), dash(e.ID), dash(e.Stamp))
		}
		fmt.Fprintf(w, "%s HIT %srank=%d score=%.2f score-channel=bm25 class=%s name=%s type=%s root=%s%s: %s:%d %q\n",
			head, prefix, i+1, h.score, dash(ch.class), dash(ch.fmName), dash(ch.fmType), dash(ch.root), where, esc(ch.file), ch.line, truncate(ch.text, 120))
	}
}

func runMemory(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		if len(args) == 0 {
			return refused(stderr, "memory", "no verb given; the verbs are search, check, stats, verify", "nova-cairn help memory")
		}
		help(stdout, "memory")
		return 0
	}
	verb := args[0]
	if !slices.Contains([]string{"stats", "search", "check", "verify"}, verb) {
		return refused(stderr, "memory", fmt.Sprintf("unknown verb %q; the verbs are search, check, stats, verify", verb), "nova-cairn help memory")
	}
	path := "memory " + verb
	// Each verb takes only its own flags: any other is refused as unknown.
	fs := flags()
	var r roots
	var coverage, front, exempt multi
	channels, links, k, failMax := new(string), new(string), new(int), new(int)
	fs.Var(&r.root, "root", "a directory to read, repeatable")
	fs.Var(&r.exclude, "exclude", "a path or glob to skip, repeatable")
	switch verb {
	case "search", "check":
		fs.StringVar(channels, "channels", "", "bm25")
		fs.IntVar(k, "k", 0, "a positive number of receipts per query")
	case "verify":
		fs.StringVar(links, "links", "", "gate or info")
		fs.Var(&coverage, "coverage", "an A:B glob pair")
		fs.Var(&front, "frontmatter", "a glob")
		fs.Var(&exempt, "exempt", "a basename prefix")
		fs.IntVar(failMax, "fail-max", 20, "a number of lines per kind: 0 prints all")
	}
	if code := parse(fs, path, args[1:], stdout, stderr); code >= 0 {
		return code
	}
	pos := fs.Args()
	refuse := func(why string) int { return refused(stderr, path, why, "") }
	// fail refuses a run the files stopped: the remedy leaves out the file that
	// could not be read, or lists the roots.
	fail := func(err error) int {
		if fe := (*fileError)(nil); errors.As(err, &fe) {
			return refused(stderr, path, err.Error(), shell(append([]string{"nova-cairn", "memory", verb, "--exclude", fe.rel}, args[1:]...)...))
		}
		return refused(stderr, path, err.Error(), shell(append([]string{"ls", "-la"}, r.root...)...))
	}
	if (verb == "stats" || verb == "verify") && len(pos) > 0 {
		return refuse(fmt.Sprintf("%s takes no argument outside a flag, got %q", path, pos[0]))
	}
	if len(r.root) == 0 {
		return refuse("--root is required: name the directory of notes; there is no environment variable and no discovery from the working directory")
	}
	retrieval := verb == "search" || verb == "check"
	if retrieval && *channels != "bm25" {
		return refuse("--channels bm25 is required: bm25 is the one channel this build has; add --channels bm25")
	}
	if retrieval && *k <= 0 {
		return refuse("--k must be positive: k is the budget of receipts, and zero is not unlimited; add --k 5, say")
	}
	if verb == "verify" {
		return verify(&r, *links, coverage, front, exempt, *failMax, refuse, fail, stdout, stderr)
	}
	for _, root := range r.root {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return refused(stderr, path, fmt.Sprintf("--root %s is not a readable directory; name a directory of notes", root), shell("ls", "-ld", root))
		}
	}
	// check reads its draft before building: a bad draft is refused first.
	var candidates []string
	source := "-"
	switch {
	case verb == "search" && len(pos) == 0:
		return refuse("no query words given; put the words to search for after the flags")
	case verb == "check" && len(pos) != 1:
		return refuse("name exactly one candidate file, or - for stdin; refusing to guess")
	case verb == "check":
		if pos[0] != "-" {
			source = pos[0]
		}
		raw, err := input(pos[0], stdin)
		if err != nil {
			return refused(stderr, path, fmt.Sprintf("reading %s: %v", source, err), shell("ls", "-l", pos[0]))
		}
		for _, p := range strings.Split(lf(string(raw)), "\n\n") {
			if len(terms(p)) >= minTerms {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			return refuse(fmt.Sprintf("%s holds no candidate paragraph of at least %d terms; nothing to check, so name a draft with prose in it", source, minTerms))
		}
	}
	t0 := time.Now()
	c, err := r.build()
	if err != nil {
		return fail(err)
	}
	head := map[string]string{"stats": "STATS", "search": "SEARCH", "check": "MEMORY"}[verb]
	if n := len(c.skipped); n > 0 {
		fmt.Fprintf(stderr, "%s WARN skipped=%d first=%s: a .json under entries/ that is not a store entry (no session and id of its own) is left out; --exclude <glob> leaves such files out quietly\n",
			head, n, field(c.skipped[0]))
	}
	if verb == "stats" {
		fmt.Fprintf(stdout, "STATS OK schema=%s files=%d entries=%d skipped=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
			schema, len(c.files), c.entries, len(c.skipped), len(c.chunks), c.bytes, len(c.df), c.avg, time.Since(t0))
		for _, cl := range slices.Sorted(maps.Keys(c.byClass)) {
			fmt.Fprintf(stdout, "STATS OK class=%s chunks=%d\n", field(cl), c.byClass[cl])
		}
		return 0
	}
	cal := "score=- score-channel=-"
	if h := c.retrieve(probe, 1); len(h) > 0 {
		cal = fmt.Sprintf("score=%.2f score-channel=bm25", h[0].score)
	}
	if verb == "search" {
		q := strings.Join(pos, " ")
		hits := c.retrieve(q, *k)
		fmt.Fprintf(stdout, "SEARCH OK hits=%d k=%d channels=bm25 files=%d entries=%d skipped=%d chunks=%d: query=%s\n", len(hits), *k, len(c.files), c.entries, len(c.skipped), len(c.chunks), strconv.Quote(q))
		fmt.Fprintf(stdout, "SEARCH CAL %s probe=unrelated-control\n", cal)
		c.printHits(stdout, "SEARCH", "", hits)
		if len(hits) == 0 {
			fmt.Fprintf(stdout, "SEARCH NOTE %s\n", noteLex)
		}
		return 0
	}
	fmt.Fprintf(stdout, "MEMORY OK candidates=%d source=%s k=%d channels=bm25 files=%d entries=%d skipped=%d chunks=%d\n", len(candidates), field(source), *k, len(c.files), c.entries, len(c.skipped), len(c.chunks))
	fmt.Fprintf(stdout, "MEMORY CAL %s probe=unrelated-control\n", cal)
	dated, missed := false, false
	for i, cand := range candidates {
		fmt.Fprintf(stdout, "MEMORY CAND n=%d: %s\n", i+1, strconv.Quote(truncate(strings.TrimSpace(cand), 100)))
		hits := c.retrieve(cand, *k)
		missed = missed || len(hits) == 0
		dated = dated || slices.ContainsFunc(hits, func(h hit) bool { return c.chunks[h.chunk].class == "log" })
		c.printHits(stdout, "MEMORY", fmt.Sprintf("cand=%d ", i+1), hits)
	}
	if missed {
		fmt.Fprintf(stdout, "MEMORY NOTE %s\n", noteLex)
	}
	if dated {
		fmt.Fprintln(stdout, "MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction")
	}
	return 0
}
