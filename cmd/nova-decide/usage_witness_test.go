package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// Stella's two CLI witnesses on #1327 at df8cedaf, R5, mirrored here.

// tsvRow reads a one-row usage TSV into field -> value.
func tsvRow(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no usage was written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("a header and one row, got %d:\n%s", len(lines), raw)
	}
	head, row := strings.Split(lines[0], "\t"), strings.Split(lines[1], "\t")
	if len(head) != len(row) {
		t.Fatalf("the row does not match the header:\n%s", raw)
	}
	out := map[string]string{}
	for i, name := range head {
		out[name] = row[i]
	}
	return out
}

// Witness 1: a 200 with a valid answer and NO usage object must not become a
// measured zero. The row carries "-" for what was never reported.
func TestRouteWritesADashForUnreportedUsage(t *testing.T) {
	useFake(t, &fake{conf: 0.97}) // an answer, and no usage at all
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u-1", "--kind", "new-verb", "--files", "3",
		"--packages", "1", "--lanes", "1", "--usage", usage, "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	got := tsvRow(t, usage)
	for _, field := range []string{"tokens_in", "tokens_out"} {
		if got[field] != "-" {
			t.Errorf("usage %s = %q, want \"-\": a successful answer is not evidence of reported usage", field, got[field])
		}
	}
	if got["rc"] != "0" {
		t.Errorf("the call itself succeeded: rc = %q", got["rc"])
	}
	var e decide.Entry
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.TokensIn != nil || e.TokensOut != nil {
		t.Errorf("the log row invented a zero: %s", raw)
	}
	if e.Calls != 1 {
		t.Errorf("the call is still on the record: %s", raw)
	}
}

// An explicitly reported zero is a measurement, and it survives as one.
func TestRouteWritesAReportedZero(t *testing.T) {
	useFake(t, &fake{conf: 0.97, usage: decide.Usage{HasInput: true, HasOutput: true}})
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u-1", "--kind", "new-verb", "--files", "3",
		"--packages", "1", "--lanes", "1", "--usage", usage, "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	got := tsvRow(t, usage)
	for _, field := range []string{"tokens_in", "tokens_out"} {
		if got[field] != "0" {
			t.Errorf("usage %s = %q, want \"0\": a reported zero is a measurement", field, got[field])
		}
	}
}

// Witness 2: a routing refusal AFTER a completed call still writes the usage
// row and the log row -- the tokens were spent and a refusal cannot unspend
// them -- and the exit code stays the refusal's own.
func TestRouteRefusalStillPersistsTheCall(t *testing.T) {
	useFake(t, &fake{conf: 0.40, pick: 1, usage: decide.Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}})
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.json")
	body := `{"minds":[
	  {"name":"low","lineage":"one","height":0,"availability":"available","ask":"card"},
	  {"name":"high","lineage":"two","height":1,"availability":"available","ask":"bus"}
	]}`
	if err := os.WriteFile(reg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u-ref", "--kind", "rebase", "--files", "2", "--packages", "1",
		"--registry", reg, "--usage", usage, "--log", log}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2: there is no rung above the top one (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "REFUSED reason=") {
		t.Errorf("the refusal line is missing: %q", stderr.String())
	}
	got := tsvRow(t, usage)
	if got["tokens_in"] != "937" || got["tokens_out"] != "12" {
		t.Errorf("the refusal discarded a call that was already paid for: %v", got)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the refusal wrote no log row: %v", err)
	}
	var e decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.Refusal == "" {
		t.Errorf("the log row must say why it was refused: %s", raw)
	}
	if e.TokensIn == nil || *e.TokensIn != 937 || e.Calls != 1 {
		t.Errorf("the log row lost the spend: %s", raw)
	}
	if e.Unit != "u-ref" {
		t.Errorf("the row lost its unit: %s", raw)
	}
}

// A refusal with no call behind it writes no usage row: there is nothing to
// account for, and an empty row would be a claim that a call was made.
func TestARefusalWithNoCallWritesNoUsage(t *testing.T) {
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--no-jev", "--unit-id", "u-top", "--kind", "rebase", "--files", "2",
		"--attempt", "flash:failed:a", "--attempt", "pro:failed:b", "--attempt", "opus:failed:c",
		"--attempt", "sol:failed:d", "--attempt", "emma:failed:e", "--attempt", "freddy:failed:f",
		"--attempt", "astra:failed:g", "--attempt", "fable:failed:h", "--attempt", "all-friends:failed:i",
		"--attempt", "glenn:failed:j", "--usage", usage, "--log", log}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(usage); err == nil {
		t.Error("no call was made, so there is nothing to account for")
	}
	// The decision is still a row: a refusal is evidence too.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the refusal wrote no log row: %v", err)
	}
	var e decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.Refusal == "" || e.Calls != 0 {
		t.Errorf("a refusal with no call is a row saying exactly that: %s", raw)
	}
}
