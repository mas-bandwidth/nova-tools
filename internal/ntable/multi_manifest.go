package ntable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// MultiBatchManifest is one atomic transaction over two to sixteen tables.
// Scope owns operation identity independently of the participating tables.
type MultiBatchManifest struct {
	Schema      int                `json:"schema"`
	Scope       string             `json:"scope"`
	OperationID string             `json:"operation_id"`
	Actor       string             `json:"actor,omitempty"`
	Tables      []MultiBatchTable  `json:"tables"`
	Members     []MultiBatchMember `json:"members"`
}

type MultiBatchTable struct {
	Name                  string `json:"name"`
	Epoch                 string `json:"epoch"`
	ExpectedTableRevision string `json:"expected_table_revision"`
}

// RecordTable selects the member namespace; placements can span compatible tables.
type MultiBatchMember struct {
	ID          string             `json:"id"`
	RecordTable string             `json:"record_table"`
	Expect      *MultiMemberExpect `json:"expect"`
	Placements  []MultiPlacement   `json:"placements,omitempty"`
	Set         map[string]string  `json:"set,omitempty"`
	Unset       []string           `json:"unset,omitempty"`
}

type MultiMemberExpect struct {
	Absent   bool                  `json:"absent,omitempty"`
	Revision string                `json:"revision,omitempty"`
	Fields   map[string]FieldGuard `json:"fields,omitempty"`
	Places   []MultiPlaceExpect    `json:"places,omitempty"`
}

type MultiPlaceExpect struct {
	Table  string `json:"table"`
	Absent bool   `json:"absent,omitempty"`
	Row    string `json:"row,omitempty"`
	Col    string `json:"col,omitempty"`
}

type MultiPlacement struct {
	Table  string          `json:"table"`
	Add    *MemberCreateOp `json:"add,omitempty"`
	Move   *MemberMoveOp   `json:"move,omitempty"`
	Remove bool            `json:"remove,omitempty"`
}

// ValidateMultiBatchManifestRaw checks the raw v2 JSON without coercing values.
// It rejects unknown or repeated keys before decoding into Go structs.
func ValidateMultiBatchManifestRaw(raw []byte) (*MultiBatchManifest, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, &ManifestError{Msg: "empty manifest"}
	}
	if err := over(limitNameManifest, LimitManifestBytes, len(raw), ""); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, &ManifestError{Msg: "manifest is not valid UTF-8"}
	}
	if err := checkEscapes(raw); err != nil {
		return nil, &ManifestError{Msg: err.Error()}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := multiValue(d, "root"); err != nil {
		return nil, &ManifestError{Msg: err.Error()}
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, &ManifestError{Msg: "unexpected trailing content after JSON manifest"}
	}
	var m MultiBatchManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &ManifestError{Msg: err.Error()}
	}
	if err := validateMultiSemantics(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

var multiKeys = map[string]map[string]bool{
	"root":      {"schema": true, "scope": true, "operation_id": true, "actor": true, "tables": true, "members": true},
	"table":     {"name": true, "epoch": true, "expected_table_revision": true},
	"member":    {"id": true, "record_table": true, "expect": true, "placements": true, "set": true, "unset": true},
	"expect":    {"absent": true, "revision": true, "fields": true, "places": true},
	"place":     {"table": true, "absent": true, "row": true, "col": true},
	"placement": {"table": true, "add": true, "move": true, "remove": true},
	"add":       {"row": true, "col": true, "score": true},
	"move":      {"row": true, "col": true, "score": true},
	"guard":     {"equals": true, "absent": true, "one_of": true},
}

func multiChild(parent, key string) string {
	switch parent + "." + key {
	case "root.tables":
		return "tables"
	case "root.members":
		return "members"
	case "member.expect":
		return "expect"
	case "member.placements":
		return "placements"
	case "member.set":
		return "set"
	case "member.unset":
		return "strings"
	case "expect.fields":
		return "fields"
	case "expect.places":
		return "places"
	case "placement.add":
		return "add"
	case "placement.move":
		return "move"
	case "guard.one_of":
		return "strings"
	}
	return ""
}

func multiValue(d *json.Decoder, kind string) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	switch kind {
	case "root", "table", "member", "expect", "place", "placement", "add", "move", "guard", "fields", "set":
		if tok != json.Delim('{') {
			return fmt.Errorf("%s must be an object", kind)
		}
		seen := map[string]bool{}
		for d.More() {
			kt, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := kt.(string)
			if !ok {
				return fmt.Errorf("%s key must be a string", kind)
			}
			if seen[key] {
				return fmt.Errorf("duplicate key %q in %s", bounded(key), kind)
			}
			seen[key] = true
			if kind != "fields" && kind != "set" && !multiKeys[kind][key] {
				return fmt.Errorf("unknown field %q in %s", bounded(key), kind)
			}
			child := multiChild(kind, key)
			if kind == "fields" {
				child = "guard"
			}
			if kind == "set" {
				child = "string"
			}
			if child == "" {
				child = multiScalarKind(kind, key)
			}
			if err := multiValue(d, child); err != nil {
				return fmt.Errorf("%s.%s: %w", kind, bounded(key), err)
			}
		}
		switch kind {
		case "root":
			if !seen["schema"] || !seen["scope"] || !seen["operation_id"] || !seen["tables"] || !seen["members"] {
				return fmt.Errorf("root requires schema, scope, operation_id, tables and members")
			}
		case "table":
			if !seen["name"] || !seen["epoch"] || !seen["expected_table_revision"] {
				return fmt.Errorf("table requires name, epoch and expected_table_revision")
			}
		case "member":
			if !seen["record_table"] || !seen["id"] || !seen["expect"] {
				return fmt.Errorf("member requires record_table, id and expect")
			}
		case "place":
			if !seen["table"] || (!seen["absent"] && (!seen["row"] || !seen["col"])) {
				return fmt.Errorf("place requires table and absent or row and col")
			}
		case "placement":
			if !seen["table"] {
				return fmt.Errorf("placement requires table")
			}
		case "add":
			if !seen["row"] || !seen["col"] || !seen["score"] {
				return fmt.Errorf("add requires row, col and score")
			}
		case "move":
			if !seen["row"] || !seen["col"] {
				return fmt.Errorf("move requires row and col")
			}
		}
		_, err = d.Token()
		return err
	case "tables", "members", "placements", "places", "strings":
		if tok != json.Delim('[') {
			return fmt.Errorf("%s must be an array", kind)
		}
		child := map[string]string{"tables": "table", "members": "member", "placements": "placement", "places": "place", "strings": "string"}[kind]
		for i := 0; d.More(); i++ {
			if err := multiValue(d, child); err != nil {
				return fmt.Errorf("%s[%d]: %w", kind, i, err)
			}
		}
		_, err = d.Token()
		return err
	case "string", "decimal":
		s, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected %s", kind)
		}
		if kind == "decimal" && !uintString(s) {
			return fmt.Errorf("expected canonical decimal string")
		}
		return nil
	case "true":
		if tok != true {
			return fmt.Errorf("expected true")
		}
		return nil
	case "score":
		if _, ok := tok.(json.Number); !ok {
			return fmt.Errorf("expected JSON number")
		}
		return nil
	case "schema":
		if _, ok := tok.(json.Number); !ok {
			return fmt.Errorf("schema must be a JSON number")
		}
		return nil
	default:
		return fmt.Errorf("unsupported manifest value")
	}
}

func multiScalarKind(kind, key string) string {
	switch key {
	case "schema":
		return "schema"
	case "score":
		return "score"
	case "absent", "remove":
		return "true"
	case "epoch", "expected_table_revision", "revision":
		return "decimal"
	default:
		return "string"
	}
}

func validateMultiSemantics(m *MultiBatchManifest) error {
	if m.Schema != 2 {
		return rule("SCHEMA", "", nil, "schema %d is not supported, expected 2", m.Schema)
	}
	if !ValidName(m.Scope) || len(m.Scope) > 128 {
		return rule("SCOPE", "", nil, "scope must be 1 to 128 bytes of letters, digits, _ . or -")
	}
	if !word(m.OperationID) {
		return rule("OPERATION", "", nil, "operation_id must be a nonempty string without control characters")
	}
	if len(m.OperationID) > 256 {
		return rule("OPERATION", "", nil, "operation_id must be at most 256 bytes")
	}
	if len(m.Tables) < 2 {
		return rule("MANIFEST", "", ErrMalformedManifest, "tables must contain at least two participants")
	}
	if err := over(limitNameMultiTables, LimitMultiTables, len(m.Tables), ""); err != nil {
		return err
	}
	if len(m.Members) == 0 {
		return rule("MANIFEST", "", ErrMalformedManifest, "a manifest names at least one member")
	}
	tables := map[string]bool{}
	for _, t := range m.Tables {
		if !ValidName(t.Name) {
			return rule("NAME", "", nil, "table %q wants letters, digits, _ . and -", bounded(t.Name))
		}
		if tables[t.Name] {
			return rule("TWICE", "", ErrDuplicateMember, "table %q appears more than once", bounded(t.Name))
		}
		tables[t.Name] = true
		if !uintString(t.Epoch) {
			return rule("EPOCH", "", nil, "epoch must be a decimal uint64 string")
		}
		if !uintString(t.ExpectedTableRevision) {
			return rule("REVISION", "", nil, "expected_table_revision must be a decimal uint64 string")
		}
	}
	changed, guards := 0, 0
	seenMembers := map[string]bool{}
	for _, e := range m.Members {
		if !word(e.ID) {
			return rule("MEMBER", "", nil, "member id must be a nonempty string without control characters")
		}
		if err := over(limitNameMemberID, LimitMemberIDBytes, len(e.ID), e.ID); err != nil {
			return err
		}
		if !tables[e.RecordTable] {
			return rule("NAME", e.ID, nil, "record_table must name a participant")
		}
		// Physical aliases can only be determined with the server's immutable prefixes.
		// This catches duplicate references in a single declared record table.
		identity := e.RecordTable + "\x00" + e.ID
		if seenMembers[identity] {
			return rule("TWICE", e.ID, ErrDuplicateMember, "member appears more than once")
		}
		seenMembers[identity] = true
		if e.Expect == nil {
			return rule("MANIFEST", e.ID, ErrMalformedManifest, "missing expect")
		}
		x := e.Expect
		if x.Places != nil && len(x.Places) == 0 {
			return rule("MANIFEST", e.ID, ErrMalformedManifest, "places must be nonempty when present")
		}
		if e.Placements != nil && len(e.Placements) == 0 {
			return rule("MANIFEST", e.ID, ErrMalformedManifest, "placements must be nonempty when present")
		}
		if x.Absent && (x.Revision != "" || x.Fields != nil) {
			return rule("MUTATION", e.ID, ErrMutation, "expect absent cannot combine with revision or fields")
		}
		if x.Revision != "" && !uintString(x.Revision) {
			return rule("ARGS", e.ID, nil, "expect revision must be a decimal string")
		}
		if err := over(limitNameGuards, LimitFieldGuards, len(x.Fields), e.ID); err != nil {
			return err
		}
		for f, g := range x.Fields {
			if !word(f) {
				return rule("ARGS", e.ID, nil, "field guard names must be nonempty without control characters")
			}
			n := 0
			if g.Equals != nil {
				n++
			}
			if g.Absent != nil {
				n++
				if !*g.Absent {
					return rule("FIELDGUARD", e.ID, ErrFieldGuard, "absent must be true")
				}
			}
			if g.OneOf != nil {
				n++
				if len(g.OneOf) == 0 {
					return rule("FIELDGUARD", e.ID, ErrFieldGuard, "one_of must be nonempty")
				}
			}
			if n != 1 {
				return rule("FIELDGUARD", e.ID, ErrFieldGuard, "field guard for %q must specify exactly one condition", bounded(f))
			}
			if err := over(limitNameOneOf, LimitOneOfOptions, len(g.OneOf), e.ID); err != nil {
				return err
			}
			options := map[string]bool{}
			for _, v := range g.OneOf {
				if options[v] {
					return rule("FIELDGUARD", e.ID, ErrFieldGuard, "one_of repeats an option")
				}
				options[v] = true
			}
		}
		places := map[string]bool{}
		for _, p := range x.Places {
			if !tables[p.Table] {
				return rule("NAME", e.ID, nil, "place table must name a participant")
			}
			if places[p.Table] {
				return rule("TWICE", e.ID, ErrDuplicateMember, "place guard repeats a table")
			}
			places[p.Table] = true
			if p.Absent {
				if p.Row != "" || p.Col != "" {
					return rule("MUTATION", e.ID, ErrMutation, "absent place cannot combine with row or col")
				}
			} else if !ValidRowKey(p.Row) || !ValidName(p.Col) {
				return rule("ARGS", e.ID, nil, "place requires valid row and col")
			}
		}
		if err := over(limitNameSet, LimitSetFields, len(e.Set), e.ID); err != nil {
			return err
		}
		if err := over(limitNameUnset, LimitUnsetFields, len(e.Unset), e.ID); err != nil {
			return err
		}
		for f, v := range e.Set {
			if reservedField(f) {
				return rule("RESERVEDFIELD", e.ID, ErrReservedField, "field %q", bounded(f))
			}
			if !word(f) {
				return rule("ARGS", e.ID, nil, "field names must be nonempty without control characters")
			}
			if err := over(limitNameFieldValue, LimitFieldValueBytes, len(v), e.ID); err != nil {
				return err
			}
		}
		unset := map[string]bool{}
		for _, f := range e.Unset {
			if unset[f] {
				return rule("ARGS", e.ID, nil, "unset repeats field %q", bounded(f))
			}
			unset[f] = true
			if reservedField(f) {
				return rule("RESERVEDFIELD", e.ID, ErrReservedField, "field %q", bounded(f))
			}
			if !word(f) {
				return rule("ARGS", e.ID, nil, "field names must be nonempty without control characters")
			}
			if _, ok := e.Set[f]; ok {
				return rule("MUTATION", e.ID, ErrMutation, "field %q cannot be both set and unset", bounded(f))
			}
		}
		mutated := len(e.Set) > 0 || len(e.Unset) > 0
		placementTables := map[string]bool{}
		for _, p := range e.Placements {
			if !tables[p.Table] {
				return rule("NAME", e.ID, nil, "placement table must name a participant")
			}
			if placementTables[p.Table] {
				return rule("TWICE", e.ID, ErrDuplicateMember, "placement repeats a table")
			}
			placementTables[p.Table] = true
			if !places[p.Table] {
				return rule("MANIFEST", e.ID, ErrMalformedManifest, "placement requires an expected place for its table")
			}
			n := 0
			if p.Add != nil {
				n++
				if !ValidRowKey(p.Add.Row) || !ValidName(p.Add.Col) || math.IsNaN(p.Add.Score) || math.IsInf(p.Add.Score, 0) {
					return rule("ARGS", e.ID, nil, "add requires valid row, col and finite score")
				}
			}
			if p.Move != nil {
				n++
				if !ValidRowKey(p.Move.Row) || !ValidName(p.Move.Col) {
					return rule("ARGS", e.ID, nil, "move requires valid row and col")
				}
				if p.Move.Score != nil && (math.IsNaN(*p.Move.Score) || math.IsInf(*p.Move.Score, 0)) {
					return rule("SCORE", e.ID, ErrInvalidScore, "expected a finite JSON number")
				}
			}
			if p.Remove {
				n++
			}
			if n != 1 {
				return rule("MUTATION", e.ID, ErrMutation, "placement must specify exactly one of add, move or remove")
			}
			mutated = true
		}
		if mutated {
			changed++
		} else {
			guards++
		}
	}
	if err := over(limitNameChanged, LimitChangedEntries, changed, ""); err != nil {
		return err
	}
	return over(limitNameGuardEntries, LimitGuardEntries, guards, "")
}
