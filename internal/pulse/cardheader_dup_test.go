package pulse

// The red team's "noticed" on the card header (report of T03 at 98e3f3a9, item 7): a
// duplicate `KIND:` was last-wins and duplicate `PATHS:` lines accumulated. A header the
// gate and `lint --card` can read two different ways is a header a card writer can aim
// at one reader and hide from the other, so a repeated key is refused outright. Only
// `TEST-EDIT:` repeats, because the spec says it repeats.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHeaderCard(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadCardHeaderRefusesARepeatedKey(t *testing.T) {
	const head = "RESULT card-1 done\n"
	for _, tc := range []struct{ name, body, want string }{
		{"KIND twice", head + "KIND: read\nKIND: fix-red\nPATHS: a/**\nTEST: a TestX\n", "KIND"},
		{"PATHS twice", head + "KIND: fix-red\nPATHS: a/**\nPATHS: b/**\nTEST: a TestX\n", "PATHS"},
		{"TEST twice", head + "KIND: fix-red\nPATHS: a/**\nTEST: a TestX\nTEST: none\n", "TEST"},
		{"LEGS twice", head + "KIND: fix-red\nPATHS: a/**\nTEST: a TestX\nLEGS: go\nLEGS: windows\n", "LEGS"},
		{"SOURCE twice", head + "KIND: fix-red\nPATHS: a/**\nTEST: a TestX\nSOURCE: o/r#1\nSOURCE: o/r#2\n", "SOURCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadCardHeader(writeHeaderCard(t, tc.body))
			if err == nil {
				t.Fatalf("a repeated %s: was read without a word; the gate and lint --card would each pick one", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the repeated line: %v", err)
			}
		})
	}
}

func TestReadCardHeaderStillRepeatsTestEdit(t *testing.T) {
	body := "RESULT card-1 done\nKIND: fix-red\nPATHS: a/**\nTEST: a TestX\nTEST-EDIT: a/one_test.go\nTEST-EDIT: a/two_test.go\n"
	h, err := ReadCardHeader(writeHeaderCard(t, body))
	if err != nil {
		t.Fatalf("TEST-EDIT: repeats by the spec: %v", err)
	}
	if len(h.TestEdits) != 2 {
		t.Errorf("TestEdits = %v, want both files", h.TestEdits)
	}
}
