package batchmodel

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// The wire decoder deliberately supports the finite BatchMemberTable instance
// (three IDs, two rows/columns, scores 1 or 2 and its two application fields).
// A manifest outside that vocabulary is not silently simplified into a model
// action. The original bytes remain the operation's identity.
type wireManifest struct {
	Schema                json.Number  `json:"schema"`
	Table                 string       `json:"table"`
	Epoch                 string       `json:"epoch"`
	ExpectedTableRevision string       `json:"expected_table_revision"`
	OperationID           string       `json:"operation_id"`
	Actor                 string       `json:"actor"`
	Members               []wireMember `json:"members"`
}
type wireMember struct {
	ID     string            `json:"id"`
	Expect *wireExpect       `json:"expect"`
	Create *wirePlaceScore   `json:"create"`
	Move   *wireMove         `json:"move"`
	Remove *bool             `json:"remove"`
	Set    map[string]string `json:"set"`
	Unset  []string          `json:"unset"`
}
type wireExpect struct {
	Absent   *bool                `json:"absent"`
	Revision *string              `json:"revision"`
	Place    *wireCell            `json:"place"`
	Fields   map[string]wireGuard `json:"fields"`
}
type wireCell struct {
	Row string `json:"row"`
	Col string `json:"col"`
}
type wirePlaceScore struct {
	Row   string      `json:"row"`
	Col   string      `json:"col"`
	Score json.Number `json:"score"`
}
type wireMove struct {
	Row   string       `json:"row"`
	Col   string       `json:"col"`
	Score *json.Number `json:"score"`
}
type wireGuard struct {
	Equals *string  `json:"equals"`
	Absent *bool    `json:"absent"`
	OneOf  []string `json:"one_of"`
}

// DecodeRequest binds a strict decode of the exact bytes sent to FCALL to the
// model action. before is an independently captured prestate, never a receipt.
// It returns an explicit outside-scope error for any legal runtime request the
// finite TLA instance cannot represent without dropping a guard or mutation.
func DecodeRequest(raw []byte, before Snapshot, tableBaseline string, memberBaseline map[string]string) (Request, BatchAction, error) {
	if len(raw) == 0 || len(raw) > 1048576 {
		return Request{}, BatchAction{}, errors.New("manifest size outside wire limit")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Request{}, BatchAction{}, err
	}
	if err := validateManifestShape(raw); err != nil {
		return Request{}, BatchAction{}, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	var w wireManifest
	if err := d.Decode(&w); err != nil {
		return Request{}, BatchAction{}, fmt.Errorf("strict manifest decode: %w", err)
	}
	if w.Schema != "1" || w.Table != "t1" || (w.Epoch != "1" && w.Epoch != "2") || w.OperationID == "" || w.Actor != "w1" {
		return Request{}, BatchAction{}, errors.New("manifest outside finite model vocabulary")
	}
	if len(w.Members) < 1 || len(w.Members) > 3 {
		return Request{}, BatchAction{}, errors.New("batch size outside finite model bound 1..3")
	}
	if _, err := decimal(w.Epoch); err != nil {
		return Request{}, BatchAction{}, err
	}
	projected, err := ProjectRevision(w.ExpectedTableRevision, tableBaseline)
	if err != nil {
		return Request{}, BatchAction{}, fmt.Errorf("table revision: %w", err)
	}
	h := sha1.Sum(raw)
	digest := hex.EncodeToString(h[:])
	q := Request{Canonical: append([]byte(nil), raw...), OperationID: w.OperationID, Digest: digest, Actor: w.Actor, Table: w.Table, Epoch: w.Epoch, Revision: w.ExpectedTableRevision,
		ExpectedRevision: map[string]*string{}}
	a := BatchAction{Table: w.Table, Epoch: w.Epoch, OperationID: w.OperationID, Actor: w.Actor, Digest: digest, Canonical: append([]byte(nil), raw...), ProjectedRevision: projected}
	seen := map[string]bool{}
	for _, m := range w.Members {
		if m.ID != "m1" && m.ID != "m2" && m.ID != "m3" {
			return Request{}, BatchAction{}, fmt.Errorf("member %q outside finite model", m.ID)
		}
		if seen[m.ID] {
			return Request{}, BatchAction{}, fmt.Errorf("duplicate member %q", m.ID)
		}
		seen[m.ID] = true
		if m.Expect == nil {
			return Request{}, BatchAction{}, fmt.Errorf("member %s lacks expect", m.ID)
		}
		observed, ok := before.Members[m.ID]
		if !ok {
			return Request{}, BatchAction{}, fmt.Errorf("member %s lacks independent prestate", m.ID)
		}
		base, ok := memberBaseline[m.ID]
		if !ok {
			return Request{}, BatchAction{}, fmt.Errorf("member %s lacks fixed revision baseline", m.ID)
		}
		entry, rawRev, err := decodeMember(w.Table, w.Epoch, m, observed, base)
		if err != nil {
			return Request{}, BatchAction{}, fmt.Errorf("member %s: %w", m.ID, err)
		}
		q.Selected = append(q.Selected, m.ID)
		q.ExpectedRevision[m.ID] = rawRev
		q.Entries = append(q.Entries, entry)
		a.Members = append(a.Members, entry)
	}
	if _, err := a.TLA(); err != nil {
		return Request{}, BatchAction{}, fmt.Errorf("unrepresentable model action: %w", err)
	}
	return q, a, nil
}

func decodeMember(table, epoch string, m wireMember, observed Member, baseline string) (MemberAction, *string, error) {
	if m.Expect.Absent != nil && !*m.Expect.Absent {
		return MemberAction{}, nil, errors.New("absent guard must be true")
	}
	absent := m.Expect.Absent != nil
	if absent && (m.Expect.Revision != nil || m.Expect.Place != nil || len(m.Expect.Fields) > 0) {
		return MemberAction{}, nil, errors.New("absent expectation cannot guard an existing record")
	}
	if m.Remove != nil && !*m.Remove {
		return MemberAction{}, nil, errors.New("remove must be true when supplied")
	}
	if m.Create != nil && (m.Move != nil || m.Remove != nil || !absent) {
		return MemberAction{}, nil, errors.New("create requires absent and cannot combine with move/remove")
	}
	if m.Move != nil && m.Remove != nil {
		return MemberAction{}, nil, errors.New("move and remove conflict")
	}
	if len(m.Set) > 1 || len(m.Unset) > 1 || len(m.Expect.Fields) > 1 {
		return MemberAction{}, nil, errors.New("multiple fields outside finite model action")
	}
	if absent && m.Create == nil {
		return MemberAction{}, nil, errors.New("absent guard-only request outside current model")
	}
	var rev *uint64
	var rawRev *string
	if m.Expect.Revision != nil {
		n, err := ProjectRevision(*m.Expect.Revision, baseline)
		if err != nil {
			return MemberAction{}, nil, err
		}
		rev = &n
		x := *m.Expect.Revision
		rawRev = &x
	}
	entry := MemberAction{ID: m.ID, Absent: absent, Revision: rev, Source: noPlaceCell(), Target: noPlaceCell(), Score: 0,
		Remove: m.Remove != nil, RemoveSupplied: m.Remove != nil, Change: m.Create != nil || m.Move != nil || m.Remove != nil || len(m.Set) > 0 || len(m.Unset) > 0}
	if !absent {
		if m.Expect.Place != nil {
			c, err := modelCell(table, epoch, m.Expect.Place.Row, m.Expect.Place.Col)
			if err != nil {
				return MemberAction{}, nil, err
			}
			entry.Source = c
		} else if observed.Place != nil {
			c, err := modelCell(table, epoch, observed.Place.Row, observed.Place.Column)
			if err != nil {
				return MemberAction{}, nil, err
			}
			entry.Source = c
		}
		if observed.Place != nil {
			s, err := modelScore(observed.Place.Score)
			if err != nil {
				return MemberAction{}, nil, err
			}
			entry.Score = s
		}
	}
	entry.Target = entry.Source
	if m.Create != nil {
		c, err := modelCell(table, epoch, m.Create.Row, m.Create.Col)
		if err != nil {
			return MemberAction{}, nil, err
		}
		entry.Target = c
		entry.Score, err = modelScore(string(m.Create.Score))
		if err != nil {
			return MemberAction{}, nil, err
		}
		entry.ScoreChange = true
	}
	if m.Move != nil {
		c, err := modelCell(table, epoch, m.Move.Row, m.Move.Col)
		if err != nil {
			return MemberAction{}, nil, err
		}
		entry.Target = c
		if m.Move.Score != nil {
			entry.Score, err = modelScore(string(*m.Move.Score))
			if err != nil {
				return MemberAction{}, nil, err
			}
			entry.ScoreChange = true
		}
	}
	if m.Remove != nil {
		entry.Target = noPlaceCell()
	}
	for f, g := range m.Expect.Fields {
		if err := modelField(f); err != nil {
			return MemberAction{}, nil, err
		}
		entry.GuardField = f
		n := 0
		if g.Equals != nil {
			n++
		}
		if g.Absent != nil {
			n++
		}
		if g.OneOf != nil {
			n++
		}
		if n != 1 {
			return MemberAction{}, nil, errors.New("field guard needs exactly one condition")
		}
		switch {
		case g.Equals != nil:
			if err := modelValue(*g.Equals); err != nil {
				return MemberAction{}, nil, err
			}
			entry.GuardKind = "equals"
			entry.GuardValues = []string{*g.Equals}
		case g.Absent != nil:
			if !*g.Absent {
				return MemberAction{}, nil, errors.New("absent field guard must be true")
			}
			entry.GuardKind = "absent"
		case g.OneOf != nil:
			if len(g.OneOf) == 0 {
				return MemberAction{}, nil, errors.New("one_of guard must be nonempty")
			}
			entry.GuardKind = "one_of"
			for _, v := range g.OneOf {
				if err := modelValue(v); err != nil {
					return MemberAction{}, nil, err
				}
			}
			entry.GuardValues = append([]string(nil), g.OneOf...)
		}
	}
	for f, v := range m.Set {
		if err := modelField(f); err != nil {
			return MemberAction{}, nil, err
		}
		if err := modelValue(v); err != nil {
			return MemberAction{}, nil, err
		}
		entry.SetField = f
		entry.SetValue = v
	}
	for _, f := range m.Unset {
		if err := modelField(f); err != nil {
			return MemberAction{}, nil, err
		}
		entry.UnsetField = f
	}
	if entry.SetField != "" && entry.SetField == entry.UnsetField {
		return MemberAction{}, nil, errors.New("same field set and unset")
	}
	return entry, rawRev, nil
}

func noPlaceCell() Cell { return Cell{"none", "0", "none", "none"} }
func modelCell(table, epoch, row, col string) (Cell, error) {
	if (row != "r1" && row != "r2") || (col != "c1" && col != "c2") {
		return Cell{}, errors.New("cell outside finite model")
	}
	return Cell{table, epoch, row, col}, nil
}
func modelScore(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s || n < 1 || n > 2 {
		return 0, fmt.Errorf("score %q outside finite model", s)
	}
	return n, nil
}
func modelField(s string) error {
	if s != "status" && s != "token" {
		return fmt.Errorf("field %q outside finite model", s)
	}
	return nil
}
func modelValue(s string) error {
	switch s {
	case "", "ready", "done", "permit", "new", "deny":
		return nil
	}
	return fmt.Errorf("value %q outside finite model", s)
}

// rejectDuplicateJSONKeys compares decoded object keys, so actor and
// \u0061ctor (also nested absent and \u0061bsent) collide.
func rejectDuplicateJSONKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := walkJSON(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}
func walkJSON(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return err
			}
			k, ok := t.(string)
			if !ok {
				return errors.New("non-string object key")
			}
			if seen[k] {
				return fmt.Errorf("duplicate JSON key %q", k)
			}
			seen[k] = true
			if err := walkJSON(d); err != nil {
				return err
			}
		}
		_, err := d.Token()
		return err
	case '[':
		for d.More() {
			if err := walkJSON(d); err != nil {
				return err
			}
		}
		_, err := d.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

// Validate the exact wire spelling and JSON type before decoding into Go
// structs. encoding/json deliberately folds object names by case and maps null
// into a nil pointer, so DisallowUnknownFields alone would change an action.
type wireShape func(json.RawMessage) error

func validateManifestShape(raw []byte) error {
	str := primitiveShape('"')
	num := numberShape
	boolean := boolShape
	cell := objectShape(map[string]wireShape{"row": str, "col": str}, "row", "col")
	place := objectShape(map[string]wireShape{"row": str, "col": str, "score": num}, "row", "col", "score")
	move := objectShape(map[string]wireShape{"row": str, "col": str, "score": num}, "row", "col")
	guard := objectShape(map[string]wireShape{"equals": str, "absent": boolean, "one_of": arrayShape(str)})
	expect := objectShape(map[string]wireShape{"absent": boolean, "revision": str, "place": cell, "fields": stringMapShape(guard)})
	member := objectShape(map[string]wireShape{"id": str, "expect": expect, "create": place, "move": move, "remove": boolean, "set": stringMapShape(str), "unset": arrayShape(str)}, "id", "expect")
	return objectShape(map[string]wireShape{"schema": num, "table": str, "epoch": str,
		"expected_table_revision": str, "operation_id": str, "actor": str, "members": arrayShape(member)},
		"schema", "table", "epoch", "expected_table_revision", "operation_id", "actor", "members")(raw)
}

func objectShape(fields map[string]wireShape, required ...string) wireShape {
	return func(raw json.RawMessage) error {
		var obj map[string]json.RawMessage
		if !rawKind(raw, '{') || json.Unmarshal(raw, &obj) != nil {
			return errors.New("expected JSON object")
		}
		for _, name := range required {
			if _, ok := obj[name]; !ok {
				return fmt.Errorf("missing JSON field %q", name)
			}
		}
		for name, value := range obj {
			check, ok := fields[name]
			if !ok {
				return fmt.Errorf("unknown or mis-cased JSON field %q", name)
			}
			if err := check(value); err != nil {
				return fmt.Errorf("field %s: %w", name, err)
			}
		}
		return nil
	}
}

func stringMapShape(value wireShape) wireShape {
	return func(raw json.RawMessage) error {
		var obj map[string]json.RawMessage
		if !rawKind(raw, '{') || json.Unmarshal(raw, &obj) != nil {
			return errors.New("expected JSON object")
		}
		for name, item := range obj {
			if err := value(item); err != nil {
				return fmt.Errorf("map key %s: %w", name, err)
			}
		}
		return nil
	}
}

func arrayShape(value wireShape) wireShape {
	return func(raw json.RawMessage) error {
		var items []json.RawMessage
		if !rawKind(raw, '[') || json.Unmarshal(raw, &items) != nil {
			return errors.New("expected JSON array")
		}
		for i, item := range items {
			if err := value(item); err != nil {
				return fmt.Errorf("array item %d: %w", i, err)
			}
		}
		return nil
	}
}

func primitiveShape(want byte) wireShape {
	return func(raw json.RawMessage) error {
		if !rawKind(raw, want) {
			return fmt.Errorf("expected JSON primitive beginning %q", want)
		}
		return nil
	}
}

func numberShape(raw json.RawMessage) error {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || (trim[0] != '-' && (trim[0] < '0' || trim[0] > '9')) {
		return errors.New("expected JSON number")
	}
	return nil
}

func boolShape(raw json.RawMessage) error {
	trim := bytes.TrimSpace(raw)
	if bytes.Equal(trim, []byte("true")) || bytes.Equal(trim, []byte("false")) {
		return nil
	}
	return errors.New("expected JSON boolean")
}

func rawKind(raw json.RawMessage, want byte) bool {
	trim := bytes.TrimSpace(raw)
	return len(trim) > 0 && trim[0] == want
}
