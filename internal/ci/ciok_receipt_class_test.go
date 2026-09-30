package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// THE RUNNER IS THE EVENT SOURCE. The signed webhook receiver sits behind a
// tailscale funnel kept off by design, and nothing polls GitHub for a check
// state (TestNoPollingPathsRemain). Our runners are self-hosted on the tailnet
// and run as the bench seat, so ci-ok reports the run itself at its end: one
// ev:github row (internal/cireceipt), the row `nova-wake watch --store`
// blocks on. The step must carry every field the row needs and must fail the
// job when the write fails: a receipt that silently did not happen must never
// read as one that did. The writer is this tree's nova-ci, run from the
// checkout (`go run ./cmd/nova-ci`), so it is versioned with the commit under
// test and no runner's installed build matters: the step names no nova-sprint
// (deprecated), no installed receipt writer under .local/bin, and probes no
// installed binary. The step is one call into tools/ci's report-run verb, which
// reads the bench's card.env and runs the writer under nova-secrets. This test
// reads the step as YAML and holds it to that shape, the call text exactly, and
// holds the two conditions exactly: the job's, with no head-repo guard, and the
// step's, with it; it then reads the verb's source for what the step used to
// carry inline (the card.env names, the nova-secrets wrapper, this tree's
// writer), and tools/ci's own tests run the verb.

const runnerReceiptVerb = `"$RUNNER_TEMP/ci" report-run`

// reportRunSource is the verb the step calls.
const reportRunSource = "tools/ci/reportrun.go"

// ciokIf is the ci-ok job's condition, exactly: every event but the nightly
// schedule, and NO head-repo guard. Every self-hosted job carries that guard,
// so on a fork's pull_request they are all skipped and ci-ok reads the skips
// as red, which is what keeps a fork PR out of the merge queue. A guard on the
// job would skip ci-ok instead, and GitHub counts a skipped required check as
// passing (cold read of #4495, 2026-09-27).
const ciokIf = `always() && github.event_name != 'schedule'`

// receiptStepIf is the receipt step's condition, exactly: always(), so a red
// run is reported as red, and the head-repo guard, because this step runs the
// checked-out tree's code holding the bench seat's password and a
// pull_request from a fork must never reach it. The guard is on the step and
// not on the job, so the fork PR's ci-ok still runs and stays red.
const receiptStepIf = `always() && (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository)`

// receiptRun is the step's whole run block, exactly: one call into the verb with
// the run's own context, and nothing before it or after it.
const receiptRun = `"$RUNNER_TEMP/ci" report-run \
  --repo "${{ github.repository }}" \
  --sha "${{ github.event.pull_request.head.sha || github.sha }}" \
  --run-id "${{ github.run_id }}" \
  --pr "${{ github.event.pull_request.number }}" \
  --workflow "${{ github.workflow }}" \
  --conclusion "${{ job.status }}"`

// runLines is a run block's non-blank lines with their indentation taken off.
func runLines(run string) []string {
	var out []string
	for _, l := range strings.Split(run, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

type ciokWorkflow struct {
	Jobs map[string]struct {
		If    string `yaml:"if"`
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
	if strings.TrimSpace(job.If) != ciokIf {
		t.Errorf("the ci-ok job's if is\n  %s\nwant\n  %s\n(no head-repo guard on the job: a skipped ci-ok counts as a passing required check, so a fork PR could enter the merge queue with no PR-stage CI; the guard belongs on the receipt step)", job.If, ciokIf)
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
	if strings.TrimSpace(cond) != receiptStepIf {
		t.Errorf("the receipt step's if is\n  %s\nwant\n  %s\n(always(): a red run is reported as red; the head-repo guard: a fork's pull_request must not run this tree's receipt writer with the bench password)", cond, receiptStepIf)
	}
	if strings.Contains(run, "|| true") {
		t.Errorf("the receipt step must fail loudly (no `|| true`):\n%s", run)
	}

	// The whole run block, exactly, with each line's indentation taken off.
	if got, want := strings.Join(runLines(run), "\n"), strings.Join(runLines(receiptRun), "\n"); got != want {
		t.Errorf("the receipt step's run block is not one call into the built tools/ci report-run with the run's own context:\n got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(run, "--job") != 0 || strings.Contains(run, "--event") || strings.Contains(run, "-branch") {
		t.Errorf("the receipt step passes a flag the row does not carry (--job, --event, --head-branch, --base-branch):\n%s", run)
	}
	if strings.Contains(run, "curl") || strings.Contains(run, "gh api") || strings.Contains(run, "api.github.com") {
		t.Errorf("the receipt step calls GitHub; the run's own context has every field:\n%s", run)
	}
	assert.False(t, strings.Contains(run, "secrets.NOVA_REDIS") || strings.Contains(run, "--password"), "the receipt step carries the password some other way:\n%s", run)

	// What the step used to carry inline is the verb's now. The writer is this
	// tree's, never an installed build: no nova-sprint, no receipt writer under
	// .local/bin (the nova-secrets wrapper is the one installed binary), no probe
	// of an installed binary's version or flags; the bench's card.env must name
	// the three values and the verb refuses when it does not.
	verb := readFile(t, filepath.Join(repoRoot(t), reportRunSource))
	for _, want := range []string{
		`"go", "run", "./cmd/nova-ci", "github", "receipt", "--from-runner", "--redis"`,
		`filepath.Join(home, ".local", "bin", "nova-secrets")`,
		`"--only", "NOVA_REDIS_BENCH_PASSWORD", "--require", "NOVA_REDIS_BENCH_PASSWORD"`,
		`"NOVA_BENCH_SEAT", "NOVA_BENCH_SOPS", "NOVA_CARD_REDIS"`,
		`card.env names no %s`,
	} {
		assert.Contains(t, verb, want, reportRunSource)
	}
	code := verbCode(verb)
	for _, never := range []string{"nova-sprint", ".local/bin/nova-ci", "~/.local/bin", "RECEIPT WRITER",
		"flag provided but not defined", "probe", "command -v nova", "which nova", "--password", "api.github.com"} {
		assert.NotContains(t, code, never, "%s names %q; the writer is this tree's nova-ci and nothing installed is probed", reportRunSource, never)
	}
	assert.Equal(t, 1, strings.Count(code, `".local", "bin"`), "%s runs an installed binary other than the nova-secrets wrapper", reportRunSource)
}

// verbCode is a Go file's text with its comments and its verb help (the raw-string
// block that describes what the verb does in words) taken out, so a law about what
// the code names does not read the prose that explains it.
func verbCode(src string) string {
	var out []string
	inHelp := false
	for _, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.Contains(l, "help: `") {
			inHelp = true
		}
		if inHelp {
			if strings.HasPrefix(t, "`,") || strings.Contains(l, "`,") && !strings.Contains(l, "help: `") {
				inHelp = false
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
