// nova-self-talk classifies self-claims in prose, in two disjoint classes:
// STANDING/DATED — what the writer permanently IS or permanently CANNOT do,
// in negative vocabulary — and INSTALLATION — a standing self-verdict built
// from neutral words, which the first class cannot see. It is an advisory
// instrument, not a wall: whether to date a finding, cut it, relocate it, or
// keep it is the writer's judgment, never the tool's.
//
// Exit 0 no findings, 1 any finding, 2 could not run. Every file is named by
// the caller; nothing is skipped by default and no basename is special by
// default — --skip and --rule-doc are both the caller's, per run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const usage = `nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,
and matched against one table of shapes: a first-person claim carrying a word of failure
(STANDING: cannot check, bad at, worst) or a neutral-worded verdict (INSTALLATION: a
self-superlative, a door stated shut, a habit). A dated claim is a record, never flagged.
A scan writes nothing; nova-self-talk shapes prints the table, each row with a sentence it finds.
first run: nova-self-talk example ./pages writes the example pages from the binary itself;
then each line under example: exits 1, because the pages hold findings.

usage:
  nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...
  nova-self-talk scan [flags] <file>...        the same scan, named as a verb
  nova-self-talk shapes [--json]               every shape and licence the scan uses, with a
                                               sentence each finds and a near miss each passes
  nova-self-talk example [--dry-run] [--json] <dir>   write the two example pages into <dir>
  nova-self-talk version                       print this build identity (--version also accepted)
  nova-self-talk help [<verb>]                 this text, or one verb's help

The first word is a verb only when it is scan, shapes, example, version or help; anything
else is the first file, so a file named like a verb is given as ./scan. Flags come before
files; use -- before a file whose name begins with a dash.

Two disjoint classes.

  STANDING / DATED   a first-person claim (I am, I cannot, I always, my <noun> is ...)
                     carrying a word of failure (fallible, broken, bad at, terrible at,
                     worst, cannot check, cannot ever ...). With a date or a measurement
                     word (2026-09-30, measured, that day) it is DATED: a record, counted
                     on one line, never quoted. Without one it is STANDING and is flagged.

  INSTALLATION       (a verdict in neutral words; a finding names its shape alone, and the
                     count line counts them as installations=)
                     a standing self-verdict built from NEUTRAL words, which the first
                     class cannot see: a self-superlative (RANKING: I am the best, my
                     weakest instrument), a door stated shut (FORECLOSURE: I will never be
                     a good planner, I have no recall), a verdict on a practice
                     (VERDICT-IDIOM: dead as a practice), or a habit (TRAIT: I always
                     overpromise, I tend to rush). Dated, instrument (RULE:, TELL:),
                     aspiration (I want to), imperative and quoted sentences are licensed.

Date it, cut it, relocate it, or keep it on purpose — the judgment is the
writer's, and this tool never makes it.

what a scan prints, one line each:
  SELFTALK FAIL <file>:<line>: <SHAPE> match="<words>": <sentence>   (stderr, in line order)
                       <SHAPE> is STANDING for the first class, else the second class's shape:
                       RANKING, FORECLOSURE, VERDICT-IDIOM or TRAIT
  SELFTALK SKIP <file> (--skip)
  SELFTALK RULEDOC <file>: <banner>     above the findings of a --rule-doc file
  SELFTALK MORE kind=<class> shown=<n> total=<t> <remedy>
  SELFTALK DATED n=<k> files=<n>
  SELFTALK OK|FAIL files=<n> claims=<n> standing=<n> installations=<n> dated=<n> [shown=<n>]
  SELFTALK NOTE <a --skip or --rule-doc name no named file has>
  SELFTALK NOTE <what a green does and does not clear>
match= is the words the shape's rule matched. files= counts the files scanned; claims= the
first class's claims, dated ones included; standing= and installations= the findings of each
class; shown= the finding lines printed. A partial check says so: the NOTE prints every run.

flags of the scan:
  --skip <basename>       do not scan files with this basename (repeatable). Nothing is
                          skipped by default, and a skip is reported on a SKIP line.
  --rule-doc <basename>   scan the file, but print its findings under a banner saying a
                          finding there is a self-verdict to relocate and NEVER a reason to
                          soften a rule (repeatable). No basename is special by default.
  --max <n>               finding lines to PRINT per class before one MORE line stands for
                          the rest. Default 20, and 0 means all. The closing line carries
                          the totals whichever way the run went.
  --json                  print the run as one JSON object on stdout instead of lines:
                          result, facts (the closing line's counts), items (one per finding,
                          skip and banner, with file, line, shape, match, text), more, notes.

exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
unreadable file). An all-skipped run exits 0 with SELFTALK SKIP files=0, never OK.

The first run needs nothing but this binary. Write the example pages, then paste the
lines under example: as they are:

  nova-self-talk example ./pages

example:
  nova-self-talk ./pages/journal.md
  nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md
  nova-self-talk --skip RULES.md ./pages/RULES.md ./pages/journal.md

All three exit 1, and that is the tool working: a finding is a sentence to date,
cut, relocate or keep on purpose, never a failure. --skip leaves a file unscanned
and says so on one SELFTALK SKIP line.
`

// The hints below turn this binary's most-hit refusals into a next step. The
// no-guessing law is unchanged -- naming no files is still exit 2 -- but a
// refusal that names only what was wrong leaves a first caller to guess what
// the tool wanted, which is the same guessing the tool refuses to do, moved
// onto the reader.
const (
	filesHint  = `nova-self-talk takes markdown FILES, named on the command line: nova-self-talk <file>... (- is stdin). There is no default set and no directory walk, so a shell glob is the usual first run (nova-self-talk memory/*.md); nova-self-talk example ./pages writes two pages to try it on.`
	unreadHint = `NOTHING was scanned, which is not a green: a file this tool cannot read is not a clean one. Fix or drop the paths above and run again.`
	baseHint   = `--skip and --rule-doc take a BASENAME, not a path: --skip RULES.md, never --skip memory/RULES.md. The match is on the file's name wherever it sits, and nothing is skipped or banner-marked by default.`
)

// hintFor returns the already-indented hint line for a kind of refusal,
// newline included. It returns package constants only, which is why printing
// its result is safe.
func hintFor(kind string) string {
	switch kind {
	case "files":
		return "  " + filesHint + "\n"
	case "unread":
		return "  " + unreadHint + "\n"
	case "basename":
		return "  " + baseHint + "\n"
	}
	return ""
}

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "catches known SHAPES only (list them: nova-self-talk shapes): register, irony and " +
	"quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the " +
	"grammar and a false one on the meaning. A green clears the known shapes, never the file."

// maxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 prints every finding"

// verbs are the words that are a verb in first position; anything else is a file.
var verbs = []string{"scan", "shapes", "example", "version", "help"}

// effects is what each verb's help says it does to the world (docs/STANDARD.md §2).
var effects = map[string]string{
	"scan":    string(tool.Inspection),
	"shapes":  string(tool.Inspection),
	"example": string(tool.LocalWrite),
	"version": string(tool.Inspection),
}

// refuse is what an unusable invocation costs: one line per problem, every problem
// this run found, each naming the door, then at most one indented hint. With --json
// it is the one JSON object on stdout instead.
func refuse(stdout, stderr io.Writer, asJSON bool, verb, hint string, problems ...string) int {
	who, door := "nova-self-talk", "nova-self-talk help"
	if verb != "scan" {
		who, door = who+" "+verb, door+" "+verb
	}
	if asJSON {
		o := tool.Refuse(problems...)
		o.Verb, o.Remedy = verb, door
		if h := strings.TrimSpace(hintFor(hint)); h != "" {
			o.Note(h)
		}
		o.Render(stdout, true)
		return 2
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "%s REFUSED: %s; run: %s\n", oneline.Escape(who), oneline.Escape(p), oneline.Escape(door))
	}
	fmt.Fprint(stderr, hintFor(hint))
	return 2
}

func main() { os.Exit(runStdin(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func runStdin(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// `<verb> -h` is that verb's help on stdout at exit 0, with the verb's effect, before
	// anything is read (the CLI style's rule (b)).
	defer verbflag.RecoverWith(stdout, "nova-self-talk", usage, &code, func(verb string) string {
		return "effect: " + effects[verb] + "\n"
	})
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	switch first {
	case "help", "-h", "-help", "--help":
		// `help <verb>` is that verb's help; `help` with anything else is the banner,
		// because help is never a refusal.
		if first == "help" && len(args) > 1 && slices.Contains(verbs, args[1]) && args[1] != "help" {
			return runStdin([]string{args[1], "-h"}, stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		verbflag.HelpIfAsked(args[1:], "version")
		return cmdVersion(args[1:], stdout, stderr)
	case "shapes":
		return cmdShapes(args[1:], stdout, stderr)
	case "example":
		return cmdExample(args[1:], stdout, stderr)
	case "scan":
		args = args[1:]
	}
	return scan(args, stdin, stdout, stderr)
}

// baseList is the value type behind both repeatable basename flags, --skip
// and --rule-doc. It refuses paths — the match is decided on basenames
// (selftalk.Base), and a value with a separator in it would silently never
// match anything.
//
// BOTH LISTS DEFAULT TO EMPTY, which is the no-defaults law applied to scope:
// which files are rule documents, or are not to be read, is the caller's to
// say, per run, and a name that matches no named file is said on a NOTE line.
type baseList []string

func (s *baseList) String() string { return strings.Join(*s, ",") }

func (s *baseList) Set(v string) error {
	if v == "" {
		return errors.New("needs a basename; refusing to guess")
	}
	if strings.ContainsAny(v, `/\`) {
		return fmt.Errorf("takes a basename, not a path: %q -- the match is on the file's name wherever it sits", v)
	}
	*s = append(*s, v)
	return nil
}

func set(l baseList) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, v := range l {
		m[v] = true
	}
	return m
}

// finding is one reported sentence, of either class.
type finding struct {
	class string // "standing" or "installation": the two are capped separately
	line  int
	shape string
	match string
	text  string
}

// page is what the scan learned about one named file.
type page struct {
	name     string
	skipped  bool
	ruledoc  bool // named by --rule-doc and has findings, so its banner prints
	findings []finding
}

// report is what a scan learned; out makes it the one value both renderings print.
type report struct {
	pages                                       []page
	scanned, claims, standing, installed, dated int
	unmatched                                   []string // the --skip and --rule-doc names no named file has
}

func scan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	asJSON := verbflag.BoolAsked(args, "json")
	fset := verbflag.New("scan")
	var skips, ruleDocs baseList
	fset.Var(&skips, "skip", "basename to skip, repeatable (nothing is skipped by default)")
	fset.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable (empty by default)")
	maxLines := fset.Int("max", bounded.Default, "finding lines to print per class before one MORE line stands for the rest; 0 prints all")
	fset.Bool("json", false, "print the run as one JSON object on stdout instead of lines")
	if err := verbflag.Parse(fset, args); err != nil {
		hint := ""
		for _, flagName := range []string{"skip", "rule-doc"} {
			if strings.Contains(err.Error(), " for flag -"+flagName+":") || err.Error() == "flag needs an argument: -"+flagName {
				hint = "basename"
			}
		}
		what := err.Error()
		if name, ok := strings.CutPrefix(what, "flag provided but not defined: "); ok {
			what = "unknown flag " + name + "; the flags are --skip, --rule-doc, --max, --json"
		}
		return refuse(stdout, stderr, asJSON, "scan", hint, oneline.Cap(what, oneline.TailBytes))
	}

	// EVERY PROBLEM THIS RUN CAN FIND IS NAMED IN ONE GO, so a caller fixes the call once.
	var problems []string
	if *maxLines < 0 {
		// Zero already means "all", so a negative ceiling is a typo with two readings
		// and gets neither.
		problems = append(problems, fmt.Sprintf("--max must be a line ceiling of zero or more (got %d); 0 means print them all", *maxLines))
	}
	files, late := positionals(args, fset)
	if late != "" {
		// A flag after the files makes the file list itself ambiguous, so no file is read.
		problems = append(problems, fmt.Sprintf("flags come before files (got %q); use -- before a filename beginning with a dash", late))
		return refuse(stdout, stderr, asJSON, "scan", "", problems...)
	}
	if len(files) == 0 {
		problems = append(problems, "no files named; refusing to guess")
		return refuse(stdout, stderr, asJSON, "scan", "files", problems...)
	}

	skipped, pinned := set(skips), set(ruleDocs)

	// EVERY NAMED FILE IS READ BEFORE ANY OF THEM IS SCANNED, and every unreadable one is
	// reported rather than the first: a caller who mistyped three paths learns about three
	// in one go, and a run that printed findings and then refused would be reporting
	// findings from a run that did not happen.
	contents := make([]string, len(files))
	unread := 0
	var piped []byte
	for i, f := range files {
		if skipped[selftalk.Base(f)] {
			continue
		}
		b, err := readNamed(f, stdin, &piped)
		if err != nil {
			unread++
			problems = append(problems, cannotRead(f, err))
			continue
		}
		contents[i] = string(b)
	}
	if len(problems) > 0 {
		hint := ""
		if unread > 0 {
			hint = "unread"
		}
		return refuse(stdout, stderr, asJSON, "scan", hint, problems...)
	}

	var r report
	for i, f := range files {
		p := page{name: f, skipped: skipped[selftalk.Base(f)]}
		if !p.skipped {
			r.scanned++
			for _, c := range selftalk.Scan(contents[i]) {
				r.claims++
				if c.Verdict != selftalk.Standing {
					// A DATED CLAIM IS THE WELCOME CASE, and it was half the output: six
					// hundred of them quoted a whole sentence each to say, six hundred times,
					// that the writer had done the thing this tool asks for. It is a count.
					r.dated++
					continue
				}
				r.standing++
				p.findings = append(p.findings, finding{"standing", c.Line, string(c.Verdict), c.Match, c.Text})
			}
			for _, in := range selftalk.ScanInstallation(contents[i]) {
				r.installed++
				p.findings = append(p.findings, finding{"installation", in.Line, string(in.Shape), in.Match, in.Text})
			}
			// The banner prints ONCE per file that has findings of either class, before them,
			// so a reader cannot meet a finding in a rule document without meeting the
			// sentence that says what it is for.
			p.ruledoc = pinned[selftalk.Base(f)] && len(p.findings) > 0
			// A reader repairs a file top to bottom, so its findings print in line order,
			// whichever class found them.
			slices.SortStableFunc(p.findings, func(a, b finding) int { return a.line - b.line })
		}
		r.pages = append(r.pages, p)
	}
	r.unmatched = unmatched(files, skips, ruleDocs)
	o := r.out(*maxLines)
	if asJSON {
		o.Render(stdout, true)
		return o.Exit
	}
	return lines(o, stdout, stderr)
}

// unmatched says, for each --skip and --rule-doc basename that no named file has,
// that it changed nothing: a name typed wrong is otherwise a silent no-op.
func unmatched(files []string, skips, ruleDocs baseList) []string {
	named := map[string]bool{}
	for _, f := range files {
		named[selftalk.Base(f)] = true
	}
	var out []string
	for _, l := range []struct {
		flag  string
		names baseList
		did   string
	}{{"--skip", skips, "skipped nothing"}, {"--rule-doc", ruleDocs, "marked nothing"}} {
		for _, n := range l.names {
			if !named[n] {
				out = append(out, l.flag+" "+n+" matched no file named on the line, so it "+l.did+" (it takes a basename, matched exactly)")
			}
		}
	}
	return out
}

// positionals returns the files after the flags, and the first argument that looks like a
// flag standing after a file (a late flag), or "".
func positionals(args []string, fset *flag.FlagSet) ([]string, string) {
	parsed := len(args) - fset.NArg()
	literal := false
	// Every scan flag but --json takes a value. A value spelled "--" is not the
	// terminator; walk the parsed prefix so only the separator protects files.
	for i := 0; i < parsed; i++ {
		if args[i] == "--" {
			literal = true
			break
		}
		if !strings.Contains(args[i], "=") && strings.TrimLeft(args[i], "-") != "json" {
			i++
		}
	}
	var files []string
	for _, arg := range fset.Args() {
		if !literal && arg == "--" {
			literal = true
			continue
		}
		if !literal && len(arg) > 1 && strings.HasPrefix(arg, "-") {
			return nil, arg
		}
		files = append(files, arg)
	}
	return files, ""
}

// readNamed reads one named file; "-" is standard input, read once however often it is named.
func readNamed(name string, stdin io.Reader, piped *[]byte) ([]byte, error) {
	if name != "-" {
		return os.ReadFile(name)
	}
	if *piped == nil {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		*piped = append([]byte{}, b...)
	}
	return *piped, nil
}

// cannotRead is the problem line for one unreadable file. A bare word that is no file is
// most often a verb guessed wrong, so that line names the verbs too.
func cannotRead(name string, err error) string {
	// Built by concatenation, not formatted: refuse escapes the whole line when it prints it.
	what := "cannot read " + oneline.Quote(name) + ": " + reason(err)
	if !strings.ContainsAny(name, `./\`) {
		what += "; it is not a verb either (the verbs are " + strings.Join(verbs, ", ") +
			"; a file of that name is ./" + name + ")"
	}
	return what
}

// reason is an error without the path a *fs.PathError repeats, since the line names the path.
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}

// out is the one value a scan prints (internal/tool's Out): every skip, banner and
// finding as an item in file order, each kind capped at --max on its own (six hundred
// STANDING claims must not eat the one INSTALLATION finding, the class a reader is
// least likely to know about), the closing counts as facts, and the notes. The typed
// lines (lines) and --json are its two renderings.
func (r report) out(maxLines int) *tool.Out {
	o := tool.Done()
	o.Verb = "scan"
	tally := bounded.NewTally(maxLines)
	shown := 0
	for _, p := range r.pages {
		if p.skipped {
			if tally.Add("skip") {
				o.Item("skip", "file", p.name)
			}
			continue
		}
		if p.ruledoc && tally.Add("ruledoc") {
			o.Item("ruledoc", "file", p.name, "banner", selftalk.RuleDocumentBanner)
		}
		for _, f := range p.findings {
			if tally.Add(f.class) {
				shown++
				o.Item(f.class, "file", p.name, "line", f.line, "shape", f.shape, "match", f.match, "text", f.text)
			}
		}
	}
	for _, kind := range tally.Kinds() {
		if tally.Shown(kind) < tally.Total(kind) {
			o.More = append(o.More, tool.More{Kind: kind, Shown: tally.Shown(kind), Total: tally.Total(kind), Remedy: maxRemedy})
		}
	}
	o.Fact("files", r.scanned).Fact("skipped", len(r.pages)-r.scanned).Fact("claims", r.claims).
		Fact("standing", r.standing).Fact("installations", r.installed).Fact("dated", r.dated).Fact("shown", shown)
	if r.standing > 0 || r.installed > 0 {
		o.Status, o.Exit = tool.Failed, 1
	}
	o.Notes = append(o.Notes, r.unmatched...)
	o.Note(note)
	return o
}

// fact is the count o's facts hold under k (out stores each as an int).
func fact(o *tool.Out, k string) int {
	for _, f := range o.Facts {
		if f.K == k {
			n, _ := f.V.(int) // ignored: out stores every fact as an int
			return n
		}
	}
	return 0
}

// field is the value of one of an item's fields.
func field(it tool.Item, k string) any {
	for _, f := range it.Fields {
		if f.K == k {
			return f.V
		}
	}
	return nil
}

// lines renders the scan's Out as this tool's typed lines: the findings and their MORE
// lines on stderr, one per line in line order, named by their shape alone (STANDING, or
// the second class's RANKING, FORECLOSURE, TRAIT, VERDICT-IDIOM); the skips, banners,
// their MORE lines, the counts and the notes on stdout.
func lines(o *tool.Out, stdout, stderr io.Writer) int {
	for _, it := range o.Items {
		switch it.Kind {
		case "skip":
			fmt.Fprintf(stdout, "SELFTALK SKIP %s (--skip)\n", oneline.Escape(field(it, "file").(string)))
		case "ruledoc":
			fmt.Fprintf(stdout, "SELFTALK RULEDOC %s: %s\n", oneline.Escape(field(it, "file").(string)), selftalk.RuleDocumentBanner)
		default:
			fmt.Fprintf(stderr, "SELFTALK FAIL %s:%d: %s match=%q: %s\n", oneline.Escape(field(it, "file").(string)), field(it, "line").(int),
				oneline.Escape(field(it, "shape").(string)), field(it, "match").(string), oneline.Escape(oneline.Cap(field(it, "text").(string), oneline.TailBytes)))
		}
	}
	more := func(w io.Writer, of func(kind string) bool) {
		for _, m := range o.More {
			if of(m.Kind) {
				fmt.Fprintln(w, bounded.MoreLine("SELFTALK", m.Kind, m.Shown, m.Total, m.Remedy))
			}
		}
	}
	notes := func() {
		for _, n := range o.Notes {
			fmt.Fprintf(stdout, "SELFTALK NOTE %s\n", oneline.Escape(n))
		}
	}
	more(stdout, func(k string) bool { return k == "skip" })
	if fact(o, "files") == 0 {
		fmt.Fprintf(stdout, "SELFTALK SKIP files=0 skipped=%d reason=all-skipped\n", fact(o, "skipped"))
		notes()
		return 0
	}
	more(stdout, func(k string) bool { return k == "ruledoc" })
	// the finding kinds' MORE lines follow their findings on stderr, in the order first met
	more(stderr, func(k string) bool { return k != "skip" && k != "ruledoc" })
	if fact(o, "dated") > 0 {
		fmt.Fprintf(stdout, "SELFTALK DATED n=%d files=%d\n", fact(o, "dated"), fact(o, "files"))
	}
	// THE COUNT LINE PRINTS ON FAILURE TOO: a scan that found six hundred things says the
	// number as well as the lines.
	if o.Status == tool.Failed {
		fmt.Fprintf(stdout, "SELFTALK FAIL files=%d claims=%d standing=%d installations=%d dated=%d shown=%d\n",
			fact(o, "files"), fact(o, "claims"), fact(o, "standing"), fact(o, "installations"), fact(o, "dated"), fact(o, "shown"))
	} else {
		fmt.Fprintf(stdout, "SELFTALK OK files=%d claims=%d standing=0 installations=0 dated=%d\n",
			fact(o, "files"), fact(o, "claims"), fact(o, "dated"))
	}
	notes()
	return o.Exit
}
