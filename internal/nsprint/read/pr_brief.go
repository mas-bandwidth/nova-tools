package read

// `read brief --pr N` (nova-tools #4335, with #4315: a read gets the card,
// its evidence, the diff and CI at head, and scores against DONE-WHEN and
// the standard's rules). One command prints everything a cold reader scores
// from, in one screen, on stdout:
//
//   - the issue: title and body. Redis keeps an issue's text only on a card
//     pushed from it (task card push --issue: the record's body is the issue
//     text, source=issue); any other issue, named by the card's SOURCE,
//     ORIGIN or ref (or --issue), is ONE REST read, GET
//     /repos/<owner>/<name>/issues/<n>, and the SOURCES line says so;
//   - the card: the imported card record task:<task> the PR record names
//     (harvest writes task=<primary>), its PATHS, DONE-WHEN, DEPENDS-ON,
//     base and the spec lines of its body (EVIDENCE, SEAMS, RULES, RECEIPTS,
//     KEEP, DO);
//   - DONE-WHEN from each source that has one (issue, card, PR record);
//   - the PR: title and body from the PR record's pr_title and pr_body (the
//     body harvest opened the PR with); a PR the record has no body for (a
//     hand PR) is ONE REST read, GET /repos/<owner>/<name>/pulls/<n>;
//   - the files changed with +/- (git diff --numstat in the bench mirror,
//     base_sha..head, else the merge base of the base branch and head), and
//     the files outside PATHS;
//   - the check rollup at head: our CI (ci:<name>:<head> and one receipt per
//     check) and the GitHub leg the webhook ingest writes
//     (ci:<name>:<head>:gh), with the failing names on one line;
//   - the lines already posted, the rubric and the post command.
//
// No GitHub polling: nothing here reads a check state from GitHub. --no-github
// makes zero HTTP calls; a section Redis has no copy of is then a GAP line.
// Exit 0 the brief is complete, 1 printed with GAP lines or refused (every
// refusal and gap is printed), 2 could not run.

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// PR record fields harvest writes beside the head (card.CopyLedger.recordPR):
// the title and body the PR was opened with, so a brief reads them from Redis.
const (
	FieldPRTitle = "pr_title"
	FieldPRBody  = "pr_body"
)

// CardSpecKeys are the body lines of a card a brief carries (the card-as-spec
// keys of #4313, and the DO line a coordinator's card states its task with).
var CardSpecKeys = []string{"DO", "EVIDENCE", "SEAMS", "RULES", "RECEIPTS", "KEEP"}

// GitHub is the one REST read a brief makes where Redis has no copy,
// through the one GitHub client (internal/gh, #4343; BaseURL empty is its
// root), counted under read-brief in Redis. Nil is --no-github: no HTTP at
// all.
type GitHub struct {
	BaseURL string // GITHUB_API_URL, else the client's root
	Token   string
	HTTP    *http.Client
	Redis   redis.Cmdable
	client  *gh.Client
}

func (g *GitHub) c() *gh.Client {
	if g.client == nil {
		g.client = &gh.Client{API: g.BaseURL, Token: g.Token, HTTP: g.HTTP, Verb: "read brief", Redis: g.Redis}
	}
	return g.client
}

// Calls is how many HTTP requests the brief made.
func (g *GitHub) Calls() int {
	if g == nil || g.client == nil {
		return 0
	}
	return g.client.Calls
}

// Endpoint is the REST path a read asks for (kind issues or pulls), printed
// on the SOURCES line.
func Endpoint(kind, owner, name, n string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/" + kind + "/" + url.PathEscape(n)
}

// TitleBody is one GET of an issue or a pull: its title and body.
func (g *GitHub) TitleBody(ctx context.Context, path string) (string, string, error) {
	var v struct {
		Title string  `json:"title"`
		Body  *string `json:"body"`
	}
	if _, err := g.c().Do(ctx, http.MethodGet, path, nil, &v); err != nil {
		var herr *gh.HTTPError
		if errors.As(err, &herr) {
			return "", "", fmt.Errorf("GET %s: github %d", path, herr.Status)
		}
		return "", "", fmt.Errorf("GET %s: %v", path, err)
	}
	body := ""
	if v.Body != nil {
		body = *v.Body
	}
	return v.Title, body, nil
}

// FileStat is one changed file: lines added and deleted (Binary when git
// counts none).
type FileStat struct {
	Path     string
	Add, Del int
	Binary   bool
}

// ParseNumstat reads `git diff --numstat --no-renames` output.
func ParseNumstat(out string) []FileStat {
	var fs []FileStat
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		f := FileStat{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			f.Binary = true
		} else {
			f.Add, _ = strconv.Atoi(parts[0])
			f.Del, _ = strconv.Atoi(parts[1])
		}
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Path < fs[j].Path })
	return fs
}

var issueRefRx = regexp.MustCompile(`^(?:([A-Za-z0-9][A-Za-z0-9._-]*)/)?([A-Za-z0-9][A-Za-z0-9._-]*)#([1-9][0-9]*)$`)

// IssueRef reads n or #n (in defaultRepo), name#n or owner/name#n, each
// optionally prefixed issue: (a card's ORIGIN); ok false when s names no
// issue. A bare name's owner is defaultRepo's when the names match, else
// prkey.DefaultOwner.
func IssueRef(s, defaultRepo string) (owner, name, n string, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "issue:") // a card's ORIGIN: issue:<name>#<n>
	dOwner, dName, _ := prkey.Split(defaultRepo)
	if v, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil {
		return dOwner, dName, strconv.Itoa(v), v > 0 && dName != ""
	}
	m := issueRefRx.FindStringSubmatch(s)
	if m == nil {
		return "", "", "", false
	}
	owner = m[1]
	if owner == "" {
		owner = prkey.DefaultOwner
		if m[2] == dName {
			owner = dOwner
		}
	}
	return owner, m[2], m[3], true
}

// SpecLines are the lines of text whose key is one of keys, in text order.
func SpecLines(text string, keys []string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		k, v, ok := cardhdr.KeyValue(line)
		if !ok || v == "" {
			continue
		}
		for _, want := range keys {
			if k == want {
				out = append(out, k+": "+v)
				break
			}
		}
	}
	return out
}

// PRBrief is everything `read brief --pr` prints, loaded from Redis, the
// mirror and at most the REST reads Redis has no copy for.
type PRBrief struct {
	Owner, Repo, N string
	Record         map[string]string // HGETALL pr:<name>:<n>
	Lines          []string
	CardKey        string
	Card           map[string]string // HGETALL task:<task>; nil when none
	IssueRef       string            // owner/name#n; "" when none is named
	IssueTitle     string
	IssueBody      string
	PRTitle        string
	PRBody         string
	Mirror         string
	DiffBase       string // the commit the diff is taken from
	Files          []FileStat
	Outside        []string
	CI             ci.Rows
	Sources        []string // where each section came from
	Gaps           []string // what could not be read, and why
	Calls          int
}

// Paths is the PATHS the scope is judged by: the PR record's, else the
// card's, else the issue's PATHS line.
func (b PRBrief) Paths() string {
	if p := strings.TrimSpace(b.Record["paths"]); p != "" {
		return p
	}
	if p := strings.TrimSpace(b.Card["paths"]); p != "" {
		return p
	}
	return firstKey(b.IssueBody, "PATHS")
}

// Failing is the check names at head that are red: our CI's checks, then
// the GitHub leg's runs as kind:name.
func (b PRBrief) Failing() []string {
	var out []string
	for _, r := range b.CI.Rows {
		if r.State == ci.SummaryRed {
			out = append(out, r.Check)
		}
	}
	for _, r := range b.CI.GH.Runs {
		if r.Word == ci.SummaryRed {
			out = append(out, r.Kind+":"+r.Name)
		}
	}
	return out
}

// PRBriefOptions are the verb's inputs.
type PRBriefOptions struct {
	Repo   string // owner/name or name
	N      string
	Issue  string // --issue: n, #n, name#n or owner/name#n; "" reads the card
	Mirror string // "" is DefaultMirror(name)
	GitHub *GitHub
}

// LoadPRBrief reads the brief: the PR record and its lines (one pipeline),
// the CI rollup at head (ci.ReadRows: the record and the GitHub leg, then the
// receipts), the card the record names (one HGETALL), the mirror numstat, and
// a REST read only for a section Redis has no copy of. err is a refusal (no
// record head) or a Redis failure; a section that cannot be read is a Gap.
func LoadPRBrief(ctx context.Context, c *redis.Client, o PRBriefOptions) (PRBrief, error) {
	owner, name, err := prkey.Split(o.Repo)
	if err != nil {
		return PRBrief{}, err
	}
	b := PRBrief{Owner: owner, Repo: name, N: o.N}
	key := Key(name, o.N)
	pipe := c.Pipeline()
	rec := pipe.HGetAll(ctx, key)
	lines := pipe.LRange(ctx, LinesKey(name, o.N), 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return b, fmt.Errorf("read %s: %w", key, err)
	}
	b.Record, b.Lines = rec.Val(), lines.Val()
	head := b.Record["head"]
	if head == "" {
		return b, fmt.Errorf("%w %s: no head; the record is written when the PR is opened (card harvest) or imported (nova-sprint pr record)", ErrMissing, key)
	}
	b.Sources = append(b.Sources, "record=redis:"+key)

	rows, err := ci.ReadRows(ctx, store.New(c), name, head)
	if err != nil {
		return b, err
	}
	b.CI = rows
	b.Sources = append(b.Sources, "ci=redis:"+rows.Key+",:gh")

	if task := strings.TrimSpace(b.Record["task"]); task != "" {
		b.CardKey = "task:" + task
		card, err := c.HGetAll(ctx, b.CardKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return b, fmt.Errorf("read %s: %w", b.CardKey, err)
		}
		if len(card) > 0 {
			b.Card = card
			b.Sources = append(b.Sources, "card=redis:"+b.CardKey)
		} else {
			b.Gaps = append(b.Gaps, "card: "+key+" names task="+task+" but "+b.CardKey+" is gone (a cleared sprint drops its cards)")
		}
	} else {
		b.Sources = append(b.Sources, "card=none ("+key+" names no task: not a card's PR)")
	}

	loadIssue(ctx, &b, o)
	loadPR(ctx, &b, o)
	loadFiles(ctx, &b, o.Mirror)
	b.Calls = o.GitHub.Calls()
	return b, nil
}

// loadIssue fills the issue: --issue, else the card's source, origin or
// ref. The card body is the issue's text when the card was pushed from it
// (source=issue); otherwise one REST read.
func loadIssue(ctx context.Context, b *PRBrief, o PRBriefOptions) {
	full := b.Owner + "/" + b.Repo
	named := ""
	switch {
	case strings.TrimSpace(o.Issue) != "":
		named = o.Issue
	case b.Card != nil:
		for _, f := range []string{"source", "origin", "ref"} {
			if _, _, _, ok := IssueRef(b.Card[f], full); ok {
				named = b.Card[f]
				break
			}
		}
	}
	owner, name, n, ok := IssueRef(named, full)
	if !ok {
		if named != "" {
			b.Gaps = append(b.Gaps, "issue: "+strconv.Quote(named)+" is not n, #n, name#n or owner/name#n")
			return
		}
		b.Sources = append(b.Sources, "issue=none (no --issue and the card names none)")
		return
	}
	b.IssueRef = owner + "/" + name + "#" + n
	if o.Issue == "" && b.Card != nil && b.Card["source"] == "issue" && strings.TrimSpace(b.Card["body"]) != "" {
		b.IssueTitle, b.IssueBody = b.Card["title"], b.Card["body"]
		b.Sources = append(b.Sources, "issue=redis:"+b.CardKey+" body (pushed from the issue, source=issue)")
		return
	}
	path := Endpoint("issues", owner, name, n)
	if o.GitHub == nil {
		b.Gaps = append(b.Gaps, "issue: Redis keeps no text for "+b.IssueRef+" (the card only names it) and --no-github forbids the one REST read GET "+path)
		return
	}
	t, body, err := o.GitHub.TitleBody(ctx, path)
	if err != nil {
		b.Gaps = append(b.Gaps, "issue: "+err.Error())
		return
	}
	b.IssueTitle, b.IssueBody = t, body
	b.Sources = append(b.Sources, "issue=rest:GET "+path+" (Redis has no copy)")
}

// loadPR fills the PR title and body: the record's pr_title and pr_body,
// else one REST read.
func loadPR(ctx context.Context, b *PRBrief, o PRBriefOptions) {
	if body := b.Record[FieldPRBody]; strings.TrimSpace(body) != "" {
		b.PRTitle, b.PRBody = b.Record[FieldPRTitle], body
		b.Sources = append(b.Sources, "pr_body=redis:"+Key(b.Repo, b.N)+" "+FieldPRBody)
		return
	}
	path := Endpoint("pulls", b.Owner, b.Repo, b.N)
	if o.GitHub == nil {
		b.Gaps = append(b.Gaps, "pr body: "+Key(b.Repo, b.N)+" has no "+FieldPRBody+" (harvest writes it; a hand PR has none) and --no-github forbids the one REST read GET "+path)
		return
	}
	t, body, err := o.GitHub.TitleBody(ctx, path)
	if err != nil {
		b.Gaps = append(b.Gaps, "pr body: "+err.Error())
		return
	}
	b.PRTitle, b.PRBody = t, body
	b.Sources = append(b.Sources, "pr_body=rest:GET "+path+" (Redis has no copy)")
}

// loadFiles fills the numstat from the mirror: base_sha..head, else the
// merge base of refs/heads/<base> and head (the diff GitHub shows).
func loadFiles(ctx context.Context, b *PRBrief, mirror string) {
	if mirror == "" {
		mirror = DefaultMirror(b.Repo)
	}
	b.Mirror = mirror
	head := b.Record["head"]
	if _, err := git(ctx, mirror, "rev-parse", "--git-dir"); err != nil {
		b.Gaps = append(b.Gaps, "files: mirror "+mirror+" is not a git directory; run mirror-refresh --once or pass --mirror")
		return
	}
	if _, err := git(ctx, mirror, "rev-parse", "--verify", "--quiet", head+"^{commit}"); err != nil {
		b.Gaps = append(b.Gaps, "files: mirror "+mirror+" has no commit "+head+" yet; wait one mirror-refresh tick (refs/pull/"+b.N+"/head)")
		return
	}
	base := strings.TrimSpace(b.Record["base_sha"])
	from := "base_sha"
	if base == "" {
		ref := strings.TrimSpace(b.Record["base"])
		if ref == "" {
			ref = "dev"
		}
		out, err := git(ctx, mirror, "merge-base", "refs/heads/"+ref, head)
		if err != nil {
			b.Gaps = append(b.Gaps, "files: the record has no base_sha and the mirror has no merge base of "+ref+" and "+short(head))
			return
		}
		base, from = strings.TrimSpace(out), "merge-base "+ref
	} else if _, err := git(ctx, mirror, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		b.Gaps = append(b.Gaps, "files: mirror "+mirror+" has no base_sha "+base+"; wait one mirror-refresh tick")
		return
	}
	out, err := git(ctx, mirror, "diff", "--numstat", "--no-renames", base+".."+head)
	if err != nil {
		b.Gaps = append(b.Gaps, "files: "+err.Error())
		return
	}
	b.DiffBase = base
	b.Files = ParseNumstat(out)
	names := make([]string, len(b.Files))
	for i, f := range b.Files {
		names[i] = f.Path
	}
	if p := b.Paths(); p != "" {
		b.Outside = OutsidePaths(p, names)
	}
	b.Sources = append(b.Sources, "files=mirror:"+mirror+" ("+from+")")
}

// RenderPR writes the brief, one section per heading, and returns nothing:
// the caller prints the receipt.
func RenderPR(w io.Writer, b PRBrief) {
	r := b.Record
	head := r["head"]
	fmt.Fprintf(w, "READ BRIEF %s/%s#%s head=%s base=%s base_sha=%s state=%s stream=%s task=%s\n",
		b.Owner, b.Repo, b.N, head, orDash(r["base"]), orDash(r["base_sha"]), orDash(r["state"]), orDash(r["stream"]), orDash(r["task"]))
	fmt.Fprintf(w, "SOURCES %s\n", strings.Join(b.Sources, " | "))

	section(w, "ISSUE "+orDash(b.IssueRef)+": "+orDash(b.IssueTitle))
	block(w, b.IssueBody)

	section(w, "CARD "+orDash(b.CardKey))
	if b.Card != nil {
		for _, kv := range [][2]string{{"TITLE", "title"}, {"KIND", "kind"}, {"ROUTE", "route"}, {"SOURCE", "source"},
			{"PATHS", "paths"}, {"DEPENDS-ON", "depends_on"}, {"BASE", "base"}, {"base-sha", "base_sha"}, {"DONE-WHEN", "done_when"}, {"TEST", "test"}} {
			if v := oneLineOf(b.Card[kv[1]]); v != "" {
				fmt.Fprintf(w, "%s: %s\n", kv[0], v)
			}
		}
		for _, l := range SpecLines(b.Card["body"], CardSpecKeys) {
			fmt.Fprintln(w, l)
		}
	} else {
		fmt.Fprintln(w, "-")
	}

	section(w, "DONE-WHEN")
	dw := 0
	for _, s := range [][2]string{{"issue", firstKey(b.IssueBody, "DONE-WHEN")}, {"card", oneLineOf(b.Card["done_when"])}, {"record", oneLineOf(r["done_when"])}} {
		if s[1] != "" {
			fmt.Fprintf(w, "%s: %s\n", s[0], s[1])
			dw++
		}
	}
	if dw == 0 {
		fmt.Fprintln(w, "- none in the issue, the card or the record: score against the issue's BUILD")
	}

	section(w, "PR #"+b.N+": "+orDash(b.PRTitle))
	block(w, b.PRBody)

	add, del := 0, 0
	for _, f := range b.Files {
		add += f.Add
		del += f.Del
	}
	scope := strconv.Itoa(len(b.Outside))
	if b.Paths() == "" {
		scope = "- (no PATHS on the record, the card or the issue)"
	}
	section(w, fmt.Sprintf("FILES %d (+%d -%d) %s..%s outside PATHS %s", len(b.Files), add, del, short(b.DiffBase), short(head), scope))
	if p := b.Paths(); p != "" {
		fmt.Fprintf(w, "PATHS: %s\n", oneLineOf(p))
	}
	for _, f := range b.Files {
		if f.Binary {
			fmt.Fprintf(w, "%6s %6s  %s\n", "bin", "bin", f.Path)
			continue
		}
		fmt.Fprintf(w, "%6s %6s  %s\n", "+"+strconv.Itoa(f.Add), "-"+strconv.Itoa(f.Del), f.Path)
	}
	for _, o := range b.Outside {
		fmt.Fprintf(w, "OUTSIDE PATHS: %s\n", o)
	}
	if b.DiffBase != "" {
		fmt.Fprintf(w, "patch: git -C %s diff %s..%s -- <file>\n", b.Mirror, b.DiffBase, head)
	}

	section(w, "CI at "+short(head))
	if b.CI.Found {
		f := b.CI.Fields
		fmt.Fprintf(w, "ours %s attempt=%s bench=%s final=%s\n", orDash(f["ci"]), orDash(f["attempt"]), orDash(f["bench"]), orDash(f["final"]))
		for _, row := range b.CI.Rows {
			if row.State == ci.SummaryPending {
				fmt.Fprintf(w, "  %s pending\n", row.Check)
				continue
			}
			fmt.Fprintf(w, "  %s %s rc=%s wall_ms=%s", row.Check, row.State, row.RC, orDash(row.WallMS))
			if row.Fail != "" {
				fmt.Fprintf(w, " fail=%s", oneLineOf(row.Fail))
			}
			if row.State == ci.SummaryRed && row.Log != "" {
				fmt.Fprintf(w, " log=%s", row.Log)
			}
			fmt.Fprintln(w)
		}
	} else {
		fmt.Fprintf(w, "ours none (%s MISSING: no CI request at this head)\n", b.CI.Key)
	}
	if g := b.CI.GH; g.Found {
		fmt.Fprintf(w, "github %s fail=%s\n", orDash(g.Word), orDash(g.Fail))
		for _, run := range g.Runs {
			fmt.Fprintf(w, "  %s:%s %s\n", run.Kind, run.Name, run.Word)
		}
	} else {
		fmt.Fprintln(w, "github none (no webhook event for this head yet)")
	}
	fmt.Fprintf(w, "FAILING: %s\n", orDash(strings.Join(b.Failing(), " ")))

	section(w, fmt.Sprintf("LINES %d", len(b.Lines)))
	for _, l := range b.Lines {
		fmt.Fprintf(w, "- %s\n", oneLineOf(l))
	}

	section(w, "RUBRIC")
	fmt.Fprintln(w, "gates: ci = every check above green at this head; base = BASE is dev or the stream branch and DEPENDS-ON is merged; scope = OUTSIDE PATHS none. A gate failure caps the score at 7.")
	fmt.Fprintln(w, "score against DONE-WHEN with evidence at head (file:line or the failing check); the standard's test rules hold (unit tests never wait on wall clock, class tests under 2 s, redis in functional tests); only tens land on product code; a score under 10 names the work to 10.")
	fmt.Fprintf(w, "post: nova-sprint read post --repo %s --n %s --line \"SCORE who=<you> head=%s score=N/10 gates=ci:ok,base:ok,scope:ok ...\"\n", b.Repo, b.N, head)

	for _, g := range b.Gaps {
		fmt.Fprintf(w, "READ BRIEF GAP %s/%s#%s %s\n", b.Owner, b.Repo, b.N, g)
	}
}

func section(w io.Writer, title string) { fmt.Fprintf(w, "\n== %s\n", title) }

// block prints text as it is, "-" when empty, ending with one newline.
func block(w io.Writer, text string) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n ")
	if strings.TrimSpace(text) == "" {
		text = "-"
	}
	fmt.Fprintln(w, text)
}

func oneLineOf(s string) string { return strings.Join(strings.Fields(s), " ") }

// firstKey is the value of the first KEY: line of text, "" when none.
func firstKey(text, key string) string {
	if l := SpecLines(text, []string{key}); len(l) > 0 {
		return strings.TrimPrefix(l[0], key+": ")
	}
	return ""
}

// BriefPR is the verb: load, render, one receipt. Exit 0 complete, 1 with
// gaps (each printed) or refused, 2 could not run.
func BriefPR(ctx context.Context, c *redis.Client, o PRBriefOptions, stdout, stderr io.Writer) int {
	b, err := LoadPRBrief(ctx, c, o)
	if err != nil {
		if errors.Is(err, ErrMissing) {
			fmt.Fprintf(stderr, "READ BRIEF REFUSED repo=%s n=%s why=%v\n", o.Repo, o.N, err)
			return 1
		}
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	RenderPR(stdout, b)
	word := "DONE"
	if len(b.Gaps) > 0 {
		word = "INCOMPLETE"
	}
	fmt.Fprintf(stdout, "\nREAD BRIEF %s %s/%s#%s head=%s files=%d outside_paths=%d failing=%d lines=%d gaps=%d github_calls=%d\n",
		word, b.Owner, b.Repo, b.N, short(b.Record["head"]), len(b.Files), len(b.Outside), len(b.Failing()), len(b.Lines), len(b.Gaps), b.Calls)
	if len(b.Gaps) > 0 {
		return 1
	}
	return 0
}
