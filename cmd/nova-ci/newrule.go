package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
)

// cmdNewRule is `nova-ci new-rule [--root <checkout>] [--dry-run] <rule-name>`.
func cmdNewRule(args []string, stdout, stderr io.Writer) int {
	return cmdScaffold("new-rule", "one argument, the rule's name (lowercase letters, digits, - or _)", 1, args, stdout, stderr,
		func(root string, a []string) ([]scaffold.Planned, error) { return scaffold.RuleFiles(root, a[0]) }, nil)
}

// cmdNewVerb is `nova-ci new-verb [--root <checkout>] [--dry-run] <tool> <verb>`;
// it refuses a verb the tool's help already lists, after writing it prints the
// dispatch case to add, and it never edits the switch.
func cmdNewVerb(args []string, stdout, stderr io.Writer) int {
	return cmdScaffold("new-verb", "two arguments, the tool (its directory under cmd/) and the new verb's name", 2, args, stdout, stderr,
		func(root string, a []string) ([]scaffold.Planned, error) {
			if helpListsVerb(a[0], a[1]) {
				return nil, fmt.Errorf("%q is already a verb %s help lists; new-verb wants a name help does not list", a[1], a[0])
			}
			return scaffold.VerbFiles(root, a[0], a[1])
		},
		func(a []string) { fmt.Fprint(stdout, scaffold.DispatchNote(a[0], a[1])) })
}

// cmdScaffold is the one body of the two scaffolding verbs: parse, render the
// files, then write them (or, with --dry-run, check them and list them). A
// file already there, a bad name, or a root that is not a checkout is a
// refusal at exit 2 and writes nothing.
func cmdScaffold(verb, wants string, nargs int, args []string, stdout, stderr io.Writer,
	plan func(root string, a []string) ([]scaffold.Planned, error), after func(a []string)) int {
	where := " " + verb
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	root := fs.String("root", ".", "the nova-tools checkout to write into (default the current directory)")
	dryRun := fs.Bool("dry-run", false, "list the files it would write, checked against the tree, and write nothing")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, verbflag.Explain(fs, err))
	}
	if fs.NArg() != nargs {
		return refuse(stderr, where, fmt.Sprintf("%s wants %s, after the flags; got %d", verb, wants, fs.NArg()))
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return refuse(stderr, where, err.Error())
	}
	outs, err := plan(abs, fs.Args())
	if err != nil {
		return refuse(stderr, where, err.Error())
	}
	if *dryRun {
		rels, err := scaffold.Check(abs, outs)
		if err != nil {
			return refuse(stderr, where, err.Error())
		}
		for _, p := range rels {
			fmt.Fprintln(stdout, "would write "+p)
		}
		fmt.Fprintf(stdout, "nova-ci%s NOTE --dry-run wrote nothing; run it again without --dry-run to write these %d files\n", where, len(rels))
		return 0
	}
	written, err := scaffold.Write(abs, outs)
	if err != nil {
		return refuse(stderr, where, err.Error())
	}
	for _, p := range written {
		fmt.Fprintln(stdout, "wrote "+p)
	}
	if after != nil {
		after(fs.Args())
	}
	return 0
}
