// Red tests for rule 8's tuning read, written before the implementation.
//
// A decisions log is JSONL, one row per decision, each row joining the
// decision, its confidence and the outcome (the label). Tune reports, per
// confidence floor, how many rows the floor decided, how many of those agreed
// with their label, and how many it escalated, then picks the best floor.
package decide

import (
	"strings"
	"testing"
)

// tonightShape is tonight's 30 labeled rows: `right` decisions at confidence
// 0.93 that agree with their label, and `wrong` at 0.40 that do not.
func tonightShape(right, wrong int) []byte {
	var b strings.Builder
	for i := 0; i < right; i++ {
		b.WriteString(`{"decision":"abstain","confidence":0.93,"label":"abstain"}` + "\n")
	}
	for i := 0; i < wrong; i++ {
		b.WriteString(`{"decision":"abstain","confidence":0.40,"label":"needs_human"}` + "\n")
	}
	return []byte(b.String())
}

func statAt(t *testing.T, res TuneResult, floor float64) FloorStat {
	t.Helper()
	for _, st := range res.Floors {
		if st.Floor == floor {
			return st
		}
	}
	t.Fatalf("no stat for floor %g: %+v", floor, res.Floors)
	return FloorStat{}
}

// Rule 8: tonight's shape sets the floor from data — floor 0.9 agrees 9/9
// (rate 1.0) and escalates the 21 wrong below it (rate 0.7).
func TestTuneTonightShape(t *testing.T) {
	res, err := Tune(tonightShape(9, 21), TuneOptions{})
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if res.Lines != 30 || res.Labeled != 30 {
		t.Fatalf("lines/labeled = %d/%d, want 30/30", res.Lines, res.Labeled)
	}
	st := statAt(t, res, 0.9)
	if st.Decided != 9 || st.Agree != 9 || st.Escalated != 21 {
		t.Fatalf("floor 0.9 stat = %+v, want decided=9 agree=9 escalated=21", st)
	}
	line := res.Render()
	if !strings.Contains(line, "TUNE floor=0.9 decided=9 agree=9 agree_rate=1.00 escalated=21 escalation_rate=0.70") {
		t.Fatalf("floor line wrong:\n%s", line)
	}
	if !strings.Contains(line, "TUNE OK lines=30 labeled=30 best_floor=0.9") {
		t.Fatalf("final line wrong:\n%s", line)
	}
}

// Rows without a label are counted in lines but skipped as unlabeled.
func TestTuneCountsUnlabeled(t *testing.T) {
	data := append(tonightShape(9, 21), []byte(`{"decision":"abstain","confidence":0.95}`+"\n")...)
	res, err := Tune(data, TuneOptions{})
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if res.Lines != 31 || res.Labeled != 30 {
		t.Fatalf("lines/labeled = %d/%d, want 31/30", res.Lines, res.Labeled)
	}
}

// The field names are configurable.
func TestTuneCustomFields(t *testing.T) {
	res, err := Tune([]byte(`{"pick":"go","conf":0.95,"outcome":"go"}`+"\n"), TuneOptions{
		Choice: "pick", Conf: "conf", Label: "outcome", Floors: []float64{0.9},
	})
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	st := statAt(t, res, 0.9)
	if st.Decided != 1 || st.Agree != 1 || st.Escalated != 0 {
		t.Fatalf("custom-fields stat = %+v, want decided=1 agree=1 escalated=0", st)
	}
}

// A line that is not a JSON object is refused, never guessed.
func TestTuneRefusesBadLine(t *testing.T) {
	if _, err := Tune([]byte("not json\n"), TuneOptions{}); err == nil {
		t.Fatal("expected refusal on a line that is not JSON")
	}
}
