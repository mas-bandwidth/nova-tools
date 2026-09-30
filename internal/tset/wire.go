package tset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	decimalRE  = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	nameRE     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	scoreRE    = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	hexScoreRE = regexp.MustCompile(`^[+-]?0[xX](?:[0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)(?:[pP][+-]?[0-9]+)?$`)
)

// ValidDecimal rejects JSON-number spellings and values beyond uint64.
func ValidDecimal(s Decimal) bool {
	if !decimalRE.MatchString(string(s)) {
		return false
	}
	_, err := strconv.ParseUint(string(s), 10, 64)
	return err == nil
}

// NextDecimal computes the exact successor without floating-point conversion.
func NextDecimal(s Decimal) (Decimal, error) {
	if !ValidDecimal(s) {
		return "", NewRefusal("REQUEST", RefusalDetail{})
	}
	n, _ := strconv.ParseUint(string(s), 10, 64)
	if n == math.MaxUint64 {
		return "", NewRefusal("OVERFLOW", RefusalDetail{})
	}
	return Decimal(strconv.FormatUint(n+1, 10)), nil
}

// ParseCellRef splits at the last colon so row names may contain colons.
func ParseCellRef(cell string) (row, col string, err error) {
	i := strings.LastIndexByte(cell, ':')
	if i <= 0 || i == len(cell)-1 {
		return "", "", NewRefusal("REQUEST", RefusalDetail{})
	}
	row, col = cell[:i], cell[i+1:]
	if code := rowCode(row); code != "" {
		return "", "", NewRefusal(code, RefusalDetail{})
	}
	if code := symbolicCode(col); code != "" {
		return "", "", NewRefusal(code, RefusalDetail{})
	}
	return row, col, nil
}

func validName(s string) bool {
	return symbolicCode(s) == ""
}

func validID(s string) bool {
	return idCode(s) == ""
}

func validRow(s string) bool {
	return rowCode(s) == ""
}

func idCode(s string) string {
	if s == "" || !utf8.ValidString(s) {
		return "REQUEST"
	}
	if len(s) > MaxIdentifierBytes {
		return "LIMIT"
	}
	return ""
}

func symbolicCode(s string) string {
	if s == "" || !nameRE.MatchString(s) {
		return "REQUEST"
	}
	if len(s) > MaxIdentifierBytes {
		return "LIMIT"
	}
	return ""
}

func rowCode(s string) string {
	if s == "" || !utf8.ValidString(s) {
		return "REQUEST"
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "REQUEST"
		}
	}
	if len(s) > MaxIdentifierBytes {
		return "LIMIT"
	}
	return ""
}

func refusalCode(err error) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return "REQUEST"
}

func validScore(s string) bool {
	if !scoreRE.MatchString(s) {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && !(f == 0 && hasNonzeroDigit(s))
}

func hasNonzeroDigit(s string) bool {
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	for _, c := range s {
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

func validScoreBound(s string) bool {
	// The target Redis range parser treats an empty token, with or without
	// the exclusive prefix, as zero. Keep this narrow: other whitespace-only
	// spellings are not implied by that observation.
	if s == "" || s == "(" {
		return true
	}
	if strings.HasPrefix(s, "(") {
		s = s[1:]
	}
	// Redis's ZRANGE/ZCOUNT bound parser uses strtod rather than the stricter
	// ZADD score grammar: leading ASCII whitespace, hex floats and underflow
	// are accepted. Trailing whitespace and NaN remain malformed.
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	if strings.TrimRight(s, " \t\n\r\v\f") != s || s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if lower == "inf" || lower == "+inf" || lower == "-inf" || lower == "infinity" || lower == "+infinity" || lower == "-infinity" {
		return true
	}
	if hexScoreRE.MatchString(s) {
		return true
	}
	if !scoreRE.MatchString(s) {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil || errors.Is(err, strconv.ErrRange)
}

// EncodeStep validates the complete request before the caller opens a Redis
// pipeline. The trusted builder chooses Op/Intent; this function never invents
// either value or hashes a freshly replanned request.
func EncodeStep(s Step) ([]byte, error) {
	return encodeStep(s, false)
}

// encodeStep permits an unnamed note-bearing intermediate only while BuildStep
// is cutting entries before the caller assigns its stable operation identity.
// Every public encode and dispatch uses the strict path.
func encodeStep(s Step, builderIntermediate bool) ([]byte, error) {
	if err := validateStep(s, !builderIntermediate); err != nil {
		return nil, err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	if len(b) > MaxWriteRequestBytes {
		return nil, NewRefusal("LIMIT", RefusalDetail{})
	}
	if jsonDepth(b) > MaxJSONDepth {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	return b, nil
}

func DecodeStep(b []byte) (Step, error) {
	if len(b) > MaxWriteRequestBytes {
		return Step{}, NewRefusal("LIMIT", RefusalDetail{})
	}
	if jsonDepth(b) > MaxJSONDepth || !strictJSONUTF8(b) {
		return Step{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	var s Step
	if err := json.Unmarshal(b, &s); err != nil {
		return Step{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	if err := ValidateStep(s); err != nil {
		return Step{}, err
	}
	return s, nil
}

func ValidateStep(s Step) error {
	return validateStep(s, true)
}

func validateStep(s Step, requireNotesOp bool) error {
	bad := func(code string) error { return NewRefusal(code, RefusalDetail{}) }
	if s.Intent != nil && len(*s.Intent) > MaxIntentBytes || len(s.Result) > MaxResultBytes {
		return bad("LIMIT")
	}
	if code := idCode(s.Space); code != "" {
		return bad(code)
	}
	if !ValidDecimal(s.Epoch) || s.Entries == nil {
		return bad("REQUEST")
	}
	if (s.Op == nil) != (s.Intent == nil) {
		return bad("REQUEST")
	}
	if requireNotesOp && len(s.Notes) != 0 && s.Op == nil {
		return bad("REQUEST")
	}
	if s.Op != nil {
		if code := idCode(*s.Op); code != "" {
			return bad(code)
		}
		if !utf8.ValidString(*s.Intent) {
			return bad("REQUEST")
		}
	}
	if !utf8.ValidString(s.Result) {
		return bad("REQUEST")
	}
	if s.Fence {
		if s.Op == nil || len(s.Entries) != 0 || len(s.Notes) != 0 || s.Result != "" {
			return bad("REQUEST")
		}
		return nil
	}
	if len(s.Entries) > MaxEntries || len(s.Notes) > MaxNotes {
		return bad("LIMIT")
	}
	tables := make(map[string]bool)
	seen := make(map[string]bool)
	rows := make(map[string]bool)
	rowsetTables := make(map[string]bool)
	seenProps := make(map[string]bool)
	candidates, guards, abouts, props := 0, 0, 0, 0
	advance := false
	sawNonRowset := false
	for i, e := range s.Entries {
		if e.Kind == "rowset" {
			if sawNonRowset || rowsetTables[e.Table] {
				return NewRefusal("REQUEST", RefusalDetail{EntryIndex: ptrInt(i), Table: e.Table})
			}
			rowsetTables[e.Table] = true
		} else if !sawNonRowset {
			sawNonRowset = true
			if len(rowsetTables) != 0 && e.Kind != "advance" {
				return NewRefusal("REQUEST", RefusalDetail{EntryIndex: ptrInt(i), Table: e.Table})
			}
		}
		if e.Kind == "advance" {
			// An advance must have a stable replay identity even when it has
			// no rowset guards, restored rows, or other entries.
			if s.Op == nil {
				return NewRefusal("REQUEST", RefusalDetail{EntryIndex: ptrInt(i)})
			}
			if advance || i != len(rowsetTables) {
				return NewRefusal("REQUEST", RefusalDetail{EntryIndex: ptrInt(i)})
			}
			advance = true
		}
		if e.Kind != "advance" {
			if code := symbolicCode(e.Table); code != "" {
				return bad(code)
			}
			tables[e.Table] = true
		}
		if len(tables) > MaxTables || len(e.IDs) > MaxIDsPerEntry {
			return bad("LIMIT")
		}
		if err := validateEntry(s.Epoch, e, i); err != nil {
			return err
		}
		switch e.Kind {
		case "create", "move", "remove", "guard":
			for _, id := range e.IDs {
				key := e.Table + "\x00" + id
				if seen[key] {
					return NewRefusal("TWICE", RefusalDetail{EntryIndex: ptrInt(i), Table: e.Table, IDs: []string{id}})
				}
				seen[key] = true
			}
			if e.Kind == "guard" {
				guards += len(e.IDs)
			} else {
				candidates += len(e.IDs)
				abouts += len(e.About)
			}
		case "rows":
			for _, row := range append(append([]string{}, e.Add...), e.Del...) {
				rows[e.Table+"\x00"+row] = true
			}
		case "rowset":
			for _, row := range e.Rows {
				rows[e.Table+"\x00"+row.Row] = true
			}
		case "prop", "propguard":
			// Amendment 2026-09-30 (property), section 2: a (t,name) pair is in
			// at most one prop entry; a propguard beside it reads the pre-state.
			props++
			if e.Kind == "prop" {
				key := e.Table + "\x00" + e.Name
				if seenProps[key] {
					return NewRefusal("TWICE", RefusalDetail{EntryIndex: ptrInt(i), Table: e.Table, Name: e.Name})
				}
				seenProps[key] = true
			}
		}
	}
	if len(rowsetTables) != 0 && !advance {
		return NewRefusal("REQUEST", RefusalDetail{EntryIndex: ptrInt(0)})
	}
	for _, n := range s.Notes {
		if n.Line.Kind != "note" || n.About == nil || !validMeta(n.Line.Meta) {
			return bad("REQUEST")
		}
		abouts += len(n.About)
		distinct := make(map[string]struct{}, len(n.About))
		for _, a := range n.About {
			if code := idCode(a); code != "" {
				return bad(code)
			}
			distinct[a] = struct{}{}
		}
		if len(distinct) > MaxIDsPerLine {
			return bad("LIMIT")
		}
	}
	rowLimit := MaxRowsPerStep
	if advance {
		rowLimit = MaxRowsWithAdvance
	}
	if candidates > MaxMemberCandidates || guards > MaxGuardMembers || abouts > MaxAboutBeforeDedup || len(rows) > rowLimit || props > MaxPropEntries {
		return bad("LIMIT")
	}
	return nil
}

func validateEntry(epoch Decimal, e Entry, index int) error {
	ref := func(code string) error {
		return NewRefusal(code, RefusalDetail{EntryIndex: ptrInt(index), Table: e.Table})
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return ref("REQUEST")
	}
	var shape Entry
	if json.Unmarshal(encoded, &shape) != nil {
		return ref("REQUEST")
	}
	if len(e.Scores) > MaxIDsPerEntry || len(e.Revs) > MaxIDsPerEntry || len(e.Each) > MaxIDsPerEntry || len(e.About) > MaxIDsPerEntry || len(e.Unset) > MaxFieldsPerMember || len(e.BeforeFields) > MaxFieldsPerMember || len(e.Add) > MaxRowsWithAdvance || len(e.Del) > MaxRowsWithAdvance || len(e.Rows) > MaxRowsWithAdvance {
		return ref("LIMIT")
	}
	if e.Kind != "advance" && e.AdvanceFrom != "" || e.Kind != "count" && e.CountMax != nil || e.Kind != "rcount" && (e.ScoreMin != "" || e.ScoreMax != "" || e.AtLeast != nil || e.AtMost != nil) {
		return ref("REQUEST")
	}
	if e.Kind != "create" && e.Kind != "move" && e.Kind != "remove" && e.Kind != "guard" && (e.IDs != nil || e.Scores != nil || e.Revs != nil || e.Set != nil || e.Each != nil || e.Unset != nil || e.BeforeFields != nil || e.About != nil || len(e.Meta) > 0 || e.To != "") {
		return ref("REQUEST")
	}
	if e.Kind != "rows" && (e.Add != nil || e.Del != nil) || e.Kind != "rowset" && e.Rows != nil || e.Kind != "count" && e.Kind != "rcount" && e.Cells != nil {
		return ref("REQUEST")
	}
	if e.Kind != "move" && e.Kind != "remove" && e.Kind != "guard" && e.From != "" {
		return ref("REQUEST")
	}
	if e.Kind != "prop" && e.Kind != "propguard" && (e.Name != "" || e.Value != nil) {
		return ref("REQUEST")
	}
	if len(e.Meta) > 0 && !validMeta(e.Meta) {
		return ref("REQUEST")
	}
	switch e.Kind {
	case "create", "move", "remove", "guard":
		if len(e.IDs) == 0 {
			return ref("REQUEST")
		}
		for _, id := range e.IDs {
			if code := idCode(id); code != "" {
				return ref(code)
			}
		}
		if e.Kind == "create" {
			if e.To == "" || len(e.Scores) != len(e.IDs) || e.From != "" || e.Revs != nil || e.Unset != nil {
				return ref("REQUEST")
			}
		} else if e.From == "" || e.Kind == "guard" && (e.To != "" || e.Scores != nil || e.Set != nil || e.Each != nil || e.Unset != nil || e.About != nil || len(e.Meta) > 0) {
			return ref("REQUEST")
		}
		if e.From != "" {
			if _, _, err := ParseCellRef(e.From); err != nil {
				return ref(refusalCode(err))
			}
		}
		if e.To != "" {
			if _, _, err := ParseCellRef(e.To); err != nil {
				return ref(refusalCode(err))
			}
		}
		if e.Kind == "remove" && (e.To != "" || e.Scores != nil) {
			return ref("REQUEST")
		}
		if e.Scores != nil && len(e.Scores) != len(e.IDs) || e.Revs != nil && len(e.Revs) != len(e.IDs) || e.Each != nil && len(e.Each) != len(e.IDs) {
			return ref("REQUEST")
		}
		if e.Kind != "guard" && e.About != nil && len(e.About) != len(e.IDs) {
			return ref("REQUEST")
		}
		for _, a := range e.About {
			if code := idCode(a); code != "" {
				return ref(code)
			}
		}
		for _, score := range e.Scores {
			if !validScore(score) {
				return ref("REQUEST")
			}
		}
		for _, rev := range e.Revs {
			if !ValidDecimal(rev) {
				return ref("REQUEST")
			}
		}
		if len(e.Set) > MaxFieldsPerMember || len(e.Unset) > MaxFieldsPerMember || len(e.BeforeFields) > MaxFieldsPerMember {
			return ref("LIMIT")
		}
		for _, field := range sortedFieldNames(e.Set) {
			value := e.Set[field]
			if reservedField(field) {
				return ref("FIELDNAME")
			}
			if code := idCode(field); code != "" {
				return ref(code)
			}
			if len(value) > MaxFieldValueBytes {
				return ref("LIMIT")
			}
			if !utf8.ValidString(value) {
				return ref("REQUEST")
			}
		}
		for _, each := range e.Each {
			if len(each) > MaxFieldsPerMember {
				return ref("LIMIT")
			}
			if each == nil {
				return ref("REQUEST")
			}
			for _, field := range sortedFieldNames(each) {
				value := each[field]
				if reservedField(field) {
					return ref("FIELDNAME")
				}
				if code := idCode(field); code != "" {
					return ref(code)
				}
				if len(value) > MaxFieldValueBytes {
					return ref("LIMIT")
				}
				if !utf8.ValidString(value) {
					return ref("REQUEST")
				}
			}
		}
		for _, field := range append(append([]string{}, e.Unset...), e.BeforeFields...) {
			if reservedField(field) {
				return ref("FIELDNAME")
			}
			if code := idCode(field); code != "" {
				return ref(code)
			}
		}
		unset := make(map[string]bool, len(e.Unset))
		for _, field := range e.Unset {
			unset[field] = true
		}
		for i, id := range e.IDs {
			effective := make(map[string]bool, len(e.Set))
			for field := range e.Set {
				effective[field] = true
			}
			if e.Each != nil {
				for field := range e.Each[i] {
					effective[field] = true
				}
			}
			if len(effective) > MaxFieldsPerMember {
				return NewRefusal("LIMIT", RefusalDetail{EntryIndex: ptrInt(index), Table: e.Table, IDs: []string{id}})
			}
			for field := range effective {
				if unset[field] {
					return NewRefusal("FIELDOVERLAP", RefusalDetail{EntryIndex: ptrInt(index), Table: e.Table, IDs: []string{id}})
				}
			}
		}
	case "prop", "propguard":
		// Amendment 2026-09-30 (property), sections 1 and 2: a name is an
		// identifier; a value is bounded and charged as a field value.
		if code := symbolicCode(e.Name); code != "" {
			return ref(code)
		}
		if e.Kind == "prop" && e.Value == nil {
			return ref("REQUEST")
		}
		if e.Value != nil {
			if len(*e.Value) > MaxFieldValueBytes {
				return ref("LIMIT")
			}
			if !utf8.ValidString(*e.Value) {
				return ref("REQUEST")
			}
		}
	case "rows":
		if e.Add == nil && e.Del == nil {
			return ref("REQUEST")
		}
		for _, row := range append(append([]string{}, e.Add...), e.Del...) {
			if code := rowCode(row); code != "" {
				return ref(code)
			}
		}
	case "rowset":
		if e.Rows == nil {
			return ref("REQUEST")
		}
		seenRows := make(map[string]bool, len(e.Rows))
		for _, row := range e.Rows {
			if code := rowCode(row.Row); code != "" {
				return ref(code)
			}
			if seenRows[row.Row] || !ValidDecimal(row.Rank) {
				return ref("REQUEST")
			}
			seenRows[row.Row] = true
			rank, _ := strconv.ParseUint(string(row.Rank), 10, 64)
			if rank > 9007199254740991 {
				return ref("REQUEST")
			}
		}
	case "advance":
		if !ValidDecimal(e.AdvanceFrom) {
			return ref("REQUEST")
		}
		if _, err := NextDecimal(e.AdvanceFrom); err != nil {
			return err
		}
	case "count":
		if len(e.Cells) > 20000 || len(e.CountMax) > 20000 {
			return ref("LIMIT")
		}
		if len(e.Cells) == 0 || len(e.Cells) != len(e.CountMax) {
			return ref("REQUEST")
		}
		for _, cell := range e.Cells {
			if _, _, err := ParseCellRef(cell); err != nil {
				return ref(refusalCode(err))
			}
		}
		for _, max := range e.CountMax {
			if max > 9007199254740991 {
				return ref("REQUEST")
			}
		}
	case "rcount":
		if len(e.Cells) > 20000 {
			return ref("LIMIT")
		}
		if len(e.Cells) == 0 || !validScoreBound(e.ScoreMin) || !validScoreBound(e.ScoreMax) || e.AtLeast == nil && e.AtMost == nil {
			return ref("REQUEST")
		}
		if e.AtLeast != nil && *e.AtLeast > 9007199254740991 || e.AtMost != nil && *e.AtMost > 9007199254740991 {
			return ref("REQUEST")
		}
		if e.AtLeast != nil && e.AtMost != nil && *e.AtLeast > *e.AtMost {
			return ref("REQUEST")
		}
		for _, cell := range e.Cells {
			if _, _, err := ParseCellRef(cell); err != nil {
				return ref(refusalCode(err))
			}
		}
	default:
		return ref("REQUEST")
	}
	return nil
}

func validField(s string) bool {
	return validID(s) && !reservedField(s)
}

func sortedFieldNames(fields map[string]string) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func reservedField(s string) bool {
	return s == "epoch" || s == "revision" || strings.HasPrefix(s, "place:")
}

func ptrInt(i int) *int { return &i }

func validMeta(raw json.RawMessage) bool {
	if len(raw) == 0 || !strictJSONUTF8(raw) {
		return false
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&value) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return false
	}
	_, ok := value.(map[string]any)
	return ok && jsonDepth(raw) <= MaxJSONDepth && finiteMetaNumbers(value)
}

func finiteMetaNumbers(value any) bool {
	switch v := value.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		return !math.IsInf(f, 0) && !math.IsNaN(f) && (err == nil || errors.Is(err, strconv.ErrRange))
	case map[string]any:
		for _, child := range v {
			if !finiteMetaNumbers(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !finiteMetaNumbers(child) {
				return false
			}
		}
	}
	return true
}

func jsonDepth(data []byte) int {
	depth, maxDepth := 0, 0
	inString, escaped := false, false
	for _, b := range data {
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maxDepth
}

// strictJSONUTF8 rejects bytes and escaped surrogate halves that encoding/json
// otherwise replaces with U+FFFD. Duplicate names still use last-wins JSON
// object semantics after this lexical check.
func strictJSONUTF8(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}

func strictObject(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return nil, errors.New("expected object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allow[name] = true
	}
	for name := range obj {
		if !allow[name] {
			return nil, fmt.Errorf("unknown property %q", name)
		}
	}
	return obj, nil
}

func canonicalMeta(raw json.RawMessage) (json.RawMessage, error) {
	if !validMeta(raw) {
		return nil, errors.New("metadata must be a JSON object")
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	return json.RawMessage(b), err
}

func (s Step) MarshalJSON() ([]byte, error) {
	m := map[string]any{"epoch": s.Epoch, "space": s.Space, "entries": s.Entries}
	if s.Fence {
		m["fence"] = true
	}
	if s.Op != nil {
		m["op"] = *s.Op
	}
	if s.Intent != nil {
		m["intent"] = *s.Intent
	}
	if s.Result != "" {
		m["result"] = s.Result
	}
	if s.Notes != nil {
		m["notes"] = s.Notes
	}
	return json.Marshal(m)
}

func (s *Step) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "epoch", "space", "op", "intent", "result", "entries", "notes", "fence")
	if err != nil {
		return err
	}
	*s = Step{}
	if err := unmarshalRequired(m, "epoch", &s.Epoch); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "space", &s.Space); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "entries", &s.Entries); err != nil {
		return err
	}
	if raw, ok := m["op"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || string(raw) == "null" {
			return errors.New("op must be a string")
		}
		s.Op = &v
	}
	if raw, ok := m["intent"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || string(raw) == "null" {
			return errors.New("intent must be a string")
		}
		s.Intent = &v
	}
	if raw, ok := m["result"]; ok {
		if err := json.Unmarshal(raw, &s.Result); err != nil || string(raw) == "null" {
			return errors.New("result must be a string")
		}
	}
	if raw, ok := m["notes"]; ok {
		if err := json.Unmarshal(raw, &s.Notes); err != nil || s.Notes == nil {
			return errors.New("notes must be an array")
		}
	}
	if raw, ok := m["fence"]; ok {
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
			return errors.New("fence must be literal true")
		}
		s.Fence = true
	}
	return nil
}

func unmarshalRequired(m map[string]json.RawMessage, name string, out any) error {
	raw, ok := m[name]
	if !ok || string(raw) == "null" {
		return fmt.Errorf("missing %s", name)
	}
	return json.Unmarshal(raw, out)
}

// Lua's bounded integer predicates accept JSON numbers such as 1.0 and 1e0.
// Decode their mathematical value exactly with work bounded by the request
// length. No big rational or exponent-sized allocation is needed: after
// trailing decimal zeros are removed, an integral int64 has at most 19 digits.
// Decimal-string epochs, revisions and sequences use their separate grammar.
func integralJSON(raw json.RawMessage, min, max int64) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9') {
		return 0, errors.New("expected JSON number")
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return 0, err
	}
	s := string(number)
	mantissa, exponent := s, 0
	if at := strings.IndexAny(s, "eE"); at >= 0 {
		mantissa = s[:at]
		exponent = boundedDecimalExponent(s[at+1:], len(s)+20)
	}
	negative := strings.HasPrefix(mantissa, "-")
	if negative {
		mantissa = mantissa[1:]
	}
	fractional := 0
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		fractional = len(mantissa) - dot - 1
	}
	first, last, digits := -1, -1, 0
	for i := 0; i < len(mantissa); i++ {
		if mantissa[i] == '.' {
			continue
		}
		if mantissa[i] != '0' {
			if first < 0 {
				first = digits
			}
			last = digits
		}
		digits++
	}
	if first < 0 { // Every spelling of zero is integral, even 0e<huge>.
		if min <= 0 && max >= 0 {
			return 0, nil
		}
		return 0, errors.New("zero outside accepted range")
	}
	scale := exponent - fractional + digits - 1 - last
	count := last - first + 1
	if scale < 0 || count+scale > 19 {
		return 0, errors.New("integer outside accepted range")
	}
	var magnitude uint64
	for i, position := 0, 0; i < len(mantissa); i++ {
		if mantissa[i] == '.' {
			continue
		}
		if position >= first && position <= last {
			magnitude = magnitude*10 + uint64(mantissa[i]-'0')
		}
		position++
	}
	for i := 0; i < scale; i++ {
		magnitude *= 10
	}
	var value int64
	if negative {
		if magnitude > uint64(1)<<63 {
			return 0, errors.New("integer outside accepted range")
		}
		if magnitude == uint64(1)<<63 {
			value = math.MinInt64
		} else {
			value = -int64(magnitude)
		}
	} else {
		if magnitude > math.MaxInt64 {
			return 0, errors.New("integer outside accepted range")
		}
		value = int64(magnitude)
	}
	if value < min || value > max {
		return 0, errors.New("integer outside accepted range")
	}
	return value, nil
}

func boundedDecimalExponent(text string, ceiling int) int {
	sign := 1
	if strings.HasPrefix(text, "-") {
		sign, text = -1, text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	value := 0
	for i := 0; i < len(text); i++ {
		if value >= ceiling {
			continue
		}
		value = value*10 + int(text[i]-'0')
		if value > ceiling {
			value = ceiling
		}
	}
	return sign * value
}

func stringObjectJSON(raw json.RawMessage) (map[string]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("expected string object")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values))
	for field, value := range values {
		v := bytes.TrimSpace(value)
		if len(v) == 0 || v[0] != '"' {
			return nil, fmt.Errorf("field %q must be a string", field)
		}
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			return nil, err
		}
		out[field] = text
	}
	return out, nil
}

func (e Entry) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": e.Kind}
	if e.Table != "" {
		m["t"] = e.Table
	}
	if e.Kind == "advance" {
		m["from"] = e.AdvanceFrom
	} else if e.From != "" {
		m["from"] = e.From
	}
	if e.To != "" {
		m["to"] = e.To
	}
	if e.IDs != nil {
		m["ids"] = e.IDs
	}
	if e.Scores != nil {
		m["scores"] = e.Scores
	}
	if e.Revs != nil {
		m["revs"] = e.Revs
	}
	if e.Set != nil {
		m["set"] = e.Set
	}
	if e.Each != nil {
		m["each"] = e.Each
	}
	if e.Unset != nil {
		m["unset"] = e.Unset
	}
	if e.BeforeFields != nil {
		m["before_fields"] = e.BeforeFields
	}
	if e.About != nil {
		m["about"] = e.About
	}
	if e.Meta != nil {
		meta, err := canonicalMeta(e.Meta)
		if err != nil {
			return nil, err
		}
		m["meta"] = meta
	}
	if e.Add != nil {
		m["add"] = e.Add
	}
	if e.Del != nil {
		m["del"] = e.Del
	}
	if e.Kind == "rowset" {
		m["rows"] = e.Rows
	}
	if e.Cells != nil {
		m["cells"] = e.Cells
	}
	if e.Name != "" {
		m["name"] = e.Name
	}
	if e.Value != nil {
		m["value"] = *e.Value
	}
	if e.Kind == "count" {
		m["max"] = e.CountMax
	} else if e.Kind == "rcount" {
		m["min"] = e.ScoreMin
		m["max"] = e.ScoreMax
		if e.AtLeast != nil {
			m["atleast"] = *e.AtLeast
		}
		if e.AtMost != nil {
			m["atmost"] = *e.AtMost
		}
	}
	return json.Marshal(m)
}

// RowRank appears both in row reads and in rowset guards. On the request
// boundary each guard item must carry exactly these two string properties.
func (r *RowRank) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "row", "rank")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "row", &r.Row); err != nil {
		return err
	}
	return unmarshalRequired(m, "rank", &r.Rank)
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "kind", "t", "from", "to", "ids", "scores", "revs", "set", "each", "unset", "before_fields", "about", "meta", "add", "del", "rows", "cells", "min", "max", "atleast", "atmost", "name", "value")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "kind", &e.Kind); err != nil {
		return err
	}
	allowed := map[string]string{
		"create":  "kind t to ids scores set each before_fields about meta",
		"move":    "kind t from to ids scores revs set each unset before_fields about meta",
		"remove":  "kind t from ids revs set each unset before_fields about meta",
		"guard":   "kind t from ids revs before_fields",
		"rows":    "kind t add del",
		"rowset":  "kind t rows",
		"advance": "kind from",
		"count":   "kind t cells max",
		"rcount":  "kind t cells min max atleast atmost",
		// Amendment 2026-09-30 (property), section 2.
		"prop":      "kind t name value",
		"propguard": "kind t name value",
	}[e.Kind]
	if allowed == "" {
		return errors.New("unknown entry kind")
	}
	permit := make(map[string]bool)
	for _, key := range strings.Fields(allowed) {
		permit[key] = true
	}
	for key := range m {
		if !permit[key] {
			return fmt.Errorf("unknown property %q for %s", key, e.Kind)
		}
	}
	if e.Kind == "rcount" {
		if _, ok := m["min"]; !ok {
			return errors.New("missing min")
		}
		if _, ok := m["max"]; !ok {
			return errors.New("missing max")
		}
	}
	if e.Kind == "rowset" {
		raw, ok := m["rows"]
		if !ok || string(raw) == "null" || json.Unmarshal(raw, &e.Rows) != nil || e.Rows == nil {
			return errors.New("rowset rows must be an array")
		}
	}
	for name, out := range map[string]any{"t": &e.Table, "to": &e.To, "ids": &e.IDs, "scores": &e.Scores, "revs": &e.Revs, "unset": &e.Unset, "before_fields": &e.BeforeFields, "about": &e.About, "add": &e.Add, "del": &e.Del, "cells": &e.Cells} {
		if raw, ok := m[name]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, out) != nil {
				return fmt.Errorf("invalid %s", name)
			}
		}
	}
	if raw, ok := m["set"]; ok {
		e.Set, err = stringObjectJSON(raw)
		if err != nil {
			return fmt.Errorf("invalid set: %w", err)
		}
	}
	if raw, ok := m["each"]; ok {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || values == nil {
			return errors.New("each must be an array")
		}
		e.Each = make([]map[string]string, len(values))
		for i, value := range values {
			e.Each[i], err = stringObjectJSON(value)
			if err != nil {
				return fmt.Errorf("invalid each[%d]: %w", i, err)
			}
		}
	}
	if raw, ok := m["from"]; ok {
		if string(raw) == "null" {
			return errors.New("from must not be null")
		}
		if e.Kind == "advance" {
			if err := json.Unmarshal(raw, &e.AdvanceFrom); err != nil {
				return err
			}
		} else if err := json.Unmarshal(raw, &e.From); err != nil {
			return err
		}
	}
	if raw, ok := m["min"]; ok {
		if string(raw) == "null" {
			return errors.New("min must not be null")
		}
		if err := json.Unmarshal(raw, &e.ScoreMin); err != nil {
			return err
		}
	}
	if raw, ok := m["max"]; ok {
		if string(raw) == "null" {
			return errors.New("max must not be null")
		}
		if e.Kind == "count" {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return err
			}
			if values == nil {
				return errors.New("max must be an array")
			}
			e.CountMax = make([]uint64, len(values))
			for i, value := range values {
				n, err := integralJSON(value, 0, math.MaxInt64)
				if err != nil {
					return fmt.Errorf("invalid max[%d]: %w", i, err)
				}
				e.CountMax[i] = uint64(n)
			}
		} else if err := json.Unmarshal(raw, &e.ScoreMax); err != nil {
			return err
		}
	}
	for name, target := range map[string]**uint64{"atleast": &e.AtLeast, "atmost": &e.AtMost} {
		if raw, ok := m[name]; ok {
			n, err := integralJSON(raw, 0, math.MaxInt64)
			if err != nil {
				return fmt.Errorf("invalid %s: %w", name, err)
			}
			value := uint64(n)
			*target = &value
		}
	}
	if raw, ok := m["meta"]; ok {
		e.Meta, err = canonicalMeta(raw)
		if err != nil {
			return err
		}
	}
	if raw, ok := m["name"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &e.Name) != nil {
			return errors.New("invalid name")
		}
	}
	if raw, ok := m["value"]; ok {
		var value string
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return errors.New("invalid value")
		}
		e.Value = &value
	}
	return nil
}

func (n NoteLine) MarshalJSON() ([]byte, error) {
	meta, err := canonicalMeta(n.Meta)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"kind": n.Kind, "meta": meta})
}

func (n *NoteLine) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "kind", "meta")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "kind", &n.Kind); err != nil {
		return err
	}
	raw, ok := m["meta"]
	if !ok {
		return errors.New("note metadata required")
	}
	n.Meta, err = canonicalMeta(raw)
	return err
}

func (n Note) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Line  NoteLine `json:"line"`
		About []string `json:"about"`
	}{n.Line, n.About})
}

func (n *Note) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "line", "about")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "line", &n.Line); err != nil {
		return err
	}
	return unmarshalRequired(m, "about", &n.About)
}

func EncodeReadPlan(p ReadPlan) ([]byte, error) {
	if err := ValidateReadPlan(p); err != nil {
		return nil, err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	if len(b) > MaxReadRequestBytes {
		return nil, NewRefusal("LIMIT", RefusalDetail{})
	}
	if jsonDepth(b) > MaxJSONDepth {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	return b, nil
}

func DecodeReadPlan(b []byte) (ReadPlan, error) {
	if len(b) > MaxReadRequestBytes {
		return ReadPlan{}, NewRefusal("LIMIT", RefusalDetail{})
	}
	if jsonDepth(b) > MaxJSONDepth || !strictJSONUTF8(b) {
		return ReadPlan{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	var p ReadPlan
	if err := json.Unmarshal(b, &p); err != nil {
		return ReadPlan{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	if err := ValidateReadPlan(p); err != nil {
		return ReadPlan{}, err
	}
	return p, nil
}

func ValidateReadPlan(p ReadPlan) error {
	bad := func(code string) error { return NewRefusal(code, RefusalDetail{}) }
	if code := idCode(p.Space); code != "" {
		return bad(code)
	}
	if !ValidDecimal(p.Epoch) || p.Queries == nil || len(p.Queries) == 0 {
		return bad("REQUEST")
	}
	if len(p.Queries) > MaxQueries {
		return bad("LIMIT")
	}
	if p.Mode != "" && p.Mode != "atomic" && p.Mode != "page" {
		return bad("REQUEST")
	}
	if p.Mode == "page" && (len(p.Queries) != 1 || p.Queries[0].Kind != "lines" && p.Queries[0].Kind != "cardlines") {
		return bad("REQUEST")
	}
	for i, q := range p.Queries {
		fail := func(code string) error { return NewRefusal(code, RefusalDetail{QueryIndex: ptrInt(i)}) }
		encoded, err := json.Marshal(q)
		if err != nil {
			return fail("REQUEST")
		}
		var shape ReadQuery
		if json.Unmarshal(encoded, &shape) != nil {
			return fail("REQUEST")
		}
		if len(q.Fields) > MaxFieldsPerMember || len(q.Cells) > 20000 {
			return fail("LIMIT")
		}
		if q.Table != "" {
			if code := symbolicCode(q.Table); code != "" {
				return fail(code)
			}
		}
		for _, field := range q.Fields {
			if reservedField(field) {
				return fail("FIELDNAME")
			}
			if code := idCode(field); code != "" {
				return fail(code)
			}
		}
		switch q.Kind {
		case "range":
			if q.Limit > 2000 {
				return fail("LIMIT")
			}
			if q.Limit < 1 || !validScoreBound(q.Min) || !validScoreBound(q.Max) {
				return fail("REQUEST")
			}
			if q.Key != "" {
				if q.Table != "" || q.Cell != "" || q.Records || q.Fields != nil || !strings.HasPrefix(q.Key, p.Space) {
					return fail("REQUEST")
				}
			} else if q.Table == "" || q.Cell == "" {
				return fail("REQUEST")
			}
			if q.Fields != nil && !q.Records {
				return fail("REQUEST")
			}
			if q.Cell != "" {
				if _, _, err := ParseCellRef(q.Cell); err != nil {
					return fail(refusalCode(err))
				}
			}
		case "count", "rcount":
			if q.Table == "" || len(q.Cells) == 0 {
				return fail("REQUEST")
			}
			if q.Kind == "rcount" && (!validScoreBound(q.Min) || !validScoreBound(q.Max)) {
				return fail("REQUEST")
			}
			for _, cell := range q.Cells {
				if _, _, err := ParseCellRef(cell); err != nil {
					return fail(refusalCode(err))
				}
			}
		case "ids":
			if len(q.IDs) > 10000 {
				return fail("LIMIT")
			}
			if q.Table == "" || q.IDs == nil || len(q.IDs) == 0 {
				return fail("REQUEST")
			}
			for _, id := range q.IDs {
				if code := idCode(id); code != "" {
					return fail(code)
				}
			}
		case "rows":
			if q.Table == "" {
				return fail("REQUEST")
			}
		case "props":
			// Amendment 2026-09-30 (property), section 3: an optional list of
			// distinct property names, at most one table's 64.
			if len(q.Names) > MaxPropsPerTable {
				return fail("LIMIT")
			}
			if q.Table == "" {
				return fail("REQUEST")
			}
			seenNames := make(map[string]bool, len(q.Names))
			for _, name := range q.Names {
				if code := symbolicCode(name); code != "" {
					return fail(code)
				}
				if seenNames[name] {
					return fail("REQUEST")
				}
				seenNames[name] = true
			}
		case "done":
			if len(q.Ops) > 2000 {
				return fail("LIMIT")
			}
			if q.Ops == nil || len(q.Ops) == 0 {
				return fail("REQUEST")
			}
			seenOps := make(map[DoneIdentity]bool)
			for _, op := range q.Ops {
				if code := idCode(op.Op); code != "" {
					return fail(code)
				}
				if !ValidDecimal(op.Epoch) || len(op.IntentDigest) != 40 || strings.Trim(op.IntentDigest, "0123456789abcdef") != "" || seenOps[op] {
					return fail("REQUEST")
				}
				seenOps[op] = true
			}
		case "last":
		case "lines":
			if q.Cursor != nil {
				return fail("REQUEST")
			}
			if q.Limit > 5000 || q.IDsLimit > 200000 {
				return fail("LIMIT")
			}
			if !ValidDecimal(q.AfterSeq) || q.Limit < 1 || q.IDsLimit < 0 {
				return fail("REQUEST")
			}
			if q.ThroughSeq != nil && !ValidDecimal(*q.ThroughSeq) {
				return fail("REQUEST")
			}
		case "cardlines":
			if len(q.Abouts) > 2000 || q.Limit > 500 {
				return fail("LIMIT")
			}
			if q.Abouts == nil || len(q.Abouts) == 0 || q.Limit < 1 {
				return fail("REQUEST")
			}
			seen := make(map[string]bool)
			for _, about := range q.Abouts {
				if code := idCode(about); code != "" {
					return fail(code)
				}
				if seen[about] {
					return fail("REQUEST")
				}
				seen[about] = true
			}
			if q.Cursor != nil {
				if len(q.Cursor.Fields) > MaxFieldsPerMember || len(q.Cursor.Positions) > 2000 {
					return fail("LIMIT")
				}
				if p.Mode != "page" || !ValidDecimal(q.Cursor.Epoch) || q.Cursor.Fields == nil || len(q.Cursor.Positions) == 0 {
					return fail("REQUEST")
				}
				for _, field := range q.Cursor.Fields {
					if reservedField(field) {
						return fail("FIELDNAME")
					}
					if code := idCode(field); code != "" {
						return fail(code)
					}
				}
				for _, position := range q.Cursor.Positions {
					if position.NextIndex > 9007199254740991 || position.ThroughIndex > 9007199254740991 {
						return fail("OVERFLOW")
					}
					if code := idCode(position.About); code != "" {
						return fail(code)
					}
					if position.NextIndex < 0 || position.ThroughIndex < -1 {
						return fail("REQUEST")
					}
				}
			}
		default:
			return fail("REQUEST")
		}
	}
	return nil
}

func (p ReadPlan) MarshalJSON() ([]byte, error) {
	m := map[string]any{"epoch": p.Epoch, "space": p.Space, "queries": p.Queries}
	if p.Mode != "" {
		m["mode"] = p.Mode
	}
	return json.Marshal(m)
}

func (p *ReadPlan) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "epoch", "space", "mode", "queries")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "epoch", &p.Epoch); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "space", &p.Space); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "queries", &p.Queries); err != nil {
		return err
	}
	if raw, ok := m["mode"]; ok {
		if err := json.Unmarshal(raw, &p.Mode); err != nil || string(raw) == "null" {
			return errors.New("mode must be a string")
		}
	}
	return nil
}

func (q ReadQuery) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": q.Kind}
	if q.Table != "" {
		m["t"] = q.Table
	}
	if q.Cell != "" {
		m["cell"] = q.Cell
	}
	if q.Key != "" {
		m["key"] = q.Key
	}
	if q.Min != "" || q.Kind == "range" || q.Kind == "rcount" {
		m["min"] = q.Min
	}
	if q.Max != "" || q.Kind == "range" || q.Kind == "rcount" {
		m["max"] = q.Max
	}
	if q.Limit != 0 {
		m["limit"] = q.Limit
	}
	if q.Desc {
		m["desc"] = true
	}
	if q.Records {
		m["records"] = true
	}
	if q.Fields != nil {
		m["fields"] = q.Fields
	}
	if q.Cells != nil {
		m["cells"] = q.Cells
	}
	if q.IDs != nil {
		m["ids"] = q.IDs
	}
	if q.Ops != nil {
		m["ops"] = q.Ops
	}
	if q.Kind == "lines" {
		m["after_seq"] = q.AfterSeq
		if q.ThroughSeq != nil {
			m["through_seq"] = *q.ThroughSeq
		}
		if q.IDsLimit != 0 {
			m["ids_limit"] = q.IDsLimit
		}
	}
	if q.Abouts != nil {
		m["abouts"] = q.Abouts
	}
	if q.Cursor != nil {
		m["cursor"] = q.Cursor
	}
	if q.IncludeMeta {
		m["include_meta"] = true
	}
	if q.Names != nil {
		m["names"] = q.Names
	}
	return json.Marshal(m)
}

func (q *ReadQuery) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "kind", "t", "cell", "key", "min", "max", "limit", "desc", "records", "fields", "cells", "ids", "ops", "after_seq", "through_seq", "ids_limit", "abouts", "cursor", "include_meta", "names")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "kind", &q.Kind); err != nil {
		return err
	}
	allowed := map[string]string{
		"range":     "kind t cell key min max limit desc records fields",
		"count":     "kind t cells",
		"rcount":    "kind t cells min max",
		"ids":       "kind t ids fields",
		"rows":      "kind t",
		"done":      "kind ops",
		"last":      "kind",
		"lines":     "kind after_seq through_seq limit ids_limit",
		"cardlines": "kind abouts cursor limit fields include_meta",
		"props":     "kind t names", // amendment 2026-09-30 (property), section 3
	}[q.Kind]
	if allowed == "" {
		return errors.New("unknown query kind")
	}
	permit := make(map[string]bool)
	for _, key := range strings.Fields(allowed) {
		permit[key] = true
	}
	for key := range m {
		if !permit[key] {
			return fmt.Errorf("unknown property %q for %s", key, q.Kind)
		}
	}
	if q.Kind == "range" || q.Kind == "rcount" {
		if _, ok := m["min"]; !ok {
			return errors.New("missing min")
		}
		if _, ok := m["max"]; !ok {
			return errors.New("missing max")
		}
	}
	if q.Kind == "range" {
		if _, hasKey := m["key"]; hasKey {
			if _, hasTable := m["t"]; hasTable {
				return errors.New("raw range key cannot name a table")
			}
			if _, hasCell := m["cell"]; hasCell {
				return errors.New("raw range key cannot name a cell")
			}
			if _, hasRecords := m["records"]; hasRecords {
				return errors.New("raw range key cannot request records")
			}
			if _, hasFields := m["fields"]; hasFields {
				return errors.New("raw range key cannot request fields")
			}
		}
	}
	for name, out := range map[string]any{"t": &q.Table, "cell": &q.Cell, "key": &q.Key, "min": &q.Min, "max": &q.Max, "desc": &q.Desc, "records": &q.Records, "fields": &q.Fields, "cells": &q.Cells, "ids": &q.IDs, "ops": &q.Ops, "after_seq": &q.AfterSeq, "through_seq": &q.ThroughSeq, "abouts": &q.Abouts, "cursor": &q.Cursor, "include_meta": &q.IncludeMeta, "names": &q.Names} {
		if raw, ok := m[name]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, out) != nil {
				return fmt.Errorf("invalid %s", name)
			}
		}
	}
	for name, target := range map[string]*int{"limit": &q.Limit, "ids_limit": &q.IDsLimit} {
		if raw, ok := m[name]; ok {
			n, err := integralJSON(raw, math.MinInt64, math.MaxInt64)
			if err != nil || int64(int(n)) != n {
				return fmt.Errorf("invalid %s", name)
			}
			*target = int(n)
		}
	}
	return nil
}

func (c *CardCursor) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "epoch", "fields", "include_meta", "positions")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "epoch", &c.Epoch); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "fields", &c.Fields); err != nil || c.Fields == nil {
		return errors.New("cursor fields must be an array")
	}
	if err := unmarshalRequired(m, "include_meta", &c.IncludeMeta); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "positions", &c.Positions); err != nil || c.Positions == nil {
		return errors.New("cursor positions must be an array")
	}
	return nil
}

func (d *DoneIdentity) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "epoch", "op", "intent_digest")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "epoch", &d.Epoch); err != nil {
		return err
	}
	if err := unmarshalRequired(m, "op", &d.Op); err != nil {
		return err
	}
	return unmarshalRequired(m, "intent_digest", &d.IntentDigest)
}

func (p *CardCursorPosition) UnmarshalJSON(data []byte) error {
	m, err := strictObject(data, "about", "next_index", "through_index")
	if err != nil {
		return err
	}
	if err := unmarshalRequired(m, "about", &p.About); err != nil {
		return err
	}
	for name, target := range map[string]*int64{"next_index": &p.NextIndex, "through_index": &p.ThroughIndex} {
		raw, ok := m[name]
		if !ok {
			return fmt.Errorf("missing %s", name)
		}
		n, err := integralJSON(raw, math.MinInt64, math.MaxInt64)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
		*target = n
	}
	return nil
}

// DecodeReply and DecodeReadReply retain deliberate refusals as machine-coded
// errors. They never return a partial success envelope beside a refusal.
func validateWriteReplyShape(data []byte, replay bool) error {
	compact := []string{"status", "epoch_before", "epoch_after", "changed", "first_seq", "last_seq", "result", "replay"}
	full := []string{"status", "epoch_before", "epoch_after", "changed", "guarded", "changed_per_entry", "first_seq", "last_seq", "lines", "result", "replay", "counters"}
	fields := full
	if replay {
		fields = compact
	}
	m, err := strictObject(data, fields...)
	if err != nil || len(m) != len(fields) {
		return errors.New("tset: malformed write response fields")
	}
	for _, name := range fields {
		if _, ok := m[name]; !ok {
			return fmt.Errorf("tset: missing write response field %s", name)
		}
	}
	for _, name := range []string{"status", "epoch_before", "epoch_after", "first_seq", "last_seq", "result"} {
		var value string
		if bytes.Equal(bytes.TrimSpace(m[name]), []byte("null")) || json.Unmarshal(m[name], &value) != nil {
			return fmt.Errorf("tset: invalid write response field %s", name)
		}
	}
	for _, name := range []string{"changed", "guarded", "lines"} {
		if _, ok := m[name]; !ok {
			continue
		}
		var value int
		if bytes.Equal(bytes.TrimSpace(m[name]), []byte("null")) || json.Unmarshal(m[name], &value) != nil {
			return fmt.Errorf("tset: invalid write response field %s", name)
		}
	}
	if replay {
		if !bytes.Equal(bytes.TrimSpace(m["replay"]), []byte("true")) {
			return errors.New("tset: invalid compact replay marker")
		}
		return nil
	}
	if !bytes.Equal(bytes.TrimSpace(m["replay"]), []byte("false")) {
		return errors.New("tset: invalid fresh replay marker")
	}
	var changedPer []int
	if raw := bytes.TrimSpace(m["changed_per_entry"]); len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &changedPer) != nil {
		return errors.New("tset: invalid changed_per_entry")
	}
	var counters map[string]json.RawMessage
	if raw := bytes.TrimSpace(m["counters"]); len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &counters) != nil {
		return errors.New("tset: invalid counters")
	}
	return nil
}

func DecodeReply(data []byte) (Reply, error) {
	var head struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &head) != nil {
		return Reply{}, errors.New("tset: malformed write response")
	}
	if head.Status == "refused" {
		var r Refusal
		if json.Unmarshal(data, &r) != nil || r.Code == "" {
			return Reply{}, errors.New("tset: malformed refusal response")
		}
		return Reply{}, &r
	}
	if head.Status != "ok" && head.Status != "fenced" {
		return Reply{}, errors.New("tset: unknown write response status")
	}
	var r Reply
	if err := json.Unmarshal(data, &r); err != nil {
		return Reply{}, err
	}
	if err := validateWriteReplyShape(data, r.Replay); err != nil {
		return Reply{}, err
	}
	if !ValidDecimal(r.EpochBefore) || !ValidDecimal(r.EpochAfter) || !ValidDecimal(r.FirstSeq) || !ValidDecimal(r.LastSeq) || r.Changed < 0 || !r.Replay && (r.Guarded < 0 || r.Lines < 0) {
		return Reply{}, errors.New("tset: malformed write response integers")
	}
	if r.Status == "fenced" && (r.EpochBefore != r.EpochAfter || r.Changed != 0 || r.FirstSeq != "0" || r.LastSeq != "0" || r.Result != "" || !r.Replay && (r.Guarded != 0 || len(r.ChangedPerEntry) != 0 || r.Lines != 0)) {
		return Reply{}, errors.New("tset: malformed fenced response")
	}
	return r, nil
}

func DecodeReadReply(data []byte) (ReadReply, error) {
	var head struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &head) != nil {
		return ReadReply{}, errors.New("tset: malformed read response")
	}
	if head.Status == "refused" {
		var r Refusal
		if json.Unmarshal(data, &r) != nil || r.Code == "" {
			return ReadReply{}, errors.New("tset: malformed refusal response")
		}
		return ReadReply{}, &r
	}
	if head.Status != "read" && head.Status != "page" {
		return ReadReply{}, errors.New("tset: unknown read response status")
	}
	var r ReadReply
	if err := json.Unmarshal(data, &r); err != nil {
		return ReadReply{}, err
	}
	if !ValidDecimal(r.Epoch) || !ValidDecimal(r.ActiveEpoch) || !ValidDecimal(r.TimeMS) {
		return ReadReply{}, errors.New("tset: malformed read response integers")
	}
	return r, nil
}
