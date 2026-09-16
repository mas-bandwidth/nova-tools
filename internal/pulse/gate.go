package pulse

// gate is rule C of #828 (pit stop 3): the mechanical red gate. The coordination loop's
// bash prototype stopped all-or-nothing -- one red CI run wrote one blanket STOP and the
// whole machine fell over -- so the gate fixes bug class C by naming the failing job and
// test, not an all-or-nothing STOP. It makes no model call: it reads the integration
// branch's latest CI run, and the STOP it writes on a red run is a specific two-line
// record, never a blanket.
//
// A red run writes STOP naming the branch, the sha, the failing job and the failing test,
// with the admission name (the issue or the test being admitted) on its second line. A
// cancelled or in-progress run is NOT red: it holds the previous verdict and leaves the
// STOP file alone. A green run removes the STOP the gate wrote -- told from a STOP a
// person wrote by its first line -- and keeps a person's STOP untouched. One GATE line,
// whatever the verdict.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Run is one CI run as the gate reads it: enough to name the branch, the sha, and what
// went red. Status and Conclusion are the values `gh run list --json` returns; Job and
// Test name the failing job and test so the STOP a red run writes is specific.
type Run struct {
	Branch     string
	SHA        string
	Status     string // queued, in_progress, completed, ...
	Conclusion string // success, failure, cancelled, skipped, ...
	Job        string // the failing job (workflow) name
	Test       string // the failing test name
}

// RunSource is the one query the gate makes: the integration branch's latest CI run. The
// gh-backed default sits behind the verb's flags; a test substitutes a fake so the gate
// runs with no network and no gh.
type RunSource interface {
	LatestRun(ctx context.Context, repo, branch string, timeout time.Duration) (Run, error)
}

// GHRunSource is the real source: `gh run list --json` for the integration branch, the
// newest run only.
type GHRunSource struct{}

// LatestRun runs `gh run list --json` and reduces the newest run to a Run.
func (GHRunSource) LatestRun(ctx context.Context, repo, branch string, timeout time.Duration) (Run, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "gh", "run", "list",
		"--repo", repo, "--branch", branch, "--limit", "1",
		"--json", "headBranch,headSha,status,conclusion,name,displayTitle")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return Run{}, fmt.Errorf("gh run list: %s", oneline.Escape(strings.TrimSpace(string(raw))))
	}
	var runs []struct {
		HeadBranch   string  `json:"headBranch"`
		HeadSHA      string  `json:"headSha"`
		Status       string  `json:"status"`
		Conclusion   *string `json:"conclusion"`
		Name         string  `json:"name"`
		DisplayTitle string  `json:"displayTitle"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		return Run{}, fmt.Errorf("gh run list: %v", err)
	}
	if len(runs) == 0 {
		return Run{}, fmt.Errorf("no run on %s@%s", repo, branch)
	}
	conclusion := ""
	if runs[0].Conclusion != nil {
		conclusion = *runs[0].Conclusion
	}
	return Run{
		Branch:     runs[0].HeadBranch,
		SHA:        runs[0].HeadSHA,
		Status:     runs[0].Status,
		Conclusion: conclusion,
		Job:        runs[0].Name,
		Test:       runs[0].DisplayTitle,
	}, nil
}

// GateInput is everything the gate verb needs, apart from flag parsing, so a test can
// drive it with a fake run source and a scratch root.
type GateInput struct {
	Repo      string
	Branch    string
	Root      string
	Admission string
	Timeout   time.Duration
	Stdout    io.Writer
	Stderr    io.Writer
	Source    RunSource
}

// gateStopPrefix is the first-line shape the gate writes, and how a STOP the gate wrote
// is told from a STOP a person wrote: only a STOP whose first line is the gate's machine
// shape is the gate's to remove on a green run.
const gateStopPrefix = "STOP branch="

// Gate reads the integration branch's latest run and applies one verdict: red writes a
// two-line STOP, green removes the STOP the gate wrote, held leaves the STOP alone.
func Gate(in GateInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Source == nil {
		in.Source = GHRunSource{}
	}
	if in.Timeout <= 0 {
		in.Timeout = 120 * time.Second
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Repo, "repo", "the owner/repo whose integration branch CI the gate reads"},
		{in.Branch, "branch", "the integration branch the gate reads"},
		{in.Root, "root", "the directory the STOP file lives in"},
		{in.Admission, "admission", "the admission name (issue or test), written on STOP's second line"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "GATE", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}

	run, err := in.Source.LatestRun(context.Background(), in.Repo, in.Branch, in.Timeout)
	if err != nil {
		return refusal(in.Stderr, "GATE", err)
	}

	stop := filepath.Join(in.Root, "STOP")

	switch verdictOf(run) {
	case verdictRed:
		if err := writeStop(stop, run, in.Admission); err != nil {
			return refusal(in.Stderr, "GATE", err)
		}
		fmt.Fprintf(in.Stdout, "GATE RED branch=%s sha=%s job=%s test=%s\n",
			oneline.Field(run.Branch), oneline.Field(run.SHA), oneline.Field(run.Job), oneline.Field(run.Test))
		return 0
	case verdictGreen:
		if isGateStop(stop) {
			if err := os.Remove(stop); err != nil {
				return refusal(in.Stderr, "GATE", err)
			}
		}
		fmt.Fprintf(in.Stdout, "GATE GREEN branch=%s sha=%s\n",
			oneline.Field(run.Branch), oneline.Field(run.SHA))
		return 0
	default:
		fmt.Fprintf(in.Stdout, "GATE HELD branch=%s sha=%s status=%s\n",
			oneline.Field(run.Branch), oneline.Field(run.SHA), oneline.Field(run.Status))
		return 0
	}
}

type gateVerdict int

const (
	verdictHeld gateVerdict = iota
	verdictGreen
	verdictRed
)

// verdictOf reduces a run to what the gate does. Only a completed, failed run is red; a
// completed, successful run is green; everything else -- not completed, cancelled, or
// skipped -- holds the previous verdict.
func verdictOf(run Run) gateVerdict {
	if run.Status != "completed" {
		return verdictHeld
	}
	switch run.Conclusion {
	case "success":
		return verdictGreen
	case "failure":
		return verdictRed
	default:
		return verdictHeld
	}
}

// writeStop writes the gate's two-line STOP: line 1 names the branch, sha, the failing
// job and the failing test; line 2 is the admission name.
func writeStop(path string, run Run, admission string) error {
	line1 := fmt.Sprintf("STOP branch=%s sha=%s job=%s test=%s",
		oneline.Field(run.Branch), oneline.Field(run.SHA), oneline.Field(run.Job), oneline.Field(run.Test))
	body := line1 + "\n" + oneline.Escape(admission) + "\n"
	return os.WriteFile(path, []byte(body), 0o644)
}

// isGateStop reports whether the STOP at path is the gate's own shape, so a green run can
// remove it without touching a STOP a person wrote.
func isGateStop(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return strings.HasPrefix(t, gateStopPrefix)
		}
	}
	return false
}
