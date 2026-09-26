// Package file is `nova-sprint file` (nova-tools #3089, #2756 spec 4.11,
// control 50): file an issue or a comment by REST from a body FILE, lint the
// body before anything is posted, and read the stored body back.
//
// On 2026-09-23 three batches of issues were posted whose whole body was a
// literal `@/private/tmp/.../<k>.md`: `gh api -f body=@file` sends the string,
// and nothing read the body back. This verb reads the file itself, refuses a
// body that is a literal @path, is under 200 characters, or (for an issue)
// lacks the card lines, posts by REST, GETs the stored body and compares it
// with the file byte for byte.
//
// Exit 0 filed and read back equal, 1 filed but the stored body differs (the
// issue exists and must be fixed by hand), 2 refused or could not run (for a
// lint refusal nothing was posted).
package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// DefaultAPI is the GitHub REST root the verb posts to.
const DefaultAPI = gh.DefaultAPI

// DefaultOrg is the owner a bare repo name (`nova-tools`) is filed under.
const DefaultOrg = "mas-bandwidth"

// MinBody is the shortest body the verb will post, in characters. The broken
// batches of 2026-09-23 were 40-70 characters of path.
const MinBody = 200

// Deps are the verb's seams. API and HTTP reach GitHub; Token supplies the
// bearer token; Push queues the build task for --push-to.
type Deps struct {
	API   string
	HTTP  *http.Client
	Token func() (string, error)
	Push  func(ctx context.Context, redisAddr string, req task.PushRequest) (task.PushResult, error)
	// Redis counts the calls under file when set.
	Redis redis.Cmdable
}

// Usage is the verb's help text.
const Usage = `usage:
  nova-sprint file --repo <owner/name|name> --title <t> --body-file <f> [--push-to <friend> [--front] --sprint <s> --redis <addr>]
  nova-sprint file --repo <owner/name|name> --comment <issue> --body-file <f>

Reads the body from the FILE (never a literal @path), refuses before posting a
body that starts with @ or is under 200 characters, and for an issue one that
lacks What:, DONE-WHEN:, PATHS:, BASE:, DEPENDS-ON: or an owner:/reader:/est:
line (reader must not be the owner). Posts by REST, reads the stored body back
and compares it byte for byte. Prints FILED <repo>#<n> len=<k> or
COMMENTED <repo>#<n> comment=<id> len=<k>. --push-to queues the build task
(kind work) for that friend in the same call, after the read-back.
exit codes: 0 filed and read back equal, 1 stored body differs, 2 refused or could not run.
`

var repoRx = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// Main runs the verb. It never posts before every refusal has been checked.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer, d Deps) int {
	fs := verbflag.New("file")
	repo := fs.String("repo", "", "")
	title := fs.String("title", "", "")
	bodyFile := fs.String("body-file", "", "")
	comment := fs.Int("comment", 0, "")
	pushTo := fs.String("push-to", "", "")
	front := fs.Bool("front", false, "")
	sprint := fs.String("sprint", "", "")
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "takes flags, not positional arguments")
	}
	r := *repo
	if r != "" && !strings.Contains(r, "/") {
		r = DefaultOrg + "/" + r
	}
	switch {
	case !repoRx.MatchString(r):
		return refuse(stderr, "--repo wants owner/name or a bare name under "+DefaultOrg)
	case *bodyFile == "":
		return refuse(stderr, "--body-file is required; the body is read from that file")
	case strings.HasPrefix(*bodyFile, "@"):
		return refuse(stderr, "--body-file takes a path, not @path")
	case *comment < 0:
		return refuse(stderr, "--comment wants an issue number")
	case *comment > 0 && *title != "":
		return refuse(stderr, "--comment posts a comment; it takes no --title")
	case *comment > 0 && *pushTo != "":
		return refuse(stderr, "--push-to queues the build task of a new issue; not with --comment")
	case *comment == 0 && strings.TrimSpace(*title) == "":
		return refuse(stderr, "--title is required for an issue")
	case *comment == 0 && (strings.HasPrefix(strings.TrimSpace(*title), "@") || strings.ContainsAny(*title, "\r\n")):
		return refuse(stderr, "--title is one line and never a literal @path")
	case *pushTo != "" && (*sprint == "" || *redisAddr == ""):
		return refuse(stderr, "--push-to needs --sprint and --redis")
	case *pushTo == "" && *front:
		return refuse(stderr, "--front only goes with --push-to")
	}
	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		return refuse(stderr, "cannot read --body-file: "+err.Error())
	}
	var findings []string
	if *comment > 0 {
		findings = LintBody(body)
	} else {
		findings = LintIssue(body)
	}
	if len(findings) > 0 {
		fmt.Fprintf(stderr, "nova-sprint file: REFUSED %s; nothing posted\n", oneline.Escape(strings.Join(findings, "; ")))
		return 2
	}
	if *pushTo != "" && d.Push == nil {
		return refuse(stderr, "--push-to has no task store wired")
	}
	c, err := newClient(d)
	if err != nil {
		return refuse(stderr, err.Error())
	}

	if *comment > 0 {
		id, err := c.createComment(ctx, r, *comment, string(body))
		if err != nil {
			return refuse(stderr, "post comment: "+err.Error())
		}
		stored, err := c.commentBody(ctx, r, id)
		if err != nil {
			return readBackFailed(stderr, fmt.Sprintf("%s#%d comment=%d", r, *comment, id), err)
		}
		if stored != string(body) {
			return differs(stderr, fmt.Sprintf("%s#%d comment=%d", r, *comment, id), body, stored)
		}
		fmt.Fprintf(stdout, "COMMENTED %s#%d comment=%d len=%d\n", r, *comment, id, len(body))
		return 0
	}

	n, url, err := c.createIssue(ctx, r, *title, string(body))
	if err != nil {
		return refuse(stderr, "post issue: "+err.Error())
	}
	stored, err := c.issueBody(ctx, r, n)
	if err != nil {
		return readBackFailed(stderr, fmt.Sprintf("%s#%d", r, n), err)
	}
	if stored != string(body) {
		return differs(stderr, fmt.Sprintf("%s#%d", r, n), body, stored)
	}
	fmt.Fprintf(stdout, "FILED %s#%d len=%d\n", r, n, len(body))
	if *pushTo == "" {
		return 0
	}
	req := task.PushRequest{
		Sprint: *sprint,
		ID:     fmt.Sprintf("build-%s-%d", r[strings.Index(r, "/")+1:], n),
		Kind:   task.KindWork,
		Title:  TaskTitle(r, n, *title, body),
		Repo:   r,
		Ref:    url,
		To:     *pushTo,
		Front:  *front,
	}
	res, err := d.Push(ctx, *redisAddr, req)
	if err != nil {
		return refuse(stderr, fmt.Sprintf("%s#%d is filed; the task push failed: %v", r, n, err))
	}
	if res.Overlap != nil {
		fmt.Fprintf(stdout, "PUSH %s %s\n", res.Status, res.Overlap)
		return res.Status.ExitCode()
	}
	fmt.Fprintf(stdout, "PUSH %s id=%s to=%s\n", res.Status, req.ID, *pushTo)
	return res.Status.ExitCode()
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-sprint file: %s; run: nova-sprint help\n", oneline.Escape(what))
	return 2
}

func readBackFailed(stderr io.Writer, where string, err error) int {
	fmt.Fprintf(stderr, "nova-sprint file: READBACK FAILED %s: %s; the post exists, check its body by hand\n", where, oneline.Escape(err.Error()))
	return 1
}

func differs(stderr io.Writer, where string, body []byte, stored string) int {
	head := stored
	if len(head) > 60 {
		head = head[:60]
	}
	fmt.Fprintf(stderr, "nova-sprint file: READBACK DIFFERS %s posted=%d stored=%d stored_head=%s; fix the body by hand\n",
		where, len(body), len(stored), strconv.Quote(head))
	return 1
}

// LintBody is the rule every posted body obeys, issue or comment: it is never
// a literal @path and it is at least MinBody characters.
func LintBody(body []byte) []string {
	var out []string
	lead := strings.TrimLeft(string(body), " \t\r\n\uFEFF")
	if strings.HasPrefix(lead, "@") {
		out = append(out, "body starts with @ (a literal @path, not the file's contents)")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(string(body))); n < MinBody {
		out = append(out, fmt.Sprintf("body is %d characters, under %d", n, MinBody))
	}
	return out
}

// requiredKeys are the card lines an issue carries, in the order a refusal
// names them. Each is a `KEY: value` line at the start of a line (markdown
// bullets and bold allowed), any case, with a value on the same line.
var requiredKeys = []string{"What", "DONE-WHEN", "PATHS", "BASE", "DEPENDS-ON"}

var (
	keyLineRx = regexp.MustCompile(`^([A-Za-z][A-Za-z-]*):(.*)$`)
	ownerRx   = regexp.MustCompile(`(?i)(?:^|\s)owner:\s*([^\s|]+)`)
	readerRx  = regexp.MustCompile(`(?i)(?:^|\s)reader:\s*([^\s|]+)`)
	estRx     = regexp.MustCompile(`(?i)(?:^|\s)est:\s*(\S[^|]*)`)
)

// Fields are the card lines LintIssue read, for the task title.
type Fields struct {
	Keys   map[string]string // upper-cased key -> value
	Owner  string
	Reader string
	Est    string
}

// ParseFields reads the card lines out of an issue body.
func ParseFields(body []byte) Fields {
	f := Fields{Keys: map[string]string{}}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.ReplaceAll(line, "**", "")
		if m := keyLineRx.FindStringSubmatch(line); m != nil {
			k := strings.ToUpper(m[1])
			v := strings.TrimSpace(m[2])
			if _, seen := f.Keys[k]; !seen || f.Keys[k] == "" {
				f.Keys[k] = v
			}
		}
		if f.Owner == "" {
			if m := ownerRx.FindStringSubmatch(line); m != nil {
				f.Owner = cleanName(m[1])
				if m := readerRx.FindStringSubmatch(line); m != nil {
					f.Reader = cleanName(m[1])
				}
				if m := estRx.FindStringSubmatch(line); m != nil {
					f.Est = strings.TrimSpace(m[1])
				}
			}
		}
	}
	return f
}

func cleanName(s string) string {
	return strings.ToLower(strings.Trim(s, "@`*,.;"))
}

// LintIssue is LintBody plus the card lines: What:, DONE-WHEN:, PATHS:,
// BASE:, DEPENDS-ON: and one line naming owner:, reader: and est:, with a
// reader who is not the owner.
func LintIssue(body []byte) []string {
	out := LintBody(body)
	f := ParseFields(body)
	var missing []string
	for _, k := range requiredKeys {
		if f.Keys[strings.ToUpper(k)] == "" {
			missing = append(missing, k+":")
		}
	}
	if len(missing) > 0 {
		out = append(out, "missing "+strings.Join(missing, " ")+" (each a line with its value)")
	}
	switch {
	case f.Owner == "":
		out = append(out, "missing the owner: reader: est: line")
	case f.Reader == "":
		out = append(out, "the owner line has no reader:")
	case f.Reader == f.Owner:
		out = append(out, "reader must not be the owner ("+f.Owner+")")
	}
	if f.Owner != "" && f.Est == "" {
		out = append(out, "the owner line has no est:")
	}
	return out
}

// TaskTitle is the build task's title in the grammar friend-queue and the
// cut cards share: `<repo>#<n> <title> | DONE-WHEN: ... | PATHS: ... |
// DEPENDS-ON: ... | reader: ... | est: ...`.
func TaskTitle(repo string, n int, title string, body []byte) string {
	f := ParseFields(body)
	clean := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "|", "/")) }
	parts := []string{fmt.Sprintf("%s#%d %s", repo, n, clean(title))}
	for _, k := range []string{"DONE-WHEN", "PATHS", "DEPENDS-ON"} {
		parts = append(parts, k+": "+clean(f.Keys[k]))
	}
	parts = append(parts, "owner: "+f.Owner, "reader: "+f.Reader, "est: "+clean(f.Est))
	return strings.Join(parts, " | ")
}

// client is the REST seam, the one GitHub client (internal/gh, #4343):
// two POSTs and two GETs of the issues API, counted under file.
type client struct {
	gh *gh.Client
}

func newClient(d Deps) (*client, error) {
	if d.Token == nil {
		return nil, errors.New("no GitHub token source")
	}
	tok, err := d.Token()
	if err != nil {
		return nil, fmt.Errorf("GitHub token: %w", err)
	}
	if strings.TrimSpace(tok) == "" {
		return nil, errors.New("GitHub token is empty; set GH_TOKEN or log gh in")
	}
	h := d.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	return &client{gh: &gh.Client{API: d.API, HTTP: h, Token: strings.TrimSpace(tok), Verb: "file", Redis: d.Redis}}, nil
}

func (c *client) createIssue(ctx context.Context, repo, title, body string) (int, string, error) {
	n, url, err := c.gh.CreateIssue(ctx, repo, title, body)
	if err != nil {
		return 0, "", short(err)
	}
	return n, url, nil
}

func (c *client) issueBody(ctx context.Context, repo string, n int) (string, error) {
	b, err := c.gh.IssueBody(ctx, repo, n)
	return b, short(err)
}

func (c *client) createComment(ctx context.Context, repo string, issue int, body string) (int64, error) {
	id, err := c.gh.Comment(ctx, repo, issue, body)
	if err != nil {
		return 0, short(err)
	}
	if id <= 0 {
		return 0, errors.New("GitHub answered without a comment id")
	}
	return id, nil
}

func (c *client) commentBody(ctx context.Context, repo string, id int64) (string, error) {
	b, err := c.gh.CommentBody(ctx, repo, id)
	return b, short(err)
}

// short keeps a non-2xx reply's error to 200 characters of body.
func short(err error) error {
	var herr *gh.HTTPError
	if errors.As(err, &herr) && len(herr.Body) > 200 {
		return fmt.Errorf("%s %s: HTTP %d: %s", herr.Method, herr.Path, herr.Status, herr.Body[:200])
	}
	return err
}
