package taskcard

// The issue is imported onto the card at push (nova-tools#3967; Glenn
// 2026-09-25 3:30 PM ET: "All of the issue data for cards should be imported
// into the redis card structure at the time of creating the card and putting
// it in waiting. Once a card is in waiting, you should not need to talk to
// github, except to close out the issue and PR when it goes to landed.").
//
// Import is the first of the card's two GitHub touch points (the second is
// the landed close): one IssueSource read of the whole issue, then one push
// that writes every field onto task:<id> and moves it to waiting in the same
// FCALL. A copy that is incomplete (the fetch failed, no title or body, the
// issue closed, or an open PR already closes it) is refused before any
// write, so a record is never half an issue. Everything after waiting reads
// the record (internal/ci TestNoGitHubReadsOnTheCardPath holds the rule).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/redis/go-redis/v9"
)

// Issue is everything a card will ever need from its origin issue, read
// once at import.
type Issue struct {
	Repo      string // owner/name
	Number    int
	URL       string // the issue's html_url: the record's origin
	Title     string
	Body      string
	State     string // open | closed
	Labels    []string
	CreatedAt string
	UpdatedAt string
	// Typed are the typed lines the issue's comments carry (SPEC and SCORE),
	// oldest comment first.
	Typed []string
	// OpenPRs are the open PRs that close this issue at import time.
	OpenPRs []int
}

// IssueSource reads one issue whole. GitHubIssues is the real one; a test
// hands in a fixture (CI-NET: no host in a test).
type IssueSource interface {
	Issue(ctx context.Context, repo string, n int) (Issue, error)
}

// ImportFields are the record fields Import writes beside the spec fields
// (ParseIssue's) and the push's own (ref, origin, title).
var ImportFields = []string{"issue_number", "issue_state", "issue_labels", "issue_created_at",
	"issue_updated_at", "issue_typed", "issue_open_prs", "issue_title", "body_sha256", "imported_at"}

// RefKey is the one-card-per-issue index: the task that holds an imported
// issue (ns_tcard_push writes it with the record; the dealer's Records reads
// it to answer an issue in DEPENDS-ON).
func RefKey(ref string) string { return deal.TaskRefKey(ref) }

// ParseIssueRef splits owner/name#n; ok is false for anything else.
func ParseIssueRef(ref string) (repo string, n int, ok bool) {
	m := refRE.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(ref[strings.LastIndexByte(ref, '#')+1:])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// Import reads the issue ref names through src and pushes the card with the
// whole issue on its record: r's ID, stream, sprint, route and flags as
// given, Ref, Origin and Title from the issue, the spec parsed from its body
// with r.Spec's non-empty fields over it, and every ImportFields field. A
// fetch that fails or a copy that is incomplete is a *Refused and nothing is
// written.
func Import(ctx context.Context, c redis.Cmdable, src IssueSource, ref string, r PushRequest) (PushResult, error) {
	repo, n, ok := ParseIssueRef(ref)
	if !ok {
		return PushResult{}, &Refused{Why: fmt.Sprintf("IMPORT %q is not <owner>/<repo>#<n>", ref)}
	}
	ref = repo + "#" + strconv.Itoa(n)
	if src == nil {
		return PushResult{}, &Refused{Why: "IMPORT " + ref + " has no issue source"}
	}
	iss, err := src.Issue(ctx, repo, n)
	if err != nil {
		return PushResult{}, &Refused{Why: fmt.Sprintf("IMPORT %s fetch failed: %s; nothing written", ref, oneLine(err.Error()))}
	}
	if why := iss.incomplete(repo, n); why != "" {
		return PushResult{}, &Refused{Why: fmt.Sprintf("IMPORT %s incomplete: %s; nothing written", ref, why)}
	}
	if len(iss.OpenPRs) > 0 {
		nums := make([]string, len(iss.OpenPRs))
		for i, p := range iss.OpenPRs {
			nums[i] = "#" + strconv.Itoa(p)
		}
		return PushResult{}, &Refused{Why: fmt.Sprintf("OPENPR %s is closed by open PR %s; build on it, never a second card (#3911)", ref, strings.Join(nums, ","))}
	}
	spec := ParseIssue(iss.Body)
	if r.Spec != nil {
		for _, f := range specFields {
			if v := *r.Spec.slot(f.field); v != "" {
				*spec.slot(f.field) = v
			}
		}
	}
	r.Spec = &spec
	r.Ref, r.Origin = ref, iss.URL
	if r.Title == "" {
		r.Title = iss.Title
	}
	sum := sha256.Sum256([]byte(spec.Body))
	prs := "none"
	labels := append([]string(nil), iss.Labels...)
	sort.Strings(labels)
	r.Extra = append(r.Extra,
		"issue_number", strconv.Itoa(iss.Number),
		"issue_state", iss.State,
		"issue_labels", dash(strings.Join(labels, ",")),
		"issue_created_at", iss.CreatedAt,
		"issue_updated_at", iss.UpdatedAt,
		"issue_typed", dash(strings.Join(iss.Typed, "\n")),
		"issue_open_prs", prs,
		"body_sha256", hex.EncodeToString(sum[:]),
		"issue_title", iss.Title,
		"imported_at", time.Now().UTC().Format(time.RFC3339))
	return Push(ctx, c, r)
}

// incomplete names what a copy lacks, or "".
func (iss Issue) incomplete(repo string, n int) string {
	var miss []string
	if iss.Number != n {
		miss = append(miss, fmt.Sprintf("number %d is not %d", iss.Number, n))
	}
	if !strings.EqualFold(iss.Repo, repo) {
		miss = append(miss, fmt.Sprintf("repo %q is not %s", iss.Repo, repo))
	}
	for _, f := range []struct{ name, v string }{{"url", iss.URL}, {"title", iss.Title}, {"body", iss.Body},
		{"created_at", iss.CreatedAt}, {"updated_at", iss.UpdatedAt}} {
		if strings.TrimSpace(f.v) == "" {
			miss = append(miss, "no "+f.name)
		}
	}
	if iss.State != "open" {
		miss = append(miss, "state "+dash(iss.State)+", not open")
	}
	return strings.Join(miss, ", ")
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// typedRE is a comment line that carries a typed word the card keeps.
var typedRE = regexp.MustCompile(`^(SPEC|SCORE)\b`)

// TypedLines are the lines of comment bodies that carry a typed word, in order.
func TypedLines(bodies []string) []string {
	var out []string
	for _, b := range bodies {
		for _, l := range strings.Split(strings.ReplaceAll(b, "\r\n", "\n"), "\n") {
			if l = strings.TrimSpace(l); typedRE.MatchString(l) {
				out = append(out, l)
			}
		}
	}
	return out
}

// Closes reports whether a PR body closes issue n of repo: a closing keyword
// then #n (the PR's own repo) or <repo>#n.
func Closes(body, prRepo, repo string, n int) bool {
	re := regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s+([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?#` + strconv.Itoa(n) + `\b`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if m[1] == "" && strings.EqualFold(prRepo, repo) || strings.EqualFold(m[1], repo) {
			return true
		}
	}
	return false
}

// GitHubIssues is the import's IssueSource: `gh api` over REST (never
// GraphQL), with the caller's GH_CONFIG_DIR. Three reads: the issue, its
// comments, and its timeline's cross-referenced open PRs. It is the only
// forge read on the card path (internal/ci TestNoGitHubReadsOnTheCardPath).
type GitHubIssues struct {
	Bin string // the gh program; empty is gh on PATH
}

// Issue implements IssueSource.
func (g GitHubIssues) Issue(ctx context.Context, repo string, n int) (Issue, error) {
	base := fmt.Sprintf("repos/%s/issues/%d", repo, n)
	raw, err := g.get(ctx, base)
	if err != nil {
		return Issue{}, err
	}
	var v struct {
		Number      int             `json:"number"`
		HTMLURL     string          `json:"html_url"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		State       string          `json:"state"`
		CreatedAt   string          `json:"created_at"`
		UpdatedAt   string          `json:"updated_at"`
		PullRequest json.RawMessage `json:"pull_request"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Issue{}, fmt.Errorf("%s: %w", base, err)
	}
	if len(v.PullRequest) > 0 && string(v.PullRequest) != "null" {
		return Issue{}, fmt.Errorf("%s#%d is a PR, not an issue", repo, n)
	}
	iss := Issue{Repo: repo, Number: v.Number, URL: v.HTMLURL, Title: v.Title, Body: v.Body, State: v.State,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	for _, l := range v.Labels {
		iss.Labels = append(iss.Labels, l.Name)
	}
	lines, err := g.get(ctx, "--paginate", base+"/comments", "--jq", ".[] | .body | @json")
	if err != nil {
		return Issue{}, err
	}
	var bodies []string
	for _, l := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		var s string
		if l != "" && json.Unmarshal([]byte(l), &s) == nil {
			bodies = append(bodies, s)
		}
	}
	iss.Typed = TypedLines(bodies)
	lines, err = g.get(ctx, "--paginate", base+"/timeline", "--jq",
		`.[] | select(.event == "cross-referenced") | .source.issue | select(.pull_request != null and .state == "open") | {number, body, repository_url} | @json`)
	if err != nil {
		return Issue{}, err
	}
	for _, l := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		var pr struct {
			Number int    `json:"number"`
			Body   string `json:"body"`
			Repo   string `json:"repository_url"`
		}
		if l == "" || json.Unmarshal([]byte(l), &pr) != nil {
			continue
		}
		prRepo := pr.Repo[strings.LastIndex(pr.Repo, "/repos/")+len("/repos/"):]
		if strings.EqualFold(prRepo, repo) && Closes(pr.Body, prRepo, repo, n) {
			iss.OpenPRs = append(iss.OpenPRs, pr.Number)
		}
	}
	return iss, nil
}

// get runs one `gh api <args>`: the import's one forge seam, guarded.
func (g GitHubIssues) get(ctx context.Context, args ...string) ([]byte, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	argv := append([]string{"api"}, args...)
	testguard.RefuseHosts(bin, argv...)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := oneLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh api %s: %s", args[len(args)-1], msg)
	}
	return out, nil
}
