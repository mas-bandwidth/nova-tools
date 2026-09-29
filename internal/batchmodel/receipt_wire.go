package batchmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The repaired runtime emits these exact fields in one batch_delta JSON value.
// Scores and touched field endpoints are the only nullable values. The fields
// map is sparse, but its key set must equal exactly the set/unset instructions.
type wireBatchDelta struct {
	OperationID   string            `json:"operation_id"`
	Digest        string            `json:"digest"`
	Actor         string            `json:"actor"`
	SelectedCount json.Number       `json:"selected_count"`
	GuardCount    json.Number       `json:"guard_count"`
	ChangedCount  json.Number       `json:"changed_count"`
	Members       []wireMemberDelta `json:"members"`
}
type wireMemberDelta struct {
	ID          string                     `json:"id"`
	BeforePlace string                     `json:"before_place"`
	AfterPlace  string                     `json:"after_place"`
	BeforeScore *json.Number               `json:"before_score"`
	AfterScore  *json.Number               `json:"after_score"`
	BeforeRev   string                     `json:"before_rev"`
	AfterRev    string                     `json:"after_rev"`
	FieldsSet   map[string]string          `json:"fields_set"`
	FieldsUnset json.RawMessage            `json:"fields_unset"`
	Fields      map[string]wireFieldChange `json:"fields"`
}
type wireFieldChange struct {
	Before *string `json:"before"`
	After  *string `json:"after"`
}

func nullableShape(inner wireShape) wireShape {
	return func(raw json.RawMessage) error {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return inner(raw)
	}
}

func validateDeltaShape(raw []byte) error {
	str := primitiveShape('"')
	number := numberShape
	change := objectShape(map[string]wireShape{"before": nullableShape(str), "after": nullableShape(str)}, "before", "after")
	member := objectShape(map[string]wireShape{
		"id": str, "before_place": str, "after_place": str, "before_score": nullableShape(number), "after_score": nullableShape(number),
		"before_rev": str, "after_rev": str, "fields_set": stringMapShape(str), "fields_unset": emptyObjectOrArrayShape(str), "fields": stringMapShape(change),
	}, "id", "before_place", "after_place", "before_score", "after_score", "before_rev", "after_rev", "fields_set", "fields_unset", "fields")
	return objectShape(map[string]wireShape{"operation_id": str, "digest": str, "actor": str, "selected_count": number, "guard_count": number, "changed_count": number, "members": arrayShape(member)},
		"operation_id", "digest", "actor", "selected_count", "guard_count", "changed_count", "members")(raw)
}

// DecodeAcceptedReply checks the exact FCALL receipt and the complete sparse
// application-field coverage against the request and independent prestate. The
// caller separately compares this receipt with the after snapshot and stream.
// Only a new accepted reply is decoded here; replay requires its previously
// validated receipt and the durable result bytes, never today's prestate.
func DecodeAcceptedReply(reply any, q Request, before Snapshot) (*Receipt, error) {
	outer, err := replyList(reply)
	if err != nil {
		return nil, err
	}
	if len(outer) != 2 || outer[0] != "OK" {
		return nil, fmt.Errorf("not an accepted batch reply")
	}
	wire, err := replyList(outer[1])
	if err != nil {
		return nil, err
	}
	if len(wire) != 7 || wire[0] != "RECEIPT" {
		return nil, fmt.Errorf("malformed batch receipt shape")
	}
	streamID, err := replyString(wire[1])
	if err != nil {
		return nil, err
	}
	epoch, err := replyString(wire[2])
	if err != nil {
		return nil, err
	}
	beforeRev, err := replyString(wire[3])
	if err != nil {
		return nil, err
	}
	afterRev, err := replyString(wire[4])
	if err != nil {
		return nil, err
	}
	kind, err := replyString(wire[5])
	if err != nil {
		return nil, err
	}
	raw, err := replyString(wire[6])
	if err != nil {
		return nil, err
	}
	if streamID == "" || epoch != q.Epoch || (kind != "changed" && kind != "noop") {
		return nil, fmt.Errorf("receipt identity/outcome outside request")
	}
	if err := rejectDuplicateJSONKeys([]byte(raw)); err != nil {
		return nil, err
	}
	if err := validateDeltaShape([]byte(raw)); err != nil {
		return nil, err
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	var delta wireBatchDelta
	if err := d.Decode(&delta); err != nil {
		return nil, err
	}
	if delta.OperationID != q.OperationID || delta.Digest != q.Digest || delta.Actor != q.Actor {
		return nil, fmt.Errorf("batch delta identity differs from request")
	}
	selected, err := smallCount(delta.SelectedCount)
	if err != nil {
		return nil, err
	}
	guards, err := smallCount(delta.GuardCount)
	if err != nil {
		return nil, err
	}
	changed, err := smallCount(delta.ChangedCount)
	if err != nil {
		return nil, err
	}
	if selected != uint64(len(q.Selected)) || len(delta.Members) != len(q.Selected) {
		return nil, fmt.Errorf("batch selection count/coverage differs")
	}
	r := &Receipt{StreamID: streamID, OperationID: q.OperationID, Digest: q.Digest, Actor: q.Actor, Table: q.Table, Epoch: q.Epoch,
		BeforeRevision: beforeRev, AfterRevision: afterRev, Kind: kind, SelectedCount: selected, GuardCount: guards, ChangedCount: changed}
	for i, m := range delta.Members {
		if m.ID != q.Selected[i] {
			return nil, fmt.Errorf("delta member order or identity differs at %d", i)
		}
		if i >= len(q.Entries) {
			return nil, fmt.Errorf("request has no decoded entry %d", i)
		}
		observed, ok := before.Members[m.ID]
		if !ok {
			return nil, fmt.Errorf("member %s prestate missing", m.ID)
		}
		entry := q.Entries[i]
		unset, err := decodeUnset(m.FieldsUnset)
		if err != nil {
			return nil, fmt.Errorf("member %s: %w", m.ID, err)
		}
		if err := checkSparseFields(m, unset, entry, observed); err != nil {
			return nil, fmt.Errorf("member %s: %w", m.ID, err)
		}
		b, err := wireMemberImage(m.BeforePlace, m.BeforeScore, m.BeforeRev, observed.Exists, observed.Epoch, observed.Fields)
		if err != nil {
			return nil, err
		}
		if !equalMember(b, observed) {
			return nil, fmt.Errorf("member %s wire before image disagrees with prestate", m.ID)
		}
		afterFields := cloneMember(observed).Fields
		if afterFields == nil {
			afterFields = map[string]string{}
		}
		for field, change := range m.Fields {
			if change.After == nil {
				delete(afterFields, field)
			} else {
				afterFields[field] = *change.After
			}
		}
		a, err := wireMemberImage(m.AfterPlace, m.AfterScore, m.AfterRev, true, q.Epoch, afterFields)
		if err != nil {
			return nil, err
		}
		r.Members = append(r.Members, Delta{ID: m.ID, Before: b, After: a})
	}
	return r, nil
}

func smallCount(raw json.Number) (uint64, error) {
	n, err := decimal(string(raw))
	if err != nil || n > 3 {
		return 0, fmt.Errorf("receipt count %q outside finite bound", raw)
	}
	return n, nil
}

func wireMemberImage(place string, score *json.Number, revision string, exists bool, epoch string, fields map[string]string) (Member, error) {
	if _, err := decimal(revision); err != nil {
		return Member{}, err
	}
	m := Member{Exists: exists, Epoch: epoch, Revision: revision, Fields: map[string]string{}}
	for f, v := range fields {
		m.Fields[f] = v
	}
	if !exists {
		if place != "" || score != nil || len(fields) != 0 {
			return Member{}, fmt.Errorf("absent member has place, score or fields")
		}
		m.Epoch = ""
		return m, nil
	}
	if place == "" {
		if score != nil {
			return Member{}, fmt.Errorf("unplaced member has score")
		}
		return m, nil
	}
	parts := strings.Split(place, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Member{}, fmt.Errorf("invalid receipt place %q", place)
	}
	if score == nil {
		return Member{}, fmt.Errorf("placed member lacks score")
	}
	n, err := modelScoreNumber(*score)
	if err != nil {
		return Member{}, err
	}
	m.Place = &Place{Row: parts[0], Column: parts[1], Score: strconv.FormatUint(n, 10)}
	return m, nil
}

func modelScoreNumber(raw json.Number) (uint64, error) {
	// The finite runtime fixture only admits numeric values 1 or 2; accept
	// equivalent JSON decimal spellings without converting through float64.
	s := string(raw)
	if s == "1" || s == "1.0" || s == "1.00" {
		return 1, nil
	}
	if s == "2" || s == "2.0" || s == "2.00" {
		return 2, nil
	}
	return 0, fmt.Errorf("receipt score %q outside finite model", raw)
}

func checkSparseFields(w wireMemberDelta, unset []string, entry MemberAction, before Member) error {
	want := map[string]bool{}
	if entry.SetField != "" {
		want[entry.SetField] = true
	}
	if entry.UnsetField != "" {
		want[entry.UnsetField] = true
	}
	if len(w.Fields) != len(want) {
		return fmt.Errorf("sparse field delta omits or adds touched field")
	}
	if len(w.FieldsSet) != (boolInt(entry.SetField != "")) || len(unset) != (boolInt(entry.UnsetField != "")) {
		return fmt.Errorf("set/unset instruction echo differs")
	}
	if entry.SetField != "" && w.FieldsSet[entry.SetField] != entry.SetValue {
		return fmt.Errorf("set instruction value differs")
	}
	if entry.UnsetField != "" && (len(unset) != 1 || unset[0] != entry.UnsetField) {
		return fmt.Errorf("unset instruction differs")
	}
	for field := range w.FieldsSet {
		if field != entry.SetField {
			return fmt.Errorf("unexpected set field %s", field)
		}
	}
	for field, change := range w.Fields {
		if !want[field] {
			return fmt.Errorf("untouched field %s included", field)
		}
		prior, exists := before.Fields[field]
		if exists != (change.Before != nil) || exists && prior != *change.Before {
			return fmt.Errorf("field %s before value disagrees with prestate", field)
		}
		if field == entry.SetField {
			if change.After == nil || *change.After != entry.SetValue {
				return fmt.Errorf("field %s after set differs", field)
			}
		} else if change.After != nil {
			return fmt.Errorf("field %s unset remained present", field)
		}
	}
	return nil
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Lua cjson encodes an empty table as {}, even when the populated value is an
// array. The table client accepts that exact empty spelling. A populated
// object is never accepted as a list of unset fields.
func emptyObjectOrArrayShape(item wireShape) wireShape {
	array := arrayShape(item)
	return func(raw json.RawMessage) error {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
			return nil
		}
		return array(raw)
	}
}

func decodeUnset(raw json.RawMessage) ([]string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
