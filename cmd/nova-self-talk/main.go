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
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
)

const usage = `nova-self-talk: the self-talk register, classified (see docs/SPEC.md)

usage:
  nova-self-talk version    print this build identity (--version also accepted)
  nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] <file>...
  nova-self-talk help

Two disjoint classes.

  STANDING / DATED   a first-person claim, in negative vocabulary, about what
                     the writer permanently IS or permanently CANNOT do. With
                     a date marker it is DATED — a measurement, a record,
                     welcome. Without one it is STANDING and is flagged.

  INSTALLATION       a standing self-verdict built from NEUTRAL words, which
                     the first class cannot see: a self-superlative (RANKING),
                     a door stated shut (FORECLOSURE), a verdict on a practice
                     (VERDICT-IDIOM), or a habitual self-report (TRAIT). Dated
                     ones are exempt here too.

Date it, cut it, relocate it, or keep it on purpose — the judgment is the
writer's, and this tool never makes it.

  --skip <basename>       do not scan files with this basename (repeatable).
                          Nothing is skipped by default.
  --rule-doc <basename>   scan the file, but print its findings under a banner
                          saying a finding there is a self-verdict to relocate
                          and NEVER a reason to soften a rule (repeatable).
                          For your rule documents. No basename is special by
                          default: one repo's filenames are not this tool's.
  --max <n>               how many finding lines to PRINT, per class, before
                          one MORE line stands for the rest. Default 20, and 0
                          means all. The closing line carries the totals
                          whichever way the run went, so a scan that found six
                          hundred says six hundred without printing six
                          hundred. DATED claims are never listed at all: they
                          are the WELCOME case -- a measurement, a record --
                          and they print as one count.

Flags come before files. Exit codes: 0 no findings, 1 findings, 2 could not
run (bad invocation, unreadable file).

example:
  nova-self-talk ./pages/journal.md
  nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md

Both exit 1, and that is the tool working: a finding is a sentence to date,
cut, relocate or keep on purpose, never a failure. ./pages is a directory of
yours; cmd/nova-self-talk/testdata/example-pages in this repo is one the size
of a first run, and both lines are run against it by the tests.
`

// The hints below turn this binary's two most-hit refusals into a next step.
// The no-guessing law is unchanged -- naming no files is still exit 2 -- but a
// refusal that names only what was wrong leaves a first caller to guess what
// the tool wanted, which is the same guessing the tool refuses to do, moved
// onto the reader.
const (
	filesHint = `nova-self-talk takes markdown FILES, named on the command line: nova-self-talk <file>... There is no default set and no directory walk, so a shell glob is the usual first run (nova-self-talk memory/*.md) -- every file scanned is one you chose, and one this tool cannot read is not a clean one.`
	baseHint  = `--skip and --rule-doc take a BASENAME, not a path: --skip RULES.md, never --skip memory/RULES.md. The match is on the file's name wherever it sits, and nothing is skipped or banner-marked by default.`
)

// hintFor returns the already-indented hint line for a kind of refusal,
// newline included. It returns package constants only, which is why printing
// its result is safe.
func hintFor(kind string) string {
	switch kind {
	case "files":
		return "  " + filesHint + "\n"
	case "basename":
		return "  " + baseHint + "\n"
	}
	return ""
}

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen " +
	"context are invisible to grammar, and a quoted verdict is a true positive on the grammar " +
	"and a false one on the meaning. A green clears the known shapes, never the file.\n"

// maxRemedy is the second half of every MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 prints every finding"

// refuse is what an unusable invocation costs: ONE line naming what was wrong, and the
// door to the usage rather than the usage itself. It was the whole 40-line banner.
func refuse(stderr io.Writer, what, hint string) int {
	fmt.Fprintf(stderr, "nova-self-talk: %s; run: nova-self-talk help\n%s", oneline.Escape(what), hintFor(hint))
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// baseList is the value type behind both repeatable basename flags, --skip
// and --rule-doc. It refuses paths — the match is decided on basenames
// (selftalk.Base), and a value with a separator in it would silently never
// match anything.
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

func run(args []string, stdout, stderr io.Writer) int {
	// `help` is a word rather than a flag because the refusals now name it: a one-line
	// refusal that ends in `run: nova-self-talk help` has to be telling the truth.
	if len(args) == 1 && args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	// `version` is a word here for the same reason `help` is, and it is recognised in
	// the same place: as the WHOLE invocation, before the flag set is built. This tool
	// takes files positionally, so a lone `version` is a file named version to the
	// parser -- the same collision `help` already has, and the same answer, because a
	// verb somebody has to remember to spell differently is a verb nobody uses. A file
	// actually named `version` is still scanned when it is named beside another.
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		return cmdVersion(nil, stdout, stderr)
	}
	fs := flag.NewFlagSet("nova-self-talk", flag.ContinueOnError)
	// Package flag is given no stream: its error text quotes the argument it could
	// not parse, raw, so an argument holding a newline authored a whole line of
	// stderr before any code in this file ran. The refusal is printed below,
	// escaped, and usage is printed where this file decides the stream.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var skips, ruleDocs baseList
	fs.Var(&skips, "skip", "basename to skip, repeatable (nothing is skipped by default)")
	fs.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable (empty by default)")
	max := fs.Int("max", bounded.Default, "finding lines to print per class before one MORE line stands for the rest; 0 prints all")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		return refuse(stderr, oneline.Cap(err.Error(), oneline.TailBytes), "basename")
	}
	if *max < 0 {
		// Zero already means "all", so a negative ceiling is a typo with two readings
		// and gets neither.
		return refuse(stderr, fmt.Sprintf("--max must be a line ceiling of zero or more (got %d); 0 means print them all", *max), "")
	}
	files := fs.Args()
	if len(files) == 0 {
		return refuse(stderr, "no files named; refusing to guess", "files")
	}

	skipped, pinned := set(skips), set(ruleDocs)

	// EVERY NAMED FILE IS READ BEFORE ANY OF THEM IS SCANNED, and every
	// unreadable one is reported rather than the first. Two reasons, and both
	// are a first run's: a caller who mistyped three paths should learn about
	// three in one go, and a run that printed findings and then refused would
	// be reporting findings from a run that did not happen.
	contents := make([]string, len(files))
	readable := true
	for i, f := range files {
		if skipped[selftalk.Base(f)] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "nova-self-talk: %s\n", oneline.Err(err))
			readable = false
			continue
		}
		contents[i] = string(b)
	}
	if !readable {
		return refuse(stderr, "NOTHING was scanned, which is not a green", "files")
	}

	// THE TWO CLASSES ARE CAPPED SEPARATELY, for the same reason the classes exist: six
	// hundred STANDING claims must not be able to eat the one INSTALLATION finding, which
	// is the one the first class cannot see and the reader is least likely to know about.
	fails := bounded.Grouped(stderr, *max, "SELFTALK", maxRemedy)
	skipLines := bounded.Capped(stdout, *max, "SELFTALK", "skip", maxRemedy)
	banners := bounded.Capped(stdout, *max, "SELFTALK", "ruledoc", maxRemedy)

	scanned, claims, standing, installed, dated := 0, 0, 0, 0, 0
	for i, f := range files {
		if skipped[selftalk.Base(f)] {
			skipLines.Line(fmt.Sprintf("SELFTALK SKIP %s (--skip)", oneline.Escape(f)))
			continue
		}
		scanned++
		text := contents[i]
		for _, c := range selftalk.Scan(text) {
			claims++
			if c.Verdict == selftalk.Standing {
				standing++
				fails.Line("standing", fmt.Sprintf("SELFTALK FAIL %s: %s: %s",
					oneline.Escape(f), c.Verdict, oneline.Escape(oneline.Cap(c.Text, oneline.TailBytes))))
				continue
			}
			// A DATED CLAIM IS THE WELCOME CASE, and it was half the output: six hundred
			// of them quoted a whole sentence each to say, six hundred times, that the
			// writer had done the thing this tool asks for. It is a count now. Nothing
			// is lost that the file does not already hold, and a run that dated
			// everything went from 78K tokens to one line.
			dated++
		}
		found := selftalk.ScanInstallation(text)
		// The banner prints ONCE per file that has findings, before them, so a
		// reader cannot meet a finding in a rule document without meeting the
		// sentence that says what it is for.
		if len(found) > 0 && pinned[selftalk.Base(f)] {
			banners.Line(fmt.Sprintf("SELFTALK RULEDOC %s: %s", oneline.Escape(f), selftalk.RuleDocumentBanner))
		}
		for _, i := range found {
			installed++
			fails.Line("installation", fmt.Sprintf("SELFTALK FAIL %s:%d: INSTALLATION %s: %s",
				oneline.Escape(f), i.Line, i.Shape, oneline.Escape(oneline.Cap(i.Text, oneline.TailBytes))))
		}
	}
	skipLines.More()
	banners.More()
	fails.More()

	if dated > 0 {
		fmt.Fprintf(stdout, "SELFTALK DATED n=%d files=%d\n", dated, scanned)
	}
	// THE COUNT LINE PRINTS ON FAILURE TOO. It printed only on a clean run, so a scan
	// that found six hundred things gave six hundred lines and never the number.
	if standing > 0 || installed > 0 {
		fmt.Fprintf(stdout, "SELFTALK FAIL files=%d claims=%d standing=%d installations=%d dated=%d shown=%d\n",
			scanned, claims, standing, installed, dated, fails.Shown())
	} else {
		fmt.Fprintf(stdout, "SELFTALK OK files=%d claims=%d standing=0 installations=0 dated=%d\n", scanned, claims, dated)
	}
	fmt.Fprint(stdout, note)
	if standing > 0 || installed > 0 {
		return 1
	}
	return 0
}
