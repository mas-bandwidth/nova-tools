package main

// report is one verb's answer as one value (STANDARD §2, one output
// structure, two renderings): each line the verb says is printed as the typed
// line a reader scans, or, with --json, gathered into internal/tool's Out and
// printed once as its JSON when the verb ends: {result {verb, status, exit,
// remedy, why}, items (one per line, its kind the line's leading words),
// notes}. The two come from the same call, so they cannot drift. A field's
// value is printed with oneline.Field unless its type says otherwise.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// jsonUsage is --json's description, the same on every verb that takes it.
const jsonUsage = "print the result as one JSON object on stdout (result, items, notes) instead of lines"

type (
	quoted string // a value printed as a quoted Go string: a remedy, a reason
	free   string // a value printed as it is: text already escaped (oneline.Err), or a list of words
	why    string // the reason a line closes with, ": <why>"
	next   string // the command a line closes with, "; run: <next>"
)

// bare is words printed with no key (a user's rules, a receipt's fields as
// their own String prints them), and v is the same thing as JSON.
type bare struct {
	text string
	v    any
}

type report struct {
	json           bool
	stdout, stderr io.Writer
	out            tool.Out
}

// newReport is the report of one verb, and gives its flag set --json.
// --json anywhere before -- asks for JSON, so a refusal of the line itself is
// JSON too.
func newReport(verb string, fs *flag.FlagSet, args []string, stdout, stderr io.Writer) *report {
	fs.Bool("json", false, jsonUsage)
	return &report{json: verbflag.BoolAsked(args, "json"), stdout: stdout, stderr: stderr, out: tool.Out{Verb: verb}}
}

// refuse says every problem with the invocation, in the one grammar
// `nova-redis[ <verb>] REFUSED: <what was wrong>; run: nova-redis help[ <verb>]`
// (STANDARD §3.1), one line each on stderr, and exits 2. Called alone it is a
// refusal in lines only: before a verb is known (no verb, an unknown one, a
// group with no subverb), and serve's, which takes no --json.
func refuse(stderr io.Writer, verb string, problems ...string) int {
	where, help := "", "nova-redis help"
	if verb != "" {
		where, help = " "+verb, help+" "+verb
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "nova-redis%s REFUSED: %s; run: %s\n", where, oneline.Escape(p), help)
	}
	return 2
}

// line says one typed line: its leading words, then key, value pairs. toErr
// puts the text line on stderr (a failure); JSON is always on stdout.
func (r *report) line(toErr bool, head string, kv ...any) {
	if r.json {
		it := tool.Item{Kind: head}
		for i := 0; i+1 < len(kv); i += 2 {
			k, v := kv[i].(string), kv[i+1]
			switch x := v.(type) {
			case quoted:
				v = string(x)
			case free:
				v = string(x)
			case bare:
				v = x.v
			case why:
				r.out.Why = append(r.out.Why, string(x))
				continue
			case next:
				r.out.Remedy = string(x)
				continue
			}
			if k == "remedy" {
				r.out.Remedy = fmt.Sprint(v)
			}
			it.Fields = append(it.Fields, tool.Field{K: k, V: v})
		}
		r.out.Items = append(r.out.Items, it)
		return
	}
	var b strings.Builder
	b.WriteString(head)
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		switch x := kv[i+1].(type) {
		case quoted:
			fmt.Fprintf(&b, " %s=%q", k, string(x))
		case free:
			fmt.Fprintf(&b, " %s=%s", k, string(x))
		case bare:
			if x.text != "" {
				b.WriteString(" " + x.text)
			}
		case why:
			b.WriteString(": " + string(x))
		case next:
			b.WriteString("; run: " + string(x))
		case string:
			fmt.Fprintf(&b, " %s=%s", k, oneline.Field(x))
		default:
			fmt.Fprintf(&b, " %s=%v", k, x)
		}
	}
	w := r.stdout
	if toErr {
		w = r.stderr
	}
	fmt.Fprintln(w, b.String())
}

// note says one NOTE line: a fact beside the result.
func (r *report) note(text string) {
	if r.json {
		r.out.Notes = append(r.out.Notes, text)
		return
	}
	fmt.Fprintln(r.stdout, "NOTE "+text)
}

// refuse is the verb's refusal: refuse's lines, or with --json the same
// problems as the result's why and its help as the remedy.
func (r *report) refuse(problems ...string) int {
	if !r.json {
		return refuse(r.stderr, r.out.Verb, problems...)
	}
	r.out.Why = append(r.out.Why, problems...)
	r.out.Remedy = "nova-redis help " + r.out.Verb
	return r.done(2)
}

// done ends the verb with code: with --json it prints the one JSON object, its
// status read from the code (0 ok, 1 failed: ran and said no, 2 refused: could
// not run).
func (r *report) done(code int) int {
	if !r.json {
		return code
	}
	r.out.Exit, r.out.Status = code, tool.OK
	switch code {
	case 0:
	case 1:
		r.out.Status = tool.Failed
	default:
		r.out.Status = tool.Refused
	}
	r.out.Render(r.stdout, true)
	return code
}
