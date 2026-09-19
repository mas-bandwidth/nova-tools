package ci

// failed_forge.go is the seam between `nova-ci failed` and a forge. The verb knows
// nothing about gh: it asks a FailForge which run it means, which jobs that run has and
// what one job's log says, and everything that comes back is DATA -- a job name, a step
// name, log text -- never an instruction. The tests hand the verb a fake that answers
// from testdata, so no test here touches the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FailedStep is one step of a job: enough to say what a cancellation cut short.
type FailedStep struct {
	Name       string
	Conclusion string
	Started    time.Time
	Completed  time.Time
}

// FailedJob is one job of a run.
type FailedJob struct {
	ID         int64
	Name       string
	Conclusion string // success, failure, cancelled, timed_out, skipped, ...
	Steps      []FailedStep
}

// Failed is true for a job whose conclusion is worth reading the log of. A skipped or
// successful job has nothing to say.
func (j FailedJob) Failed() bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "", "success", "skipped", "neutral":
		return false
	}
	return true
}

// Cancelled is true for a job the run cut down rather than one that went red of its own.
// The forge spells it both ways, and the two are the same job.
func (j FailedJob) Cancelled() bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "cancelled", "canceled":
		return true
	}
	return false
}

// RunSelector is the one run the caller means, in the words they used: a run id, a pull
// request, that pull request's merge-queue run, or a branch.
type RunSelector struct {
	Run        int64
	PR         int
	Branch     string
	MergeGroup bool
}

// FailForge is what `nova-ci failed` needs from a forge, and all of it.
type FailForge interface {
	// ResolveRun turns the caller's words into one run id.
	ResolveRun(sel RunSelector) (int64, error)
	// Jobs lists the run's jobs, in the forge's own order.
	Jobs(runID int64) ([]FailedJob, error)
	// JobLog is one job's whole log, raw: timestamps, ANSI and all.
	JobLog(jobID int64) (string, error)
}

// ReadFailedRun is the verb's whole engine: resolve the run, read every job that did not
// succeed, and fold the logs into one report. jobFilter keeps only the jobs whose name
// contains it; empty keeps them all.
func ReadFailedRun(f FailForge, sel RunSelector, jobFilter string) (int64, FailedReport, error) {
	runID, err := f.ResolveRun(sel)
	if err != nil {
		return 0, FailedReport{}, err
	}
	jobs, err := f.Jobs(runID)
	if err != nil {
		return runID, FailedReport{}, err
	}
	var report FailedReport
	var failing []string
	for _, j := range jobs {
		if !j.Failed() {
			continue
		}
		failing = append(failing, j.Name)
		if jobFilter != "" && !strings.Contains(j.Name, jobFilter) {
			continue
		}
		report.Jobs++
		if j.Cancelled() {
			report.Cancelled++
		}
		said := report.findings()
		report.Cancels = append(report.Cancels, CancelledSteps(j)...)
		log, err := f.JobLog(j.ID)
		if err != nil {
			// One log the forge will not hand over is a LINE, not the end of the
			// report. The forge answers 404 (BlobNotFound) for a cancelled job's log
			// while its run is still in progress, and refusing the whole run over
			// that would hide every other job's red -- which is exactly what this
			// verb exists to surface. Say which job, and say why.
			report.Unread = append(report.Unread, Unread{Job: j.Name, Reason: oneline.Err(err)})
			continue
		}
		failures, timeouts := ParseJobLog(j.Name, log)
		report.Failures = append(report.Failures, failures...)
		report.Timeouts = append(report.Timeouts, timeouts...)
		// EVERY red job gets a line. A job whose log holds no test event -- a compiler
		// error under -Werror inside a make step, so `go test` never ran -- said nothing
		// through the parser, and counting it in jobs= while naming it nowhere is how a
		// red run reads as a green one. Say which job, which step, and what the runner
		// marked as the errors.
		if report.findings() == said {
			report.NoTests = append(report.NoTests, NoTest{
				Job:   j.Name,
				Step:  FailedStepName(j),
				Lines: LogErrorLines(log),
			})
		}
	}
	// A filter that matched nothing is NOT a green run, and must never read as one: the
	// caller asked about a job this run does not have, so say which jobs it does have.
	if jobFilter != "" && report.Jobs == 0 {
		if len(failing) == 0 {
			return runID, FailedReport{}, fmt.Errorf("--job %q matched nothing: no job of run %d failed", jobFilter, runID)
		}
		return runID, FailedReport{}, fmt.Errorf("--job %q matched none of the %d failing jobs of run %d: %s",
			jobFilter, len(failing), runID, nameList(failing))
	}
	return runID, report, nil
}

// nameList is the jobs a filter could have matched, capped at three with the rest
// counted, each quoted because it is what the caller pastes back into --job.
func nameList(names []string) string {
	const cap3 = 3
	shown := names
	if len(shown) > cap3 {
		shown = shown[:cap3]
	}
	quoted := make([]string, len(shown))
	for i, n := range shown {
		quoted[i] = oneline.Quote(n)
	}
	s := strings.Join(quoted, ", ")
	if dropped := len(names) - len(shown); dropped > 0 {
		s += fmt.Sprintf(", +%d", dropped)
	}
	return s
}

// GHFailForge is the one FailForge that shells to gh. Every invocation it makes is in
// this file, so a reader can check them all at once, and none of them writes: this verb
// reads a run and says what it found.
type GHFailForge struct {
	Repo    string // <owner>/<name>
	GH      string // the gh executable, "gh" unless the caller names another
	Timeout time.Duration
}

// NewGHFailForge returns a forge over one repository.
func NewGHFailForge(repo, ghPath string, timeout time.Duration) *GHFailForge {
	if strings.TrimSpace(ghPath) == "" {
		ghPath = "gh"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &GHFailForge{Repo: repo, GH: ghPath, Timeout: timeout}
}

// maxRunPages bounds the paging of every list below: a run has tens of jobs, not
// thousands, and an unbounded loop against a host is a hang waiting to happen.
const maxRunPages = 10

// ResolveRun turns the selector into a run id.
func (g *GHFailForge) ResolveRun(sel RunSelector) (int64, error) {
	switch {
	case sel.Run > 0:
		return sel.Run, nil
	case sel.PR > 0 && sel.MergeGroup:
		return g.mergeGroupRun(sel.PR)
	case sel.PR > 0:
		return g.prRun(sel.PR)
	case sel.Branch != "":
		id, err := g.latestRun("branch="+urlQueryEscape(sel.Branch), "")
		if err != nil {
			return 0, err
		}
		if id == 0 {
			return 0, fmt.Errorf("no run on branch %s of %s", sel.Branch, g.Repo)
		}
		return id, nil
	}
	return 0, fmt.Errorf("no run named: give one of --run, --pr or --branch")
}

// prRun is the latest run of the pull request's head branch, preferring a run of the
// head commit itself so a stale run on the same branch is never read as this one.
func (g *GHFailForge) prRun(pr int) (int64, error) {
	out, err := g.gh(false, "api", fmt.Sprintf("repos/%s/pulls/%d", g.Repo, pr),
		"--jq", ".head.ref + \"\\t\" + .head.sha")
	if err != nil {
		return 0, fmt.Errorf("could not read pull request %d of %s: %w", pr, g.Repo, err)
	}
	ref, sha, _ := strings.Cut(strings.TrimSpace(out), "\t")
	if ref == "" {
		return 0, fmt.Errorf("pull request %d of %s named no head branch", pr, g.Repo)
	}
	id, err := g.latestRun("branch="+urlQueryEscape(ref), sha)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("no run on %s, the head branch of pull request %d", ref, pr)
	}
	return id, nil
}

// mergeGroupRun is the latest merge_group run of the queue branch this pull request was
// queued on. GitHub names that branch gh-readonly-queue/<base>/pr-<n>-<sha>, so the pull
// request number is in the branch and no second call is needed to find it.
func (g *GHFailForge) mergeGroupRun(pr int) (int64, error) {
	runs, err := g.listRuns("event=merge_group")
	if err != nil {
		return 0, err
	}
	marker := fmt.Sprintf("/pr-%d-", pr)
	var best int64
	for _, r := range runs {
		if !strings.Contains(r.HeadBranch, marker) {
			continue
		}
		if r.ID > best {
			best = r.ID
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("no merge_group run for pull request %d of %s; the queue branch carries %q", pr, g.Repo, marker)
	}
	return best, nil
}

// ghRun is one row of the runs list, named as the forge names it.
type ghRun struct {
	ID         int64  `json:"id"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
}

// latestRun is the newest run matching the query, preferring one whose head is sha when
// sha is given, so a stale run of an older push is never read as this one.
func (g *GHFailForge) latestRun(query, sha string) (int64, error) {
	runs, err := g.listRuns(query)
	if err != nil {
		return 0, err
	}
	var best, bestAtSHA int64
	for _, r := range runs {
		if r.ID > best {
			best = r.ID
		}
		if sha != "" && r.HeadSHA == sha && r.ID > bestAtSHA {
			bestAtSHA = r.ID
		}
	}
	if bestAtSHA != 0 {
		return bestAtSHA, nil
	}
	return best, nil
}

// listRuns reads one page of the run list. One page of a hundred runs is more history
// than "the latest run" needs, and a second page would only be older.
func (g *GHFailForge) listRuns(query string) ([]ghRun, error) {
	out, err := g.gh(false, "api", fmt.Sprintf("repos/%s/actions/runs?per_page=100&%s", g.Repo, query))
	if err != nil {
		return nil, fmt.Errorf("could not list the runs of %s: %w", g.Repo, err)
	}
	var body struct {
		Runs []ghRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("gh did not answer JSON this tool can read for the run list: %w", err)
	}
	return body.Runs, nil
}

// Jobs lists the run's jobs, paging until the forge's own total_count is met.
func (g *GHFailForge) Jobs(runID int64) ([]FailedJob, error) {
	var jobs []FailedJob
	for page := 1; page <= maxRunPages; page++ {
		out, err := g.gh(false, "api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100&page=%d", g.Repo, runID, page))
		if err != nil {
			return nil, fmt.Errorf("could not list the jobs of run %d: %w", runID, err)
		}
		var body struct {
			Total int `json:"total_count"`
			Jobs  []struct {
				ID         int64  `json:"id"`
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
				Steps      []struct {
					Name        string `json:"name"`
					Conclusion  string `json:"conclusion"`
					StartedAt   string `json:"started_at"`
					CompletedAt string `json:"completed_at"`
				} `json:"steps"`
			} `json:"jobs"`
		}
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			return nil, fmt.Errorf("gh did not answer JSON this tool can read for the job list: %w", err)
		}
		if len(body.Jobs) == 0 {
			break
		}
		for _, j := range body.Jobs {
			job := FailedJob{ID: j.ID, Name: j.Name, Conclusion: j.Conclusion}
			for _, s := range j.Steps {
				job.Steps = append(job.Steps, FailedStep{
					Name:       s.Name,
					Conclusion: s.Conclusion,
					Started:    parseForgeTime(s.StartedAt),
					Completed:  parseForgeTime(s.CompletedAt),
				})
			}
			jobs = append(jobs, job)
		}
		if len(jobs) >= body.Total {
			break
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

// JobLog fetches one job's whole log. gh refuses to hand over a log holding terminal
// escape sequences unless it is told to, and a CI log always holds them, so the flag is
// the normal call; a gh too old to know it is asked again without.
func (g *GHFailForge) JobLog(jobID int64) (string, error) {
	path := fmt.Sprintf("repos/%s/actions/jobs/%d/logs", g.Repo, jobID)
	out, err := g.gh(true, "api", "--allow-escape-sequences", path)
	if err == nil {
		return out, nil
	}
	if !strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "unknown shorthand") {
		return "", fmt.Errorf("could not read the log of job %d: %w", jobID, err)
	}
	out, err = g.gh(true, "api", path)
	if err != nil {
		return "", fmt.Errorf("could not read the log of job %d: %w", jobID, err)
	}
	return out, nil
}

// parseForgeTime reads one RFC 3339 stamp, and returns the zero time for anything else:
// a missing stamp means the duration is unknown, never that it was zero.
func parseForgeTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// urlQueryEscape escapes a branch name for a query string without pulling net/url in for
// one call: a branch may hold a slash, which the forge wants encoded.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// gh runs one gh invocation under this forge's timeout and returns its stdout. big says
// the answer is a log rather than a line of JSON, which only changes the error this
// wraps: a truncated log and a truncated field read very differently.
func (g *GHFailForge) gh(big bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.GH, args...)
	out, err := cmd.Output()
	if err != nil {
		what := "gh " + strings.Join(args, " ")
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %w: %s", what, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if big && len(out) == 0 {
		return "", fmt.Errorf("gh %s answered nothing", strings.Join(args, " "))
	}
	return string(out), nil
}
