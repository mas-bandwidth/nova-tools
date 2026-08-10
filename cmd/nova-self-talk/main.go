// nova-self-talk classifies first-person self-claims in prose: sentences
// about what the writer permanently IS or permanently CANNOT do. It is an
// advisory instrument, not a wall — it flags standing claims; whether to
// date one, cut one, or keep one is the writer's judgment, never the tool's.
//
// Exit 0 no standing claims, 1 standing claims found, 2 could not run.
// Every file is named by the caller and nothing is skipped by default.
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
  nova-self-talk [--skip <basename>]... <file>...

Finds first-person claims about what the writer permanently IS or permanently
CANNOT do. A claim carrying a date marker is DATED — a measurement, a record,
welcome. One without is STANDING and is flagged: date it, cut it, or keep it
on purpose — the judgment is the writer's, and this tool never makes it.

  --skip <basename>   do not scan files with this basename (repeatable).
                      For your rule documents: a rule document is a list of
                      absolutes and will always flag, and flagging is it
                      working — skip it by name; never soften a rule to
                      improve a score. Nothing is skipped by default.

Flags come before files. Exit codes: 0 no standing claims, 1 standing claims
found, 2 could not run (bad invocation, unreadable file).
`

// note prints on every completed run, pass or fail: a green from a partial
// check reads exactly like a green from a complete one, and this check is
// structurally partial.
const note = "SELFTALK NOTE catches one class only: first-person claims in negative vocabulary; " +
	"trait claims in neutral words escape it by design. A green clears one class, never the file.\n"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// skipList is the repeatable --skip flag: basenames that must not be
// scanned. It refuses paths — the match is decided on basenames
// (selftalk.Base), and a value with a separator in it would silently never
// match anything.
type skipList []string

func (s *skipList) String() string { return strings.Join(*s, ",") }

func (s *skipList) Set(v string) error {
	if v == "" {
		return errors.New("needs a basename; refusing to guess")
	}
	if strings.ContainsAny(v, `/\`) {
		return fmt.Errorf("takes a basename, not a path: %q", v)
	}
	*s = append(*s, v)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-self-talk", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // errors are printed by Parse; usage is printed below, where we decide the stream
	var skips skipList
	fs.Var(&skips, "skip", "basename to skip, repeatable (rule documents always flag; skip them, never soften them)")
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

	skipped := make(map[string]bool, len(skips))
	for _, s := range skips {
		skipped[s] = true
	}

	scanned, claims, standing := 0, 0, 0
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
		for _, c := range selftalk.Scan(string(b)) {
			claims++
			if c.Verdict == selftalk.Standing {
				standing++
				fmt.Fprintf(stderr, "SELFTALK FAIL %s: %s: %s\n", f, c.Verdict, c.Text)
			} else {
				fmt.Fprintf(stdout, "SELFTALK %s %s: %s\n", c.Verdict, f, c.Text)
			}
		}
	}

	if standing == 0 {
		fmt.Fprintf(stdout, "SELFTALK OK files=%d claims=%d standing=0\n", scanned, claims)
	}
	fmt.Fprint(stdout, note)
	if standing > 0 {
		return 1
	}
	return 0
}
