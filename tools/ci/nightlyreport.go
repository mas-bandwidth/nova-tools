package main

import (
	"encoding/json"
	"fmt"
	"time"
)

func init() {
	register(verb{
		name:    "nightly-report",
		summary: "open or update the one 'nightly tagged suite red' issue of the day",
		help: `usage: go run ./tools/ci nightly-report

ONE ISSUE PER DAY, not one per leg. Run by the nightly workflow's report job when
any leg failed: it makes sure there is exactly one open issue titled
"nightly tagged suite red <UTC date>" and adds a comment pointing at this run
when one is already open, else creates it.

Environment: GH_TOKEN (gh's own), GITHUB_REPOSITORY, GITHUB_SERVER_URL and
GITHUB_RUN_ID, which build the run's URL. A red leg of the nightly tier blocks
nothing: the issue is for the morning.

exit 0  the issue was commented on or created
exit 1  a gh call failed
`,
		do: func(e env, args []string) int { return nightlyReport(e, osCmdRunner{}, time.Now, args) },
	})
}

// nightlyReport is the verb over a runner and a clock.
func nightlyReport(e env, r cmdRunner, now func() time.Time, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(e.stderr, "nightly-report: takes no arguments")
		return 2
	}
	repo, server, runID := e.getenv("GITHUB_REPOSITORY"), e.getenv("GITHUB_SERVER_URL"), e.getenv("GITHUB_RUN_ID")
	if repo == "" || server == "" || runID == "" {
		fmt.Fprintln(e.stderr, "nightly-report: GITHUB_REPOSITORY, GITHUB_SERVER_URL and GITHUB_RUN_ID are required")
		return 2
	}
	title := "nightly tagged suite red " + now().UTC().Format("2006-01-02")
	runURL := fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, runID)
	gh := func(a ...string) (string, int) {
		out, code, err := capture(r, cmdSpec{Name: "gh", Args: append(a, "--repo", repo), Dir: e.dir, Stderr: e.stderr})
		if err != nil {
			fmt.Fprintf(e.stderr, "nightly-report: gh: %v\n", err)
			return "", 127
		}
		return out, code
	}

	out, code := gh("issue", "list", "--state", "open", "--limit", "200", "--json", "number,title")
	if code != 0 {
		fmt.Fprintf(e.stderr, "nightly-report: gh issue list exited %d\n", code)
		return 1
	}
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		fmt.Fprintf(e.stderr, "nightly-report: gh issue list printed no issue list: %v\n", err)
		return 1
	}
	for _, is := range issues {
		if is.Title != title {
			continue
		}
		fmt.Fprintf(e.stdout, "issue #%d already open for today; adding a comment\n", is.Number)
		out, code := gh("issue", "comment", fmt.Sprint(is.Number), "--body", "A nightly tagged suite is still red: "+runURL)
		if out != "" {
			fmt.Fprintln(e.stdout, out)
		}
		if code != 0 {
			fmt.Fprintf(e.stderr, "nightly-report: gh issue comment exited %d\n", code)
			return 1
		}
		return 0
	}
	out, code = gh("issue", "create", "--title", title, "--body", "A leg of the nightly tagged suites failed.\n\nRun: "+runURL)
	if out != "" {
		fmt.Fprintln(e.stdout, out)
	}
	if code != 0 {
		fmt.Fprintf(e.stderr, "nightly-report: gh issue create exited %d\n", code)
		return 1
	}
	return 0
}
