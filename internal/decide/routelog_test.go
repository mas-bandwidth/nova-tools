package decide

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The log is append-only JSON lines: every decision, its evidence, the rung it
// tried, what followed, and what Rowan would have picked.
func TestAppendEntryIsAppendOnlyJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	first := Entry{
		Unit:       "u1",
		Kind:       KindRebase,
		Evidence:   Unit{ID: "u1", Kind: KindRebase, Files: 2, Packages: 1},
		RungTried:  "flash",
		Height:     0,
		Confidence: 0.9,
		Floor:      DefaultFloor,
		Source:     SourceRules,
		RowanPick:  "flash",
	}
	if err := AppendEntry(path, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Unit = "u2"
	second.Outcome = OutcomeOK
	second.RungSucceeded = "flash"
	if err := AppendEntry(path, second); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two decisions are two lines, got %d:\n%s", len(lines), raw)
	}
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not one JSON object: %v", i+1, err)
		}
		if e.Time == "" {
			t.Errorf("line %d carries no time", i+1)
		}
		if e.Evidence.Kind != KindRebase {
			t.Errorf("line %d dropped the evidence", i+1)
		}
		if e.RowanPick == "" {
			t.Errorf("line %d does not say what Rowan would have picked", i+1)
		}
	}
	// The log is the tool's own, and what that MEANS is the platform's own.
	// Unix has the mode bit the tool asked for; Windows has no POSIX mode bits
	// at all -- Go reports every writable file there as 0666 whatever was
	// requested, and the real protection is an ACL this test cannot read -- so
	// asserting 0600 there was asserting something no Windows bench can be
	// true. The permission the tool REQUESTS is 0600 on both.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := fi.Mode().Perm()
	if runtime.GOOS == "windows" {
		if mode&0o200 == 0 {
			t.Errorf("the log the tool appends to is not writable: mode %v", mode)
		}
	} else if mode != 0o600 {
		t.Errorf("the log is the tool's own: mode %v", mode)
	}
}

// A log with no rows is no summary, not a guessed one.
func TestReadEntriesRefusesRubbish(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEntries(bad); err == nil {
		t.Error("a line that is not a JSON object parsed, want a refusal")
	}
	if _, err := ReadEntries(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a missing log parsed, want a refusal naming the path")
	}
}

// The summary counts the escalations per kind and regenerates the starting rung
// from the rows: the lowest rung that is carrying its own weight.
func TestSummaryRegeneratesTheStartingRung(t *testing.T) {
	reg := testRegistry(t)
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	entries := []Entry{
		{Unit: "a", Kind: KindRebase, RungTried: "flash", Height: 0, Outcome: OutcomeFailed, RowanPick: "flash", Evidence: Unit{ID: "a", Kind: KindRebase}},
		{Unit: "b", Kind: KindRebase, RungTried: "pro", Height: 1, Outcome: OutcomeOK, RungSucceeded: "pro", RowanPick: "pro", SteppedUp: true,
			Evidence: Unit{ID: "b", Kind: KindRebase, Attempts: []Attempt{{Rung: "flash", Outcome: OutcomeFailed}}}},
		{Unit: "c", Kind: KindRebase, RungTried: "pro", Height: 1, Outcome: OutcomeOK, RungSucceeded: "pro", RowanPick: "pro",
			Evidence: Unit{ID: "c", Kind: KindRebase, Attempts: []Attempt{{Rung: "flash", Outcome: OutcomeFailed}}}},
		{Unit: "d", Kind: KindSpec, RungTried: "fable", Height: 4, Outcome: OutcomeOK, RungSucceeded: "fable", RowanPick: "fable",
			Evidence: Unit{ID: "d", Kind: KindSpec}},
	}
	for _, e := range entries {
		if err := AppendEntry(path, e); err != nil {
			t.Fatal(err)
		}
	}
	read, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := Summarize(reg, read)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]KindSummary{}
	for _, k := range sum.Kinds {
		byKind[k.Kind] = k
	}
	rebase, ok := byKind[KindRebase]
	if !ok {
		t.Fatalf("no rebase row in the summary: %+v", sum.Kinds)
	}
	if rebase.Decisions != 3 {
		t.Errorf("rebase decisions = %d, want 3", rebase.Decisions)
	}
	if rebase.Escalations != 2 {
		t.Errorf("rebase escalations = %d, want 2 (the two rows that carried a failed attempt)", rebase.Escalations)
	}
	if rebase.StartHeight != 1 || rebase.StartRung != "pro" {
		t.Errorf("flash failed three times and pro carried it twice: start = %s at %d, want pro at 1", rebase.StartRung, rebase.StartHeight)
	}
	if rebase.DefaultRung == rebase.StartRung {
		t.Errorf("the regenerated rung must be reported beside the default it replaces (both %s)", rebase.DefaultRung)
	}
	spec, ok := byKind[KindSpec]
	if !ok {
		t.Fatal("no spec row in the summary")
	}
	if spec.StartHeight != 4 {
		t.Errorf("spec start height = %d, want 4", spec.StartHeight)
	}
	render := sum.Render()
	for _, want := range []string{"LOG kind=rebase", "escalations=2", "start_rung=pro", "LOG OK"} {
		if !strings.Contains(render, want) {
			t.Errorf("the summary is missing %q:\n%s", want, render)
		}
	}
	if strings.Count(strings.TrimSuffix(render, "\n"), "\n") != len(sum.Kinds) {
		t.Errorf("one line per kind then the finish:\n%s", render)
	}
}

// A kind with no success keeps the rung it started from: a starting rung with
// no rows behind it is not regenerated.
func TestSummaryKeepsTheDefaultWithNoSuccess(t *testing.T) {
	reg := testRegistry(t)
	sum, err := Summarize(reg, []Entry{
		{Unit: "a", Kind: KindNewVerb, RungTried: "opus", Height: 2, Outcome: OutcomeFailed, RowanPick: "opus", Evidence: Unit{ID: "a", Kind: KindNewVerb}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Kinds) != 1 {
		t.Fatalf("one kind, got %d", len(sum.Kinds))
	}
	k := sum.Kinds[0]
	if k.StartRung != k.DefaultRung {
		t.Errorf("no success: start = %s, want the default %s", k.StartRung, k.DefaultRung)
	}
}
