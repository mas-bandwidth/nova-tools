package swarm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// THE FAKE STORE EVALUATES THE STATEMENT IT IS HANDED. A fake that answered a LiveUsage's
// reads from its own fields would let the SQL drift from what the fake assumes it says (the
// second cold read of PR 5953). This one parses each statement with a grammar of exactly the
// SQL these readers emit and evaluates it over a message table held as SQLite holds it: a
// rowid, the `data` JSON, time_created and a time_updated that may be NULL or absent. A
// statement outside the grammar is an error, so a change to the SQL fails a test until the
// fake is taught its meaning.
//
//	stmt    := SELECT expr {, expr} FROM message [WHERE expr] [ORDER BY rowid]
//	expr    := cmp {OR cmp}
//	cmp     := primary [IS NOT (NULL | 'text') | > int | >= int | IN ( int {, int} )]
//	primary := ( expr ) | rowid | time_created | time_updated | int
//	         | COALESCE ( expr , expr ) | json_extract ( data , 'path' )

// sqlMessage is one row of the fake's message table.
type sqlMessage struct {
	rowid   int64
	data    string // the JSON OpenCode writes
	created int64
	updated *int64 // NULL when nil
}

// sqlTable is the fake's message table; untimed is one with no time_updated column.
type sqlTable struct {
	rows    []sqlMessage // in rowid order
	untimed bool
}

// sqlVal is one SQL value: NULL, or a number or a text with its printed form.
type sqlVal struct {
	null  bool
	num   bool
	n     float64
	text  string
	truth bool // a boolean's value, printed 1 or 0
}

func sqlInt(n int64) sqlVal { return sqlVal{num: true, n: float64(n), text: strconv.FormatInt(n, 10)} }

func sqlBool(b bool) sqlVal {
	v := sqlInt(0)
	if b {
		v = sqlInt(1)
	}
	v.truth = b
	return v
}

func (v sqlVal) isTrue() bool { return !v.null && (v.truth || (v.num && v.n != 0)) }

type sqlTok struct {
	kind string // "id", "int", "str", "op"
	s    string
}

func sqlLex(src string) ([]sqlTok, error) {
	var toks []sqlTok
	for i := 0; i < len(src); {
		c := rune(src[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '\'':
			j := strings.IndexByte(src[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("an unclosed string at %d", i)
			}
			toks = append(toks, sqlTok{"str", src[i+1 : i+1+j]})
			i += j + 2
		case c == '-' || unicode.IsDigit(c):
			j := i + 1
			for j < len(src) && unicode.IsDigit(rune(src[j])) {
				j++
			}
			toks = append(toks, sqlTok{"int", src[i:j]})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(src) && (unicode.IsLetter(rune(src[j])) || unicode.IsDigit(rune(src[j])) || src[j] == '_') {
				j++
			}
			toks = append(toks, sqlTok{"id", src[i:j]})
			i = j
		case strings.HasPrefix(src[i:], ">="):
			toks = append(toks, sqlTok{"op", ">="})
			i += 2
		case strings.ContainsRune("(),>", c):
			toks = append(toks, sqlTok{"op", string(c)})
			i++
		default:
			return nil, fmt.Errorf("an unknown character %q at %d", c, i)
		}
	}
	return toks, nil
}

// sqlNode evaluates over one row.
type sqlNode func(m sqlMessage, t *sqlTable) (sqlVal, error)

type sqlParser struct {
	toks []sqlTok
	at   int
}

func (p *sqlParser) peek() sqlTok {
	if p.at < len(p.toks) {
		return p.toks[p.at]
	}
	return sqlTok{}
}

func (p *sqlParser) word(s string) bool {
	if t := p.peek(); (t.kind == "id" || t.kind == "op") && t.s == s {
		p.at++
		return true
	}
	return false
}

func (p *sqlParser) must(s string) error {
	if !p.word(s) {
		return fmt.Errorf("want %q at token %d, found %q", s, p.at, p.peek().s)
	}
	return nil
}

func (p *sqlParser) integer() (int64, error) {
	t := p.peek()
	if t.kind != "int" {
		return 0, fmt.Errorf("want an integer at token %d, found %q", p.at, t.s)
	}
	p.at++
	return strconv.ParseInt(t.s, 10, 64)
}

func (p *sqlParser) expr() (sqlNode, error) {
	left, err := p.cmp()
	if err != nil {
		return nil, err
	}
	for p.word("OR") {
		right, err := p.cmp()
		if err != nil {
			return nil, err
		}
		l := left
		left = func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			a, err := l(m, t)
			if err != nil {
				return a, err
			}
			b, err := right(m, t)
			return sqlBool(a.isTrue() || b.isTrue()), err
		}
	}
	return left, nil
}

func (p *sqlParser) cmp() (sqlNode, error) {
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	switch {
	case p.word("IS"):
		if err := p.must("NOT"); err != nil {
			return nil, err
		}
		if p.word("NULL") {
			return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
				v, err := x(m, t)
				return sqlBool(!v.null), err
			}, nil
		}
		lit := p.peek()
		if lit.kind != "str" {
			return nil, fmt.Errorf("want NULL or a text after IS NOT, found %q", lit.s)
		}
		p.at++
		return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			v, err := x(m, t)
			return sqlBool(v.null || v.num || v.text != lit.s), err // NULL IS NOT 'x' is true
		}, nil
	case p.word(">"), p.word(">="):
		op := p.toks[p.at-1].s
		n, err := p.integer()
		if err != nil {
			return nil, err
		}
		return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			v, err := x(m, t)
			if err != nil || v.null || !v.num {
				return sqlBool(false), err
			}
			if op == ">" {
				return sqlBool(v.n > float64(n)), nil
			}
			return sqlBool(v.n >= float64(n)), nil
		}, nil
	case p.word("IN"):
		if err := p.must("("); err != nil {
			return nil, err
		}
		set := map[float64]bool{}
		for {
			n, err := p.integer()
			if err != nil {
				return nil, err
			}
			set[float64(n)] = true
			if !p.word(",") {
				break
			}
		}
		if err := p.must(")"); err != nil {
			return nil, err
		}
		return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			v, err := x(m, t)
			return sqlBool(!v.null && v.num && set[v.n]), err
		}, nil
	}
	return x, nil
}

func (p *sqlParser) primary() (sqlNode, error) {
	t := p.peek()
	switch {
	case p.word("("):
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		return e, p.must(")")
	case t.kind == "int":
		n, err := p.integer()
		v := sqlInt(n)
		return func(sqlMessage, *sqlTable) (sqlVal, error) { return v, nil }, err
	case p.word("rowid"):
		return func(m sqlMessage, _ *sqlTable) (sqlVal, error) { return sqlInt(m.rowid), nil }, nil
	case p.word("time_created"):
		return func(m sqlMessage, _ *sqlTable) (sqlVal, error) { return sqlInt(m.created), nil }, nil
	case p.word("time_updated"):
		return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			if t.untimed {
				return sqlVal{}, fmt.Errorf("Parse error: no such column: time_updated")
			}
			if m.updated == nil {
				return sqlVal{null: true}, nil
			}
			return sqlInt(*m.updated), nil
		}, nil
	case p.word("COALESCE"):
		if err := p.must("("); err != nil {
			return nil, err
		}
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.must(","); err != nil {
			return nil, err
		}
		b, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.must(")"); err != nil {
			return nil, err
		}
		return func(m sqlMessage, t *sqlTable) (sqlVal, error) {
			v, err := a(m, t)
			if err != nil || !v.null {
				return v, err
			}
			return b(m, t)
		}, nil
	case p.word("json_extract"):
		if err := p.must("("); err != nil {
			return nil, err
		}
		if err := p.must("data"); err != nil {
			return nil, err
		}
		if err := p.must(","); err != nil {
			return nil, err
		}
		path := p.peek()
		if path.kind != "str" || !strings.HasPrefix(path.s, "$.") {
			return nil, fmt.Errorf("want a '$.path', found %q", path.s)
		}
		p.at++
		if err := p.must(")"); err != nil {
			return nil, err
		}
		keys := strings.Split(strings.TrimPrefix(path.s, "$."), ".")
		return func(m sqlMessage, _ *sqlTable) (sqlVal, error) { return jsonExtract(m.data, keys) }, nil
	}
	return nil, fmt.Errorf("an expression the fake does not know at token %d: %q", p.at, t.s)
}

// jsonExtract is SQLite's json_extract of one object path: NULL for a key not there or a
// JSON null, a number or a text as itself, an object as its JSON text.
func jsonExtract(data string, keys []string) (sqlVal, error) {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return sqlVal{}, err
	}
	for _, k := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			return sqlVal{null: true}, nil
		}
		if v, ok = obj[k]; !ok {
			return sqlVal{null: true}, nil
		}
	}
	switch x := v.(type) {
	case nil:
		return sqlVal{null: true}, nil
	case json.Number:
		f, err := x.Float64()
		return sqlVal{num: true, n: f, text: x.String()}, err
	case string:
		return sqlVal{text: x}, nil
	case bool:
		return sqlBool(x), nil
	default:
		b, err := json.Marshal(x)
		return sqlVal{text: string(b)}, err
	}
}

// run evaluates one statement over the table and prints its rows as `sqlite3 -tabs` does:
// a NULL is the empty cell.
func (t *sqlTable) run(sql string) ([][]string, error) {
	toks, err := sqlLex(sql)
	if err != nil {
		return nil, err
	}
	p := &sqlParser{toks: toks}
	if err := p.must("SELECT"); err != nil {
		return nil, err
	}
	var cols []sqlNode
	for {
		c, err := p.expr()
		if err != nil {
			return nil, err
		}
		cols = append(cols, c)
		if !p.word(",") {
			break
		}
	}
	if err := p.must("FROM"); err != nil {
		return nil, err
	}
	if err := p.must("message"); err != nil {
		return nil, err
	}
	var where sqlNode
	if p.word("WHERE") {
		if where, err = p.expr(); err != nil {
			return nil, err
		}
	}
	if p.word("ORDER") {
		if err := p.must("BY"); err != nil {
			return nil, err
		}
		if err := p.must("rowid"); err != nil {
			return nil, err
		}
	}
	if p.at != len(p.toks) {
		return nil, fmt.Errorf("the fake's grammar ends at token %d: %q", p.at, p.peek().s)
	}
	var out [][]string
	for _, m := range t.rows { // rowid order: ORDER BY rowid, and SQLite's own order without it
		if where != nil {
			v, err := where(m, t)
			if err != nil {
				return nil, err
			}
			if !v.isTrue() {
				continue
			}
		}
		row := make([]string, len(cols))
		for i, c := range cols {
			v, err := c(m, t)
			if err != nil {
				return nil, err
			}
			if !v.null {
				row[i] = v.text
			}
		}
		out = append(out, row)
	}
	return out, nil
}
