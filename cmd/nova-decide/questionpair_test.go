package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verb loads the pair and refuses BEFORE the provider is dialled. The fake
// counts requests, so "before the call" is proved by zero requests and not by
// the order of the source.
func TestTheVerbRefusesAMissingTypedFieldWithoutCallingTheProvider(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "questions-x.json"), `{
      "criteria_version": "t.1",
      "criteria_file": "criteria-x.md",
      "state_fields": [{"name": "security_shaped_package", "type": "bool"}],
      "questions": {"x": {"type": "choice", "instructions": "pick criteria-x.md", "criteria": {"a": "the a", "b": "the b"}}}
    }`)
	write(t, filepath.Join(dir, "criteria-x.md"), "version: t.1\nTHE LONG CRITERIA BODY\n")
	write(t, filepath.Join(dir, "state.md"), "kind: dogfood\n")

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"answers":{"x":{"type":"choice","choice":"a","confidence":0.9}}}`))
	}))
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "test-key")

	var out, errb bytes.Buffer
	code := run([]string{"--questions", filepath.Join(dir, "questions-x.json"),
		"--state", filepath.Join(dir, "state.md"), "--base-url", srv.URL, "--key-env", "JEV_API_KEY"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "security_shaped_package") {
		t.Errorf("the refusal names the missing field; got %q", errb.String())
	}
	if calls != 0 {
		t.Errorf("the provider was called %d time(s) before the state was refused", calls)
	}
}

// And when the state is whole, the criteria the question named are in the bytes.
func TestTheVerbSendsTheCriteriaItNamed(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "questions-x.json"), `{
      "criteria_version": "t.1",
      "criteria_file": "criteria-x.md",
      "state_fields": [{"name": "security_shaped_package", "type": "bool"}],
      "questions": {"x": {"type": "choice", "instructions": "pick criteria-x.md", "criteria": {"a": "the a", "b": "the b"}}}
    }`)
	write(t, filepath.Join(dir, "criteria-x.md"), "version: t.1\nTHE LONG CRITERIA BODY\n")
	write(t, filepath.Join(dir, "state.md"), "security_shaped_package: no\n")
	write(t, filepath.Join(dir, "unrelated.md"), "UNRELATED LOCAL CONTENT")

	var captured struct {
		State string `json:"state"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_, _ = w.Write([]byte(`{"answers":{"x":{"type":"choice","choice":"a","confidence":0.9}}}`))
	}))
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "test-key")

	var out, errb bytes.Buffer
	if code := run([]string{"--questions", filepath.Join(dir, "questions-x.json"),
		"--state", filepath.Join(dir, "state.md"), "--base-url", srv.URL, "--key-env", "JEV_API_KEY", "--floor", "0.5"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(captured.State, "THE LONG CRITERIA BODY") {
		t.Errorf("the criteria the question named did not go out:\n%s", captured.State)
	}
	if !strings.Contains(captured.State, "security_shaped_package: no") {
		t.Errorf("the state did not go out:\n%s", captured.State)
	}
	if strings.Contains(captured.State, "UNRELATED LOCAL CONTENT") {
		t.Error("a file the question did not name went out over the wire")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
