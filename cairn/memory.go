package main

import (
	"cmp"
	"flag"
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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// memory searches your own markdown notes and checks a draft against them.
// The index (BM25 over paragraphs) is rebuilt in memory every run, so there
// is nothing to go stale and nothing is ever written. Every ordering is total:
// ties break by chunk id, so the same corpus gives the same bytes.

const (
	schema   = "nova-memory/2" // the token scheme: normalizer + tokenizer + chunking
	minTerms = 3               // a paragraph of fewer terms is a heading fragment
	maxFile  = 8 << 20         // a markdown file over this is refused by name
	probe    = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"
	noteLex  = "lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic"
)

type chunk struct {
	file, class, root, text, fmName, fmType string
	line, n                                 int
	terms                                   map[string]int
}

type corpus struct {
	chunks  []chunk
	df      map[string]int
	post    map[string][]int
	avg     float64
	files   []string
	bytes   int
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

// walk lists the markdown files under root (slash paths, sorted), skipping .git,
// anything excluded, and anything that is not a regular file (a symlink is never followed).
func (r *roots) walk(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel != "." && ((d.IsDir() && d.Name() == ".git") || r.excluded(rel)):
			if d.IsDir() {
				return filepath.SkipDir
			}
		case !d.IsDir() && strings.HasSuffix(rel, ".md") && d.Type().IsRegular():
			files = append(files, rel)
		}
		return nil
	})
	slices.Sort(files)
	if err == nil && len(files) == 0 {
		err = fmt.Errorf("no markdown files found — wrong root, or everything excluded")
	}
	return files, err
}

func readFile(root, rel string) (string, error) {
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if len(raw) > maxFile {
		err = fmt.Errorf("%s is over the %d-byte file cap; exclude it with --exclude", rel, maxFile)
	}
	return string(raw), err
}

func (r *roots) build() (*corpus, error) {
	c := &corpus{df: map[string]int{}, post: map[string][]int{}, byClass: map[string]int{}}
	total := 0
	for _, root := range r.root {
		files, err := r.walk(root)
		if err != nil {
			return nil, fmt.Errorf("building the index over %s: %v", esc(root), err)
		}
		c.files = append(c.files, files...)
		for _, f := range files {
			raw, err := readFile(root, f)
			if err != nil {
				return nil, err
			}
			c.bytes += len(raw)
			text := lf(raw)
			name, typ, end := frontmatter(text)
			text = strings.Repeat("\n", strings.Count(text[:end], "\n")) + text[end:] // keep line numbers
			class := "."
			if i := strings.IndexByte(f, '/'); i >= 0 {
				class = f[:i]
			}
			line := 1
			for _, p := range strings.Split(text, "\n\n") {
				start := line + len(p) - len(strings.TrimLeft(p, "\n"))
				line += strings.Count(p, "\n") + 2
				ts := terms(p)
				if len(ts) < minTerms {
					continue
				}
				tf := map[string]int{}
				for _, t := range ts {
					tf[t]++
				}
				id := len(c.chunks)
				c.chunks = append(c.chunks, chunk{f, class, root, strings.Trim(p, "\n"), name, typ, start, len(ts), tf})
				for t := range tf {
					c.df[t]++
					c.post[t] = append(c.post[t], id)
				}
				c.byClass[class]++
				total += len(ts)
			}
		}
	}
	if len(c.chunks) == 0 {
		return nil, fmt.Errorf("the corpus holds no indexable paragraph")
	}
	c.avg = float64(total) / float64(len(c.chunks))
	return c, nil
}

type hit struct {
	chunk int
	rank  int // 0-based rank among scored chunks
	score float64
}

// query ranks the chunks by BM25, best first (ties by chunk id).
func (c *corpus) query(text string) []hit {
	scores := map[int]float64{}
	seen := map[string]bool{}
	for _, t := range terms(text) {
		if seen[t] || c.post[t] == nil {
			seen[t] = true
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

// retrieve is the k best files for text: each file's best paragraph, fused= 1/(60+rank).
// A tail of hits whose score rounds to 0.00 is dropped unless every hit does.
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

func (c *corpus) hitLine(token, prefix string, n int, h hit) string {
	ch := c.chunks[h.chunk]
	or := func(s string) string {
		if s == "" {
			return "-"
		}
		return field(s)
	}
	return fmt.Sprintf("%s HIT %srank=%d score=%.2f score-channel=bm25 fused=%.5f class=%s name=%s type=%s root=%s: %s:%d %q\n",
		token, prefix, n, h.score, 1/(60+float64(h.rank)), field(ch.class), or(ch.fmName), or(ch.fmType), or(ch.root), esc(ch.file), ch.line, truncate(ch.text, 120))
}

func runMemory(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "MEMORY REFUSED: no verb given; the verbs are stats, search, check, verify; run: nova-cairn help")
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("nova-cairn memory "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "run: nova-cairn help") }
	var r roots
	var coverage, front, exempt multi
	fs.Var(&r.root, "root", "corpus root directory, repeatable (required)")
	fs.Var(&r.exclude, "exclude", "path or glob to skip, repeatable")
	channels := fs.String("channels", "", "retrieval channel: bm25 (required for search and check)")
	k := fs.Int("k", 0, "receipts per query, positive (required for search and check)")
	links := fs.String("links", "", "gate|info: whether unresolved wikilinks drive the exit code (verify)")
	fs.Var(&coverage, "coverage", "A:B glob pair: every file matching A is named in some file matching B (verify)")
	fs.Var(&front, "frontmatter", "glob whose files must carry a frontmatter name: (verify)")
	fs.Var(&exempt, "exempt", "basename prefix exempt from --frontmatter (verify)")
	failMax := fs.Int("fail-max", 20, "finding lines to print per kind before one MORE line; 0 prints all (verify)")
	flags, pos := split(args[1:])
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	refuse := func(why string) int {
		fmt.Fprintf(stderr, "%s REFUSED: %s; run: nova-cairn help\n", strings.ToUpper(verb), esc(why))
		return 2
	}
	if !slices.Contains([]string{"stats", "search", "check", "verify"}, verb) {
		return refuse(fmt.Sprintf("unknown verb %q; the verbs are stats, search, check, verify", verb))
	}
	if len(r.root) == 0 {
		return refuse("--root is required: there is no environment variable and no discovery from the working directory")
	}
	retrieval := verb == "search" || verb == "check"
	if retrieval && *channels != "bm25" {
		return refuse("--channels bm25 is required: bm25 is the one channel this build has")
	}
	if retrieval && *k <= 0 {
		return refuse("--k must be positive: k is the budget of receipts, and zero is not unlimited")
	}
	if verb == "verify" {
		return verify(&r, *links, coverage, front, exempt, *failMax, stdout, stderr)
	}
	for _, root := range r.root {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return refuse(fmt.Sprintf("--root %s is not a readable directory", esc(root)))
		}
	}
	// check reads its draft before building: a bad draft is refused first.
	var candidates []string
	source := "-"
	switch {
	case verb == "search" && len(pos) == 0:
		return refuse("no query words given; refusing to guess")
	case verb == "check" && len(pos) != 1:
		return refuse("name exactly one candidate file, or - for stdin; refusing to guess")
	case verb == "check":
		in := stdin
		if pos[0] != "-" {
			f, err := os.Open(pos[0])
			if err != nil {
				return refuse(err.Error())
			}
			defer f.Close()
			in, source = f, pos[0]
		}
		raw, err := io.ReadAll(in)
		if err != nil {
			return refuse(fmt.Sprintf("reading %s: %v", esc(source), err))
		}
		for _, p := range strings.Split(lf(string(raw)), "\n\n") {
			if len(terms(p)) >= minTerms {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			return refuse(fmt.Sprintf("%s holds no candidate paragraph of at least %d terms; nothing to check", esc(source), minTerms))
		}
	}
	t0 := time.Now()
	c, err := r.build()
	if err != nil {
		return refuse(err.Error())
	}
	if verb == "stats" {
		fmt.Fprintf(stdout, "STATS OK schema=%s files=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
			schema, len(c.files), len(c.chunks), c.bytes, len(c.df), c.avg, time.Since(t0))
		for _, cl := range slices.Sorted(maps.Keys(c.byClass)) {
			fmt.Fprintf(stdout, "STATS OK class=%s chunks=%d\n", field(cl), c.byClass[cl])
		}
		return 0
	}
	token, prefix := "SEARCH", ""
	cal := "score=- score-channel=-"
	if h := c.retrieve(probe, 1); len(h) > 0 {
		cal = fmt.Sprintf("score=%.2f score-channel=bm25", h[0].score)
	}
	if verb == "search" {
		q := strings.Join(pos, " ")
		hits := c.retrieve(q, *k)
		fmt.Fprintf(stdout, "SEARCH OK hits=%d k=%d channels=bm25 files=%d chunks=%d: query=%s\n", len(hits), *k, len(c.files), len(c.chunks), quote(q))
		fmt.Fprintf(stdout, "SEARCH CAL %s probe=unrelated-control\n", cal)
		c.printHits(stdout, token, prefix, hits)
		fmt.Fprintf(stdout, "SEARCH NOTE %s\n", noteLex)
		return 0
	}
	token = "MEMORY"
	fmt.Fprintf(stdout, "MEMORY OK candidates=%d source=%s k=%d channels=bm25 files=%d chunks=%d\n", len(candidates), field(source), *k, len(c.files), len(c.chunks))
	fmt.Fprintf(stdout, "MEMORY CAL %s probe=unrelated-control\n", cal)
	for i, cand := range candidates {
		fmt.Fprintf(stdout, "MEMORY CAND n=%d: %s\n", i+1, quote(truncate(strings.TrimSpace(cand), 100)))
		c.printHits(stdout, token, fmt.Sprintf("cand=%d ", i+1), c.retrieve(cand, *k))
	}
	fmt.Fprintf(stdout, "MEMORY NOTE %s\n", noteLex)
	fmt.Fprintln(stdout, "MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours")
	fmt.Fprintln(stdout, "MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction")
	return 0
}

func (c *corpus) printHits(w io.Writer, token, prefix string, hits []hit) {
	if len(hits) == 0 {
		fmt.Fprintf(w, "%s MISS %severy query term is out of vocabulary for this corpus\n", token, prefix)
	}
	for i, h := range hits {
		fmt.Fprint(w, c.hitLine(token, prefix, i+1, h))
	}
}
