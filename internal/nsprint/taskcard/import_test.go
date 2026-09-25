package taskcard_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// issueSource is a counting fixture IssueSource: every call is a forge read.
type issueSource struct {
	calls  int
	issues map[string]taskcard.Issue
	err    error
}

func (s *issueSource) Issue(_ context.Context, repo string, n int) (taskcard.Issue, error) {
	s.calls++
	if s.err != nil {
		return taskcard.Issue{}, s.err
	}
	iss, ok := s.issues[repo+"#"+itoa(n)]
	if !ok {
		return taskcard.Issue{}, errors.New("HTTP 404: Not Found")
	}
	return iss, nil
}

func itoa(n int) string {
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

const importRef = "mas-bandwidth/nova-tools#3967"

func fixtureIssue() taskcard.Issue {
	body := strings.Join([]string{
		"STREAM: " + cardStream, "WHO: any", "KIND: build", "TYPE: code",
		"BASE: dev", "base-sha: " + baseSHA, "PATHS: internal/nsprint/taskcard/import.go",
		"DEPENDS-ON: nova-tools#3916", "EST: 45", "PRIORITY: 2",
		"", "The issue is imported onto the card at push.", "",
		"DONE-WHEN: `go test ./internal/nsprint/taskcard -run TestCardCarriesTheWholeIssue` passes"}, "\n")
	return taskcard.Issue{Repo: "mas-bandwidth/nova-tools", Number: 3967,
		URL: "https://forge.invalid/mas-bandwidth/nova-tools/issues/3967", Title: "the issue is imported onto the card at push",
		Body: body, State: "open", Labels: []string{"swarm", "cards"},
		CreatedAt: "2026-09-25T19:30:00Z", UpdatedAt: "2026-09-25T19:35:00Z",
		Typed: taskcard.TypedLines([]string{"looks good\nSPEC who=emma 10/10 head=abc1234", "SCORE 9/10 who=stella"})}
}

// TestCardCarriesTheWholeIssue is the #3967 DONE-WHEN (first half): a push
// from an issue fixture leaves every field the card will ever need on the
// record, in waiting, with one issue read; the fetch failing, an incomplete
// copy, an open PR that already closes the issue and a second card for the
// same issue are each refused with nothing written; and everything after
// waiting (the harness header, the friend brief, the deal, fsck) reads the
// record and never the source.
func TestCardCarriesTheWholeIssue(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	src := &issueSource{issues: map[string]taskcard.Issue{importRef: fixtureIssue()}}
	r, err := taskcard.Import(ctx, c, src, importRef, taskcard.PushRequest{ID: "imp-3967", Where: "waiting", Sprint: sprint, By: "rowan"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if r.Where != "waiting" || src.calls != 1 {
		t.Fatalf("import: where=%s reads=%d, want waiting and one read", r.Where, src.calls)
	}
	rec, err := taskcard.Record(ctx, c, "imp-3967")
	if err != nil {
		t.Fatal(err)
	}
	iss := fixtureIssue()
	sum := sha256.Sum256([]byte(iss.Body))
	want := map[string]string{
		"where": "waiting", "stream": cardStream, "ref": importRef, "origin": iss.URL, "title": iss.Title,
		"issue_title": iss.Title, "issue_number": "3967", "issue_state": "open", "issue_labels": "cards,swarm",
		"issue_created_at": iss.CreatedAt, "issue_updated_at": iss.UpdatedAt, "issue_open_prs": "none",
		"issue_typed": "SPEC who=emma 10/10 head=abc1234\nSCORE 9/10 who=stella",
		"body":        iss.Body,
		"body_sha256": hex.EncodeToString(sum[:]),
		"who":         "any", "kind": "build", "type": "code", "base": "dev", "base_sha": baseSHA,
		"paths": "internal/nsprint/taskcard/import.go", "depends_on": "nova-tools#3916", "blocked_on": "nova-tools#3916",
		"est": "45", "priority": "2", "test": "./internal/nsprint/taskcard TestCardCarriesTheWholeIssue",
		"done_when": "`go test ./internal/nsprint/taskcard -run TestCardCarriesTheWholeIssue` passes", "source": "issue",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record %s = %q, want %q", k, rec[k], v)
		}
	}
	if rec["imported_at"] == "" {
		t.Error("record has no imported_at")
	}
	if ttl := c.TTL(ctx, taskcard.Key("imp-3967")).Val(); ttl != -1 {
		t.Errorf("task:imp-3967 TTL %v, want none (keys do not expire)", ttl)
	}
	if got := c.Get(ctx, taskcard.RefKey(importRef)).Val(); got != "imp-3967" {
		t.Errorf("%s = %q, want imp-3967", taskcard.RefKey(importRef), got)
	}

	// After waiting nothing reads the issue: the brief, the deal and fsck
	// read the record.
	if _, err := taskcard.RenderBrief("imp-3967", rec); err != nil {
		t.Fatalf("brief: %v", err)
	}
	if res, err := taskcard.Deal(ctx, c, "rowan", []taskcard.DealMove{{ID: "imp-3967", To: "stella"}}); err != nil || res[0].Refused != "" {
		t.Fatalf("deal: %v %+v", err, res)
	}
	clean(t, c, "after the deal")
	if src.calls != 1 {
		t.Fatalf("issue reads after waiting: %d, want 0", src.calls-1)
	}

	// Refusals write nothing.
	gone := func(id string) {
		t.Helper()
		if n := c.Exists(ctx, taskcard.Key(id)).Val(); n != 0 {
			t.Fatalf("task:%s written by a refused import", id)
		}
	}
	refused := func(name string, src taskcard.IssueSource, ref, id, want string) {
		t.Helper()
		_, err := taskcard.Import(ctx, c, src, ref, taskcard.PushRequest{ID: id, Where: "waiting", Sprint: sprint, By: "rowan"})
		why, ok := taskcard.IsRefused(err)
		if !ok || !strings.Contains(why, want) {
			t.Fatalf("%s: err %v, want a refusal naming %q", name, err, want)
		}
		gone(id)
	}
	refused("fetch fails", &issueSource{err: errors.New("HTTP 502: Bad Gateway")}, "mas-bandwidth/nova-tools#3968", "imp-3968", "fetch failed: HTTP 502")
	empty := fixtureIssue()
	empty.Number, empty.Body = 3969, ""
	refused("no body", &issueSource{issues: map[string]taskcard.Issue{"mas-bandwidth/nova-tools#3969": empty}}, "mas-bandwidth/nova-tools#3969", "imp-3969", "no body")
	withPR := fixtureIssue()
	withPR.Number, withPR.OpenPRs = 3970, []int{3971}
	refused("open PR", &issueSource{issues: map[string]taskcard.Issue{"mas-bandwidth/nova-tools#3970": withPR}}, "mas-bandwidth/nova-tools#3970", "imp-3970", "OPENPR mas-bandwidth/nova-tools#3970 is closed by open PR #3971")
	refused("second card", src, importRef, "imp-3967-b", "DUPLICATE "+importRef+" is task:imp-3967 at ready")
	refused("not a ref", src, "nova-tools#3967", "imp-x", "is not <owner>/<repo>#<n>")
}

// TestGitHubIssuesReadsTheWholeIssue reads the import's REST answers through
// a fake gh: the issue, the typed comment lines, and only the open PRs that
// close it; a PR number is refused as an issue.
func TestGitHubIssuesReadsTheWholeIssue(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	script := `#!/bin/sh
case "$*" in
  "api repos/o/r/issues/7") printf '%s\n' '{"number":7,"html_url":"https://forge.invalid/o/r/issues/7","title":"T","body":"B\nDONE-WHEN: x","state":"open","created_at":"c","updated_at":"u","labels":[{"name":"l1"}]}' ;;
  "api --paginate repos/o/r/issues/7/comments"*) printf '%s\n' '"no typed line"' '"SCORE 10/10 who=emma\nSPEC ok"' ;;
  "api --paginate repos/o/r/issues/7/timeline"*) printf '%s\n' '{"number":8,"body":"Closes #7","repository_url":"https://api.invalid/repos/o/r"}' '{"number":9,"body":"DEPENDS-ON: #7","repository_url":"https://api.invalid/repos/o/r"}' ;;
  "api repos/o/r/issues/5") echo '{"number":5,"pull_request":{"url":"x"}}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := taskcard.GitHubIssues{Bin: gh}
	iss, err := g.Issue(context.Background(), "o/r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if iss.Title != "T" || iss.Body != "B\nDONE-WHEN: x" || iss.URL != "https://forge.invalid/o/r/issues/7" || len(iss.Labels) != 1 ||
		strings.Join(iss.Typed, "|") != "SCORE 10/10 who=emma|SPEC ok" || len(iss.OpenPRs) != 1 || iss.OpenPRs[0] != 8 {
		t.Fatalf("issue %+v", iss)
	}
	if _, err := g.Issue(context.Background(), "o/r", 5); err == nil || !strings.Contains(err.Error(), "is a PR") {
		t.Fatalf("PR as issue: %v", err)
	}
}
