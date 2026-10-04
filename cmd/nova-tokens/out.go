package main

// One verb's result in its two renderings (docs/STANDARD.md section 2: one value, lines or
// --json). A verb writes each typed line through a sink: as text the line goes to the stream
// it belongs to, exactly as this tool has always printed it; under --json the same values
// are an item, a fact or a note of one tool.Out, printed on stdout as one object when the
// verb ends. The lines are this tool's own (TOKENS DAY, SUM PAIR, CHECK MISSING), which is
// why the text is not the skeleton's renderer: the skeleton names every line by its verb.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// sink is one verb's output while it runs.
type sink struct {
	json           bool
	o              *tool.Out
	stdout, stderr io.Writer
}

func newSink(verb string, asJSON bool, stdout, stderr io.Writer) *sink {
	o := tool.Done()
	o.Verb = verb
	return &sink{json: asJSON, o: o, stdout: stdout, stderr: stderr}
}

// out and err are where a text line goes: its stream, or nowhere under --json, where the
// line's values are an item instead.
func (s *sink) out() io.Writer {
	if s.json {
		return io.Discard
	}
	return s.stdout
}

func (s *sink) err() io.Writer {
	if s.json {
		return io.Discard
	}
	return s.stderr
}

// item, fact and note record a line's values under --json; as text they do nothing.
func (s *sink) item(kind string, kv ...any) {
	if s.json {
		s.o.Item(kind, kv...)
	}
}

func (s *sink) fact(k string, v any) {
	if s.json {
		s.o.Fact(k, v)
	}
}

func (s *sink) note(text string) {
	if s.json {
		s.o.Note(text)
	}
}

// list is a capped listing of one kind: bounded's lines and its MORE line as text, on
// stderr when toErr; under --json the items are capped the same way when the verb ends.
func (s *sink) list(toErr bool, max int, token, kind, remedy string) *bounded.List {
	w := s.out()
	if toErr {
		w = s.err()
	}
	return bounded.Capped(w, max, token, kind, remedy)
}

// done ends the verb with its exit code. As text the lines are already out; under --json
// the one object is printed now, its listings capped at max (0 keeps all), and its exit is
// the code given.
func (s *sink) done(code, max int) int {
	if !s.json {
		return code
	}
	s.o.Exit = code
	switch code {
	case 0:
		s.o.Status = tool.OK
	case 1:
		s.o.Status = tool.Failed
	default:
		s.o.Status = tool.Refused
	}
	s.o.Cap(max)
	return s.o.Render(s.stdout, true)
}

// refusals collects every independent problem so one run reports them all: sending a
// first run back three times for three flags is three refusals the first one already
// knew about. With a sink under --json the refusal is one JSON object on stdout.
type refusals struct {
	token string
	list  []string
	s     *sink
}

func (r *refusals) add(problem string) { r.list = append(r.list, problem) }

func (r *refusals) required(name, value, wants string) {
	if strings.TrimSpace(value) == "" {
		r.add("--" + name + " is required; it wants " + wants + "; refusing to guess")
	}
}

// print writes one line per problem, in the order they were found, and returns exit 2.
func (r *refusals) print(stderr io.Writer) int {
	if r.s != nil && r.s.json {
		o := tool.Refuse(r.list...)
		o.Verb, o.Remedy = r.s.o.Verb, "nova-tokens help"
		return o.Render(r.s.stdout, true)
	}
	for _, problem := range r.list {
		writeRefusal(stderr, r.token, problem, "nova-tokens help")
	}
	return 2
}

// writeRefusal is the one refusal line: `<TOKEN> REFUSED: <why>; run: <remedy>`.
func writeRefusal(w io.Writer, token, why, remedy string) {
	fmt.Fprintf(w, "%s REFUSED: %s; run: %s\n", oneline.Field(token), oneline.Escape(why), oneline.Escape(remedy))
}

// refuse is an invocation the dispatcher cannot run: no verb, an unknown verb. One line,
// the verbs there are, and the door.
func refuse(stdout, stderr io.Writer, asJSON bool, why string) int {
	if asJSON {
		o := tool.Refuse(why)
		o.Verb, o.Remedy = "tokens", "nova-tokens help"
		return o.Render(stdout, true)
	}
	writeRefusal(stderr, "TOKENS", why, "nova-tokens help")
	return 2
}

// start declares --json, parses one verb's flags, and returns the verb's sink. A flag the
// verb does not take, one missing its value or one with a value it cannot take is refused
// in the verb's own grammar, naming the verb's flags and the nearest one, with the verb's
// help as the remedy (verbflag.Explain, the wording internal/tool gives every tool); so is
// a positional argument, since every verb is flags only. ok is false when it refused, and
// code is then the exit.
func start(fs *flag.FlagSet, args []string, token string, stdout, stderr io.Writer) (s *sink, code int, ok bool) {
	asJSON := fs.Bool("json", false, "print the result as one JSON object on stdout instead of lines")
	s = newSink(fs.Name(), verbflag.BoolGiven(fs, args, "json"), stdout, stderr)
	if err := verbflag.Parse(fs, args); err != nil {
		return s, refuseVerb(s, token, oneline.Cap(verbflag.Explain(fs, err), oneline.TailBytes)), false
	}
	s.json = *asJSON
	if n := fs.NArg(); n > 0 {
		why := fmt.Sprintf("takes no positional arguments, got %d (%s); flags come before arguments",
			n, oneline.Field(strings.Join(fs.Args(), " ")))
		return s, refuseVerb(s, token, why), false
	}
	return s, 0, true
}

// refuseVerb is one refusal of a verb's invocation, its remedy the verb's own help.
func refuseVerb(s *sink, token, why string) int {
	remedy := "nova-tokens " + s.o.Verb + " -h"
	if s.json {
		o := tool.Refuse(why)
		o.Verb, o.Remedy = s.o.Verb, remedy
		return o.Render(s.stdout, true)
	}
	writeRefusal(s.stderr, token, why, remedy)
	return 2
}

// line is one typed line, `<TOKEN> <KIND> k=v ...[: <tail>]`: every value one token
// (oneline.Field), the tail free text (oneline.Escape). It returns the text for the stream
// and, under --json, records the same values as an item of that kind, the tail as its why.
func (s *sink) line(token, kind, tail string, kv ...any) string {
	text := oneline.Field(token) + " " + oneline.Field(kind) + fields(kv...)
	if tail != "" {
		text += ": " + oneline.Escape(tail)
		kv = append(kv, "why", tool.Text(tail))
	}
	s.item(strings.ToLower(kind), kv...)
	return text
}

// fields is key=value pairs as a line carries them, each one a separate field: the key (a
// string) and the value one token each (oneline.Field).
func fields(kv ...any) string {
	text := ""
	for i := 0; i+1 < len(kv); i += 2 {
		text += " " + oneline.Field(kv[i].(string)) + "=" + oneline.Field(fmt.Sprint(kv[i+1]))
	}
	return text
}

// factFields is fields that are also the result's facts under --json: a summary line.
func (s *sink) factFields(kv ...any) string {
	for i := 0; i+1 < len(kv); i += 2 {
		s.fact(kv[i].(string), kv[i+1])
	}
	return fields(kv...)
}

// dryRunFields is " dry_run=true" and the pairs after it on a write verb's last line under
// --dry-run, and "" otherwise; under --json they are facts.
func (s *sink) dryRunFields(on bool, kv ...any) string {
	if !on {
		return ""
	}
	return s.factFields(append([]any{"dry_run", true}, kv...)...)
}
