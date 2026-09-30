package tool

import (
	"encoding/json"
	"fmt"
	"io"
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
// A payload (the version line, a document a program reads) is printed as it is
// in the text form, and is the "payload" key of the JSON. Values are strings,
// integers or booleans; an empty value is "-" in the text form.
type Out struct {
	Verb    string
	Status  Status
	Exit    int
	Remedy  string   // what to run next: the tool's help on a refusal unless the verb names better
	Why     []string // every reason it failed or was refused
	Facts   Fields
	Items   []Item
	More    []More
	Notes   []string
	Payload string

	token   string // the first word of every line: the verb, upper-case
	printed bool   // the verb printed its own output (Flags.Prints)
}

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

// Item adds one row of a kind, its fields given as key, value, key, value.
func (o *Out) Item(kind string, kv ...any) *Out {
	it := Item{Kind: kind}
	for i := 0; i+1 < len(kv); i += 2 {
		it.Fields = append(it.Fields, Field{fmt.Sprint(kv[i]), kv[i+1]})
	}
	o.Items = append(o.Items, it)
	return o
}

// capItems keeps the first max items of each kind (0 keeps all) and records a
// More for each kind with items left over, counted by internal/bounded.
func (o *Out) capItems(max int) {
	g := bounded.Grouped(io.Discard, max, "", MaxRemedy)
	kept := o.Items[:0]
	for _, it := range o.Items {
		shown := g.Shown()
		g.Line(it.Kind, "")
		if g.Shown() > shown {
			kept = append(kept, it)
		}
	}
	o.Items = kept
	for _, kind := range g.Kinds() {
		if l := g.List(kind); l.Elided() > 0 {
			o.More = append(o.More, More{kind, l.Shown(), l.Total(), MaxRemedy})
		}
	}
}

// Render writes o as typed lines, or as one JSON object when json is set.
// Every value goes through internal/oneline.
func (o *Out) Render(w io.Writer, asJSON bool) {
	if asJSON {
		raw, _ := json.Marshal(o)
		fmt.Fprintf(w, "%s\n", raw)
		return
	}
	token := o.token
	if token == "" {
		token = strings.ToUpper(o.Verb)
	}
	if o.Payload != "" && o.Status == OK {
		fmt.Fprintln(w, strings.TrimSuffix(o.Payload, "\n"))
	} else {
		head := token + " " + word[o.Status] + o.Facts.text()
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
		fmt.Fprintln(w, oneline.Field(token)+" "+oneline.Field(strings.ToUpper(it.Kind))+it.Fields.text())
	}
	for _, m := range o.More {
		fmt.Fprintln(w, bounded.MoreLine(token, m.Kind, m.Shown, m.Total, m.Remedy))
	}
	for _, n := range o.Notes {
		fmt.Fprintln(w, oneline.Field(token)+" NOTE "+oneline.Escape(n))
	}
}

func (fs Fields) text() string {
	var b strings.Builder
	for _, f := range fs {
		v := fmt.Sprint(f.V)
		if v == "" {
			v = "-"
		}
		b.WriteString(" " + oneline.Field(f.K) + "=" + oneline.Field(v))
	}
	return b.String()
}

// MarshalJSON writes the fields as one object in the order they were added.
func (fs Fields) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fs {
		k, _ := json.Marshal(f.K)
		v, err := json.Marshal(f.V)
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
		Remedy string   `json:"remedy,omitempty"`
		Why    []string `json:"why,omitempty"`
	}
	return json.Marshal(struct {
		Result  result   `json:"result"`
		Facts   Fields   `json:"facts"`
		Items   []Item   `json:"items,omitempty"`
		More    []More   `json:"more,omitempty"`
		Notes   []string `json:"notes,omitempty"`
		Payload string   `json:"payload,omitempty"`
	}{result{o.Verb, o.Status, o.Exit, o.Remedy, o.Why}, o.Facts, o.Items, o.More, o.Notes, o.Payload})
}
