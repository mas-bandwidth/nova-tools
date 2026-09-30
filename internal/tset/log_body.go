package tset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LogBody is a stored line's compact body d, decoded (L2 1.1): the kind tag,
// the call's time, and each kind's fields in the stored words. From and To
// are nil when the body omits them; Score and Rev are aligned with IDs, each
// a [before, after] pair whose nil is JSON null.
type LogBody struct {
	K      string
	MS     string
	Tbl    string
	From   *string
	To     *string
	IDs    []string
	About  []string
	Score  [][2]*string
	Rev    [][2]*string
	Shared map[string]string
	Set    []map[string]string
	Unset  [][]string
	Meta   json.RawMessage
	Add    []RowRank
	Del    []string
}

type rawLogBody struct {
	K      *string             `json:"k"`
	MS     *string             `json:"ms"`
	Tbl    string              `json:"tbl"`
	From   *string             `json:"from"`
	To     *string             `json:"to"`
	IDs    []string            `json:"ids"`
	About  []string            `json:"about"`
	Score  [][]*string         `json:"score"`
	Rev    [][]*string         `json:"rev"`
	Shared map[string]string   `json:"shared"`
	Set    []map[string]string `json:"set"`
	Unset  [][]string          `json:"unset"`
	Meta   json.RawMessage     `json:"meta"`
	Add    []RowRank           `json:"add"`
	Del    []string            `json:"del"`
}

// ParseLogBody decodes a stored body and checks what the store's reader
// checks: a known kind tag, a time, and a member line's arrays aligned with
// its ids. An error is a body the supported writer does not write (DRIFT).
func ParseLogBody(d string) (LogBody, error) {
	var r rawLogBody
	dec := json.NewDecoder(strings.NewReader(d))
	if err := dec.Decode(&r); err != nil {
		return LogBody{}, fmt.Errorf("log body: %w", err)
	}
	if r.K == nil || LogWords[*r.K] == "" {
		return LogBody{}, errors.New("log body: no known kind tag")
	}
	if r.MS == nil {
		return LogBody{}, errors.New("log body: no time")
	}
	b := LogBody{K: *r.K, MS: *r.MS, Tbl: r.Tbl, From: r.From, To: r.To, IDs: r.IDs, About: r.About,
		Shared: r.Shared, Set: r.Set, Unset: r.Unset, Meta: r.Meta, Add: r.Add, Del: r.Del}
	if len(bytes.TrimSpace(b.Meta)) == 0 || string(bytes.TrimSpace(b.Meta)) == "null" {
		b.Meta = nil
	}
	switch b.K {
	case "c", "m", "x":
		k := len(b.IDs)
		if len(b.About) != k || len(r.Score) != k || len(r.Rev) != k ||
			(r.Set != nil && len(r.Set) != k) || (r.Unset != nil && len(r.Unset) != k) {
			return LogBody{}, errors.New("log body: a member line's arrays are not aligned with its ids")
		}
		for j := 0; j < k; j++ {
			if len(r.Score[j]) != 2 || len(r.Rev[j]) != 2 {
				return LogBody{}, errors.New("log body: a score or revision is not a pair")
			}
			b.Score = append(b.Score, [2]*string{r.Score[j][0], r.Score[j][1]})
			b.Rev = append(b.Rev, [2]*string{r.Rev[j][0], r.Rev[j][1]})
		}
	}
	return b, nil
}

// Count is the id count n a body says: its ids for a member line, its about
// set for a note, 0 otherwise (L2 1.1).
func (b LogBody) Count() int {
	switch b.K {
	case "c", "m", "x":
		return len(b.IDs)
	case "n":
		return len(b.About)
	}
	return 0
}

// ProjectLine is table_set_log.lua's projection of one stored line onto one
// primary (L2 4, L1 7): an item per member id whose about is the primary,
// with its own effect and only the requested fields, or the note itself. No
// other primary's ids or fields are copied out. A line that does not name
// the primary is DRIFT.
func ProjectLine(line LogLine, about string, fields []string, includeMeta bool) ([]json.RawMessage, *Refusal) {
	drift := NewRefusal("DRIFT", RefusalDetail{Budget: "history", IDs: []string{about}})
	b, err := ParseLogBody(line.D)
	if err != nil {
		return nil, NewRefusal("DRIFT", RefusalDetail{Budget: "log_body"})
	}
	meta := func() (string, bool) {
		if !includeMeta || b.Meta == nil {
			return "", false
		}
		s, ok, err := encodeMeta(b.Meta)
		if err != nil || !ok {
			// An empty stored object or array is still meta the line carries.
			return string(bytes.TrimSpace(b.Meta)), true
		}
		return s, true
	}
	var out []json.RawMessage
	if b.K == "n" {
		named := false
		for _, p := range b.About {
			if p == about {
				named = true
				break
			}
		}
		if !named {
			return nil, drift
		}
		item := `{"seq":` + cjsonString(string(line.Seq)) + `,"kind":"note","at_ms":` + cjsonString(b.MS)
		if m, ok := meta(); ok {
			item += `,"meta":` + m
		}
		return []json.RawMessage{json.RawMessage(item + "}")}, nil
	}
	if b.K != "c" && b.K != "m" && b.K != "x" {
		return nil, drift
	}
	for j, id := range b.IDs {
		if b.About[j] != about {
			continue
		}
		var own map[string]string
		if b.Set != nil {
			own = b.Set[j]
		}
		gone := map[string]bool{}
		if b.Unset != nil {
			for _, name := range b.Unset[j] {
				gone[name] = true
			}
		}
		set := map[string]string{}
		var unset []string
		for _, f := range fields {
			v, ok := own[f]
			if !ok {
				v, ok = b.Shared[f]
			}
			if _, done := set[f]; ok && !done {
				set[f] = v
			} else if gone[f] {
				delete(gone, f)
				unset = append(unset, f)
			}
		}
		var s strings.Builder
		s.WriteString(`{"seq":` + cjsonString(string(line.Seq)) + `,"kind":` + cjsonString(LogWords[b.K]) +
			`,"at_ms":` + cjsonString(b.MS) + `,"table":` + cjsonString(b.Tbl) + `,"id":` + cjsonString(id) +
			`,"score":[` + nullablePtr(b.Score[j][0]) + `,` + nullablePtr(b.Score[j][1]) + `]` +
			`,"rev":[` + nullablePtr(b.Rev[j][0]) + `,` + nullablePtr(b.Rev[j][1]) + `]`)
		if b.From != nil {
			s.WriteString(`,"from":` + cjsonString(*b.From))
		}
		if b.To != nil {
			s.WriteString(`,"to":` + cjsonString(*b.To))
		}
		if len(set) != 0 {
			s.WriteString(`,"set":` + cjsonObject(set))
		}
		if len(unset) != 0 {
			s.WriteString(`,"unset":` + cjsonStrings(unset))
		}
		if m, ok := meta(); ok {
			s.WriteString(`,"meta":` + m)
		}
		s.WriteString("}")
		out = append(out, json.RawMessage(s.String()))
	}
	if len(out) == 0 {
		return nil, drift
	}
	return out, nil
}

func nullablePtr(s *string) string {
	if s == nil {
		return "null"
	}
	return cjsonString(*s)
}
