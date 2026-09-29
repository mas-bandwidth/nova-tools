package batchmodel

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
)

// RedisCapture reads a disposable store through the already-landed shared
// tablemodel Redis runner. It never derives state from an FCALL receipt. The
// caller must keep the owned server quiescent between each before/after scan.
type RedisCapture struct {
	Store                *tablemodel.Store
	EpochKey, EpochField string
}

// StreamEvent is the independently read tail of the table change stream.
type StreamEvent struct {
	ID        string
	Fields    map[string]string
	RawFields []string // exact ordered XRANGE field/value sequence
}

// Capture reads every Redis key and the finite model's selected member facts.
// The complete image is encoded by key type and full contents so refusals and
// identical retries can prove the whole disposable store remained unchanged.
func (a RedisCapture) Capture(ctx context.Context, q Request) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if a.Store == nil {
		return Snapshot{}, fmt.Errorf("nil owned Redis store")
	}
	if q.Table == "" || q.Epoch == "" {
		return Snapshot{}, fmt.Errorf("capture needs table and epoch")
	}
	if _, err := decimal(q.Epoch); err != nil {
		return Snapshot{}, fmt.Errorf("capture epoch: %w", err)
	}
	image, err := a.ScanImage(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	epochKey, epochField := a.EpochKey, a.EpochField
	if epochKey == "" {
		epochKey = "replay:epoch"
	}
	if epochField == "" {
		epochField = "n"
	}
	epochFields, err := hashReply(a.Store.Cmd("HGETALL", epochKey))
	if err != nil {
		return Snapshot{}, err
	}
	epoch := epochFields[epochField]
	if epoch == "" {
		epoch = "0"
	}
	revisionFields, err := hashReply(a.Store.Cmd("HGETALL", ntable.RevisionKey(q.Table)))
	if err != nil {
		return Snapshot{}, err
	}
	rev := revisionFields["n"]
	if rev == "" {
		rev = "0"
	}
	s := Snapshot{Image: image, Epoch: epoch, TableRevision: rev, EpochFields: epochFields, RevisionFields: revisionFields, Members: map[string]Member{}}
	if err := a.readFiniteCells(q.Table, &s); err != nil {
		return Snapshot{}, err
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		m, err := a.readMember(q.Table, id)
		if err != nil {
			return Snapshot{}, fmt.Errorf("member %s: %w", id, err)
		}
		s.Members[id] = m
	}
	s.OperationRecords = map[string]OperationRecord{}
	opsKey := operationHashKey(q.Table)
	for key := range image {
		if strings.HasPrefix(key, "table:"+q.Table+":") && strings.Contains(key, ":op:") {
			return Snapshot{}, fmt.Errorf("legacy per-operation key %s outside current runtime protocol", key)
		}
	}
	if _, exists := image[opsKey]; exists {
		h, err := hashReply(a.Store.Cmd("HGETALL", opsKey))
		if err != nil {
			return Snapshot{}, fmt.Errorf("operation hash %s: %w", opsKey, err)
		}
		for field, raw := range h {
			record, err := decodeOperationRecord(q.Table, field, raw)
			if err != nil {
				return Snapshot{}, fmt.Errorf("operation %s[%s]: %w", opsKey, field, err)
			}
			s.OperationRecords[field] = record
		}
	}
	s.Operations = uint64(len(s.OperationRecords))
	stream := ntable.ChangesKey(q.Table)
	n, err := replyInt(a.Store.Cmd("XLEN", stream))
	if err != nil {
		return Snapshot{}, err
	}
	s.Receipts = n
	events, err := a.events(stream)
	if err != nil {
		return Snapshot{}, err
	}
	s.Events = events
	if uint64(len(events)) != s.Receipts {
		return Snapshot{}, fmt.Errorf("stream length changed during capture")
	}
	s.StreamInfo, err = a.readStreamInfo(stream)
	if err != nil {
		return Snapshot{}, err
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		s.LastEvent = &last
	}
	if record, ok := s.OperationRecords[operationField(q.Epoch, q.OperationID)]; ok {
		s.Recorded = &record
	}
	return s, nil
}

func operationHashKey(table string) string   { return "table:" + table + ":ops" }
func operationField(epoch, id string) string { return epoch + ":" + id }

// The physical operations hash is shared across epochs. Each field is a
// separately validated logical record, keyed by the exact epoch and ID.
func decodeOperationRecord(table, field, raw string) (OperationRecord, error) {
	epoch, id, ok := strings.Cut(field, ":")
	if !ok || id == "" || (epoch != "1" && epoch != "2") {
		return OperationRecord{}, fmt.Errorf("field identity outside finite fixture")
	}
	if err := rejectDuplicateJSONKeys([]byte(raw)); err != nil {
		return OperationRecord{}, err
	}
	str := primitiveShape('"')
	shape := objectShape(map[string]wireShape{
		"operation_id": str, "digest": str, "request": str, "stream_id": str,
		"epoch": str, "rev_before": str, "rev_after": str, "outcome": str, "result": str,
	}, "operation_id", "digest", "request", "stream_id", "epoch", "rev_before", "rev_after", "outcome", "result")
	if err := shape([]byte(raw)); err != nil {
		return OperationRecord{}, err
	}
	var r struct{ OperationID, Digest, Request, StreamID, Epoch, RevBefore, RevAfter, Outcome, Result string }
	// Decode by wire names, rather than Go's case-insensitive field matching.
	var fields map[string]string
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return OperationRecord{}, err
	}
	r.OperationID, r.Digest, r.Request, r.StreamID = fields["operation_id"], fields["digest"], fields["request"], fields["stream_id"]
	r.Epoch, r.RevBefore, r.RevAfter, r.Outcome, r.Result = fields["epoch"], fields["rev_before"], fields["rev_after"], fields["outcome"], fields["result"]
	if r.Epoch != epoch || r.OperationID != id || r.StreamID == "" || (r.Outcome != "changed" && r.Outcome != "noop") {
		return OperationRecord{}, fmt.Errorf("record identity or outcome differs from hash field")
	}
	if _, err := decimal(r.RevBefore); err != nil {
		return OperationRecord{}, err
	}
	if _, err := decimal(r.RevAfter); err != nil {
		return OperationRecord{}, err
	}
	if err := rejectDuplicateJSONKeys([]byte(r.Request)); err != nil {
		return OperationRecord{}, fmt.Errorf("request: %w", err)
	}
	if err := validateManifestShape([]byte(r.Request)); err != nil {
		return OperationRecord{}, fmt.Errorf("request: %w", err)
	}
	var request struct {
		Table       string `json:"table"`
		Epoch       string `json:"epoch"`
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal([]byte(r.Request), &request); err != nil || request.Table != table || request.Epoch != epoch || request.OperationID != id {
		return OperationRecord{}, fmt.Errorf("stored request identity differs from record")
	}
	digest := sha1.Sum([]byte(r.Request))
	if r.Digest != hex.EncodeToString(digest[:]) {
		return OperationRecord{}, fmt.Errorf("stored request digest differs")
	}
	if !json.Valid([]byte(r.Result)) {
		return OperationRecord{}, fmt.Errorf("stored result is not JSON")
	}
	return OperationRecord{Table: table, Epoch: epoch, OperationID: id, Digest: r.Digest,
		ReceiptID: r.StreamID, BeforeRevision: r.RevBefore, AfterRevision: r.RevAfter,
		Outcome: r.Outcome, Canonical: []byte(r.Request), ResultJSON: []byte(r.Result)}, nil
}

func (a RedisCapture) readFiniteCells(table string, s *Snapshot) error {
	s.Definitions = map[string]map[string]string{}
	s.Rows = map[string][]string{}
	s.RowScores = map[string]map[string]string{}
	s.RowFields = map[string]map[string]map[string]string{}
	s.Bindings = map[Cell]string{}
	s.CellTypes = map[Cell]string{}
	s.CellMembers = map[Cell]map[string]string{}
	for _, epoch := range []string{"1", "2"} {
		n, _ := decimal(epoch)
		definition, err := hashReply(a.Store.Cmd("HGETALL", ntable.EpochPrefix(table, n)+":definition"))
		if err != nil {
			return fmt.Errorf("epoch %s definition: %w", epoch, err)
		}
		s.Definitions[epoch] = definition
		s.RowFields[epoch] = map[string]map[string]string{}
		s.RowScores[epoch] = map[string]string{}
		rowsKey := ntable.RowsKeyAt(table, n)
		rowKind, err := replyString(a.Store.Cmd("TYPE", rowsKey))
		if err != nil {
			return err
		}
		if rowKind != "none" && rowKind != "zset" {
			return fmt.Errorf("rows %s has type %s", epoch, rowKind)
		}
		if rowKind == "zset" {
			raw, err := replyList(a.Store.Cmd("ZRANGE", rowsKey, 0, -1, "WITHSCORES"))
			if err != nil {
				return err
			}
			if len(raw)%2 != 0 {
				return fmt.Errorf("odd rows ZSET reply in epoch %s", epoch)
			}
			for i := 0; i < len(raw); i += 2 {
				row, err := replyString(raw[i])
				if err != nil {
					return err
				}
				score, err := replyString(raw[i+1])
				if err != nil {
					return err
				}
				if _, duplicate := s.RowScores[epoch][row]; duplicate {
					return fmt.Errorf("duplicate row %s in epoch %s", row, epoch)
				}
				s.Rows[epoch] = append(s.Rows[epoch], row)
				s.RowScores[epoch][row] = score
			}
		}
		for _, row := range []string{"r1", "r2"} {
			rowFields, err := hashReply(a.Store.Cmd("HGETALL", ntable.RowKeyAt(table, row, n)))
			if err != nil {
				return fmt.Errorf("epoch %s row %s: %w", epoch, row, err)
			}
			s.RowFields[epoch][row] = rowFields
			for _, col := range []string{"c1", "c2"} {
				if target := rowFields["key:"+col]; target != "" {
					s.Bindings[Cell{Table: table, Epoch: epoch, Row: row, Column: col}] = target
				}
			}
		}
		for _, row := range []string{"r1", "r2"} {
			for _, col := range []string{"c1", "c2"} {
				cell := Cell{Table: table, Epoch: epoch, Row: row, Column: col}
				key := ntable.CellKeyAt(table, row, col, n)
				kind, err := replyString(a.Store.Cmd("TYPE", key))
				if err != nil {
					return err
				}
				s.CellTypes[cell] = kind
				members := map[string]string{}
				if kind == "zset" {
					raw, err := replyList(a.Store.Cmd("ZRANGE", key, 0, -1, "WITHSCORES"))
					if err != nil {
						return err
					}
					if len(raw)%2 != 0 {
						return fmt.Errorf("odd zset reply for %s", key)
					}
					for i := 0; i < len(raw); i += 2 {
						id, err := replyString(raw[i])
						if err != nil {
							return err
						}
						score, err := replyString(raw[i+1])
						if err != nil {
							return err
						}
						if _, exists := members[id]; exists {
							return fmt.Errorf("duplicate member %s in %s", id, key)
						}
						members[id] = score
					}
				}
				s.CellMembers[cell] = members
			}
		}
	}
	return nil
}

// ScanImage stores Redis' lossless serialized value for every key, including
// stream IDs/groups/internal metadata. SCAN may repeat a key, so the final
// sorted key set rather than scan order is used. Expiring keys are outside this
// quiescent fixture: their TTL can change between two read-only observations.
func (a RedisCapture) ScanImage(ctx context.Context) (map[string][]byte, error) {
	if a.Store == nil {
		return nil, fmt.Errorf("nil owned Redis store")
	}
	keys := map[string]bool{}
	cursor := "0"
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parts, err := replyList(a.Store.Cmd("SCAN", cursor, "COUNT", 1000))
		if err != nil || len(parts) != 2 {
			return nil, fmt.Errorf("SCAN reply: %v", err)
		}
		cursor, err = replyString(parts[0])
		if err != nil {
			return nil, err
		}
		found, err := replyList(parts[1])
		if err != nil {
			return nil, err
		}
		for _, v := range found {
			k, err := replyString(v)
			if err != nil {
				return nil, err
			}
			keys[k] = true
		}
		if cursor == "0" {
			break
		}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	image := make(map[string][]byte, len(keys))
	for _, k := range ordered {
		kind, err := replyString(a.Store.Cmd("TYPE", k))
		if err != nil {
			return nil, err
		}
		if kind == "none" {
			return nil, fmt.Errorf("key %q disappeared during capture", k)
		}
		ttl, err := replySignedInt(a.Store.Cmd("PTTL", k))
		if err != nil {
			return nil, err
		}
		if ttl != -1 {
			return nil, fmt.Errorf("key %q has expiring or missing TTL %d", k, ttl)
		}
		value, err := replyString(a.Store.Cmd("DUMP", k))
		if err != nil {
			return nil, fmt.Errorf("DUMP %q: %w", k, err)
		}
		raw := make([]byte, 8+len(kind)+len(value))
		binary.BigEndian.PutUint64(raw[:8], uint64(len(kind)))
		copy(raw[8:], kind)
		copy(raw[8+len(kind):], value)
		image[k] = raw
	}
	return image, nil
}

func (a RedisCapture) readMember(table, id string) (Member, error) {
	h, err := hashReply(a.Store.Cmd("HGETALL", ntable.MemberKey(id)))
	if err != nil {
		return Member{}, err
	}
	if len(h) == 0 {
		return Member{Revision: "0"}, nil
	}
	m := Member{Exists: true, Epoch: h["epoch"], Revision: h["revision"], Fields: map[string]string{}}
	if m.Revision == "" {
		m.Revision = "0"
	}
	for k, v := range h {
		if strings.HasPrefix(k, "place:") && k != "place:"+table {
			return Member{}, fmt.Errorf("member %s has unmodelled foreign placement %q", id, k)
		}
		if k != "epoch" && k != "revision" && !strings.HasPrefix(k, "place:") {
			m.Fields[k] = v
		}
	}
	if place, hasPlace := h["place:"+table]; hasPlace && place == "" {
		return Member{}, fmt.Errorf("member %s has empty placement field", id)
	}
	if place := h["place:"+table]; place != "" {
		i := strings.LastIndex(place, ":")
		if i < 1 || i == len(place)-1 {
			return Member{}, fmt.Errorf("invalid placement %q", place)
		}
		row, col := place[:i], place[i+1:]
		epoch := m.Epoch
		if epoch == "" {
			epoch = "0"
		}
		e, err := decimal(epoch)
		if err != nil {
			return Member{}, err
		}
		score, err := optionalString(a.Store.Cmd("ZSCORE", ntable.CellKeyAt(table, row, col, e), id))
		if err != nil {
			return Member{}, err
		}
		if score == "" {
			return Member{}, fmt.Errorf("placed member %s lacks owned score", id)
		}
		m.Place = &Place{Row: row, Column: col, Score: score}
	}
	return m, nil
}

func (a RedisCapture) events(stream string) ([]StreamEvent, error) {
	v, err := replyList(a.Store.Cmd("XRANGE", stream, "-", "+"))
	if err != nil {
		return nil, err
	}
	out := make([]StreamEvent, 0, len(v))
	for _, raw := range v {
		entry, err := replyList(raw)
		if err != nil || len(entry) != 2 {
			return nil, fmt.Errorf("malformed stream entry: %v", err)
		}
		id, err := replyString(entry[0])
		if err != nil {
			return nil, err
		}
		ordered, err := replyList(entry[1])
		if err != nil {
			return nil, err
		}
		rawFields := make([]string, len(ordered))
		for i, v := range ordered {
			rawFields[i], err = replyString(v)
			if err != nil {
				return nil, err
			}
		}
		fields, err := hashReply(entry[1])
		if err != nil {
			return nil, err
		}
		out = append(out, StreamEvent{ID: id, Fields: fields, RawFields: rawFields})
	}
	return out, nil
}

func (a RedisCapture) readStreamInfo(stream string) (StreamInfo, error) {
	var info StreamInfo
	kind, err := replyString(a.Store.Cmd("TYPE", stream))
	if err != nil {
		return info, err
	}
	if kind == "none" {
		return StreamInfo{LastGeneratedID: "0-0", MaxDeletedID: "0-0"}, nil
	}
	if kind != "stream" {
		return info, fmt.Errorf("change-stream key has type %s", kind)
	}
	groups, err := replyList(a.Store.Cmd("XINFO", "GROUPS", stream))
	if err != nil {
		return info, err
	}
	if len(groups) != 0 {
		return info, fmt.Errorf("disposable change stream has consumer groups")
	}
	flat, err := replyList(a.Store.Cmd("XINFO", "STREAM", stream))
	if err != nil {
		return info, err
	}
	if len(flat)%2 != 0 {
		return info, fmt.Errorf("odd XINFO STREAM reply")
	}
	seen := map[string]bool{}
	for i := 0; i < len(flat); i += 2 {
		key, err := replyString(flat[i])
		if err != nil {
			return info, err
		}
		if seen[key] {
			return info, fmt.Errorf("duplicate XINFO STREAM field %s", key)
		}
		seen[key] = true
		switch key {
		case "entries-added":
			info.EntriesAdded, err = replyInt(flat[i+1])
		case "last-generated-id":
			info.LastGeneratedID, err = replyString(flat[i+1])
		case "max-deleted-entry-id":
			info.MaxDeletedID, err = replyString(flat[i+1])
		}
		if err != nil {
			return info, fmt.Errorf("XINFO STREAM %s: %w", key, err)
		}
	}
	for _, key := range []string{"entries-added", "last-generated-id", "max-deleted-entry-id"} {
		if !seen[key] {
			return info, fmt.Errorf("XINFO STREAM lacks %s", key)
		}
	}
	return info, nil
}

func replyList(v any) ([]any, error) {
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected Redis list, got %T", v)
	}
	return a, nil
}
func replyString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected Redis string, got %T", v)
	}
	return s, nil
}
func optionalString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	return replyString(v)
}
func replyInt(v any) (uint64, error) {
	switch x := v.(type) {
	case int64:
		if x < 0 {
			return 0, fmt.Errorf("negative Redis integer")
		}
		return uint64(x), nil
	case string:
		return decimal(x)
	}
	return 0, fmt.Errorf("expected Redis integer, got %T", v)
}
func replySignedInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("expected signed Redis integer, got %T", v)
	}
}
func hashReply(v any) (map[string]string, error) {
	parts, err := replyList(v)
	if err != nil {
		return nil, err
	}
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("odd Redis field list")
	}
	out := make(map[string]string, len(parts)/2)
	for i := 0; i < len(parts); i += 2 {
		k, err := replyString(parts[i])
		if err != nil {
			return nil, err
		}
		val, err := replyString(parts[i+1])
		if err != nil {
			return nil, err
		}
		if _, ok := out[k]; ok {
			return nil, fmt.Errorf("duplicate Redis field %q", k)
		}
		out[k] = val
	}
	return out, nil
}
