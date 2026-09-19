package merge

import (
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
