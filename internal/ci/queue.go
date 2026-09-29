package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultFailLines is how many FAIL lines are extracted from a failed job's log.
const DefaultFailLines = 3

// QueueEntry is one entry of the merge queue: its PR number, position, state,
// and the details of its latest merge_group run if any.
type QueueEntry struct {
	PR         int      `json:"pr"`
	Position   int      `json:"position"`
	State      string   `json:"state"` // e.g. QUEUED, AWAITING_CHECKS, MERGEABLE, LOCKED
	HeadSHA    string   `json:"head_sha,omitempty"`
	EnqueuedAt string   `json:"enqueued_at,omitempty"`
	RunID      int64    `json:"run_id,omitempty"`
	Conclusion string   `json:"conclusion,omitempty"`
	Job        string   `json:"job,omitempty"`
	FailLines  []string `json:"fail_lines,omitempty"`
}

// QueueReport holds the merge queue status for one repository and branch.
type QueueReport struct {
	Repo    string       `json:"repo"`
	Branch  string       `json:"branch"`
	Entries []QueueEntry `json:"entries"`
}

// Lines formats the report as receipt lines, one receipt line per entry.
// When the queue is empty, it prints one QUEUE OK line.
func (r QueueReport) Lines() []string {
	if len(r.Entries) == 0 {
		return []string{fmt.Sprintf("QUEUE OK repo=%s branch=%s entries=0", oneline.Field(r.Repo), oneline.Field(r.Branch))}
	}
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		runStr := "-"
		if e.RunID > 0 {
			runStr = strconv.FormatInt(e.RunID, 10)
		}
		conclusionStr := "-"
		if e.Conclusion != "" {
			conclusionStr = e.Conclusion
		}
		jobStr := "-"
		if e.Job != "" {
			jobStr = oneline.Field(e.Job)
		}
		failStr := "-"
		if len(e.FailLines) > 0 {
			failStr = oneline.Field(strings.Join(e.FailLines, " ; "))
		}
		line := fmt.Sprintf("QUEUE repo=%s branch=%s pr=%d pos=%d state=%s run=%s conclusion=%s job=%s fail=%s",
			oneline.Field(r.Repo), oneline.Field(r.Branch), e.PR, e.Position,
			oneline.Field(e.State), runStr, oneline.Field(conclusionStr),
			jobStr, failStr)
		out = append(out, line)
	}
	return out
}

// Table formats the report as an aligned text table.
func (r QueueReport) Table() string {
	if len(r.Entries) == 0 {
		return fmt.Sprintf("QUEUE OK repo=%s branch=%s entries=0\n", r.Repo, r.Branch)
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PR\tPOS\tSTATE\tRUN\tCONCLUSION\tJOB\tFAIL")
	for _, e := range r.Entries {
		runStr := "-"
		if e.RunID > 0 {
			runStr = strconv.FormatInt(e.RunID, 10)
		}
		conclusionStr := "-"
		if e.Conclusion != "" {
			conclusionStr = e.Conclusion
		}
		jobStr := "-"
		if e.Job != "" {
			jobStr = e.Job
		}
		failStr := "-"
		if len(e.FailLines) > 0 {
			failStr = strings.Join(e.FailLines, " ; ")
		}
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			e.PR, e.Position, e.State, runStr, conclusionStr, jobStr, failStr)
	}
	tw.Flush()
	return b.String()
}

// JSON formats the report as indented JSON. When the queue is empty, it returns an empty list [].
func (r QueueReport) JSON() ([]byte, error) {
	if len(r.Entries) == 0 {
		return []byte("[]"), nil
	}
	return json.MarshalIndent(r, "", "  ")
}

// UnmarshalJSON unmarshals either a full QueueReport object or an empty list [].
func (r *QueueReport) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "[]" {
		r.Entries = nil
		return nil
	}
	type alias QueueReport
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = QueueReport(a)
	return nil
}

// ExtractFailLines extracts the first failing test lines or error lines from
// a job log. If maxLines <= 0, DefaultFailLines is used.
func ExtractFailLines(jobName, logText string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = DefaultFailLines
	}

	// Clean lines (handling JSON frames if present, stripping ANSI and stamps).
	rawLines := strings.Split(logText, "\n")
	cleaned := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		clean := StripLogLine(raw)
		if ev, ok := decodeEvent(clean); ok {
			if ev.Action == "output" {
				out := strings.TrimSpace(StripLogLine(ev.Output))
				if out != "" {
					cleaned = append(cleaned, out)
				}
			}
		} else {
			trimmed := strings.TrimSpace(clean)
			if trimmed != "" {
				cleaned = append(cleaned, trimmed)
			}
		}
	}

	// 1. Look for go test failure header: read through subtest headers to the first _test.go: line and lines after it.
	for i, line := range cleaned {
		if strings.HasPrefix(line, "--- FAIL:") {
			testName := ""
			fields := strings.Fields(strings.TrimPrefix(line, "--- FAIL:"))
			if len(fields) > 0 {
				testName = fields[0]
			}
			lastFailHeader := line
			lastFailIdx := i
			foundTestGo := false
			var testGoLines []string
			for j := i + 1; j < len(cleaned); j++ {
				nxt := cleaned[j]
				if strings.HasPrefix(nxt, "--- FAIL:") {
					subFields := strings.Fields(strings.TrimPrefix(nxt, "--- FAIL:"))
					if len(subFields) > 0 && testName != "" && strings.HasPrefix(subFields[0], testName+"/") {
						lastFailHeader = nxt
						lastFailIdx = j
						continue
					}
					break
				}
				if strings.HasPrefix(nxt, "=== ") || nxt == "FAIL" {
					break
				}
				if strings.Contains(nxt, "_test.go:") {
					foundTestGo = true
					testGoLines = append(testGoLines, nxt)
					for k := j + 1; k < len(cleaned) && len(testGoLines)+1 < maxLines; k++ {
						after := cleaned[k]
						if strings.HasPrefix(after, "--- ") || strings.HasPrefix(after, "=== ") || after == "FAIL" {
							break
						}
						testGoLines = append(testGoLines, after)
					}
					break
				}
			}
			if foundTestGo {
				lines := append([]string{lastFailHeader}, testGoLines...)
				if len(lines) > maxLines {
					lines = lines[:maxLines]
				}
				return lines
			}
			// Fall back to collecting lines under lastFailHeader if no _test.go: was found
			lines := []string{lastFailHeader}
			for j := lastFailIdx + 1; j < len(cleaned) && len(lines) < maxLines; j++ {
				nxt := cleaned[j]
				if strings.HasPrefix(nxt, "--- ") || strings.HasPrefix(nxt, "=== ") || nxt == "FAIL" {
					break
				}
				lines = append(lines, nxt)
			}
			return lines
		}
	}

	// 2. Look for panic / timeout line
	for _, line := range cleaned {
		if strings.HasPrefix(line, "panic:") {
			return []string{line}
		}
	}

	// 3. Look for runner errors / compiler errors
	errLines := LogErrorLines(logText)
	if len(errLines) > 0 {
		var lines []string
		for _, l := range errLines {
			if len(lines) >= maxLines {
				break
			}
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				lines = append(lines, trimmed)
			}
		}
		return lines
	}

	// 4. Fall back to any line containing FAIL, Error or error:
	var lines []string
	for _, line := range cleaned {
		if strings.Contains(line, "FAIL") || strings.Contains(line, "Error") || strings.Contains(line, "error:") {
			lines = append(lines, line)
			if len(lines) >= maxLines {
				break
			}
		}
	}
	return lines
}

// QueueNode is one raw entry as returned by the forge's mergeQueue query.
type QueueNode struct {
	PR         int
	Position   int
	State      string
	EnqueuedAt string
	HeadSHA    string
}

// MergeGroupRun is one merge_group workflow run as returned by the forge.
type MergeGroupRun struct {
	ID         int64
	HeadBranch string
	HeadSHA    string
	Status     string
	Conclusion string
}

// QueueForge is what `nova-ci queue` needs from the forge.
type QueueForge interface {
	QueueEntries(ctx context.Context, repo, branch string) ([]QueueNode, error)
	MergeGroupRuns(ctx context.Context, repo string) ([]MergeGroupRun, error)
	Jobs(ctx context.Context, repo string, runID int64) ([]FailedJob, error)
	JobLog(ctx context.Context, repo string, jobID int64) (string, error)
}

// QueueFilter selects a specific PR or run ID to inspect.
type QueueFilter struct {
	PR    int
	RunID int64
}

func isFailedConclusion(c string) bool {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "failure", "timed_out", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func extractPRFromBranch(branch string) int {
	const marker = "/pr-"
	idx := strings.Index(branch, marker)
	if idx == -1 {
		return 0
	}
	rest := branch[idx+len(marker):]
	dash := strings.IndexByte(rest, '-')
	if dash != -1 {
		rest = rest[:dash]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func populateFailedJob(ctx context.Context, f QueueForge, repo string, runID int64, failLinesCap int, entry *QueueEntry) error {
	jobs, err := f.Jobs(ctx, repo, runID)
	if err != nil {
		return fmt.Errorf("list jobs for run %d of %s: %w; failed job details were not read: check forge access and retry", runID, repo, err)
	}
	for _, j := range jobs {
		if j.Failed() {
			entry.Job = j.Name
			logText, err := f.JobLog(ctx, repo, j.ID)
			if err != nil {
				return fmt.Errorf("read log for job %d (%s) of %s: %w; job failure log was not read: check forge access and retry", j.ID, j.Name, repo, err)
			}
			entry.FailLines = ExtractFailLines(j.Name, logText, failLinesCap)
			break
		}
	}
	return nil
}

// InspectQueue reads the merge queue for repo and branch and resolves failed
// merge_group runs for queued PRs, or inspects a specific PR or run if filtered.
func InspectQueue(ctx context.Context, f QueueForge, repo, branch string, failLinesCap int, filters ...QueueFilter) (QueueReport, error) {
	var filter QueueFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	report := QueueReport{
		Repo:   repo,
		Branch: branch,
	}

	if filter.RunID > 0 {
		runs, err := f.MergeGroupRuns(ctx, repo)
		if err != nil {
			return QueueReport{}, fmt.Errorf("list merge_group runs of %s: %w; merge-group runs were not read: check forge access and retry", repo, err)
		}
		var targetRun *MergeGroupRun
		for i := range runs {
			if runs[i].ID == filter.RunID {
				targetRun = &runs[i]
				break
			}
		}
		if targetRun == nil {
			return QueueReport{}, fmt.Errorf("merge_group run %d not found in %s", filter.RunID, repo)
		}

		prNum := extractPRFromBranch(targetRun.HeadBranch)
		nodes, err := f.QueueEntries(ctx, repo, branch)
		if err != nil {
			return QueueReport{}, fmt.Errorf("query merge queue for branch %s of %s: %w; merge queue entries were not read: check forge access or branch name and retry", branch, repo, err)
		}

		var queueNode *QueueNode
		if prNum > 0 {
			for i := range nodes {
				if nodes[i].PR == prNum {
					queueNode = &nodes[i]
					break
				}
			}
		}

		entry := QueueEntry{
			PR:         prNum,
			Position:   -1,
			State:      "DEQUEUED",
			HeadSHA:    targetRun.HeadSHA,
			RunID:      targetRun.ID,
			Conclusion: strings.ToLower(strings.TrimSpace(targetRun.Conclusion)),
		}
		if entry.Conclusion == "" {
			entry.Conclusion = strings.ToLower(strings.TrimSpace(targetRun.Status))
		}
		if queueNode != nil {
			entry.Position = queueNode.Position
			entry.State = queueNode.State
			entry.EnqueuedAt = queueNode.EnqueuedAt
			if entry.HeadSHA == "" {
				entry.HeadSHA = queueNode.HeadSHA
			}
		}

		if isFailedConclusion(entry.Conclusion) {
			if err := populateFailedJob(ctx, f, repo, targetRun.ID, failLinesCap, &entry); err != nil {
				return QueueReport{}, err
			}
		}

		report.Entries = []QueueEntry{entry}
		return report, nil
	}

	if filter.PR > 0 {
		nodes, err := f.QueueEntries(ctx, repo, branch)
		if err != nil {
			return QueueReport{}, fmt.Errorf("query merge queue for branch %s of %s: %w; merge queue entries were not read: check forge access or branch name and retry", branch, repo, err)
		}
		var queueNode *QueueNode
		for i := range nodes {
			if nodes[i].PR == filter.PR {
				queueNode = &nodes[i]
				break
			}
		}

		runs, err := f.MergeGroupRuns(ctx, repo)
		if err != nil {
			return QueueReport{}, fmt.Errorf("list merge_group runs of %s: %w; merge-group runs were not read: check forge access and retry", repo, err)
		}

		marker := fmt.Sprintf("/pr-%d-", filter.PR)
		var latestFailedRun *MergeGroupRun
		var latestRun *MergeGroupRun
		for i := range runs {
			r := &runs[i]
			if strings.Contains(r.HeadBranch, marker) {
				if latestRun == nil || r.ID > latestRun.ID {
					latestRun = r
				}
				if isFailedConclusion(r.Conclusion) {
					if latestFailedRun == nil || r.ID > latestFailedRun.ID {
						latestFailedRun = r
					}
				}
			}
		}

		var bestRun *MergeGroupRun
		if latestFailedRun != nil {
			bestRun = latestFailedRun
		} else {
			bestRun = latestRun
		}

		if queueNode == nil && bestRun == nil {
			return QueueReport{}, fmt.Errorf("PR %d not found in merge queue or merge_group runs of %s", filter.PR, repo)
		}

		entry := QueueEntry{
			PR:       filter.PR,
			Position: -1,
			State:    "DEQUEUED",
		}
		if queueNode != nil {
			entry.Position = queueNode.Position
			entry.State = queueNode.State
			entry.HeadSHA = queueNode.HeadSHA
			entry.EnqueuedAt = queueNode.EnqueuedAt
		}
		if bestRun != nil {
			entry.RunID = bestRun.ID
			if entry.HeadSHA == "" {
				entry.HeadSHA = bestRun.HeadSHA
			}
			entry.Conclusion = strings.ToLower(strings.TrimSpace(bestRun.Conclusion))
			if entry.Conclusion == "" {
				entry.Conclusion = strings.ToLower(strings.TrimSpace(bestRun.Status))
			}

			if isFailedConclusion(entry.Conclusion) {
				if err := populateFailedJob(ctx, f, repo, bestRun.ID, failLinesCap, &entry); err != nil {
					return QueueReport{}, err
				}
			}
		}

		report.Entries = []QueueEntry{entry}
		return report, nil
	}

	nodes, err := f.QueueEntries(ctx, repo, branch)
	if err != nil {
		return QueueReport{}, fmt.Errorf("query merge queue for branch %s of %s: %w; merge queue entries were not read: check forge access or branch name and retry", branch, repo, err)
	}
	if len(nodes) == 0 {
		return report, nil
	}

	// Fetch merge_group runs once to match against all queued PRs.
	runs, err := f.MergeGroupRuns(ctx, repo)
	if err != nil {
		return QueueReport{}, fmt.Errorf("list merge_group runs of %s: %w; merge-group runs were not read: check forge access and retry", repo, err)
	}

	for _, n := range nodes {
		entry := QueueEntry{
			PR:         n.PR,
			Position:   n.Position,
			State:      n.State,
			HeadSHA:    n.HeadSHA,
			EnqueuedAt: n.EnqueuedAt,
		}

		// Find the newest merge_group run for this PR.
		marker := fmt.Sprintf("/pr-%d-", n.PR)
		var bestRun *MergeGroupRun
		for i := range runs {
			r := &runs[i]
			if strings.Contains(r.HeadBranch, marker) {
				if bestRun == nil || r.ID > bestRun.ID {
					bestRun = r
				}
			}
		}

		if bestRun != nil {
			entry.RunID = bestRun.ID
			entry.Conclusion = strings.ToLower(strings.TrimSpace(bestRun.Conclusion))
			if entry.Conclusion == "" {
				entry.Conclusion = strings.ToLower(strings.TrimSpace(bestRun.Status))
			}

			// If the run failed, find the failed job and extract FAIL lines.
			if isFailedConclusion(entry.Conclusion) {
				if err := populateFailedJob(ctx, f, repo, bestRun.ID, failLinesCap, &entry); err != nil {
					return QueueReport{}, err
				}
			}
		}

		report.Entries = append(report.Entries, entry)
	}

	return report, nil
}

// cleanForgeError normalizes forge errors to clean user-facing messages
// without leaking raw GraphQL query strings or CLI internals.
func cleanForgeError(err error, rawOut, repo string) error {
	msg := rawOut
	if msg == "" && err != nil {
		msg = err.Error()
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "could not resolve to a repository") || strings.Contains(low, "not found"):
		return fmt.Errorf("repository %s not found", repo)
	case strings.Contains(low, "auth login") || strings.Contains(low, "bad credentials") || strings.Contains(low, "not logged in") || strings.Contains(low, "authentication") || strings.Contains(low, "401"):
		return fmt.Errorf("not authenticated to forge: run gh auth login")
	}
	clean := strings.TrimSpace(msg)
	if idx := strings.Index(clean, "query($"); idx != -1 {
		if endIdx := strings.Index(clean[idx:], "}"); endIdx != -1 {
			clean = strings.TrimSpace(clean[:idx] + clean[idx+endIdx+1:])
		}
	}
	if clean == "" && err != nil {
		clean = err.Error()
	}
	return errors.New(clean)
}

// GHQueueForge is the production QueueForge that shells out to gh.
type GHQueueForge struct {
	Repo    string
	GH      string
	Timeout time.Duration
	Runner  func(ctx context.Context, args ...string) (string, error)
}

// NewGHQueueForge returns a production forge over one repository.
func NewGHQueueForge(repo string, timeout time.Duration, runner func(ctx context.Context, args ...string) (string, error)) *GHQueueForge {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &GHQueueForge{
		Repo:    repo,
		GH:      "gh",
		Timeout: timeout,
		Runner:  runner,
	}
}

func (g *GHQueueForge) run(ctx context.Context, args ...string) (string, error) {
	if g.Runner != nil {
		out, err := g.Runner(ctx, args...)
		if err != nil {
			return out, cleanForgeError(err, out, g.Repo)
		}
		return out, nil
	}
	ghBin := g.GH
	if ghBin == "" {
		ghBin = "gh"
	}
	cctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, ghBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), cleanForgeError(err, string(out), g.Repo)
	}
	return string(out), nil
}

const mergeQueueGraphQuery = `query($owner:String!,$name:String!,$branch:String!){repository(owner:$owner,name:$name){mergeQueue(branch:$branch){entries(first:100){nodes{position state enqueuedAt pullRequest{number headRefOid}}}}}}`

// QueueEntries queries the merge queue entries using GraphQL.
func (g *GHQueueForge) QueueEntries(ctx context.Context, repo, branch string) ([]QueueNode, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid repository %q", repo)
	}
	out, err := g.run(ctx, "api", "graphql",
		"-F", "owner="+owner,
		"-F", "name="+name,
		"-F", "branch="+branch,
		"-f", "query="+mergeQueueGraphQuery,
	)
	if err != nil {
		return nil, fmt.Errorf("could not query merge queue for %s on %s: %w", branch, repo, err)
	}

	var resp struct {
		Data struct {
			Repository *struct {
				MergeQueue *struct {
					Entries struct {
						Nodes []struct {
							Position    int    `json:"position"`
							State       string `json:"state"`
							EnqueuedAt  string `json:"enqueuedAt"`
							PullRequest struct {
								Number     int    `json:"number"`
								HeadRefOid string `json:"headRefOid"`
							} `json:"pullRequest"`
						} `json:"nodes"`
					} `json:"entries"`
				} `json:"mergeQueue"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("gh did not answer JSON this tool can read for merge queue: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, cleanForgeError(nil, resp.Errors[0].Message, repo)
	}
	if resp.Data.Repository == nil {
		return nil, fmt.Errorf("repository %s not found", repo)
	}
	if resp.Data.Repository.MergeQueue == nil {
		return nil, fmt.Errorf("no merge queue found for branch %s of %s", branch, repo)
	}

	var nodes []QueueNode
	for _, n := range resp.Data.Repository.MergeQueue.Entries.Nodes {
		nodes = append(nodes, QueueNode{
			PR:         n.PullRequest.Number,
			Position:   n.Position,
			State:      n.State,
			EnqueuedAt: n.EnqueuedAt,
			HeadSHA:    n.PullRequest.HeadRefOid,
		})
	}
	return nodes, nil
}

// MergeGroupRuns lists recent merge_group workflow runs.
func (g *GHQueueForge) MergeGroupRuns(ctx context.Context, repo string) ([]MergeGroupRun, error) {
	out, err := g.run(ctx, "api", fmt.Sprintf("repos/%s/actions/runs?event=merge_group&per_page=100", repo))
	if err != nil {
		return nil, fmt.Errorf("could not list merge_group runs of %s: %w", repo, err)
	}
	var body struct {
		Runs []struct {
			ID         int64  `json:"id"`
			HeadBranch string `json:"head_branch"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("gh did not answer JSON for runs list: %w", err)
	}
	var runs []MergeGroupRun
	for _, r := range body.Runs {
		runs = append(runs, MergeGroupRun{
			ID:         r.ID,
			HeadBranch: r.HeadBranch,
			HeadSHA:    r.HeadSHA,
			Status:     r.Status,
			Conclusion: r.Conclusion,
		})
	}
	return runs, nil
}

// Jobs lists the jobs for a given run ID.
func (g *GHQueueForge) Jobs(ctx context.Context, repo string, runID int64) ([]FailedJob, error) {
	out, err := g.run(ctx, "api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, runID))
	if err != nil {
		return nil, fmt.Errorf("could not list jobs of run %d: %w", runID, err)
	}
	var body struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Attempt    int    `json:"run_attempt"`
			HeadSHA    string `json:"head_sha"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("gh did not answer JSON for jobs list: %w", err)
	}
	var jobs []FailedJob
	for _, j := range body.Jobs {
		jobs = append(jobs, FailedJob{
			ID:         j.ID,
			Name:       j.Name,
			Conclusion: j.Conclusion,
			Attempt:    j.Attempt,
			HeadSHA:    j.HeadSHA,
		})
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

// JobLog fetches the log of a given job ID.
func (g *GHQueueForge) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	path := fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID)
	out, err := g.run(ctx, "api", "--allow-escape-sequences", path)
	if err == nil {
		return out, nil
	}
	if !strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "unknown shorthand") {
		return "", fmt.Errorf("could not read log of job %d: %w", jobID, err)
	}
	out, err = g.run(ctx, "api", path)
	if err != nil {
		return "", fmt.Errorf("could not read log of job %d: %w", jobID, err)
	}
	return out, nil
}
