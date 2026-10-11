// scan reads the named files sentence by sentence and flags each standing self-verdict — the plain run's scan, named as a verb.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "catches known SHAPES only (list them: nova-self-talk shapes): register, irony and " +
	"quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the " +
	"grammar and a false one on the meaning. A green clears the known shapes, never the file."

// maxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 prints every finding"

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

func scan(args []string, stdin io.Reader, stdout, stderr io.Writer, wd string) int {
	fset := verbflag.New("scan")
	var skips, ruleDocs baseList
	fset.Var(&skips, "skip", "basename to skip, repeatable (nothing is skipped by default)")
	fset.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable (empty by default)")
	maxLines := fset.Int("max", bounded.Default, "finding lines to print per class before one MORE line stands for the rest; 0 prints all")
	fset.Bool("json", false, "print the run as one JSON object on stdout instead of lines")
	// FLAGS MAY STAND BEFORE, BETWEEN OR AFTER THE FILES, as nova-memory's parse lets
	// them and as docs/STANDARD.md section 1 wants (one shape across the set):
	// flagsAndFiles moves them ahead of the files, so the flag package parses every
	// one, and a `--` the caller writes ends the flags.
	flags, files := flagsAndFiles(args, fset)
	asJSON := verbflag.BoolAsked(flags, "json")
	// The flags parse on their own: they are self-contained after the split (each
	// value-taking flag holds its value), and a missing value is the flag package's
	// own "needs an argument" rather than the next file.
	if err := verbflag.Parse(fset, flags); err != nil {
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
		b, err := readNamed(f, stdin, &piped, wd)
		if err != nil {
			unread++
			problems = append(problems, cannotRead(f, err))
			continue
		}
		contents[i] = string(b)
		// A byte page is not text, and a file this tool cannot read is not a clean
		// one (docs/STANDARD.md section 2): a binary decodes into a string that scans
		// with no claims, which reads as SELFTALK OK. Refuse it with the unreadable
		// files, naming the file.
		if !utf8.Valid(b) {
			unread++
			problems = append(problems, cannotRead(f, fmt.Errorf("file is not valid UTF-8")))
			continue
		}
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

// flagsAndFiles splits one scan invocation into its flags and its files. Flags
// may stand before, between or after the files (docs/STANDARD.md section 1, one
// shape across the set; nova-memory's parse takes them anywhere), so the flags
// are moved ahead of the files for the one parse, where each value-taking flag
// keeps its value. A `--` the caller writes ends the flags; a value of a flag
// that takes one (--skip --) is that value, never the terminator; a lone `-` is
// standard input, a file.
func flagsAndFiles(args []string, fset *flag.FlagSet) (flags, files []string) {
	literal := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case literal:
			files = append(files, a)
		case a == "--":
			literal = true
		case len(a) > 1 && strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
			f := fset.Lookup(name)
			if f != nil && !inline {
				if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
					if i+1 < len(args) {
						i++
						flags = append(flags, args[i])
					}
				}
			}
		default:
			files = append(files, a)
		}
	}
	return flags, files
}

// openPath is the path a read opens. A relative name is opened against wd, the
// working directory main passes from os.Getwd and a test passes as its own
// (docs/STANDARD.md section 8). The name printed stays the caller's words.
func openPath(wd, name string) string {
	if name == "-" || name == "" || wd == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(wd, name)
}

// readNamed reads one named file; "-" is standard input, read once however often it is named.
func readNamed(name string, stdin io.Reader, piped *[]byte, wd string) ([]byte, error) {
	if name != "-" {
		return os.ReadFile(openPath(wd, name))
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

// out is the one value a scan prints (pkg/tool's Out): every skip, banner and
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
