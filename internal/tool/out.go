package tool

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Status is how a verb ended: ok (exit 0), failed (it ran and said no, exit 1)
// or refused (it could not run, exit 2).
type Status string

const (
	OK      Status = "ok"
	Failed  Status = "failed"
	Refused Status = "refused"
)

// word is the status as the first line spells it.
var word = map[Status]string{OK: "OK", Failed: "FAIL", Refused: "REFUSED"}

// Out is the one value every verb returns. Render writes it as typed lines:
//
//	<TOKEN> OK|FAIL|REFUSED k=v ...[: <why>][; run: <remedy>]   one line per why
//	<TOKEN> <KIND> k=v ...                                      one line per item
//	<TOKEN> MORE kind=<kind> shown=<n> total=<n> <remedy>       one per capped kind
//	<TOKEN> NOTE <text>                                         one per note
//
// or as the JSON of the same value:
//
//	{"result":{"verb","status","exit","remedy","why"},"facts":{},"items":[{"kind","fields"}],
//	 "more":[{"kind","shown","total","remedy"}],"notes":[],"payload":""}
//
// A payload (the version line, a document a program reads) is printed as it is,
// last, in the text form, and alone when the result carries nothing else; it is
// the "payload" key of the JSON. Values are strings, integers, booleans or
// Text; an empty value is "-" in the text form. The JSON is not HTML-escaped:
// `<dir>` is `<dir>` in both renderings.
type Out struct {
	Verb    string
	Status  Status
	Exit    int
	Word    string   // the tool's own status word in place of OK or FAIL (Out.As); "" is the plain one
	Remedy  string   // what to run next: the tool's help on a refusal unless the verb names better
	Why     []string // every reason it failed or was refused
	Facts   Fields
	Items   []Item
	More    []More
	Notes   []string
	Payload string

	token    string   // the first word of every line: the verb, upper-case
	printed  bool     // the verb printed its own output (Flags.Prints)
	findings []string // item kinds whose lines go to stderr (Findings)
}

// Text is a value of free text, a reason, a sentence or a command to run, as
// a fact or an item's field: the text form prints it after the line's typed
// fields, its prose tail, quoted (oneline.Quote) so it keeps its spaces and
// is not hex-escaped, where a typed value (a name, a path, a count) is one
// token (oneline.Field): `REPORT UNKNOWN name=x path=p reason="no version
// line" run="nova-version report -h"`. JSON carries it as a string under its key.
type Text string

// Field is one key=value.
type Field struct {
	K string
	V any
}

// Fields keeps its keys in the order they were added, in both renderings.
type Fields []Field

// Item is one typed row: `<TOKEN> <KIND> k=v ...`.
type Item struct {
	Kind   string `json:"kind"`
	Fields Fields `json:"fields"`
}

// More stands for the items of one kind that --max did not list.
type More struct {
	Kind   string `json:"kind"`
	Shown  int    `json:"shown"`
	Total  int    `json:"total"`
	Remedy string `json:"remedy"`
}

// MaxRemedy is the remedy every MORE line names.
const MaxRemedy = "--max <n> raises the ceiling, --max 0 lists all"

// Done is an OK result, Refuse one that could not run, Fail one that ran and said no.
func Done() *Out                     { return &Out{Status: OK} }
func Refuse(why ...string) *Out      { return &Out{Status: Refused, Exit: 2, Why: why} }
func Fail(why ...string) *Out        { return &Out{Status: Failed, Exit: 1, Why: why} }
func Payload(text string) *Out       { return &Out{Status: OK, Payload: text} }
func (o *Out) Note(text string) *Out { o.Notes = append(o.Notes, text); return o }

// Exit is the result of a verb that printed its own output (Flags.Prints).
func Exit(code int) *Out {
	o := &Out{Status: OK, Exit: code, printed: true}
	switch {
	case code == 1:
		o.Status = Failed
	case code != 0:
		o.Status = Refused
	}
	return o
}

// Fact adds one key=value to the first line.
func (o *Out) Fact(k string, v any) *Out { o.Facts = append(o.Facts, Field{k, v}); return o }

// As puts one of the tool's own status words (Tool.Words) in place of OK or
// FAIL on the first line; the status and the exit stay: `Done().As("UNCHANGED")`
// exits 0, and a gate that says no is `Fail(why).As("STALE")`, exit 1, apart
// from a refusal's 2. A word the tool does not declare, or one on a refusal,
// is turned into a FAIL naming the bug.
func (o *Out) As(word string) *Out { o.Word = word; return o }

// Findings names the item kinds that are a verb's findings: in the text form
// their lines go to stderr, and the rest of a verb that ran (its first line,
// its other items, MORE and NOTE) to stdout, whether it said OK or FAIL. A
// refusal stays whole on stderr; JSON stays one object on stdout.
func (o *Out) Findings(kinds ...string) *Out { o.findings = append(o.findings, kinds...); return o }

// Item adds one row of a kind, its fields given as key, value, key, value.
func (o *Out) Item(kind string, kv ...any) *Out {
	it := Item{Kind: kind}
	for i := 0; i+1 < len(kv); i += 2 {
		it.Fields = append(it.Fields, Field{fmt.Sprint(kv[i]), kv[i+1]})
	}
	o.Items = append(o.Items, it)
	return o
}

// Cap keeps the first max items of each kind (0 keeps all) and records a
// More for each kind with items left over, counted by internal/bounded: the
// `MORE kind= shown= total=` cut. A verb with Flags.Max is capped by the
// skeleton; a verb that bounds a listing of its own calls Cap.
func (o *Out) Cap(max int) *Out {
	tl := bounded.NewTally(max)
	kept := o.Items[:0]
	for _, it := range o.Items {
		if tl.Add(it.Kind) {
			kept = append(kept, it)
		}
	}
	o.Items = kept
	for _, kind := range tl.Kinds() {
		if tl.Shown(kind) < tl.Total(kind) {
			o.More = append(o.More, More{kind, tl.Shown(kind), tl.Total(kind), MaxRemedy})
		}
	}
	return o
}

// Render writes o as typed lines, or as one JSON object when json is set, and
// returns the exit that stands: o.Exit, or 1 when o is no JSON (a NaN or an
// infinite float, a value of the verb's own that JSON cannot carry), which is
// then a FAIL line naming the verb, never an empty line and a success.
// Every value goes through internal/oneline.
func (o *Out) Render(w io.Writer, asJSON bool) int { return o.render(w, w, w, asJSON) }

// render is Render with the lines of the finding kinds (Findings) on found,
// and the FAIL line of a result that is no JSON on failed.
func (o *Out) render(w, found, failed io.Writer, asJSON bool) int {
	if asJSON {
		raw, err := marshal(o)
		if err != nil {
			for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
				err = e // the value's own error, not the MarshalJSON calls around it
			}
			f := Fail("the result is no JSON, so it is not printed: " + err.Error())
			f.Verb, f.token = o.Verb, o.token
			return f.render(failed, failed, failed, false)
		}
		fmt.Fprintf(w, "%s\n", raw)
		return o.Exit
	}
	token := o.token
	if token == "" {
		token = strings.ToUpper(o.Verb)
	}
	// A bare payload is the whole of the text form (the version line); a
	// payload with anything else beside it prints last, after the lines.
	bare := o.Payload != "" && o.Status == OK && o.Remedy == "" && len(o.Facts)+len(o.Items)+len(o.Notes) == 0
	if !bare {
		head := token + " " + cmp.Or(o.Word, word[o.Status]) + o.Facts.text()
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
	}
	for _, it := range o.Items {
		to := w
		if slices.Contains(o.findings, it.Kind) {
			to = found
		}
		fmt.Fprintln(to, oneline.Field(token)+" "+oneline.Field(strings.ToUpper(it.Kind))+it.Fields.text())
	}
	for _, m := range o.More {
		fmt.Fprintln(w, bounded.MoreLine(token, m.Kind, m.Shown, m.Total, m.Remedy))
	}
	for _, n := range o.Notes {
		fmt.Fprintln(w, oneline.Field(token)+" NOTE "+oneline.Escape(n))
	}
	if o.Payload != "" {
		fmt.Fprintln(w, strings.TrimSuffix(o.Payload, "\n"))
	}
	return o.Exit
}

// text is the fields of one line: the typed ones in order, each one token
// (oneline.Field), then the Text ones, the line's prose tail, each quoted
// (oneline.Quote) so it keeps its spaces and a reader sees where it ends.
func (fs Fields) text() string {
	var typed, prose strings.Builder
	for _, f := range fs {
		v := fmt.Sprint(f.V)
		b := &typed
		switch _, text := f.V.(Text); {
		case v == "":
			v = "-"
		case text:
			v, b = oneline.Quote(v), &prose
		default:
			v = oneline.Field(v)
		}
		b.WriteString(" " + oneline.Field(f.K) + "=" + v)
	}
	return typed.String() + prose.String()
}

// marshal is json.Marshal without the HTML escape (`<` stays `<`): the
// output is read by a program or an AI, never pasted into a page.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// MarshalJSON writes the fields as one object in the order they were added.
func (fs Fields) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fs {
		k, err := marshal(f.K)
		if err != nil {
			return nil, err
		}
		v, err := marshal(f.V)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// MarshalJSON is the JSON rendering: the result, then the value's parts.
func (o *Out) MarshalJSON() ([]byte, error) {
	type result struct {
		Verb   string   `json:"verb"`
		Status Status   `json:"status"`
		Exit   int      `json:"exit"`
		Word   string   `json:"word,omitempty"`
		Remedy string   `json:"remedy,omitempty"`
		Why    []string `json:"why,omitempty"`
	}
	return marshal(struct {
		Result  result   `json:"result"`
		Facts   Fields   `json:"facts"`
		Items   []Item   `json:"items,omitempty"`
		More    []More   `json:"more,omitempty"`
		Notes   []string `json:"notes,omitempty"`
		Payload string   `json:"payload,omitempty"`
	}{result{o.Verb, o.Status, o.Exit, o.Word, o.Remedy, o.Why}, o.Facts, o.Items, o.More, o.Notes, o.Payload})
}
