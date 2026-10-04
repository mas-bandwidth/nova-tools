package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	// provedAPI is the host the merge-group ask goes to.
	provedAPI = "https://api.github.com"
	// provedTimeout bounds the one HTTP ask.
	provedTimeout = 20 * time.Second
)

func init() {
	register(verb{
		name:    "proved-by-merge-group",
		summary: "ask whether a successful merge_group run of this sha exists",
		help: `ci proved-by-merge-group --repo <owner/name> --sha <sha>   (GH_TOKEN in the environment)

A push to dev is the merge queue's own merge commit almost every time, and the queue
already ran the workflow on that exact sha as a merge_group (its head_sha is the commit
that lands). Running the shards a third time on the same tree proves nothing and costs a
full run on every machine. This asks the GitHub API for a successful merge_group run of
the sha: when one exists the vet and test steps are skipped and the job passes on that
evidence. A direct push to dev has no such run and runs in full.

ANY API TROUBLE READS AS "NOT PROVED", never as a skip: a transport error, a non-200
status, a body that does not parse, all count zero runs. Prints
"PROVED sha=<sha> merge_group_runs=<n> proved=<true|false>" and appends
proved=<true|false> to $GITHUB_OUTPUT.

Exit 0 answered (either way), 1 $GITHUB_OUTPUT cannot be written, 2 bad usage.

example:
  GH_TOKEN=... go run ./tools/ci proved-by-merge-group --repo owner/name --sha 0123abc
`,
		do: func(e env, args []string) int { return provedVerb(e, args, selRealHost()) },
	})
}

func provedVerb(e env, args []string, h selHost) int {
	const name = "proved-by-merge-group"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	repo := fs.String("repo", "", "owner/name")
	sha := fs.String("sha", "", "the commit that landed")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *repo == "" || *sha == "" {
		return selRefuse(e, name, "--repo and --sha are required")
	}
	n := provedRuns(h.client, e.getenv("GH_TOKEN"), *repo, *sha)
	proved := n > 0
	fmt.Fprintf(e.stdout, "PROVED sha=%s merge_group_runs=%d proved=%t\n", *sha, n, proved)
	if err := appendGitHubFile(e.getenv, "GITHUB_OUTPUT", fmt.Sprintf("proved=%t", proved)); err != nil {
		fmt.Fprintf(e.stderr, "proved-by-merge-group: %v\n", err)
		return 1
	}
	return 0
}

// provedRuns is how many successful merge_group runs of sha the API lists; 0
// on any trouble.
func provedRuns(client selHTTP, token, repo, sha string) int {
	q := url.Values{}
	q.Set("event", "merge_group")
	q.Set("head_sha", sha)
	q.Set("status", "success")
	q.Set("per_page", "1")
	ctx, cancel := context.WithTimeout(context.Background(), provedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provedAPI+"/repos/"+repo+"/actions/runs?"+q.Encode(), nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var body struct {
		TotalCount int `json:"total_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.TotalCount < 0 {
		return 0
	}
	return body.TotalCount
}
