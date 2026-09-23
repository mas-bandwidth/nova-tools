// Package fleetkube is the pod side of SPEC-FLEET-KUBE: one card per Job, and what that
// pod does from the harness to the clip (Part 2, "Logs, the timeline, and the clip").
//
// The pod's stdout is the harness log and goes to the node's log store; the pod's own
// start, end, exit and cost are appended to the SAME usage.tsv the launcher writes
// (swarm.CardUsageColumns, via swarm.AppendCardUsage), so nova-pulse and nova-swarm read one
// schema and one file whether the card ran under the launcher or a pod. After each card the
// pod's last step is the clip (swarm.Clip, SPEC-JOBS section 6): commit the card's branch,
// harvest its RESULT.md, reset the kept worktree to base.
package fleetkube

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The pod's steps, in the one order a pod runs them.
const (
	StepHarness = "harness"
	StepUsage   = "usage"
	StepClip    = "clip"
)

// PodSteps is the declared order. The clip is last: nothing a pod does comes after it.
var PodSteps = []string{StepHarness, StepUsage, StepClip}

// ResultFile is the card's result inside the worktree, harvested into the job dir.
const ResultFile = "RESULT.md"

// HarnessRun is what the harness is handed: the worktree to work in and the pod's stdout.
type HarnessRun struct {
	Worktree string
	Stdout   io.Writer
}

// Harness runs the card and returns its exit code and what the provider billed. A usage
// the provider did not report keeps its dashes, never zeros.
type Harness func(HarnessRun) (rc int, cost swarm.ProviderUsage)

// Pod is one card's pod.
type Pod struct {
	Label    string // the card's label, the usage row's job column
	Attempt  int    // the attempt number; 0 is taken as 1
	Worktree string // the kept worktree
	Branch   string // the card's topic branch
	Base     string // the base the clip resets the worktree to
	JobDir   string // the job dir on the shared volume: RESULT.md and usage.tsv land here
	Provider string
	Model    string

	Harness Harness
	Stdout  io.Writer        // the pod's stdout (the node's log store); nil is os.Stdout
	Now     func() time.Time // nil is time.Now
	OnStep  func(step string)
}

// PodReport is what a pod did, in order.
type PodReport struct {
	Steps []string
	RC    int
	Row   swarm.UsageRow
	Clip  swarm.ClipResult
}

// UsagePath is the job's usage.tsv: the same file the launcher appends to.
func (p Pod) UsagePath() string { return filepath.Join(p.JobDir, "usage.tsv") }

// RunPod runs the card's harness, appends the pod's usage row, then clips. The usage row and
// the clip run whatever the harness's exit code: a failing card is accounted and clipped too.
func RunPod(p Pod) (PodReport, error) {
	if p.Harness == nil {
		return PodReport{}, fmt.Errorf("the pod has no harness to run")
	}
	for name, v := range map[string]string{"label": p.Label, "worktree": p.Worktree, "branch": p.Branch, "base": p.Base, "job dir": p.JobDir} {
		if strings.TrimSpace(v) == "" {
			return PodReport{}, fmt.Errorf("the pod's %s is empty", name)
		}
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	stdout := p.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	var rep PodReport
	step := func(s string) {
		rep.Steps = append(rep.Steps, s)
		if p.OnStep != nil {
			p.OnStep(s)
		}
	}

	// (1) THE HARNESS. Its log is the pod's stdout, raw; nothing of it goes to usage.tsv.
	step(StepHarness)
	start := now()
	rc, cost := p.Harness(HarnessRun{Worktree: p.Worktree, Stdout: stdout})
	end := now()
	rep.RC = rc

	// (2) THE USAGE ROW, in the launcher's schema, appended to the same file.
	step(StepUsage)
	rep.Row = UsageRow(p, start, end, rc, cost)
	if err := os.MkdirAll(p.JobDir, 0o755); err != nil {
		return rep, err
	}
	if err := swarm.AppendCardUsage(p.UsagePath(), rep.Row); err != nil {
		return rep, fmt.Errorf("the usage row could not be appended to %s: %w", p.UsagePath(), err)
	}

	// (3) THE CLIP, last.
	step(StepClip)
	clip, err := swarm.Clip(swarm.ClipRequest{
		Worktree: p.Worktree, Branch: p.Branch, Base: p.Base,
		Message: "card " + p.Label, Result: ResultFile, Harvest: p.JobDir,
	})
	rep.Clip = clip
	if err != nil {
		return rep, fmt.Errorf("the clip failed: %w", err)
	}
	return rep, nil
}

// UsageRow is the pod's row: its own start, end, exit and cost, in swarm.CardUsageColumns.
func UsageRow(p Pod, start, end time.Time, rc int, cost swarm.ProviderUsage) swarm.UsageRow {
	attempt := p.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	row := swarm.UsageRow{
		"job": p.Label, "attempt": strconv.Itoa(attempt),
		"started":  start.UTC().Format(time.RFC3339),
		"ended":    end.UTC().Format(time.RFC3339),
		"rc":       strconv.Itoa(rc),
		"provider": dash(p.Provider), "model": dash(p.Model),
	}
	for _, c := range swarm.TokenColumns {
		row[c] = dash(cost.Values[c])
	}
	row["usd"] = dash(cost.Values["usd"])
	return row
}

// JobCommand is the Job container's command list, in PodSteps order: the card's harness
// command as the caller built it (nova-secrets exec ... nova-swarm native ...), then the clip
// as the final command. The usage row is written by the pod runner between the two.
func JobCommand(p Pod, harness string) []string {
	return []string{
		harness,
		"nova-work clip --worktree " + shq(p.Worktree) + " --branch " + shq(p.Branch) +
			" --base " + shq(p.Base) + " --result " + ResultFile + " --harvest " + shq(p.JobDir),
	}
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return swarm.Dash
	}
	return s
}

// shq single-quotes a word for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
