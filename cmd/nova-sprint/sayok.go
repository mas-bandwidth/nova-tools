package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
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

// findingsCount is "1 finding" or "<n> findings".
func findingsCount(n int) string {
	if n == 1 {
		return "1 finding"
	}
	return fmt.Sprintf("%d findings", n)
}
