package pulse

// The gate is the one writer of STOP. Pit stop 3, class C: STOP used to be all-or-nothing
// and written by a hand, so the red's own fix card could not launch (issue #828, bug 6) and
// a cancelled run read as red and froze the bench (bug 8). Here: a FAILED run writes STOP
// with the red's name in it, a CANCELLED or still-running one changes nothing, and a GREEN
// one lifts only the STOP this gate wrote. RESUME is the gate's alone -- a person's STOP is
// a person's to lift.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// StopFile is the halt, in the queue directory. Its line 1 is the gate's own record --
// MAIN-RED <sha> run=<id> job=<name> test=<name> -- and its line 2 is the admission name
// admission.go reads.
const StopFile = "STOP"

// StopMark is what makes a STOP the gate's own. A STOP whose first line does not begin with
// it was written by a person, and no green lifts it.
const StopMark = "MAIN-RED"

// gateWorkflow and gateEvent name the one run the gate reads for the branch tip: the ci
// workflow's push run, the CL tier's own verdict. A certification, workflow_dispatch,
// schedule or merge_group run at the same sha never decides the branch (issue #879).
const (
	gateWorkflow = "ci"
	gateEvent    = "push"
)

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

// Rerunner is the optional half of a RunSource that can ask the provider to rerun a run's
// failed jobs. The shipped ghRunSource implements it; a source that cannot rerun leaves the
// gate's STOP as it is today.
type Rerunner interface {
	RerunFailed(repo string, runID int64) error
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

// ciWallMax is the CI wall that earns a pit stop (issue #888, Glenn 2026-09-17:
// "when it is over 2 minutes, you pit stop and fix that"). A run whose wall
// (created_at to updated_at) is over it writes a CI-WALL line to <queue>/REDS
// and the gate's status line carries STOP: ci-wall.
const ciWallMax = 120

// wallJobser is the jobs API the gate already fetches, read for walls: the job
// with the largest wall is the long pole. A source that does not offer it
// leaves the pole unknown ("-"), never a guess.
type wallJobser interface {
	Jobs(repo string, runID int64) ([]CIJob, error)
}

// stampWall is the seconds between two RFC3339 stamps, false when either is missing.
func stampWall(from, to string) (int64, bool) {
	if from == "" || to == "" {
		return 0, false
	}
	a, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return 0, false
	}
	b, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return 0, false
	}
	return int64(b.Sub(a).Seconds()), true
}

// runWall is the run's wall from the runs API the gate already reads.
func runWall(r CIRun) (int64, bool) { return stampWall(r.CreatedAt, r.UpdatedAt) }

// jobWall is one job's wall from the jobs API the gate already fetches.
func jobWall(j CIJob) (int64, bool) { return stampWall(j.StartedAt, j.CompletedAt) }

// longPole is the job with the largest wall, or "-" when unknown.
func longPole(in GateInput, run CIRun) (string, string) {
	if s, ok := in.Source.(wallJobser); ok {
		if jobs, err := s.Jobs(in.Repo, run.ID); err == nil {
			best, bestWall, found := "-", int64(-1), false
			for _, j := range jobs {
				w, ok := jobWall(j)
				if !ok {
					continue
				}
				if !found || w > bestWall {
					best, bestWall, found = j.Name, w, true
				}
			}
			if found {
				if best == "" {
					best = "-"
				}
				return oneline.Field(best), fmt.Sprintf("%d", bestWall)
			}
		}
	}
	return "-", "-"
}

// checkCIWall records the run's wall for the branch tip: a wall over ciWallMax
// writes one CI-WALL line to <queue>/REDS and reports the STOP suffix the
// gate's status line carries. A wall under it changes nothing.
func checkCIWall(in GateInput, run CIRun) string {
	wall, ok := runWall(run)
	if !ok || wall <= ciWallMax {
		return ""
	}
	name, jobWall := longPole(in, run)
	if err := os.MkdirAll(in.Queue, 0o755); err == nil {
		appendLine(filepath.Join(in.Queue, "REDS"),
			fmt.Sprintf("CI-WALL %d %d long-pole=%s %s", run.ID, wall, name, jobWall))
	}
	return " STOP: ci-wall"
}

// failingTest matches the failing test's name in a Go test log.
var failingTest = regexp.MustCompile(`(?m)^\s*--- FAIL: ([^\s/]+)`)

// issueRef matches the issue a job log names, which is the admission name when there is one.
var issueRef = regexp.MustCompile(`#([0-9]+)\b`)

// Gate reads the branch's latest ci run and writes, holds or lifts the STOP. It prints
// exactly ONE line: GATE RED (exit 1, the check says NO), GATE HELD or GATE GREEN (exit 0),
// or GATE REFUSED (exit 2) when the source could not answer -- a dead source is never green.
func Gate(in GateInput) int {
	// A verb validates its own inputs as well as its flags do, so a programmatic caller
	// cannot write a STOP at the root of wherever the process is standing (taken from the
	// shape of PR #833), and a nil writer is never a panic.
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	for _, need := range []struct{ value, name, wants string }{
		{in.Repo, "repo", "the repository the ci run belongs to, owner/name"},
		{in.Branch, "branch", "the integration branch this gate holds"},
		{in.Queue, "queue", "the queue directory STOP lives in"},
	} {
		if strings.TrimSpace(need.value) == "" {
			fmt.Fprintf(in.Stderr, "GATE REFUSED: missing --%s; refusing to guess (%s)\n", need.name, need.wants)
			return 2
		}
	}
	if in.Source == nil {
		fmt.Fprintf(in.Stderr, "GATE REFUSED: no run source; refusing to guess (pass --source <file>, or build one with NewGHRunSource)\n")
		return 2
	}
	if in.Decide && in.Decider == nil {
		fmt.Fprintf(in.Stderr, "GATE REFUSED: --decide needs a decider; refusing to guess (set --key-env, or drop --decide)\n")
		return 2
	}

	stop := filepath.Join(in.Queue, StopFile)
	runs, err := in.Source.LatestRun(in.Repo, in.Branch)
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATE REFUSED repo=%s branch=%s: %s (the gate never guesses a verdict; fix the source, or pass --source <file>)\n",
			oneline.Field(in.Repo), oneline.Field(in.Branch), oneline.Err(err))
		return 2
	}
	run, ok := ciPushRun(runs)
	if !ok {
		// No ci push run for the branch tip: NOT a verdict. The certification that is
		// green says nothing about the tier the gate holds, and a run of another event
		// at the same sha is a run of another lane.
		fmt.Fprintf(in.Stdout, "GATE HELD repo=%s branch=%s reason=no-ci-push-run stop=unchanged\n",
			oneline.Field(in.Repo), oneline.Field(in.Branch))
		return 0
	}

	switch runVerdict(run) {
	case "red":
		wallStop := checkCIWall(in, run)
		job, jerr := in.Source.FailedJob(in.Repo, run.ID)
		name, test := "-", "-"
		if jerr == nil {
			if job.Name != "" {
				name = job.Name
			}
			if m := failingTest.FindStringSubmatch(job.Log); m != nil {
				test = m[1]
			}
		}
		// The typed decision, when asked for: a flaky verdict at or above the floor reruns
		// the failed jobs once for this head; anything else keeps today's STOP.
		decideField := ""
		if in.Decide {
			verdict, conf := in.classify(job)
			value := "?"
			if verdict != "" && conf >= in.Floor {
				value = verdict
			}
			decideField = fmt.Sprintf(" decide=%s conf=%.2f", oneline.Field(value), conf)
			if verdict == "flaky" && conf >= in.Floor {
				if r, ok := in.Source.(Rerunner); ok && !hasRerun(in.Queue, run.HeadSHA) {
					if err := r.RerunFailed(in.Repo, run.ID); err == nil {
						if err := writeRerunMarker(in.Queue, run.HeadSHA, name); err != nil {
							fmt.Fprintf(in.Stderr, "GATE REFUSED queue=%s: %s (the gate could not write the rerun marker; fix the directory)\n", oneline.Field(in.Queue), oneline.Err(err))
							return 2
						}
						fmt.Fprintf(in.Stdout, "GATE RERUN sha=%s job=%s conf=%.2f\n", sha12(run.HeadSHA), oneline.Field(name), conf)
						return 0
					}
				}
			}
		}
		admit := admissionName(job.Log, test)
		body := fmt.Sprintf("%s %s run=%d job=%s test=%s\n", StopMark, sha12(run.HeadSHA), run.ID, oneline.Field(name), oneline.Field(test))
		if admit != "" {
			body += admit + "\n"
		}
		if err := os.MkdirAll(in.Queue, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "GATE REFUSED queue=%s: %s (the queue directory is where STOP lives; make it)\n", oneline.Field(in.Queue), oneline.Err(err))
			return 2
		}
		if err := os.WriteFile(stop, []byte(body), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "GATE REFUSED queue=%s: %s (the gate could not write STOP; check the directory)\n", oneline.Field(in.Queue), oneline.Err(err))
			return 2
		}
		fmt.Fprintf(in.Stdout, "GATE RED repo=%s branch=%s sha=%s run=%d job=%s test=%s admit=%s stop=written%s\n",
			oneline.Field(in.Repo), oneline.Field(in.Branch), sha12(run.HeadSHA), run.ID,
			oneline.Field(name), oneline.Field(test), oneline.Field(dash(admit)), wallStop+decideField)
		return 1

	case "green":
		wallStop := checkCIWall(in, run)
		state := "none"
		if raw, err := os.ReadFile(stop); err == nil {
			if strings.HasPrefix(strings.TrimSpace(string(raw)), StopMark) {
				if err := os.Remove(stop); err != nil {
					fmt.Fprintf(in.Stderr, "GATE REFUSED queue=%s: %s (the gate could not lift its own STOP; remove it by hand)\n", oneline.Field(in.Queue), oneline.Err(err))
					return 2
				}
				state = "cleared"
			} else {
				// A person wrote this one. RESUME is the gate's alone, and this is not the
				// gate's STOP: a green never lifts a person's hold.
				state = "kept"
			}
		}
		fmt.Fprintf(in.Stdout, "GATE GREEN repo=%s branch=%s sha=%s run=%d stop=%s%s\n",
			oneline.Field(in.Repo), oneline.Field(in.Branch), sha12(run.HeadSHA), run.ID, state, wallStop)
		return 0

	default:
		wallStop := checkCIWall(in, run)
		// Cancelled, queued, still running, or no run at all: NOT a verdict. Bug 8 is what
		// reading one as red costs -- a cancelled dev run froze the bench for six minutes.
		fmt.Fprintf(in.Stdout, "GATE HELD repo=%s branch=%s sha=%s run=%d reason=%s stop=unchanged%s\n",
			oneline.Field(in.Repo), oneline.Field(in.Branch), sha12(run.HeadSHA), run.ID, oneline.Field(heldReason(run)), wallStop)
		return 0
	}
}

// verdict reads one run: only a completed run with a conclusive conclusion is a verdict.
func runVerdict(r CIRun) string {
	if r.ID == 0 && r.Status == "" {
		return "held"
	}
	if r.Status != "completed" {
		return "held"
	}
	switch r.Conclusion {
	case "success":
		return "green"
	case "failure", "timed_out", "startup_failure":
		return "red"
	default:
		// cancelled, skipped, neutral, action_required: nothing changes on these.
		return "held"
	}
}

// ciPushRun is the gate's one run: the newest ci workflow run whose event is push, whatever
// its status -- an in-progress push run is a hold, never a verdict from an older run or a
// run of another workflow at the same sha. The list comes newest first from gh.
func ciPushRun(runs []CIRun) (CIRun, bool) {
	for _, r := range runs {
		if r.Workflow == gateWorkflow && r.Event == gateEvent {
			return r, true
		}
	}
	return CIRun{}, false
}

// heldReason names why a run is not a verdict, in one token.
func heldReason(r CIRun) string {
	if r.ID == 0 && r.Status == "" {
		return "no-run"
	}
	if r.Status != "completed" {
		return r.Status
	}
	if r.Conclusion == "" {
		return "no-conclusion"
	}
	return r.Conclusion
}

// admissionName is STOP's line 2: the issue the failing job's log names, else the failing
// test's name. When it is neither -- a red nobody can name -- there is no line 2 at all,
// and admission.go admits nothing: a red we cannot name is a red we cannot work around.
func admissionName(log, test string) string {
	if m := issueRef.FindStringSubmatch(log); m != nil {
		return "#" + m[1]
	}
	if test != "" && test != "-" {
		return test
	}
	return ""
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// classify asks the decider one typed decision about the failing job: a verdict choice
// among flaky, real and unknown, and a noul naming whether the failure matches an open
// known-flaky pattern. An error or a missing answer is no verdict at all.
func (in GateInput) classify(job CIJob) (string, float64) {
	answers, _, err := in.Decider.Decide(context.Background(), in.gateState(job), gateQuestions())
	if err != nil {
		return "", 0
	}
	a, ok := answers["verdict"]
	if !ok {
		return "", 0
	}
	return a.Choice, a.Confidence
}

// gateQuestions is the typed decision the gate asks: the verdict the rerun turns on, and
// the noul that says whether the failure is one of the known flakes.
func gateQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"verdict": {
			Instructions: "Classify the failing CI job from its log tail. flaky: a timing, network, runner or rate-limit failure unrelated to the change. real: a test or build failure caused by the code. unknown: cannot tell from the tail.",
			Choice: map[string]string{
				"flaky":   "a timing, network, runner or rate-limit failure unrelated to the change",
				"real":    "a test or build failure caused by the code",
				"unknown": "cannot tell from the tail",
			},
		},
		"same_class_as_known": {
			Instructions: "Is this failure the same class as one of the known-flaky patterns in the state? Answer noul (null) when it is not.",
			Noul:         true,
		},
	}
}

// gateState is what the decision is given and nothing else: the failing job's name, the
// last 60 lines of its log escaped as triage escapes evidence, and the open known-flaky
// patterns of <queue>/FLAKY.txt, one regex per line.
func (in GateInput) gateState(job CIJob) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CI JOB %s FAILED\n", oneline.Field(job.Name))
	b.WriteString("LOG TAIL\n")
	b.WriteString(logTail(job.Log, 60))
	b.WriteString("KNOWN-FLAKY PATTERNS\n")
	patterns := readLines(filepath.Join(in.Queue, "FLAKY.txt"))
	if len(patterns) == 0 {
		b.WriteString("none\n")
	}
	for _, p := range patterns {
		fmt.Fprintf(&b, "%s\n", oneline.Cap(oneline.Escape(p), evidenceMax))
	}
	return b.String()
}

// logTail is the last n lines of a log, each escaped and capped as triage escapes evidence
// so the state is a bounded packet and never a transcript.
func logTail(log string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(oneline.Cap(oneline.Escape(l), evidenceMax))
		b.WriteByte('\n')
	}
	return b.String()
}

// rerunMarker is the file that records one rerun for a head. The gate reads it before it
// reruns and never reruns the same head twice.
func rerunMarker(queue, sha string) string {
	return filepath.Join(queue, "RERUN-"+sha12(sha))
}

func hasRerun(queue, sha string) bool {
	_, err := os.Stat(rerunMarker(queue, sha))
	return err == nil
}

func writeRerunMarker(queue, sha, job string) error {
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("GATE RERUN sha=%s job=%s\n", sha12(sha), oneline.Field(job))
	return os.WriteFile(rerunMarker(queue, sha), []byte(body), 0o644)
}

// ghRunSource is the shipped source: `gh run list` for the verdict, `gh run view` for the
// failing job and its log. Every child is bounded by a timeout (SPEC-MERGE's reason).
type ghRunSource struct{ timeout time.Duration }

// NewGHRunSource is the gate's source when no --source file is given.
func NewGHRunSource(timeout time.Duration) RunSource { return ghRunSource{timeout: timeout} }

func (g ghRunSource) LatestRun(repo, branch string) ([]CIRun, error) {
	raw, err := g.sh("gh", "run", "list", "--repo", repo, "--branch", branch,
		"--workflow", gateWorkflow, "--event", gateEvent, "--limit", "1",
		"--json", "status,conclusion,headSha,databaseId,workflowName,event,createdAt,updatedAt")
	if err != nil {
		return nil, err
	}
	var runs []CIRun
	if err := json.Unmarshal([]byte(raw), &runs); err != nil {
		return nil, fmt.Errorf("gh run list did not answer json: %s", oneline.Err(err))
	}
	return runs, nil
}

// Jobs reads the run's jobs with their walls, so the ci-wall rule can name the
// long pole. It is the same jobs API FailedJob already fetches.
func (g ghRunSource) Jobs(repo string, runID int64) ([]CIJob, error) {
	raw, err := g.sh("gh", "run", "view", fmt.Sprintf("%d", runID), "--repo", repo, "--json", "jobs")
	if err != nil {
		return nil, err
	}
	var view struct {
		Jobs []CIJob `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return nil, fmt.Errorf("gh run view did not answer json: %s", oneline.Err(err))
	}
	return view.Jobs, nil
}

func (g ghRunSource) FailedJob(repo string, runID int64) (CIJob, error) {
	id := fmt.Sprintf("%d", runID)
	raw, err := g.sh("gh", "run", "view", id, "--repo", repo, "--json", "jobs")
	if err != nil {
		return CIJob{}, err
	}
	var view struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return CIJob{}, fmt.Errorf("gh run view did not answer json: %s", oneline.Err(err))
	}
	job := CIJob{}
	for _, j := range view.Jobs {
		if j.Conclusion == "failure" {
			job.Name = j.Name
			break
		}
	}
	log, err := g.sh("gh", "run", "view", id, "--repo", repo, "--log-failed")
	if err != nil {
		// The name is worth having without the log: the gate still writes a STOP.
		return job, nil
	}
	job.Log = capLog(log)
	return job, nil
}

// RerunFailed asks gh to rerun one run's failed jobs. It is the gate's one side effect on a
// flaky verdict, and it is bounded like every other child.
func (g ghRunSource) RerunFailed(repo string, runID int64) error {
	_, err := g.sh("gh", "run", "rerun", strconv.FormatInt(runID, 10), "--repo", repo, "--failed")
	return err
}

// logCap is how much of a failing log the gate reads: the first FAIL line and the issue
// beside it are near the top of it, and the rest is tokens nobody reads.
const logCap = 64 << 10

func capLog(s string) string {
	if len(s) > logCap {
		return s[:logCap]
	}
	return s
}

func (g ghRunSource) sh(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), oneline.Err(err))
	}
	return string(out), nil
}

// fileRunSource is --source: the runs and the failing job read out of one file, so the gate
// runs with no gh, no network and no fixture on PATH at all.
type fileRunSource struct {
	Runs []CIRun `json:"runs"`
	Job  CIJob   `json:"job"`
	List []CIJob `json:"jobs"`
}

// NewFileRunSource reads a source file: {"runs":[...],"job":{"name":...,"log":...}}, or a
// bare array of runs.
func NewFileRunSource(path string) (RunSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read --source %s: %s (it holds the runs gh would have answered)", path, oneline.Err(err))
	}
	var src fileRunSource
	if err := json.Unmarshal(raw, &src); err == nil && (len(src.Runs) > 0 || src.Job.Name != "" || len(src.List) > 0) {
		return &src, nil
	}
	var runs []CIRun
	if err := json.Unmarshal(raw, &runs); err != nil {
		return nil, fmt.Errorf("--source %s is not the run json: %s (it is {\"runs\":[...],\"job\":{...}} or a bare run array)", path, oneline.Err(err))
	}
	return &fileRunSource{Runs: runs}, nil
}

func (f *fileRunSource) LatestRun(repo, branch string) ([]CIRun, error) {
	return f.Runs, nil
}

func (f *fileRunSource) FailedJob(repo string, runID int64) (CIJob, error) { return f.Job, nil }

// Jobs is the file's jobs array, so a --source run names the same long pole
// the jobs API would have answered.
func (f *fileRunSource) Jobs(repo string, runID int64) ([]CIJob, error) {
	if len(f.List) > 0 {
		return f.List, nil
	}
	if f.Job.Name != "" {
		return []CIJob{f.Job}, nil
	}
	return nil, fmt.Errorf("no jobs in --source %s", repo)
}
