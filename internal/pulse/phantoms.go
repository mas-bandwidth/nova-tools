package pulse

// Rule E4: A RUNNER BUSY FOR A RUN THAT IS NOT RUNNING IS HOLDING A SLOT FOR NOBODY.
//
// 2026-09-16 (issue #828 class E; the ad hoc inventory on #854): ten runs the API still
// called in progress held eight Studio runners busy for two hours. Nothing on the repo was
// running -- the runs were the remains of cancelled shards whose jobs never finished
// cancelling -- and every queued PR waited behind them. A person found them by paging
// through the API by hand, run by run, and force-cancelled each one.
//
// Rule E2 (runners.go) restarts a runner stuck busy; that frees the machine but leaves the
// run, and a restarted runner picks the phantom job up again. This verb goes the other way,
// at the run: a busy runner that no in-progress job names is a phantom, and with
// --force-cancel the runs still holding its jobs are cancelled by force.
//
// The two halves are RunnerTable (runners.go, the busy list) and RunTable (here, the runs
// and their jobs). A test drives fakes: no test of this package cancels a run or reaches
// the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultPhantomScan is how many recent runs --force-cancel looks through for the jobs a
// phantom still holds. Ten phantom runs hid among about this many on 2026-09-16, and the
// scan is bounded because an unbounded one is a rate limit.
const DefaultPhantomScan = 60

// RunView is one workflow run, as little of it as the rule needs.
type RunView struct {
	ID     int64  `json:"databaseId"`
	Status string `json:"status"` // queued, in_progress, completed
}

// JobView is one job of one run: which runner it is on, and whether it claims to be running.
type JobView struct {
	ID       int64  `json:"id"`
	RunID    int64  `json:"run_id"`
	Status   string `json:"status"`
	RunnerNm string `json:"runner_name"`
}

// RunTable is the runs half of the rule. InProgressRuns answers "is anything actually
// running", Jobs answers "which runner is this run's work on", and ForceCancel is the only
// thing here that changes the world.
type RunTable interface {
	InProgressRuns(repo string) ([]RunView, error)
	RecentRuns(repo string, limit int) ([]RunView, error)
	Jobs(repo string, runID int64) ([]JobView, error)
	ForceCancel(repo string, runID int64) error
}

// PhantomsInput is `fleet phantoms`, held apart from flag parsing.
type PhantomsInput struct {
	Repo        string
	Runners     RunnerTable
	Runs        RunTable
	ForceCancel bool
	Scan        int // recent runs to look through, default DefaultPhantomScan
	DryRun      bool
	Stdout      io.Writer
	Stderr      io.Writer
}

// Phantoms finds the runners the API reports busy that no in-progress job names, and with
// --force-cancel cancels the runs whose jobs still sit on them. One PHANTOMS line, counts
// not lists.
//
// It returns 0 when the scan was complete, 1 when the API refused part of it (the line
// still says what was scanned: a partial answer named as partial is worth more than a
// refusal, because the person is looking at a jammed queue while they read it), and 2 when
// the invocation was unusable.
func Phantoms(in PhantomsInput) int {
	switch {
	case strings.TrimSpace(in.Repo) == "":
		fmt.Fprintf(in.Stderr, "PHANTOMS REFUSED: --repo is required, owner/name; refusing to guess\n")
		return 2
	case in.Runners == nil || in.Runs == nil:
		fmt.Fprintf(in.Stderr, "PHANTOMS REFUSED: no runner table is wired (this is a bug in the caller)\n")
		return 2
	}
	scan := in.Scan
	if scan <= 0 {
		scan = DefaultPhantomScan
	}
	partial := false

	runners, err := in.Runners.Runners(in.Repo)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PHANTOMS REFUSED: the runner list could not be read: %s\n", oneline.Err(err))
		return 2
	}
	busy := []string{}
	for _, r := range runners {
		if r.Busy {
			busy = append(busy, r.Name)
		}
	}

	// What is genuinely running. The API errs under load often enough that a single failure
	// must not read as "nothing is running" -- that reading would call every busy runner a
	// phantom and cancel the work of a healthy bench.
	running, err := retryOnce(func() ([]RunView, error) { return in.Runs.InProgressRuns(in.Repo) })
	if err != nil {
		fmt.Fprintf(in.Stderr, "PHANTOMS REFUSED: the in-progress runs could not be read twice: %s\n", oneline.Err(err))
		return 2
	}

	// A runner named by a job of an in-progress run is working, whatever else is true.
	working := map[string]bool{}
	for _, run := range running {
		jobs, err := retryOnce(func() ([]JobView, error) { return in.Runs.Jobs(in.Repo, run.ID) })
		if err != nil {
			// Unknown, so nothing here is called a phantom: the safe reading of an
			// unreadable run is that every runner it might name is busy on it.
			partial = true
			fmt.Fprintf(in.Stderr, "PHANTOMS NOTE run=%d jobs could not be read twice: %s\n", run.ID, oneline.Err(err))
			for _, name := range busy {
				working[name] = true
			}
			continue
		}
		for _, j := range jobs {
			if j.Status == "in_progress" || j.Status == "queued" {
				working[j.RunnerNm] = true
			}
		}
	}

	phantoms := []string{}
	for _, name := range busy {
		if !working[name] {
			phantoms = append(phantoms, name)
		}
	}
	slices.Sort(phantoms)

	scanned, cancelled := 0, 0
	if in.ForceCancel && len(phantoms) > 0 {
		cancelled, scanned, partial = cancelPhantomRuns(in, phantoms, scan, partial)
	}

	fmt.Fprintf(in.Stdout, "PHANTOMS repo=%s runners=%d busy=%d phantom=%d scanned=%d cancelled=%d force=%t complete=%t\n",
		oneline.Field(in.Repo), len(runners), len(busy), len(phantoms), scanned, cancelled, in.ForceCancel, !partial)
	if partial {
		return 1
	}
	return 0
}

// cancelPhantomRuns looks through the recent runs of any status -- a phantom's run often
// reads `completed` while its job still reads `in_progress`, which is the whole disease --
// and force-cancels every run holding a job on a phantom runner. Each run is cancelled once.
func cancelPhantomRuns(in PhantomsInput, phantoms []string, scan int, partial bool) (cancelled, scanned int, stillPartial bool) {
	isPhantom := map[string]bool{}
	for _, p := range phantoms {
		isPhantom[p] = true
	}
	recent, err := retryOnce(func() ([]RunView, error) { return in.Runs.RecentRuns(in.Repo, scan) })
	if err != nil {
		fmt.Fprintf(in.Stderr, "PHANTOMS NOTE the recent runs could not be read twice: %s\n", oneline.Err(err))
		return 0, 0, true
	}
	notes := &boundedNotes{w: in.Stderr, kind: "PHANTOMS", max: 5}
	defer notes.close()
	done := map[int64]bool{}
	for _, run := range recent {
		scanned++
		jobs, err := retryOnce(func() ([]JobView, error) { return in.Runs.Jobs(in.Repo, run.ID) })
		if err != nil {
			partial = true
			notes.note("PHANTOMS NOTE run=%d jobs could not be read twice: %s", run.ID, oneline.Err(err))
			continue
		}
		for _, j := range jobs {
			if j.Status != "in_progress" || !isPhantom[j.RunnerNm] || done[run.ID] {
				continue
			}
			done[run.ID] = true
			cancelled++
			if in.DryRun {
				continue
			}
			if err := in.Runs.ForceCancel(in.Repo, run.ID); err != nil {
				partial = true
				cancelled--
				notes.note("PHANTOMS NOTE run=%d could not be force-cancelled: %s", run.ID, oneline.Err(err))
			}
		}
	}
	return cancelled, scanned, partial
}

// retryOnce is the API's own flakiness, handled once and named when it persists. Twice is
// enough: a third try is a rate limit, and the line that says what was scanned is the
// better answer.
func retryOnce[T any](call func() (T, error)) (T, error) {
	out, err := call()
	if err == nil {
		return out, nil
	}
	return call()
}

// GHRuns is the real RunTable: bounded gh calls, the runs and their jobs by the API's own
// paging, and `--force-cancel` as the API spells it -- the plain cancel is what left these
// runs in this state in the first place.
type GHRuns struct{ Timeout time.Duration }

func (g GHRuns) InProgressRuns(repo string) ([]RunView, error) {
	return g.runs("repos/" + repo + "/actions/runs?status=in_progress&per_page=50")
}

func (g GHRuns) RecentRuns(repo string, limit int) ([]RunView, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	return g.runs(fmt.Sprintf("repos/%s/actions/runs?per_page=%d", repo, limit))
}

func (g GHRuns) runs(path string) ([]RunView, error) {
	out, err := g.sh("gh", "api", path, "--jq", "[.workflow_runs[] | {databaseId: .id, status: .status}]")
	if err != nil {
		return nil, err
	}
	var runs []RunView
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return nil, fmt.Errorf("the run list did not parse: %w", err)
	}
	return runs, nil
}

func (g GHRuns) Jobs(repo string, runID int64) ([]JobView, error) {
	out, err := g.sh("gh", "api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, runID),
		"--jq", "[.jobs[] | {id: .id, run_id: .run_id, status: .status, runner_name: .runner_name}]")
	if err != nil {
		return nil, err
	}
	var jobs []JobView
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &jobs); err != nil {
		return nil, fmt.Errorf("the job list of run %d did not parse: %w", runID, err)
	}
	return jobs, nil
}

func (g GHRuns) ForceCancel(repo string, runID int64) error {
	_, err := g.sh("gh", "api", "-X", "POST", fmt.Sprintf("repos/%s/actions/runs/%d/force-cancel", repo, runID))
	return err
}

func (g GHRuns) sh(name string, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(raw), nil
}
