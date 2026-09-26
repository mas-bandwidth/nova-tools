package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// LAND PR (nova-tools#4311): one pull request through GitHub's merge queue,
// watched to its merge commit. The scratch script of 2026-09-26 (wait for
// the PR's checks, enqueue it, watch the merge_group run, print the MERGED
// line) ran about twenty times that day; this is it as a verb, through the
// REST and GraphQL API with the token from the environment, never a child
// process. A verb until the queue leaves GitHub (#3597).
//
// The walk: read the PR (its node id, head, mergeable_state); poll its
// head's check runs until every one has completed (a failed one ends the
// walk naming it) and the forge calls the PR mergeable; enqueue it with the
// enqueuePullRequest mutation (the one admission the tools make, the door
// internal/merge names); then poll the PR and the repository's merge_group
// workflow runs, printing each QUEUE RUN status as it changes, until the PR
// is merged (MERGED <sha>) or a queue run fails (FAILED <job names>) or the
// timeout passes. Every wait goes through Sleep and every clock read through
// Now, so a test walks the whole thing with no real time.

// ErrNoToken is the typed refusal when no GitHub token is in the environment.
var ErrNoToken = &Refusal{Why: "no GitHub token", Remedy: "export GH_TOKEN (or GITHUB_TOKEN) for the seat that lands"}

// LandPROptions is one land pr run.
type LandPROptions struct {
	Repo    string // owner/name
	N       int    // the pull request
	Timeout time.Duration
	Tick    time.Duration
	// Now and Sleep are the clock; nil is the wall clock.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Log   io.Writer // the progress lines; nil discards them
}

// LandPRReport is how the walk ended: State merged, failed, timeout, closed
// or conflict; MergeSHA when merged; Failed the check or job names when a
// check or queue run failed.
type LandPRReport struct {
	State    string
	MergeSHA string
	Failed   []string
	// Enqueued says the PR was in the queue when the walk ended (or already).
	Enqueued bool
}

type prView struct {
	NodeID         string `json:"node_id"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type checkRuns struct {
	Total int `json:"total_count"`
	Runs  []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"check_runs"`
}

type queueRuns struct {
	Runs []struct {
		ID         int64  `json:"id"`
		HeadBranch string `json:"head_branch"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		CreatedAt  string `json:"created_at"`
	} `json:"workflow_runs"`
}

type runJobs struct {
	Jobs []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"jobs"`
}

// LandPR runs the walk. The error is a *Refusal for a refused input, else
// the API error that stopped it; a walk that ended in FAILED or timeout
// returns its report with a nil error.
func LandPR(ctx context.Context, gh *GitHub, o LandPROptions) (LandPRReport, error) {
	var rep LandPRReport
	if gh == nil || gh.Token == "" {
		return rep, ErrNoToken
	}
	if o.N < 1 || !strings.Contains(o.Repo, "/") {
		return rep, &Refusal{Why: "land pr wants <n> and --repo owner/name", Remedy: "nova-sprint land pr <n> --repo owner/name"}
	}
	if o.Tick <= 0 {
		o.Tick = 10 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	say := func(format string, a ...any) { fmt.Fprintf(o.Log, "PR %d "+format+"\n", append([]any{o.N}, a...)...) }
	deadline := o.Now().Add(o.Timeout)
	wait := func() (bool, error) {
		if !o.Now().Before(deadline) {
			return false, nil
		}
		return true, o.Sleep(ctx, o.Tick)
	}
	prPath := fmt.Sprintf("/repos/%s/pulls/%d", o.Repo, o.N)

	// Checks at the head, until they are all green or one is red.
	var pr prView
	lastChecks := ""
	for {
		pr = prView{}
		if _, err := gh.do(ctx, http.MethodGet, prPath, nil, &pr); err != nil {
			return rep, err
		}
		if pr.Merged {
			rep.State, rep.MergeSHA = "merged", pr.MergeCommitSHA
			say("MERGED %s", pr.MergeCommitSHA)
			return rep, nil
		}
		if pr.State == "closed" {
			rep.State = "closed"
			say("FAILED closed without a merge")
			return rep, nil
		}
		if pr.MergeableState == "dirty" {
			rep.State = "conflict"
			say("FAILED conflict with the base (mergeable_state=dirty)")
			return rep, nil
		}
		var cr checkRuns
		if _, err := gh.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", o.Repo, pr.Head.SHA), nil, &cr); err != nil {
			return rep, err
		}
		pass, pending := 0, 0
		var failed []string
		for _, r := range cr.Runs {
			switch {
			case r.Status != "completed":
				pending++
			case r.Conclusion == "success" || r.Conclusion == "skipped" || r.Conclusion == "neutral":
				pass++
			default:
				failed = append(failed, r.Name)
			}
		}
		if line := fmt.Sprintf("CHECKS %d/%d", pass, len(cr.Runs)); line != lastChecks {
			lastChecks = line
			say("%s", line)
		}
		if len(failed) > 0 {
			sort.Strings(failed)
			rep.State, rep.Failed = "failed", failed
			say("FAILED %s", strings.Join(failed, ","))
			return rep, nil
		}
		if pending == 0 && mergeableNow(pr.MergeableState) {
			break
		}
		more, err := wait()
		if err != nil {
			return rep, err
		}
		if !more {
			rep.State = "timeout"
			say("FAILED timeout after %s waiting for checks (mergeable_state=%s)", o.Timeout, pr.MergeableState)
			return rep, nil
		}
	}

	// The one admission: enqueuePullRequest by node id.
	already, err := gh.enqueue(ctx, pr.NodeID)
	if err != nil {
		return rep, err
	}
	rep.Enqueued = true
	if already {
		say("ENQUEUED already")
	} else {
		say("ENQUEUED")
	}

	// The queue: the PR merged, or its merge_group run failed.
	marker := fmt.Sprintf("/pr-%d-", o.N)
	seen := map[int64]string{}
	for {
		more, err := wait()
		if err != nil {
			return rep, err
		}
		if !more {
			rep.State = "timeout"
			say("FAILED timeout after %s in the queue", o.Timeout)
			return rep, nil
		}
		pr = prView{}
		if _, err := gh.do(ctx, http.MethodGet, prPath, nil, &pr); err != nil {
			return rep, err
		}
		if pr.Merged {
			rep.State, rep.MergeSHA = "merged", pr.MergeCommitSHA
			say("MERGED %s", pr.MergeCommitSHA)
			return rep, nil
		}
		if pr.State == "closed" {
			rep.State = "closed"
			say("FAILED closed without a merge")
			return rep, nil
		}
		var qr queueRuns
		if _, err := gh.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/runs?event=merge_group&per_page=30", o.Repo), nil, &qr); err != nil {
			return rep, err
		}
		// The PR's newest queue entry: the group branch with the latest run.
		group, groupAt := "", ""
		for _, r := range qr.Runs {
			if strings.Contains(r.HeadBranch, marker) && r.CreatedAt > groupAt {
				group, groupAt = r.HeadBranch, r.CreatedAt
			}
		}
		for _, r := range qr.Runs {
			if r.HeadBranch != group || group == "" {
				continue
			}
			word := r.Status
			if r.Status == "completed" {
				word = "completed " + r.Conclusion
			}
			if seen[r.ID] != word {
				seen[r.ID] = word
				say("QUEUE RUN %d %s", r.ID, word)
			}
			if r.Status == "completed" && r.Conclusion != "success" {
				var jobs runJobs
				if _, err := gh.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/runs/%d/jobs?per_page=100", o.Repo, r.ID), nil, &jobs); err != nil {
					return rep, err
				}
				var failed []string
				for _, j := range jobs.Jobs {
					if j.Conclusion != "success" && j.Conclusion != "skipped" && j.Conclusion != "neutral" {
						failed = append(failed, j.Name)
					}
				}
				sort.Strings(failed)
				if len(failed) == 0 {
					failed = []string{"run " + fmt.Sprint(r.ID) + " " + r.Conclusion}
				}
				rep.State, rep.Failed = "failed", failed
				say("FAILED %s", strings.Join(failed, ","))
				return rep, nil
			}
		}
	}
}

// mergeableNow is the forge's word for a PR the queue will take: clean, or
// unstable (a non-required check is red, which the check walk above already
// judged), or has_hooks.
func mergeableNow(state string) bool {
	switch state {
	case "clean", "unstable", "has_hooks":
		return true
	}
	return false
}

// enqueue runs the enqueuePullRequest mutation. already is true when the
// forge says the PR is in the queue already, which is the same outcome.
func (g *GitHub) enqueue(ctx context.Context, nodeID string) (already bool, err error) {
	if nodeID == "" {
		return false, fmt.Errorf("the forge named no node id for the pull request, and the queue mutation takes one")
	}
	var out struct {
		Data struct {
			Enqueue struct {
				Entry struct {
					ID string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"enqueuePullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_, err = g.do(ctx, http.MethodPost, "/graphql", map[string]any{
		"query":     "mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){mergeQueueEntry{id}}}",
		"variables": map[string]string{"id": nodeID},
	}, &out)
	if err != nil {
		return false, err
	}
	for _, e := range out.Errors {
		if strings.Contains(strings.ToLower(e.Message), "already") {
			return true, nil
		}
		return false, fmt.Errorf("enqueuePullRequest: %s", e.Message)
	}
	if out.Data.Enqueue.Entry.ID == "" {
		return false, fmt.Errorf("enqueuePullRequest: no merge queue entry in the reply")
	}
	return false, nil
}
