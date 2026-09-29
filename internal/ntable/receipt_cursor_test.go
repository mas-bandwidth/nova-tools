package ntable

import (
	"errors"
	"strings"
	"testing"
)

func TestReceiptCursorIDValidation(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"1-0", "9999999999999-42", "18446744073709551615-18446744073709551615"} {
		if !validReceiptID(id) {
			t.Errorf("valid ID %q refused", id)
		}
	}
	for _, id := range []string{"", "1", "1-", "-1", "01-0", "1-00", "1-2-3", "18446744073709551616-0"} {
		if validReceiptID(id) {
			t.Errorf("invalid ID %q accepted", id)
		}
	}
}

func TestReceiptEntryRequiresExactRevisionStep(t *testing.T) {
	t.Parallel()
	entry := func(before, after string) []any {
		return []any{"9999999999999-42", []any{"rev_before", before, "rev_after", after, "epoch", "0", "verb", "apply"}}
	}
	if got, err := receiptEntry(entry("18446744073709551614", "18446744073709551615")); err != nil || got.Revision != ^uint64(0) {
		t.Fatalf("max revision step: %+v %v", got, err)
	}
	for _, pair := range [][2]string{{"5", "7"}, {"18446744073709551615", "0"}, {"01", "2"}} {
		if _, err := receiptEntry(entry(pair[0], pair[1])); err == nil {
			t.Errorf("accepted broken revision step %v", pair)
		}
	}
}

func TestReceiptPageRejectsFalseContinuationAndOutOfOrderIDs(t *testing.T) {
	t.Parallel()
	event := func(id, before, after string, extra ...any) []any {
		fields := []any{"rev_before", before, "rev_after", after, "epoch", "0", "verb", "apply"}
		fields = append(fields, extra...)
		return []any{id, fields}
	}
	tests := []struct {
		name   string
		cursor ReceiptCursor
		limit  int
		reply  []any
		gap    bool
	}{
		{"false continuation", ReceiptCursor{}, 1, []any{"PAGE", "1", "1", []any{event("2-0", "0", "1")}}, true},
		{"decreasing IDs", ReceiptCursor{}, 2, []any{"PAGE", "2", "0", []any{event("2-0", "0", "1"), event("1-0", "1", "2")}}, true},
		{"repeated anchor", ReceiptCursor{Table: "t", ID: "2-0", Revision: 1}, 1, []any{"PAGE", "2", "0", []any{event("2-0", "1", "2")}}, true},
		{"oversized page", ReceiptCursor{}, 1, []any{"PAGE", "1", "0", []any{event("2-0", "0", "1", "blob", strings.Repeat("x", LimitReceiptPageByte))}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			page, err := parseReceiptPage("t", tt.cursor, tt.limit, tt.reply)
			if err == nil || len(page.Events) != 0 {
				t.Fatalf("accepted malformed page: %+v %v", page, err)
			}
			if tt.gap && !errors.Is(err, ErrReceiptCursorGap) {
				t.Fatalf("wanted cursor gap, got %v", err)
			}
		})
	}
}
