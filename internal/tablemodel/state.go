package tablemodel

import (
	"encoding/json"
	"fmt"
	"sort"
)

// The observed state of the store in the model's vocabulary, and its JSON
// form. The JSON is the trace the replay retains: every value is an array,
// exactly the nesting the TLA+ tuples have, so a trace reads next to the
// harness generated from it.

// Phys is one physical table: a table name at an epoch. JSON [table, epoch].
type Phys struct {
	Table string
	Epoch int
}

// Cell is a cell of a physical table, or one of the two sentinels.
// JSON [[table, epoch], row, column].
type Cell struct {
	P        Phys
	Row, Col string
}

// External is the cell the bound set outside every table lives in, and NoPlace
// is where a member is when it has no place in a table.
var (
	External = Cell{Phys{"external", 0}, "external", "external"}
	NoPlace  = Cell{Phys{"none", 0}, "none", "none"}
)

// RowAt is a row present in a physical table. JSON [[table, epoch], row].
type RowAt struct {
	P   Phys
	Row string
}

// Bind is a cell bound to a target. JSON [cell, target].
type Bind struct{ Cell, Target Cell }

// Datum is a member of a cell with its score. JSON [cell, member, score].
type Datum struct {
	Cell   Cell
	Member string
	Score  int
}

// Location is where a member is in one physical table. JSON [[table, epoch], cell].
type Location struct {
	P    Phys
	Cell Cell
}

// Placement is the place of one member in every physical table.
// JSON [member, [location...]].
type Placement struct {
	Member    string
	Locations []Location
}

// State is everything the model observes of the store at one moment.
type State struct {
	Live   []Phys         `json:"live"`
	Rows   []RowAt        `json:"rows"`
	Binds  []Bind         `json:"binds"`
	Data   []Datum        `json:"data"`
	Place  []Placement    `json:"place"`
	Active int            `json:"active"`
	Seen   map[string]int `json:"seen"`
}

// Clone returns a copy that shares nothing with s.
func (s State) Clone() State {
	c := State{Active: s.Active, Seen: map[string]int{}}
	c.Live = append([]Phys{}, s.Live...)
	c.Rows = append([]RowAt{}, s.Rows...)
	c.Binds = append([]Bind{}, s.Binds...)
	c.Data = append([]Datum{}, s.Data...)
	for _, p := range s.Place {
		c.Place = append(c.Place, Placement{p.Member, append([]Location{}, p.Locations...)})
	}
	for k, v := range s.Seen {
		c.Seen[k] = v
	}
	return c
}

func tuple(v ...any) ([]byte, error) { return json.Marshal(v) }

func (p Phys) MarshalJSON() ([]byte, error)      { return tuple(p.Table, p.Epoch) }
func (c Cell) MarshalJSON() ([]byte, error)      { return tuple(c.P, c.Row, c.Col) }
func (r RowAt) MarshalJSON() ([]byte, error)     { return tuple(r.P, r.Row) }
func (b Bind) MarshalJSON() ([]byte, error)      { return tuple(b.Cell, b.Target) }
func (d Datum) MarshalJSON() ([]byte, error)     { return tuple(d.Cell, d.Member, d.Score) }
func (l Location) MarshalJSON() ([]byte, error)  { return tuple(l.P, l.Cell) }
func (p Placement) MarshalJSON() ([]byte, error) { return tuple(p.Member, p.Locations) }

// elements splits a JSON array into exactly n raw elements.
func elements(data []byte, n int, what string) ([]json.RawMessage, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != n {
		return nil, fmt.Errorf("%s is not an array of %d elements: %s", what, n, data)
	}
	return parts, nil
}

func decode(parts []json.RawMessage, into ...any) error {
	for i, p := range parts {
		if err := json.Unmarshal(p, into[i]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Phys) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 2, "a physical table")
	if err != nil {
		return err
	}
	return decode(parts, &p.Table, &p.Epoch)
}

func (c *Cell) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 3, "a cell")
	if err != nil {
		return err
	}
	return decode(parts, &c.P, &c.Row, &c.Col)
}

func (r *RowAt) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 2, "a row")
	if err != nil {
		return err
	}
	return decode(parts, &r.P, &r.Row)
}

func (b *Bind) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 2, "a binding")
	if err != nil {
		return err
	}
	return decode(parts, &b.Cell, &b.Target)
}

func (d *Datum) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 3, "a datum")
	if err != nil {
		return err
	}
	return decode(parts, &d.Cell, &d.Member, &d.Score)
}

func (l *Location) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 2, "a location")
	if err != nil {
		return err
	}
	return decode(parts, &l.P, &l.Cell)
}

func (p *Placement) UnmarshalJSON(data []byte) error {
	parts, err := elements(data, 2, "a placement")
	if err != nil {
		return err
	}
	return decode(parts, &p.Member, &p.Locations)
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
