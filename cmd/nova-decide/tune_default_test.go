package main

import (
	"bytes"
	"strings"
	"testing"
)

// The flag that carries the fallback into the verb. The fixture is an
// OBSERVATION log -- internal/decide/testdata/reader-observations-2026-09-19.jsonl,
// whose README says it tunes nothing -- and these assertions are about the
// arithmetic the flag adds, not about any floor.
func TestTuneDefaultFlagPrintsWhatTheDefaultAnsweredDifferently(t *testing.T) {
	const log = "../../internal/decide/testdata/reader-observations-2026-09-19.jsonl"
	var out, errb bytes.Buffer
	if code := run([]string{"tune", "--decisions", log, "--floors", "0.5,0.65,0.8,0.9", "--max-escalation", "0.7"}, &out, &errb); code != 0 {
		t.Fatalf("tune without a default exited %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "missed=") {
		t.Errorf("no default named means no miss count to print; got:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"tune", "--decisions", log, "--floors", "0.5,0.65,0.8,0.9", "--max-escalation", "0.7", "--default", "opus-child"}, &out, &errb); code != 0 {
		t.Fatalf("tune --default exited %d: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"floor=0.5 decided=40 agree=27 agree_rate=0.68 escalated=7 escalation_rate=0.15 defaulted=7 default_agree=7 missed=0",
		"floor=0.9 decided=22 agree=19 agree_rate=0.86 escalated=25 escalation_rate=0.53 defaulted=25 default_agree=21 missed=4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tune --default is missing %q; got:\n%s", want, got)
		}
	}
}

// A default nothing in the log ever answered is a refusal with a remedy, and
// the exit code is the verb's bad-decisions 2, never a silent 0.
func TestTuneDefaultNoRowAnsweredIsRefused(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"tune", "--decisions", "../../internal/decide/testdata/reader-observations-2026-09-19.jsonl", "--default", "a-reader-nobody-named"}, &out, &errb)
	if code != 2 {
		t.Fatalf("a default nothing answered exited %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "refusing to guess") {
		t.Errorf("the refusal says why and what to do; got %q", errb.String())
	}
}
