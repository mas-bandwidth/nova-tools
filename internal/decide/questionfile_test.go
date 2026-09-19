package decide

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repair 1 (Stella, stella-c30b89a2d9f4, 2026-09-19): the runtime boundary was
// claimed and not real. ParseQuestions admitted criteria_version, criteria_file
// and state_fields and then DISCARDED them; the request carried the caller's
// state and the compact questions and nothing else, so the long criteria file
// the question named was never loaded, never embedded and never validated
// against. The docs gate proved text parity between two files on disk, which is
// not ingestion.
//
// LoadQuestionFile carries the pair through the boundary, and Payload is what
// actually goes out.

func writeQuestionFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodQuestions = `{
  "criteria_version": "test.1",
  "criteria_file": "criteria-x.md",
  "state_fields": [
    {"name": "security_shaped_package", "type": "bool"},
    {"name": "design_defaults_taken", "type": "int"},
    {"name": "note", "type": "string", "optional": true}
  ],
  "questions": {"x": {"type": "choice", "instructions": "pick", "criteria": {"a": "the a", "b": "the b"}}}
}`

func TestLoadQuestionFileCarriesTheCriteriaAndTheTypedFields(t *testing.T) {
	dir := t.TempDir()
	p := writeQuestionFile(t, dir, "questions-x.json", goodQuestions)
	writeQuestionFile(t, dir, "criteria-x.md", "version: test.1\nTHE LONG CRITERIA BODY\n")
	f, err := LoadQuestionFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.CriteriaVersion != "test.1" {
		t.Errorf("criteria version %q", f.CriteriaVersion)
	}
	if !strings.Contains(f.Criteria, "THE LONG CRITERIA BODY") {
		t.Errorf("the criteria file was not loaded: %q", f.Criteria)
	}
	if len(f.StateFields) != 3 {
		t.Fatalf("%d state fields, want 3", len(f.StateFields))
	}
	if len(f.Questions) != 1 {
		t.Errorf("%d questions, want 1", len(f.Questions))
	}
}

// A criteria file whose version line disagrees with the question's is refused
// BEFORE any provider call: a pair that is not one pair is not a pair.
func TestACriteriaFileOfAnotherVersionIsRefusedBeforeTheCall(t *testing.T) {
	dir := t.TempDir()
	p := writeQuestionFile(t, dir, "questions-x.json", goodQuestions)
	writeQuestionFile(t, dir, "criteria-x.md", "version: test.2\nbody\n")
	if _, err := LoadQuestionFile(p); err == nil || !strings.Contains(err.Error(), "test.2") {
		t.Fatalf("want a refusal naming the version found, got %v", err)
	}
}

// The referenced file is resolved INSIDE the question file's own directory. An
// absolute path, a parent escape or a symlink out is a refusal: a question file
// must not be a way to read an unrelated local file and post it to a provider.
func TestACriteriaPathThatLeavesTheQuestionDirectoryIsRefused(t *testing.T) {
	for _, bad := range []string{"../secret.md", "/etc/hosts", "sub/../../secret.md"} {
		dir := t.TempDir()
		body := strings.Replace(goodQuestions, `"criteria-x.md"`, `"`+bad+`"`, 1)
		p := writeQuestionFile(t, dir, "questions-x.json", body)
		if _, err := LoadQuestionFile(p); err == nil {
			t.Errorf("criteria_file %q was accepted; it leaves the question file's directory", bad)
		}
	}
	// A symlink pointing out is the same refusal, taken on the resolved path.
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("version: test.1\nSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := writeQuestionFile(t, dir, "questions-x.json", goodQuestions)
	if err := os.Symlink(outside, filepath.Join(dir, "criteria-x.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadQuestionFile(p); err == nil {
		t.Error("a criteria_file symlinked out of the question directory was accepted")
	}
}

// Payload is the refusal gate: a required typed field the state does not carry,
// or carries with the wrong type, refuses BEFORE the provider is dialled.
func TestPayloadRefusesAStateMissingARequiredTypedField(t *testing.T) {
	dir := t.TempDir()
	p := writeQuestionFile(t, dir, "questions-x.json", goodQuestions)
	writeQuestionFile(t, dir, "criteria-x.md", "version: test.1\nbody\n")
	f, err := LoadQuestionFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Payload("design_defaults_taken: 2\n"); err == nil || !strings.Contains(err.Error(), "security_shaped_package") {
		t.Errorf("a missing required field must be named in the refusal, got %v", err)
	}
	if _, err := f.Payload("security_shaped_package: maybe\ndesign_defaults_taken: 2\n"); err == nil || !strings.Contains(err.Error(), "bool") {
		t.Errorf("a wrong type must be named in the refusal, got %v", err)
	}
	if _, err := f.Payload("security_shaped_package: yes\ndesign_defaults_taken: two\n"); err == nil || !strings.Contains(err.Error(), "int") {
		t.Errorf("a wrong int must be named in the refusal, got %v", err)
	}
	good, err := f.Payload("security_shaped_package: yes\ndesign_defaults_taken: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(good, "version: test.1") || !strings.Contains(good, "design_defaults_taken: 2") {
		t.Errorf("the payload carries the criteria AND the state; got %q", good)
	}
}

// The request-capture control: what actually goes out over the wire. The
// criteria the question names must be IN the bytes, and no file the question
// did not name may be.
func TestTheBytesOnTheWireCarryTheNamedCriteriaAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	p := writeQuestionFile(t, dir, "questions-x.json", goodQuestions)
	writeQuestionFile(t, dir, "criteria-x.md", "version: test.1\nTHE LONG CRITERIA BODY\n")
	writeQuestionFile(t, dir, "unrelated.md", "UNRELATED LOCAL CONTENT")
	f, err := LoadQuestionFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		State     string                     `json:"state"`
		Model     string                     `json:"model"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"x":{"type":"choice","choice":"a","confidence":0.9}}}`))
	}))
	defer srv.Close()
	t.Setenv(DefaultKeyEnv, "test-key")
	c, err := New(srv.URL, DefaultKeyEnv)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := f.Payload("security_shaped_package: no\ndesign_defaults_taken: 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Decide(context.Background(), payload, f.Questions); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured.State, "THE LONG CRITERIA BODY") {
		t.Errorf("the criteria the question names is not in the bytes that went out:\n%s", captured.State)
	}
	if !strings.Contains(captured.State, "version: test.1") {
		t.Errorf("the criteria version is not in the bytes that went out")
	}
	if strings.Contains(captured.State, "UNRELATED LOCAL CONTENT") {
		t.Errorf("a file the question did not name went out over the wire")
	}
}

// Every question file this repository ships loads, and its criteria pair holds.
func TestTheShippedQuestionFilesLoadWithTheirCriteria(t *testing.T) {
	matches, err := filepath.Glob("../../docs/decide/questions-*.json")
	if err != nil || len(matches) < 3 {
		t.Fatalf("want the three shipped question files, got %v (%v)", matches, err)
	}
	for _, m := range matches {
		f, err := LoadQuestionFile(m)
		if err != nil {
			t.Errorf("%s: %v", m, err)
			continue
		}
		if strings.TrimSpace(f.Criteria) == "" {
			t.Errorf("%s loaded no criteria", m)
		}
		if len(f.StateFields) == 0 {
			t.Errorf("%s declares no typed state fields", m)
		}
	}
}
