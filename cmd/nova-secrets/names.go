// names.go holds the names verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func runNamesCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("names", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	maxFlag := fs.Int("max", 20, maxUse)
	jsonFlag := fs.Bool("json", false, "print the result as one JSON object on stdout, a refusal included: result, facts, items, more")
	// A refusal asked for as --json is JSON, the flag refusals included: --json is read
	// off the words as the parser reads them, so it holds when the parse stops early.
	if err := parseFlags(fs, args); err != nil {
		if verbflag.BoolGiven(fs, args, "json") {
			return namesJSON(s.stdout, secrets.NamesReport{}, err)
		}
		return s.refuse("names", 2, err)
	}

	r, err := secrets.RunNames(*storeFlag, *asFlag, *maxFlag)
	if *jsonFlag {
		return namesJSON(s.stdout, r, err)
	}
	if err != nil {
		return s.refuse("names", 2, err)
	}
	okLine, names, more := r.Lines()

	for _, n := range names {
		fmt.Fprintln(s.stdout, n)
	}
	if more != "" {
		fmt.Fprintln(s.stdout, more)
	}
	fmt.Fprintln(s.stdout, okLine)
	return 0
}

// namesJSON renders names' one value as JSON (STANDARD §2: one value, two renderings)
// and returns the exit code: the key names and counts, never a value.
func namesJSON(w io.Writer, r secrets.NamesReport, err error) int {
	o := tool.Done()
	if err != nil {
		o = tool.Refuse(err.Error())
		if !oneline.HasRemedy(err.Error()) {
			o.Remedy = "nova-secrets names -h"
		}
	} else {
		o.Fact("as", r.As).Fact("keys", r.Total).Fact("shown", len(r.Rows)).Fact("sealed", r.Sealed).Fact("clear", r.Clear)
		for _, n := range r.Rows {
			o.Item("key", "key", n.Name, "clear", n.Clear)
		}
		if len(r.Rows) < r.Total {
			o.More = append(o.More, tool.More{Kind: "key", Shown: len(r.Rows), Total: r.Total, Remedy: tool.MaxRemedy})
		}
	}
	o.Verb = "names"
	o.Render(w, true)
	return o.Exit
}
