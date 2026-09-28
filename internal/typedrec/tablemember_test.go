package typedrec

import "testing"

func TestTableMemberValidatesTheCompleteReply(t *testing.T) {
	t.Parallel()
	valid := []any{"MEMBER", "18446744073709551615", "9007199254740993", "placed", "stream: build", "done"}
	got, err := ParseTableMember(valid)
	if err != nil || got.Epoch != 18446744073709551615 || got.Revision != 9007199254740993 || got.Row != "stream: build" || got.Column != "done" {
		t.Fatalf("valid: %+v %v", got, err)
	}
	for _, bad := range [][]any{
		nil, {"MEMBER", "0"}, {"MEMBER", "00", "1", "missing", "", ""},
		{"MEMBER", "0", "18446744073709551616", "missing", "", ""},
		{"MEMBER", "0", "1", "unknown", "", ""},
		{"MEMBER", "0", "1", "missing", "r", "c"},
		{"MEMBER", "0", "1", "placed", "", "c"},
		{"MEMBER", "0", int64(1), "unplaced", "", ""},
	} {
		if _, err := ParseTableMember(bad); err == nil {
			t.Errorf("accepted malformed %v", bad)
		}
	}
}
