// quickstart.go holds the quickstart verb: its flags, its run and the helpers only it uses.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

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
// rules are testable from either one. Each argument goes through oneline.Escape
// first, so the echo is one line whatever an argument holds; the POSIX shell's
// one quoter is oneline.ShellWord. Windows keeps its own form, because
// oneline.ShellWord is POSIX-only: cmd.exe and PowerShell both take a
// double-quoted argument literally, backslashes included, which is exactly what
// a Windows path needs, and a double quote cannot appear in a Windows path at
// all -- one arriving from --words is doubled, which is how that shell spells
// its own quote.
func commandLineFor(argv []string, windows bool) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		esc := oneline.Escape(a)
		if !windows {
			parts = append(parts, oneline.ShellWord(esc))
			continue
		}
		if esc != "" && !needsQuoting(esc, true) {
			parts = append(parts, esc)
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(esc, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " ")
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

// stepArgs is one step's argv, built in the order the parser reads it: the verb, its
// flags, then -- when a positional starts with a dash (a lone - is stdin and stays a
// positional), then the positionals. A flag is never added to a finished argv: after
// -- it would be a query word or a file, not a flag.
func stepArgs(verb string, flags []string, positionals ...string) []string {
	argv := append([]string{verb}, flags...)
	if slices.ContainsFunc(positionals, func(p string) bool { return p != "-" && strings.HasPrefix(p, "-") }) {
		argv = append(argv, "--")
	}
	return append(argv, positionals...)
}

// jsonStep runs one step whose argv carries --json among its flags and records it as an
// item of o: the command line, its exit, and its own result object.
func jsonStep(o *tool.Out, argv []string, stdin io.Reader, stderr io.Writer) int {
	var out bytes.Buffer
	code := run(argv, stdin, &out, stderr)
	o.Item("step", "command", tool.Text("nova-memory "+commandLine(argv)), "exit", code, "result", json.RawMessage(bytes.TrimSpace(out.Bytes())))
	return code
}

// stepFailed reports a step that could not run. quickstart exits 0 only when
// all three ran: a partial demonstration that exited 0 would be teaching the
// green, and the green is the one thing this tool is careful about.
func stepFailed(verb string, code int, stderr io.Writer) int {
	return refuse(stderr, " quickstart", fmt.Sprintf("the %s step could not run (exit %d); nothing further was attempted", verb, code))
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
	asJSON := fs.Bool("json", false, "print the three steps' results as one JSON object instead of lines")
	given, pos, ok := parse(fs, args, stderr, "root")
	if given == nil {
		return 2
	}
	bad := !ok
	if len(pos) > 0 {
		refuse(stderr, " quickstart", fmt.Sprintf("unexpected argument %q; the words for the search go after --words", pos[0]))
		bad = true
	}
	if given["draft"] && strings.TrimSpace(*draft) == "" {
		refuse(stderr, " quickstart", "--draft names a candidate file; omit it to use this corpus's own first paragraph")
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
		return refuse(stderr, " quickstart", "this corpus holds no indexable paragraph; there is nothing to demonstrate on")
	}
	wordsSource := "given"
	if len(words) == 0 {
		words, wordsSource = topTerms(c, 3), "corpus-top-terms"
		if len(words) == 0 {
			return refuse(stderr, " quickstart", "this corpus has no term to demonstrate a search with; name some with --words")
		}
	}
	candidate := "corpus-first-paragraph"
	if *draft != "" {
		candidate = *draft
	}

	// Flags first, then positionals: package flag stops at the first
	// non-flag argument, and every echoed line has to be one a reader can run.
	var common []string
	for _, r := range rf.root {
		common = append(common, "--root", r)
	}
	for _, e := range rf.excludes {
		common = append(common, "--exclude", e)
	}
	if *asJSON {
		// Each step answers in JSON too, and --json is one of its flags, before any
		// -- and any word (stepArgs).
		common = append(common, "--json")
	}
	// The words are free text, quoted at the end of the line as typed: one field per word
	// would split a word holding a blank, and a hex escape is no way to show a reader what
	// was searched.
	o := result("quickstart").Fact("root", strings.Join(rf.root, " ")).Fact("steps", 3).Fact("channels", "bm25").
		Fact("k", quickstartSearchK+"/"+quickstartCheckK).Fact("words-source", wordsSource).Fact("candidate", candidate).
		Fact("words", tool.Text(strings.Join(words, " ")))
	runStep := func(argv []string, stdin io.Reader) int {
		if *asJSON {
			return jsonStep(o, argv, stdin, stderr)
		}
		return step(argv, stdin, stdout, stderr)
	}
	if !*asJSON {
		fmt.Fprintf(stdout, "QUICKSTART RUN root=%s steps=3 channels=bm25 k=%s/%s words-source=%s candidate=%s words=%s\n",
			oneline.Field(strings.Join(rf.root, " ")), quickstartSearchK, quickstartCheckK,
			oneline.Field(wordsSource), oneline.Field(candidate), oneline.Quote(strings.Join(words, " ")))
	}

	statsArgs := stepArgs("stats", common)
	if code := runStep(statsArgs, strings.NewReader("")); code != 0 {
		return stepFailed("stats", code, stderr)
	}

	searchArgs := stepArgs("search", append(slices.Clone(common), "--channels", "bm25", "--k", quickstartSearchK), words...)
	if code := runStep(searchArgs, strings.NewReader("")); code != 0 {
		return stepFailed("search", code, stderr)
	}

	checkFlags := append(slices.Clone(common), "--channels", "bm25", "--k", quickstartCheckK)
	checkArgs := stepArgs("check", checkFlags, *draft)
	checkIn := strings.NewReader("")
	if *draft == "" {
		// The demonstration with the answer known: a paragraph the corpus
		// certainly holds, so a first run sees what "you already know this"
		// looks like when it is true, and can compare it against the
		// calibration band on the same screen.
		checkArgs = stepArgs("check", checkFlags, "-")
		checkIn = strings.NewReader(c.Chunks[0].Original)
		if *asJSON {
			o.Fact("demo", c.Chunks[0].File+":"+strconv.Itoa(c.Chunks[0].Line))
		} else {
			fmt.Fprintf(stdout, "QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: %s:%d\n",
				oneline.Escape(c.Chunks[0].File), c.Chunks[0].Line)
		}
	}
	if code := runStep(checkArgs, checkIn); code != 0 {
		return stepFailed("check", code, stderr)
	}

	if *asJSON {
		return o.Fact("done", 3).Note(quickstartChoiceNote).Render(stdout, true)
	}
	fmt.Fprintf(stdout, "QUICKSTART OK done=3\n")
	fmt.Fprintf(stdout, "QUICKSTART NOTE %s\n", quickstartChoiceNote)
	return 0
}
