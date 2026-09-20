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
// the question, and the pair is loaded and carried into the request at runtime
// (Glenn, 2026-09-19: "if you inform it via input tokens what sort of criteria
// it should use to make decisions, I'm sure this will help it improve"; the
// ingestion itself is proved in internal/decide by a request-capture control,
// because text parity between two files on disk is not ingestion).
//
// This gate is the documentation half: every choice the question offers is
// explained in the criteria file, every typed state field it declares is
// documented there, the version the question cites is the version the file
// carries, and -- the repair Stella named -- the NORMATIVE pair names no
// person, no model and no house role. The roster belongs in an example.
type decideQuestionFile struct {
	CriteriaVersion string `json:"criteria_version"`
	CriteriaFile    string `json:"criteria_file"`
	// Machinery is the rules the question is answered UNDER. It is part of
	// the versioned pair, so the binding between a question and the
	// machinery that constrains its answer is a contract and not a name
	// match inside a verb.
	Machinery   string `json:"machinery"`
	StateFields []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Optional bool   `json:"optional"`
	} `json:"state_fields"`
	Questions map[string]struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	} `json:"questions"`
}

// houseNames are the names a normative question or criteria file must not
// carry: a contract that hard-codes one house's roster is not portable, and no
// model response creates or overrides an ownership binding.
var houseNames = []string{
	"Johnny", "johnny", "Stella", "stella", "Emma", "emma", "Rowan", "rowan",
	"Freddy", "freddy", "Astra", "astra", "Fable", "fable", "Glenn", "glenn",
	"Opus", "opus", "Sol ", "sol-", "opus-child", "fable-child", "DeepSeek", "deepseek",
	"jev-latest",
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
		if strings.ContainsAny(q.CriteriaFile, "/\\") {
			t.Errorf("%s names criteria_file %q; it sits BESIDE its question file, so the name carries no path", name, q.CriteriaFile)
		}
		if len(q.StateFields) == 0 {
			t.Errorf("%s declares no state_fields; the facts the asker computes first are the question's shape", name)
		}
		for _, sf := range q.StateFields {
			switch sf.Type {
			case "string", "int", "bool":
			default:
				t.Errorf("%s state field %q has type %q, want string, int or bool", name, sf.Name, sf.Type)
			}
		}
		critPath := filepath.Join(dir, q.CriteriaFile)
		crit, err := os.ReadFile(critPath)
		if err != nil {
			t.Errorf("%s cites %s: %v", name, q.CriteriaFile, err)
			continue
		}
		text := string(crit)
		if want := fmt.Sprintf("version: %s", q.CriteriaVersion); !strings.Contains(text, want) {
			t.Errorf("%s cites criteria_version %s and %s does not carry %q",
				name, q.CriteriaVersion, q.CriteriaFile, want)
		}
		// A question answered under machinery says so IN THE PAIR, and the
		// criteria file says what the machinery does. The who-reads question
		// is the one that has it: its rules are enforced at the call boundary
		// and an unstated binding is a rule nothing calls.
		switch q.Machinery {
		case "":
			if strings.Contains(name, "reader") {
				t.Errorf("%s asks the who-reads question and declares no machinery; the rules in internal/decide/readers.go would be prose again", name)
			}
		case "who-reads":
			if !strings.Contains(text, "machinery") {
				t.Errorf("%s declares machinery %q and %s never says what it does", name, q.Machinery, q.CriteriaFile)
			}
		default:
			t.Errorf("%s declares machinery %q, want who-reads or none", name, q.Machinery)
		}
		qname := strings.TrimSuffix(strings.TrimPrefix(name, "questions-"), ".json")
		if !strings.Contains(q.Questions[qname].Instructions, q.CriteriaFile) {
			t.Errorf("%s: the instructions do not name %s, so an asker has no way to know what to embed", name, q.CriteriaFile)
		}
		for qn, question := range q.Questions {
			if question.Type != "choice" {
				t.Errorf("%s question %s is type %q; these are choices", name, qn, question.Type)
			}
			if len(question.Criteria) == 0 {
				t.Errorf("%s question %s offers no criteria", name, qn)
			}
			for choice := range question.Criteria {
				if !strings.Contains(text, choice) {
					t.Errorf("%s question %s offers %q and %s never explains it", name, qn, choice, q.CriteriaFile)
				}
			}
		}
		for _, sf := range q.StateFields {
			if !strings.Contains(text, "`"+sf.Name+"`") {
				t.Errorf("%s declares state field %q and %s does not document it", name, sf.Name, q.CriteriaFile)
			}
		}
		// The portability repair: the normative pair carries no roster.
		for _, path := range []string{filepath.Join(dir, name), critPath} {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// `ask-glenn` is an EXISTING API token: internal/decide's
			// HelpAskGlenn, which `nova-decide help --state` prints today.
			// A criteria file must be able to name the value it gates on.
			// Renaming that constant to a role is owed and is not this
			// change; until then it is the one exception, and only in that
			// exact spelling.
			scrubbed := strings.ReplaceAll(string(body), "ask-glenn", "ask-<person>")
			for _, house := range houseNames {
				if strings.Contains(scrubbed, house) {
					t.Errorf("%s names %q; a normative question and its criteria carry configured ROLES, and the roster belongs in docs/decide/examples/", path, house)
				}
			}
		}
	}
	if seen < 3 {
		t.Errorf("found %d question files in docs/decide, want the reader, triage and escalate questions", seen)
	}
}

// The examples directory is where a house's own roster and its trial rows live,
// and it says out loud that it binds nothing.
func TestTheDecideExamplesSayTheyAreNotTheContract(t *testing.T) {
	matches, err := filepath.Glob("../../docs/decide/examples/*.md")
	if err != nil || len(matches) == 0 {
		t.Fatalf("want at least one example file, got %v (%v)", matches, err)
	}
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "not part of the contract") {
			t.Errorf("%s does not say it is not part of the contract", m)
		}
	}
}
