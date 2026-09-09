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
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-memory: membership is a lookup, never a scan (see SPEC.md)

usage:
  nova-memory quickstart --root <dir> [--words <w>]... [--draft <file>] [--exclude <glob>]...
  nova-memory stats  --root <dir> [--exclude <glob>]...
  nova-memory search --root <dir> --channels <list> --k <n> [--exclude <glob>]... <words>...
  nova-memory check  --root <dir> --channels <list> --k <n> [--exclude <glob>]... <file|->
  nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                     [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
  nova-memory eval   --root <dir> --channels <list> --k <n> --floor <f> [--exclude <glob>]... <gold.tsv>

quickstart is the first run and nothing else: it runs stats, then one search,
then one check, PRINTING each command line above that command's output, so
what you saw came from a line you can now edit and run yourself. It is not a
default channel or a default k — it names both on every line it prints, and
says so again at the end.

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
  --words <w>           quickstart only, repeatable: the words the
                        demonstration search runs. Default: the corpus's three
                        most frequent terms that are not function words, named
                        on the printed command line like any other choice.
  --draft <file>        quickstart only: the candidate the demonstration check
                        reads. Default: this corpus's own first paragraph, fed
                        on stdin, which shows you what "you already know this"
                        looks like when it is certainly true.

A refusal reports every flag it can see at once — two missing flags are two
sentences and one run, not two runs.

exit codes: 0 ran and passed, 1 ran and failed, 2 could not run (bad invocation).

example:
  nova-memory quickstart --root ./corpus
  nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
  nova-memory check  --root ./corpus --channels bm25 --k 3 draft.md
`

// The three hints below turn this binary's three most-hit refusals into a next
// step. The no-guessing law is unchanged — a missing flag is still exit 2 and
// still says "refusing to guess" — but a refusal that only names what was
// wrong leaves a first-time caller to guess what the flag wanted, which is the
// same guessing the tool refuses to do, moved onto the reader. Each hint says
// what the flag IS and what a first run should put there.
const (
	rootHint = `--root <dir> is your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run`
	// The same sentence serves the missing flag and the unknown name, because
	// naming a directory is exactly how the flag gets misread.
	channelsHint = `--channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start`
	kHint        = `--k is the number of hits to return and is required (search: 3 to 5; check: 2 or 3 per paragraph)`
)

// hintFor returns the already-indented hint line for a required flag, newline
// included, or "" for a flag whose own usage entry is the whole story. It
// returns package constants only, which is why printing its result is safe.
func hintFor(name string) string {
	switch name {
	case "root":
		return "  " + rootHint + "\n"
	case "channels":
		return "  " + channelsHint + "\n"
	case "k":
		return "  " + kHint + "\n"
	}
	return ""
}

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
	case "quickstart":
		return cmdQuickstart(args[1:], stdout, stderr)
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
//
// EVERY missing flag is reported, not the first: a first run that is two flags
// short must learn that in one run. The returned set says which flags were
// given, so a caller can check the VALUE of each flag it actually received and
// add those refusals to the same run — "--channels is required" and
// "--channels named a directory" must never both print about one invocation,
// and neither must a bad --k hide a bad --channels.
//
// given is nil when the arguments could not be parsed at all: nothing after
// that is knowable, so the caller stops rather than guessing which flags
// arrived.
//
// Package flag is given no stream: its error text quotes the argument it
// could not parse, raw, and its usage dump follows -- so an argument holding
// a newline authored a whole line of stderr before any code in this file ran.
// The refusal is printed here instead, escaped, and -h after a verb is refused
// at exit 2 like any other unusable invocation.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) (given map[string]bool, ok bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "nova-memory %s: %s\n\n%s", fs.Name(), oneline.Err(err), usage)
		return nil, false
	}
	given = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	sorted := append([]string(nil), required...)
	sort.Strings(sorted) // deterministic order, not map or caller order
	ok = true
	for _, name := range sorted {
		if !given[name] {
			fmt.Fprintf(stderr, "nova-memory %s: --%s is required; refusing to guess\n", fs.Name(), name)
			fmt.Fprint(stderr, hintFor(name))
			ok = false
		}
	}
	return given, ok
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
		fmt.Fprintf(stderr, "nova-memory %s: --root %s is not a readable directory\n", name, oneline.Escape(*r.root))
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
		fmt.Fprintf(stderr, "nova-memory %s: building the index over %s: %s\n", name, oneline.Escape(*r.root), oneline.Err(err))
		return nil, 0, false
	}
	return c, time.Since(t0), true
}

// channelNames validates the --channels list and returns the names in it. An
// unknown name is a refusal, never a silent drop: a run that quietly used
// fewer channels than asked reports a number that means something else.
//
// It is deliberately separate from building the channels, and needs no
// corpus, so a bad --channels is refused in the same run as a bad --k rather
// than a build later — the reader who typed a directory name into --channels
// is exactly the reader who should not have to run the tool three times to
// find their three mistakes.
func channelNames(spec, verb string, stderr io.Writer) ([]string, bool) {
	if strings.TrimSpace(spec) == "" {
		fmt.Fprintf(stderr, "nova-memory %s: --channels named no channels; refusing to guess\n  %s\n", verb, channelsHint)
		return nil, false
	}
	var out []string
	for _, name := range strings.Split(spec, ",") {
		switch n := strings.TrimSpace(name); n {
		case "bm25", "trigram":
			out = append(out, n)
		case "":
			// A stray comma is a typo. Dropping it silently would run fewer
			// channels than the caller asked for and report the number under
			// a name that no longer describes it.
			fmt.Fprintf(stderr, "nova-memory %s: --channels %q has an empty entry; refusing to guess\n", verb, spec)
			return nil, false
		default:
			fmt.Fprintf(stderr, "nova-memory %s: unknown channel %q: %s\n", verb, n, channelsHint)
			return nil, false
		}
	}
	return out, true
}

// newChannels builds the channels for names channelNames already accepted, so
// the only names reaching this switch are the two that exist.
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
// frontmatter prints as "-" so the field count never changes. The class, the
// name and the type are the corpus's own text and are fields, so each is one
// token; the file is a positional slot and keeps its spaces; the snippet is
// Go-quoted, which is one line in a different escape form.
func hitLine(token, prefix string, rank int, h memindex.FileHit) string {
	name, typ := h.FMName, h.FMType
	if name == "" {
		name = "-"
	}
	if typ == "" {
		typ = "-"
	}
	return fmt.Sprintf("%s HIT %srank=%d %s fused=%.5f class=%s name=%s type=%s: %s:%d %q\n",
		token, prefix, rank, scoreFields(h.Native, h.NativeChan), h.Fused, oneline.Field(h.Class), oneline.Field(name), oneline.Field(typ), oneline.Escape(h.File), h.Para, h.Snippet)
}

// ---------------------------------------------------------------------------
// quickstart — the first run, which SAYS what it chose

// The finished sentence a quickstart run ends on. The whole verb exists to
// buy the reader this line honestly: they have now seen the tool work, and
// they are told in the same breath that both numbers were picked for them
// THIS ONCE and are picked by nobody at all on the next run.
const quickstartChoiceNote = "this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k"

// quickstartK is the pair the demonstration runs on: 3 hits for one query,
// 2 receipts per candidate paragraph — the low end of what the --k hint tells
// a first run to use, so the output stays readable on a screen.
const (
	quickstartSearchK = "3"
	quickstartCheckK  = "2"
)

// quickstartFunctionWords is a chooser for the demonstration QUERY and is not
// a stopword list: nothing here is dropped from the index, from a query, or
// from a score. memindex deliberately has no stopwords (it measured them
// unnecessary), and this must not become one by the back door — it only keeps
// "the" and "and" from being what the tool shows a stranger as their corpus's
// three most characteristic words. The small cardinals are here for the same
// reason as "the": they are quantifiers, and a corpus of measurements is full
// of them.
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

// commandLine renders a step's argv as a line a reader can PASTE BACK into the
// shell of the machine that printed it. Every argument goes through
// oneline.Escape first, so the echo is one line whatever an argument holds —
// Escape keeps a path's spaces and its backslashes, which is what a path is
// made of. The quoting on top of that is the shell's, and WHICH shell is a
// platform fact rather than a style: rendering an argument as a Go string
// literal doubled every backslash in it, so on Windows the echoed check step
// named a path that does not exist, in a line that does not paste. Nothing is
// quoted that does not need it, so the ordinary case — a path of ordinary
// characters — is echoed verbatim on every platform.
func commandLine(argv []string) string {
	return commandLineFor(argv, runtime.GOOS == "windows")
}

// commandLineFor is commandLine with the platform passed in, so both shells'
// rules are testable from either one.
func commandLineFor(argv []string, windows bool) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, shellArg(a, windows))
	}
	return strings.Join(parts, " ")
}

// shellArg renders one argument for that platform's shell, and quotes only
// when the argument holds something the shell would otherwise act on.
func shellArg(s string, windows bool) string {
	esc := oneline.Escape(s)
	if esc != "" && !needsQuoting(esc, windows) {
		return esc
	}
	if windows {
		// cmd.exe and PowerShell both take a double-quoted argument literally,
		// backslashes included, which is exactly what a Windows path needs. A
		// double quote cannot appear in a Windows path at all; one arriving
		// from --words is doubled, which is how that shell spells its own
		// quote.
		return `"` + strings.ReplaceAll(esc, `"`, `""`) + `"`
	}
	// A single-quoted POSIX word is literal up to its closing quote, so the
	// backslashes, dollars and spaces inside it survive the paste. The one
	// character it cannot hold is its own quote, which is closed, escaped and
	// reopened.
	return "'" + strings.ReplaceAll(esc, "'", `'\''`) + "'"
}

// needsQuoting is true for every character but the ones a shell hands to the
// program unchanged. The list is deliberately short — anything unlisted is
// quoted, which is never wrong, only noisier — and it differs by platform in
// the two characters this bug was about: a backslash is a path separator on
// Windows and an escape on a POSIX shell, and a tilde is an ordinary character
// in a short Windows path (RUNNER~1) and an expansion on a POSIX one.
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

// step echoes one command line and then RUNS it, through the same dispatch a
// caller reaches from a shell. Echoing and running from one argv is the point:
// a printed command that was not what executed teaches an invocation that does
// not work, to exactly the reader who cannot tell.
func step(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "$ nova-memory %s\n", commandLine(argv))
	return run(argv, stdin, stdout, stderr)
}

// stepFailed reports a step that could not run. quickstart exits 0 only when
// all three ran: a partial demonstration that exited 0 would be teaching the
// green, and the green is the one thing this tool is careful about.
func stepFailed(verb string, code int, stderr io.Writer) int {
	fmt.Fprintf(stderr, "nova-memory quickstart: the %s step could not run (exit %d); nothing further was attempted\n", verb, code)
	return 2
}

// topTerms picks the demonstration query when the caller gave none: the terms
// present in the most chunks, function words aside. They are the corpus's own
// vocabulary rather than an invented query, which is the point — and because
// common terms are the WEAKEST BM25 evidence, the words are printed on the
// command line and named on the OK line, so what the reader sees is a real
// query they can improve rather than a good one they must trust.
//
// The order is total — count, then the term itself — because Go randomizes
// map iteration and two quickstart runs over one tree must print one thing.
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

func cmdQuickstart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	rf := addRootFlags(fs)
	var words multiFlag
	fs.Var(&words, "words", "word for the demonstration search, repeatable (default: the corpus's three most frequent non-function words)")
	draft := fs.String("draft", "", "candidate file for the demonstration check (default: this corpus's own first paragraph)")
	given, ok := parse(fs, args, stderr, "root")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-memory quickstart: unexpected argument %q; the words for the search go after --words\n", fs.Arg(0))
		bad = true
	}
	if given["draft"] && strings.TrimSpace(*draft) == "" {
		fmt.Fprintln(stderr, "nova-memory quickstart: --draft names a candidate file; omit it to use this corpus's own first paragraph")
		bad = true
	}
	if bad {
		return 2
	}

	// Built once here, before any step, only to choose what the caller did not:
	// the query words and the demonstration candidate. Each step below builds
	// its own index, because each step is a command the reader can run alone
	// and must behave identically when they do.
	c, _, ok := rf.build("quickstart", stderr)
	if !ok {
		return 2
	}
	if len(c.Chunks) == 0 {
		// memindex.Build refuses an empty corpus, so this is a guard and not a
		// path — stated rather than assumed, because the alternative is an
		// index panic on the friendliest verb in the tool.
		fmt.Fprintln(stderr, "nova-memory quickstart: this corpus holds no indexable paragraph; there is nothing to demonstrate on")
		return 2
	}
	wordsSource := "given"
	if len(words) == 0 {
		words, wordsSource = topTerms(c, 3), "corpus-top-terms"
		if len(words) == 0 {
			fmt.Fprintln(stderr, "nova-memory quickstart: this corpus has no term to demonstrate a search with; name some with --words")
			return 2
		}
	}
	candidate := "corpus-first-paragraph"
	if *draft != "" {
		candidate = *draft
	}

	// Flags first, then positionals: package flag stops at the first
	// non-flag argument, and every echoed line has to be one a reader can run.
	common := []string{"--root", *rf.root}
	for _, e := range rf.excludes {
		common = append(common, "--exclude", e)
	}
	fmt.Fprintf(stdout, "QUICKSTART OK root=%s steps=3 channels=bm25 k=%s/%s words=%s words-source=%s candidate=%s\n",
		oneline.Field(*rf.root), quickstartSearchK, quickstartCheckK,
		oneline.Field(strings.Join(words, " ")), oneline.Field(wordsSource), oneline.Field(candidate))

	statsArgs := append([]string{"stats"}, common...)
	if code := step(statsArgs, strings.NewReader(""), stdout, stderr); code != 0 {
		return stepFailed("stats", code, stderr)
	}

	searchArgs := append([]string{"search"}, common...)
	searchArgs = append(searchArgs, "--channels", "bm25", "--k", quickstartSearchK)
	searchArgs = append(searchArgs, words...)
	if code := step(searchArgs, strings.NewReader(""), stdout, stderr); code != 0 {
		return stepFailed("search", code, stderr)
	}

	checkArgs := append([]string{"check"}, common...)
	checkArgs = append(checkArgs, "--channels", "bm25", "--k", quickstartCheckK)
	checkIn := strings.NewReader("")
	if *draft != "" {
		checkArgs = append(checkArgs, *draft)
	} else {
		// The demonstration with the answer known: a paragraph the corpus
		// certainly holds, so a first run sees what "you already know this"
		// looks like when it is true, and can compare it against the
		// calibration band on the same screen.
		checkArgs = append(checkArgs, "-")
		checkIn = strings.NewReader(c.Chunks[0].Text)
		fmt.Fprintf(stdout, "QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: %s:%d\n",
			oneline.Escape(c.Chunks[0].File), c.Chunks[0].Para)
	}
	if code := step(checkArgs, checkIn, stdout, stderr); code != 0 {
		return stepFailed("check", code, stderr)
	}

	fmt.Fprintf(stdout, "QUICKSTART NOTE %s\n", quickstartChoiceNote)
	return 0
}

// ---------------------------------------------------------------------------
// stats — m, measured

func cmdStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	rf := addRootFlags(fs)
	given, ok := parse(fs, args, stderr, "root")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-memory stats: unexpected argument %q\n", fs.Arg(0))
		bad = true
	}
	if bad {
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
		fmt.Fprintf(stdout, "STATS OK class=%s chunks=%d\n", oneline.Field(cl), c.ByClass[cl])
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
	given, ok := parse(fs, args, stderr, "root", "channels", "k")
	if given == nil {
		return 2
	}
	// One run, every reason. A value is only judged when the flag carrying it
	// was given, so a missing flag says one thing and not two.
	bad := !ok
	if given["k"] && !checkK(*k, "search", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "search", stderr); !ok {
			bad = true
		}
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "nova-memory search: no query words given; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}
	query := strings.Join(fs.Args(), " ")
	c, _, ok := rf.build("search", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)
	hits := memindex.Retrieve(c, chans, query, *k)
	fmt.Fprintf(stdout, "SEARCH OK query=%s hits=%d k=%d channels=%s files=%d chunks=%d\n",
		oneline.Field(query), len(hits), *k, chanNames(chans), len(c.Files), len(c.Chunks))
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
	given, ok := parse(fs, args, stderr, "root", "channels", "k")
	if given == nil {
		return 2
	}
	bad := !ok
	if given["k"] && !checkK(*k, "check", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "check", stderr); !ok {
			bad = true
		}
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "nova-memory check: name exactly one candidate file, or - for stdin; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}

	src := stdin
	name := "-"
	if fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(stderr, "nova-memory check: %s\n", oneline.Err(err))
			return 2
		}
		defer f.Close()
		src, name = f, fs.Arg(0)
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory check: reading %s: %s\n", oneline.Escape(name), oneline.Err(err))
		return 2
	}
	var candidates []string
	// The same line-ending normalization memindex.Build does before its own
	// blank-line split: a CRLF candidate file must chunk into the paragraphs
	// its LF twin does, or check queries one giant blob against a corpus that
	// was indexed paragraph by paragraph.
	for _, p := range strings.Split(memindex.NormalizeNewlines(string(raw)), "\n\n") {
		if len(memindex.Tokenize(p)) >= memindex.MinTerms {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		// Unusable input, not a verdict: a run over nothing must never print
		// a green that a caller reads as "nothing was already known".
		fmt.Fprintf(stderr, "nova-memory check: %s holds no candidate paragraph of at least %d terms; nothing to check\n", oneline.Escape(name), memindex.MinTerms)
		return 2
	}

	c, _, ok := rf.build("check", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)

	fmt.Fprintf(stdout, "MEMORY OK candidates=%d source=%s k=%d channels=%s files=%d chunks=%d\n",
		len(candidates), oneline.Field(name), *k, chanNames(chans), len(c.Files), len(c.Chunks))
	fmt.Fprintf(stdout, "MEMORY CAL %s probe=unrelated-control\n", scoreFields(calibration(c, chans)))
	for i, cand := range candidates {
		fmt.Fprintf(stdout, "MEMORY CAND n=%d: %q\n", i+1, memindex.Truncate(memindex.Normalize(cand), 100))
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
	given, ok := parse(fs, args, stderr, "root", "links")
	if given == nil {
		return 2
	}
	bad := !ok
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-memory verify: unexpected argument %q\n", fs.Arg(0))
		bad = true
	}
	gateLinks := false
	if given["links"] {
		switch *links {
		case "gate":
			gateLinks = true
		case "info":
			gateLinks = false
		default:
			fmt.Fprintf(stderr, "nova-memory verify: --links must be gate or info (got %q); refusing to guess\n", *links)
			bad = true
		}
	}
	if bad {
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
			fmt.Fprintf(stderr, "nova-memory verify: %s\n", oneline.Err(err))
			return 2
		}
		gating = append(gating, fnds...)
	}
	for _, g := range front {
		fnds, err := memindex.FrontmatterPresent(fsys, g, exempt)
		if err != nil {
			fmt.Fprintf(stderr, "nova-memory verify: %s\n", oneline.Err(err))
			return 2
		}
		gating = append(gating, fnds...)
	}
	wl, err := memindex.Wikilinks(fsys, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory verify: %s\n", oneline.Err(err))
		return 2
	}
	if gateLinks {
		gating = append(gating, wl...)
	} else {
		info = wl
	}

	for _, f := range info {
		fmt.Fprintf(stdout, "VERIFY INFO %s: %s\n", f.Kind, oneline.Escape(f.Detail))
	}
	if len(gating) > 0 {
		for _, f := range gating {
			fmt.Fprintf(stderr, "VERIFY FAIL %s %s\n", f.Kind, oneline.Escape(f.Detail))
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
	given, ok := parse(fs, args, stderr, "root", "channels", "k", "floor")
	if given == nil {
		return 2
	}
	bad := !ok
	if given["k"] && !checkK(*k, "eval", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "eval", stderr); !ok {
			bad = true
		}
	}
	if given["floor"] && (*floor <= 0 || *floor > 1) {
		fmt.Fprintf(stderr, "nova-memory eval: --floor must be in (0,1] (got %g); a harness that cannot fail is not a measurement\n", *floor)
		bad = true
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "nova-memory eval: name exactly one gold file; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}
	rows, err := readGold(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "nova-memory eval: %s\n", oneline.Err(err))
		return 2
	}

	c, _, ok := rf.build("eval", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)

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
			fmt.Fprintf(stdout, "EVAL HIT rank=%d query=%s\n", rank, oneline.Field(row.query))
		} else {
			fmt.Fprintf(stdout, "EVAL MISS query=%s expected=%s\n", oneline.Field(row.query), oneline.Field(strings.Join(row.expected, ",")))
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
