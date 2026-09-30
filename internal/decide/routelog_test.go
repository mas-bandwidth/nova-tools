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
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decide.jsonl")
	first := Entry{
		Unit:       "u1",
		Kind:       KindRebase,
		Evidence:   Unit{ID: "u1", Kind: KindRebase, Files: 2, Packages: 1},
		RungTried:  "flash",
		Height:     0,
		Confidence: measured(0.9),
		Floor:      measured(DefaultFloor),
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
