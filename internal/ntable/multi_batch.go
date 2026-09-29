package ntable

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

const FnApplyMulti = "ns_table_apply_multi"

// MultiBatchReceipt identifies the full receipt in the scope stream. Replay
// means the same exact request was found in the scope's durable operation log.
type MultiBatchReceipt struct {
	ID     string
	Replay bool
	Delta  *MultiBatchDelta
}

type MultiBatchDelta struct {
	Schema        int                     `json:"schema"`
	Scope         string                  `json:"scope"`
	OperationID   string                  `json:"operation_id"`
	Digest        string                  `json:"digest"`
	Actor         string                  `json:"actor"`
	Outcome       string                  `json:"outcome"`
	Tables        []MultiBatchTableDelta  `json:"tables"`
	SelectedCount int                     `json:"selected_count"`
	GuardCount    int                     `json:"guard_count"`
	ChangedCount  int                     `json:"changed_count"`
	Members       []MultiBatchMemberDelta `json:"members"`
}

type MultiBatchTableDelta struct {
	Name      string `json:"name"`
	Epoch     string `json:"epoch"`
	RevBefore string `json:"rev_before"`
	RevAfter  string `json:"rev_after"`
}

type MultiBatchMemberDelta struct {
	RecordTable string                     `json:"record_table"`
	ID          string                     `json:"id"`
	BeforeRev   string                     `json:"before_rev"`
	AfterRev    string                     `json:"after_rev"`
	FieldsSet   map[string]string          `json:"fields_set"`
	FieldsUnset []string                   `json:"fields_unset"`
	Fields      map[string]FieldChange     `json:"fields"`
	Placements  []MultiBatchPlacementDelta `json:"placements,omitempty"`
}

type MultiBatchPlacementDelta struct {
	Table           string   `json:"table"`
	BeforePlace     string   `json:"before_place"`
	AfterPlace      string   `json:"after_place"`
	BeforeScoreText *string  `json:"before_score"`
	AfterScoreText  *string  `json:"after_score"`
	BeforeScore     *float64 `json:"-"`
	AfterScore      *float64 `json:"-"`
}

// cjson may encode empty Lua tables as [] or {}. Both mean an empty field map
// or slice in a receipt, while nonempty values retain their exact JSON shape.
func multiEmpty(raw json.RawMessage) bool {
	s := string(raw)
	return s == "" || s == "null" || s == "[]" || s == "{}"
}

func (d *MultiBatchDelta) UnmarshalJSON(data []byte) error {
	type alias MultiBatchDelta
	var raw struct {
		*alias
		Tables  json.RawMessage `json:"tables"`
		Members json.RawMessage `json:"members"`
	}
	raw.alias = (*alias)(d)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if multiEmpty(raw.Tables) {
		d.Tables = []MultiBatchTableDelta{}
	} else if err := json.Unmarshal(raw.Tables, &d.Tables); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	if multiEmpty(raw.Members) {
		d.Members = []MultiBatchMemberDelta{}
	} else if err := json.Unmarshal(raw.Members, &d.Members); err != nil {
		return fmt.Errorf("members: %w", err)
	}
	return nil
}

func (d *MultiBatchMemberDelta) UnmarshalJSON(data []byte) error {
	type alias MultiBatchMemberDelta
	var raw struct {
		*alias
		FieldsSet   json.RawMessage `json:"fields_set"`
		FieldsUnset json.RawMessage `json:"fields_unset"`
		Fields      json.RawMessage `json:"fields"`
		Placements  json.RawMessage `json:"placements"`
	}
	raw.alias = (*alias)(d)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if multiEmpty(raw.FieldsSet) {
		d.FieldsSet = map[string]string{}
	} else if err := json.Unmarshal(raw.FieldsSet, &d.FieldsSet); err != nil {
		return fmt.Errorf("fields_set: %w", err)
	}
	if multiEmpty(raw.FieldsUnset) {
		d.FieldsUnset = []string{}
	} else if err := json.Unmarshal(raw.FieldsUnset, &d.FieldsUnset); err != nil {
		return fmt.Errorf("fields_unset: %w", err)
	}
	if multiEmpty(raw.Fields) {
		d.Fields = map[string]FieldChange{}
	} else if err := json.Unmarshal(raw.Fields, &d.Fields); err != nil {
		return fmt.Errorf("fields: %w", err)
	}
	if multiEmpty(raw.Placements) {
		d.Placements = []MultiBatchPlacementDelta{}
	} else if err := json.Unmarshal(raw.Placements, &d.Placements); err != nil {
		return fmt.Errorf("placements: %w", err)
	}
	return nil
}

func (d *MultiBatchPlacementDelta) UnmarshalJSON(data []byte) error {
	type alias MultiBatchPlacementDelta
	if err := json.Unmarshal(data, (*alias)(d)); err != nil {
		return err
	}
	var err error
	if d.BeforeScore, err = parseScoreText(d.BeforeScoreText); err != nil {
		return err
	}
	if d.AfterScore, err = parseScoreText(d.AfterScoreText); err != nil {
		return err
	}
	return nil
}

func parseMultiBatchReply(reply []any) (MultiBatchReceipt, error) {
	if len(reply) < 2 || fmt.Sprint(reply[0]) != "OK" {
		return MultiBatchReceipt{}, fmt.Errorf("malformed multi batch reply")
	}
	w, ok := reply[1].([]any)
	if !ok || len(w) != 3 || fmt.Sprint(w[0]) != "MULTI_RECEIPT" {
		return MultiBatchReceipt{}, fmt.Errorf("malformed committed multi receipt")
	}
	id := fmt.Sprint(w[1])
	ms, seq, ok := strings.Cut(id, "-")
	if !ok || !uintString(ms) || !uintString(seq) {
		return MultiBatchReceipt{}, fmt.Errorf("missing multi receipt stream id")
	}
	r := MultiBatchReceipt{ID: id}
	if len(reply) > 2 {
		if len(reply) != 3 || fmt.Sprint(reply[2]) != "REPLAY" {
			return MultiBatchReceipt{}, fmt.Errorf("malformed multi replay marker")
		}
		r.Replay = true
	}
	rawDelta := []byte(fmt.Sprint(w[2]))
	if err := multiReceiptShape(rawDelta); err != nil {
		return MultiBatchReceipt{}, fmt.Errorf("malformed multi batch delta: %w", err)
	}
	var delta MultiBatchDelta
	if err := json.Unmarshal(rawDelta, &delta); err != nil {
		return MultiBatchReceipt{}, fmt.Errorf("decode multi batch delta: %w", err)
	}
	if delta.Schema != 2 {
		return MultiBatchReceipt{}, fmt.Errorf("multi receipt schema %d, want 2", delta.Schema)
	}
	r.Delta = &delta
	return r, nil
}

func multiReceiptObject(raw json.RawMessage, required ...string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, fmt.Errorf("expected receipt object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	for _, field := range required {
		value, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("missing %s", field)
		}
	}
	return object, nil
}

func multiReceiptArray(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return nil, fmt.Errorf("expected receipt array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

// Check required properties before decoding into structs: a missing JSON key
// otherwise becomes a Go zero value that could pass request identity checks.
func multiReceiptShape(raw []byte) error {
	root, err := multiReceiptObject(raw, "schema", "scope", "operation_id", "digest", "actor", "outcome", "tables", "selected_count", "guard_count", "changed_count", "members")
	if err != nil {
		return err
	}
	tables, err := multiReceiptArray(root["tables"])
	if err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	for i, table := range tables {
		if _, err := multiReceiptObject(table, "name", "epoch", "rev_before", "rev_after"); err != nil {
			return fmt.Errorf("tables[%d]: %w", i, err)
		}
	}
	members, err := multiReceiptArray(root["members"])
	if err != nil {
		return fmt.Errorf("members: %w", err)
	}
	for i, member := range members {
		object, err := multiReceiptObject(member, "record_table", "id", "before_rev", "after_rev", "fields_set", "fields_unset", "fields")
		if err != nil {
			return fmt.Errorf("members[%d]: %w", i, err)
		}
		if rawPlaces, ok := object["placements"]; ok {
			places, err := multiReceiptArray(rawPlaces)
			if err != nil {
				return fmt.Errorf("members[%d].placements: %w", i, err)
			}
			for j, place := range places {
				p, err := multiReceiptObject(place, "table", "before_place", "after_place")
				if err != nil {
					return fmt.Errorf("members[%d].placements[%d]: %w", i, j, err)
				}
				if _, ok := p["before_score"]; !ok {
					return fmt.Errorf("members[%d].placements[%d] missing before_score", i, j)
				}
				if _, ok := p["after_score"]; !ok {
					return fmt.Errorf("members[%d].placements[%d] missing after_score", i, j)
				}
			}
		}
	}
	return nil
}

func multiReceiptMatches(r MultiBatchReceipt, m MultiBatchManifest, body string) error {
	d := r.Delta
	sum := sha1.Sum([]byte(body))
	if d == nil || d.Schema != 2 || d.Scope != m.Scope || d.OperationID != m.OperationID || d.Digest != fmt.Sprintf("%x", sum) || d.Actor != m.Actor {
		return fmt.Errorf("receipt identity or digest does not match the request")
	}
	if len(d.Tables) != len(m.Tables) || len(d.Members) != len(m.Members) || d.SelectedCount != len(m.Members) {
		return fmt.Errorf("receipt participant or member count does not match the request")
	}
	for i, actual := range d.Tables {
		want := m.Tables[i]
		before, err := strconv.ParseUint(actual.RevBefore, 10, 64)
		if err != nil || !uintString(actual.RevBefore) || before == math.MaxUint64 || actual.Name != want.Name || actual.Epoch != want.Epoch || actual.RevBefore != want.ExpectedTableRevision || actual.RevAfter != strconv.FormatUint(before+1, 10) {
			return fmt.Errorf("tables[%d] revision or identity does not match the request", i)
		}
	}
	guardCount, changedInstructions, effectiveChanges := 0, 0, 0
	for i, actual := range d.Members {
		want := m.Members[i]
		if actual.RecordTable != want.RecordTable || actual.ID != want.ID || len(actual.Placements) != len(want.Placements) {
			return fmt.Errorf("members[%d] identity or placement count does not match the request", i)
		}
		before, err := strconv.ParseUint(actual.BeforeRev, 10, 64)
		if err != nil || !uintString(actual.BeforeRev) || !uintString(actual.AfterRev) {
			return fmt.Errorf("members[%d] has invalid revision", i)
		}
		if (want.Expect.Absent && actual.BeforeRev != "0") || (!want.Expect.Absent && want.Expect.Revision != "" && actual.BeforeRev != want.Expect.Revision) {
			return fmt.Errorf("members[%d] before revision does not match the guard", i)
		}
		after, err := strconv.ParseUint(actual.AfterRev, 10, 64)
		if err != nil || after < before || after-before > 1 {
			return fmt.Errorf("members[%d] has invalid revision advance", i)
		}
		mutating := len(want.Placements) > 0 || len(want.Set) > 0 || len(want.Unset) > 0
		if mutating {
			changedInstructions++
		} else {
			guardCount++
		}
		if after != before {
			if !mutating {
				return fmt.Errorf("members[%d] guard unexpectedly changed", i)
			}
			effectiveChanges++
		}
		placementEffective := false
		for j, p := range actual.Placements {
			instruction := want.Placements[j]
			if p.Table != instruction.Table || (p.BeforePlace == "") != (p.BeforeScoreText == nil) || (p.AfterPlace == "") != (p.AfterScoreText == nil) {
				return fmt.Errorf("members[%d].placements[%d] identity or score presence is invalid", i, j)
			}
			if (p.BeforeScore != nil && (math.IsNaN(*p.BeforeScore) || math.IsInf(*p.BeforeScore, 0))) || (p.AfterScore != nil && (math.IsNaN(*p.AfterScore) || math.IsInf(*p.AfterScore, 0))) {
				return fmt.Errorf("members[%d].placements[%d] has invalid score", i, j)
			}
			for _, guard := range want.Expect.Places {
				if guard.Table == p.Table {
					if (guard.Absent && p.BeforePlace != "") || (!guard.Absent && p.BeforePlace != guard.Row+":"+guard.Col) {
						return fmt.Errorf("members[%d].placements[%d] before place does not match the guard", i, j)
					}
					break
				}
			}
			if instruction.Add != nil {
				placementEffective = true
				if p.BeforePlace != "" || p.AfterPlace != instruction.Add.Row+":"+instruction.Add.Col || p.AfterScore == nil || *p.AfterScore != instruction.Add.Score {
					return fmt.Errorf("members[%d].placements[%d] add does not match", i, j)
				}
			} else if instruction.Move != nil {
				if p.BeforePlace == "" || p.AfterPlace != instruction.Move.Row+":"+instruction.Move.Col || p.AfterScore == nil {
					return fmt.Errorf("members[%d].placements[%d] move does not match", i, j)
				}
				if instruction.Move.Score != nil && *p.AfterScore != *instruction.Move.Score {
					return fmt.Errorf("members[%d].placements[%d] move score does not match", i, j)
				}
				if instruction.Move.Score == nil && p.BeforeScore != nil && *p.AfterScore != *p.BeforeScore {
					return fmt.Errorf("members[%d].placements[%d] move lost score", i, j)
				}
				if p.BeforePlace != p.AfterPlace || (p.BeforeScore != nil && *p.BeforeScore != *p.AfterScore) {
					placementEffective = true
				}
			} else if !instruction.Remove || p.BeforePlace == "" || p.AfterPlace != "" {
				return fmt.Errorf("members[%d].placements[%d] remove does not match", i, j)
			} else {
				placementEffective = true
			}
		}
		if placementEffective && after == before {
			return fmt.Errorf("members[%d] effective placement did not advance its revision", i)
		}
	}
	if d.GuardCount != guardCount || d.ChangedCount != effectiveChanges || effectiveChanges > changedInstructions || (effectiveChanges == 0 && d.Outcome != "noop") || (effectiveChanges > 0 && d.Outcome != "changed") {
		return fmt.Errorf("receipt outcome or counts are inconsistent")
	}
	return nil
}

// multiRefused translates schema-2 refusals before using the table refusal
// decoder. A scope names the durable operation ledger, not a participant table.
// Table-first replies put the participant before the schema-1 detail fields;
// PLACEGUARD puts it after the member id. Replies without an identified table
// keep scope context and offer a table list rather than naming the scope as one.
func multiRefused(reply []any, m MultiBatchManifest) error {
	o := operation{opID: m.OperationID, batch: true}
	if len(reply) == 0 || fmt.Sprint(reply[0]) != "REFUSED" {
		return nil
	}
	malformed := func() error {
		return fmt.Errorf("%s: %w: malformed refusal (changed=unknown); send the identical manifest with the same operation id", multiScopeLocation(m), ErrUnknownOutcome)
	}
	if len(reply) < 3 {
		return malformed()
	}
	normalized := reply
	table := ""
	code := fmt.Sprint(reply[1])
	switch code {
	case "NOTABLE", "STALE", "EPOCHAHEAD", "REVISION":
		if (code == "STALE" || code == "EPOCHAHEAD") && len(reply) < 5 ||
			code == "REVISION" && len(reply) < 4 {
			return malformed()
		}
		table = fmt.Sprint(reply[2])
		normalized = append([]any{reply[0], reply[1]}, reply[3:]...)
	case "PLACEGUARD":
		if len(reply) < 6 {
			return malformed()
		}
		table = fmt.Sprint(reply[3])
		normalized = []any{reply[0], reply[1], reply[2], reply[4], reply[5]}
	case "MEMBEREXISTS":
		if len(reply) >= 4 && multiNamesTable(m, fmt.Sprint(reply[3])) {
			table = fmt.Sprint(reply[3])
			observed := any("member record")
			if len(reply) >= 5 {
				observed = reply[4]
			}
			normalized = []any{reply[0], reply[1], reply[2], observed}
		}
	case "NOTMEMBER":
		if len(reply) >= 5 && multiNamesTable(m, fmt.Sprint(reply[3])) {
			table = fmt.Sprint(reply[3])
			normalized = []any{reply[0], reply[1], reply[2], "unplaced", reply[4]}
		}
	case "TWICE":
		if len(reply) >= 4 && multiNamesTable(m, fmt.Sprint(reply[3])) {
			table = fmt.Sprint(reply[3])
			normalized = []any{reply[0], reply[1], reply[2]}
		}
	case "SCORE", "CONFIG":
		if len(reply) >= 4 && multiNamesTable(m, fmt.Sprint(reply[3])) {
			table = fmt.Sprint(reply[3])
			if code == "SCORE" && len(reply) >= 5 {
				normalized = []any{reply[0], reply[1], reply[2], reply[4]}
			}
		}
	}
	o.table = table
	err := o.refused(normalized)
	if table == "" {
		if refusal, ok := err.(*Refusal); ok {
			refusal.Location = multiScopeLocation(m)
			var limit *LimitError
			if errors.As(err, &limit) && limit.Member != "" {
				refusal.Location += fmt.Sprintf(" member %q", limit.Member)
			}
			refusal.Next = "nova-table list"
		}
	}
	return err
}

func multiScopeLocation(m MultiBatchManifest) string {
	return fmt.Sprintf("multi batch scope %q operation %q", m.Scope, m.OperationID)
}

func multiBeforeSending(m MultiBatchManifest, err error) error {
	o := operation{table: m.Scope, opID: m.OperationID, batch: true}
	answer := o.beforeSending(err)
	var refusal *Refusal
	if errors.As(answer, &refusal) {
		refusal.Location = multiScopeLocation(m)
		refusal.Next = "nova-table batch -h"
		return answer
	}
	var manifest *ManifestError
	if errors.As(err, &manifest) {
		return fmt.Errorf("%s: invalid batch manifest: %w; %s; changed=no; run: nova-table batch -h", multiScopeLocation(m), err, CheckedBeforeSending)
	}
	return answer
}

func multiNamesTable(m MultiBatchManifest, name string) bool {
	for _, table := range m.Tables {
		if table.Name == name {
			return true
		}
	}
	return false
}

// ApplyMultiBatch sends one v2 manifest as one Redis function call. A transport
// error has an unknown outcome; resend the identical manifest and operation ID
// to obtain the durable receipt or perform the original transaction.
func ApplyMultiBatch(ctx context.Context, c redis.Cmdable, m MultiBatchManifest) (MultiBatchReceipt, error) {
	if m.Tables == nil {
		m.Tables = []MultiBatchTable{}
	}
	if m.Members == nil {
		m.Members = []MultiBatchMember{}
	}
	body, err := payload(m)
	if err != nil {
		return MultiBatchReceipt{}, err
	}
	if _, err = ValidateMultiBatchManifestRaw([]byte(body)); err != nil {
		return MultiBatchReceipt{}, multiBeforeSending(m, err)
	}
	if c == nil {
		return MultiBatchReceipt{}, fmt.Errorf("multi batch scope %q: nil Redis client", m.Scope)
	}
	cmd := c.FCall(ctx, FnApplyMulti, nil, m.Scope, body)
	reply, err := cmd.Slice()
	if err != nil {
		return MultiBatchReceipt{}, fmt.Errorf("multi batch scope %q operation %q: %w: %w (changed=unknown); send the identical manifest with the same operation id", m.Scope, m.OperationID, ErrUnknownOutcome, err)
	}
	if err := multiRefused(reply, m); err != nil {
		return MultiBatchReceipt{}, err
	}
	r, err := parseMultiBatchReply(reply)
	if err != nil {
		return MultiBatchReceipt{}, fmt.Errorf("multi batch scope %q operation %q: %w: %w (receipt unverified); send the identical manifest with the same operation id", m.Scope, m.OperationID, ErrUnknownOutcome, err)
	}
	if err := multiReceiptMatches(r, m, body); err != nil {
		return MultiBatchReceipt{}, fmt.Errorf("multi batch scope %q operation %q: %w: %w; send the identical manifest with the same operation id", m.Scope, m.OperationID, ErrUnknownOutcome, err)
	}
	return r, nil
}
