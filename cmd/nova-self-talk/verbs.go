package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// cmdShapes prints the detector table the scan uses, row for row (selftalk.Rules): what each
// row finds, how, one sentence it reports and one near miss it passes. It is the same table the
// scan walks and the one its tests run, so the listing cannot claim a shape the scan misses.
func cmdShapes(args []string, stdout, stderr io.Writer) int {
	asJSON := verbflag.BoolAsked(args, "json")
	fset := verbflag.New("shapes")
	fset.Bool("json", false, "print the table as one JSON object on stdout instead of lines")
	if err := verbflag.Parse(fset, args); err != nil {
		return refuse(stdout, stderr, asJSON, "shapes", "", oneline.Cap(err.Error(), oneline.TailBytes)+"; the one flag is --json")
	}
	if fset.NArg() > 0 {
		return refuse(stdout, stderr, asJSON, "shapes", "", fmt.Sprintf("takes no arguments, got %q", fset.Arg(0)))
	}
	rules := selftalk.Rules()
	count := map[string]int{}
	for _, r := range rules {
		count[r.Class]++
	}
	if asJSON {
		o := tool.Done()
		o.Verb = "shapes"
		o.Fact("rows", len(rules)).Fact("standing", count["standing"]).
			Fact("installation", count["installation"]).Fact("licensed", count["licensed"])
		for _, r := range rules {
			o.Item(r.Class, "shape", r.Name, "says", r.Says, "finds", r.Finds, "passes", r.Passes, "pattern", r.Pattern)
		}
		o.Render(stdout, true)
		return 0
	}
	fmt.Fprintf(stdout, "SHAPES OK rows=%d standing=%d installation=%d licensed=%d\n",
		len(rules), count["standing"], count["installation"], count["licensed"])
	for _, r := range rules {
		fmt.Fprintf(stdout, "SHAPES ROW class=%s shape=%s says=%q finds=%q passes=%q pattern=%q\n",
			oneline.Field(r.Class), oneline.Field(r.Name), r.Says, r.Finds, r.Passes, r.Pattern)
	}
	fmt.Fprintf(stdout, "SHAPES NOTE %s\n", shapesNote)
	return 0
}

// shapesNote says how to read a row, and where to try one.
const shapesNote = "a standing or installation row reports its finds= sentence and not its passes= one; " +
	"a licensed row's finds= is reported and its passes= is not, because of the licence. " +
	"Try them: nova-self-talk example ./pages, then nova-self-talk ./pages/journal.md"

// pages are the two example pages, inside the binary, so a first run needs nothing but it.
//
//go:embed testdata/example-pages/*.md
var pages embed.FS

// cmdExample writes the example pages into a directory of the caller's: a local write and the
// only write this tool makes. A page already there with the same bytes is kept; one with other
// bytes is never replaced, and the run refuses before writing anything.
func cmdExample(args []string, stdout, stderr io.Writer) int {
	asJSON := verbflag.BoolAsked(args, "json")
	fset := verbflag.New("example")
	dry := fset.Bool("dry-run", false, "print what would be written and write nothing")
	fset.Bool("json", false, "print the result as one JSON object on stdout instead of a line")
	if err := verbflag.Parse(fset, args); err != nil {
		return refuse(stdout, stderr, asJSON, "example", "", oneline.Cap(err.Error(), oneline.TailBytes)+"; the flags are --dry-run, --json")
	}
	if fset.NArg() != 1 {
		return refuse(stdout, stderr, asJSON, "example", "",
			fmt.Sprintf("takes one directory to write the pages into, got %d arguments: nova-self-talk example ./pages", fset.NArg()))
	}
	dir := fset.Arg(0)
	entries, err := pages.ReadDir("testdata/example-pages")
	if err != nil {
		return refuse(stdout, stderr, asJSON, "example", "", "the pages built into this binary cannot be read: "+err.Error())
	}
	// Every problem is found before anything is written; the strings are concatenated, and
	// refuse escapes each one where it prints it.
	var write, kept, problems []string
	bodies := map[string][]byte{}
	for _, e := range entries {
		body, err := pages.ReadFile(path.Join("testdata/example-pages", e.Name()))
		if err != nil {
			return refuse(stdout, stderr, asJSON, "example", "", "the pages built into this binary cannot be read: "+err.Error())
		}
		target := filepath.Join(dir, e.Name())
		switch have, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(have, body):
			kept = append(kept, e.Name())
		case err == nil:
			problems = append(problems, oneline.Quote(target)+" exists with other content and is never replaced; name an empty directory")
		case os.IsNotExist(err):
			write, bodies[e.Name()] = append(write, e.Name()), body
		default:
			problems = append(problems, "cannot read "+oneline.Quote(target)+" to compare: "+reason(err))
		}
	}
	if len(problems) > 0 {
		return refuse(stdout, stderr, asJSON, "example", "", problems...)
	}
	if !*dry {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refuse(stdout, stderr, asJSON, "example", "", "cannot make "+oneline.Quote(dir)+": "+reason(err))
		}
		for _, name := range write {
			if err := os.WriteFile(filepath.Join(dir, name), bodies[name], 0o644); err != nil {
				return refuse(stdout, stderr, asJSON, "example", "", "cannot write "+oneline.Quote(filepath.Join(dir, name))+": "+reason(err))
			}
		}
	}
	wrote, next := "wrote", "nova-self-talk "+filepath.Join(dir, "journal.md")
	if *dry {
		wrote, next = "would-write", "nova-self-talk example "+dir
	}
	if asJSON {
		o := tool.Done()
		o.Verb, o.Remedy = "example", next
		o.Fact("dir", dir).Fact(wrote, strings.Join(write, ",")).Fact("kept", strings.Join(kept, ","))
		o.Render(stdout, true)
		return 0
	}
	fmt.Fprintf(stdout, "EXAMPLE OK dir=%s %s=%s kept=%s; run: %s\n", oneline.Field(dir), oneline.Field(wrote),
		oneline.Field(dash(strings.Join(write, ","))), oneline.Field(dash(strings.Join(kept, ","))), oneline.Escape(next))
	return 0
}

// dash is a field's empty value as the typed line spells it.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
