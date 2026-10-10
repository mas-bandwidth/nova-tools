// nova-self-talk classifies self-claims in prose, in two disjoint classes:
// STANDING/DATED — what the writer permanently IS or permanently CANNOT do,
// in negative vocabulary — and INSTALLATION — a standing self-verdict built
// from neutral words, which the first class cannot see. It is an advisory
// instrument, not a wall: whether to date a finding, cut it, relocate it, or
// keep it is the writer's judgment, never the tool's.
// Finding match and text are capped at oneline.TailBytes in both line and JSON renderings.
//
// Exit 0 no findings, 1 any finding, 2 could not run. Every file is named by
// the caller; nothing is skipped by default and no basename is special by
// default — --skip and --rule-doc are both the caller's, per run.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
else is the first file, so a file named like a verb is given as ./scan. Flags may stand
before, between or after the files; -- ends the flags, for a file whose name begins with
a dash.

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
  Findings print on stderr in line order; skips, banners, the DATED count, the closing
  count line, SKIP files=0 and NOTE print on stdout. Run with 2>/dev/null: the count
  line, DATED and NOTE remain on stdout.
  SELFTALK FAIL <file>:<line>: <SHAPE> match="<words>": <sentence>
                       <SHAPE> is STANDING for the first class, else the second class's shape:
                       RANKING, FORECLOSURE, VERDICT-IDIOM or TRAIT
  SELFTALK SKIP <file> (--skip)
  SELFTALK RULEDOC <file>: <banner>     above the findings of a --rule-doc file
  SELFTALK MORE kind=<class> shown=<n> total=<t> <remedy>
  SELFTALK DATED n=<k> files=<n>
  SELFTALK OK|FAIL files=<n> claims=<n> standing=<n> installations=<n> dated=<n> [shown=<n>]
  SELFTALK SKIP files=0 skipped=<n> reason=all-skipped
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

setup:
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
	if h, ok := hints[kind]; ok {
		return "  " + h + "\n"
	}
	return ""
}

// hints is the one hint each kind of refusal carries; hintFor indents it and
// adds the newline.
var hints = map[string]string{
	"files":    filesHint,
	"unread":   unreadHint,
	"basename": baseHint,
}

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

func main() {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	os.Exit(runStdin(wd, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// runStdin runs one invocation. wd is the working directory a relative file is
// read against: main passes os.Getwd, a test passes a directory of its own
// (docs/STANDARD.md section 8), and an empty wd leaves the path for the OS.
func runStdin(wd string, args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// `<verb> -h` prints that verb's help on stdout at exit 0, with the verb's effect, before anything is read
	// (docs/CLI-STYLE.md rule (b)).
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
			return runStdin(wd, []string{args[1], "-h"}, stdin, stdout, stderr)
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
	return scan(args, stdin, stdout, stderr, wd)
}

// reason is an error without the path a *fs.PathError repeats, since the line names the path.
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}
