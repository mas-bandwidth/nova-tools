package pulse

// The gate is the one writer of STOP. Pit stop 3, class C: STOP used to be all-or-nothing
// and written by a hand, so the red's own fix card could not launch (issue #828, bug 6) and
// a cancelled run read as red and froze the bench (bug 8). Here: a FAILED run writes STOP
// with the red's name in it, a CANCELLED or still-running one changes nothing, and a GREEN
// one lifts only the STOP this gate wrote. RESUME is the gate's alone -- a person's STOP is
// a person's to lift.

import (
	"context"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// StopFile is the halt, in the queue directory. Its line 1 is the gate's own record --
// MAIN-RED <sha> run=<id> job=<name> test=<name> -- and its line 2 is the admission name
// admission.go reads.
const StopFile = "STOP"

// CIRun is one run as `gh run list --json status,conclusion,headSha,databaseId,workflowName,event,createdAt,updatedAt`
// gives it. The gate reads only the ci workflow's push runs, so it can tell the branch's own
// run from a certification, workflow_dispatch, schedule or merge_group run at the same sha.
// CreatedAt/UpdatedAt are the run's wall, which the ci-wall rule (issue #888) reads.
type CIRun struct {
	ID         int64  `json:"databaseId"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"headSha"`
	Workflow   string `json:"workflowName"`
	Event      string `json:"event"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// CIJob is the failing job of a run: its name, and its log, which is where the failing test
// and the issue the red belongs to are named. StartedAt/CompletedAt are the job's wall,
// which the ci-wall rule reads to name the long pole.
type CIJob struct {
	Name        string `json:"name"`
	Log         string `json:"log"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

// RunSource is where the gate's verdict comes from. The shipped one wraps gh; a test's
// answers a fixture, so no test in this package reaches the network.
type RunSource interface {
	LatestRun(repo, branch string) ([]CIRun, error)
	FailedJob(repo string, runID int64) (CIJob, error)
}

// Decider is the typed decision the gate asks about a failing job: one call, a verdict
// choice and a noul. The shipped one is *decide.Client; a test passes a fake, so no test
// reaches the network.
type Decider interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// GateInput is the verb's input, held apart from flag parsing.
type GateInput struct {
	Repo   string
	Branch string
	Queue  string // the queue directory STOP lives in
	Source RunSource
	Stdout io.Writer
	Stderr io.Writer

	// Decide turns on the gate's typed decision: a failing job is classified behind Floor,
	// and a flaky verdict reruns the failed jobs once for the head. A decision below the
	// floor is a suggestion, never an authorization, and today's STOP stands.
	Decide  bool
	Floor   float64
	Decider Decider
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
