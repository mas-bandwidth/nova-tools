package typedrec_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestParseDisposition is DISPOSITION v1's table (#2506 rev 4 part B): who
// and verdict required, head required for APPROVE, an APPROVE strict (whole
// line), a HOLD lenient.
func TestParseDisposition(t *testing.T) {
	const h40 = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name          string
		line          string
		ok, valid     bool
		whole         bool
		field, defect string
		who, head     string
	}{
		{name: "approve whole", line: "DISPOSITION who=Stella head=" + h40 + " verdict=APPROVE score=9", ok: true, valid: true, whole: true, who: "stella", head: h40},
		{name: "approve short head", line: "  DISPOSITION who=emma head=5ADF9CB2 verdict=approve score=10/10 ", ok: true, valid: true, whole: true, who: "emma", head: "5adf9cb2"},
		{name: "approve no head", line: "DISPOSITION who=stella verdict=APPROVE", ok: true, field: "head", defect: "missing", whole: true, who: "stella"},
		{name: "approve not whole", line: "DISPOSITION who=stella head=" + h40 + " verdict=APPROVE and some prose", ok: true, field: "line", defect: "malformed", who: "stella", head: h40},
		{name: "approve repeated key", line: "DISPOSITION who=stella head=" + h40 + " verdict=APPROVE who=emma", ok: true, field: "line", defect: "malformed", who: "emma", head: h40},
		{name: "approve bad head", line: "DISPOSITION who=stella head=zzzzzzz verdict=APPROVE", ok: true, field: "head", defect: "malformed", whole: true, who: "stella", head: "zzzzzzz"},
		{name: "approve bad score", line: "DISPOSITION who=stella head=" + h40 + " verdict=APPROVE score=11", ok: true, field: "score", defect: "malformed", whole: true, who: "stella", head: h40},
		{name: "hold no head", line: "DISPOSITION who=johnny verdict=HOLD", ok: true, valid: true, whole: true, who: "johnny"},
		{name: "hold lenient prose", line: "DISPOSITION who=johnny verdict=HOLD because the test is red", ok: true, valid: true, who: "johnny"},
		{name: "hold quoted scope", line: `DISPOSITION who=johnny head=abcdef1 verdict=HOLD scope="internal/merge"`, ok: true, valid: true, whole: true, who: "johnny", head: "abcdef1"},
		{name: "who missing", line: "DISPOSITION head=" + h40 + " verdict=HOLD", ok: true, field: "who", defect: "missing", whole: true, head: h40},
		{name: "note verdict", line: "DISPOSITION who=rowan verdict=NOTE", ok: true, field: "verdict", defect: "malformed", whole: true, who: "rowan"},
		{name: "no verdict", line: "DISPOSITION who=stella head=" + h40},
		{name: "not the word", line: "Disposition who=stella verdict=HOLD"},
		{name: "prose", line: "the DISPOSITION who=stella verdict=HOLD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := typedrec.ParseDisposition(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v (%+v)", ok, tc.ok, c)
			}
			if !ok {
				return
			}
			if c.Valid != tc.valid || c.Whole != tc.whole || c.Field != tc.field || c.Defect != tc.defect || c.Who != tc.who || c.Head != tc.head {
				t.Fatalf("got %+v; want valid=%v whole=%v field=%q defect=%q who=%q head=%q", c, tc.valid, tc.whole, tc.field, tc.defect, tc.who, tc.head)
			}
			if tc.valid != (c.Refusal() == "") {
				t.Fatalf("Refusal()=%q for valid=%v", c.Refusal(), tc.valid)
			}
		})
	}
}
