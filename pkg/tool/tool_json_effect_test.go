package tool

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// printsJSONTool is a tool with a Prints verb that also takes --json (it
// registers the flag itself), the shape the card's first two findings name:
// nova-doctor run, nova-check spelling, nova-check convergence.
func printsJSONTool() *Tool {
	return &Tool{
		Name:      "nova-prints",
		What:      "a tool whose prints verb takes --json",
		How:       "It prints its own rendering.",
		ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{
			{
				Name:   "spell",
				Usage:  "spell --word <w>",
				Effect: Inspection,
				Flags: func(f *Flags) {
					f.Prints()
					f.Bool("json", false, "print the result as one JSON object instead of lines")
					f.Required("word", "the word")
				},
				Run: func(c *Call) *Out {
					fmt.Fprintln(c.Stdout, "SPELL "+c.Str("word"))
					return Exit(0)
				},
			},
		},
	}
}

// TestPrintsVerbsHelpAgreesAndPreRunRefusalsHonourJSONAndEffectHasOneColon
// pins the skeleton's separation of "a verb renders its own stdout" from "a
// verb takes --json" (the banner lists only verbs that truly lack --json, and
// a pre-run refusal under --json is the JSON object on stdout), and the join
// that drops a default effect's tail when a caller appends a clause.
func TestPrintsVerbsHelpAgreesAndPreRunRefusalsHonourJSONAndEffectHasOneColon(t *testing.T) {
	t.Parallel()

	t.Run("a prints verb that takes --json is not named as lacking it", func(t *testing.T) {
		t.Parallel()
		banner := printsJSONTool().Banner()
		assert.Contains(t, banner, "Every verb takes --json")
		assert.NotContains(t, banner, "Every verb but")
		assert.NotContains(t, banner, "take it too")
	})

	t.Run("a missing required flag under --json is the JSON refusal on stdout", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, printsJSONTool()).Run(2, "spell", "--json")
		assert.Empty(t, r.Stderr, "a refusal under --json leaves nothing on stderr")
		assert.Contains(t, r.Stdout, `"status":"refused"`)
		assert.Contains(t, r.Stdout, `"why":["--word is required`)
	})

	t.Run("a clause after a default effect drops the default's tail", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{
			Name: "nova-effect", What: "writes", How: "It writes.",
			ExitTable: "0 done, 1 said no, 2 could not run.",
			Verbs: []Verb{{
				Name: "write", Usage: "write",
				Effect: LocalWrite + ": the cursor file ... is rewritten",
				Run:    func(*Call) *Out { return Done() },
			}},
		}
		r := NewRig(t, tool).Capture("write", "-h")
		assert.Contains(t, r.Stdout, "effect: local write: the cursor file ... is rewritten\n")
		assert.NotContains(t, r.Stdout, "writes files on this machine: the cursor")
	})
}
