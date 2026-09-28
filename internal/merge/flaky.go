package merge

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file is the merge-group failure reader: the failed jobs and the '--- FAIL' test
// names of one merge-group run, which the typed decision of `nova-merge classify` judges.
// Everything read here is DATA, exactly as everything else the host returns.

// RunJob is one failed job of a merge-group run: the runner that took it, the Go package
// the failing tests live in, the test names '--- FAIL' named, and whether the pull request
// changed that package.
type RunJob struct {
	Name    string
	Runner  string
	Package string
	Tests   []string
	Changed bool
}

// MergeRun is one merge-group run's failure: the evidence the classify decision reads.
type MergeRun struct {
	ID    int64
	Event string
	PR    int
	Jobs  []RunJob
}

// failingTest matches a Go test's name on a '--- FAIL' line.
var failingTest = regexp.MustCompile(`(?m)^\s*--- FAIL: ([^\s]+)`)

// failingPkg matches the package line a Go test run prints after its failures.
var failingPkg = regexp.MustCompile(`(?m)^FAIL\s+(\S+)`)

// ParseFailingTests reads the test names and the package out of one failed job log.
// A log with no '--- FAIL' line names no test; a log with no FAIL package line names no
// package. Both empty answers are kept, never guessed.
func ParseFailingTests(log string) (tests []string, pkg string) {
	for _, m := range failingTest.FindAllStringSubmatch(log, -1) {
		tests = append(tests, strings.TrimSuffix(m[1], "("))
	}
	if m := failingPkg.FindStringSubmatch(log); m != nil {
		pkg = m[1]
	}
	return tests, pkg
}

// MergeGroupRun reads one run's event, its pull request, its failed jobs and their logs.
// It is the GH client's reading of the forge; a test drives a FakeHost instead and never
// reaches the network.
func (h *GH) MergeGroupRun(id int) (MergeRun, error) {
	run := MergeRun{ID: int64(id)}
	meta, err := h.gh("api", fmt.Sprintf("repos/%s/actions/runs/%d", h.Repo, id))
	if err != nil {
		return MergeRun{}, err
	}
	var rmeta struct {
		Event        string `json:"event"`
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	}
	if err := json.Unmarshal([]byte(meta), &rmeta); err != nil {
		return MergeRun{}, fmt.Errorf("gh api run %d did not answer JSON this tool can read: %w", id, err)
	}
	run.Event = rmeta.Event
	if len(rmeta.PullRequests) > 0 {
		run.PR = rmeta.PullRequests[0].Number
	}

	jobsRaw, err := h.gh("api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", h.Repo, id))
	if err != nil {
		return MergeRun{}, err
	}
	var jraw struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			RunnerName string `json:"runner_name"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(jobsRaw), &jraw); err != nil {
		return MergeRun{}, fmt.Errorf("gh api run %d jobs did not answer JSON this tool can read: %w", id, err)
	}
	changed := h.changedPackages(run.PR)
	byName := map[string]RunJob{}
	for _, j := range jraw.Jobs {
		if Bucket(j.Conclusion) != "red" {
			continue
		}
		byName[j.Name] = RunJob{Name: j.Name, Runner: j.RunnerName}
	}
	if len(byName) == 0 {
		return run, nil
	}
	// One `gh run view --log-failed` carries every failed job's log, each line prefixed
	// with the job's name. A log that cannot be read leaves the job named with no test:
	// the classification then sees a failed job with no test name, never a guess.
	if logs, err := h.gh("run", "view", strconv.Itoa(id), "--repo", h.Repo, "--log-failed"); err == nil {
		for jobName, log := range logsByJob(logs) {
			j, ok := byName[jobName]
			if !ok {
				continue
			}
			j.Tests, j.Package = ParseFailingTests(log)
			j.Changed = changed[j.Package]
			byName[jobName] = j
		}
	}
	for _, j := range byName {
		run.Jobs = append(run.Jobs, j)
	}
	return run, nil
}

// logsByJob splits `gh run view --log-failed` output, whose every line begins with the
// job's name and a tab, into one log per job.
func logsByJob(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		name, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		out[name] += rest + "\n"
	}
	return out
}

// changedPackages is the set of package directories the pull request's files live in. A
// run with no pull request, or files that cannot be read, answers nothing: a package the
// decision could not confirm was changed is reported as not changed.
func (h *GH) changedPackages(pr int) map[string]bool {
	if pr <= 0 {
		return nil
	}
	out, err := h.gh("api", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", h.Repo, pr), "--jq", ".[].filename")
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.LastIndex(line, "/"); i >= 0 {
			set[line[:i]] = true
		} else {
			set["."] = true
		}
	}
	return set
}
