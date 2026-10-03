// nova-self-talk classifies self-claims in prose, in two disjoint classes:
// STANDING, a first-person claim carrying a word of failure, and INSTALLATION,
// a standing self-verdict built from neutral words. It is an advisory
// instrument, not a wall: whether to date a finding, cut it, relocate it, or
// keep it is the writer's judgment, never the tool's.
//
// Exit 0 means no findings, exit 1 means any finding, and exit 2 means the
// verb could not run. Every file is named by the caller; nothing is skipped
// by default and no basename is special by default. Dispatch, help, version,
// refusals and the output envelope are internal/tool's. A scan's findings go
// to stderr and its summary to stdout, which is the skeleton's Findings
// rendering (docs/CLI-STYLE.md (e)).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The hints below turn this binary's most-hit refusals into a next step. The
// no-guessing law is unchanged — naming no files is still exit 2 — but a
// refusal that names only what was wrong leaves a first caller to guess what
// the tool wanted, which is the same guessing the tool refuses to do, moved
// onto the reader.
const (
	filesHint  = `nova-self-talk takes markdown FILES, named on the command line: nova-self-talk <file>... (- is stdin). There is no default set and no directory walk, so a shell glob is the usual first run (nova-self-talk memory/*.md); nova-self-talk example --dir ./pages writes two pages to try it on.`
	unreadHint = `NOTHING was scanned, which is not a green: a file this tool cannot read is not a clean one. Fix or drop the paths above and run again.`
	baseHint   = `--skip and --rule-doc take a BASENAME, not a path: --skip RULES.md, never --skip memory/RULES.md. The match is on the file's name wherever it sits, and nothing is skipped or banner-marked by default.`
)

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "catches known SHAPES only (list them: nova-self-talk shapes): register, irony and " +
	"quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the " +
	"grammar and a false one on the meaning. A green clears the known shapes, never the file."

// verbs are the words that are a verb in first position; anything else is a file.
var verbs = []string{"scan", "shapes", "example", "version", "help"}

func main() { os.Exit(selfTalk(os.Args[1:]).Main()) }

// selfTalk is the command. args is the invocation the scan verb reads its
// files from: the skeleton keeps those positionals on the flag set and does
// not hand them back, so the verb holds the slice it was called with.
func selfTalk(args []string) *tool.Tool {
	var fs *tool.Flags
	return &tool.Tool{
		Name:    "nova-self-talk",
		What:    "flags sentences where a writer passes a standing verdict on themselves",
		Stamp:   version,
		Default: "scan",
		Words:   []string{"SKIP"},
		How: `each named file (- is stdin) is read sentence by sentence against one shape table.
STANDING is a first-person failure word; INSTALLATION a neutral-worded verdict.
A dated claim is a record, counted, never quoted. A scan writes nothing.
shapes prints the table. State lives in the named files, never in this tool.
first run: nova-self-talk example --dir ./pages`,
		ExitTable: "0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation, unreadable file).",
		Verbs: []tool.Verb{
			{
				Name:  "scan",
				Usage: "[--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...\nscan [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...",
				Example: "./pages/journal.md\n" +
					"--rule-doc RULES.md ./pages/RULES.md ./pages/journal.md\n" +
					"--skip RULES.md ./pages/RULES.md ./pages/journal.md",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					fs = f
					var skips, ruleDocs baseList
					f.Var(&skips, "skip", "basename to skip, repeatable (nothing is skipped by default)")
					f.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable (empty by default)")
					f.Max()
					f.Check(func(c *tool.Call) {
						files, late := positionals(verbArgs(args), fs.FlagSet)
						if late != "" {
							c.Problem(fmt.Sprintf("flags come before files (got %q); use -- before a filename beginning with a dash", late))
							return
						}
						if len(files) == 0 {
							c.Problem("no files named; refusing to guess")
							c.Problem(filesHint)
						}
					})
				},
				Run: func(c *tool.Call) *tool.Out { return scan(c, verbArgs(args), fs) },
			},
			{
				Name:   "shapes",
				Usage:  "shapes [--json]",
				Effect: tool.Inspection,
				Detail: shapeCatalogue,
				Flags:  func(*tool.Flags) {},
				Run:    cmdShapes,
			},
			{
				Name:   "example",
				Usage:  "example [--dir <dir>] [--dry-run] [--json]",
				Effect: tool.LocalWrite,
				DryRun: true,
				Detail: "Writes the two example pages into --dir. A page already there with the same bytes is kept; one with other bytes is never replaced.",
				Flags: func(f *tool.Flags) {
					f.Required("dir", "one directory to write the pages into, as nova-self-talk example --dir ./pages")
				},
				Run: cmdExample,
			},
		},
	}
}

// verbArgs is the scan verb's own arguments: the word scan is the verb, and
// every other first word is a file or a flag of the default verb.
func verbArgs(args []string) []string {
	if len(args) > 0 && args[0] == "scan" {
		return args[1:]
	}
	return args
}

// baseList is the value type behind both repeatable basename flags, --skip
// and --rule-doc. It refuses paths: the match is decided on basenames
// (selftalk.Base), and a value with a separator in it would silently never
// match anything. Repeatable cannot carry that refusal, because it drops an
// empty value and splits on commas, so a basename stays this flag's own value.
//
// BOTH LISTS DEFAULT TO EMPTY, which is the no-defaults law applied to scope.
// This tool's ancestor hardcoded one repo's rule-document names; the condition
// of promotion here was that the list move to the caller and the default
// become empty, and that condition governs the banner list exactly as it
// governs the skip list.
type baseList []string

func (s *baseList) String() string { return strings.Join(*s, ",") }

func (s *baseList) Set(v string) error {
	if v == "" {
		return errors.New(baseHint)
	}
	if strings.ContainsAny(v, `/\`) {
		return fmt.Errorf("takes a basename, not a path: %q -- the match is on the file's name wherever it sits", v)
	}
	*s = append(*s, v)
	return nil
}

func (s *baseList) Get() any { return []string(*s) }

func set(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, v := range l {
		m[v] = true
	}
	return m
}

// scan reads every named file and returns one result: findings of each class
// are items the skeleton caps with --max and sends to stderr, and the summary
// stays on stdout (docs/CLI-STYLE.md (e), the one exception, via Findings).
func scan(c *tool.Call, args []string, fs *tool.Flags) *tool.Out {
	files, _ := positionals(args, fs.FlagSet)
	skipped, pinned := set(c.Get("skip").([]string)), set(c.Get("rule-doc").([]string))

	// EVERY NAMED FILE IS READ BEFORE ANY OF THEM IS SCANNED, and every unreadable one is
	// reported rather than the first: a caller who mistyped three paths learns about three
	// in one go, and a run that printed findings and then refused would be reporting
	// findings from a run that did not happen.
	contents := make([]string, len(files))
	var problems []string
	unread := 0
	var piped []byte
	for i, f := range files {
		if skipped[selftalk.Base(f)] {
			continue
		}
		b, err := readNamed(f, c.Stdin, &piped)
		if err != nil {
			unread++
			problems = append(problems, cannotRead(f, err))
			continue
		}
		contents[i] = string(b)
	}
	if len(problems) > 0 {
		o := tool.Refuse(problems...)
		if unread > 0 {
			o.Note(unreadHint)
		}
		return o
	}

	o := tool.Done()
	var scanned, claims, standing, installed, dated int
	for i, f := range files {
		if skipped[selftalk.Base(f)] {
			o.ItemText("skip", "(--skip)", "file", f)
			continue
		}
		scanned++
		var page []tool.Item
		for _, claim := range selftalk.Scan(contents[i]) {
			claims++
			if claim.Verdict != selftalk.Standing {
				// A DATED CLAIM IS THE WELCOME CASE. It is a count, never a quoted sentence.
				dated++
				continue
			}
			standing++
			page = append(page, item("standing", f, claim.Line, string(claim.Verdict), claim.Match, claim.Text))
		}
		for _, in := range selftalk.ScanInstallation(contents[i]) {
			installed++
			page = append(page, item("installation", f, in.Line, string(in.Shape), in.Match, in.Text))
		}
		// The banner is the first item of a file that has findings, so a reader meets
		// the sentence that says what a finding there is for before the finding.
		if pinned[selftalk.Base(f)] && len(page) > 0 {
			o.ItemText("ruledoc", selftalk.RuleDocumentBanner, "file", f)
		}
		o.Items = append(o.Items, page...)
	}
	if scanned == 0 {
		o.Status, o.Exit, o.Word = tool.OK, 0, "SKIP"
	}
	if standing > 0 || installed > 0 {
		o.Status, o.Exit, o.Word = tool.Failed, 1, ""
	}
	shown := shownOf(c.Int("max"), standing, installed)
	o.Fact("files", scanned).Fact("claims", claims).Fact("standing", standing).
		Fact("installations", installed).Fact("dated", dated).Fact("shown", shown).
		Fact("skipped", len(files)-scanned)
	if scanned == 0 {
		o.Fact("reason", "all-skipped")
	}
	if dated > 0 {
		o.Item("dated", "n", dated, "files", scanned)
	}
	o.Note(note)
	return o.Findings("standing", "installation")
}

// item is one finding row: file, line and shape are typed fields, and the
// matched words and the sentence are prose (tool.Text) so a gap in the words stays a gap.
func item(kind, file string, line int, shape, match, text string) tool.Item {
	return tool.Item{Kind: kind, Fields: tool.Fields{
		{K: "file", V: file}, {K: "line", V: line}, {K: "shape", V: shape},
		{K: "match", V: tool.Text(match)}, {K: "text", V: tool.Text(text)},
	}}
}

// shownOf is how many finding lines --max will print, counted the way the
// skeleton's cap counts: each class has its own ceiling, and 0 lists all.
func shownOf(max, standing, installed int) int {
	if max == 0 {
		return standing + installed
	}
	if standing > max {
		standing = max
	}
	if installed > max {
		installed = max
	}
	return standing + installed
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
