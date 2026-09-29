package definition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseCard is a valid card whose lines the refusal table edits. Its line numbers:
// 1 contract, 2 SCHEMA, 3 ID, 4 TITLE, 5 KIND, 6 PATHS, 7 DEPENDS-ON, 8 TIER,
// 9 TEST, 10 DONE-WHEN, 11 DOORS, 12 PROBES, 13 blank, 14 heading, 15 blank, 16 text.
const baseCard = `RESULT: card-alpha sha=0123456789ab
SCHEMA: v2
ID: card-alpha
TITLE: Reject an empty queue name
KIND: fix-red
PATHS: internal/queue/name.go, internal/queue/name_test.go
DEPENDS-ON: -
TIER: flash
TEST: internal/queue TestNameRefusesEmpty
DONE-WHEN: The named test fails at the base and passes at the head.
DOORS: none
PROBES: none

# Reject an empty queue name

The queue constructor refuses an empty name.
`

func baseLines() []string { return strings.Split(strings.TrimSuffix(baseCard, "\n"), "\n") }

func join(ls []string) string { return strings.Join(ls, "\n") + "\n" }

// edit applies one change to the base card's lines.
func edit(fn func(ls []string) []string) string { return join(fn(baseLines())) }

func replaceLine(n int, text string) string {
	return edit(func(ls []string) []string { ls[n-1] = text; return ls })
}

func insertAfter(n int, text string) string {
	return edit(func(ls []string) []string {
		return append(ls[:n], append([]string{text}, ls[n:]...)...)
	})
}

func deleteLine(n int) string {
	return edit(func(ls []string) []string { return append(ls[:n-1], ls[n:]...) })
}

func one(name, text string) []Source { return []Source{{Name: name, Data: []byte(text)}} }

func mustParse(t *testing.T, srcs []Source) []Definition {
	t.Helper()
	defs, refs := Parse(srcs)
	if len(refs) > 0 {
		t.Fatalf("Parse refused: %v", Lines(refs))
	}
	return defs
}

func readTestdata(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hasRefusal(rs []Refusal, file string, line int, key string, cause Cause) (Refusal, bool) {
	for _, r := range rs {
		if r.File == file && r.Line == line && r.Key == key && r.Cause == cause {
			return r, true
		}
	}
	return Refusal{}, false
}

// wellFormed checks the shape every refusal has: an operation, a cause, what was
// found, a next action, and a rendering that is one line.
func wellFormed(t *testing.T, r Refusal) {
	t.Helper()
	if r.Operation == "" || r.Cause == "" || r.Found == "" || r.Next == "" {
		t.Errorf("refusal is missing a field: %+v", r)
	}
	s := r.String()
	if strings.ContainsAny(s, "\n\r") || !strings.HasPrefix(s, "REFUSED "+r.Operation) || !strings.Contains(s, "cause="+string(r.Cause)) {
		t.Errorf("refusal renders badly: %q", s)
	}
}
