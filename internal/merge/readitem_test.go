package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadItemKeepsTheLegacyPathAndBytes(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	sub := Submission{At: "2026-09-14T01:02:03Z", Rand: "a1b2c3"}
	item, err := ReadItem("rowan%2Fwire-probe", "emma", head, "hold", "line one\nline two", sub)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := "reads/rowan%2Fwire-probe/emma-aaaaaaaaaaaa-20260914T010203Z-a1b2c3.json"
	if item.Path != wantPath {
		t.Fatalf("path = %q, want %q", item.Path, wantPath)
	}
	wantBody := "{\n" +
		"  \"who\": \"emma\",\n" +
		"  \"verdict\": \"hold\",\n" +
		"  \"note\": \"line one\\nline two\",\n" +
		"  \"at\": \"2026-09-14T01:02:03Z\",\n" +
		"  \"head\": \"" + head + "\",\n" +
		"  \"file\": \"" + wantPath + "\"\n" +
		"}\n"
	if string(item.Body) != wantBody {
		t.Fatalf("body changed:\nwant %s\ngot  %s", wantBody, item.Body)
	}
}

func TestReadItemRefusesInvalidReadShape(t *testing.T) {
	t.Parallel()
	sub := Submission{At: "2026-09-14T01:02:03Z", Rand: "a1b2c3"}
	for _, tc := range []struct {
		name, who, head, verdict string
	}{
		{"empty who", "", strings.Repeat("a", 40), "approve"},
		{"bad head", "emma", "short", "approve"},
		{"bad verdict", "emma", strings.Repeat("a", 40), "abstain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item, err := ReadItem("951", tc.who, tc.head, tc.verdict, "", sub)
			if err == nil {
				t.Fatalf("ReadItem returned item %+v for invalid input", item)
			}
			if item.Path != "" || len(item.Body) != 0 {
				t.Fatalf("invalid input returned an item: %+v", item)
			}
		})
	}
}

func TestEvaluateReadsRetainsParserHoldAfterDocsScopedApprove(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	entry := &Entry{
		PR:        1,
		OID:       head,
		NeedsRead: "no",
		Reads: []Read{
			{
				Who:     "rowan",
				Verdict: "hold",
				Scope:   "parser",
				Head:    head,
				At:      "2026-09-19T10:00:00Z",
			},
			{
				Who:      "rowan",
				Verdict:  "approve",
				Scope:    "docs",
				Head:     head,
				At:       "2026-09-19T10:05:00Z",
				Releases: []string{},
			},
		},
	}

	st := EvaluateReads(entry, "author")
	if !st.Held {
		t.Fatalf("expected st.Held to be true (parser hold must not be wiped by docs approve), got: %+v", st)
	}
	if st.Holds != 1 {
		t.Fatalf("expected st.Holds to be 1, got: %d", st.Holds)
	}
	if st.Satisfied {
		t.Fatalf("held entry must not be satisfied, got: %+v", st)
	}
}

func TestEvaluateReadsCarriesUnresolvedStaleHeadHold(t *testing.T) {
	t.Parallel()
	h1 := strings.Repeat("1", 40)
	h2 := strings.Repeat("2", 40)

	// Stella witness: an H1 HOLD followed by a push to H2 with NeedsRead=no.
	// Must return Held:true, Holds:1, Satisfied:false.
	entry := &Entry{
		PR:        1,
		OID:       h2,
		NeedsRead: "no",
		Reads: []Read{
			{
				Who:     "stella",
				Verdict: "hold",
				Head:    h1,
				At:      "2026-09-19T10:00:00Z",
			},
		},
	}

	st := EvaluateReads(entry, "author")
	if !st.Held {
		t.Fatalf("unresolved H1 hold must carry to H2, got Held=false: %+v", st)
	}
	if st.Holds != 1 {
		t.Fatalf("expected Holds=1, got: %d", st.Holds)
	}
	if st.Satisfied {
		t.Fatalf("unresolved H1 hold must prevent Satisfied=true under NeedsRead=no: %+v", st)
	}

	// Now add an unscoped APPROVE at H2: hold is cleared, and NeedsRead=no becomes satisfied.
	entry.Reads = append(entry.Reads, Read{
		Who:     "stella",
		Verdict: "approve",
		Head:    h2,
		At:      "2026-09-19T10:10:00Z",
	})
	stCleared := EvaluateReads(entry, "author")
	if stCleared.Held {
		t.Fatalf("current-head unscoped approve must clear prior-head hold: %+v", stCleared)
	}
	if stCleared.Holds != 0 {
		t.Fatalf("expected Holds=0 after clearance, got: %d", stCleared.Holds)
	}
	if !stCleared.Satisfied {
		t.Fatalf("expected Satisfied=true once hold is cleared under NeedsRead=no: %+v", stCleared)
	}
}

func TestLoadLaneVerdictsPropagatesErrors(t *testing.T) {
	t.Parallel()

	// 1. Missing lane directory returns error
	missingDir := filepath.Join(t.TempDir(), "nonexistent-lane")
	vs, err := LoadLaneVerdicts(missingDir, 1)
	if err == nil {
		t.Fatalf("LoadLaneVerdicts on nonexistent lane directory must return error, got vs=%v", vs)
	}

	// 2. Corrupt JSON returns error naming the file
	laneDir := t.TempDir()
	readsDir := filepath.Join(laneDir, ReadsDir, "1")
	if err := os.MkdirAll(readsDir, 0755); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(readsDir, "corrupt.json")
	if err := os.WriteFile(badFile, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	vs, err = LoadLaneVerdicts(laneDir, 1)
	if err == nil {
		t.Fatalf("LoadLaneVerdicts on corrupt JSON file must return error, got vs=%v", vs)
	}
	if !strings.Contains(err.Error(), "corrupt.json") {
		t.Fatalf("LoadLaneVerdicts error must name the bad file: %v", err)
	}
}

func TestLoadLaneVerdictsRefusesLaneNone(t *testing.T) {
	t.Parallel()
	vs, err := LoadLaneVerdicts("none", 1)
	if err == nil {
		t.Fatalf("LoadLaneVerdicts on 'none' must return error, got vs=%v", vs)
	}
	if !strings.Contains(err.Error(), "none") {
		t.Fatalf("LoadLaneVerdicts error must name 'none': %v", err)
	}
}
