package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// sayOK prints a verb's success as its typed line, or, with --json, as one JSON
// object of the same facts: the verbs that wrote their line by hand took --json
// and printed the line anyway (tool ledger P8; docs/STANDARD.md section 2, one
// value, two renderings).
func sayOK(w io.Writer, asJSON bool, verbName, line string, facts map[string]any) {
	if !asJSON {
		fmt.Fprintln(w, line)
		return
	}
	o := map[string]any{"verb": verbName, "status": "ok", "exit": 0}
	maps.Copy(o, facts)
	// ignored: a map of strings, numbers and lists of strings always encodes
	b, _ := json.Marshal(o)
	fmt.Fprintln(w, string(b))
}

// unfilledSays is what add and brief say of a brief cut from the card template with
// lines of it left unfilled (swarm.UnfilledTemplateLines): the card is admitted, since
// the lint holds the rules and not the fill-ins, and the reader is told before a worker
// is handed REPO: <owner>/<name> as its repository.
func unfilledSays(what, brief string) []string {
	fs := swarm.UnfilledTemplateLines(brief)
	if len(fs) == 0 {
		return nil
	}
	var lines []string
	for i, f := range fs {
		if i == 3 {
			lines = append(lines, fmt.Sprintf("and %d more", len(fs)-3))
			break
		}
		l := strings.TrimSpace(f.Excerpt)
		if len(l) > 60 {
			l = l[:60] + "..."
		}
		lines = append(lines, fmt.Sprintf("line %d: %s", f.Line, l))
	}
	return []string{fmt.Sprintf("%s holds %d of the card template's lines unfilled (%s); a worker is handed them as they are: fill each <...> in, then run nova-sprint brief <id> --brief-file <path> before it is dealt (nova-swarm lint --card <file> names them all)",
		what, len(fs), strings.Join(lines, "; "))}
}

// findingsCount is "1 finding" or "<n> findings".
func findingsCount(n int) string {
	if n == 1 {
		return "1 finding"
	}
	return fmt.Sprintf("%d findings", n)
}
