package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// verbOut is one inspection verb's result (check, policy, probe), built once and rendered
// two ways (docs/STANDARD.md §2, "one output structure, two renderings"): the typed lines
// this tool has always printed, or with --json one JSON object on stdout from
// pkg/tool's Out, a refusal included. Every line of those verbs goes through here,
// so the two renderings cannot say different things.
type verbOut struct {
	token          string // the first word of each line: CHECK, POLICY, PROBE
	json           bool
	out            *tool.Out
	stdout, stderr io.Writer
}

func newVerbOut(verb string, asJSON bool, stdout, stderr io.Writer) *verbOut {
	o := tool.Done()
	o.Verb = verb
	return &verbOut{token: oneline.Field(verbToken[verb]), json: asJSON, out: o, stdout: stdout, stderr: stderr}
}

var verbToken = map[string]string{"check": "CHECK", "policy": "POLICY", "probe": "PROBE"}

// refuse is one `<TOKEN> REFUSED reason=<r>: <text>` line on stderr, text already rendered;
// a text that names no next step of its own ends with the verb's help. Under --json it is
// one reason of the result, whose status is st: tool.Refused (could not run, exit 2) or
// tool.Failed (ran and said NO, exit 1).
func (v *verbOut) refuse(st tool.Status, reason, text string) {
	if !v.json {
		fmt.Fprintf(v.stderr, "%s REFUSED reason=%s: %s\n", v.token, oneline.Field(reason), oneline.WithRemedy(text, "nova-sandbox "+v.out.Verb+" -h"))
		return
	}
	if v.out.Status != tool.Refused {
		v.out.Status, v.out.Exit = st, map[tool.Status]int{tool.Refused: 2, tool.Failed: 1}[st]
	}
	v.out.Why = append(v.out.Why, reason+": "+text)
}

// note is one `<TOKEN> NOTE <text>` line on stderr, or one of the result's notes.
func (v *verbOut) note(text string) {
	if !v.json {
		fmt.Fprintf(v.stderr, "%s NOTE %s\n", v.token, oneline.Escape(text))
		return
	}
	v.out.Note(text)
}

// item is one typed row on stdout, line as the text form prints it; kind and kv are the
// same row for JSON.
func (v *verbOut) item(line, kind string, kv ...any) {
	if !v.json {
		fmt.Fprintln(v.stdout, line)
		return
	}
	v.out.Item(kind, kv...)
}

// done ends the verb and returns code. The text form prints line (if any) on w, which is
// where the verb has always printed its closing line; --json adds kv as the facts and
// prints the whole result on stdout.
func (v *verbOut) done(w io.Writer, line string, code int, kv ...any) int {
	if !v.json {
		if line != "" {
			fmt.Fprintln(w, line)
		}
		return code
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v.out.Fact(fmt.Sprint(kv[i]), kv[i+1])
	}
	if v.out.Status != tool.OK {
		v.out.Remedy = "nova-sandbox " + v.out.Verb + " -h"
	}
	v.out.Render(v.stdout, true)
	return code
}
