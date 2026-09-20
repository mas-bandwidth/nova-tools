package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// An observation log cannot bless a floor.
//
// Stella's r2 hold (#1925, e1198bab): the prose withdrawal landed -- the
// fixture is named `reader-observations-*`, every row carries
// `adjudicated: false`, and the README says it tunes nothing -- but the VERB
// still read the file, ignored that status, and printed
// `TUNE OK lines=47 labeled=47 best_floor=0.5` at exit 0. A boundary that
// exists only in prose is not a boundary.
//
// This control is the real CLI entry on the file the repository actually
// ships. The arithmetic stays available, under a flag that says out loud it
// recommends nothing; adjudicated truth, and logs from before the field
// existed, are untouched.
const bundledObservations = "../../internal/decide/testdata/reader-observations-2026-09-19.jsonl"

func TestTheBundledObservationsCannotBlessAFloorThroughTheRealCLI(t *testing.T) {
	floors := []string{"--floors", "0.5,0.65,0.8,0.9", "--max-escalation", "0.7"}

	// 1. The refusal. No floor, no exit 0, and the reason names the status.
	var out, errb bytes.Buffer
	args := append([]string{"tune", "--decisions", bundledObservations, "--default", "opus-child"}, floors...)
	if code := run(args, &out, &errb); code != 2 {
		t.Fatalf("the bundled observations exited %d, want 2; stdout=%q", code, out.String())
	}
	if strings.Contains(out.String(), "best_floor") {
		t.Errorf("a non-adjudicated log recommended a floor:\n%s", out.String())
	}
	for _, want := range []string{"adjudicated", "--observations"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the refusal does not name %q; got %q", want, errb.String())
		}
	}

	// 2. The explicitly non-recommending mode: the arithmetic, and no floor.
	out.Reset()
	errb.Reset()
	args = append([]string{"tune", "--decisions", bundledObservations, "--default", "opus-child", "--observations"}, floors...)
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("--observations exited %d: %s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "TUNE OK") {
		t.Errorf("observations printed the OK line a tuned floor earns:\n%s", got)
	}
	if !strings.Contains(got, "best_floor=none") {
		t.Errorf("observations must say best_floor=none:\n%s", got)
	}
	if !strings.Contains(got, "observations=47") {
		t.Errorf("the observation count is the reason there is no floor:\n%s", got)
	}
	// The arithmetic Stella asked to preserve is still all there.
	if !strings.Contains(got, "floor=0.5 decided=40 agree=27 agree_rate=0.68 escalated=7 escalation_rate=0.15 defaulted=7 default_agree=7 missed=0") {
		t.Errorf("the per-floor arithmetic changed:\n%s", got)
	}

	// 3. Adjudicated truth still tunes, and still prints a floor.
	out.Reset()
	errb.Reset()
	if code := run([]string{"tune", "--decisions", tuneLog(t, "truth.jsonl", `,"adjudicated":true`), "--floors", "0.5,0.9"}, &out, &errb); code != 0 {
		t.Fatalf("an adjudicated log exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "TUNE OK") || strings.Contains(out.String(), "best_floor=none") {
		t.Errorf("adjudicated truth must still tune:\n%s", out.String())
	}

	// 4. And a log from before the field existed is read exactly as it was:
	// a historical format is not silently reinterpreted.
	out.Reset()
	errb.Reset()
	if code := run([]string{"tune", "--decisions", tuneLog(t, "historical.jsonl", ""), "--floors", "0.5,0.9"}, &out, &errb); code != 0 {
		t.Fatalf("a log predating the field exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "TUNE OK") || strings.Contains(out.String(), "best_floor=none") {
		t.Errorf("a log with no adjudicated field must tune as it always did:\n%s", out.String())
	}
}

// tuneLog writes a small decisions log, every row carrying the suffix given
// (the adjudicated field, or nothing at all for the historical shape).
func tuneLog(t *testing.T, name, suffix string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < decideMinLabeled+2; i++ {
		answer := "child-review"
		if i%3 == 0 {
			answer = "design-authority"
		}
		fmt.Fprintf(&b, `{"decision":%q,"label":%q,"confidence":%.2f%s}`+"\n", answer, answer, 0.55+float64(i%5)*0.1, suffix)
	}
	path := filepath.Join(t.TempDir(), name)
	write(t, path, b.String())
	return path
}

// decideMinLabeled mirrors decide.MinLabeled so the helper writes enough rows
// to be tunable at all.
const decideMinLabeled = 10
