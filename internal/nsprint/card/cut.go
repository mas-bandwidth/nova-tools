package card

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ctxindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

// CUT: A GITHUB ISSUE BECOMES ONE CARD RECORD IN REDIS (nova-tools#3623).
//
// The retired pulse cutter rendered a card file into a queue directory; card cut reads
// the issue, renders the card body in the card-push shape (WHO, STREAM,
// DEPENDS-ON and WHY, PATHS, DONE-WHEN, BASE, base-sha, EST, ORIGIN), inlines
// the S2 context block (internal/ctxindex) when an index is named, stores the
// body's exact bytes at BodyKey before the card is published, and pushes the
// card with PushBatch, the one card writer. No file, no queue directory.

// Issue is the part of a GitHub issue a cut reads.
type Issue struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	State string `json:"state"`
}

// IssueSource is the forge seam: one issue read, and the tip of a branch when
// the issue names no base-sha.
type IssueSource interface {
	Issue(ctx context.Context, repo string, n int) (Issue, error)
	BranchSHA(ctx context.Context, repo, ref string) (string, error)
}

// CutInput is one card cut.
type CutInput struct {
	Sprint string
	Repo   string // owner/name
	Issue  int
	Spec   int    // a spec issue in the same repo whose text the context lookup also reads; 0 is none
	Index  string // a ctxindex directory; "" inlines no context
	Stream string // overrides the issue's STREAM: line
	Base   string // overrides the issue's BASE: line
}

// CutCard is what a cut rendered: the card's label and exact bytes, and the
// number of spec IDs the context block carries.
type CutCard struct {
	Label    string
	Stream   string
	Origin   string
	Body     []byte
	Contexts int
}

var (
	repoNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	parenRE    = regexp.MustCompile(`\(([^()]*)\)`)
	bareRefRE  = regexp.MustCompile(`^#([1-9][0-9]*)$`)
	nameRefRE  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)#([1-9][0-9]*)$`)
)

// RenderCut reads the issue (and the spec issue) and renders the card. It
// refuses, before any write, an issue with no STREAM (and no override), an
// unparsable DEPENDS-ON, and one missing PATHS, DONE-WHEN or DEPENDS-ON.
func RenderCut(ctx context.Context, src IssueSource, in CutInput) (CutCard, error) {
	if !repoNameRE.MatchString(in.Repo) {
		return CutCard{}, fmt.Errorf("--repo %q is not owner/name", in.Repo)
	}
	if in.Issue <= 0 {
		return CutCard{}, errors.New("--issue must be a positive issue number")
	}
	issue, err := src.Issue(ctx, in.Repo, in.Issue)
	if err != nil {
		return CutCard{}, fmt.Errorf("read %s#%d: %v", in.Repo, in.Issue, err)
	}
	fields := IssueFields(issue.Body)
	stream := strings.TrimSpace(in.Stream)
	if stream == "" {
		stream = fields["STREAM"]
	}
	if stream == "" {
		return CutCard{}, fmt.Errorf("%s#%d has no STREAM: line; a card belongs to one work stream (add the line to the issue or pass --stream)", in.Repo, in.Issue)
	}
	for _, key := range []string{"PATHS", "DEPENDS-ON", "DONE-WHEN"} {
		if fields[key] == "" {
			return CutCard{}, fmt.Errorf("%s#%d has no %s: line", in.Repo, in.Issue, key)
		}
	}
	name := in.Repo[strings.IndexByte(in.Repo, '/')+1:]
	label := fmt.Sprintf("%s-%d", name, in.Issue)
	depends, why, err := CutDepends(in.Repo, fields["DEPENDS-ON"])
	if err != nil {
		return CutCard{}, fmt.Errorf("%s#%d: %v", in.Repo, in.Issue, err)
	}
	if _, _, err := parseDepends(label, depends); err != nil {
		return CutCard{}, fmt.Errorf("%s#%d: %v", in.Repo, in.Issue, err)
	}
	if why == "" {
		why = fields["WHY"]
	}
	base := strings.TrimSpace(in.Base)
	if base == "" {
		base, _, _ = strings.Cut(fields["BASE"], "@")
		base = strings.TrimSpace(base)
	}
	if base == "" {
		return CutCard{}, fmt.Errorf("%s#%d has no BASE: line; pass --base <branch>", in.Repo, in.Issue)
	}
	baseSHA := strings.ToLower(fields["base-sha"])
	if !shaRE.MatchString(baseSHA) {
		if baseSHA, err = src.BranchSHA(ctx, in.Repo, base); err != nil {
			return CutCard{}, fmt.Errorf("resolve %s %s: %v", in.Repo, base, err)
		}
		if !shaRE.MatchString(baseSHA) {
			return CutCard{}, fmt.Errorf("resolve %s %s: %q is not 40 lowercase hex", in.Repo, base, baseSHA)
		}
	}
	who := fields["WHO"]
	if who == "" {
		who = "any"
	}
	lookup := []string{issue.Title, issue.Body}
	var spec Issue
	if in.Spec > 0 {
		if spec, err = src.Issue(ctx, in.Repo, in.Spec); err != nil {
			return CutCard{}, fmt.Errorf("read spec %s#%d: %v", in.Repo, in.Spec, err)
		}
		lookup = append(lookup, spec.Title, spec.Body)
	}
	title := oneline.Escape(strings.TrimSpace(issue.Title))
	origin := IssueURL(in.Repo, in.Issue)
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=<sha12> %s #%d fixed with its red test first: %s\n", label, in.Repo, in.Issue, title)
	head := [][2]string{
		{"KIND", typedrec.KindFix},
		{"TASK", title},
		{"REPO", in.Repo},
		{"BASE", base},
		{"base-sha", baseSHA},
		{"PATHS", fields["PATHS"]},
		{"DEPENDS-ON", depends},
		{"WHY", why},
		{"DONE-WHEN", fields["DONE-WHEN"]},
		{"WHO", who},
		{"STREAM", stream},
		{"EST", fields["EST"]},
		{"ORIGIN", origin},
	}
	if in.Spec > 0 {
		head = append(head, [2]string{"SPEC", fmt.Sprintf("%s#%d", in.Repo, in.Spec)})
	}
	for _, kv := range head {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], oneline.Escape(kv[1]))
		}
	}
	fmt.Fprintf(&b, "\nISSUE %s#%d, quoted:\n", in.Repo, in.Issue)
	quote(&b, issue.Body)
	if in.Spec > 0 {
		fmt.Fprintf(&b, "\nSPEC %s#%d (%s), quoted:\n", in.Repo, in.Spec, oneline.Escape(strings.TrimSpace(spec.Title)))
		quote(&b, spec.Body)
	}
	contexts := 0
	if in.Index != "" {
		ix, err := ctxindex.Open(in.Index)
		if err != nil {
			return CutCard{}, fmt.Errorf("--index %s: %v", in.Index, err)
		}
		block, err := ix.Context(strings.Join(lookup, "\n"))
		if err != nil {
			return CutCard{}, fmt.Errorf("--index %s: %v", in.Index, err)
		}
		if block != "" {
			contexts = strings.Count(block, "\nSPEC ")
			b.WriteString("\n" + block)
		}
	}
	return CutCard{Label: label, Stream: stream, Origin: origin, Body: []byte(sealContract(b.String())), Contexts: contexts}, nil
}

// IssueURL is the card's ORIGIN: the issue's page on GitHub.
func IssueURL(repo string, n int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, n)
}

// quote writes text with every line prefixed "> ", so no line of the issue
// can be read as a header line of the card (ReadCardBase reads the first 40
// lines for base-sha:).
func quote(b *strings.Builder, text string) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(strings.TrimRight("> "+line, " ") + "\n")
	}
}

// sealContract replaces line 1's <sha12> with the sha-12 of every line below
// it, as the retired pulse cutters did, so line 1 cannot drift from the body.
func sealContract(card string) string {
	first, rest, _ := strings.Cut(card, "\n")
	sum := sha256.Sum256([]byte(rest))
	return strings.ReplaceAll(first, "<sha12>", hex.EncodeToString(sum[:])[:12]) + "\n" + rest
}

// cutKeys are the issue lines a cut reads, first occurrence wins.
var cutKeys = []string{"STREAM", "PATHS", "DEPENDS-ON", "WHY", "DONE-WHEN", "BASE", "base-sha", "EST", "WHO"}

// IssueFields reads the card's lines from an issue body: the first line of
// each key in cutKeys, anywhere in the body, after list markers and Markdown
// emphasis around the key ("- **DONE-WHEN:** ...").
func IssueFields(body string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimLeft(line, "-*> ")
		line = strings.TrimPrefix(line, "**")
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.Trim(key, "*`"))
		value = strings.TrimSpace(strings.TrimLeft(value, "*"))
		for _, k := range cutKeys {
			if key == k && value != "" {
				if _, seen := out[k]; !seen {
					out[k] = unquoteCode(value)
				}
			}
		}
	}
	return out
}

// unquoteCode drops the backticks of a value that is one code span
// ("`9da3b12d...`"); a value that merely contains code keeps them.
func unquoteCode(v string) string {
	if len(v) > 1 && strings.HasPrefix(v, "`") && strings.HasSuffix(v, "`") && strings.Count(v, "`") == 2 {
		return strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

// CutDepends turns an issue's DEPENDS-ON value into the card vocabulary:
// "none" stays none; "#<n>" and "<name>#<n>" become owner/name#n in repo's
// owner; a parenthetical "(WHY: ...)" is returned as the why and every other
// parenthetical is dropped. An entry that is still not a card id,
// owner/repo#n, <slug>:sentinel or task:<id> (stream/<slug>, the older
// spelling of a stream edge, is its sentinel) is refused.
func CutDepends(repo, value string) (string, string, error) {
	why := ""
	value = parenRE.ReplaceAllStringFunc(value, func(p string) string {
		inner := strings.TrimSpace(p[1 : len(p)-1])
		if w, ok := strings.CutPrefix(inner, "WHY:"); ok && why == "" {
			why = strings.TrimSpace(w)
		}
		return ""
	})
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("DEPENDS-ON names nothing; write none")
	}
	if strings.EqualFold(value, "none") || value == "-" {
		return "none", why, nil
	}
	owner := repo[:strings.IndexByte(repo, '/')]
	var entries []string
	for _, part := range strings.Split(value, ",") {
		entry := strings.TrimSpace(part)
		if m := bareRefRE.FindStringSubmatch(entry); m != nil {
			entry = repo + "#" + m[1]
		} else if m := nameRefRE.FindStringSubmatch(entry); m != nil {
			entry = owner + "/" + m[1] + "#" + m[2]
		}
		if _, err := parseDependency(entry); err != nil || strings.ContainsAny(entry, " \t") {
			return "", "", fmt.Errorf("DEPENDS-ON: %q is not a card id, owner/repo#n, <slug>:sentinel or task:<id>", entry)
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, ","), why, nil
}

// Cut renders the card, stores its exact bytes at BodyKey (so card run can
// read the body the moment the card is dealt), and pushes it with PushBatch.
// It returns the card and the push's place (pool, waiting or exists).
func Cut(ctx context.Context, client *redis.Client, src IssueSource, in CutInput) (CutCard, VerbResult, error) {
	if !sprintRE.MatchString(in.Sprint) {
		return CutCard{}, VerbResult{}, errors.New("sprint name must match [a-z0-9-]{1,40}")
	}
	c, err := RenderCut(ctx, src, in)
	if err != nil {
		return CutCard{}, VerbResult{}, err
	}
	sum := sha256.Sum256(c.Body)
	if err := client.SetNX(ctx, BodyKey(in.Sprint, hex.EncodeToString(sum[:])), c.Body, BodyTTL).Err(); err != nil {
		return c, VerbResult{}, fmt.Errorf("store body: %v", err)
	}
	res := PushBatch(ctx, client, in.Sprint, []CardFile{{Name: c.Label, Body: c.Body}}, PushOptions{})[0]
	return c, res, nil
}

// GHIssues is the real IssueSource: the one GitHub client (#4343), REST
// only, never GraphQL; the issue read is the import (issue -> card).
type GHIssues struct {
	Client *gh.Client
}

// Issue implements IssueSource.
func (g GHIssues) Issue(ctx context.Context, repo string, n int) (Issue, error) {
	if g.Client == nil {
		return Issue{}, errors.New("card: no GitHub client")
	}
	var out struct {
		Title string  `json:"title"`
		Body  *string `json:"body"`
		State string  `json:"state"`
	}
	if _, err := g.Client.Do(ctx, http.MethodGet, "/repos/"+repo+"/issues/"+strconv.Itoa(n), nil, &out); err != nil {
		return Issue{}, err
	}
	is := Issue{Title: out.Title, State: out.State}
	if out.Body != nil {
		is.Body = *out.Body
	}
	return is, nil
}

// BranchSHA implements IssueSource.
func (g GHIssues) BranchSHA(ctx context.Context, repo, ref string) (string, error) {
	if g.Client == nil {
		return "", errors.New("card: no GitHub client")
	}
	var out struct {
		SHA string `json:"sha"`
	}
	if _, err := g.Client.Do(ctx, http.MethodGet, "/repos/"+repo+"/commits/"+ref, nil, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.SHA), nil
}
