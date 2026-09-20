package pulse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// prCarryingIssue is the first open, then recently merged, pull request that
// already names this issue. The 2026-09-20 fix wave re-cut 27 of 27 because
// generators asked cut for work a PR already carried; cut itself now asks
// the forge before it writes a card. A forge that does not answer is an
// error, never "no PR": cutting while unread is how the wave happened.
func prCarryingIssue(repo string, issue int) (state string, number int, how string, err error) {
	for _, state := range []string{"open", "merged"} {
		raw, err := ghJSON(childTimeout, "pr", "list", "-R", repo,
			"--state", state, "--limit", "100",
			"--json", "number,title,body,headRefName")
		if err != nil {
			return "", 0, "", err
		}
		var rows []struct {
			Number      int    `json:"number"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			HeadRefName string `json:"headRefName"`
		}
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			return "", 0, "", fmt.Errorf("gh pr list --state %s did not answer JSON this tool can read: %w", state, err)
		}
		for _, r := range rows {
			if how := howPRNamesIssue(r.Title, r.Body, r.HeadRefName, issue); how != "" {
				return state, r.Number, how, nil
			}
		}
	}
	return "", 0, "", nil
}

// closingIssueRef is GitHub's closing form: Fixes/Closes/Resolves #N, any
// common tense, so a PR that lands the issue is the same PR the cutter must
// not duplicate.
var closingIssueRef = regexp.MustCompile(`(?i)\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#([0-9]+)\b`)

// branchIssueRef is the issue number as its own path token in a head ref
// (`johnny/1979-the-widget`, `rowan/issue-1979-foo`), never a prefix of a
// longer number.
var branchIssueRef = regexp.MustCompile(`(^|[-/_])([0-9]+)([-/_]|$)`)

func howPRNamesIssue(title, body, branch string, issue int) string {
	if issue < 1 {
		return ""
	}
	if how := closingHow(title, issue); how != "" {
		return how
	}
	if how := closingHow(body, issue); how != "" {
		return how
	}
	hash := "#" + strconv.Itoa(issue)
	if hasHashIssue(title, issue) {
		return "title " + hash
	}
	if hasHashIssue(body, issue) {
		return "body " + hash
	}
	if branchHasIssue(branch, issue) {
		return "branch=" + branch
	}
	return ""
}

func closingHow(s string, issue int) string {
	for _, m := range closingIssueRef.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[2])
		if err == nil && n == issue {
			return strings.TrimSpace(m[0])
		}
	}
	return ""
}

func hasHashIssue(s string, issue int) bool {
	for _, n := range issueRefs(s) {
		if n == issue {
			return true
		}
	}
	return false
}

func branchHasIssue(branch string, issue int) bool {
	want := strconv.Itoa(issue)
	for _, m := range branchIssueRef.FindAllStringSubmatch(branch, -1) {
		if m[2] == want {
			return true
		}
	}
	return false
}
