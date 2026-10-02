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
// Every path and every scope comes from a flag, with no config file and no
// environment variable: a missing --root is a refusal, never a guess. The
// retrieval has defaults (every channel, k=10), named on every line that used
// them. Exit 0 ran and passed, 1 ran and failed, 2 could not run.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// memoryTool is the command: its banner, its verbs and their flags. Every verb is an
// inspection: the index is built in memory from the corpus and nothing is written.
func memoryTool() *tool.Tool {
	return &tool.Tool{
		Name: "nova-memory",
		What: "search your own markdown notes, and check a draft against what they already say",
		How: `each run reads the --root directories and builds its index in
memory (bm25 words, trigrams); nothing is written. search prints the k best
passages with file:line and the quoted text; check names the notes a draft
repeats; verify gates links and frontmatter. The CAL line is the score a fixed
unrelated probe gets here: a hit scoring at or below it is no better than noise.`,
		ExitTable: "0 ran and passed, 1 ran and failed, 2 could not run (bad invocation).",
		Stamp:     version,
		Setup: `mkdir -p ./corpus/notes && printf 'The lantern glazing needs clean cloths for brass and glass.\n' > ./corpus/notes/lantern.md && ` +
			`printf '[Lantern care](lantern.md) keeps the glazing clean.\n' > ./corpus/notes/index-notes.md && cp ./corpus/notes/lantern.md ./draft.md`,
		Verbs: []tool.Verb{cmdQuickstart(), cmdStats(), cmdSearch(), cmdCheck(), cmdVerify(), cmdEval(), cmdBoot()},
	}
}

// inspection is every verb's effect.
const inspection = tool.Inspection + " (the index lives in memory for the run)"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// run is one invocation, through the shared skeleton (internal/tool).
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return memoryTool().Run(args, stdin, stdout, stderr)
}

// rootWants is what --root is, in the words of its refusal: the flag is never guessed.
const rootWants = "your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run, and repeat it to index several roots in one ranking"

// channelsHint is what --channels names, for a value that names something else.
const channelsHint = `--channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start`

// rankDetail is how search and check rank a hit, for their -h.
const rankDetail = `class is the top-level directory ("." for root files); name/type are frontmatter
values, with "-" meaning absent. With two channels a hit is ranked by fused= (rank
fusion over the channels); its score= is its score in the channel named beside it
(score-channel=), which compares only with scores of that channel and with the
CAL line, so score= need not fall with rank.`

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

// ---------------------------------------------------------------------------
// Flag plumbing: the no-guessing rule, enforced once

// multiFlag is a repeatable string flag. It starts empty and stays empty
// unless the caller says otherwise — scope is never inherited.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }
func (m *multiFlag) Get() any           { return []string(*m) }

// strs reads a repeatable flag.
func strs(c *tool.Call, name string) []string { return c.Get(name).([]string) }

// addRootFlags declares the flags every verb that indexes needs, and refuses a run
// that names no root.
func addRootFlags(f *tool.Flags) {
	f.Var(new(multiFlag), "root", "the corpus `dir`, repeatable: several roots are indexed together in one ranking, every receipt naming its root (required)")
	f.Var(new(multiFlag), "exclude", "a path or `glob` to skip, repeatable (nothing but .git is excluded by default)")
	f.Check(func(c *tool.Call) {
		if len(strs(c, "root")) == 0 {
			c.Problem("--root is required; it wants " + rootWants + "; refusing to guess")
		}
	})
}

// rootFlags carries the flags every verb needs to build an index.
type rootFlags struct {
	root     []string
	excludes []string
}

func rootsOf(c *tool.Call) *rootFlags {
	return &rootFlags{root: strs(c, "root"), excludes: strs(c, "exclude")}
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

// build derives the index, or the refusal saying why it could not. Every failure
// here is exit 2: the check could not run. With several roots each is built on its
// own filesystem and the corpuses merged, so one ranking spans them and each
// chunk remembers which root it came from.
func (r *rootFlags) build() (*memindex.Corpus, time.Duration, *tool.Out) {
	t0 := time.Now()
	parts := make([]*memindex.Corpus, 0, len(r.root))
	for _, root := range r.root {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			return nil, 0, tool.Refuse(fmt.Sprintf("--root %s is not a readable directory", oneline.Escape(root)))
		}
		c, err := memindex.Build(os.DirFS(root), r.excluded)
		if err != nil {
			return nil, 0, tool.Refuse(fmt.Sprintf("building the index over %s: %s", oneline.Escape(root), oneline.Err(err)))
		}
		parts = append(parts, c)
	}
	c := memindex.Merge(parts, r.root)
	return c, time.Since(t0), nil
}

// channelNames validates the --channels list and returns the names in it, or the
// problem with it. An unknown name is a refusal, never a silent drop: a run that
// quietly used fewer channels than asked reports a number that means something else.
// It needs no corpus, so a bad --channels is refused in the same run as a bad --k.
func channelNames(spec string) ([]string, string) {
	if strings.TrimSpace(spec) == "" {
		return nil, "--channels named no channels; " + channelsHint + "; refusing to guess"
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
			return nil, fmt.Sprintf("--channels %q has an empty entry; refusing to guess", spec)
		default:
			return nil, fmt.Sprintf("unknown channel %q: %s", n, channelsHint)
		}
	}
	return out, ""
}

// The retrieval defaults: every channel, and k=10 receipts. A run that names neither
// says both on its OK line (channels=, k=), so what was chosen is never hidden.
const (
	allChannels = "bm25,trigram"
	defaultK    = 10
)

// addRetrievalFlags declares --channels and --k, and refuses a value of either that
// names no retrieval, in the same run as every other problem.
func addRetrievalFlags(f *tool.Flags, kIs string) {
	f.String("channels", allChannels, "a comma-separated `list` of retrieval channels, bm25 and trigram (default: both); the OK line names the channels that ran")
	f.Int("k", defaultK, kIs+", positive (default 10): k is the mind's budget, and zero is not unlimited")
	f.Check(func(c *tool.Call) {
		if k := c.Int("k"); k <= 0 {
			c.Problem(fmt.Sprintf("--k must be a positive receipt budget (got %d); refusing to guess", k))
		}
		if _, why := channelNames(c.Str("channels")); why != "" {
			c.Problem(why)
		}
	})
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
	root := h.Root
	if root == "" {
		root = "-"
	}
	return fmt.Sprintf("%s HIT %srank=%d %s fused=%.5f class=%s name=%s type=%s root=%s: %s:%d %q\n",
		token, prefix, rank, scoreFields(h.Native, h.NativeChan), h.Fused, oneline.Field(h.Class), oneline.Field(name), oneline.Field(typ), oneline.Field(root), oneline.Escape(h.File), h.Line, h.Snippet)
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
// two characters: a backslash is a path separator on
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

// jsonStep runs one step under --json and records it as an item of o: the command line,
// its exit, and its own result object.
func jsonStep(o *tool.Out, argv []string, stdin io.Reader, stderr io.Writer) int {
	var out bytes.Buffer
	code := run(append(argv, "--json"), stdin, &out, stderr)
	o.Item("step", "command", tool.Text("nova-memory "+commandLine(argv)), "exit", code, "result", json.RawMessage(bytes.TrimSpace(out.Bytes())))
	return code
}

// stepFailed is a step that could not run. quickstart exits 0 only when all three
// ran: a partial demonstration that exited 0 would be teaching the green, and the
// green is the one thing this tool is careful about.
func stepFailed(verb string, code int) *tool.Out {
	return tool.Refuse(fmt.Sprintf("the %s step could not run (exit %d); nothing further was attempted", verb, code))
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

func cmdQuickstart() tool.Verb {
	return tool.Verb{
		Name:    "quickstart",
		Usage:   "quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]...",
		Example: "quickstart --root ./corpus",
		Effect:  inspection,
		Detail: `quickstart is the first run and nothing else: it runs stats, then one search,
then one check, PRINTING each command line above that command's output, so
what you saw came from a line you can now edit and run yourself. It names the
channel and the k on every line it prints, and says so again at the end.`,
		Flags: func(f *tool.Flags) {
			addRootFlags(f)
			f.Var(new(multiFlag), "words", "a `word` for the demonstration search, repeatable (default: the corpus's three most frequent terms that are not function words)")
			f.String("draft", "", "the candidate `file` the demonstration check reads (default: this corpus's own first paragraph, on stdin)")
			f.Check(func(c *tool.Call) {
				if c.Given("draft") && strings.TrimSpace(c.Str("draft")) == "" {
					c.Problem("--draft names a candidate file; omit it to use this corpus's own first paragraph")
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			rf, words, draft, asJSON := rootsOf(c), strs(c, "words"), c.Str("draft"), c.Bool("json")
			// Built once here, before any step, only to choose what the caller did not:
			// the query words and the demonstration candidate. Each step below builds
			// its own index, because each step is a command the reader can run alone
			// and must behave identically when they do.
			corpus, _, refused := rf.build()
			if refused != nil {
				return refused
			}
			if len(corpus.Chunks) == 0 {
				// memindex.Build refuses an empty corpus, so this is a guard and not a
				// path — stated rather than assumed, because the alternative is an
				// index panic on the friendliest verb in the tool.
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

			// Flags first, then positionals, so every echoed line reads the way the
			// usage line writes it.
			var common []string
			for _, r := range rf.root {
				common = append(common, "--root", r)
			}
			for _, e := range rf.excludes {
				common = append(common, "--exclude", e)
			}
			// The words are free text, quoted at the end of the line as typed: one field per word
			// would split a word holding a blank, and a hex escape is no way to show a reader what
			// was searched.
			o := tool.Done().Fact("root", strings.Join(rf.root, " ")).Fact("steps", 3).Fact("channels", "bm25").
				Fact("k", quickstartSearchK+"/"+quickstartCheckK).Fact("words-source", wordsSource).Fact("candidate", candidate).
				Fact("words", tool.Text(strings.Join(words, " ")))
			runStep := func(argv []string, stdin io.Reader) int {
				if asJSON {
					return jsonStep(o, argv, stdin, c.Stderr)
				}
				return step(argv, stdin, c.Stdout, c.Stderr)
			}
			if !asJSON {
				fmt.Fprintf(c.Stdout, "QUICKSTART RUN root=%s steps=3 channels=bm25 k=%s/%s words-source=%s candidate=%s words=%s\n",
					oneline.Field(strings.Join(rf.root, " ")), quickstartSearchK, quickstartCheckK,
					oneline.Field(wordsSource), oneline.Field(candidate), oneline.Quote(strings.Join(words, " ")))
			}

			if code := runStep(append([]string{"stats"}, common...), strings.NewReader("")); code != 0 {
				return stepFailed("stats", code)
			}

			searchArgs := append(append([]string{"search"}, common...), "--channels", "bm25", "--k", quickstartSearchK)
			for _, w := range words {
				if strings.HasPrefix(w, "-") {
					searchArgs = append(searchArgs, "--")
					break
				}
			}
			if code := runStep(append(searchArgs, words...), strings.NewReader("")); code != 0 {
				return stepFailed("search", code)
			}

			checkArgs := append(append([]string{"check"}, common...), "--channels", "bm25", "--k", quickstartCheckK)
			checkIn := strings.NewReader("")
			if draft != "" {
				checkArgs = append(checkArgs, draft)
			} else {
				// The demonstration with the answer known: a paragraph the corpus
				// certainly holds, so a first run sees what "you already know this"
				// looks like when it is true, and can compare it against the
				// calibration band on the same screen.
				first := corpus.Chunks[0]
				checkArgs = append(checkArgs, "-")
				checkIn = strings.NewReader(first.Original)
				if asJSON {
					o.Fact("demo", first.File+":"+strconv.Itoa(first.Line))
				} else {
					fmt.Fprintf(c.Stdout, "QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: %s:%d\n",
						oneline.Escape(first.File), first.Line)
				}
			}
			if code := runStep(checkArgs, checkIn); code != 0 {
				return stepFailed("check", code)
			}

			if asJSON {
				return o.Fact("done", 3).Note(quickstartChoiceNote)
			}
			fmt.Fprintf(c.Stdout, "QUICKSTART OK done=3\n")
			fmt.Fprintf(c.Stdout, "QUICKSTART NOTE %s\n", quickstartChoiceNote)
			return tool.Exit(0)
		},
	}
}

// ---------------------------------------------------------------------------
// stats — m, measured

func cmdStats() tool.Verb {
	return tool.Verb{
		Name:   "stats",
		Usage:  "stats --root <dir>... [--exclude <glob>]...",
		Effect: inspection,
		Flags:  addRootFlags,
		Run: func(c *tool.Call) *tool.Out {
			corpus, buildTime, refused := rootsOf(c).build()
			if refused != nil {
				return refused
			}
			// build= is the one field that is not byte-reproducible: it is a measured
			// duration, labelled as one. Everything else is derived from the tree.
			if c.Bool("json") {
				o := tool.Done().Fact("schema", memindex.SchemaVersion).Fact("files", len(corpus.Files)).Fact("chunks", len(corpus.Chunks)).
					Fact("bytes", corpus.Bytes).Fact("vocab", len(corpus.DF)).Fact("avg-terms", math.Round(corpus.AvgLen*10)/10).Fact("build", buildTime.String())
				for _, cl := range slices.Sorted(maps.Keys(corpus.ByClass)) {
					o.Item("class", "class", cl, "chunks", corpus.ByClass[cl])
				}
				return o
			}
			fmt.Fprintf(c.Stdout, "STATS OK schema=%s files=%d chunks=%d bytes=%d vocab=%d avg-terms=%.1f build=%v\n",
				memindex.SchemaVersion, len(corpus.Files), len(corpus.Chunks), corpus.Bytes, len(corpus.DF), corpus.AvgLen, buildTime)
			for _, cl := range slices.Sorted(maps.Keys(corpus.ByClass)) {
				fmt.Fprintf(c.Stdout, "STATS OK class=%s chunks=%d\n", oneline.Field(cl), corpus.ByClass[cl])
			}
			return tool.Exit(0)
		},
	}
}

// ---------------------------------------------------------------------------
// boot — the session loads a pin, never walks the directory

func cmdBoot() tool.Verb {
	return tool.Verb{
		Name:   "boot",
		Usage:  "boot --root <dir> --pin <file>",
		Effect: inspection,
		Detail: "boot loads a pin, not a directory: it reads exactly the files the pin names and never walks --root.",
		Flags: func(f *tool.Flags) {
			f.Required("root", "the memory root directory the pin's paths are relative to")
			f.Required("pin", "the pin file naming the memories a session loads, one slash path per line relative to --root (# comments and blank lines ignored)")
		},
		Run: func(c *tool.Call) *tool.Out {
			n, bytes, why := loadPin(c.Str("root"), c.Str("pin"))
			if why != "" {
				return tool.Refuse(why)
			}
			if c.Bool("json") {
				return tool.Done().Fact("files", n).Fact("bytes", bytes)
			}
			fmt.Fprintf(c.Stdout, "BOOT OK files=%d bytes=%d\n", n, bytes)
			return tool.Exit(0)
		},
	}
}

// loadPin reads the pin file and loads exactly the files it names, relative to
// root and never by walking the directory. It returns the count and the byte
// total of the loaded memories, or why it could not. Every misshapen entry is a
// refusal, because a boot that silently skipped a named memory is a self that
// loaded less than it thinks it did.
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
		why := ""
		switch {
		case strings.HasPrefix(e, "/") || filepath.IsAbs(e):
			why = "is absolute; every entry is relative to --root"
		case e != path.Clean(e):
			why = `is not canonical (no "./", "//", ".." or trailing "/")`
		case e == ".." || strings.HasPrefix(e, "../"):
			why = "escapes --root"
		case seen[e]:
			why = "appears twice; double-counted bytes are a lie"
		}
		if why == "" {
			seen[e] = true
			fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(e)))
			switch {
			case err != nil:
				why = "does not exist under --root"
			case !fi.Mode().IsRegular():
				why = "is not a regular file"
			case fi.Size() == 0:
				why = "is empty; a memory of zero bytes cannot be loaded"
			default:
				total += fi.Size()
			}
		}
		if why != "" {
			return 0, 0, fmt.Sprintf("pin entry %q %s", e, why)
		}
	}
	return len(entries), total, ""
}

// readPin reads one memory path per line; blank lines and lines starting with
// # are ignored. Order is preserved — it is the boot order.
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

// ---------------------------------------------------------------------------
// search — one query, k receipts

func cmdSearch() tool.Verb {
	return tool.Verb{
		Name:    "search",
		Usage:   "search --root <dir>... [--channels <list>] [--k <n>] [--exclude <glob>]... <words>...",
		Example: "search --root ./corpus --channels bm25 --k 3 lantern glazing brass",
		Effect:  inspection,
		Detail:  rankDetail,
		Flags: func(f *tool.Flags) {
			addRootFlags(f)
			addRetrievalFlags(f, "receipts per query")
			f.Args("<words>...")
		},
		Run: func(c *tool.Call) *tool.Out {
			corpus, _, refused := rootsOf(c).build()
			if refused != nil {
				return refused
			}
			query, chans := strings.Join(c.Args(), " "), channelsOf(c, corpus)
			hits := memindex.Retrieve(corpus, chans, query, c.Int("k"))
			return retrievalResult{Verb: "search", Query: query, K: c.Int("k"), Channels: chanNames(chans), Files: len(corpus.Files), Chunks: len(corpus.Chunks),
				Calibration: calibrationHits(corpus, chans), Candidates: []retrievalCandidate{{Hits: hits}}, Notes: []string{noteLexical}}.out(c)
		},
	}
}

// channelsOf builds the channels --channels names; the check above has refused any
// other name.
func channelsOf(c *tool.Call, corpus *memindex.Corpus) []memindex.Channel {
	names, _ := channelNames(c.Str("channels"))
	return newChannels(corpus, names)
}

// ---------------------------------------------------------------------------
// check — the consolidation gate that never judges

func cmdCheck() tool.Verb {
	return tool.Verb{
		Name:    "check",
		Token:   "MEMORY",
		Usage:   "check --root <dir>... [--channels <list>] [--k <n>] [--exclude <glob>]... <file|->",
		Example: "check --root ./corpus --channels bm25 --k 3 draft.md",
		Effect:  inspection,
		Detail:  rankDetail,
		Flags: func(f *tool.Flags) {
			addRootFlags(f)
			addRetrievalFlags(f, "receipts per candidate paragraph")
			f.Args("<file|->")
		},
		Run: func(c *tool.Call) *tool.Out {
			src, name := c.Stdin, c.Args()[0]
			if name != "-" {
				f, err := os.Open(name)
				if err != nil {
					return tool.Refuse(oneline.Err(err))
				}
				defer f.Close()
				src = f
			}
			raw, err := io.ReadAll(src)
			if err != nil {
				return tool.Refuse(fmt.Sprintf("reading %s: %s", oneline.Escape(name), oneline.Err(err)))
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
				return tool.Refuse(fmt.Sprintf("%s holds no candidate paragraph of at least %d terms; nothing to check", oneline.Escape(name), memindex.MinTerms))
			}
			corpus, _, refused := rootsOf(c).build()
			if refused != nil {
				return refused
			}
			chans, k := channelsOf(c, corpus), c.Int("k")
			result := retrievalResult{Verb: "check", Source: name, K: k, Channels: chanNames(chans), Files: len(corpus.Files), Chunks: len(corpus.Chunks), Calibration: calibrationHits(corpus, chans), Notes: []string{noteLexical, "this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours", "a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction"}}
			for _, cand := range candidates {
				result.Candidates = append(result.Candidates, retrievalCandidate{Text: memindex.Truncate(strings.TrimSpace(cand), 100), Hits: memindex.Retrieve(corpus, chans, cand, k)})
			}
			return result.out(c)
		},
	}
}

// ---------------------------------------------------------------------------
// verify — the coverage ritual, mechanized

// addFailMax declares --fail-max, the finding lines a capped listing prints.
func addFailMax(f *tool.Flags, what string) {
	f.Int("fail-max", bounded.Default, what+" to print before one MORE line stands for the rest; 0 prints all (the count is never capped)")
	f.Check(func(c *tool.Call) {
		if n := c.Int("fail-max"); n < 0 {
			// Zero already means "all". A negative ceiling is neither a number of lines nor
			// a way of asking for every line, so it is a typo with two readings and gets
			// neither.
			c.Problem(fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", n))
		}
	})
}

func cmdVerify() tool.Verb {
	return tool.Verb{
		Name:    "verify",
		Usage:   "verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]... [--fail-max <n>]",
		Example: "verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md",
		Effect:  inspection,
		Detail: `verify caps each KIND of finding separately, so ten thousand wikilink findings
cannot bury the one frontmatter finding; the count line carries every total.`,
		Flags: func(f *tool.Flags) {
			addRootFlags(f)
			f.Required("links", "gate (unresolved [[wikilinks]] fail the run) or info (they are reported and do not)")
			f.Var(new(multiFlag), "coverage", "an `A:B` pair of globs, repeatable: every file matching A is named in some file matching B")
			f.Var(new(multiFlag), "frontmatter", "a `glob` whose files must carry a frontmatter name:, repeatable")
			f.Var(new(multiFlag), "exempt", "a basename `prefix` of listings exempt from --frontmatter, repeatable (nothing is exempt by default)")
			addFailMax(f, "finding lines per kind")
			f.Check(func(c *tool.Call) {
				if links := c.Str("links"); links != "" && links != "gate" && links != "info" {
					c.Problem(fmt.Sprintf("--links must be gate or info (got %q); refusing to guess", links))
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			rf, links, failMax := rootsOf(c), c.Str("links"), c.Int("fail-max")
			coverage, front, exempt := strs(c, "coverage"), strs(c, "frontmatter"), strs(c, "exempt")
			gateLinks := links == "gate"
			switch {
			case len(rf.root) != 1:
				// --coverage and --frontmatter globs and [[wikilink]] resolution all
				// walk one tree, and a relative .md link resolves against one root, so
				// verification names one root and no more.
				return tool.Refuse(fmt.Sprintf("--root names exactly one tree for verification, but %d were given", len(rf.root)))
			case len(coverage) == 0 && len(front) == 0 && !gateLinks:
				// Every check is off and wikilinks are informational: this run can
				// only ever exit 0. A green that could not have been anything else is
				// not a check, so it is refused rather than printed.
				return tool.Refuse("no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification")
			case len(exempt) > 0 && len(front) == 0:
				return tool.Refuse("--exempt only applies to --frontmatter, which was not given")
			}
			corpus, _, refused := rf.build()
			if refused != nil {
				return refused
			}
			fsys := memindex.Excluding(os.DirFS(rf.root[0]), rf.excluded)

			var gating, info []memindex.Finding
			coverageFindings, frontmatterFindings := 0, 0
			for _, pair := range coverage {
				a, b, found := strings.Cut(pair, ":")
				if !found || a == "" || b == "" {
					return tool.Refuse(fmt.Sprintf("--coverage wants A:B, got %q", pair))
				}
				fnds, err := memindex.Coverage(fsys, a, b)
				if err != nil {
					return tool.Refuse(oneline.Err(err))
				}
				coverageFindings += len(fnds)
				gating = append(gating, fnds...)
			}
			for _, g := range front {
				fnds, err := memindex.FrontmatterPresent(fsys, g, exempt)
				if err != nil {
					return tool.Refuse(oneline.Err(err))
				}
				frontmatterFindings += len(fnds)
				gating = append(gating, fnds...)
			}
			wl, err := memindex.Wikilinks(fsys, corpus)
			if err != nil {
				return tool.Refuse(oneline.Err(err))
			}
			if gateLinks {
				gating = append(gating, wl...)
			} else {
				info = wl
			}

			if c.Bool("json") {
				o := tool.Done()
				for _, f := range info {
					o.Item(f.Kind, "gating", false, "detail", tool.Text(f.Detail))
				}
				for _, f := range gating {
					o.Item(f.Kind, "gating", true, "detail", tool.Text(f.Detail))
				}
				o.Fact("gating", len(gating)).Fact("info", len(info)).Fact("coverage", coverageFindings).
					Fact("frontmatter", frontmatterFindings).Fact("links", links)
				if len(gating) > 0 {
					o.Status, o.Exit = tool.Failed, 1
				}
				return o.Cap(failMax)
			}

			// EACH KIND IS CAPPED SEPARATELY. A flat cap over the concatenated findings would
			// mean that on a corpus with ten thousand unresolved wikilinks the twenty lines a
			// reader gets are twenty wikilinks, and the one frontmatter finding -- the finding
			// they did not already know about -- is the line the cap ate.
			infos := bounded.Grouped(c.Stdout, failMax, "VERIFY", failMaxRemedy)
			for _, f := range info {
				infos.Line(f.Kind, fmt.Sprintf("VERIFY INFO %s: %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
			}
			infos.More()

			fails := bounded.Grouped(c.Stderr, failMax, "VERIFY", failMaxRemedy)
			for _, f := range gating {
				fails.Line(f.Kind, fmt.Sprintf("VERIFY FAIL %s %s", f.Kind, oneline.Escape(oneline.Cap(f.Detail, oneline.TailBytes))))
			}
			fails.More()

			// THE COUNT LINE PRINTS ON FAILURE TOO: the listing above is capped, so counting
			// its lines would understate how bad a failing run is, and the total is here.
			if fails.Total() > 0 {
				fmt.Fprintf(c.Stderr, "VERIFY FAIL gating=%d shown=%d info=%d coverage=%d frontmatter=%d links=%s\n",
					fails.Total(), fails.Shown(), infos.Total(), coverageFindings, frontmatterFindings, links)
				return tool.Exit(1)
			}
			fmt.Fprintf(c.Stdout, "VERIFY OK gating=0 info=%d shown=%d coverage=%d frontmatter=%d links=%s\n",
				infos.Total(), infos.Shown(), coverageFindings, frontmatterFindings, links)
			return tool.Exit(0)
		},
	}
}

// ---------------------------------------------------------------------------
// eval — the known-answer harness, shipped with the tool

func cmdEval() tool.Verb {
	return tool.Verb{
		Name:   "eval",
		Usage:  "eval --root <dir>... [--channels <list>] [--k <n>] --floor <f> [--exclude <glob>]... [--fail-max <n>] <gold.tsv>",
		Effect: inspection,
		Detail: `eval is the known-answer harness: each gold row is query<TAB>path[,path], and the
run measures recall@k and MRR and fails below --floor. Misses are listed; hits are a count.`,
		Flags: func(f *tool.Flags) {
			addRootFlags(f)
			addRetrievalFlags(f, "receipts per query")
			f.Float64("floor", 0, "the minimum recall@k, in (0,1]: a harness with no floor cannot fail (required)")
			addFailMax(f, "MISS lines")
			f.Args("<gold.tsv>")
			f.Check(func(c *tool.Call) {
				floor := c.Get("floor").(float64)
				switch {
				case !c.Given("floor"):
					c.Problem("--floor is required; it wants the minimum recall@k, in (0,1]; refusing to guess")
				case math.IsNaN(floor) || math.IsInf(floor, 0) || floor <= 0 || floor > 1:
					c.Problem(fmt.Sprintf("--floor must be in (0,1] (got %g); a harness that cannot fail is not a measurement", floor))
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			k, floor, failMax := c.Int("k"), c.Get("floor").(float64), c.Int("fail-max")
			rows, err := readGold(c.Args()[0])
			if err != nil {
				return tool.Refuse(oneline.Err(err))
			}
			corpus, _, refused := rootsOf(c).build()
			if refused != nil {
				return refused
			}
			chans := channelsOf(c, corpus)

			// THE HITS ARE A COUNT: a line per passing row would say "this worked" five hundred
			// times beside the summary that already carries the number. Only the misses -- the
			// rows a reader can act on -- are listed, capped like every other listing here.
			listing := c.Stdout
			if c.Bool("json") {
				listing = io.Discard
			}
			o := tool.Done()
			misses := bounded.Capped(listing, failMax, "EVAL", "miss", failMaxRemedy)
			hits := 0
			var mrr float64
			for _, row := range rows {
				rank := 0
				for i, h := range memindex.Retrieve(corpus, chans, row.query, k) {
					if slices.ContainsFunc(row.expected, func(e string) bool { return strings.Contains(h.File, e) }) {
						rank = i + 1
						break
					}
				}
				if rank != 0 {
					hits++
					mrr += 1.0 / float64(rank)
					continue
				}
				misses.Line(fmt.Sprintf("EVAL MISS query=%s expected=%s",
					oneline.Field(oneline.Cap(row.query, oneline.TailBytes)),
					oneline.Field(oneline.Cap(strings.Join(row.expected, ","), oneline.TailBytes))))
				o.Item("miss", "query", tool.Text(row.query), "expected", strings.Join(row.expected, ","))
			}
			misses.More()
			recall := float64(hits) / float64(len(rows))
			mrr /= float64(len(rows))
			if c.Bool("json") {
				o.Fact("k", k).Fact("recall", recall).Fact("floor", floor).Fact("rows", len(rows)).Fact("hits", hits).
					Fact("misses", misses.Total()).Fact("mrr", mrr).Fact("channels", chanNames(chans))
				if recall < floor {
					o.Status, o.Exit = tool.Failed, 1
					o.Why = []string{fmt.Sprintf("recall@%d=%.3f is below the floor %.3f", k, recall, floor)}
				}
				return o.Cap(failMax)
			}
			if recall < floor {
				fmt.Fprintf(c.Stderr, "EVAL FAIL recall@%d=%.3f below floor %.3f (%d/%d, misses=%d shown=%d, mrr=%.3f, channels=%s)\n",
					k, recall, floor, hits, len(rows), misses.Total(), misses.Shown(), mrr, chanNames(chans))
				return tool.Exit(1)
			}
			fmt.Fprintf(c.Stdout, "EVAL OK recall@%d=%.3f floor=%.3f rows=%d hits=%d misses=%d shown=%d mrr=%.3f channels=%s\n",
				k, recall, floor, len(rows), hits, misses.Total(), misses.Shown(), mrr, chanNames(chans))
			return tool.Exit(0)
		},
	}
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
