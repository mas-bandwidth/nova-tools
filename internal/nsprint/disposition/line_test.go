package disposition

import "testing"

func TestParseTypedLines(t *testing.T) {
	h := "B2D830D36BEF4D7D907B461E7A176C6009996D9D"
	for _, tc := range []struct {
		body, want string
	}{
		{"DISPOSITION who=stella head=" + h + " verdict=HOLD score=7\n\nwhy", "RECORD"},
		{"DISPOSITION who=stella head=" + h + " verdict=APPROVE score=10/10", "RECORD"},
		{"DISPOSITION who=stella head=" + h + " verdict=NOTE score=5", "NORECORD verdict=NOTE"},
		{"DISPOSITION who=stella HOLD #3092 at 2ae0c7c7 score=6", "REFUSED bare token HOLD"},
		{"DISPOSITION who=stella who=emma head=" + h + " verdict=HOLD score=7", "REFUSED repeated key who"},
		{"DISPOSITION who=stella head=" + h + " verdict=HOLD score=7 color=red", "REFUSED unknown-key color"},
		{"DISPOSITION who=stella verdict=HOLD score=7", "REFUSED missing head"},
		{"DISPOSITION who=stella head=" + h + " verdict=HOLD score=11", "REFUSED score not 1-10"},
		{"REPAIR who=rowan head=" + h + " ready=false: later", "NORECORD ready=false"},
		{"> DISPOSITION who=stella head=" + h + " verdict=HOLD score=7", "NORECORD prose"},
		{"disposition who=stella", "NORECORD prose"},
	} {
		if got := Parse(tc.body).String(); got != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
	r := Parse("DISPOSITION who=stella head=" + h + " verdict=APPROVE score=10: tail text\nbody")
	if r.Line.Score != 10 || r.Line.Tail != "tail text" || r.Line.Reason != "tail text\nbody" || r.Line.Head != "b2d830d36bef4d7d907b461e7a176c6009996d9d" {
		t.Fatalf("tail parse: %+v", r.Line)
	}
}

func TestClassify(t *testing.T) {
	for reason, want := range map[string]string{
		"CI is red on shard 2. Pending rerun.":      "ci",
		"red at internal/x/y.go:12 on the shard":    "substance",
		"waits on #3091 landing first":              "order",
		"waits on #7 and the parser is wrong. Fix.": "substance",
		"the helper overstates its result":          "substance",
	} {
		if got := Classify(reason, 7); got != want {
			t.Errorf("Classify(%q) = %s, want %s", reason, got, want)
		}
	}
}
