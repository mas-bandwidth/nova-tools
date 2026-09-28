package line

// The typed-line store (nova-tools #3595): a typed line is a Redis record
// keyed by PR, head, who and kind, written by one call into the nova_sprint
// library (ns_line_post in internal/nsprint/fn/lua/line.lua) and read by one
// read-only call (ns_line_list). The record carries the gates measured from
// Redis (ci from ci:<name>:<head>, base from the PR record) and from the
// caller's mirror diff (scope against the record's PATHS); a typed gate that
// disagrees with a measured one is refused, so a line's gates are facts,
// not the reader's claim.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// Function names registered by internal/nsprint/fn/lua/line.lua.
const (
	FunctionPost = "ns_line_post"
	FunctionList = "ns_line_list"
)

// Kinds are the typed lines; the first word of the line.
var Kinds = []string{"SCORE", "HOLD", "REPAIR", "SPEC", "SPEC-WRITTEN", "CLOSE", "JEV-DIFF"}

// ErrMalformed wraps every parse refusal.
var ErrMalformed = errors.New("malformed typed line")

var (
	whoRx   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	headRx  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	scoreRx = regexp.MustCompile(`^(10|[0-9])(/10)?$`)
	gatesRx = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[a-z0-9_-]+(,[a-z][a-z0-9_-]*:[a-z0-9_-]+)*$`)
	nRx     = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
)

// Line is one parsed typed line. Score is -1 when the line has none.
type Line struct {
	Kind, Who, Head string
	Score           int
	Gates           string
	Text            string
}

// Parse reads the first line of text as <KIND> who=<w> head=<sha> [score=N/10]
// [gates=<g>:<v>,...] ...; everything after it is the body. It refuses a line
// with an unknown kind, no or a malformed who or head, a SCORE without a
// score, a score outside 0..10, or a gates field that is not name:word pairs.
func Parse(text string) (Line, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	first, _, _ := strings.Cut(text, "\n")
	tok := strings.Fields(first)
	if len(tok) == 0 {
		return Line{}, fmt.Errorf("%w: empty; want <KIND> who=<name> head=<sha> ...", ErrMalformed)
	}
	l := Line{Kind: tok[0], Score: -1, Text: text}
	if !known(l.Kind) {
		return Line{}, fmt.Errorf("%w: first word %q is not one of %s", ErrMalformed, l.Kind, strings.Join(Kinds, "|"))
	}
	kv := map[string]string{}
	for _, w := range tok[1:] {
		k, v, ok := strings.Cut(w, "=")
		if !ok || kv[k] != "" {
			continue
		}
		kv[k] = strings.TrimRight(v, ":;")
	}
	l.Who, l.Head = strings.ToLower(kv["who"]), strings.ToLower(kv["head"])
	if !whoRx.MatchString(l.Who) {
		return Line{}, fmt.Errorf("%w: who=%q, want who=<name> ([a-z0-9._-])", ErrMalformed, kv["who"])
	}
	if !headRx.MatchString(l.Head) {
		return Line{}, fmt.Errorf("%w: head=%q, want head=<sha> (7 to 40 hex)", ErrMalformed, kv["head"])
	}
	if s, ok := kv["score"]; ok {
		if !scoreRx.MatchString(s) {
			return Line{}, fmt.Errorf("%w: score=%q, want score=N/10 with N in 0..10", ErrMalformed, s)
		}
		l.Score, _ = strconv.Atoi(strings.TrimSuffix(s, "/10"))
	} else if l.Kind == "SCORE" {
		return Line{}, fmt.Errorf("%w: a SCORE line needs score=N/10", ErrMalformed)
	}
	if g, ok := kv["gates"]; ok {
		g = strings.ToLower(strings.TrimRight(g, ",:;"))
		if !gatesRx.MatchString(g) {
			return Line{}, fmt.Errorf("%w: gates=%q, want gates=<name>:<word>,... (ci:ok,base:ok,scope:ok)", ErrMalformed, kv["gates"])
		}
		l.Gates = g
	}
	return l, nil
}

func known(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Build is the typed line from flags: the header, then the body on the lines
// after it. score < 0 leaves score out.
func Build(kind, who, head string, score int, gates, body string) string {
	s := kind + " who=" + who + " head=" + head
	if score >= 0 {
		s += " score=" + strconv.Itoa(score) + "/10"
	}
	if gates != "" {
		s += " gates=" + gates
	}
	if body = strings.TrimSpace(body); body != "" {
		s += "\n" + body
	}
	return s
}

// Scope is the scope gate as the caller measured it from the mirror: Word is
// ok, no, or "" (unmeasured); Outside names the files outside PATHS.
type Scope struct {
	Word    string
	Outside []string
}

// Posted is the store's reply to a post.
type Posted struct {
	Status   string   // POSTED, EXISTS, MISSING or REFUSED
	Key      string   // the line record (POSTED, EXISTS) or the PR record (MISSING)
	Head     string   // the full head the line is keyed at
	Gates    string   // the gates stored: measured where measured, else as typed
	Measured string   // the gate names measured
	Lines    int      // lines in the PR's log after the post
	Why      string   // REFUSED
	Moved    int      // tasks the line moved (a posted CLOSE or SCORE, nova-tools #3779)
	Cut      int      // copies the SCORE cut: a fix copy, fresh reads (#4094)
	Same     int      // tasks already where the event puts them
	Skipped  []string // "<id>: <why>" per task the move refused
	// Plans are the plans a CLOSE line's landing landed with their stitch
	// (nova-tools#4317), each "parent=<id> ref=<repo#n|-> origin=<url|->",
	// from the reply's "PLAN ..." notes (TE.plan_note).
	Plans []string
	Line  Line
}

// LineKey is one line record: pr:<name>:<n>:line:<head>:<who>:<kind>.
func LineKey(repo, n, head, who, kind string) string {
	return HeadKey(repo, n, head) + ":" + who + ":" + kind
}

// HeadKey is the zset of <who>:<kind> at one head.
func HeadKey(repo, n, head string) string { return prkey.KeyText(repo, n) + ":line:" + head }

// Post parses text and stores it through ns_line_post: one call.
func Post(ctx context.Context, c redis.Cmdable, repo, n, text string, scope Scope) (Posted, error) {
	return post(ctx, c, "post", repo, n, text, scope, "")
}

func post(ctx context.Context, c redis.Cmdable, mode, repo, n, text string, scope Scope, source string) (Posted, error) {
	l, err := Parse(text)
	if err != nil {
		return Posted{Status: "REFUSED", Why: err.Error()}, err
	}
	_, name, err := prkey.Split(repo)
	if err != nil || !nRx.MatchString(n) {
		return Posted{}, fmt.Errorf("line post: want a repo name and a PR number, got %q %q", repo, n)
	}
	score := ""
	if l.Score >= 0 {
		score = strconv.Itoa(l.Score)
	}
	res, err := c.FCall(ctx, FunctionPost, nil, mode, name, n, l.Head, l.Who, l.Kind, score, l.Gates,
		scope.Word, strings.Join(scope.Outside, " "), l.Text, source).StringSlice()
	if err != nil {
		return Posted{}, fmt.Errorf("%s: %w", FunctionPost, err)
	}
	p := Posted{Line: l}
	if len(res) > 0 {
		p.Status = res[0]
	}
	switch {
	case p.Status == "POSTED" && len(res) >= 9:
		p.Key, p.Head, p.Gates, p.Measured = res[1], res[2], res[3], res[4]
		p.Lines, _ = strconv.Atoi(res[5])
		p.Moved, _ = strconv.Atoi(res[6])
		p.Same, _ = strconv.Atoi(res[7])
		p.Cut, _ = strconv.Atoi(res[9])
		for _, note := range res[10:] {
			if plan, ok := strings.CutPrefix(note, "PLAN "); ok {
				p.Plans = append(p.Plans, plan)
				continue
			}
			p.Skipped = append(p.Skipped, note)
		}
	case p.Status == "REFUSED" && len(res) == 2:
		p.Why = res[1]
	case (p.Status == "MISSING" || p.Status == "EXISTS") && len(res) == 2:
		p.Key = res[1]
	default:
		return p, fmt.Errorf("%s: unexpected reply %q", FunctionPost, res)
	}
	return p, nil
}

// List is every line record at head ("" is the record's head; a prefix
// expands), oldest first, through ns_line_list: one read-only call. It
// returns the full head and the records, each a field map.
func List(ctx context.Context, c redis.Cmdable, repo, n, head string) (string, []map[string]string, error) {
	_, name, err := prkey.Split(repo)
	if err != nil || !nRx.MatchString(n) {
		return "", nil, fmt.Errorf("line list: want a repo name and a PR number, got %q %q", repo, n)
	}
	v, err := c.FCallRO(ctx, FunctionList, nil, name, n, strings.ToLower(head)).Slice()
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", FunctionList, err)
	}
	if len(v) < 2 {
		return "", nil, fmt.Errorf("%s: unexpected reply %v", FunctionList, v)
	}
	if fmt.Sprint(v[0]) == "MISSING" {
		return "", nil, fmt.Errorf("MISSING %v: no head; the record is written when the PR is opened or imported", v[1])
	}
	full := fmt.Sprint(v[1])
	var out []map[string]string
	for _, item := range v[2:] {
		flat, _ := item.([]any)
		m := map[string]string{}
		for i := 0; i+1 < len(flat); i += 2 {
			m[fmt.Sprint(flat[i])] = fmt.Sprint(flat[i+1])
		}
		out = append(out, m)
	}
	return full, out, nil
}

// Comment is one GitHub issue comment as `gh api .../issues/<n>/comments`
// prints it; only the fields the importer reads.
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// ParseComments reads a comments array (the REST reply, or several pages of
// it concatenated by `gh api --paginate`).
func ParseComments(r io.Reader) ([]Comment, error) {
	dec := json.NewDecoder(r)
	var all []Comment
	for {
		var page []Comment
		if err := dec.Decode(&page); errors.Is(err, io.EOF) {
			return all, nil
		} else if err != nil {
			return nil, fmt.Errorf("comments: %w", err)
		}
		all = append(all, page...)
	}
}

// Imported counts one import.
type Imported struct {
	Comments, Typed, Imported, Existed, Missing int
}

// Import copies the typed lines in comments into the store: a comment whose
// first line is a typed line becomes a record with source comment:<id>; prose
// is skipped. An import never refuses on a gate (the line is history) and is
// idempotent: a comment already imported is EXISTS. No GitHub call: the
// caller hands the comments in.
func Import(ctx context.Context, c redis.Cmdable, repo, n string, comments []Comment) (Imported, error) {
	var res Imported
	for _, cm := range comments {
		res.Comments++
		if _, err := Parse(cm.Body); err != nil {
			continue
		}
		res.Typed++
		p, err := post(ctx, c, "import", repo, n, cm.Body, Scope{}, "comment:"+strconv.FormatInt(cm.ID, 10))
		if err != nil {
			return res, err
		}
		switch p.Status {
		case "POSTED":
			res.Imported++
		case "EXISTS":
			res.Existed++
		case "MISSING":
			res.Missing++
		}
	}
	return res, nil
}
