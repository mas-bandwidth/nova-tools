// Command ciplan is the plan job at the head of .github/workflows/ci.yml. It
// reads the repository's self-hosted runners through the Actions API, counts
// online-and-idle runners per pool (studio, space, hulk, vision), and writes a
// runs_on JSON object the test shards and the merge gate read with fromJSON:
// each shard gets its pool's label array while the pool's idle capacity covers
// it and the hosted image for that OS once it does not. A push to dev or main
// and the nightly schedule keep the fleet unconditionally. A spill is one line
// in the job summary.
//
// The planner never blocks CI. If the API cannot be read, it routes every shard
// to GitHub-hosted runners -- the overflow the spillover exists to provide --
// rather than failing the run at the gate.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/plan"
)

const apiBase = "https://api.github.com"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ciplan:", err)
		os.Exit(1)
	}
}

func run() error {
	event := os.Getenv("GITHUB_EVENT_NAME")
	fleet := plan.FleetUnconditional(event)
	assignments := plan.Assignments(event)

	var runners []plan.Runner
	if !fleet {
		var err error
		runners, err = loadRunners()
		if err != nil {
			// The planner never blocks CI: a run that cannot read the fleet
			// spills to hosted runners instead of holding every shard.
			fmt.Fprintf(os.Stderr, "ciplan: reading runners: %v; routing the overflow to GitHub-hosted runners\n", err)
		}
	}

	res := plan.Route(runners, assignments, plan.Reserve, fleet)

	raw, err := json.Marshal(res.RunsOn)
	if err != nil {
		return err
	}
	if err := appendTo("GITHUB_OUTPUT", fmt.Sprintf("runs_on=%s\n", raw)); err != nil {
		return err
	}
	fmt.Println(string(raw))

	summary := spillSummary(res, fleet)
	if err := appendTo("GITHUB_STEP_SUMMARY", summary+"\n"); err != nil {
		return err
	}
	fmt.Println(summary)
	return nil
}

// loadRunners returns the repository's runners. CIPLAN_RUNNERS_FILE and
// CIPLAN_RUNNERS exist so the arithmetic can be exercised from a fixture of
// runner states without a token; otherwise it pages the Actions API.
func loadRunners() ([]plan.Runner, error) {
	if path := os.Getenv("CIPLAN_RUNNERS_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return plan.ParseRunners(raw)
	}
	if inline := os.Getenv("CIPLAN_RUNNERS"); inline != "" {
		return plan.ParseRunners([]byte(inline))
	}
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return nil, fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN is not set")
	}
	const perPage = 100
	var all []plan.Runner
	for page := 1; page <= 50; page++ {
		url := fmt.Sprintf("%s/repos/%s/actions/runners?per_page=%d&page=%d", apiBase, repo, perPage, page)
		body, err := get(url, token)
		if err != nil {
			return nil, err
		}
		p, err := plan.ParseRunnerPage(body)
		if err != nil {
			return nil, err
		}
		all = append(all, p.Runners...)
		if len(p.Runners) < perPage {
			break
		}
	}
	return all, nil
}

func get(url, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// spillSummary is the one summary line: what left the fleet and why.
func spillSummary(res plan.Result, fleet bool) string {
	switch {
	case fleet:
		return "spill: none (push or schedule keeps the fleet unconditionally)"
	case len(res.Spilled) == 0:
		return "spill: none (the fleet has idle capacity for every shard)"
	default:
		return fmt.Sprintf("spill: %d shard(s) overflow to GitHub-hosted runners: %s",
			len(res.Spilled), strings.Join(res.Spilled, ", "))
	}
}

// appendTo appends to the file named by an environment variable when it is set,
// which is how a job writes outputs and the summary.
func appendTo(env, text string) error {
	path := os.Getenv(env)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
