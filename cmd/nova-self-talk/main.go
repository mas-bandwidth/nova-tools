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

	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
)

const usage = `nova-self-talk: the self-talk register, classified (see SPEC.md)

usage:
  nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... <file>...

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

Flags come before files. Exit codes: 0 no findings, 1 findings, 2 could not
run (bad invocation, unreadable file).
`

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen " +
	"context are invisible to grammar, and a quoted verdict is a true positive on the grammar " +
	"and a false one on the meaning. A green clears the known shapes, never the file.\n"

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
		return fmt.Errorf("takes a basename, not a path: %q", v)
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
	fs := flag.NewFlagSet("nova-self-talk", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // errors are printed by Parse; usage is printed below, where we decide the stream
	var skips, ruleDocs baseList
	fs.Var(&skips, "skip", "basename to skip, repeatable (nothing is skipped by default)")
	fs.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable (empty by default)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprint(stderr, usage)
		fmt.Fprintln(stderr, "nova-self-talk: no files named; refusing to guess")
		return 2
	}

	skipped, pinned := set(skips), set(ruleDocs)

	scanned, claims, standing, installed := 0, 0, 0, 0
	for _, f := range files {
		if skipped[selftalk.Base(f)] {
			fmt.Fprintf(stdout, "SELFTALK SKIP %s (--skip)\n", f)
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "nova-self-talk: %v\n", err)
			return 2
		}
		scanned++
		text := string(b)
		for _, c := range selftalk.Scan(text) {
			claims++
			if c.Verdict == selftalk.Standing {
				standing++
				fmt.Fprintf(stderr, "SELFTALK FAIL %s: %s: %s\n", f, c.Verdict, c.Text)
			} else {
				fmt.Fprintf(stdout, "SELFTALK %s %s: %s\n", c.Verdict, f, c.Text)
			}
		}
		found := selftalk.ScanInstallation(text)
		// The banner prints ONCE per file that has findings, before them, so a
		// reader cannot meet a finding in a rule document without meeting the
		// sentence that says what it is for.
		if len(found) > 0 && pinned[selftalk.Base(f)] {
			fmt.Fprintf(stdout, "SELFTALK RULEDOC %s: %s\n", f, selftalk.RuleDocumentBanner)
		}
		for _, i := range found {
			installed++
			fmt.Fprintf(stderr, "SELFTALK FAIL %s:%d: INSTALLATION %s: %s\n", f, i.Line, i.Shape, i.Text)
		}
	}

	if standing == 0 && installed == 0 {
		fmt.Fprintf(stdout, "SELFTALK OK files=%d claims=%d standing=0 installations=0\n", scanned, claims)
	}
	fmt.Fprint(stdout, note)
	if standing > 0 || installed > 0 {
		return 1
	}
	return 0
}
