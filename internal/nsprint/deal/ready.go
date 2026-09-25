package deal

// ready.go is the dealer's DEPENDS-ON gate (nova-tools #3066, SPEC-CARD
// clause 10, #2756 3.2 `card release`).
//
// THE HURT (2026-09-23). Six of Rowan's build children closed blocked because
// their DEPENDS-ON named tasks that were closed while their PRs (#3011, #2959,
// #3006...) were not on dev, and #3053 and #3060 were built stacked on
// unmerged PRs. The dealer handed out work whose foundation was not there:
// the child was cut against code that was not in its base, and its PR could
// not merge.
//
// THE RULE. A queued card is READY only when every DEPENDS-ON entry is
// LANDED on the card's base:
//
//   - `-`, `none` or an empty field: waits for nothing;
//   - a card id (a label in the same sprint): the card is `landed`
//     (pr-to-read's merge commit on dev), or its PR is merged into the
//     dependent card's base, read from its PR record; or the card ended DONE with no PR
//     and no pushed commit (closed without a PR: nothing to land);
//   - `<owner>/<repo>#<n>`: the PR is merged into the dependent card's base,
//     or, when n is an issue, the issue is closed (its task landed); both
//     read from Redis (Records), never the forge.
//
// OK, reviewed, verified or an open PR is not landed. A PR closed without
// merging, a card cancelled or superseded unlanded, and a card id the sprint
// does not have can no longer land: they are reported by name on every pass,
// never silently passed over. A question the records cannot answer is
// unknown and the card waits: no evidence is not negative evidence, and it is
// not positive evidence either. A merged PR whose REST base is empty, or
// whose dependent card names no BASE, is unknown for the same reason: fail
// closed on an unresolved base rather than release the card on a match that
// was never checked (Stella's HOLD2 on #3080).
//
// THE MOVE. The pass reads the pool and the waiting set; a pooled card that is
// not ready moves to `s:<S>:waiting`, a waiting card that became ready moves
// back to the pool at its score, both in ONE fenced ns_card_gate call per
// sprint. ns_card_deal deals only pooled cards, so a waiting card cannot be
// dealt even by a stale plan. The pool the table counts is therefore the
// ready antichain.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
)

// DepCard is what the gate needs of a card another card depends on: its
// `s:<S>:card:<label>` fields. Found is false when the sprint has no such card.
type DepCard struct {
	Found     bool
	State     string
	Outcome   string
	Repo      string
	Base      string
	PR        int
	PushedSHA string
}

// Ref is the answer for one number: a PR (IsPR) with its merge state
// and base, or an issue with its state.
type Ref struct {
	IsPR   bool
	Merged bool
	State  string // open or closed
	Base   string // the PR's base branch
}

// PRs is the dealer's one seam for the state of <repo>#<n>. Records is the
// real one and reads Redis only (#3967); tests hand in a map.
type PRs interface {
	Ref(ctx context.Context, repo string, n int) (Ref, error)
}

// Gate writes the gate's moves for one sprint in one fenced call:
// pool -> waiting for a card that is not ready (why names the entry), waiting
// -> pool for one that is, and the why of a card that stays waiting when it
// changed. It returns ErrFenced on a stale token.
type Gate interface {
	Gate(ctx context.Context, fence, sprint string, moves []GateMove) error
}

// Gate move verbs.
const (
	GateWait    = "wait"
	GateRelease = "release"
)

// GateMove is one card's gate outcome in a pass.
type GateMove struct {
	Label string
	Verb  string // GateWait or GateRelease
	Why   string // empty on a release
}

// Blocked is a card that is not dealt this pass and why: the entry, and what
// the records or the sprint say of it.
type Blocked struct {
	Sprint string
	Label  string
	Why    string
}

// noDeps reports whether a DEPENDS-ON list waits for nothing.
func noDeps(deps []string) bool {
	for _, d := range deps {
		if d != "" && d != "-" && d != "none" {
			return false
		}
	}
	return true
}

// ParseRef splits `<owner>/<repo>#<n>`; ok is false for anything else, which
// the gate reads as a card id.
func ParseRef(entry string) (repo string, n int, ok bool) {
	return parseRef(entry)
}

func parseRef(entry string) (repo string, n int, ok bool) {
	i := strings.LastIndexByte(entry, '#')
	if i <= 0 || !strings.Contains(entry[:i], "/") {
		return "", 0, false
	}
	n, err := strconv.Atoi(entry[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return entry[:i], n, true
}

// DepEntry is one DEPENDS-ON entry in the same shape the dealer's gate uses.
// A repository reference has Repo and N; every other spelling is a card Label.
type DepEntry struct {
	Label string
	Repo  string
	N     int
}

// DepEntries parses the card's stored depends_on value with the dealer's one
// parser. Classification passes these shapes to Redis; Redis reads the actual
// dependency evidence atomically with its classification write.
func DepEntries(dependsOn string) []DepEntry {
	var out []DepEntry
	for _, entry := range splitDeps(dependsOn) {
		if entry == "" || entry == "-" || entry == "none" {
			continue
		}
		if repo, n, ok := parseRef(entry); ok {
			out = append(out, DepEntry{Repo: repo, N: n})
		} else {
			out = append(out, DepEntry{Label: entry})
		}
	}
	return out
}

type refKey struct {
	repo string
	n    int
}

type refAnswer struct {
	ref Ref
	err error
}

// resolver asks the records each <repo>#<n> once per pass.
type resolver struct {
	prs   PRs
	cache map[refKey]refAnswer
}

func (r *resolver) ref(ctx context.Context, repo string, n int) (Ref, error) {
	k := refKey{repo, n}
	if a, ok := r.cache[k]; ok {
		return a.ref, a.err
	}
	var a refAnswer
	if r.prs == nil {
		a.err = errors.New("no records seam")
	} else {
		a.ref, a.err = r.prs.Ref(ctx, repo, n)
	}
	r.cache[k] = a
	return a.ref, a.err
}

// LandedPR says whether a PR answer is landed on base; why is empty when it is.
func LandedPR(ref Ref, err error, name, base string) string {
	return landedPR(ref, err, name, base)
}

// landedPR says whether a PR answer is landed on base; why is empty when it is.
func landedPR(ref Ref, err error, name, base string) string {
	switch {
	case err != nil:
		return name + " unknown: " + oneLine(err.Error())
	case !ref.IsPR:
		if ref.State == "closed" {
			return ""
		}
		return name + " open"
	case ref.Merged && (base == "" || ref.Base == ""):
		return name + " unknown: base-unresolved"
	case ref.Merged && ref.Base != base:
		return name + " merged into " + ref.Base + ", not " + base
	case ref.Merged:
		return ""
	case ref.State == "closed":
		return name + " can no longer land: closed without merge"
	}
	return name + " open"
}

// entryWhy is empty when the entry is landed for card c, else why it is not.
func (r *resolver) entryWhy(ctx context.Context, in Input, c Card, entry string) string {
	if repo, n, ok := parseRef(entry); ok {
		ref, err := r.ref(ctx, repo, n)
		return landedPR(ref, err, entry, c.Base)
	}
	d := in.Deps[c.Sprint+"/"+entry]
	if !d.Found {
		return entry + " can no longer land: no such card in sprint " + c.Sprint
	}
	switch {
	case d.State == "landed":
		return ""
	case d.PR > 0:
		repo := d.Repo
		if repo == "" {
			repo = c.Repo
		}
		if repo == "" {
			return fmt.Sprintf("%s unknown: PR #%d has no repo", entry, d.PR)
		}
		name := fmt.Sprintf("%s (%s#%d)", entry, repo, d.PR)
		ref, err := r.ref(ctx, repo, d.PR)
		if err == nil && !ref.IsPR {
			// The card names a PR, so only a PR answer can release it: a 404 on
			// pulls/<n> that fell back to a closed issue is not a merge.
			return name + " unknown: not a PR (issue " + ref.State + ")"
		}
		return landedPR(ref, err, name, c.Base)
	case d.State == "ended" && d.Outcome == "DONE" && d.PushedSHA == "":
		// Closed with no PR and no commit: there is nothing to land.
		return ""
	case d.State == "cancelled" || d.State == "superseded":
		return entry + " can no longer land: card " + d.State + " without a PR"
	}
	state := d.State
	if state == "" {
		state = "unknown"
	}
	return entry + " not landed: card " + state + ", no PR"
}

// why is empty when every DEPENDS-ON entry of c is landed; else the entries
// that are not, joined by "; ".
func (r *resolver) why(ctx context.Context, in Input, c Card) string {
	if noDeps(c.DependsOn) {
		return ""
	}
	var out []string
	for _, e := range c.DependsOn {
		if e == "" || e == "-" || e == "none" {
			continue
		}
		if w := r.entryWhy(ctx, in, c, e); w != "" {
			out = append(out, w)
		}
	}
	return strings.Join(out, "; ")
}

// Ready applies the DEPENDS-ON gate to one Input. It returns the Input whose
// pools hold only ready cards (the waiting cards that became ready among
// them), the moves each sprint's Gate call must write, and every card that is
// not dealt this pass with why. Ready never writes.
func Ready(ctx context.Context, in Input, prs PRs) (Input, map[string][]GateMove, []Blocked) {
	r := &resolver{prs: prs, cache: map[refKey]refAnswer{}}
	moves := map[string][]GateMove{}
	var blocked []Blocked
	out := in
	out.Sprints = make([]Sprint, len(in.Sprints))
	for i, s := range in.Sprints {
		ns := s
		ns.Pool, ns.Waiting = nil, nil
		for _, c := range s.Pool {
			if w := r.why(ctx, in, c); w != "" {
				moves[s.Name] = append(moves[s.Name], GateMove{Label: c.Label, Verb: GateWait, Why: w})
				blocked = append(blocked, Blocked{Sprint: s.Name, Label: c.Label, Why: w})
				ns.Waiting = append(ns.Waiting, c)
				continue
			}
			ns.Pool = append(ns.Pool, c)
		}
		for _, c := range s.Waiting {
			w := r.why(ctx, in, c)
			if w == "" {
				moves[s.Name] = append(moves[s.Name], GateMove{Label: c.Label, Verb: GateRelease})
				ns.Pool = append(ns.Pool, c)
				continue
			}
			if w != c.WaitWhy {
				moves[s.Name] = append(moves[s.Name], GateMove{Label: c.Label, Verb: GateWait, Why: w})
			}
			blocked = append(blocked, Blocked{Sprint: s.Name, Label: c.Label, Why: w})
			ns.Waiting = append(ns.Waiting, c)
		}
		out.Sprints[i] = ns
	}
	return out, moves, blocked
}

// Records is the dealer's PRs seam (nova-tools#3967, THE BOUNDARY): every
// answer is read from Redis, never the forge. A PR is its record pr:<name>:<n>
// (prkey.Key; written by harvest when it opens the PR, moved to landed or
// merged by the lander, base from harvest): merged when its state is landed
// or merged or merged_at is set, closed when its state is closed, else open.
// An issue is the task that imported it (TaskRefKey, written by task push
// --issue): closed when that task is landed or done/ok, else open. A number
// Redis has no record of is unknown and the card waits: import it (task push
// --issue) or let harvest record the PR; the dealer never asks GitHub.
type Records struct {
	C redis.Cmdable
}

// TaskRefKey is the one-card-per-issue index ns_tcard_push writes for an
// import: taskref:<owner/repo#n> names the task holding the issue.
func TaskRefKey(ref string) string { return "taskref:" + ref }

// Ref implements PRs from the records: one pipeline, two reads, and at most
// one more for the importing task's where.
func (r Records) Ref(ctx context.Context, repo string, n int) (Ref, error) {
	if r.C == nil {
		return Ref{}, errors.New("no Redis for the records")
	}
	full, err := prkey.Full(repo)
	if err != nil {
		return Ref{}, err
	}
	pipe := r.C.Pipeline()
	pr := pipe.HGetAll(ctx, prkey.Key(full, n))
	task := pipe.Get(ctx, TaskRefKey(fmt.Sprintf("%s#%d", full, n)))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Ref{}, err
	}
	if rec := pr.Val(); len(rec) > 0 {
		out := Ref{IsPR: true, State: "open", Base: rec["base"]}
		switch rec["state"] {
		case "landed", "merged":
			out.Merged, out.State = true, "closed"
		case "closed":
			out.State = "closed"
		}
		if rec["merged_at"] != "" {
			out.Merged, out.State = true, "closed"
		}
		return out, nil
	}
	if id := task.Val(); id != "" {
		f, err := r.C.HMGet(ctx, "task:"+id, "where", "where_ok").Result()
		if err != nil {
			return Ref{}, err
		}
		where, _ := f[0].(string)
		ok, _ := f[1].(string)
		if where == "landed" || where == "done" && ok == "ok" {
			return Ref{State: "closed"}, nil
		}
		return Ref{State: "open"}, nil
	}
	return Ref{}, fmt.Errorf("no record of %s#%d in Redis (import the issue with task push --issue; harvest records a PR)", full, n)
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
