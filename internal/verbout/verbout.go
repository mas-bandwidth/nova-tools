// Package verbout is the shared output structure and encoder for tools in this repository.
//
// SPEC: docs/SPEC.md "Output grammar" and docs/CLI-STYLE.md.
//
// Every verb builds one value:
//   - result {verb, status: ok|refused|failed, exit, remedy}
//   - facts  {k: v, ...} (ordered key-value pairs)
//   - items  [{kind, fields, text} ...]
//   - more   [{kind, shown, total, remedy} ...]
//   - notes  ["line", ...]
//
// Two renderings are produced from the same value:
//   - Text: <TOKEN> OK|FAIL|REFUSED k=v ...; one item per line, MORE, NOTE.
//   - JSON: {"result": {...}, "facts": {...}, "items": [...], "more": [...], "notes": [...]}
package verbout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Status constants.
const (
	StatusOK      = "ok"
	StatusRefused = "refused"
	StatusFailed  = "failed"
)

// FactPair is an ordered key-value fact.
type FactPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Facts holds ordered key-value pairs. In JSON, it marshals as an object
// {"k1": "v1", "k2": "v2"} where keys appear in insertion order.
type Facts struct {
	pairs []FactPair
}

// NewFacts creates an empty Facts container.
func NewFacts() *Facts {
	return &Facts{}
}

// Set sets or appends key=val. If key exists, its value is updated.
func (f *Facts) Set(key, val string) *Facts {
	if f == nil {
		return f
	}
	for i := range f.pairs {
		if f.pairs[i].Key == key {
			f.pairs[i].Value = val
			return f
		}
	}
	f.pairs = append(f.pairs, FactPair{Key: key, Value: val})
	return f
}

// Get returns the value for key, if present.
func (f *Facts) Get(key string) (string, bool) {
	if f == nil {
		return "", false
	}
	for _, p := range f.pairs {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// Pairs returns a copy of the ordered pairs.
func (f *Facts) Pairs() []FactPair {
	if f == nil {
		return nil
	}
	out := make([]FactPair, len(f.pairs))
	copy(out, f.pairs)
	return out
}

// ToMap returns the facts as a Go map.
func (f *Facts) ToMap() map[string]string {
	m := make(map[string]string)
	if f != nil {
		for _, p := range f.pairs {
			m[p.Key] = p.Value
		}
	}
	return m
}

// Len returns the count of facts.
func (f *Facts) Len() int {
	if f == nil {
		return 0
	}
	return len(f.pairs)
}

// MarshalJSON marshals Facts as a JSON object, preserving insertion order.
func (f *Facts) MarshalJSON() ([]byte, error) {
	if f == nil || len(f.pairs) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range f.pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(p.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON unmarshals a JSON object into ordered Facts.
func (f *Facts) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("verbout: expected {, got %v", t)
	}
	f.pairs = nil
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := kt.(string)
		if !ok {
			return fmt.Errorf("verbout: expected string key, got %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			f.pairs = append(f.pairs, FactPair{Key: key, Value: s})
		} else {
			f.pairs = append(f.pairs, FactPair{Key: key, Value: string(raw)})
		}
	}
	_, err = dec.Token() // closing '}'
	return err
}

// Result describes the outcome of running a verb.
type Result struct {
	Verb   string `json:"verb"`
	Status string `json:"status"` // ok | refused | failed
	Exit   int    `json:"exit"`
	Remedy string `json:"remedy,omitempty"`
}

// Item is one typed line item in a listing.
type Item struct {
	Kind   string `json:"kind"`
	Fields *Facts `json:"fields,omitempty"`
	Text   string `json:"text,omitempty"`
}

// Items is a slice of Item.
type Items []Item

// More describes capped output when bounded.
type More struct {
	Kind   string `json:"kind"`
	Shown  int    `json:"shown"`
	Total  int    `json:"total"`
	Remedy string `json:"remedy,omitempty"`
}

// Value represents the unified output structure.
type Value struct {
	Result Result   `json:"result"`
	Facts  *Facts   `json:"facts,omitempty"`
	Items  Items    `json:"items,omitempty"`
	More   []More   `json:"more,omitempty"`
	Notes  []string `json:"notes,omitempty"`

	// Token overrides the uppercase verb token in text rendering if set.
	Token string `json:"-"`

	// StatusLast causes items/more/notes to render before the result line in text mode.
	StatusLast bool `json:"-"`
}

// Out is an alias for Value per the specification.
type Out = Value

// New creates a new Value for verb with StatusOK.
func New(verb string) *Value {
	return OK(verb)
}

// OK creates a Value with status "ok" and exit 0.
func OK(verb string) *Value {
	return &Value{
		Result: Result{
			Verb:   verb,
			Status: StatusOK,
			Exit:   0,
		},
		Facts: NewFacts(),
	}
}

// Failed creates a Value with status "failed".
func Failed(verb string, exit int) *Value {
	if exit == 0 {
		exit = 1
	}
	return &Value{
		Result: Result{
			Verb:   verb,
			Status: StatusFailed,
			Exit:   exit,
		},
		Facts: NewFacts(),
	}
}

// Refuse creates a Value with status "refused" and exit 2.
func Refuse(verb, what, remedy string) *Value {
	v := &Value{
		Result: Result{
			Verb:   verb,
			Status: StatusRefused,
			Exit:   2,
			Remedy: remedy,
		},
		Facts: NewFacts(),
	}
	if what != "" {
		v.Fact("reason", what)
	}
	return v
}

// SetToken sets an explicit token for text rendering (e.g. "LINKS", "QUICKSTART").
func (v *Value) SetToken(token string) *Value {
	v.Token = token
	return v
}

// Fact adds a key=val fact.
func (v *Value) Fact(key, val string) *Value {
	if v.Facts == nil {
		v.Facts = NewFacts()
	}
	v.Facts.Set(key, val)
	return v
}

// FactInt adds a key=int fact.
func (v *Value) FactInt(key string, val int) *Value {
	return v.Fact(key, strconv.Itoa(val))
}

// FactInt64 adds a key=int64 fact.
func (v *Value) FactInt64(key string, val int64) *Value {
	return v.Fact(key, strconv.FormatInt(val, 10))
}

// Item adds a typed item line with text.
func (v *Value) Item(kind, text string) *Value {
	v.Items = append(v.Items, Item{Kind: kind, Text: text})
	return v
}

// ItemFields adds a typed item line with structured fields.
func (v *Value) ItemFields(kind string, fields *Facts) *Value {
	v.Items = append(v.Items, Item{Kind: kind, Fields: fields})
	return v
}

// AddMore adds a More capping descriptor.
func (v *Value) AddMore(kind string, shown, total int, remedy string) *Value {
	v.More = append(v.More, More{
		Kind:   kind,
		Shown:  shown,
		Total:  total,
		Remedy: remedy,
	})
	return v
}

// Note adds a NOTE line.
func (v *Value) Note(s string) *Value {
	v.Notes = append(v.Notes, s)
	return v
}

// TokenName returns the uppercase event token.
func (v *Value) TokenName() string {
	if v.Token != "" {
		return v.Token
	}
	t := strings.ToUpper(v.Result.Verb)
	t = strings.ReplaceAll(t, "-", "")
	return t
}

// RenderJSON serializes v to w as JSON.
func (v *Value) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// JSON is an alias for RenderJSON.
func (v *Value) JSON(w io.Writer) error {
	return v.RenderJSON(w)
}

// RenderText formats v into line-oriented text per CLI-STYLE and SPEC.
func (v *Value) RenderText(w io.Writer) error {
	token := v.TokenName()

	renderResult := func() {
		var statusWord string
		switch v.Result.Status {
		case StatusOK:
			statusWord = "OK"
		case StatusFailed:
			statusWord = "FAIL"
		case StatusRefused:
			statusWord = "REFUSED"
		default:
			statusWord = strings.ToUpper(v.Result.Status)
		}

		var b strings.Builder
		b.WriteString(token)
		b.WriteString(" ")
		b.WriteString(statusWord)

		if v.Facts != nil {
			for _, p := range v.Facts.Pairs() {
				b.WriteString(" ")
				if p.Value == "" {
					b.WriteString(p.Key)
				} else {
					b.WriteString(p.Key)
					b.WriteString("=")
					b.WriteString(oneline.Field(p.Value))
				}
			}
		}

		if v.Result.Remedy != "" {
			// If not already in facts under remedy= or next=
			_, hasRemedy := v.Facts.Get("remedy")
			_, hasNext := v.Facts.Get("next")
			if !hasRemedy && !hasNext {
				rem := strings.TrimPrefix(v.Result.Remedy, "run: ")
				b.WriteString("; run: ")
				b.WriteString(oneline.Escape(rem))
			}
		}
		b.WriteString("\n")
		_, _ = io.WriteString(w, b.String())
	}

	renderItemsAndMore := func() {
		for _, item := range v.Items {
			var b strings.Builder
			b.WriteString(token)
			b.WriteString(" ")
			b.WriteString(item.Kind)
			if item.Fields != nil {
				for _, p := range item.Fields.Pairs() {
					b.WriteString(" ")
					b.WriteString(p.Key)
					b.WriteString("=")
					b.WriteString(oneline.Field(p.Value))
				}
			}
			if item.Text != "" {
				b.WriteString(" ")
				b.WriteString(item.Text)
			}
			b.WriteString("\n")
			_, _ = io.WriteString(w, b.String())
		}

		for _, m := range v.More {
			if m.Total > m.Shown {
				var b strings.Builder
				b.WriteString(token)
				b.WriteString(" MORE kind=")
				b.WriteString(oneline.Field(m.Kind))
				b.WriteString(" shown=")
				b.WriteString(strconv.Itoa(m.Shown))
				b.WriteString(" total=")
				b.WriteString(strconv.Itoa(m.Total))
				if m.Remedy != "" {
					b.WriteString(" ")
					b.WriteString(oneline.Escape(m.Remedy))
				}
				b.WriteString("\n")
				_, _ = io.WriteString(w, b.String())
			}
		}

		for _, n := range v.Notes {
			var b strings.Builder
			b.WriteString(token)
			b.WriteString(" NOTE ")
			b.WriteString(oneline.Escape(n))
			b.WriteString("\n")
			_, _ = io.WriteString(w, b.String())
		}
	}

	if v.StatusLast {
		renderItemsAndMore()
		renderResult()
	} else {
		renderResult()
		renderItemsAndMore()
	}

	return nil
}

// Lines is an alias for RenderText.
func (v *Value) Lines(w io.Writer) error {
	return v.RenderText(w)
}

// Text returns the rendered text string.
func (v *Value) Text() string {
	var buf bytes.Buffer
	_ = v.RenderText(&buf)
	return buf.String()
}

// Emit writes v to stdout (if OK) or stderr (if FAIL/REFUSED), or as JSON to stdout if asJSON is true.
// It returns Result.Exit.
func (v *Value) Emit(stdout, stderr io.Writer, asJSON bool) int {
	if asJSON {
		_ = v.RenderJSON(stdout)
		return v.Result.Exit
	}
	var w io.Writer = stdout
	if v.Result.Status != StatusOK {
		w = stderr
	}
	_ = v.RenderText(w)
	return v.Result.Exit
}

// EmitTo writes v to w, as JSON if asJSON is true, otherwise as lines.
// It returns Result.Exit.
func (v *Value) EmitTo(w io.Writer, asJSON bool) int {
	if asJSON {
		_ = v.RenderJSON(w)
	} else {
		_ = v.RenderText(w)
	}
	return v.Result.Exit
}

// BoundedList provides an in-memory bounded collector that feeds into Value.Items and Value.More.
type BoundedList struct {
	val    *Value
	max    int
	kind   string
	remedy string
	shown  int
	total  int
}

// Bounded returns a BoundedList for collecting capped items.
func (v *Value) Bounded(max int, kind, remedy string) *BoundedList {
	return &BoundedList{
		val:    v,
		max:    max,
		kind:   kind,
		remedy: remedy,
	}
}

// Line records one item line. It is always counted toward Total, and added to Items while within max.
func (b *BoundedList) Line(kind, text string) {
	b.total++
	if b.max > 0 && b.shown >= b.max {
		return
	}
	b.val.Item(kind, text)
	b.shown++
}

// Finish adds the More descriptor to Value if items were elided.
func (b *BoundedList) Finish() {
	if b.total > b.shown {
		b.val.AddMore(b.kind, b.shown, b.total, b.remedy)
	}
}

// Shown is how many items were recorded in Value.Items.
func (b *BoundedList) Shown() int { return b.shown }

// Total is total items offered to Line.
func (b *BoundedList) Total() int { return b.total }

// Elided is Total minus Shown.
func (b *BoundedList) Elided() int { return b.total - b.shown }
