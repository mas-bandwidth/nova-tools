// shapes.go holds the shapes verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
