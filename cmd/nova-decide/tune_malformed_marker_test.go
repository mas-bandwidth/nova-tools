package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// ABSENT IS NOT THE SAME AS INVALID.
//
// Stella's expanded Go read of #1925 at 352dad03: the adjudication boundary
// asked `boolField` whether a row said `adjudicated`, and `boolField` answered
// `(false, false)` for a field that was absent AND for a field that was
// present and not a boolean. So a twelve-row log carrying
// `"adjudicated": "maybe"` was read as a log from before the field existed and
// tuned: `TUNE OK ... best_floor=0.5`, exit 0.
//
// An absent field is a historical format and is read as it always was. A
// PRESENT field that is not a JSON boolean is a row whose adjudication status
// nobody can read, which is not the same as a row that never claimed one, and
// it is a refusal naming the line and the field -- under `--observations` too,
// because that flag admits a log that says it is observations, not a log that
// says nothing legible at all.
func TestAPresentButMalformedAdjudicationMarkerRefusesAndNeverTunes(t *testing.T) {
	for _, marker := range []string{`"maybe"`, `null`, `{}`, `[]`, `1`, `"true"`} {
		t.Run("adjudicated:"+marker, func(t *testing.T) {
			log := markerLog(t, `,"adjudicated":`+marker)
			for _, extra := range [][]string{nil, {"--observations"}} {
				var out, errb bytes.Buffer
				args := append([]string{"tune", "--decisions", log, "--floors", "0.5,0.9"}, extra...)
				code := run(args, &out, &errb)
				if code != 2 {
					t.Fatalf("args %v exited %d, want 2; stdout=%q", extra, code, out.String())
				}
				if strings.Contains(out.String(), "best_floor") {
					t.Errorf("a row nobody can read the adjudication of recommended a floor:\n%s", out.String())
				}
				for _, want := range []string{"adjudicated", "line 1"} {
					if !strings.Contains(errb.String(), want) {
						t.Errorf("the refusal does not name %q; got %q", want, errb.String())
					}
				}
			}
		})
	}

	// And what must NOT move: a genuine boolean either way, and a log from
	// before the field existed.
	t.Run("a genuine true still tunes", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"tune", "--decisions", markerLog(t, `,"adjudicated":true`), "--floors", "0.5,0.9"}, &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "TUNE OK") {
			t.Errorf("adjudicated truth stopped tuning:\n%s", out.String())
		}
	})
	t.Run("a genuine false is the adjudication refusal, not the malformed one", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"tune", "--decisions", markerLog(t, `,"adjudicated":false`), "--floors", "0.5,0.9"}, &out, &errb); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "not-adjudicated") {
			t.Errorf("a legible false must keep its own reason; got %q", errb.String())
		}
		out.Reset()
		errb.Reset()
		if code := run([]string{"tune", "--decisions", markerLog(t, `,"adjudicated":false`), "--observations", "--floors", "0.5,0.9"}, &out, &errb); code != 0 {
			t.Fatalf("--observations on a legible false exited %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "best_floor=none") {
			t.Errorf("the observation mode changed:\n%s", out.String())
		}
	})
	t.Run("an absent field is still a historical log", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"tune", "--decisions", markerLog(t, ``), "--floors", "0.5,0.9"}, &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "TUNE OK") {
			t.Errorf("a log predating the field stopped tuning:\n%s", out.String())
		}
	})
}

// markerLog is a twelve-row decisions log, every row carrying the given
// suffix. Twelve is over decide.MinLabeled, so nothing here is refused for
// being too small: what is tested is the marker and nothing else.
func markerLog(t *testing.T, suffix string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < decideMinLabeled+2; i++ {
		fmt.Fprintf(&b, `{"decision":"child-review","label":"child-review","confidence":%.2f%s}`+"\n", 0.55+float64(i%5)*0.1, suffix)
	}
	path := filepath.Join(t.TempDir(), "marker.jsonl")
	write(t, path, b.String())
	return path
}
