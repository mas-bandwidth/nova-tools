package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// THE RUNNER IS THE EVENT SOURCE (card gh-ci-receipts, stream github).
// Measured 2026-09-26 12:38 PM ET: ev:github empty (XLEN 0) and zero
// ci:*:gh keys, so `nova-sprint land pr` (#4326) could only print WAITING:
// the signed webhook receiver sits behind a tailscale funnel kept off by
// design, and nothing polls GitHub for a check state (TestNoPollingPathsRemain).
// Our runners are self-hosted on the tailnet and run as the bench seat, so
// ci-ok reports the run itself at its end, with `nova-sprint ci github
// --from-runner`, and that step must carry every field the record needs and
// must fail the job when the write fails: a landing never waits on a
// receipt that silently did not happen. This test reads the step as YAML
// and holds it to that shape.

const runnerReceiptVerb = "ci github --from-runner"

type ciokWorkflow struct {
	Jobs map[string]struct {
		Needs yaml.Node `yaml:"needs"` // a list, or one job as a string
		Steps []struct {
			Name            string `yaml:"name"`
			If              string `yaml:"if"`
			Run             string `yaml:"run"`
			ContinueOnError bool   `yaml:"continue-on-error"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestCIOKReportsEveryRunToRedisFromTheRunner(t *testing.T) {
	t.Parallel()

	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf ciokWorkflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["ci-ok"]
	if !ok {
		t.Fatal("ci.yml has no ci-ok job")
	}
	var run, cond string
	found := 0
	for _, s := range job.Steps {
		if !strings.Contains(s.Run, runnerReceiptVerb) {
			continue
		}
		found++
		run, cond = s.Run, s.If
		if s.ContinueOnError {
			t.Errorf("step %q tolerates its own failure; a receipt that did not happen must redden ci-ok", s.Name)
		}
	}
	if found != 1 {
		t.Fatalf("ci-ok has %d steps calling %q, want exactly one", found, runnerReceiptVerb)
	}
	if strings.TrimSpace(cond) != "always()" {
		t.Errorf("the receipt step's if is %q, want always(): a red run is reported as red", cond)
	}
	if !strings.Contains(run, "set -euo pipefail") || strings.Contains(run, "|| true") {
		t.Errorf("the receipt step must fail loudly (set -euo pipefail, no `|| true`):\n%s", run)
	}

	// Every field of the record, from the run's own context, never a GitHub call.
	fields := map[string]string{
		"--repo":        "${{ github.repository }}",
		"--sha":         "${{ github.event.pull_request.head.sha || github.sha }}",
		"--run-id":      "${{ github.run_id }}",
		"--event":       "${{ github.event_name }}",
		"--head-branch": "${{ github.head_ref",
		"--base-branch": "${{ github.base_ref",
		"--pr":          "${{ github.event.pull_request.number }}",
		"--workflow":    "${{ github.workflow }}",
		"--conclusion":  "${{ job.status }}",
	}
	for flag, expr := range fields {
		if !strings.Contains(run, flag+` "`+expr) {
			t.Errorf("the receipt step does not pass %s from %q", flag, expr)
		}
	}
	// One --job per job ci-ok aggregates, each from needs.<job>.result.
	var needs []string
	if job.Needs.Kind == yaml.ScalarNode {
		needs = []string{job.Needs.Value}
	} else {
		for _, n := range job.Needs.Content {
			needs = append(needs, n.Value)
		}
	}
	if len(needs) == 0 {
		t.Fatal("ci-ok has no needs list")
	}
	for _, need := range needs {
		want := `--job "` + need + `=${{ needs.` + need + `.result }}"`
		if !strings.Contains(run, want) {
			t.Errorf("the receipt step does not pass %s", want)
		}
	}
	jobRx := regexp.MustCompile(`--job "([^=]+)=`)
	for _, m := range jobRx.FindAllStringSubmatch(run, -1) {
		known := false
		for _, need := range needs {
			known = known || need == m[1]
		}
		if !known {
			t.Errorf("the receipt step passes --job %s, which ci-ok does not need", m[1])
		}
	}
	if strings.Contains(run, "curl") || strings.Contains(run, "gh api") || strings.Contains(run, "api.github.com") {
		t.Errorf("the receipt step calls GitHub; the run's own context has every field:\n%s", run)
	}

	// The bench seat: the password from nova-secrets exec, never a flag or a file.
	for _, want := range []string{"nova-secrets\" exec", "--require NOVA_REDIS_BENCH_PASSWORD", "NOVA_SPRINT_REDIS_USER=bench",
		"NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD", `--redis "$NOVA_CARD_REDIS"`} {
		if !strings.Contains(run, want) {
			t.Errorf("the receipt step does not run as the bench seat: missing %q", want)
		}
	}
	if strings.Contains(run, "secrets.NOVA_REDIS") || strings.Contains(run, "--password") {
		t.Errorf("the receipt step carries the password some other way:\n%s", run)
	}
}
