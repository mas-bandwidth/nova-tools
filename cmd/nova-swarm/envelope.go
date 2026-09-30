package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// printJSONEnvelope formats and prints one line of JSON matching the standard envelope:
// {result: {verb: "...", status: "...", exit: ...}, facts: {...}, items: [...], notes: [...]}
func printJSONEnvelope(w io.Writer, verb, status string, exit int, facts map[string]any, items []any, notes []string) int {
	env := map[string]any{
		"result": map[string]any{
			"verb":   verb,
			"status": status,
			"exit":   exit,
		},
	}
	if facts != nil {
		env["facts"] = facts
	}
	if items != nil {
		env["items"] = items
	}
	if notes != nil {
		env["notes"] = notes
	}
	b, _ := json.Marshal(env)
	fmt.Fprintln(w, string(b))
	return exit
}
