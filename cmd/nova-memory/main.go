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
	"bytes"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const usage = `nova-memory: search your own markdown notes, and check a draft against what they already say

how it works: each run reads the --root directories and builds its index in
memory (bm25 words, trigrams); nothing is written. search prints the k best
passages with file:line and the quoted text; check names the notes a draft
repeats; verify gates links and frontmatter.
first run: quickstart --root on any folder of .md files, or create the small
corpus in setup: and run the lines under example:.

SEARCH CAL score=1.46 score-channel=bm25 probe=unrelated-control
is the CAL line every retrieval run prints. CAL is context, not a cutoff.
CAL is the top score of a fixed unrelated query, for scale; it does not
prove relevance, and a hit's score= at or below it is not noise: the probe
may be about your own notes, and a right answer can fall below it. search -h
prints the probe's own text. The example's search prints, as its rank 1 of 2:
SEARCH HIT rank=1 score=0.99 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/lantern.md:1 "The lantern glazing needs clean cloths for brass and glass."
class is the top-level directory ("." for root files); name/type are
frontmatter values, with "-" meaning absent.

usage:
  nova-memory version    print this build identity (--version also accepted)
  nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]... [--json]
  nova-memory stats  --root <dir>... [--exclude <glob>]... [--json]
  nova-memory search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <words>...
  nova-memory check  --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <file|->
  nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                     [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
                     [--fail-max <n>] [--json]
  nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]...
                     [--fail-max <n>] [--json] <gold.tsv>
  nova-memory boot   --root <dir> --pin <file> [--json]

quickstart is the first run and nothing else: it runs stats, then one search,
then one check, PRINTING each command line above that command's output, so
what you saw came from a line you can now edit and run yourself. It is not a
default channel or a default k — it names both on every line it prints, and
says so again at the end.

flags:
  --json                every verb but version: the same result as one JSON
                        object on stdout, a refusal included.
  --root <dir>          the corpus root. Required, always: there is no
                        environment variable and no discovery from the working
                        directory. Repeatable (--root <dir> --root <dir> ...):
                        several roots are indexed together in one ranking, and
                        every receipt names the root it came from. A tool that
                        guesses which corpus you meant can answer "you already
                        know this" about someone else's.
  --channels <list>     comma-separated retrieval channels: bm25, trigram.
                        Required: which retrieval you ran is part of what an
                        answer means, and no channel set is right by default:
                        eval can measure bm25+trigram worse than bm25 alone.
                        With two channels a hit is ranked by fused= (rank
                        fusion over the channels); its native score is named
                        for the channel that produced it (bm25= or trigram=),
                        beside score-channel=, which compares only with scores
                        of that channel and with the CAL line, so score= need not fall with rank.
  --k <n>               receipts per query, positive. Required: k IS the mind's
                        budget, and zero is not "unlimited".
  --exclude <glob>      path or glob to skip, repeatable. Nothing is excluded
                        by default except .git; every exclusion is yours,
                        stated this run.
  --whole               search and check: print each hit's whole paragraph in
                        place of its 120-byte snippet, so a word just past that
                        cut still prints. A paragraph past the byte cap is cut
                        at the cap and the dropped bytes are counted in the
                        value (...+<n>B), so the cut is never silent.
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
  --fail-max <n>        verify and eval only: how many finding lines to PRINT
                        before one MORE line stands for the rest. Default 20,
                        and 0 means all. The count is never capped -- the
                        summary line carries the total whether the run passed
                        or failed -- because a reader who wanted the number
                        should not have to pay for the list. verify caps each
                        KIND separately, so ten thousand wikilink findings
                        cannot bury the one frontmatter finding.
  --words <w>           quickstart only, repeatable: the words the
                        demonstration search runs. Default: the corpus's three
                        most frequent terms that are not function words, named
                        on the printed command line like any other choice.
  --draft <file>        quickstart only: the candidate the demonstration check
                        reads. Default: this corpus's own first paragraph, fed
                        on stdin, which shows you what "you already know this"
                        looks like when it is certainly true.
  --pin <file>          boot only: the pin file naming the memories to check
                        (checks the pin: every file present and readable, and
                        their size), one slash path per line relative to
                        --root (# comments and blank lines ignored). Required —
                        boot never walks the directory.

A refusal reports every flag it can see at once — two missing flags are two
sentences and one run, not two runs. Flags may stand before or after the
file or the query words; -- ends the flags, and a query word that starts
with - goes after it. Every verb is an inspection: it reads the corpus and
writes nothing (` + "`<verb> -h`" + ` says so, with the verb's flags).

exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
that finds nothing is still 0, and says so on its MISS line. check: 0 even
when the draft repeats a note (the example's check does: it hands you
receipts, and the verdict stays yours). verify: 0 clean, 1 a finding (a
wikilink finding gates only under --links gate). eval: 0 at or above
--floor, 1 recall@k under --floor. 2 could not run (bad invocation).

setup:
  mkdir -p ./corpus/notes
  printf 'The lantern glazing needs clean cloths for brass and glass.\n' > ./corpus/notes/lantern.md
  printf '[Lantern care](lantern.md) keeps the glazing clean.\n' > ./corpus/notes/index-notes.md
  cp ./corpus/notes/lantern.md ./draft.md

example:
  nova-memory quickstart --root ./corpus
  nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
  nova-memory check  --root ./corpus --channels bm25 --k 3 draft.md
  nova-memory verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md
`

// The three hints below turn this binary's three most-hit refusals into a next
// step. The no-guessing law is unchanged — a missing flag is still exit 2 and
// still says "refusing to guess" — but a refusal that only names what was
// wrong leaves a first-time caller to guess what the flag wanted, which is the
// same guessing the tool refuses to do, moved onto the reader. Each hint says
// what the flag IS and what a first run should put there.
const (
	rootHint = `--root <dir> is your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run — and repeat it to index several roots in one ranking`
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
	case "links":
		return "  --links gate makes unresolved wikilinks fail; --links info reports them without failing.\n"
	}
	return ""
}

// result is a verb's one result under --json, the same values its lines carry.
func result(verb string) *tool.Out {
	o := tool.Done()
	o.Verb = verb
	return o
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

// failMaxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index, so the line that says what was not
// shown says in the same breath how to see it.
const failMaxRemedy = "--fail-max <n> raises the ceiling, --fail-max 0 prints every finding"

// verbs are the verbs in the order the usage names them. Every one is an inspection: the
// index is built in memory from the corpus and nothing is written.
var verbs = []string{"quickstart", "stats", "search", "check", "verify", "eval", "boot", "version"}

// tokens are the words a verb's lines open with; check's are MEMORY.
var tokens = map[string]string{"check": "MEMORY"}

// refuse is what an unusable invocation costs: ONE line in the refusal grammar every tool
// shares, `<TOKEN> REFUSED: <what>; run: nova-memory help`, naming what was wrong, and the
// door to the usage rather than the usage itself. where is "" for the tool, or " <verb>".
func refuse(stderr io.Writer, where, what string) int {
	return refuseWith(stderr, where, what, "nova-memory help")
}

// refuseWith is refuse with the command to run next.
func refuseWith(stderr io.Writer, where, what, remedy string) int {
	verb := strings.TrimSpace(where)
	token := strings.ToUpper(verb)
	if t, ok := tokens[verb]; ok {
		token = t
	}
	if token == "" {
		token = "MEMORY"
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s; run: %s\n", oneline.Field(token), oneline.Escape(what), oneline.Escape(remedy))
	return 2
}

// verbHelp is the lines run adds to a verb's -h: its effect, and for a verb that
// needs extra context (eval's gold file format, boot's pin check, search's
// calibration probe), that context.
func verbHelp(verb string) string {
	extra := ""
	switch verb {
	case "eval":
		extra = "gold file format: query<TAB>expected[,expected]\n" +
			"  how often should the lantern glazing be washed\tnotes/lantern.md\n" +
			"  washing the glazing before an onshore gale\tnotes/lantern.md,log/1974-03-11.md\n"
	case "boot":
		extra = "checks the pin: every file present and readable, and their size\n"
	case "search":
		extra = "calibration probe text: " + calibrationProbe + "\n"
	}
	return extra + "effect: " + string(tool.Inspection) + " (the index lives in memory for the run)\n"
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help, its effect included, on stdout
	// at exit 0, before anything is read or written (the CLI style's rule (b)).
	defer verbflag.RecoverWith(stdout, "nova-memory", usage, &code, verbHelp)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verbs are "+verbflag.List(verbs)+", and quickstart is the first run")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			// --help goes right after the verb: after a word or a -- it would be one.
			return run(append([]string{args[1], "--help"}, args[2:]...), stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return 0
	}
	if !verbflag.BoolAsked(args[1:], "json") {
		return dispatch(args[0], args[1:], stdin, stdout, stderr)
	}
	// Under --json a verb prints its one result on stdout and no line on stderr, so what
	// reaches stderr is a refusal: it becomes one refused result on stdout too.
	var problems bytes.Buffer
	code = dispatch(args[0], args[1:], stdin, stdout, &problems)
	if problems.Len() == 0 {
		return code
	}
	var why []string
	for _, l := range strings.Split(strings.TrimSpace(problems.String()), "\n") {
		why = append(why, strings.TrimSpace(l))
	}
	o := tool.Refuse(why...)
	o.Verb, o.Remedy = args[0], "nova-memory help"
	return o.Render(stdout, true)
}

// dispatch runs one verb.
func dispatch(verb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch verb {
	case "quickstart":
		return cmdQuickstart(args, stdout, stderr)
	case "stats":
		return cmdStats(args, stdout, stderr)
	case "search":
		return cmdSearch(args, stdout, stderr)
	case "check":
		return cmdCheck(args, stdin, stdout, stderr)
	case "verify":
		return cmdVerify(args, stdout, stderr)
	case "eval":
		return cmdEval(args, stdout, stderr)
	case "boot":
		return cmdBoot(args, stdout, stderr)
	case "version", "--version":
		return cmdVersion(args, stdout, stderr, version)
	}
	near := ""
	if n := verbflag.Nearest(verb, verbs); n != "" {
		near = " did you mean " + n + "?"
	}
	return refuse(stderr, "", "unknown verb "+strconv.Quote(verb)+";"+near+" the verbs are "+verbflag.List(verbs))
}

// ---------------------------------------------------------------------------
// Flag plumbing: the no-guessing rule, enforced once

// multiFlag is a repeatable string flag. It starts empty and stays empty
// unless the caller says otherwise — scope is never inherited.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// parse runs a verb's flag set and enforces the no-guessing rule: every required flag
// must have been GIVEN. Whether it was given is asked of the flag set, not inferred from
// the value, so "--k 0" is a different (and differently worded) refusal from a missing --k.
//
// EVERY missing flag is reported, not the first: a first run that is two flags short
// learns that in one run. The returned set says which flags were given, so a caller
// checks the VALUE of each flag it actually received and adds those refusals to the same
// run, and neither does a bad --k hide a bad --channels.
//
// Flags may stand before, between or after the positional arguments (a file, the query
// words): `check ... draft.md --json` is --json, never a second file. `--` ends the flags,
// and a query word that starts with a dash comes after it.
//
// given is nil when the arguments could not be parsed at all: nothing after that is
// knowable. An unknown flag, one missing its value or one with a value it cannot take is
// refused naming the verb's flags and the nearest one (verbflag.Explain, the wording
// pkg/tool gives every tool), with the verb's help as the remedy. -h after a verb is
// not refused: verbflag.Parse raises that verb's help, which run prints on stdout at exit 0.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) (given map[string]bool, pos []string, ok bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	for {
		if err := verbflag.Parse(fs, args); err != nil {
			refuseWith(stderr, " "+fs.Name(), oneline.Cap(verbflag.Explain(fs, err), oneline.TailBytes), "nova-memory "+fs.Name()+" -h")
			return nil, nil, false
		}
		rest := fs.Args()
		if len(rest) == 0 || slices.Contains(args[:len(args)-len(rest)], "--") {
			pos = append(pos, rest...)
			break
		}
		pos, args = append(pos, rest[0]), rest[1:]
	}
	given = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	ok = true
	for _, name := range slices.Sorted(slices.Values(required)) { // deterministic order, not caller order
		if !given[name] {
			refuse(stderr, " "+fs.Name(), fmt.Sprintf("--%s is required; refusing to guess", name))
			fmt.Fprint(stderr, hintFor(name))
			ok = false
		}
	}
	return given, pos, ok
}

// rootFlags carries the flags every verb needs to build an index.
type rootFlags struct {
	root     multiFlag
	excludes multiFlag
}

func addRootFlags(fs *flag.FlagSet) *rootFlags {
	r := &rootFlags{}
	fs.Var(&r.root, "root", "corpus root directory, repeatable (required)")
	fs.Var(&r.excludes, "exclude", "path or glob to skip, repeatable (nothing is excluded by default)")
	return r
}

func (r *rootFlags) excluded(p string) bool {
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

// build derives the index, or explains why it could not. Every failure here
// is exit 2: the check could not run. With several roots each is built on its
// own filesystem and the corpuses merged, so one ranking spans them and each
// chunk remembers which root it came from.
func (r *rootFlags) build(name string, stderr io.Writer) (*memindex.Corpus, time.Duration, bool) {
	t0 := time.Now()
	parts := make([]*memindex.Corpus, 0, len(r.root))
	for _, root := range r.root {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			refuse(stderr, " "+name, fmt.Sprintf("--root %s is not a readable directory", oneline.Escape(root)))
			return nil, 0, false
		}
		// Belt to the walk-time type check in memindex.Build: every read the
		// build makes goes through an os.Root, so a symlink swapped in
		// between the walk and the read cannot leave the root (security#76
		// finding 1, re-filed from security#58 finding 1).
		rf, err := os.OpenRoot(root)
		if err != nil {
			refuse(stderr, " "+name, fmt.Sprintf("--root %s is not a readable directory: %s", oneline.Escape(root), oneline.Err(err)))
			return nil, 0, false
		}
		c, err := memindex.Build(rf.FS(), r.excluded)
		rf.Close() // ignored: a read-only root holds nothing to flush
		if err != nil {
			refuse(stderr, " "+name, fmt.Sprintf("building the index over %s: %s", oneline.Escape(root), oneline.Err(err)))
			return nil, 0, false
		}
		parts = append(parts, c)
	}
	c := memindex.Merge(parts, r.root)
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
		refuse(stderr, " "+verb, "--channels named no channels; refusing to guess")
		fmt.Fprintf(stderr, "  %s\n", channelsHint)
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
			refuse(stderr, " "+verb, fmt.Sprintf("--channels %q has an empty entry; refusing to guess", spec))
			return nil, false
		default:
			// The hint is on the same line, so the one line names the channels there are.
			refuse(stderr, " "+verb, fmt.Sprintf("unknown channel %q: %s", n, channelsHint))
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
		refuse(stderr, " "+verb, fmt.Sprintf("--k must be a positive receipt budget (got %d); refusing to guess", k))
		return false
	}
	return true
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

// receiptHits stops a receipt list at the last hit above zero. scoreFields
// prints every native score with two decimals, so a hit whose score rounds to
// 0.00 is the same text an absent score would give and a reader cannot tell it
// from no evidence. The list is left whole when no hit prints above zero: the
// MISS line already explains an empty result as out-of-vocabulary, which a
// weak in-vocabulary match is not.
func receiptHits(hits []memindex.FileHit) []memindex.FileHit {
	last := -1
	for i, h := range hits {
		if math.Round(h.Native*100) > 0 {
			last = i
		}
	}
	if last < 0 {
		return hits
	}
	return hits[:last+1]
}

// wholeCap bounds a --whole paragraph. The snippet cuts at 120 bytes and the
// words searched for can sit just past that cut; --whole prints the whole
// paragraph up to this cap, and oneline.Cap counts the bytes it dropped, so
// the cut is never silent. The cap keeps one pathological paragraph from being
// the whole of a reader's context.
const wholeCap = 4096

// wholePassage is the paragraph --whole prints: the whole original text,
// bounded by wholeCap with oneline.Cap's `...+<n>B` note when it had to cut.
func wholePassage(s string) string { return oneline.Cap(s, wholeCap) }

// hitLine renders one receipt as a single machine-scannable line. Absent
// frontmatter prints as "-" so the field count never changes. The class, the
// name and the type are the corpus's own text and are fields, so each is one
// token; the file is a positional slot and keeps its spaces; the passage is
// Go-quoted, which is one line in a different escape form. whole swaps the
// 120-byte snippet for the whole paragraph (capped, and marked when cut).
//
// Under fusion (more than one channel) the native score is printed under the
// name of the channel that produced it — bm25= or trigram= — because the
// channels score on different scales and one bare score= beside rank= would
// claim an ordering the number does not have (M-4). score-channel= still names
// it, so a reader who looks for the channel finds it either way.
func hitLine(token, prefix string, rank int, h memindex.FileHit, whole, fused bool) string {
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
	native := scoreFields(h.Native, h.NativeChan)
	if fused && h.NativeChan != "" {
		native = fmt.Sprintf("%s=%.2f score-channel=%s", h.NativeChan, h.Native, h.NativeChan)
	}
	passage := h.Snippet
	if whole {
		passage = wholePassage(h.Whole)
	}
	return fmt.Sprintf("%s HIT %srank=%d %s fused=%.5f class=%s name=%s type=%s root=%s: %s:%d %q\n",
		token, prefix, rank, native, h.Fused, oneline.Field(h.Class), oneline.Field(name), oneline.Field(typ), oneline.Field(root), oneline.Escape(h.File), h.Line, passage)
}

// ---------------------------------------------------------------------------
// quickstart — the first run, which SAYS what it chose

// ---------------------------------------------------------------------------
// stats — m, measured

func cmdStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	rf := addRootFlags(fs)
	asJSON := fs.Bool("json", false, "print the result as one JSON object instead of lines")
	given, pos, ok := parse(fs, args, stderr, "root")
	if given == nil {
		return 2
	}
	bad := !ok
	if len(pos) > 0 {
		refuse(stderr, " stats", fmt.Sprintf("unexpected argument %q", pos[0]))
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
	if *asJSON {
		o := result("stats").Fact("schema", memindex.SchemaVersion).Fact("files", len(c.Files)).Fact("chunks", len(c.Chunks)).
			Fact("bytes", c.Bytes).Fact("vocab", len(c.DF)).Fact("avg-terms", math.Round(c.AvgLen*10)/10).Fact("build", buildTime.String())
		for _, cl := range slices.Sorted(maps.Keys(c.ByClass)) {
			o.Item("class", "class", cl, "chunks", c.ByClass[cl])
		}
		return o.Render(stdout, true)
	}
	fmt.Fprintf(stdout, "STATS OK schema=%s files=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
		memindex.SchemaVersion, len(c.Files), len(c.Chunks), c.Bytes, len(c.DF), c.AvgLen, buildTime)
	for _, cl := range slices.Sorted(maps.Keys(c.ByClass)) {
		fmt.Fprintf(stdout, "STATS OK class=%s chunks=%d\n", oneline.Field(cl), c.ByClass[cl])
	}
	return 0
}

// ---------------------------------------------------------------------------
// boot — checks the pin: every file present and readable, and their size

// ---------------------------------------------------------------------------
// search — one query, k receipts

// ---------------------------------------------------------------------------
// check — the consolidation gate that never judges

// ---------------------------------------------------------------------------
// verify — the coverage ritual, mechanized

// ---------------------------------------------------------------------------
// eval — the known-answer harness, shipped with the tool
