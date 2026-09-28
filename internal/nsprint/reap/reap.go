// Package reap closes stale superseded sprint PRs (nova-tools#3156): nova-sprint
// pr reap. It decides from the Redis records alone and GitHub's close is its
// one outward write, budgeted per run; no GitHub read feeds a decision.
//
// The candidates are the sprint's card PRs (s:<S>:prcard, written by the
// harvest). One read-only call, ns_pr_reap_read (internal/nsprint/fn/lua/reap.lua),
// returns each PR record with its card's origin issue and the work tasks
// naming the PR or that issue. A PR is reaped when it is still open on the
// record and one rule holds:
//
//	landed-by     its card's work landed by another PR: another card PR of the
//	              same origin or label whose record is landed or merged, or a
//	              task naming them that is landed with a different pr
//	read-under-8  the read at its head scored under MinScore and a task of
//	              its card is closed (nothing will fix it)
//	branch-gone   its record carries branch_gone (pr record --branch-gone)
//
// and never when the read at its head is MinScore or more (an APPROVE at
// head) or a task naming it or its issue is live (waiting, ready, working,
// parked: a fix is coming). Every decision is recorded on the PR record
// before any close (one pipeline of ns_pr_reap); then, oldest PR first and at
// most Budget of them, ns_pr_reap_gate checks the record, the head and the
// live tasks again next to that PR's one close call, and the outcomes go back
// in one pipeline of ns_pr_reap_end. A close that fails stays pending and the
// next run retries it; MaxAttempts failures make it STUCK.
package reap

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// MinScore is the read at head that keeps a PR: an APPROVE (8 or more).
const MinScore = 8

// MaxAttempts failed closes make a reaped PR STUCK until a person acts.
const MaxAttempts = 10

// DefaultBudget is the most GitHub close calls one run makes.
const DefaultBudget = 10

// Rules.
const (
	RuleLandedBy   = "landed-by"
	RuleReadUnder8 = "read-under-8"
	RuleBranchGone = "branch-gone"
)

// Task is one work task naming a PR or its card's issue.
type Task struct {
	ID, State string
	PR        int // the PR its pr field names, 0 for none
}

// PR is one card PR as ns_pr_reap_read returns it.
type PR struct {
	Repo      string // as the card names it (owner/name or name)
	N         int
	Label     string
	Key       string // pr:<name>:<n>
	Exists    bool
	State     string
	Head      string
	Reads     string // the raw reads field; its length is the fence
	ClosedAt  string
	Gone      string // branch_gone
	Reap      string
	ReapClose string
	Attempts  int
	ReapBy    string
	ReapWhy   string
	OriginRef string // <name>#<issue>, "" when the card names none
	Tasks     []Task
}

// Ref is <name>#<n>, the ref index's spelling.
func (p PR) Ref() string { return prkey.Name(p.Repo) + "#" + strconv.Itoa(p.N) }

// Pending is a decision whose close has not happened.
func (p PR) Pending() bool {
	return p.ReapClose == "pending" || strings.HasPrefix(p.ReapClose, "failed:")
}

func (p PR) lines() []string {
	var out []string
	for _, l := range strings.Split(p.Reads, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

const perPR = 17

// Load reads every card PR of sprint S in one FCALL_RO.
func Load(ctx context.Context, c redis.Cmdable, sprint string) ([]PR, error) {
	raw, err := c.FCallRO(ctx, "ns_pr_reap_read", nil, sprint).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("ns_pr_reap_read: %w", err)
	}
	if len(raw)%perPR != 0 {
		return nil, fmt.Errorf("ns_pr_reap_read: %d values is not %d per PR", len(raw), perPR)
	}
	var out []PR
	for i := 0; i < len(raw); i += perPR {
		v := raw[i : i+perPR]
		n, err := strconv.Atoi(v[1])
		if err != nil {
			return nil, fmt.Errorf("ns_pr_reap_read: pr %q", v[1])
		}
		att, _ := strconv.Atoi(v[11])
		p := PR{Repo: v[0], N: n, Label: v[2], Exists: v[3] == "1",
			State: v[4], Head: v[5], Reads: v[6], ClosedAt: v[7], Gone: v[8], Reap: v[9], ReapClose: v[10],
			Attempts: att, ReapBy: v[12], ReapWhy: v[13], OriginRef: v[14], Key: v[16]}
		for _, w := range strings.Fields(v[15]) {
			f := strings.SplitN(w, "|", 3)
			for len(f) < 3 {
				f = append(f, "")
			}
			k, _ := strconv.Atoi(f[2])
			p.Tasks = append(p.Tasks, Task{ID: f[0], State: f[1], PR: k})
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].N < out[j].N
	})
	return out, nil
}

// Verdicts.
const (
	Reap    = "REAP"
	Keep    = "KEEP"
	Done    = "DONE" // closed, landed or merged on the record: nothing to do
	Missing = "MISSING"
	Stuck   = "STUCK"
)

// Decision is the verdict on one PR.
type Decision struct {
	PR      PR
	Verdict string
	Rule    string
	By      string
	Why     string
}

var live = map[string]bool{"waiting": true, "ready": true, "working": true, "parked": true}

func landedState(s string) bool { return s == "landed" || s == "merged" }

// Decide is the whole policy, from the records alone.
func Decide(prs []PR) []Decision {
	byOrigin, byLabel := map[string][]int{}, map[string][]int{}
	for i, p := range prs {
		if p.OriginRef != "" {
			byOrigin[p.OriginRef] = append(byOrigin[p.OriginRef], i)
		}
		if p.Label != "" {
			byLabel[p.Label] = append(byLabel[p.Label], i)
		}
	}
	out := make([]Decision, len(prs))
	for i, p := range prs {
		d := decide(p, i, prs, byOrigin, byLabel)
		if d.Verdict == Reap && p.Pending() && p.Attempts >= MaxAttempts {
			d.Verdict = Stuck
		}
		out[i] = d
	}
	return out
}

func decide(p PR, i int, prs []PR, byOrigin, byLabel map[string][]int) Decision {
	d := Decision{PR: p}
	switch {
	case !p.Exists:
		d.Verdict, d.Why = Missing, "no record "+p.Key
		return d
	case landedState(p.State) || p.State == "closed" || p.ClosedAt != "":
		d.Verdict, d.Why = Done, "state="+p.State
		return d
	}
	read := stream.ReadAt(p.lines(), p.Head)
	if read.Score >= MinScore {
		d.Verdict, d.Why = Keep, fmt.Sprintf("approve who=%s score=%d", read.Who, read.Score)
		return d
	}
	for _, t := range p.Tasks {
		if live[t.State] {
			d.Verdict, d.Why = Keep, "live "+t.ID+" "+t.State
			return d
		}
	}
	d.Verdict = Reap
	// landed-by: another card PR of the same work landed.
	seen := map[int]bool{i: true}
	for _, idx := range append(append([]int{}, byOrigin[p.OriginRef]...), byLabel[p.Label]...) {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		if q := prs[idx]; landedState(q.State) {
			d.Rule, d.By, d.Why = RuleLandedBy, q.Ref(), q.Key+" state="+q.State
			return d
		}
	}
	for _, t := range p.Tasks {
		if t.State == "landed" && t.PR != 0 && t.PR != p.N {
			d.Rule, d.By = RuleLandedBy, prkey.Name(p.Repo)+"#"+strconv.Itoa(t.PR)
			d.Why = "task " + t.ID + " landed pr=" + strconv.Itoa(t.PR)
			return d
		}
	}
	// read-under-8: a low read at head and the card closed.
	if read.Score >= 0 {
		for _, t := range p.Tasks {
			if t.State == "closed" || t.State == "done" {
				d.Rule, d.By = RuleReadUnder8, "who="+read.Who
				d.Why = fmt.Sprintf("score=%d at %s; task %s %s", read.Score, stream.Short(p.Head), t.ID, t.State)
				return d
			}
		}
	}
	if p.Gone != "" {
		d.Rule, d.By, d.Why = RuleBranchGone, "branch", "branch_gone="+p.Gone
		return d
	}
	d.Verdict, d.Why = Keep, "no rule"
	return d
}

// Closer is GitHub's PR close; *stream.GitHub is one.
type Closer interface {
	Close(ctx context.Context, repo string, n int) error
}

// Options for one run.
type Options struct {
	Sprint string
	DryRun bool
	Budget int // close calls this run; <= 0 is DefaultBudget
	Closer Closer
}

// Report is one run's lines and counts.
type Report struct {
	Lines                                                                 []string
	PRs, Reap, Closed, Deferred, Failed, Stuck, Revoked, Keep, Done, Miss int
	Calls                                                                 int
}

// Summary is the receipt line.
func (r Report) Summary(sprint string, dry bool) string {
	return fmt.Sprintf("PR REAP sprint=%s prs=%d reap=%d closed=%d deferred=%d failed=%d stuck=%d revoked=%d keep=%d done=%d missing=%d calls=%d dry_run=%t",
		sprint, r.PRs, r.Reap, r.Closed, r.Deferred, r.Failed, r.Stuck, r.Revoked, r.Keep, r.Done, r.Miss, r.Calls, dry)
}

func (r *Report) add(format string, a ...any) { r.Lines = append(r.Lines, fmt.Sprintf(format, a...)) }

var httpStatus = regexp.MustCompile(`HTTP (\d{3})`)

func status(err error) string {
	if m := httpStatus.FindStringSubmatch(err.Error()); m != nil {
		return m[1]
	}
	return "error"
}

// Run is one reap: read, decide, record, then gate and close each.
func Run(ctx context.Context, c redis.Cmdable, o Options) (Report, error) {
	var rep Report
	if o.Sprint == "" {
		return rep, errors.New("reap: no sprint")
	}
	budget := o.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	prs, err := Load(ctx, c, o.Sprint)
	if err != nil {
		return rep, err
	}
	rep.PRs = len(prs)
	ds := Decide(prs)
	var reaps []Decision
	var revokes []Decision
	for _, d := range ds {
		p := d.PR
		switch d.Verdict {
		case Reap:
			rep.Reap++
			reaps = append(reaps, d)
		case Stuck:
			rep.Stuck++
			rep.add("STUCK %s#%d rule=%s close=%s attempts=%d", p.Repo, p.N, p.Reap, p.ReapClose, p.Attempts)
		case Keep, Missing:
			if d.Verdict == Keep {
				rep.Keep++
			} else {
				rep.Miss++
				rep.add("MISSING %s#%d %s", p.Repo, p.N, d.Why)
			}
			if p.Pending() {
				revokes = append(revokes, d)
			}
		case Done:
			rep.Done++
		}
	}
	if o.DryRun {
		for _, d := range reaps {
			rep.add("REAP %s#%d rule=%s by=%s why=%s close=dry-run", d.PR.Repo, d.PR.N, d.Rule, d.By, d.Why)
		}
		for _, d := range revokes {
			rep.Revoked++
			rep.add("REVOKE %s#%d why=%s", d.PR.Repo, d.PR.N, d.Why)
		}
		return rep, nil
	}
	// Record every decision (and every revoke of a pending one) before any close.
	pipe := c.Pipeline()
	dec := make([]*redis.Cmd, len(reaps))
	for i, d := range reaps {
		dec[i] = pipe.FCall(ctx, "ns_pr_reap", nil, o.Sprint, d.PR.Key, d.PR.Head,
			strconv.Itoa(len(d.PR.Reads)), d.Rule, d.By, d.Why)
	}
	for _, d := range revokes {
		pipe.FCall(ctx, "ns_pr_reap_end", nil, o.Sprint, d.PR.Key, "revoked:"+strings.ReplaceAll(d.Why, " ", "_"))
		rep.Revoked++
		rep.add("REVOKE %s#%d why=%s", d.PR.Repo, d.PR.N, d.Why)
	}
	if len(reaps)+len(revokes) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return rep, fmt.Errorf("ns_pr_reap: %w", err)
		}
	}
	type outcome struct {
		d   Decision
		out string
	}
	var outs []outcome
	for i, d := range reaps {
		p := d.PR
		if r, _ := dec[i].Text(); r != "OK" {
			rep.Reap--
			rep.add("SKIP %s#%d rule=%s why=%s", p.Repo, p.N, d.Rule, strings.ToLower(r))
			continue
		}
		if o.Closer == nil || rep.Calls >= budget {
			rep.Deferred++
			rep.add("REAP %s#%d rule=%s by=%s why=%s close=deferred attempts=%d", p.Repo, p.N, d.Rule, d.By, d.Why, p.Attempts)
			continue
		}
		args := []any{o.Sprint, p.Key, p.Head, strconv.Itoa(len(p.Reads)), p.Ref()}
		if p.OriginRef != "" && p.OriginRef != p.Ref() {
			args = append(args, p.OriginRef)
		}
		g, err := c.FCall(ctx, "ns_pr_reap_gate", nil, args...).Text()
		if err != nil {
			return rep, fmt.Errorf("ns_pr_reap_gate: %w", err)
		}
		if g != "GO" {
			rep.Reap--
			rep.Revoked++
			rep.add("REVOKE %s#%d why=%s", p.Repo, p.N, strings.ToLower(strings.TrimPrefix(g, "REVOKED|")))
			continue
		}
		full, err := prkey.Full(p.Repo)
		if err != nil {
			return rep, err
		}
		rep.Calls++
		if err := o.Closer.Close(ctx, full, p.N); err != nil {
			if errors.Is(err, stream.ErrBudget) {
				rep.Calls--
				rep.Deferred++
				rep.add("REAP %s#%d rule=%s by=%s why=%s close=deferred attempts=%d", p.Repo, p.N, d.Rule, d.By, d.Why, p.Attempts)
				continue
			}
			outs = append(outs, outcome{d, "failed:" + status(err)})
			continue
		}
		outs = append(outs, outcome{d, "done"})
	}
	if len(outs) > 0 {
		pipe := c.Pipeline()
		ends := make([]*redis.Cmd, len(outs))
		for i, x := range outs {
			ends[i] = pipe.FCall(ctx, "ns_pr_reap_end", nil, o.Sprint, x.d.PR.Key, x.out)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return rep, fmt.Errorf("ns_pr_reap_end: %w", err)
		}
		for i, x := range outs {
			p := x.d.PR
			att, _ := ends[i].Text()
			if x.out == "done" {
				rep.Closed++
			} else {
				rep.Failed++
			}
			rep.add("REAP %s#%d rule=%s by=%s why=%s close=%s attempts=%s", p.Repo, p.N, x.d.Rule, x.d.By, x.d.Why, x.out, att)
		}
	}
	return rep, nil
}
