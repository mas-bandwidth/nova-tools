// Red tests for nova-swarm triage --decide (card 8333, nova-tools #896).
//
// triage --decide asks TypeSafe Jev for a typed abstain reason per finished
// task: reason choice {provider_error, wall, prompt_defect, done, budget} and
// needs_human noul, behind a floor. The decision is a suggestion, never an
// authorization: below the floor the line says decide=? and the pool's own
// class stands. Each decision is logged beside the outcome in
// <pool>/decisions.log (rule 8), idempotent per (task, attempt).
//
// No test calls a provider over the network: the first test runs triage
// against an httptest fake (skipped where the sandbox forbids listening
// sockets, the way internal/decide's own suite does), and the rest drive the
// same path through the in-package decideDo seam with no socket at all.
package swarm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

const decideTestReport = "# t\n\n## Head\nfindings: 1\nnotes read: 0\nrepo: o/n\nrev: abc\na paragraph.\n\n## Findings\n- something `THE RULE, VERBATIM` internal/x.go:10\n\n## Per item\n| item | state | evidence |\n| --- | --- | --- |\n| an item | red | x.go:1 |\n"

// decideTestPool builds a temp pool with one finished task whose harness.log
// holds logBody. It returns the pool and the task id.
func decideTestPool(t *testing.T, logBody string) (*Pool, string) {
	t.Helper()
	dir := t.TempDir()
	poolDir := filepath.Join(dir, "pool")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPool(poolDir)
	if err != nil {
		t.Fatal(err)
	}
	id := "20260917T000000Z-task-abc123"
	jobDir := filepath.Join(dir, "job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(decideTestReport), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Label: "task", Job: jobDir, End: EndDone, RC: 1, Class: ClassPlanOnly}
	if err := os.WriteFile(filepath.Join(poolDir, Done, id+".task"), []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(poolDir, Done, id+".json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	usage := "job\tattempt\tfrom\tstarted\tended\tend\trc\tprovider\tmodel\trepo\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		id + "\t1\t-\t2026-09-17T00:00:00Z\t2026-09-17T00:01:00Z\tdone\t1\tfake\tfake-model\t-\t10\t5\t-\t-\t-\t-\n"
	if err := os.WriteFile(filepath.Join(poolDir, Usage, id+".tsv"), []byte(usage), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, id
}

func decideTestNow() time.Time { return time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) }

// decideFake answers reason=provider_error with high confidence and records
// the state it was asked about. It is the socket-free stand-in for the
// httptest fake below, driving the same decideOne path.
func decideFake(gotState *string) decideFunc {
	return func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		if gotState != nil {
			*gotState = state
		}
		if _, ok := qs["reason"]; !ok {
			panic("triage must ask the reason choice")
		}
		if _, ok := qs["needs_human"]; !ok {
			panic("triage must ask the needs_human noul")
		}
		return map[string]decide.Answer{
			"reason":      {Type: "choice", Choice: "provider_error", Probabilities: map[string]float64{"provider_error": 0.93}, Confidence: 0.93},
			"needs_human": {Type: "noul", Noul: 0.12, Confidence: 0.12},
		}, decide.Usage{}, nil
	}
}

func triageReportLine(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "TRIAGE REPORT id="+id) {
			return l
		}
	}
	return ""
}

// A task whose harness.log ends in 'Internal server error' gets
// reason=provider_error when the fake says so, and the state sent starts with
// that error line. Against an httptest fake returning the documented
// response shape; skipped where the sandbox forbids listening sockets.
func TestTriageDecideReasonProviderError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); socket-free tests pin the contract", err)
	}
	ln.Close()

	p, id := decideTestPool(t, "starting up\nkey sk-abcdefgh12345678 failed\nInternal server error\n")
	var gotState, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if s, ok := body["state"].(string); ok {
			gotState = s
		}
		raw, _ := json.Marshal(body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers": {
			"reason": {"type":"choice","choice":"provider_error","probabilities":{"provider_error":0.93},"confidence":0.93},
			"needs_human": {"type":"noul","noul":0.12}
		}, "usage": {"input_tokens": 10, "output_tokens": 3}}`))
	}))
	defer srv.Close()

	t.Setenv("CARD8333_JEV_KEY", "sekret")
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, KeyEnv: "CARD8333_JEV_KEY", BaseURL: srv.URL,
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
	}
	line := triageReportLine(out.String(), id)
	if line == "" {
		t.Fatalf("no TRIAGE REPORT line for %s in:\n%s", id, out.String())
	}
	if !strings.Contains(line, "decide=provider_error") {
		t.Fatalf("TRIAGE line missing decide=provider_error: %q", line)
	}
	if !strings.Contains(line, "needs_human=0.12") {
		t.Fatalf("TRIAGE line missing needs_human: %q", line)
	}
	if !strings.HasPrefix(gotState, "Internal server error") {
		t.Fatalf("state sent must start with the harness error line, got: %q", gotState)
	}
	if strings.Contains(gotBody, "sk-abcdefgh12345678") {
		t.Fatalf("request body leaks the redacted key: %q", gotBody)
	}
	// The HTTP provider call's usage is recorded in pool usage.tsv
	rawUsage, err := os.ReadFile(p.Path("usage.tsv"))
	if err != nil {
		t.Fatalf("usage.tsv was not written: %v", err)
	}
	if !strings.Contains(string(rawUsage), "10\t3") {
		t.Fatalf("usage.tsv missing tokens 10 and 3:\n%s", string(rawUsage))
	}
}

// The same contract with no socket: the state triage builds starts with the
// harness error line.
func TestTriageDecideStateStartsWithErrorLine(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nworking\nInternal server error\n")
	var gotState string
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: decideFake(&gotState),
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
	}
	line := triageReportLine(out.String(), id)
	if line == "" {
		t.Fatalf("no TRIAGE REPORT line for %s in:\n%s", id, out.String())
	}
	if !strings.Contains(line, "decide=provider_error") {
		t.Fatalf("TRIAGE line missing decide=provider_error: %q", line)
	}
	if !strings.HasPrefix(gotState, "Internal server error") {
		t.Fatalf("state sent must start with the harness error line, got: %q", gotState)
	}
}

// A redacted key never appears in the state sent.
func TestTriageDecideRedactsSecrets(t *testing.T) {
	p, _ := decideTestPool(t, "calling provider\nkey sk-abcdefgh12345678 failed\nAPI_KEY=hunter2-secret\nInternal server error\n")
	var gotState string
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: decideFake(&gotState),
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
	}
	if strings.Contains(gotState, "sk-abcdefgh12345678") {
		t.Fatalf("state leaks the redacted key: %q", gotState)
	}
	if strings.Contains(gotState, "hunter2-secret") {
		t.Fatalf("state leaks the API_KEY value: %q", gotState)
	}
}

// decisions.log gains one line per task and is idempotent per (task,
// attempt): a second triage --decide run adds nothing.
func TestTriageDecideLogIdempotent(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	run := func() string {
		var out, errOut bytes.Buffer
		if rc := Triage(TriageInput{
			Pool: p, Max: 0, All: true,
			Decide: true, Floor: 0.9, decideDo: decideFake(nil),
			Stdout: &out, Stderr: &errOut, Now: decideTestNow,
		}); rc != 0 {
			t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
		}
		return out.String()
	}
	run()
	raw, err := os.ReadFile(filepath.Join(p.Dir, "decisions.log"))
	if err != nil {
		t.Fatalf("decisions.log was not written: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("decisions.log must gain one line per task, got %d: %q", len(lines), string(raw))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decisions.log line is not JSON: %v", err)
	}
	for _, key := range []string{"task", "class", "decision", "confidence", "needs_human", "time"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("decisions.log line missing %q: %q", key, lines[0])
		}
	}
	if entry["task"] != id {
		t.Fatalf("decisions.log task = %v, want %s", entry["task"], id)
	}
	run()
	raw2, err := os.ReadFile(filepath.Join(p.Dir, "decisions.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines2 := strings.Split(strings.TrimRight(string(raw2), "\n"), "\n")
	if len(lines2) != 1 {
		t.Fatalf("second triage --decide must not duplicate decisions.log lines, got %d", len(lines2))
	}
}

// Below the floor the line says decide=? and the pool's own class stands.
func TestTriageDecideBelowFloorAbstains(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	weak := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		return map[string]decide.Answer{
			"reason":      {Type: "choice", Choice: "wall", Probabilities: map[string]float64{"wall": 0.41}, Confidence: 0.41},
			"needs_human": {Type: "noul", Noul: 0.77, Confidence: 0.77},
		}, decide.Usage{}, nil
	}
	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: weak,
		Stdout: &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
	}
	line := triageReportLine(out.String(), id)
	if line == "" {
		t.Fatalf("no TRIAGE REPORT line for %s in:\n%s", id, out.String())
	}
	if !strings.Contains(line, "decide=?") {
		t.Fatalf("below-floor line must say decide=?: %q", line)
	}
	if !strings.Contains(line, "result=ok") {
		t.Fatalf("below the floor the pool's own class stands: %q", line)
	}
}

// Provider usage from the Jev seam is recorded through the card-usage contract
// (AppendCardUsage), matching nova-decide route (jev:swarm-usage).
func TestTriageDecideRecordsProviderUsage(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	customUsage := filepath.Join(t.TempDir(), "custom-usage.tsv")

	withUsage := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		return map[string]decide.Answer{
			"reason":      {Type: "choice", Choice: "provider_error", Probabilities: map[string]float64{"provider_error": 0.95}, Confidence: 0.95},
			"needs_human": {Type: "noul", Noul: 0.10, Confidence: 0.10},
		}, decide.Usage{InputTokens: 42, OutputTokens: 17, HasInput: true, HasOutput: true}, nil
	}

	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: withUsage,
		UsagePath: customUsage,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d, stderr: %s", rc, errOut.String())
	}

	// Verify custom usage TSV was written
	raw, err := os.ReadFile(customUsage)
	if err != nil {
		t.Fatalf("custom usage.tsv was not written: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("custom usage.tsv must hold header and 1 row, got %d:\n%s", len(lines), string(raw))
	}
	head := strings.Split(lines[0], "\t")
	row := strings.Split(lines[1], "\t")
	lookup := map[string]string{}
	for i, col := range head {
		if i < len(row) {
			lookup[col] = row[i]
		}
	}
	if lookup["job"] != id {
		t.Errorf("job = %q, want %s", lookup["job"], id)
	}
	if lookup["attempt"] != "1" {
		t.Errorf("attempt = %q, want 1", lookup["attempt"])
	}
	if lookup["provider"] != "typesafe" {
		t.Errorf("provider = %q, want typesafe", lookup["provider"])
	}
	if lookup["model"] != decide.DefaultModel {
		t.Errorf("model = %q, want %s", lookup["model"], decide.DefaultModel)
	}
	if lookup["tokens_in"] != "42" {
		t.Errorf("tokens_in = %q, want 42", lookup["tokens_in"])
	}
	if lookup["tokens_out"] != "17" {
		t.Errorf("tokens_out = %q, want 17", lookup["tokens_out"])
	}
	if lookup["rc"] != "0" {
		t.Errorf("rc = %q, want 0", lookup["rc"])
	}

	// Verify pool usage.tsv was also written
	poolRaw, err := os.ReadFile(p.Path("usage.tsv"))
	if err != nil {
		t.Fatalf("pool usage.tsv was not written: %v", err)
	}
	if !strings.Contains(string(poolRaw), id) || !strings.Contains(string(poolRaw), "typesafe") {
		t.Fatalf("pool usage.tsv content mismatch:\n%s", string(poolRaw))
	}

	// Second run must be idempotent: does not duplicate usage rows for the same (task, attempt)
	rc2 := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: withUsage,
		UsagePath: customUsage,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc2 != 0 {
		t.Fatalf("second triage rc = %d", rc2)
	}
	raw2, _ := os.ReadFile(customUsage)
	lines2 := strings.Split(strings.TrimRight(string(raw2), "\n"), "\n")
	if len(lines2) != 2 {
		t.Fatalf("second triage run must not duplicate usage row, got %d lines", len(lines2))
	}
}

// A failed provider call still records its usage with rc=2 before failing.
func TestTriageDecideUsageOnFailure(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	usageFile := filepath.Join(t.TempDir(), "usage.tsv")

	failedCall := func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		return nil, decide.Usage{InputTokens: 100, OutputTokens: 0, HasInput: true, HasOutput: false}, fmt.Errorf("provider 500 error")
	}

	var out, errOut bytes.Buffer
	rc := Triage(TriageInput{
		Pool: p, Max: 0, All: true,
		Decide: true, Floor: 0.9, decideDo: failedCall,
		UsagePath: usageFile,
		Stdout:    &out, Stderr: &errOut, Now: decideTestNow,
	})
	if rc != 0 {
		t.Fatalf("triage rc = %d", rc)
	}

	raw, err := os.ReadFile(usageFile)
	if err != nil {
		t.Fatalf("usage.tsv not written on failure: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("usage.tsv want 2 lines, got %d:\n%s", len(lines), string(raw))
	}
	head := strings.Split(lines[0], "\t")
	row := strings.Split(lines[1], "\t")
	lookup := map[string]string{}
	for i, col := range head {
		if i < len(row) {
			lookup[col] = row[i]
		}
	}
	if lookup["job"] != id {
		t.Errorf("job = %q, want %s", lookup["job"], id)
	}
	if lookup["tokens_in"] != "100" {
		t.Errorf("tokens_in = %q, want 100", lookup["tokens_in"])
	}
	if lookup["tokens_out"] != "-" {
		t.Errorf("tokens_out = %q, want - for unreported counter", lookup["tokens_out"])
	}
	if lookup["rc"] != "2" {
		t.Errorf("rc = %q, want 2 on failed call", lookup["rc"])
	}
}
