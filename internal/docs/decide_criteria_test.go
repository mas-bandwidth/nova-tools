package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The criteria a typed question is answered against are a VERSIONED file beside
// the question, and the asker embeds it (Glenn, 2026-09-19: "if you inform it
// via input tokens what sort of criteria it should use to make decisions, I'm
// sure this will help it improve"). Before this, twelve manager lanes each
// carried their own copy of the who-reads question and they had already drifted:
// work-swarm's named emma as the default and tools13's named opus-child, so the
// same card asked in two lanes was two different questions.
//
// This is the gate that keeps one question and one criteria file in lockstep:
// every choice the question offers is explained in the criteria file, every
// state field the question declares is documented there, and the version the
// question cites is the version the file carries.
type decideQuestionFile struct {
	CriteriaVersion string   `json:"criteria_version"`
	CriteriaFile    string   `json:"criteria_file"`
	StateFields     []string `json:"state_fields"`
	Questions       map[string]struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	} `json:"questions"`
}

func TestEveryDecideQuestionCarriesItsVersionedCriteria(t *testing.T) {
	const root = "../.."
	dir := filepath.Join(root, "docs", "decide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("docs/decide is where a typed question and its criteria live: %v", err)
	}
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "questions-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		seen++
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var q decideQuestionFile
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.TrimSpace(q.CriteriaVersion) == "" {
			t.Errorf("%s names no criteria_version; criteria that cannot be versioned cannot be tuned", name)
			continue
		}
		if strings.TrimSpace(q.CriteriaFile) == "" {
			t.Errorf("%s names no criteria_file", name)
			continue
		}
		if len(q.StateFields) == 0 {
			t.Errorf("%s declares no state_fields; the facts the asker computes first are the question's shape", name)
		}
		crit, err := os.ReadFile(filepath.Join(root, q.CriteriaFile))
		if err != nil {
			t.Errorf("%s cites %s: %v", name, q.CriteriaFile, err)
			continue
		}
		text := string(crit)
		if want := fmt.Sprintf("version: %s", q.CriteriaVersion); !strings.Contains(text, want) {
			t.Errorf("%s cites criteria_version %s and %s does not carry %q",
				name, q.CriteriaVersion, q.CriteriaFile, want)
		}
		if !strings.Contains(q.Questions[strings.TrimSuffix(strings.TrimPrefix(name, "questions-"), ".json")].Instructions, q.CriteriaFile) {
			t.Errorf("%s: the instructions do not name %s, so an asker has no way to know what to embed", name, q.CriteriaFile)
		}
		for qname, question := range q.Questions {
			if question.Type != "choice" {
				t.Errorf("%s question %s is type %q; these are choices", name, qname, question.Type)
			}
			if len(question.Criteria) == 0 {
				t.Errorf("%s question %s offers no criteria", name, qname)
			}
			for choice := range question.Criteria {
				if !strings.Contains(text, choice) {
					t.Errorf("%s question %s offers %q and %s never explains it", name, qname, choice, q.CriteriaFile)
				}
			}
		}
		for _, field := range q.StateFields {
			if !strings.Contains(text, "`"+field+"`") {
				t.Errorf("%s declares state field %q and %s does not document it", name, field, q.CriteriaFile)
			}
		}
	}
	if seen < 2 {
		t.Errorf("found %d question files in docs/decide, want the reader and the triage question at least", seen)
	}
}
