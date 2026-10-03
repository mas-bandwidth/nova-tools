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
// not run. The dispatch, the banner, the help, the version verb and the
// refusal line are internal/tool's (docs/STANDARD.md section 2).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The three hints turn a missing flag into a next step. Each says what the
// flag is and what a first run should put there (ONBOARDING point 2). They
// are the flags' wants text, so the skeleton's refusal names them.
const (
	rootHint     = `--root <dir> is your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run — and repeat it to index several roots in one ranking (the cairn beside memory/)`
	channelsHint = `--channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start`
	kHint        = `--k is the number of hits to return and is required (search: 3 to 5; check: 2 or 3 per paragraph)`
	linksHint    = `--links gate makes unresolved wikilinks fail; --links info reports them without failing`
)

// calibrationProbe is a fixed, corpus-unrelated English sentence, scored once
// per run so every report carries a live negative band. Changing it is a
// schema change; it is part of what memindex.SchemaVersion names.
const calibrationProbe = "the quarterly marketing budget for the regional office needs revised headcount projections before the fiscal deadline"

// noteLexical prints on every retrieval run, pass or fail. This is a lexical
// index and nothing else.
const noteLexical = "lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic"

// failMaxRemedy is the second half of every MORE line verify and eval print.
const failMaxRemedy = "--fail-max <n> raises the ceiling, --fail-max 0 prints every finding"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return memoryTool().Run(liftWords(args), stdin, stdout, stderr)
}

// memoryTool is the command: its verbs, and the shared skeleton that
// dispatches, refuses and renders (docs/STANDARD.md section 2).
func memoryTool() *tool.Tool {
	return &tool.Tool{
		Name:  "nova-memory",
		What:  "search your own markdown notes, and check a draft against what they already say",
		Stamp: version,
		How: `each run reads the --root trees and builds an index in memory; nothing is written.
search and check print receipts; verify and eval gate; the tree is the store.
CAL is what a fixed unrelated probe scores here, the band a hit is read against.
first run: quickstart --root on any folder of .md files.`,
		ExitTable: "0 ran and passed, 1 ran and failed, 2 could not run (bad invocation).",
		Verbs: []tool.Verb{
			{
				Name:    "quickstart",
				Usage:   "quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]...",
				Example: "quickstart --root ./corpus",
				Effect:  tool.Inspection,
				Flags:   quickstartFlags,
				Run:     cmdQuickstart,
			},
			{
				Name:   "stats",
				Usage:  "stats --root <dir>... [--exclude <glob>]...",
				Effect: tool.Inspection,
				Flags:  statsFlags,
				Run:    cmdStats,
			},
			{
				Name:    "search",
				Usage:   "search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <words>...",
				Example: "search --root ./corpus --channels bm25 --k 3 lantern glazing brass",
				Effect:  tool.Inspection,
				Flags:   searchFlags,
				Run:     cmdSearch,
			},
			{
				Name:    "check",
				Usage:   "check --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <file|->",
				Example: "check --root ./corpus --channels bm25 --k 3 draft.md",
				Effect:  tool.Inspection,
				Flags:   checkFlags,
				Run:     cmdCheck,
			},
			{
				Name:    "verify",
				Usage:   "verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]... [--fail-max <n>]",
				Example: "verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md",
				Effect:  tool.Inspection,
				Flags:   verifyFlags,
				Run:     cmdVerify,
			},
			{
				Name:   "eval",
				Usage:  "eval --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]... [--fail-max <n>] <gold.tsv>",
				Effect: tool.Inspection,
				Flags:  evalFlags,
				Run:    cmdEval,
			},
			{
				Name:   "boot",
				Usage:  "boot --root <dir> --pin <file>",
				Effect: tool.Inspection,
				Flags:  bootFlags,
				Run:    cmdBoot,
			},
		},
	}
}

// liftWords carries the free words of search, check and eval on --words.
// internal/tool refuses a positional on every verb but one default, and three
// verbs here take free words (docs/CLI.md). The skeleton still dispatches.
func liftWords(args []string) []string {
	if len(args) < 2 || (args[0] != "search" && args[0] != "check" && args[0] != "eval") {
		return args
	}
	head, tail, ok := splitWords(args)
	if !ok {
		return args
	}
	for _, w := range tail {
		head = append(head, "--words", w)
	}
	return head
}

func splitWords(args []string) (head, tail []string, ok bool) {
	takes := map[string]bool{"root": true, "exclude": true, "channels": true, "k": true, "floor": true, "fail-max": true, "words": true}
	head = []string{args[0]}
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return head, args[i+1:], true
		}
		if len(a) < 2 || a[0] != '-' {
			return head, args[i:], true
		}
		name, _, eq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		v, known := takes[name]
		if !known && name != "json" && name != "h" && name != "help" {
			return nil, nil, false
		}
		head = append(head, a)
		if v && !eq {
			i++
			if i >= len(args) {
				return nil, nil, false
			}
			head = append(head, args[i])
		}
	}
	return head, nil, true
}

// multiFlag is a repeatable string flag. It starts empty and stays empty
// unless the caller says otherwise: scope is never inherited. It does not
// split on commas, because a path may hold one.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }
func (m *multiFlag) Get() any           { return []string(*m) }

// rootFlags carries the flags every indexing verb needs to build an index.
type rootFlags struct {
	root     multiFlag
	excludes multiFlag
}

func addRootFlags(fs *flag.FlagSet) *rootFlags {
	r := &rootFlags{}
	fs.Var(&r.root, "root", rootHint+" (required)")
	fs.Var(&r.excludes, "exclude", "path or glob to skip, repeatable (nothing is excluded by default)")
	return r
}

func (r *rootFlags) excluded(p string) bool {
	for _, e := range r.excludes {
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
		if ok, _ := path.Match(e, p); ok {
			return true
		}
	}
	return false
}

func rootsOf(c *tool.Call) *rootFlags {
	return &rootFlags{root: multiFlag(c.Get("root").([]string)), excludes: multiFlag(c.Get("exclude").([]string))}
}

func wordsOf(c *tool.Call) []string { return c.Get("words").([]string) }

func wantRoot(c *tool.Call) {
	if len(c.Get("root").([]string)) == 0 {
		c.Problem("--root is required; it wants " + rootHint + "; refusing to guess")
	}
}

func wantK(c *tool.Call) {
	if !c.Given("k") {
		c.Problem("--k is required; it wants " + kHint + "; refusing to guess")
		return
	}
	if c.Int("k") <= 0 {
		c.Problem(fmt.Sprintf("--k must be a positive receipt budget (got %d); refusing to guess", c.Int("k")))
	}
}

func wantChannels(c *tool.Call) {
	if !c.Given("channels") {
		c.Problem("--channels is required; it wants " + channelsHint + "; refusing to guess")
		return
	}
	if strings.TrimSpace(c.Str("channels")) == "" {
		c.Problem("--channels named no channels; refusing to guess; " + channelsHint)
		return
	}
	if _, prob := channelNames(c.Str("channels")); prob != "" {
		c.Problem(prob)
	}
}

// channelNames validates the --channels list and returns the names in it. An
// unknown name is a problem, never a silent drop.
func channelNames(spec string) ([]string, string) {
	var out []string
	for _, name := range strings.Split(spec, ",") {
		switch n := strings.TrimSpace(name); n {
		case "bm25", "trigram":
			out = append(out, n)
		case "":
			return nil, fmt.Sprintf("--channels %q has an empty entry; refusing to guess", spec)
		default:
			return nil, fmt.Sprintf("unknown channel %q: %s", n, channelsHint)
		}
	}
	return out, ""
}

func newChannels(c *memindex.Corpus, names []string) []memindex.Channel {
	out := make([]memindex.Channel, 0, len(names))
	for _, name := range names {
		if name == "trigram" {
			out = append(out, memindex.NewTrigram(c))
			continue
		}
		out = append(out, memindex.NewBM25(c))
	}
	return out
}

func chanNames(chans []memindex.Channel) string {
	names := make([]string, 0, len(chans))
	for _, ch := range chans {
		names = append(names, ch.Name())
	}
	return strings.Join(names, ",")
}

func (r *rootFlags) build() (*memindex.Corpus, time.Duration, string) {
	t0 := time.Now()
	parts := make([]*memindex.Corpus, 0, len(r.root))
	for _, root := range r.root {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			return nil, 0, fmt.Sprintf("--root %s is not a readable directory", oneline.Escape(root))
		}
		c, err := memindex.Build(os.DirFS(root), r.excluded)
		if err != nil {
			return nil, 0, fmt.Sprintf("building the index over %s: %s", oneline.Escape(root), oneline.Err(err))
		}
		parts = append(parts, c)
	}
	return memindex.Merge(parts, r.root), time.Since(t0), ""
}

func scoreFields(score float64, chn string) string {
	if chn == "" {
		return "score=- score-channel=-"
	}
	return fmt.Sprintf("score=%.2f score-channel=%s", score, chn)
}

func hitLine(token, prefix string, rank int, h memindex.FileHit) string {
	name, typ := h.FMName, h.FMType
	if name == "" {
		name = "-"
	}
	if typ == "" {
		typ = "-"
	}
	root := h.Root
	if root == "" {
		root = "-"
	}
	return fmt.Sprintf("%s HIT %srank=%d %s fused=%.5f class=%s name=%s type=%s root=%s: %s:%d %q\n",
		token, prefix, rank, scoreFields(h.Native, h.NativeChan), h.Fused, oneline.Field(h.Class), oneline.Field(name), oneline.Field(typ), oneline.Field(root), oneline.Escape(h.File), h.Line, h.Snippet)
}

func quickstartFlags(f *tool.Flags) {
	// Prints stays: the echoed commands and the RUN, DEMO and OK lines are not one Out.
	f.Prints()
	addRootFlags(f.FlagSet)
	var words multiFlag
	f.Var(&words, "words", "word for the demonstration search, repeatable (default: the corpus's three most frequent non-function words)")
	f.String("draft", "", "candidate file for the demonstration check (default: this corpus's own first paragraph)")
	f.Check(func(c *tool.Call) {
		wantRoot(c)
		if c.Given("draft") && strings.TrimSpace(c.Str("draft")) == "" {
			c.Problem("--draft names a candidate file; omit it to use this corpus's own first paragraph")
		}
	})
}

func statsFlags(f *tool.Flags) {
	// Prints stays: one OK line per class, which Out would render as an item rather than a second head.
	f.Prints()
	addRootFlags(f.FlagSet)
	f.Check(wantRoot)
}

func searchFlags(f *tool.Flags) {
	retrievalFlags(f, "no query words given; refusing to guess", func(n int) bool { return n > 0 })
}

func checkFlags(f *tool.Flags) {
	retrievalFlags(f, "name exactly one candidate file, or - for stdin; refusing to guess", func(n int) bool { return n == 1 })
}

// retrievalFlags declares the flags search and check share. Prints is set
// because the skeleton cannot render the query prose tail, the MEMORY token
// or the file:line receipt (docs/STANDARD.md section 2); retrieval.go keeps
// that printer, named as kept.
func retrievalFlags(f *tool.Flags, wordsProblem string, wordsOK func(int) bool) {
	f.Prints()
	f.Bool("json", false, "render the retrieval result as JSON of the same evidence")
	addRootFlags(f.FlagSet)
	f.String("channels", "", channelsHint)
	f.Int("k", 0, kHint)
	var words multiFlag
	f.Var(&words, "words", "the free words after the flags; also accepted positionally")
	f.Check(func(c *tool.Call) {
		wantChannels(c)
		wantK(c)
		wantRoot(c)
		if !wordsOK(len(wordsOf(c))) {
			c.Problem(wordsProblem)
		}
	})
}

func verifyFlags(f *tool.Flags) {
	// Prints stays: INFO and FAIL lines carry a bare kind word, and Out has no field for that word.
	f.Prints()
	addRootFlags(f.FlagSet)
	f.String("links", "", linksHint+" (required)")
	var coverage, front, exempt multiFlag
	f.Var(&coverage, "coverage", "A:B glob pair, repeatable")
	f.Var(&front, "frontmatter", "glob whose files must carry a frontmatter name:, repeatable")
	f.Var(&exempt, "exempt", "basename prefix exempt from --frontmatter, repeatable (nothing is exempt by default)")
	f.Int("fail-max", bounded.Default, "finding lines to print per kind before one MORE line stands for the rest; 0 prints all")
	f.Check(func(c *tool.Call) { verifyChecks(c, coverage, front, exempt) })
}

func verifyChecks(c *tool.Call, coverage, front, exempt multiFlag) {
	wantRoot(c)
	if !c.Given("links") {
		c.Problem("--links is required; it wants " + linksHint + "; refusing to guess")
	} else if c.Str("links") != "gate" && c.Str("links") != "info" {
		c.Problem(fmt.Sprintf("--links must be gate or info (got %q); refusing to guess", c.Str("links")))
	}
	if c.Given("fail-max") && c.Int("fail-max") < 0 {
		c.Problem(fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", c.Int("fail-max")))
	}
	if len(c.Get("root").([]string)) > 1 {
		c.Problem(fmt.Sprintf("--root names exactly one tree for verification, but %d were given", len(c.Get("root").([]string))))
	}
	if c.Str("links") == "info" && len(coverage) == 0 && len(front) == 0 {
		c.Problem("no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification")
	}
	if len(exempt) > 0 && len(front) == 0 {
		c.Problem("--exempt only applies to --frontmatter, which was not given")
	}
	for _, pair := range coverage {
		if a, b, found := strings.Cut(pair, ":"); !found || a == "" || b == "" {
			c.Problem(fmt.Sprintf("--coverage wants A:B, got %q", pair))
		}
	}
}

func evalFlags(f *tool.Flags) {
	// Prints stays: the FAIL line is prose around recall@k, not a status word plus facts.
	f.Prints()
	addRootFlags(f.FlagSet)
	f.String("channels", "", channelsHint)
	f.Int("k", 0, kHint)
	f.Float64("floor", 0, "minimum recall@k in (0,1] (required)")
	f.Int("fail-max", bounded.Default, "MISS lines to print before one MORE line stands for the rest; 0 prints all")
	var words multiFlag
	f.Var(&words, "words", "the gold file; also accepted positionally")
	f.Check(func(c *tool.Call) {
		wantChannels(c)
		wantFloor(c)
		wantK(c)
		wantRoot(c)
		if c.Given("fail-max") && c.Int("fail-max") < 0 {
			c.Problem(fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", c.Int("fail-max")))
		}
		if len(wordsOf(c)) != 1 {
			c.Problem("name exactly one gold file; refusing to guess")
		}
	})
}

func wantFloor(c *tool.Call) {
	if !c.Given("floor") {
		c.Problem("--floor is required; it wants a minimum recall@k in (0,1]; refusing to guess")
		return
	}
	floor := c.Get("floor").(float64)
	if math.IsNaN(floor) || math.IsInf(floor, 0) || floor <= 0 || floor > 1 {
		c.Problem(fmt.Sprintf("--floor must be in (0,1] (got %g); a harness that cannot fail is not a measurement", floor))
	}
}

func bootFlags(f *tool.Flags) {
	f.Required("root", "the memory root directory")
	f.Required("pin", "the pin file naming the memories to load, one slash path per line")
	f.Check(func(c *tool.Call) {
		if c.Given("pin") && strings.TrimSpace(c.Str("pin")) == "" {
			c.Problem("--pin names the file listing the memories to load; name it")
		}
	})
}

const quickstartChoiceNote = "this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k"

const (
	quickstartSearchK = "3"
	quickstartCheckK  = "2"
)

var quickstartFunctionWords = map[string]bool{
	"one": true, "two": true, "three": true, "four": true, "five": true, "six": true,
	"seven": true, "eight": true, "nine": true, "ten": true, "both": true, "another": true,
	"about": true, "after": true, "again": true, "all": true, "also": true, "an": true,
	"and": true, "any": true, "are": true, "as": true, "at": true, "be": true,
	"because": true, "been": true, "before": true, "being": true, "but": true, "by": true,
	"can": true, "could": true, "did": true, "do": true, "does": true, "each": true,
	"even": true, "every": true, "for": true, "from": true, "had": true, "has": true,
	"have": true, "he": true, "her": true, "here": true, "him": true, "his": true,
	"how": true, "if": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "just": true, "me": true, "more": true, "most": true, "much": true,
	"must": true, "my": true, "no": true, "nor": true, "not": true, "of": true,
	"off": true, "on": true, "once": true, "only": true, "or": true, "other": true,
	"our": true, "out": true, "over": true, "own": true, "same": true,
	"she": true, "should": true, "so": true, "some": true, "such": true, "than": true,
	"that": true, "the": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "those": true, "through": true, "to": true,
	"too": true, "under": true, "until": true, "up": true, "us": true, "very": true,
	"was": true, "we": true, "were": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "who": true, "why": true, "will": true, "with": true,
	"would": true, "you": true, "your": true,
}

func commandLine(argv []string) string { return commandLineFor(argv, runtime.GOOS == "windows") }

func commandLineFor(argv []string, windows bool) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, shellArg(a, windows))
	}
	return strings.Join(parts, " ")
}

func shellArg(s string, windows bool) string {
	esc := oneline.Escape(s)
	if esc != "" && !needsQuoting(esc, windows) {
		return esc
	}
	if windows {
		return `"` + strings.ReplaceAll(esc, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(esc, "'", `'\''`) + "'"
}

func needsQuoting(s string, windows bool) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(`-_./=+:,@`, r):
		case windows && strings.ContainsRune(`\~`, r):
		default:
			return true
		}
	}
	return false
}

func step(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "$ nova-memory %s\n", commandLine(argv))
	return run(argv, stdin, stdout, stderr)
}

func topTerms(c *memindex.Corpus, n int) []string {
	terms := make([]string, 0, len(c.DF))
	for t := range c.DF {
		if !quickstartFunctionWords[t] {
			terms = append(terms, t)
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		if c.DF[terms[i]] != c.DF[terms[j]] {
			return c.DF[terms[i]] > c.DF[terms[j]]
		}
		return terms[i] < terms[j]
	})
	if len(terms) > n {
		terms = terms[:n]
	}
	return terms
}

func cmdQuickstart(c *tool.Call) *tool.Out {
	rf := rootsOf(c)
	words := multiFlag(wordsOf(c))
	draft := c.Str("draft")
	corpus, _, prob := rf.build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	if len(corpus.Chunks) == 0 {
		return tool.Refuse("this corpus holds no indexable paragraph; there is nothing to demonstrate on")
	}
	wordsSource := "given"
	if len(words) == 0 {
		words, wordsSource = topTerms(corpus, 3), "corpus-top-terms"
		if len(words) == 0 {
			return tool.Refuse("this corpus has no term to demonstrate a search with; name some with --words")
		}
	}
	candidate := "corpus-first-paragraph"
	if draft != "" {
		candidate = draft
	}
	var common []string
	for _, r := range rf.root {
		common = append(common, "--root", r)
	}
	for _, e := range rf.excludes {
		common = append(common, "--exclude", e)
	}
	fmt.Fprintf(c.Stdout, "QUICKSTART RUN root=%s steps=3 channels=bm25 k=%s/%s words=%s words-source=%s candidate=%s\n",
		oneline.Field(strings.Join(rf.root, " ")), quickstartSearchK, quickstartCheckK,
		oneline.Field(strings.Join(words, " ")), oneline.Field(wordsSource), oneline.Field(candidate))
	if code := step(append([]string{"stats"}, common...), strings.NewReader(""), c.Stdout, c.Stderr); code != 0 {
		return tool.Refuse(fmt.Sprintf("the stats step could not run (exit %d); nothing further was attempted", code))
	}
	searchArgs := append(append([]string{"search"}, common...), "--channels", "bm25", "--k", quickstartSearchK)
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			searchArgs = append(searchArgs, "--")
			break
		}
	}
	searchArgs = append(searchArgs, words...)
	if code := step(searchArgs, strings.NewReader(""), c.Stdout, c.Stderr); code != 0 {
		return tool.Refuse(fmt.Sprintf("the search step could not run (exit %d); nothing further was attempted", code))
	}
	checkArgs := append(append([]string{"check"}, common...), "--channels", "bm25", "--k", quickstartCheckK)
	checkIn := strings.NewReader("")
	if draft != "" {
		checkArgs = append(checkArgs, draft)
	} else {
		checkArgs = append(checkArgs, "-")
		checkIn = strings.NewReader(corpus.Chunks[0].Original)
		fmt.Fprintf(c.Stdout, "QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: %s:%d\n",
			oneline.Escape(corpus.Chunks[0].File), corpus.Chunks[0].Line)
	}
	if code := step(checkArgs, checkIn, c.Stdout, c.Stderr); code != 0 {
		return tool.Refuse(fmt.Sprintf("the check step could not run (exit %d); nothing further was attempted", code))
	}
	fmt.Fprintf(c.Stdout, "QUICKSTART OK done=3\n")
	fmt.Fprintf(c.Stdout, "QUICKSTART NOTE %s\n", quickstartChoiceNote)
	return tool.Exit(0)
}

func cmdStats(c *tool.Call) *tool.Out {
	corpus, buildTime, prob := rootsOf(c).build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	fmt.Fprintf(c.Stdout, "STATS OK schema=%s files=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
		memindex.SchemaVersion, len(corpus.Files), len(corpus.Chunks), corpus.Bytes, len(corpus.DF), corpus.AvgLen, buildTime)
	classes := make([]string, 0, len(corpus.ByClass))
	for cl := range corpus.ByClass {
		classes = append(classes, cl)
	}
	sort.Strings(classes)
	for _, cl := range classes {
		fmt.Fprintf(c.Stdout, "STATS OK class=%s chunks=%d\n", oneline.Field(cl), corpus.ByClass[cl])
	}
	return tool.Exit(0)
}

func cmdBoot(c *tool.Call) *tool.Out {
	n, bytes, prob := loadPin(c.Str("root"), c.Str("pin"))
	if prob != "" {
		return tool.Refuse(prob)
	}
	return tool.Done().Fact("files", n).Fact("bytes", bytes)
}

func loadPin(root, pin string) (int, int64, string) {
	entries, err := readPin(pin)
	if err != nil {
		return 0, 0, oneline.Err(err)
	}
	if len(entries) == 0 {
		return 0, 0, fmt.Sprintf("--pin %s names no memories; a boot of nothing is not a boot", oneline.Escape(pin))
	}
	var total int64
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if prob := pinEntry(e, seen); prob != "" {
			return 0, 0, prob
		}
		seen[e] = true
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(e)))
		if err != nil {
			return 0, 0, fmt.Sprintf("pin entry %q does not exist under --root", e)
		}
		if !fi.Mode().IsRegular() {
			return 0, 0, fmt.Sprintf("pin entry %q is not a regular file", e)
		}
		if fi.Size() == 0 {
			return 0, 0, fmt.Sprintf("pin entry %q is empty; a memory of zero bytes cannot be loaded", e)
		}
		total += fi.Size()
	}
	return len(entries), total, ""
}

func pinEntry(e string, seen map[string]bool) string {
	if strings.HasPrefix(e, "/") || filepath.IsAbs(e) {
		return fmt.Sprintf("pin entry %q is absolute; every entry is relative to --root", e)
	}
	if e != path.Clean(e) {
		return fmt.Sprintf("pin entry %q is not canonical (no \"./\", \"//\", \"..\" or trailing \"/\")", e)
	}
	if e == ".." || strings.HasPrefix(e, "../") {
		return fmt.Sprintf("pin entry %q escapes --root", e)
	}
	if seen[e] {
		return fmt.Sprintf("pin entry %q appears twice; double-counted bytes are a lie", e)
	}
	return ""
}

func readPin(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func cmdSearch(c *tool.Call) *tool.Out {
	names, _ := channelNames(c.Str("channels"))
	corpus, _, prob := rootsOf(c).build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	chans := newChannels(corpus, names)
	query := strings.Join(wordsOf(c), " ")
	hits := memindex.Retrieve(corpus, chans, query, c.Int("k"))
	result := retrievalResult{Verb: "search", Query: query, K: c.Int("k"), Channels: chanNames(chans), Files: len(corpus.Files), Chunks: len(corpus.Chunks), Calibration: calibrationHits(corpus, chans), Candidates: []retrievalCandidate{{Hits: hits}}, Notes: []string{noteLexical}}
	result.render(c.Stdout, c.Bool("json"))
	return tool.Exit(0)
}

func cmdCheck(c *tool.Call) *tool.Out {
	names, _ := channelNames(c.Str("channels"))
	src, name, prob := openCandidate(c)
	if prob != "" {
		return tool.Refuse(prob)
	}
	defer src.Close()
	raw, err := io.ReadAll(src)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("reading %s: %s", oneline.Escape(name), oneline.Err(err)))
	}
	var candidates []string
	for _, p := range strings.Split(memindex.NormalizeNewlines(string(raw)), "\n\n") {
		if len(memindex.Tokenize(p)) >= memindex.MinTerms {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return tool.Refuse(fmt.Sprintf("%s holds no candidate paragraph of at least %d terms; nothing to check", oneline.Escape(name), memindex.MinTerms))
	}
	corpus, _, prob := rootsOf(c).build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	chans := newChannels(corpus, names)
	result := retrievalResult{Verb: "check", Source: name, K: c.Int("k"), Channels: chanNames(chans), Files: len(corpus.Files), Chunks: len(corpus.Chunks), Calibration: calibrationHits(corpus, chans), Notes: []string{noteLexical, "this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours", "a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction"}}
	for _, cand := range candidates {
		result.Candidates = append(result.Candidates, retrievalCandidate{Text: memindex.Truncate(strings.TrimSpace(cand), 100), Hits: memindex.Retrieve(corpus, chans, cand, c.Int("k"))})
	}
	result.render(c.Stdout, c.Bool("json"))
	return tool.Exit(0)
}

func openCandidate(c *tool.Call) (io.ReadCloser, string, string) {
	arg := wordsOf(c)[0]
	if arg == "-" {
		return io.NopCloser(c.Stdin), "-", ""
	}
	f, err := os.Open(arg)
	if err != nil {
		return nil, "", err.Error()
	}
	return f, arg, ""
}

func cmdVerify(c *tool.Call) *tool.Out {
	rf := rootsOf(c)
	links := c.Str("links")
	corpus, _, prob := rf.build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	fsys := memindex.Excluding(os.DirFS(rf.root[0]), rf.excluded)
	gating, info, coverageN, frontN, prob := verifyFindings(c, fsys, corpus, links)
	if prob != "" {
		return tool.Refuse(prob)
	}
	infos := bounded.Grouped(c.Stdout, c.Int("fail-max"), "VERIFY", failMaxRemedy)
	for _, f := range info {
		infos.Line(f.Kind, fmt.Sprintf("VERIFY INFO %s: %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
	}
	infos.More()
	fails := bounded.Grouped(c.Stderr, c.Int("fail-max"), "VERIFY", failMaxRemedy)
	for _, f := range gating {
		fails.Line(f.Kind, fmt.Sprintf("VERIFY FAIL %s %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
	}
	fails.More()
	if fails.Total() > 0 {
		fmt.Fprintf(c.Stderr, "VERIFY FAIL gating=%d shown=%d info=%d coverage=%d frontmatter=%d links=%s\n",
			fails.Total(), fails.Shown(), infos.Total(), coverageN, frontN, oneline.Field(links))
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "VERIFY OK gating=0 info=%d shown=%d coverage=%d frontmatter=%d links=%s\n",
		infos.Total(), infos.Shown(), coverageN, frontN, oneline.Field(links))
	return tool.Exit(0)
}

func verifyFindings(c *tool.Call, fsys fs.FS, corpus *memindex.Corpus, links string) (gating, info []memindex.Finding, coverageN, frontN int, prob string) {
	for _, pair := range c.Get("coverage").([]string) {
		a, b, _ := strings.Cut(pair, ":")
		fnds, err := memindex.Coverage(fsys, a, b)
		if err != nil {
			return nil, nil, 0, 0, err.Error()
		}
		coverageN += len(fnds)
		gating = append(gating, fnds...)
	}
	exempt := c.Get("exempt").([]string)
	for _, g := range c.Get("frontmatter").([]string) {
		fnds, err := memindex.FrontmatterPresent(fsys, g, exempt)
		if err != nil {
			return nil, nil, 0, 0, err.Error()
		}
		frontN += len(fnds)
		gating = append(gating, fnds...)
	}
	wl, err := memindex.Wikilinks(fsys, corpus)
	if err != nil {
		return nil, nil, 0, 0, err.Error()
	}
	if links == "gate" {
		gating = append(gating, wl...)
	} else {
		info = wl
	}
	return gating, info, coverageN, frontN, ""
}

func cmdEval(c *tool.Call) *tool.Out {
	names, _ := channelNames(c.Str("channels"))
	rows, err := readGold(wordsOf(c)[0])
	if err != nil {
		return tool.Refuse(err.Error())
	}
	corpus, _, prob := rootsOf(c).build()
	if prob != "" {
		return tool.Refuse(prob)
	}
	chans := newChannels(corpus, names)
	k := c.Int("k")
	floor := c.Get("floor").(float64)
	misses := bounded.Capped(c.Stdout, c.Int("fail-max"), "EVAL", "miss", failMaxRemedy)
	hits := 0
	var mrr float64
	for _, row := range rows {
		rank := goldRank(corpus, chans, row, k)
		if rank != 0 {
			hits++
			mrr += 1.0 / float64(rank)
			continue
		}
		misses.Line(fmt.Sprintf("EVAL MISS query=%s expected=%s",
			oneline.Field(oneline.Cap(row.query, oneline.TailBytes)),
			oneline.Field(oneline.Cap(strings.Join(row.expected, ","), oneline.TailBytes))))
	}
	misses.More()
	recall := float64(hits) / float64(len(rows))
	mrr /= float64(len(rows))
	if recall < floor {
		fmt.Fprintf(c.Stderr, "EVAL FAIL recall@%d=%.3f below floor %.3f (%d/%d, misses=%d shown=%d, mrr=%.3f, channels=%s)\n",
			k, recall, floor, hits, len(rows), misses.Total(), misses.Shown(), mrr, chanNames(chans))
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "EVAL OK recall@%d=%.3f floor=%.3f rows=%d hits=%d misses=%d shown=%d mrr=%.3f channels=%s\n",
		k, recall, floor, len(rows), hits, misses.Total(), misses.Shown(), mrr, chanNames(chans))
	return tool.Exit(0)
}

func goldRank(c *memindex.Corpus, chans []memindex.Channel, row goldRow, k int) int {
	for i, h := range memindex.Retrieve(c, chans, row.query, k) {
		for _, e := range row.expected {
			if strings.Contains(h.File, e) {
				return i + 1
			}
		}
	}
	return 0
}

type goldRow struct {
	query    string
	expected []string
}

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
