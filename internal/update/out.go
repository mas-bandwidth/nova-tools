package update

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The verbs of this package build one value each, internal/tool's Out, and it is
// rendered either as typed lines or as the JSON of the same value (STANDARD §2,
// "one output structure, two renderings"). nova-version's snapshot, diff and moved
// go through internal/tool, which renders them; the verbs here that nova-update
// runs, and report, which both tools share, are rendered by emit below, because
// internal/tool's line rendering has no place for prose inside an item: a reason
// or a command there would print as one escaped key=value field (\x20 for every
// blank), and a reason is printed plain. Once an item can carry a prose tail in
// internal/tool, emit, capped and text go and these verbs move onto it.

// prose are the item fields printed plain after the typed fields, in this order:
// `: <reason|command|detail>` and then ` (<remedy>)`. Every other field is typed.
var prose = []string{"reason", "command", "detail"}

// statusWord is the word a status line opens with, as internal/tool spells it.
var statusWord = map[tool.Status]string{tool.OK: "OK", tool.Failed: "FAIL", tool.Refused: "REFUSED"}

// emit renders o, capped at max items of each kind (0 keeps all): as one JSON
// object on stdout with --json, else as typed lines, on stdout when it is OK and
// on stderr when it is not. A value that is only a payload (a --draft note) is the
// document asked for and goes to stdout whatever its exit. It returns the exit
// code o carries.
func emit(o *tool.Out, asJSON bool, max int, out, errs io.Writer) int {
	o = capped(o, max)
	w := out
	if !asJSON && o.Status != tool.OK && !payloadOnly(o) {
		w = errs
	}
	var b bytes.Buffer
	if asJSON {
		o.Render(&b, true)
	} else {
		text(&b, o)
	}
	if _, err := w.Write(b.Bytes()); err != nil && o.Exit == 0 {
		return 1 // the result did not reach its reader (a closed pipe): the exit says so
	}
	return o.Exit
}

// refused is a refusal of verb with one reason and the command to run next.
func refused(verb, run, why string) *tool.Out {
	o := tool.Refuse(why)
	o.Verb, o.Remedy = verb, run
	return o
}

// capped is o with at most max items of each kind and a More for each kind cut.
func capped(o *tool.Out, max int) *tool.Out {
	c := *o
	c.Items = nil
	tally := bounded.NewTally(max)
	for _, it := range o.Items {
		if tally.Add(it.Kind) {
			c.Items = append(c.Items, it)
		}
	}
	for _, kind := range tally.Kinds() {
		if tally.Shown(kind) < tally.Total(kind) {
			c.More = append(c.More, tool.More{Kind: kind, Shown: tally.Shown(kind), Total: tally.Total(kind), Remedy: tool.MaxRemedy})
		}
	}
	return &c
}

// text writes o as typed lines in internal/tool's layout: the status line with the
// facts (one per reason when there are several), one line per item, a MORE line
// per kind cut, a NOTE line per note, and the payload last. A value holding only a
// payload (a --draft note) prints the payload alone.
func text(w io.Writer, o *tool.Out) {
	if payloadOnly(o) {
		fmt.Fprintln(w, strings.TrimSuffix(o.Payload, "\n"))
		return
	}
	token := strings.ToUpper(o.Verb)
	head := token + " " + statusWord[o.Status] + typed(o.Facts)
	tail := ""
	if o.Remedy != "" {
		tail = "; run: " + oneline.Escape(o.Remedy)
	}
	if len(o.Why) == 0 {
		fmt.Fprintln(w, head+tail)
	}
	for _, why := range o.Why {
		fmt.Fprintln(w, head+": "+oneline.Escape(why)+tail)
	}
	for _, it := range o.Items {
		line := token + " " + strings.ToUpper(it.Kind) + typed(it.Fields)
		said := map[string]string{}
		for _, f := range it.Fields {
			said[f.K] = fmt.Sprint(f.V)
		}
		for _, k := range prose {
			if v, ok := said[k]; ok {
				line += ": " + oneline.Escape(v)
			}
		}
		if v, ok := said["remedy"]; ok {
			line += " (" + oneline.Escape(v) + ")"
		}
		fmt.Fprintln(w, line)
	}
	for _, m := range o.More {
		fmt.Fprintln(w, bounded.MoreLine(token, m.Kind, m.Shown, m.Total, m.Remedy))
	}
	for _, n := range o.Notes {
		fmt.Fprintln(w, token+" NOTE "+oneline.Escape(n))
	}
	if o.Payload != "" {
		fmt.Fprintln(w, strings.TrimSuffix(o.Payload, "\n"))
	}
}

// payloadOnly reports whether o carries a payload and nothing else.
func payloadOnly(o *tool.Out) bool {
	return o.Payload != "" && len(o.Facts)+len(o.Items)+len(o.Notes)+len(o.Why) == 0
}

// typed is the key=value fields of a line, each value one oneline token and an
// empty one "-"; the prose fields and remedy are left for the line's tail.
func typed(fs tool.Fields) string {
	var b strings.Builder
	for _, f := range fs {
		if f.K == "remedy" || slices.Contains(prose, f.K) {
			continue
		}
		b.WriteString(" " + f.K + "=" + field(fmt.Sprint(f.V)))
	}
	return b.String()
}
