package decide

import (
	"os"
	"strings"
	"testing"
)

// A question file that carries its own criteria version is still a question
// file.
//
// The envelope `{"questions": {...}}` was accepted only when it was the ONLY
// top-level key, so a file that also names the criteria it is answered against
// -- `criteria_version`, `criteria_file`, `state_fields` -- fell through to the
// bare form and every one of those keys was read as a question:
// `question "criteria_version" has no instructions`. The three question files
// this repository ships are exactly that shape.
func TestAQuestionFileMayCarryItsCriteriaMetadata(t *testing.T) {
	for _, path := range []string{
		"../../docs/decide/questions-reader.json",
		"../../docs/decide/questions-triage.json",
		"../../docs/decide/questions-escalate.json",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		qs, err := ParseQuestions(raw)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if len(qs) != 1 {
			t.Errorf("%s holds %d questions, want 1", path, len(qs))
		}
		for name, q := range qs {
			if len(q.Choice) == 0 {
				t.Errorf("%s question %s offers no choices", path, name)
			}
			if strings.TrimSpace(q.Instructions) == "" {
				t.Errorf("%s question %s has no instructions", path, name)
			}
		}
	}
}

// A key beside the envelope that is NOT one of the four the metadata holds is a
// refusal that names it, never a question read by accident.
func TestAnUnknownKeyBesideTheEnvelopeIsRefusedByName(t *testing.T) {
	_, err := ParseQuestions([]byte(`{"questions":{"q":{"type":"choice","instructions":"i","criteria":{"a":"b"}}},"criteria_versionn":"x"}`))
	if err == nil {
		t.Fatal("a misspelled metadata key must be refused, not read as a question")
	}
	if !strings.Contains(err.Error(), "criteria_versionn") {
		t.Errorf("the refusal names the key it did not know; got %v", err)
	}
}
