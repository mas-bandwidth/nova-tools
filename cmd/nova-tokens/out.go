package main

// One verb's result in its two renderings (docs/STANDARD.md section 2: one value, lines or
// --json). A verb writes each typed line through a sink: as text the line goes to the stream
// it belongs to; under --json the same values are an item, a fact or a note of one
// tool.Out, printed on stdout as one object when the verb ends. The lines are this tool's
// own (TOKENS DAY, SUM PAIR, CHECK MISSING), which is why the text is not the skeleton's
// renderer: the skeleton names every line by its verb.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// sink is one verb's output while it runs.
type sink struct {
	json           bool
	o              *tool.Out
	stdout, stderr io.Writer
}

func newSink(c *tool.Call, verb string) *sink {
	o := tool.Done()
	o.Verb = verb
	return &sink{json: c.Bool("json"), o: o, stdout: c.Stdout, stderr: c.Stderr}
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
func (s *sink) done(code, max int) *tool.Out {
	if s.json {
		s.o.Exit, s.o.Status = code, map[int]tool.Status{0: tool.OK, 1: tool.Failed}[code]
		if code > 1 {
			s.o.Status = tool.Refused
		}
		code = s.o.Cap(max).Render(s.stdout, true)
	}
	return tool.Exit(code)
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
