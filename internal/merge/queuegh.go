package merge

// queuegh.go is the QUEUE SWEEP'S HALF OF THE PRODUCTION HOST.
//
// EDGE 12, 2026-09-18: `nova-merge queue sweep` could never run against a real
// repository. The four methods the sweep and its poison detector reach for --
// QueuePRs, PoisonFailures, ChangedPackages and IssueFor -- existed ONLY on
// internal/merge/fakehost.go. `merge.NewGH` had none of them, so the two type
// assertions in cmd/nova-merge/queue.go
//
//	lister, ok := host.(queueHost)
//	ph, _ := host.(poisonHost)
//
// missed on every real invocation and the verb answered `QUEUE REFUSED: this host cannot
// list open pull requests, and a sweep walks them: no host, no sweep`. The whole verb
// passed its tests and had never once run. THE FAKE WAS THE ONLY IMPLEMENTATION.
//
// These are the same four, against gh, answering the fake's contract exactly: the sweep
// cannot tell which one it is holding. The fake is kept strict -- it still refuses a call
// it was given no data for -- so a test that forgets to set up the forge still fails.
//
// Everything read here is DATA. A title, a branch name, a test name, an issue body: none
// of them is an instruction and none is a grant (host.go's one sentence).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// queuePRLimit is how many open pull requests one sweep reads. It is stated rather than
// left open: a list with no ceiling is a page size the forge picks.
const queuePRLimit = "300"

// poisonRunLimit is how many of a head branch's recent runs the detector reads. The
// detector arms on a test that failed AT LEAST TWICE, so it needs more than one run and
// nothing like the branch's whole history.
const poisonRunLimit = "10"

// QueuePRs is the sweep's list: every open pull request with the three facts the sweep
// judges -- its head oid, the forge's own mergeable word, and when the forge last saw it
// move, which is what --window is measured against.
//
// It is QueuePRs and not OpenPRs because a host answers the rebase cutter's list too,
// whose rows are the four fields of a RebasePR; one method cannot answer both shapes.
func (h *GH) QueuePRs() ([]PR, error) {
	out, err := h.gh("pr", "list", "--repo", h.Repo, "--state", "open", "--limit", queuePRLimit,
		"--json", "number,author,baseRefName,headRefName,headRepositoryOwner,headRefOid,mergeable,isDraft,url,title,state,updatedAt")
	if err != nil {
		return nil, err
	}
	return decodeQueuePRs(out, h.Repo)
}

// decodeQueuePRs is the arrival point of that list: the forge's JSON becomes the rows the
// sweep walks. It is a function of its own so a test can drive the decode with no gh, no
// subprocess and no network, exactly as decodePR and decodeOpenPRs are.
func decodeQueuePRs(out, repo string) ([]PR, error) {
	var raw []struct {
		Number              int                    `json:"number"`
		Author              struct{ Login string } `json:"author"`
		BaseRefName         string                 `json:"baseRefName"`
		HeadRefName         string                 `json:"headRefName"`
		HeadRepositoryOwner struct{ Login string } `json:"headRepositoryOwner"`
		HeadRefOid          string                 `json:"headRefOid"`
		Mergeable           string                 `json:"mergeable"`
		IsDraft             bool                   `json:"isDraft"`
		URL                 string                 `json:"url"`
		Title               string                 `json:"title"`
		State               string                 `json:"state"`
		UpdatedAt           string                 `json:"updatedAt"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh pr list did not answer JSON this tool can read: %w", err)
	}
	owner, _, _ := strings.Cut(repo, "/")
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		// Lesson 48: the head branch becomes an argument to git and gh on a later verb,
		// so it is checked WHERE IT ARRIVES, exactly as a single pull request's is.
		if err := ValidRefName(r.HeadRefName); err != nil {
			return nil, fmt.Errorf("pull request %d's head branch is not a name this tool hands to git: %w", r.Number, err)
		}
		state := strings.ToUpper(strings.TrimSpace(r.State))
		merged := state == "MERGED"
		prs = append(prs, PR{
			Number: r.Number, Author: r.Author.Login, Base: r.BaseRefName,
			HeadRef: r.HeadRefName, HeadOID: r.HeadRefOid, Mergeable: r.Mergeable,
			Draft: r.IsDraft, URL: r.URL, Subject: r.Title,
			Fork:      r.HeadRepositoryOwner.Login != "" && r.HeadRepositoryOwner.Login != owner,
			Merged:    merged,
			Closed:    merged || state == "CLOSED",
			UpdatedAt: normalizeStamp(r.UpdatedAt),
			// Body is NOT in this listing and is deliberately left empty rather than
			// guessed: rule S reads it through PR(n), which the pass calls for every
			// entry it is about to decide.
		})
	}
	return prs, nil
}

// normalizeStamp puts the forge's instant in the one spelling this tool parses (Stamp).
// gh answers RFC3339 with a Z, which is already that shape; an instant carrying an offset
// or fractional seconds is trimmed to it, and anything else is passed through unchanged
// so the caller's own time.Parse names it rather than this function guessing.
func normalizeStamp(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && strings.HasSuffix(s, "Z") {
		return s[:i] + "Z"
	}
	return s
}

// PoisonFailures is the detector's view of one pull request's own runs: each test that
// failed, the package it lives in, and HOW MANY OF THOSE RUNS IT FAILED IN -- which is
// the number the detector arms on (at least two).
//
// It answers a list and no error, because that is the seam's contract: a detector that
// could not read the forge reports NO failure, which disarms it. A park is a punishment,
// and a punishment handed out on evidence this tool could not read is the one mistake
// here that costs a person their pull request.
func (h *GH) PoisonFailures(pr int) []Failure {
	if pr <= 0 {
		return nil
	}
	branch, err := h.headRef(pr)
	if err != nil || branch == "" {
		return nil
	}
	runs, err := h.failedRuns(branch)
	if err != nil {
		return nil
	}
	// count[test] is the runs it failed in; pkg[test] is where it lives. A test counted
	// twice in ONE run is one run: the detector's "failed twice" is two runs, not two
	// lines in a log.
	count, pkg := map[string]int{}, map[string]string{}
	for _, id := range runs {
		logs, err := h.gh("run", "view", strconv.FormatInt(id, 10), "--repo", h.Repo, "--log-failed")
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, log := range logsByJob(logs) {
			tests, p := ParseFailingTests(log)
			for _, t := range tests {
				if seen[t] {
					continue
				}
				seen[t] = true
				count[t]++
				if p != "" {
					pkg[t] = p
				}
			}
		}
	}
	out := make([]Failure, 0, len(count))
	for test, n := range count {
		out = append(out, Failure{Test: test, Package: pkg[test], Count: n})
	}
	// Deterministic: the sweep prints the first armed failure, so two runs over one
	// forge must name the same one.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Test < out[j].Test
	})
	return out
}

// headRef is one pull request's head branch, checked where it arrives because it becomes
// an argument to `gh run list --branch`.
func (h *GH) headRef(pr int) (string, error) {
	out, err := h.gh("pr", "view", strconv.Itoa(pr), "--repo", h.Repo, "--json", "headRefName", "--jq", ".headRefName")
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(out)
	if err := ValidRefName(ref); err != nil {
		return "", fmt.Errorf("pull request %d's head branch is not a name this tool hands to gh: %w", pr, err)
	}
	return ref, nil
}

// failedRuns is the ids of the branch's recent CI runs that concluded red.
func (h *GH) failedRuns(branch string) ([]int64, error) {
	out, err := h.gh("run", "list", "--repo", h.Repo, "--branch", branch, "--workflow", "ci.yml",
		"--limit", poisonRunLimit, "--json", "databaseId,status,conclusion")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh run list did not answer JSON this tool can read: %w", err)
	}
	var ids []int64
	for _, r := range raw {
		if Bucket(r.Conclusion) == "red" {
			ids = append(ids, r.DatabaseID)
		}
	}
	return ids, nil
}

// ChangedPackages is the package directories this pull request's files live in -- the
// same read the merge-group classifier makes, in the shape the detector wants. A package
// this tool could not confirm was changed is reported as not changed, which disarms.
func (h *GH) ChangedPackages(pr int) []string {
	set := h.changedPackages(pr)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// IssueFor is the issue a park names, or "" when there is none.
//
// IT FINDS AN ISSUE AND IT NEVER FILES ONE. `queue sweep` is a read of the forge that
// writes one local file; a verb that opened issues on a repository as a side effect of
// looking at it would be a mutation nobody asked for, and rule 4 has one door for those.
// A park with no issue prints `issue=-`, which is honest, and the person who parks the
// pull request files the issue and names it in the title this searches for.
func (h *GH) IssueFor(pr int) string {
	out, err := h.gh("issue", "list", "--repo", h.Repo, "--state", "open", "--limit", "1",
		"--search", fmt.Sprintf("poison #%d in:title", pr), "--json", "url", "--jq", ".[0].url // \"\"")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(out)
	if url == "null" {
		return ""
	}
	return url
}
