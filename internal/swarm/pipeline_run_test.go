//go:build !windows

package swarm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// THE PIPELINE RUNS (issue #856). One model call per model step, no transcript, no tools:
// the harness clones, runs the test, commits and writes the files, and the model answers
// each step with ONE artifact. These tests hold a fake endpoint and a real git repository,
// so what is proved is the machinery and not a provider.

// fakeKey is the key the endpoint demands and no log may ever hold.
const fakeKey = "sk-fake-3141592653589793-never-logged"

// fixtureFixCard is the fix card of the issue: three model steps -- the red test, the fix,
// the RESULT -- around the harness's own clone, test and commit.
const fixtureFixCard = "RESULT: CARD-F the fixture fix card\n" +
	"You are a Go engineer. Work only inside ./repo. MODE: pipeline\n" +
	"STEP 1. git rev-parse --abbrev-ref HEAD\n" +
	"STEP 2. Write the red test in `hello_test.go` naming what `hello.go` must say.\n" +
	"STEP T. grep -q FIXED hello.go\n" +
	"STEP 3. Implement the smallest fix in `hello.go` that makes the failing line pass.\n" +
	"STEP C. git add -A && git commit -q -m fixture && git log --oneline -1 | cat\n" +
	"STEP 4. Write RESULT.md: line 1 the RESULT line above, then red:, green:, one unsure: line.\n"

const redTestDiff = "diff --git a/hello_test.go b/hello_test.go\n" +
	"new file mode 100644\n" +
	"--- /dev/null\n" +
	"+++ b/hello_test.go\n" +
	"@@ -0,0 +1,3 @@\n" +
	"+package hello\n" +
	"+\n" +
	"+// red: Name must be FIXED\n"

const fixDiff = "diff --git a/hello.go b/hello.go\n" +
	"--- a/hello.go\n" +
	"+++ b/hello.go\n" +
	"@@ -1,3 +1,3 @@\n" +
	" package hello\n" +
	" \n" +
	"-const Name = \"hello\"\n" +
	"+const Name = \"FIXED\"\n"

const resultBody = "RESULT: CARD-F the fixture fix card\n" +
	"DONE -- the fixture fix landed with its red test first\n" +
	"red: hello_test.go -- Name must be FIXED -- needs hello.go\n" +
	"green: the grep passes\n" +
	"unsure: nothing\n"

func fenced(body string) string { return "```\n" + body + "```\n" }

// pipelineRepo makes a real git repository holding the file the fix diff patches.
func pipelineRepo(t *testing.T, jobDir string) string {
	t.Helper()
	repo := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hello.go"), []byte("package hello\n\nconst Name = \"hello\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "user.name", "Fixture"},
		{"add", "-A"},
		{"commit", "-q", "-m", "fixture base"},
	} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", argv, err, out)
		}
	}
	return repo
}

// fakeEndpoint answers each call with the next canned body, demands the key on the
// Authorization header, and records every prompt it was sent.
type fakeEndpoint struct {
	server  *httptest.Server
	calls   atomic.Int64
	bodies  []string
	prompts []string
}

func newFakeEndpoint(t *testing.T, bodies ...string) *fakeEndpoint {
	t.Helper()
	fe := &fakeEndpoint{bodies: bodies}
	fe.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+fakeKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		var whole strings.Builder
		for _, m := range req.Messages {
			whole.WriteString(m.Content)
		}
		n := int(fe.calls.Add(1))
		fe.prompts = append(fe.prompts, whole.String())
		body := "no more answers"
		if n <= len(fe.bodies) {
			body = fe.bodies[n-1]
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s}}],"usage":{"prompt_tokens":%d,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}}`,
			mustJSON(body), 100+n)
	}))
	t.Cleanup(fe.server.Close)
	return fe
}

func mustJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// pipelineFixture wires a job directory, a repository, a fake endpoint and a config that
// carries the key in a FILE, the way a bench does.
func pipelineFixture(t *testing.T, card string, bodies ...string) (PipelineConfig, *fakeEndpoint, *bytes.Buffer) {
	t.Helper()
	jobDir := t.TempDir()
	pipelineRepo(t, jobDir)
	fe := newFakeEndpoint(t, bodies...)
	keyFile := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(keyFile, []byte(fakeKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route, err := ResolveRoute("deepseek/deepseek-v4-pro", RouteFiles{KeyFile: keyFile, Endpoint: fe.server.URL})
	if err != nil {
		t.Fatalf("the route did not resolve: %v", err)
	}
	if route.Key != fakeKey {
		t.Fatalf("the route did not read the key file %s", keyFile)
	}
	log := &bytes.Buffer{}
	return PipelineConfig{
		Card:   []byte(card),
		JobDir: jobDir,
		Label:  "card-f",
		Route:  route,
		Log:    log,
	}, fe, log
}

// TEST 1: the fixture fix card runs in EXACTLY three model calls, and the log shows each
// call's input size. Thirty harness turns of re-sent transcript become three calls.
func TestPipelineFixCardRunsInThreeModelCalls(t *testing.T) {
	cfg, fe, log := pipelineFixture(t, fixtureFixCard, fenced(redTestDiff), fenced(fixDiff), fenced(resultBody))
	res := RunPipeline(context.Background(), cfg)
	if res.Refusal != "" {
		t.Fatalf("the card was refused: %s\n%s", res.Refusal, log.String())
	}
	if got := fe.calls.Load(); got != 3 {
		t.Errorf("the endpoint was called %d times, want exactly 3:\n%s", got, log.String())
	}
	if res.Calls != 3 || res.Steps != 6 {
		t.Errorf("the result says steps=%d calls=%d, want 6 and 3", res.Steps, res.Calls)
	}
	// Each call's input size is on the log, so the bill is readable without the provider.
	sizes := 0
	for _, line := range strings.Split(log.String(), "\n") {
		if strings.Contains(line, "kind=model") && strings.Contains(line, " in=") {
			sizes++
		}
	}
	if sizes != 3 {
		t.Errorf("the log shows %d model-step input sizes, want 3:\n%s", sizes, log.String())
	}
	// The harness applied both diffs and wrote the RESULT: the work is done, not described.
	body, err := os.ReadFile(filepath.Join(cfg.JobDir, "repo", "hello.go"))
	if err != nil || !strings.Contains(string(body), "FIXED") {
		t.Errorf("the fix diff was not applied: %v %q", err, body)
	}
	if _, err := os.Stat(filepath.Join(cfg.JobDir, "repo", "hello_test.go")); err != nil {
		t.Errorf("the red test was not applied: %v", err)
	}
	result, err := os.ReadFile(filepath.Join(cfg.JobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("no RESULT.md at the job root: %v", err)
	}
	if !strings.HasPrefix(string(result), "RESULT: CARD-F the fixture fix card\n") {
		t.Errorf("RESULT.md line 1 is not the contract line:\n%s", result)
	}
	// No transcript: each call carries the card's preamble and its OWN step, never the
	// step before it. Call 3 must not hold step 2's instruction.
	if strings.Contains(fe.prompts[2], "Write the red test") {
		t.Errorf("call 3 carried an earlier step's text: memory between calls")
	}
	// Accounting: one usage row per step, and the tokens folded onto the card's own row.
	rows := readUsageRows(t, filepath.Join(cfg.JobDir, "usage.tsv"))
	if len(rows) < 4 {
		t.Fatalf("usage.tsv holds %d rows, want the card total and one per model step", len(rows))
	}
	if res.TokensIn != 101+102+103 || res.TokensOut != 21 {
		t.Errorf("the run folded in=%d out=%d, want 306 and 21", res.TokensIn, res.TokensOut)
	}
}

// TEST 2: a card that is not MODE: explore gets one call per model step and one retry. A
// model step that answers with prose instead of the artifact is retried once with the parse
// error, and the fourth call fails the card with ONE line naming the step and the remedy.
func TestPipelineFourthCallIsRefusedWithTheRemedyLine(t *testing.T) {
	cfg, fe, log := pipelineFixture(t, fixtureFixCard,
		fenced(redTestDiff), fenced(fixDiff),
		"I should look at the repository first. Let me run a few commands.",
		"I still need to explore before I can write this.")
	res := RunPipeline(context.Background(), cfg)
	if res.Refusal == "" {
		t.Fatalf("a card that asked for a fourth call was admitted:\n%s", log.String())
	}
	if got := fe.calls.Load(); got != 4 {
		t.Errorf("the endpoint was called %d times, want 4 (three steps and one retry)", got)
	}
	if !strings.Contains(res.Refusal, "step=6") {
		t.Errorf("the refusal does not name the step: %q", res.Refusal)
	}
	if !strings.Contains(res.Refusal, "MODE: explore") {
		t.Errorf("the refusal carries no remedy: %q", res.Refusal)
	}
	if strings.Count(strings.TrimSpace(res.Refusal), "\n") != 0 {
		t.Errorf("the refusal is more than one line: %q", res.Refusal)
	}
}

// TEST 3: the diff-apply path refuses a diff that touches a path outside ./repo, before git
// is asked to apply it, and the file outside is never written.
func TestPipelineRefusesADiffOutsideTheRepo(t *testing.T) {
	escape := "diff --git a/../escaped.go b/../escaped.go\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/../escaped.go\n@@ -0,0 +1 @@\n+package escaped\n"
	cfg, _, log := pipelineFixture(t, fixtureFixCard, fenced(escape), fenced(escape), fenced(fixDiff), fenced(resultBody))
	res := RunPipeline(context.Background(), cfg)
	if res.Refusal == "" {
		t.Fatalf("a diff leaving ./repo was applied:\n%s", log.String())
	}
	if !strings.Contains(res.Refusal, "step=2") {
		t.Errorf("the refusal does not name the step that answered: %q", res.Refusal)
	}
	if _, err := os.Stat(filepath.Join(cfg.JobDir, "escaped.go")); err == nil {
		t.Errorf("the escaping file was written at %s", filepath.Join(cfg.JobDir, "escaped.go"))
	}
}

// TEST 4: the key is read from its FILE and never appears in anything the run writes.
func TestPipelineNeverWritesTheKeyToAnyLog(t *testing.T) {
	cfg, _, log := pipelineFixture(t, fixtureFixCard, fenced(redTestDiff), fenced(fixDiff), fenced(resultBody))
	if res := RunPipeline(context.Background(), cfg); res.Refusal != "" {
		t.Fatalf("the card was refused: %s", res.Refusal)
	}
	if strings.Contains(log.String(), fakeKey) {
		t.Fatalf("the key is in the run log")
	}
	err := filepath.WalkDir(cfg.JobDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if bytes.Contains(raw, []byte(fakeKey)) {
			t.Errorf("the key is in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// readUsageRows reads every data row of a usage.tsv, not merely the first.
func readUsageRows(t *testing.T, path string) []map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no usage.tsv at %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	head := strings.Split(lines[0], "\t")
	var rows []map[string]string
	for _, line := range lines[1:] {
		cells := strings.Split(line, "\t")
		row := map[string]string{}
		for i, name := range head {
			if i < len(cells) {
				row[name] = cells[i]
			}
		}
		rows = append(rows, row)
	}
	return rows
}
