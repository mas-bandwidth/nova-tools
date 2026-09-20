package pulse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// prListPage is one gh pr list fetch. GitHub serves at most 100 per page;
// --limit 100 is one page, and a full page means another page may exist.
const prListPage = 100

// prMergedLookback is the "recently merged" window. Just-landed PRs of the
// 2026-09-20 wave were hours old; a week covers a weekend without scanning
// the whole merge history. The scan pages until a short page or until the
// oldest row of a full page is outside this window (newest-first).
const prMergedLookback = 7 * 24 * time.Hour

type carryingPRRow struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HeadRefName string `json:"headRefName"`
	MergedAt    string `json:"mergedAt"`
}

// prCarryingIssue is the first open, then recently merged, pull request that
// already names this issue. Open PRs are paged until exhaustion before a
// miss is concluded — --limit 100 missed live PR #1730 Closes #1649 when 218
// were open (#2041 HOLD). Merged PRs are paged through prMergedLookback.
// A forge that does not answer is an error, never "no PR".
func prCarryingIssue(repo string, issue int) (state string, number int, how string, err error) {
	now := time.Now().UTC()
	n, how, err := scanPRs(repo, "open", issue, now, false)
	if err != nil {
		return "", 0, "", err
	}
	if n != 0 {
		return "open", n, how, nil
	}
	n, how, err = scanPRs(repo, "merged", issue, now, true)
	if err != nil {
		return "", 0, "", err
	}
	if n != 0 {
		return "merged", n, how, nil
	}
	return "", 0, "", nil
}

func scanPRs(repo, state string, issue int, now time.Time, window bool) (int, string, error) {
	limit := prListPage
	for {
		rows, err := listCarryingPRs(repo, state, limit)
		if err != nil {
			return 0, "", err
		}
		for _, r := range rows {
			if window && !mergedInWindow(r.MergedAt, now) {
				continue
			}
			if how := howPRNamesIssue(r.Title, r.Body, r.HeadRefName, issue); how != "" {
				return r.Number, how, nil
			}
		}
		if len(rows) < limit {
			return 0, "", nil
		}
		if window && len(rows) > 0 && !mergedInWindow(rows[len(rows)-1].MergedAt, now) {
			return 0, "", nil
		}
		limit += prListPage
	}
}

func listCarryingPRs(repo, state string, limit int) ([]carryingPRRow, error) {
	raw, err := ghJSON(childTimeout, "pr", "list", "-R", repo,
		"--state", state, "--limit", strconv.Itoa(limit),
		"--json", "number,title,body,headRefName,mergedAt")
	if err != nil {
		return nil, err
	}
	var rows []carryingPRRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("gh pr list --state %s did not answer JSON this tool can read: %w", state, err)
	}
	return rows, nil
}

func mergedInWindow(mergedAt string, now time.Time) bool {
	if mergedAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, mergedAt)
	if err != nil {
		return true
	}
	return !t.Before(now.Add(-prMergedLookback))
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
