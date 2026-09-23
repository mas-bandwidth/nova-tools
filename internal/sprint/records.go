package sprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// DONE FLIPS ONLY FROM A PRIMARY RECORD. Not from a bus note, not from a friend saying so,
// not from me (Glenn, 2026-09-22, and the ruling before it): an issue closed, a pull request
// merged, a typed line at head, a card landed. Everything else is chat. This file is the two
// seams that reach those records -- GitHub, and the card event stream -- and nothing else in
// the package may set State to closed.
//
// The GitHub half is real here. The ev:cards half is an interface with a fake, because the
// stream and its fold are nova-tools #2587, still in review: when it lands, its Store drops
// in behind Cards with no change above this line. A fake is STRICT LIKE THE REAL TOOL
// (AGENTS.md), so the fake here refuses a label the real one would refuse.

// Record is what a primary record says about one task.
type Record struct {
	Closed   bool
	Evidence string // the merge sha, the landed sha, the typed line id, `issue closed`
	At       time.Time
}

// Records is the GitHub half: issues, pull requests and the typed line at head.
type Records interface {
	Look(ctx context.Context, t Task) (Record, error)
}

// Cards is the ev:cards half: a card landed. #2587's stream is the implementation; a fake
// stands in until it lands.
type Cards interface {
	Landed(ctx context.Context, label string) (Record, error)
}

// Ref is a parsed task ref: `owner/name#123`, optionally `@<sha>` for "at this head".
type Ref struct {
	Repo   string
	Number int
	Head   string
}

// ParseRef reads a ref, or says what the ref WANTS. A task whose ref does not parse is not
// a task whose state anyone can prove, so it is a refusal rather than a skipped line.
func ParseRef(ref string) (Ref, error) {
	ref = strings.TrimSpace(ref)
	at := ""
	if i := strings.LastIndex(ref, "@"); i > 0 {
		at = strings.ToLower(ref[i+1:])
		ref = ref[:i]
	}
	i := strings.LastIndex(ref, "#")
	if i <= 0 || i+1 >= len(ref) {
		return Ref{}, fmt.Errorf("ref %q wants owner/name#<number>[@<sha>], the shape a record can be looked up by", ref)
	}
	n, err := strconv.Atoi(strings.TrimSpace(ref[i+1:]))
	if err != nil || n <= 0 {
		return Ref{}, fmt.Errorf("ref %q wants a whole issue or pull request number after #", ref)
	}
	repo := strings.TrimSpace(ref[:i])
	if !strings.Contains(repo, "/") {
		return Ref{}, fmt.Errorf("ref %q wants owner/name before #", ref)
	}
	return Ref{Repo: repo, Number: n, Head: at}, nil
}

// GH is the GitHub half over the `gh` command. It shells out rather than speaking the API
// itself because gh already holds the identity the coordinator runs as, and a second auth
// path is a second thing to get wrong.
type GH struct {
	Path      string // the gh binary; "gh" when empty
	ConfigDir string // GH_CONFIG_DIR, so a seat's own config is used and never a default
	Timeout   time.Duration
	// Friends are the names whose typed line counts. A line by someone outside this list
	// is not a friend read (two independent friends; cold reads are evidence, not a read).
	Friends []string
	// run is the seam a test replaces; it takes gh's arguments and returns its stdout.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// NewGH returns the GitHub records reader.
func NewGH(path, configDir string, friends []string, timeout time.Duration) *GH {
	g := &GH{Path: path, ConfigDir: configDir, Friends: friends, Timeout: timeout}
	if g.Path == "" {
		g.Path = "gh"
	}
	if g.Timeout <= 0 {
		g.Timeout = 30 * time.Second
	}
	g.run = g.exec
	return g
}

func (g *GH) exec(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.Path, args...)
	if g.ConfigDir != "" {
		cmd.Env = append(cmd.Environ(), "GH_CONFIG_DIR="+g.ConfigDir)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", g.Path, strings.Join(args, " "), err)
	}
	return out, nil
}

type ghIssue struct {
	State    string `json:"state"`
	ClosedAt string `json:"closedAt"`
}

type ghPR struct {
	State       string `json:"state"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	HeadRefOID string `json:"headRefOid"`
	Comments   []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
		URL       string `json:"url"`
	} `json:"comments"`
}

// Look asks GitHub what it knows about the task's ref. A `read` task is closed by a typed
// line at head by a friend; everything else by the merge or the close.
func (g *GH) Look(ctx context.Context, t Task) (Record, error) {
	r, err := ParseRef(t.Ref)
	if err != nil {
		return Record{}, err
	}
	if t.Kind == KindRead || t.Kind == KindReview {
		return g.typedLine(ctx, r, t)
	}
	// A pull request first: `gh issue view` on a pull request number is a refusal, and a
	// task whose ref is a PR is the common case on a fixes day.
	if rec, ok, err := g.pr(ctx, r); err == nil && ok {
		return rec, nil
	}
	out, err := g.run(ctx, "issue", "view", strconv.Itoa(r.Number), "-R", r.Repo, "--json", "state,closedAt")
	if err != nil {
		return Record{}, err
	}
	var issue ghIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return Record{}, fmt.Errorf("gh issue view %s#%d: %w", r.Repo, r.Number, err)
	}
	if !strings.EqualFold(issue.State, "CLOSED") {
		return Record{}, nil
	}
	return Record{Closed: true, Evidence: fmt.Sprintf("%s#%d issue closed", r.Repo, r.Number), At: parseTime(issue.ClosedAt)}, nil
}

func (g *GH) pr(ctx context.Context, r Ref) (Record, bool, error) {
	out, err := g.run(ctx, "pr", "view", strconv.Itoa(r.Number), "-R", r.Repo, "--json", "state,mergedAt,mergeCommit")
	if err != nil {
		return Record{}, false, err
	}
	var pr ghPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return Record{}, false, err
	}
	if !strings.EqualFold(pr.State, "MERGED") {
		return Record{}, true, nil
	}
	sha := pr.MergeCommit.OID
	if sha == "" {
		sha = "merged"
	}
	return Record{Closed: true, Evidence: fmt.Sprintf("%s#%d merged %s", r.Repo, r.Number, short(sha)), At: parseTime(pr.MergedAt)}, true, nil
}

// typedLine closes a read when a FRIEND has typed a DISPOSITION line bound to the CURRENT
// head. A line at an older head is carried, not a read of what is there now, and a line by
// a login outside the friends list is not a friend read at all.
func (g *GH) typedLine(ctx context.Context, r Ref, t Task) (Record, error) {
	out, err := g.run(ctx, "pr", "view", strconv.Itoa(r.Number), "-R", r.Repo, "--json", "headRefOid,comments")
	if err != nil {
		return Record{}, err
	}
	var pr ghPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return Record{}, err
	}
	head := strings.ToLower(pr.HeadRefOID)
	if r.Head != "" {
		head = r.Head
	}
	for _, c := range pr.Comments {
		body := merge.StripQuotedAndCode(c.Body)
		for _, line := range strings.Split(body, "\n") {
			who, at, verdict, _, ok := merge.ParseDispositionLine(line)
			if !ok || verdict == "" {
				continue
			}
			if head != "" && strings.ToLower(at) != head {
				continue // carried from an older head: no evidence about this one
			}
			if !g.isFriend(who) && !g.isFriend(c.Author.Login) {
				continue
			}
			return Record{
				Closed:   true,
				Evidence: fmt.Sprintf("%s#%d %s by %s at %s", r.Repo, r.Number, verdict, firstNonEmpty(who, c.Author.Login), short(head)),
				At:       parseTime(c.CreatedAt),
			}, nil
		}
	}
	return Record{}, nil
}

func (g *GH) isFriend(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, f := range g.Friends {
		if strings.EqualFold(strings.TrimSpace(f), name) {
			return true
		}
	}
	return len(g.Friends) == 0
}

// FakeCards is the ev:cards half until #2587 lands: a map from card label to the landed
// record. It is strict like the real stream -- an empty label is a refusal there and here.
type FakeCards struct {
	Landings map[string]Record
}

// Landed answers what the stream would answer.
func (f *FakeCards) Landed(ctx context.Context, label string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(label) == "" {
		return Record{}, fmt.Errorf("label is required; it is the id the whole card stream joins on")
	}
	return f.Landings[label], nil
}

// Flip is the only way a task's state becomes closed: it asks the primary records and
// returns the tasks it changed. A task whose ref names a card (kind card with a label ref)
// is asked of the card stream; everything else of GitHub. A record that says nothing leaves
// the task exactly as it was -- no evidence is not negative evidence.
func Flip(ctx context.Context, tasks []Task, recs Records, cards Cards, now time.Time) ([]Task, []error) {
	var changed []Task
	var problems []error
	for _, t := range tasks {
		if t.State == StateClosed {
			continue
		}
		var (
			rec Record
			err error
		)
		switch {
		case t.Kind == KindCard && cards != nil && !strings.Contains(t.Ref, "#"):
			rec, err = cards.Landed(ctx, t.Ref)
		case recs != nil:
			rec, err = recs.Look(ctx, t)
		default:
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("task %s (%s): %w", t.ID, t.Ref, err))
			continue
		}
		if !rec.Closed {
			continue
		}
		t.State = StateClosed
		t.Evidence = rec.Evidence
		t.DoneAt = rec.At
		if t.DoneAt.IsZero() {
			t.DoneAt = now
		}
		if !t.LeasedAt.IsZero() && t.DoneAt.After(t.LeasedAt) {
			t.Actual = int(t.DoneAt.Sub(t.LeasedAt).Minutes())
		}
		changed = append(changed, t)
	}
	return changed, problems
}

func parseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
