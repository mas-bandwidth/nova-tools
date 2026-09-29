package ntable

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	FnReceipts           = "ns_table_receipts"
	LimitReceiptPage     = 128
	LimitReceiptPageByte = 4 << 20
)

var ErrReceiptCursorGap = errors.New("table receipt cursor cannot prove a complete stream")

// ReceiptCursor identifies the last event the caller has handled. The zero
// cursor starts at the beginning; its table is filled by ReadReceiptPage.
// Persist Next only after handling all events in the returned page.
type ReceiptCursor struct {
	Table    string
	ID       string
	Revision uint64
	Epoch    uint64
}

// ReceiptEvent is the complete stream entry, including ordinary or batch
// metadata. IDs are opaque Redis stream positions, never sequential counters.
type ReceiptEvent struct {
	ID       string
	Revision uint64
	Before   uint64
	Epoch    uint64
	Verb     string
	Fields   map[string]string
}

type ReceiptPage struct {
	Events          []ReceiptEvent
	Next            ReceiptCursor
	CurrentRevision uint64
	HasMore         bool
}

func validReceiptID(id string) bool {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok || strings.Contains(seq, "-") {
		return false
	}
	for _, part := range []string{ms, seq} {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}

func receiptIDLess(a, b string) bool {
	ams, aseq, _ := strings.Cut(a, "-")
	bms, bseq, _ := strings.Cut(b, "-")
	av, _ := strconv.ParseUint(ams, 10, 64)
	bv, _ := strconv.ParseUint(bms, 10, 64)
	if av != bv {
		return av < bv
	}
	av, _ = strconv.ParseUint(aseq, 10, 64)
	bv, _ = strconv.ParseUint(bseq, 10, 64)
	return av < bv
}

func receiptText(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func receiptUint(v any) (uint64, bool) {
	s, ok := receiptText(v)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && strconv.FormatUint(n, 10) == s
}

func receiptEntry(raw any) (ReceiptEvent, error) {
	entry, ok := raw.([]any)
	if !ok || len(entry) != 2 {
		return ReceiptEvent{}, errors.New("malformed receipt event")
	}
	id, ok := receiptText(entry[0])
	if !ok || !validReceiptID(id) {
		return ReceiptEvent{}, errors.New("malformed receipt event ID")
	}
	flat, ok := entry[1].([]any)
	if !ok || len(flat)%2 != 0 {
		return ReceiptEvent{}, errors.New("malformed receipt event fields")
	}
	fields := make(map[string]string, len(flat)/2)
	for i := 0; i < len(flat); i += 2 {
		k, keyOK := receiptText(flat[i])
		v, valueOK := receiptText(flat[i+1])
		if !keyOK || !valueOK {
			return ReceiptEvent{}, errors.New("malformed receipt event field")
		}
		if _, duplicate := fields[k]; duplicate {
			return ReceiptEvent{}, errors.New("duplicate receipt event field")
		}
		fields[k] = v
	}
	before, ok := receiptUint(fields["rev_before"])
	if !ok {
		return ReceiptEvent{}, errors.New("malformed receipt event before revision")
	}
	after, ok := receiptUint(fields["rev_after"])
	if !ok || before == ^uint64(0) || after != before+1 {
		return ReceiptEvent{}, errors.New("malformed receipt event after revision")
	}
	epoch, ok := receiptUint(fields["epoch"])
	if !ok || fields["verb"] == "" {
		return ReceiptEvent{}, errors.New("malformed receipt event epoch or verb")
	}
	return ReceiptEvent{ID: id, Revision: after, Before: before, Epoch: epoch, Verb: fields["verb"], Fields: fields}, nil
}

// ReadReceiptPage returns a bounded, consistent page of one table's change
// stream. It refuses a missing anchor, missing event, trimmed tail, malformed
// revision chain or a cursor for another table; it never persists the cursor.
// A complete drop and recreate sequence is returned as ordinary lifecycle
// events because the table's revision and stream survive both writes.
func ReadReceiptPage(ctx context.Context, c redis.Cmdable, table string, cursor ReceiptCursor, limit int) (ReceiptPage, error) {
	if !ValidName(table) {
		return ReceiptPage{}, fmt.Errorf("invalid table name %q", table)
	}
	if limit < 1 || limit > LimitReceiptPage {
		return ReceiptPage{}, fmt.Errorf("receipt page limit must be 1..%d: %d", LimitReceiptPage, limit)
	}
	if cursor.Table != "" && cursor.Table != table {
		return ReceiptPage{}, fmt.Errorf("%w: cursor table %q differs from %q", ErrReceiptCursorGap, cursor.Table, table)
	}
	if cursor.ID == "" {
		if cursor.Revision != 0 || cursor.Epoch != 0 {
			return ReceiptPage{}, fmt.Errorf("%w: initial cursor must have zero revision and epoch", ErrReceiptCursorGap)
		}
	} else if !validReceiptID(cursor.ID) {
		return ReceiptPage{}, fmt.Errorf("%w: invalid cursor stream ID", ErrReceiptCursorGap)
	}
	epoch := ""
	if cursor.ID != "" {
		epoch = strconv.FormatUint(cursor.Epoch, 10)
	}
	cmd := c.FCallRO(ctx, FnReceipts, []string{DefKey(table)}, table, cursor.ID,
		strconv.FormatUint(cursor.Revision, 10), epoch, strconv.Itoa(limit))
	reply, err := cmd.Slice()
	if err != nil {
		return ReceiptPage{}, fmt.Errorf("read table %q receipts: %w", table, err)
	}
	return parseReceiptPage(table, cursor, limit, reply)
}

func parseReceiptPage(table string, cursor ReceiptCursor, limit int, reply []any) (ReceiptPage, error) {
	if len(reply) >= 2 && reply[0] == "REFUSED" {
		code, _ := receiptText(reply[1])
		detail := ""
		if len(reply) >= 3 {
			detail, _ = receiptText(reply[2])
		}
		switch code {
		case "CURSORGAP":
			return ReceiptPage{}, fmt.Errorf("%w: %s", ErrReceiptCursorGap, detail)
		case "NOTABLE":
			return ReceiptPage{}, fmt.Errorf("%w: %s", ErrNoTable, detail)
		case "WRONGTYPE":
			return ReceiptPage{}, fmt.Errorf("%w: %s", ErrWrongType, detail)
		default:
			return ReceiptPage{}, fmt.Errorf("table %q receipts refused (%s): %s", table, code, detail)
		}
	}
	if len(reply) != 4 || reply[0] != "PAGE" {
		return ReceiptPage{}, fmt.Errorf("table %q: malformed receipt page", table)
	}
	current, ok := receiptUint(reply[1])
	if !ok {
		return ReceiptPage{}, fmt.Errorf("table %q: malformed current revision", table)
	}
	more, ok := receiptText(reply[2])
	if !ok || (more != "0" && more != "1") {
		return ReceiptPage{}, fmt.Errorf("table %q: malformed receipt continuation", table)
	}
	rawEvents, ok := reply[3].([]any)
	if !ok || len(rawEvents) > limit || (more == "1" && len(rawEvents) != limit) {
		return ReceiptPage{}, fmt.Errorf("table %q: malformed receipt count", table)
	}
	page := ReceiptPage{Events: make([]ReceiptEvent, 0, len(rawEvents)),
		Next:            ReceiptCursor{Table: table, ID: cursor.ID, Revision: cursor.Revision, Epoch: cursor.Epoch},
		CurrentRevision: current, HasMore: more == "1"}
	if page.Next.Revision > current {
		return ReceiptPage{}, fmt.Errorf("%w: cursor is ahead of current revision", ErrReceiptCursorGap)
	}
	bytes := 0
	for _, raw := range rawEvents {
		event, err := receiptEntry(raw)
		if err != nil {
			return ReceiptPage{}, fmt.Errorf("table %q: %w", table, err)
		}
		if event.Before != page.Next.Revision {
			return ReceiptPage{}, fmt.Errorf("%w: receipt revision chain is broken", ErrReceiptCursorGap)
		}
		if page.Next.ID != "" && !receiptIDLess(page.Next.ID, event.ID) {
			return ReceiptPage{}, fmt.Errorf("%w: receipt stream IDs are not increasing", ErrReceiptCursorGap)
		}
		if len(event.ID) > LimitReceiptPageByte-bytes {
			return ReceiptPage{}, fmt.Errorf("table %q: receipt page exceeds %d bytes", table, LimitReceiptPageByte)
		}
		bytes += len(event.ID)
		for field, value := range event.Fields {
			if len(field) > LimitReceiptPageByte-bytes || len(value) > LimitReceiptPageByte-bytes-len(field) {
				return ReceiptPage{}, fmt.Errorf("table %q: receipt page exceeds %d bytes", table, LimitReceiptPageByte)
			}
			bytes += len(field) + len(value)
		}
		page.Events = append(page.Events, event)
		page.Next.ID, page.Next.Revision, page.Next.Epoch = event.ID, event.Revision, event.Epoch
	}
	if !page.HasMore && page.Next.Revision != current {
		return ReceiptPage{}, fmt.Errorf("%w: receipt tail is missing", ErrReceiptCursorGap)
	}
	if page.HasMore && page.Next.Revision >= current {
		return ReceiptPage{}, fmt.Errorf("%w: continuation exceeds current revision", ErrReceiptCursorGap)
	}
	return page, nil
}
