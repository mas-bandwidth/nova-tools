// nova-memory makes membership in a markdown corpus a LOOKUP, never a scan.
//
// WHAT IT IS FOR. A mind that keeps its memory as markdown answers "do I
// already know this?" by re-reading everything it is, and that cost grows
// with every day lived: consolidating n new learnings against m existing ones
// is O(n*m), and m only ever gets bigger. This tool rebuilds an in-memory
// index from the corpus on every run and answers membership questions with
// top-k receipted candidates, so the mind's judgment budget per new learning
// is k — a constant — however large the corpus grows.
//
// WHAT IT REFUSES TO BE. Not a write path (it never touches the corpus), not
// a judge (the author decides after the receipts), not the boot (query for
// WORK, traverse for SELF — a relevance-ranked lens must not replace the
// linear read that meets what you did not ask for), and not authoritative
// (the tree is the store; the index stops existing when the process exits).
//
// Every path, every scope, and every budget comes from a flag. There are no
// defaults, no config file, and no environment variable: a missing flag is a
// refusal, never a guess. Exit 0 ran and passed, 1 ran and failed, 2 could
// not run.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
)

const usage = `nova-memory: membership is a lookup, never a scan (see SPEC.md)

usage:
  nova-memory stats  --root <dir> [--exclude <glob>]...
  nova-memory search --root <dir> --channels <list> --k <n> [--exclude <glob>]... <words>...
  nova-memory check  --root <dir> --channels <list> --k <n> [--exclude <glob>]... <file|->
  nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                     [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
  nova-memory eval   --root <dir> --channels <list> --k <n> --floor <f> [--exclude <glob>]... <gold.tsv>

flags:
  --root <dir>          the corpus root. Required, always: there is no
                        environment variable and no discovery from the working
                        directory. A tool that guesses which corpus you meant
                        can answer "you already know this" about someone else's.
  --channels <list>     comma-separated retrieval channels: bm25, trigram.
                        Required: which retrieval you ran is part of what an
                        answer means, and no channel set is right by default —
                        on the corpus this was ported from, eval measured
                        bm25+trigram WORSE than bm25 alone.
  --k <n>               receipts per query, positive. Required: k IS the mind's
                        budget, and zero is not "unlimited".
  --exclude <glob>      path or glob to skip, repeatable. Nothing is excluded
                        by default except .git; every exclusion is yours,
                        stated this run.
  --floor <f>           eval only: minimum recall@k, in (0,1]. Required — a
                        harness with no floor cannot fail, so its green is
                        worth nothing.
  --links <gate|info>   verify only: whether unresolved [[wikilinks]] drive the
                        exit code. Required — state it, do not inherit it.
  --coverage <A:B>      verify only, repeatable: every file matching glob A is
                        named in some file matching glob B.
  --frontmatter <glob>  verify only, repeatable: files matching must carry a
                        frontmatter name:.
  --exempt <prefix>     verify only, repeatable: basename prefixes that are
                        listings, not entries, and are exempt from
                        --frontmatter. Nothing is exempt by default.

exit codes: 0 ran and passed, 1 ran and failed, 2 could not run (bad invocation).
`

// calibrationProbe is a fixed, corpus-unrelated English sentence, scored once
// per run so every report carries a LIVE negative band — "unrelated text
// scores about this much on YOUR corpus" — instead of a stale number from
// someone else's. Changing it is a schema change; it is part of what
// memindex.SchemaVersion names.
const calibrationProbe = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"

// noteLexical prints on every retrieval run, pass or fail. This is a lexical
// index and nothing else: a green from a partial instrument reads exactly
// like a green from a complete one.
const noteLexical = "lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "stats":
		return cmdStats(args[1:], stdout, stderr)
	case "search":
		return cmdSearch(args[1:], stdout, stderr)
	case "check":
		return cmdCheck(args[1:], stdin, stdout, stderr)
	case "verify":
		return cmdVerify(args[1:], stdout, stderr)
	case "eval":
		return cmdEval(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "nova-memory: unknown subcommand %q\n\n%s", args[0], usage)
		return 2
	}
}

// ---------------------------------------------------------------------------
// Flag plumbing: the no-guessing rule, enforced once

// multiFlag is a repeatable string flag. It starts empty and stays empty
// unless the caller says otherwise — scope is never inherited.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// parse runs a subcommand flag set and enforces the no-guessing rule: every
// required flag must have been GIVEN. Whether it was given is asked of the
// flag set, not inferred from the value, so "--k 0" is a different (and
// differently worded) refusal from a missing --k.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) (ok bool) {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return false
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	sorted := append([]string(nil), required...)
	sort.Strings(sorted) // deterministic order, not map or caller order
	ok = true
	for _, name := range sorted {
		if !given[name] {
			fmt.Fprintf(stderr, "nova-memory %s: --%s is required; refusing to guess\n", fs.Name(), name)
			ok = false
		}
	}
	return ok
}

// rootFlags carries the flags every verb needs to build an index.
type rootFlags struct {
	root     *string
	excludes multiFlag
}

func addRootFlags(fs *flag.FlagSet) *rootFlags {
	r := &rootFlags{root: fs.String("root", "", "corpus root directory (required)")}
	fs.Var(&r.excludes, "exclude", "path or glob to skip, repeatable (nothing is excluded by default)")
	return r
}

// build derives the index, or explains why it could not. Every failure here
// is exit 2: the check could not run.
func (r *rootFlags) build(name string, stderr io.Writer) (*memindex.Corpus, time.Duration, bool) {
	fi, err := os.Stat(*r.root)
	if err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "nova-memory %s: --root %s is not a readable directory\n", name, *r.root)
		return nil, 0, false
	}
	exclude := func(p string) bool {
		for _, e := range r.excludes {
			if p == e || strings.HasPrefix(p, e+"/") {
				return true
			}
			// path.Match, not filepath.Match: corpus paths are always
			// slash-separated, on every platform.
			if ok, _ := path.Match(e, p); ok {
				return true
			}
		}
		return false
	}
	t0 := time.Now()
	c, err := memindex.Build(os.DirFS(*r.root), exclude)
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory %s: building the index over %s: %v\n", name, *r.root, err)
		return nil, 0, false
	}
	return c, time.Since(t0), true
}

// channelSpec turns the required --channels list into channels. An unknown
// name is a refusal, never a silent drop: a run that quietly used fewer
// channels than asked reports a number that means something else.
func channelSpec(c *memindex.Corpus, spec, verb string, stderr io.Writer) ([]memindex.Channel, bool) {
	if strings.TrimSpace(spec) == "" {
		fmt.Fprintf(stderr, "nova-memory %s: --channels named no channels; refusing to guess\n", verb)
		return nil, false
	}
	var out []memindex.Channel
	for _, name := range strings.Split(spec, ",") {
		switch strings.TrimSpace(name) {
		case "bm25":
			out = append(out, memindex.NewBM25(c))
		case "trigram":
			out = append(out, memindex.NewTrigram(c))
		case "":
			// A stray comma is a typo. Dropping it silently would run fewer
			// channels than the caller asked for and report the number under
			// a name that no longer describes it.
			fmt.Fprintf(stderr, "nova-memory %s: --channels %q has an empty entry; refusing to guess\n", verb, spec)
			return nil, false
		default:
			fmt.Fprintf(stderr, "nova-memory %s: unknown channel %q (have: bm25, trigram)\n", verb, strings.TrimSpace(name))
			return nil, false
		}
	}
	return out, true
}

func chanNames(chans []memindex.Channel) string {
	names := make([]string, 0, len(chans))
	for _, ch := range chans {
		names = append(names, ch.Name())
	}
	return strings.Join(names, ",")
}

// checkK refuses a non-positive receipt budget. k is the mind's budget, and
// zero does not mean unlimited.
func checkK(k int, verb string, stderr io.Writer) bool {
	if k <= 0 {
		fmt.Fprintf(stderr, "nova-memory %s: --k must be a positive receipt budget (got %d); refusing to guess\n", verb, k)
		return false
	}
	return true
}

// calibration scores the fixed unrelated probe once per run and returns the
// band: the top hit's native score and the channel that produced it. An empty
// channel name means the probe surfaced nothing at all — which is a different
// fact from "the probe scored zero", and must not print as one.
func calibration(c *memindex.Corpus, chans []memindex.Channel) (float64, string) {
	hits := memindex.Retrieve(c, chans, calibrationProbe, 1)
	if len(hits) == 0 {
		return 0, ""
	}
	return hits[0].Native, hits[0].NativeChan
}

// scoreFields renders the native-score pair. The score is the chunk's score in
// the channel that ACTUALLY surfaced it, and that channel is named on the same
// line, because a fused hit in a multi-channel run need not have been scored
// by the first channel named — and a fabricated 0.00 read against the
// calibration band says "weaker than unrelated control text" about a hit that
// was never scored there at all. "-" in both fields when nothing scored it, so
// the field count never changes.
func scoreFields(score float64, chn string) string {
	if chn == "" {
		return "score=- score-channel=-"
	}
	return fmt.Sprintf("score=%.2f score-channel=%s", score, chn)
}

// hitLine renders one receipt as a single machine-scannable line. Absent
// frontmatter prints as "-" so the field count never changes.
func hitLine(token, prefix string, rank int, h memindex.FileHit) string {
	name, typ := h.FMName, h.FMType
	if name == "" {
		name = "-"
	}
	if typ == "" {
		typ = "-"
	}
	return fmt.Sprintf("%s HIT %srank=%d %s fused=%.5f class=%s name=%s type=%s %s:%d %q\n",
		token, prefix, rank, scoreFields(h.Native, h.NativeChan), h.Fused, h.Class, name, typ, h.File, h.Para, h.Snippet)
}

// ---------------------------------------------------------------------------
// stats — m, measured

func cmdStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	rf := addRootFlags(fs)
	if !parse(fs, args, stderr, "root") {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-memory stats: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	c, buildTime, ok := rf.build("stats", stderr)
	if !ok {
		return 2
	}
	// build= is the one field that is not byte-reproducible: it is a measured
	// duration, labelled as one. Everything else is derived from the tree.
	fmt.Fprintf(stdout, "STATS OK schema=%s files=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
		memindex.SchemaVersion, len(c.Files), len(c.Chunks), c.Bytes, len(c.DF), c.AvgLen, buildTime)
	classes := make([]string, 0, len(c.ByClass))
	for cl := range c.ByClass {
		classes = append(classes, cl)
	}
	sort.Strings(classes)
	for _, cl := range classes {
		fmt.Fprintf(stdout, "STATS OK class=%s chunks=%d\n", cl, c.ByClass[cl])
	}
	return 0
}

// ---------------------------------------------------------------------------
// search — one query, k receipts

func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per query, positive (required)")
	if !parse(fs, args, stderr, "root", "channels", "k") {
		return 2
	}
	if !checkK(*k, "search", stderr) {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "nova-memory search: no query words given; refusing to guess")
		return 2
	}
	query := strings.Join(fs.Args(), " ")
	c, _, ok := rf.build("search", stderr)
	if !ok {
		return 2
	}
	chans, ok := channelSpec(c, *channels, "search", stderr)
	if !ok {
		return 2
	}
	hits := memindex.Retrieve(c, chans, query, *k)
	fmt.Fprintf(stdout, "SEARCH OK query=%q hits=%d k=%d channels=%s files=%d chunks=%d\n",
		query, len(hits), *k, chanNames(chans), len(c.Files), len(c.Chunks))
	fmt.Fprintf(stdout, "SEARCH CAL %s probe=unrelated-control\n", scoreFields(calibration(c, chans)))
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "SEARCH MISS every query term is out of vocabulary for this corpus")
	}
	for i, h := range hits {
		fmt.Fprint(stdout, hitLine("SEARCH", "", i+1, h))
	}
	fmt.Fprintf(stdout, "SEARCH NOTE %s\n", noteLexical)
	return 0
}

// ---------------------------------------------------------------------------
// check — the consolidation gate that never judges

func cmdCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per candidate, positive (required)")
	if !parse(fs, args, stderr, "root", "channels", "k") {
		return 2
	}
	if !checkK(*k, "check", stderr) {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "nova-memory check: name exactly one candidate file, or - for stdin; refusing to guess")
		return 2
	}

	src := stdin
	name := "-"
	if fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(stderr, "nova-memory check: %v\n", err)
			return 2
		}
		defer f.Close()
		src, name = f, fs.Arg(0)
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory check: reading %s: %v\n", name, err)
		return 2
	}
	var candidates []string
	// The same line-ending normalization memindex.Build does before its own
	// blank-line split: a CRLF candidate file must chunk into the paragraphs
	// its LF twin does, or check queries one giant blob against a corpus that
	// was indexed paragraph by paragraph.
	for _, p := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n") {
		if len(memindex.Tokenize(p)) >= memindex.MinTerms {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		// Unusable input, not a verdict: a run over nothing must never print
		// a green that a caller reads as "nothing was already known".
		fmt.Fprintf(stderr, "nova-memory check: %s holds no candidate paragraph of at least %d terms; nothing to check\n", name, memindex.MinTerms)
		return 2
	}

	c, _, ok := rf.build("check", stderr)
	if !ok {
		return 2
	}
	chans, ok := channelSpec(c, *channels, "check", stderr)
	if !ok {
		return 2
	}

	fmt.Fprintf(stdout, "MEMORY OK candidates=%d source=%s k=%d channels=%s files=%d chunks=%d\n",
		len(candidates), name, *k, chanNames(chans), len(c.Files), len(c.Chunks))
	fmt.Fprintf(stdout, "MEMORY CAL %s probe=unrelated-control\n", scoreFields(calibration(c, chans)))
	for i, cand := range candidates {
		fmt.Fprintf(stdout, "MEMORY CAND n=%d %q\n", i+1, memindex.Truncate(memindex.Normalize(cand), 100))
		hits := memindex.Retrieve(c, chans, cand, *k)
		if len(hits) == 0 {
			fmt.Fprintf(stdout, "MEMORY MISS cand=%d every query term is out of vocabulary for this corpus\n", i+1)
			continue
		}
		for j, h := range hits {
			fmt.Fprint(stdout, hitLine("MEMORY", fmt.Sprintf("cand=%d ", i+1), j+1, h))
		}
	}
	fmt.Fprintf(stdout, "MEMORY NOTE %s\n", noteLexical)
	fmt.Fprintln(stdout, "MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours")
	// Class-relative, deliberately: the corpus classifies itself by top-level
	// directory and this tool assumes nothing whatever about layout, so the
	// NOTE must not talk as if every adopter's tree has one canonical memory
	// directory the way the line this was written on does.
	fmt.Fprintln(stdout, "MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction")
	return 0
}

// ---------------------------------------------------------------------------
// verify — the coverage ritual, mechanized

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	rf := addRootFlags(fs)
	links := fs.String("links", "", "gate|info: whether unresolved wikilinks drive the exit code (required)")
	var coverage, front, exempt multiFlag
	fs.Var(&coverage, "coverage", "A:B glob pair, repeatable")
	fs.Var(&front, "frontmatter", "glob whose files must carry a frontmatter name:, repeatable")
	fs.Var(&exempt, "exempt", "basename prefix exempt from --frontmatter, repeatable (nothing is exempt by default)")
	if !parse(fs, args, stderr, "root", "links") {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-memory verify: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	gateLinks := false
	switch *links {
	case "gate":
		gateLinks = true
	case "info":
		gateLinks = false
	default:
		fmt.Fprintf(stderr, "nova-memory verify: --links must be gate or info (got %q); refusing to guess\n", *links)
		return 2
	}
	if len(coverage) == 0 && len(front) == 0 && !gateLinks {
		// Every check is off and wikilinks are informational: this run can
		// only ever exit 0. A green that could not have been anything else is
		// not a check, so it is refused rather than printed.
		fmt.Fprintln(stderr, "nova-memory verify: no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification")
		return 2
	}
	if len(exempt) > 0 && len(front) == 0 {
		fmt.Fprintln(stderr, "nova-memory verify: --exempt only applies to --frontmatter, which was not given")
		return 2
	}

	c, _, ok := rf.build("verify", stderr)
	if !ok {
		return 2
	}
	fsys := os.DirFS(*rf.root)

	var gating, info []memindex.Finding
	for _, pair := range coverage {
		a, b, found := strings.Cut(pair, ":")
		if !found || a == "" || b == "" {
			fmt.Fprintf(stderr, "nova-memory verify: --coverage wants A:B, got %q\n", pair)
			return 2
		}
		fnds, err := memindex.Coverage(fsys, a, b)
		if err != nil {
			fmt.Fprintf(stderr, "nova-memory verify: %v\n", err)
			return 2
		}
		gating = append(gating, fnds...)
	}
	for _, g := range front {
		fnds, err := memindex.FrontmatterPresent(fsys, g, exempt)
		if err != nil {
			fmt.Fprintf(stderr, "nova-memory verify: %v\n", err)
			return 2
		}
		gating = append(gating, fnds...)
	}
	wl, err := memindex.Wikilinks(fsys, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory verify: %v\n", err)
		return 2
	}
	if gateLinks {
		gating = append(gating, wl...)
	} else {
		info = wl
	}

	for _, f := range info {
		fmt.Fprintf(stdout, "VERIFY INFO %s %s\n", f.Kind, f.Detail)
	}
	if len(gating) > 0 {
		for _, f := range gating {
			fmt.Fprintf(stderr, "VERIFY FAIL %s %s\n", f.Kind, f.Detail)
		}
		return 1
	}
	fmt.Fprintf(stdout, "VERIFY OK gating=0 info=%d coverage=%d frontmatter=%d links=%s\n",
		len(info), len(coverage), len(front), *links)
	return 0
}

// ---------------------------------------------------------------------------
// eval — the known-answer harness, shipped with the tool

func cmdEval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per query, positive (required)")
	floor := fs.Float64("floor", 0, "minimum recall@k in (0,1] (required)")
	if !parse(fs, args, stderr, "root", "channels", "k", "floor") {
		return 2
	}
	if !checkK(*k, "eval", stderr) {
		return 2
	}
	if *floor <= 0 || *floor > 1 {
		fmt.Fprintf(stderr, "nova-memory eval: --floor must be in (0,1] (got %g); a harness that cannot fail is not a measurement\n", *floor)
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "nova-memory eval: name exactly one gold file; refusing to guess")
		return 2
	}
	rows, err := readGold(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory eval: %v\n", err)
		return 2
	}

	c, _, ok := rf.build("eval", stderr)
	if !ok {
		return 2
	}
	chans, ok := channelSpec(c, *channels, "eval", stderr)
	if !ok {
		return 2
	}

	hits := 0
	var mrr float64
	for _, row := range rows {
		rank := 0
		for i, h := range memindex.Retrieve(c, chans, row.query, *k) {
			for _, e := range row.expected {
				if strings.Contains(h.File, e) {
					rank = i + 1
					break
				}
			}
			if rank != 0 {
				break
			}
		}
		if rank != 0 {
			hits++
			mrr += 1.0 / float64(rank)
			fmt.Fprintf(stdout, "EVAL HIT rank=%d query=%q\n", rank, row.query)
		} else {
			fmt.Fprintf(stdout, "EVAL MISS query=%q expected=%s\n", row.query, strings.Join(row.expected, ","))
		}
	}
	recall := float64(hits) / float64(len(rows))
	mrr /= float64(len(rows))
	if recall < *floor {
		fmt.Fprintf(stderr, "EVAL FAIL recall@%d=%.3f below floor %.3f (%d/%d, mrr=%.3f, channels=%s)\n",
			*k, recall, *floor, hits, len(rows), mrr, chanNames(chans))
		return 1
	}
	fmt.Fprintf(stdout, "EVAL OK recall@%d=%.3f floor=%.3f rows=%d hits=%d mrr=%.3f channels=%s\n",
		*k, recall, *floor, len(rows), hits, mrr, chanNames(chans))
	return 0
}

// goldRow is one known-answer case: a query, and the paths any one of which
// counts as the right answer.
type goldRow struct {
	query    string
	expected []string
}

// readGold parses the known-answer file: `query<TAB>path[,path]` per line,
// `#` comments and blank lines ignored. Every malformed shape is an error and
// never a skipped row — a row silently dropped, or a row that can never match
// because its expectation side is empty, moves the measured recall without
// moving anything the reader can see.
func readGold(name string) ([]goldRow, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []goldRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		// Trim line endings only. Trimming all whitespace first would eat the
		// TAB on a row whose query or expectation side is empty, and those two
		// malformed shapes would then report as "no TAB" — the wrong finding,
		// and the reason the empty-side rows below are refused at all.
		line := strings.TrimRight(sc.Text(), "\r\n")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		q, expects, found := strings.Cut(line, "\t")
		if !found {
			return nil, fmt.Errorf("%s line %d has no TAB (format: query<TAB>expected[,expected])", name, lineNo)
		}
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("%s line %d has an empty query", name, lineNo)
		}
		var expected []string
		for _, e := range strings.Split(expects, ",") {
			if e = strings.TrimSpace(e); e != "" {
				expected = append(expected, e)
			}
		}
		if len(expected) == 0 {
			return nil, fmt.Errorf("%s line %d names no expected path — a row that can never hit is a silent drag on recall, not a case", name, lineNo)
		}
		rows = append(rows, goldRow{query: q, expected: expected})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s holds zero rows — a broken harness, not a pass", name)
	}
	return rows, nil
}
