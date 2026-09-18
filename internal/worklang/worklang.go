/*
Package worklang is the bounded reader for a `.work` plan (docs/SPEC-WORKLANG.md).

A plan is data the kernel reads, never a program it evaluates. The reader is the
restricted-Lisp grammar the rest of this repo already uses for work files: a
sequence of s-expressions made only of lists, keywords, strings, integers and
bare symbols, with every evaluation form refused at the boundary and three bounds enforced as
the reader runs -- bytes, nesting depth and node count. Nothing read here is ever
evaluated: there is no eval, and a dispatch macro is a refusal with the byte
offset that owes it, not a form the reader will run.

Every refusal is a *Refusal carrying exit code 2, so the command that opens a
plan can print one line and stop, never a partial parse.
*/
package worklang

import (
	"fmt"
	"strings"
)

// Limits is the three bounds every plan read is held to. A zero value is not
// usable; build one with DefaultLimits or set all three. The names are the flag
// names the caller exposes: --max-bytes, --max-depth, --max-nodes.
type Limits struct {
	MaxBytes int
	MaxDepth int
	MaxNodes int
}

// DefaultLimits is the shape the kernel's intake limits already carry
// (lisp/nova-work/src/control.lisp): 64 KiB, depth 64, 4096 nodes.
func DefaultLimits() Limits {
	return Limits{MaxBytes: 65536, MaxDepth: 64, MaxNodes: 4096}
}

// Refusal is a plan this reader would not read. It is always exit 2: the tool
// could not run, and the input is refused whole rather than truncated.
type Refusal struct {
	File   string
	Reason string
}

func (r *Refusal) Error() string {
	if r.File == "" {
		return "plan: " + r.Reason
	}
	return "plan file=" + r.File + ": " + r.Reason
}

// ExitCode is 2 for every refusal, the code docs/nova-lessons.md fixes for a
// plan that could not be read.
func (r *Refusal) ExitCode() int { return 2 }

func refuse(file, reason string) *Refusal { return &Refusal{File: file, Reason: reason} }

// Kind is the type of one restricted form.
type Kind int

const (
	// List is a parenthesized form, possibly empty.
	List Kind = iota
	// Keyword is a `:name` token, stored without the leading colon.
	Keyword
	// String is a double-quoted string, stored decoded.
	String
	// Integer is a decimal integer.
	Integer
	// Symbol is a bare token such as go-fix or false, stored verbatim. The
	// plan language uses symbols for values that are not text (kind names,
	// booleans), and they are data: nothing here is ever evaluated.
	Symbol
)

// Form is one restricted s-expression. Offset is the form's first byte, so a
// refusal can name where in the file it happened.
type Form struct {
	Kind   Kind
	Offset int
	List   []Form
	Value  string // keyword name (no colon) or decoded string
	Int    int64
}

// IsKeyword reports whether f is the keyword named name (without the colon).
func (f Form) IsKeyword(name string) bool { return f.Kind == Keyword && f.Value == name }

// Text returns the decoded string of a String form, or "".
func (f Form) Text() string {
	if f.Kind == String {
		return f.Value
	}
	return ""
}

// reader walks the bytes of one plan under the three bounds.
type reader struct {
	file   string
	data   []byte
	pos    int
	depth  int
	nodes  int
	limits Limits
}

// Read reads exactly one restricted form from data, holding it to limits and
// refusing anything else. The file name is carried into every refusal.
func Read(file string, data []byte, limits Limits) (Form, error) {
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxNodes <= 0 {
		return Form{}, refuse(file, "the reader's three bounds must all be positive; refusing to guess")
	}
	// BYTES first: a file past the byte bound is refused before a byte of it is
	// parsed, never truncated to fit.
	if len(data) > limits.MaxBytes {
		return Form{}, refuse(file, fmt.Sprintf(
			"past --max-bytes=%d (file is %d bytes); refused whole, never truncated",
			limits.MaxBytes, len(data)))
	}
	// The lexical pass refuses every evaluation form at its byte offset before
	// the parser can intern one. String and comment text is opaque.
	if err := scanSyntax(file, data); err != nil {
		return Form{}, err
	}
	r := &reader{file: file, data: data, limits: limits}
	form, err := r.form()
	if err != nil {
		return Form{}, err
	}
	r.skipSpace()
	if r.pos < len(r.data) {
		return Form{}, refuse(file, fmt.Sprintf(
			"trailing bytes after one form, at byte=%d", r.pos))
	}
	return form, nil
}

// scanSyntax lexes the raw text and refuses the tokens the restricted grammar
// does not admit, each at its own UTF-8 start byte. A `#.` dispatch macro is
// the first of them; the rest are the reader macros that would evaluate or
// escape. Comment and string bytes are opaque, so a `#.` inside either is text.
func scanSyntax(file string, data []byte) error {
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// opaque
		case c == ';':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '#':
			return refuse(file, fmt.Sprintf(
				"dispatch macro at byte=%d; a plan is data, never a program, so remove it", i))
		case c == '|':
			return refuse(file, fmt.Sprintf("multiple escape at byte=%d", i))
		case c == '\'':
			return refuse(file, fmt.Sprintf("quote at byte=%d", i))
		case c == '`':
			return refuse(file, fmt.Sprintf("backquote at byte=%d", i))
		case c == ',':
			return refuse(file, fmt.Sprintf("unquote at byte=%d", i))
		case c == '\\':
			return refuse(file, fmt.Sprintf("single escape at byte=%d", i))
		}
	}
	return nil
}

// skipSpace consumes whitespace and line comments. A comment runs to the end of
// its line and is text, never syntax.
func (r *reader) skipSpace() {
	for r.pos < len(r.data) {
		switch r.data[r.pos] {
		case ' ', '\t', '\r', '\n', '\f':
			r.pos++
		case ';':
			for r.pos < len(r.data) && r.data[r.pos] != '\n' && r.data[r.pos] != '\r' {
				r.pos++
			}
		default:
			return
		}
	}
}

// node counts one atom against --max-nodes and refuses at the atom's byte when
// the bound is past.
func (r *reader) node(off int) error {
	r.nodes++
	if r.nodes > r.limits.MaxNodes {
		return refuse(r.file, fmt.Sprintf(
			"past --max-nodes=%d at byte=%d; refused whole, never truncated",
			r.limits.MaxNodes, off))
	}
	return nil
}

func (r *reader) form() (Form, error) {
	r.skipSpace()
	if r.pos >= len(r.data) {
		return Form{}, refuse(r.file, fmt.Sprintf(
			"unbalanced form; input ended at byte=%d", r.pos))
	}
	switch c := r.data[r.pos]; {
	case c == '(':
		return r.list()
	case c == '"':
		return r.str()
	case c == ':':
		return r.keyword()
	case c == '+' || c == '-' || (c >= '0' && c <= '9'):
		return r.integer()
	default:
		return r.symbol()
	}
}

// symbol reads a bare token such as go-fix or false. It is restricted data,
// never a function: the reader has no environment and no eval.
func (r *reader) symbol() (Form, error) {
	start := r.pos
	for r.pos < len(r.data) && !isBoundary(r.data[r.pos]) {
		r.pos++
	}
	if r.pos == start {
		return Form{}, refuse(r.file, fmt.Sprintf("forbidden token at byte=%d", start))
	}
	if err := r.node(start); err != nil {
		return Form{}, err
	}
	return Form{Kind: Symbol, Offset: start, Value: string(r.data[start:r.pos])}, nil
}

func (r *reader) list() (Form, error) {
	start := r.pos
	r.pos++
	r.depth++
	if r.depth > r.limits.MaxDepth {
		return Form{}, refuse(r.file, fmt.Sprintf(
			"past --max-depth=%d at byte=%d; refused whole, never truncated",
			r.limits.MaxDepth, start))
	}
	defer func() { r.depth-- }()
	out := Form{Kind: List, Offset: start}
	for {
		r.skipSpace()
		if r.pos >= len(r.data) {
			return Form{}, refuse(r.file, fmt.Sprintf(
				"unbalanced form; input ended at byte=%d", r.pos))
		}
		if r.data[r.pos] == ')' {
			r.pos++
			return out, nil
		}
		item, err := r.form()
		if err != nil {
			return Form{}, err
		}
		out.List = append(out.List, item)
	}
}

func (r *reader) str() (Form, error) {
	start := r.pos
	r.pos++
	var b strings.Builder
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		switch c {
		case '"':
			r.pos++
			if err := r.node(start); err != nil {
				return Form{}, err
			}
			return Form{Kind: String, Offset: start, Value: b.String()}, nil
		case '\\':
			r.pos++
			if r.pos >= len(r.data) {
				return Form{}, refuse(r.file, fmt.Sprintf(
					"unterminated string at byte=%d", start))
			}
			b.WriteByte(r.data[r.pos])
			r.pos++
		default:
			b.WriteByte(c)
			r.pos++
		}
	}
	return Form{}, refuse(r.file, fmt.Sprintf("unterminated string at byte=%d", start))
}

func (r *reader) keyword() (Form, error) {
	start := r.pos
	r.pos++ // ':'
	begin := r.pos
	for r.pos < len(r.data) && !isBoundary(r.data[r.pos]) {
		r.pos++
	}
	name := string(r.data[begin:r.pos])
	if name == "" || strings.Contains(name, ":") {
		return Form{}, refuse(r.file, fmt.Sprintf("forbidden token at byte=%d", start))
	}
	if err := r.node(start); err != nil {
		return Form{}, err
	}
	return Form{Kind: Keyword, Offset: start, Value: name}, nil
}

func (r *reader) integer() (Form, error) {
	start := r.pos
	if r.data[r.pos] == '+' || r.data[r.pos] == '-' {
		r.pos++
	}
	begin := r.pos
	for r.pos < len(r.data) && r.data[r.pos] >= '0' && r.data[r.pos] <= '9' {
		r.pos++
	}
	if r.pos == begin || (r.pos < len(r.data) && !isBoundary(r.data[r.pos])) {
		return Form{}, refuse(r.file, fmt.Sprintf("forbidden token at byte=%d", start))
	}
	var n int64
	for _, c := range r.data[begin:r.pos] {
		n = n*10 + int64(c-'0')
	}
	if start < len(r.data) && r.data[start] == '-' {
		n = -n
	}
	if err := r.node(start); err != nil {
		return Form{}, err
	}
	return Form{Kind: Integer, Offset: start, Int: n}, nil
}

func isBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f', '(', ')', ';', '"', '#', '|', '\'', '`', ',', '\\':
		return true
	}
	return false
}
