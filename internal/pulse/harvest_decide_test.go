package pulse

// Red tests for the harvest's decide step (nova-tools #896, card 8336). A finished
// job is classified before any push by one typed choice behind a floor: fixed and
// failed push as today, no-change and already-fixed push nothing and mark the job
// harvested (already-fixed names the test the RESULT.md red: line carries), and
// off-branch pushes nothing and prints the remedy. Below the floor the class is
// unknown and today's path runs unchanged. The httptest fake and the in-process
// fake keep every test off the network and never read the real key.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeClassDecider answers the harvest's one class question without a provider:
// a test picks the class and its confidence, and reads back the state the
// harvest sent and how many times it asked.
type fakeClassDecider struct {
	choice string
	conf   float64
	err    error
	state  string
	asked  int
}

func (f *fakeClassDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.asked++
	f.state = state
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	return map[string]decide.Answer{
		"class": {Type: "choice", Choice: f.choice, Probabilities: map[string]float64{f.choice: f.conf}, Confidence: f.conf},
	}, decide.Usage{}, nil
}

// harvestDecideRun folds one root with the decision route on and the given floor.
func harvestDecideRun(t *testing.T, root string, dec Decider, floor float64) (string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Decide: true, Floor: floor, Decider: dec,
		Stdout: &out, Stderr: &errs,
	})
	return out.String(), errs.String()
}

// oneDoneCard writes the card whose RESULT.md carries every feature the class
// question is asked over: the first line, the BRANCH line, a red: test and the
// files: line.
func oneDoneCard(t *testing.T, root string) {
	t.Helper()
	addCard(t, root, "a", "1", "flash", "RESULT a sha=aaa",
		"RESULT a sha=aaa\nDONE\nBRANCH rowan/br1\nREPO owner/repo\nred: TestFooBar -- boom\ngreen: ok\nfiles: a.go b.go\n")
}

// harvest-class-asks-the-question: the harvest sends the RESULT.md first line, the
// BRANCH line, the commits past the base and the files line as the bounded state,
// asks one choice named class with the five options, and an above-floor fixed
// answer pushes as today with class=fixed conf=0.95 on the HARVEST PR line.
func TestHarvestDecideAsksTheClassQuestion(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	states := make(chan string, 1)
	questions := make(chan map[string]map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			t.Errorf("authorization = %q, want Bearer testkey", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			State     string                    `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		_ = json.Unmarshal(raw, &body)
		states <- body.State
		questions <- body.Questions
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answers":{"class":{"type":"choice","choice":"fixed","confidence":0.95}},"usage":{"input_tokens":5,"output_tokens":6}}`)
	}))
	defer srv.Close()

	t.Setenv("CARD8336_JEV_KEY", "testkey")
	client, err := decide.New(srv.URL, "CARD8336_JEV_KEY")
	if err != nil {
		t.Fatalf("decide.New: %v", err)
	}

	out, _ := harvestDecideRun(t, root, client, 0.9)
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("a fixed class must push as today, got:\n%s", out)
	}
	if !strings.Contains(out, "class=fixed conf=0.95") {
		t.Fatalf("the HARVEST line does not carry class=fixed conf=0.95:\n%s", out)
	}

	state := <-states
	for _, want := range []string{"RESULT a sha=aaa", "BRANCH rowan/br1", "commits", "files: a.go b.go"} {
		if !strings.Contains(state, want) {
			t.Fatalf("the state does not carry %q:\n%s", want, state)
		}
	}
	class := (<-questions)["class"]
	if class == nil {
		t.Fatal("the harvest sent no class question")
	}
	if class["type"] != "choice" {
		t.Fatalf("class question type = %v, want choice", class["type"])
	}
	crit, _ := class["criteria"].(map[string]any)
	for _, opt := range []string{"fixed", "already-fixed", "no-change", "failed", "off-branch"} {
		if _, ok := crit[opt]; !ok {
			t.Fatalf("class criteria missing %q: %v", opt, crit)
		}
	}
}

// harvest-already-fixed-skips-the-push: an above-floor already-fixed answer pushes
// nothing, marks the job harvested and names the RESULT.md red: test for the closer.
func TestHarvestAlreadyFixedSkipsThePush(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeClassDecider{choice: "already-fixed", conf: 0.95}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if strings.Contains(out, "pushed=1") || strings.Contains(out, "prs=1") {
		t.Fatalf("already-fixed must push nothing, got:\n%s", out)
	}
	if !strings.Contains(out, "class=already-fixed") || !strings.Contains(out, "test=TestFooBar") {
		t.Fatalf("the HARVEST line must carry class=already-fixed and test=TestFooBar:\n%s", out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("already-fixed pushed or opened a PR: %s", l)
		}
	}
	seen, err := os.ReadFile(filepath.Join(root, "seen.tsv"))
	if err != nil || !strings.Contains(string(seen), "\t") {
		t.Fatalf("already-fixed did not mark the job harvested:\n%s", seen)
	}
	if dec.asked != 1 {
		t.Fatalf("the class question was asked %d times, want 1", dec.asked)
	}
}

// harvest-no-change-skips-the-push: an above-floor no-change answer pushes nothing
// and marks the job harvested.
func TestHarvestNoChangeSkipsThePush(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeClassDecider{choice: "no-change", conf: 0.93}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if strings.Contains(out, "pushed=1") || strings.Contains(out, "prs=1") {
		t.Fatalf("no-change must push nothing, got:\n%s", out)
	}
	if !strings.Contains(out, "class=no-change") {
		t.Fatalf("the HARVEST line must carry class=no-change:\n%s", out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("no-change pushed or opened a PR: %s", l)
		}
	}
}

// harvest-off-branch-prints-the-remedy: an above-floor off-branch answer pushes
// nothing and the HARVEST line carries the remedy.
func TestHarvestOffBranchPrintsTheRemedy(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeClassDecider{choice: "off-branch", conf: 0.97}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if strings.Contains(out, "pushed=1") || strings.Contains(out, "prs=1") {
		t.Fatalf("off-branch must push nothing, got:\n%s", out)
	}
	if !strings.Contains(out, "class=off-branch") || !strings.Contains(out, "remedy=") {
		t.Fatalf("the HARVEST line must carry class=off-branch and a remedy:\n%s", out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("off-branch pushed or opened a PR: %s", l)
		}
	}
}

// harvest-below-floor-changes-nothing: a no-change answer at 0.60 under the 0.90
// floor is a suggestion, never an authorization: class=unknown, below=class, and
// today's push happens exactly as it did without the call.
func TestHarvestBelowFloorBehavesAsToday(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeClassDecider{choice: "no-change", conf: 0.60}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("below the floor the harvest must push as today, got:\n%s", out)
	}
	if !strings.Contains(out, "class=unknown") || !strings.Contains(out, "below=class") {
		t.Fatalf("below the floor the line must read class=unknown below=class:\n%s", out)
	}
}

// harvest-provider-error-changes-nothing: a provider that cannot answer leaves the
// harvest on today's path with class=unknown, never a failed harvest.
func TestHarvestDecideErrorBehavesAsToday(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeClassDecider{err: fmt.Errorf("provider down")}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("a provider error must leave today's path, got:\n%s", out)
	}
	if !strings.Contains(out, "class=unknown") {
		t.Fatalf("a provider error must read class=unknown:\n%s", out)
	}
}

// fakeHarvestDecider answers the harvest's class, result and red_owner questions
// without a provider: a test picks each answer and its confidence, and counts the
// calls to prove the three ride one call.
type fakeHarvestDecider struct {
	class, result, owner             string
	classConf, resultConf, ownerConf float64
	asked                            int
}

func (f *fakeHarvestDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.asked++
	ans := func(choice string, conf float64) decide.Answer {
		return decide.Answer{Type: "choice", Choice: choice, Probabilities: map[string]float64{choice: conf}, Confidence: conf}
	}
	return map[string]decide.Answer{
		"class":     ans(f.class, f.classConf),
		"result":    ans(f.result, f.resultConf),
		"red_owner": ans(f.owner, f.ownerConf),
	}, decide.Usage{}, nil
}

// harvest-result-decided-beside-class: the harvest asks result and red_owner beside
// the branch class question in the same call. An above-floor defect/row answer
// carries result=defect red_owner=row on the HARVEST line, prints one
// HARVEST FINDING-CANDIDATE and still harvests as today; and a
// "command not found" quoted inside a passing test's expected-output block is not
// a bench red (the rule table does not fire, the provider is asked).
func TestHarvestResultDecidedBesideClass(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/42")
	oneDoneCard(t, root)

	dec := &fakeHarvestDecider{class: "fixed", classConf: 0.95, result: "defect", resultConf: 0.95, owner: "row", ownerConf: 0.95}
	out, _ := harvestDecideRun(t, root, dec, 0.9)
	if !strings.Contains(out, "result=defect red_owner=row") {
		t.Fatalf("the HARVEST line must carry result=defect red_owner=row:\n%s", out)
	}
	if !strings.Contains(out, "class=fixed") {
		t.Fatalf("the class question still rides beside the result question:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST FINDING-CANDIDATE job=a pointer=") {
		t.Fatalf("a defect must print one HARVEST FINDING-CANDIDATE line:\n%s", out)
	}
	if !strings.Contains(out, "pushed=1") {
		t.Fatalf("a defect still harvests as today, got:\n%s", out)
	}
	if dec.asked != 1 {
		t.Fatalf("result and red_owner ride the one class call: asked=%d, want 1", dec.asked)
	}

	root2, specs2, arglog2 := setupPulse(t)
	fakeGit(t, specs2, arglog2)
	fakeGH(t, specs2, arglog2, "https://example.com/owner/repo/pull/43")
	oneDoneCard(t, root2)
	job := filepath.Join(root2, "1", "jobs", "a")
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte("=== RUN TestFooBar\n--- PASS: TestFooBar\nPASS\nok example.com/mod 0.1s\nexpected output:\n```\nsh: gofmt: command not found\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dec2 := &fakeHarvestDecider{class: "fixed", classConf: 0.95, result: "clean", resultConf: 0.95, owner: "na", ownerConf: 0.95}
	out2, _ := harvestDecideRun(t, root2, dec2, 0.9)
	if strings.Contains(out2, "result=blocked-toolchain") {
		t.Fatalf("a quoted expected-output block is not a bench red:\n%s", out2)
	}
	if dec2.asked != 1 {
		t.Fatalf("the quoted toolchain string must not short-circuit the call: asked=%d, want 1", dec2.asked)
	}
}
