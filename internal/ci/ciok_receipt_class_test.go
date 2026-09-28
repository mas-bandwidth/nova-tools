package ci

import (
	"path/filepath"
	"strings"
	"testing"

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
// (deprecated, Glenn 2026-09-27), no installed receipt writer under
// .local/bin, and probes no installed binary. This test reads the step as
// YAML and holds it to that shape, the command text exactly, and holds the
// two conditions exactly: the job's, with no head-repo guard, and the step's,
// with it.

const runnerReceiptVerb = "github receipt --from-runner"

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

// receiptRun is the step's whole run block, exactly: the prelude that reads
// the bench's card.env, then the bench seat's nova-secrets wrapper around this
// tree's writer. Nothing may stand before the exec (a cd, an export PATH) or
// after it.
const receiptRun = `set -euo pipefail
env_file="$HOME/nova-bench/launch/card.env"
[ -f "$env_file" ] || { echo "no $env_file on this runner: run the rowan-tools bench play on $(hostname) (it writes the bench seat and store address)"; exit 1; }
# shellcheck disable=SC1090
. "$env_file"
: "${NOVA_BENCH_SEAT:?card.env names no NOVA_BENCH_SEAT}" "${NOVA_BENCH_SOPS:?card.env names no NOVA_BENCH_SOPS}" "${NOVA_CARD_REDIS:?card.env names no NOVA_CARD_REDIS}"
exec "$HOME/.local/bin/nova-secrets" exec --store "$HOME/nova-bench/secrets" --as "$NOVA_BENCH_SEAT" \
  --key "$HOME/.config/nova-secrets/$NOVA_BENCH_SEAT.key" --sops "$NOVA_BENCH_SOPS" \
  --only NOVA_REDIS_BENCH_PASSWORD --require NOVA_REDIS_BENCH_PASSWORD -- \
  /usr/bin/env NOVA_SPRINT_REDIS_USER=bench NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD \
  go run ./cmd/nova-ci github receipt --from-runner --redis "$NOVA_CARD_REDIS" \
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
	if !strings.Contains(run, "set -euo pipefail") || strings.Contains(run, "|| true") {
		t.Errorf("the receipt step must fail loudly (set -euo pipefail, no `|| true`):\n%s", run)
	}

	// The whole run block, exactly, with each line's indentation taken off.
	if got, want := strings.Join(runLines(run), "\n"), strings.Join(runLines(receiptRun), "\n"); got != want {
		t.Errorf("the receipt step's run block is not the card.env prelude and this tree's nova-ci under the bench seat:\n got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(run, "--job") != 0 || strings.Contains(run, "--event") || strings.Contains(run, "-branch") {
		t.Errorf("the receipt step passes a flag the row does not carry (--job, --event, --head-branch, --base-branch):\n%s", run)
	}
	if strings.Contains(run, "curl") || strings.Contains(run, "gh api") || strings.Contains(run, "api.github.com") {
		t.Errorf("the receipt step calls GitHub; the run's own context has every field:\n%s", run)
	}

	// The writer is this tree's, never an installed build: no nova-sprint, no
	// receipt writer under .local/bin (the nova-secrets wrapper is the one
	// installed binary), no probe of an installed binary's version or flags.
	for _, never := range []string{"nova-sprint", ".local/bin/nova-ci", "~/.local/bin", "installed", "RECEIPT WRITER",
		"flag provided but not defined", "probe", "command -v nova", "which nova", "version"} {
		if strings.Contains(run, never) {
			t.Errorf("the receipt step names %q; the writer is this tree's nova-ci and nothing installed is probed:\n%s", never, run)
		}
	}
	if strings.Count(run, ".local/bin/") != 1 || !strings.Contains(run, `"$HOME/.local/bin/nova-secrets" exec`) {
		t.Errorf("the receipt step runs an installed binary other than the nova-secrets wrapper:\n%s", run)
	}
	if strings.Contains(run, "secrets.NOVA_REDIS") || strings.Contains(run, "--password") {
		t.Errorf("the receipt step carries the password some other way:\n%s", run)
	}
}
