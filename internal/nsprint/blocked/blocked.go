// Package blocked is the waiting set's two duties (nova-tools#3366, spec
// #3364; contract: rowan-new specs/ws-index.md). "Blocked" is not a state of
// its own: a task whose DEPENDS-ON is not yet landed in its base is in
// ws:<stream>:waiting, and its task:<id> hash names the conditions in
// blocked_on (written by `task block --on`, internal/nsprint/taskbatch).
//
// List reads every stream's waiting set oldest first. Resolve is the move
// waiting -> ready once every condition is landed, and waiting -> parked
// (why no-parent:<cond>) once a parent can never land; it runs from the
// reconciler every pass (cmd/nova-sprint/waiting_resolve.go registers the
// duty), so no coordinator step releases a task. Every move goes through
// the one move primitive, ns_ws_move_many (internal/nsprint/ws), never a
// direct ZADD; the ready score is the task's created_at, so a released task
// takes its original position, never "now".
//
// A condition is met when:
//
//	task:<id> | card:<id>    task:<id> state is landed, or closed and not cancelled
//	<owner>/<repo>#<n>        pr:<repo>:<n> state is landed or merged (the lander's
//	<repo>#<n>                landed move), else the base branch of the repo's
//	                          mirror holds it: the record's head is an ancestor of
//	                          the base (one merge-base call), or a commit on the base
//	                          lands or closes #<n>. Never a GitHub call.
//
// A parent is gone (the task is parked no-parent:<cond>, never left waiting)
// when task:<id> is closed with cancelled=1, or pr:<repo>:<n> is closed and
// the mirror's base does not hold it. Any other condition (stream/, spec:,
// key:, ...) is not this resolver's to decide: the task stays waiting.
//
// Cost per pass: four pipelined reads (stream names, the waiting sets, the
// waiting tasks' hashes, the parents' hashes), a git call only for a
// <repo>#<n> parent Redis does not already show landed (a landed answer is
// cached for good, an unlanded one until the base tip moves), and one
// ns_ws_move_many per distinct parent list.
package blocked

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// Kind is what a condition names.
type Kind string

const (
	KindTask  Kind = "task"  // task:<id> or card:<id>
	KindPR    Kind = "pr"    // <owner>/<repo>#<n> or <repo>#<n>, a PR or an issue
	KindOther Kind = "other" // stream/, spec:, key:, node: ...: not resolved here
)

// Cond is one DEPENDS-ON condition.
type Cond struct {
	Raw  string
	Kind Kind
	ID   string // KindTask: the task id
	Repo string // KindPR: the bare repository name
	N    int    // KindPR: the number
}

var (
	taskRE = regexp.MustCompile(`^(?:task|card):([A-Za-z0-9._:-]+)$`)
	prRE   = regexp.MustCompile(`^(?:([A-Za-z0-9_.-]+)/)?([A-Za-z0-9_.-]+)#([0-9]+)$`)
)

// Parse splits a DEPENDS-ON list: "none" or "" is no condition; otherwise
// conditions joined by ';' or ','. A condition holding a space is not a
// dependency (a reason such as "spec not ready") and is refused.
func Parse(text string) ([]Cond, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "none" {
		return nil, nil
	}
	var out []Cond
	seen := map[string]bool{}
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == ',' }) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		if strings.ContainsAny(raw, " \t\r\n") {
			return nil, fmt.Errorf("DEPENDS-ON %q is not a condition", raw)
		}
		c := Cond{Raw: raw, Kind: KindOther}
		if m := taskRE.FindStringSubmatch(raw); m != nil {
			c.Kind, c.ID = KindTask, m[1]
		} else if m := prRE.FindStringSubmatch(raw); m != nil {
			n, err := strconv.Atoi(m[3])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("DEPENDS-ON %q has no number", raw)
			}
			c.Kind, c.Repo, c.N = KindPR, m[2], n
		}
		out = append(out, c)
	}
	return out, nil
}

// Row is one waiting task: its stream, id, created_at (its score in every
// set) and the conditions it waits on (blocked_on; "" when it names none).
type Row struct {
	Stream  string
	ID      string
	Created int64 // ms
	On      string
	Reason  string // blocked_reason, when the hash has one
}

// Age is how long the task has existed at now.
func (r Row) Age(now time.Time) time.Duration {
	return now.Sub(time.UnixMilli(r.Created)).Truncate(time.Second)
}

// Streams are the stream names in ws:order rank, then any in ws:names the
// order does not rank, by name. One pipeline.
func Streams(ctx context.Context, c redis.Cmdable) ([]string, error) {
	pipe := c.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	names := pipe.SMembers(ctx, "ws:names")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read stream names: %w", err)
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range order.Val() {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	rest := []string{}
	for _, s := range names.Val() {
		if !seen[s] {
			seen[s] = true
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	return append(out, rest...), nil
}

// List reads the waiting set of one stream (every stream when stream is ""),
// streams in rank order, each oldest first: two pipelines after the names.
func List(ctx context.Context, c redis.Cmdable, stream string) ([]Row, error) {
	streams := []string{stream}
	if stream == "" {
		var err error
		if streams, err = Streams(ctx, c); err != nil {
			return nil, err
		}
	}
	if len(streams) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	sets := make([]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		sets[i] = pipe.ZRangeWithScores(ctx, ws.Key(s, "waiting"), 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read waiting sets: %w", err)
	}
	var rows []Row
	for i, s := range streams {
		for _, z := range sets[i].Val() {
			rows = append(rows, Row{Stream: s, ID: fmt.Sprint(z.Member), Created: int64(z.Score)})
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	pipe = c.Pipeline()
	fields := make([]*redis.SliceCmd, len(rows))
	for i, r := range rows {
		fields[i] = pipe.HMGet(ctx, "task:"+r.ID, "blocked_on", "blocked_reason")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read waiting tasks: %w", err)
	}
	for i := range rows {
		v := fields[i].Val()
		rows[i].On, rows[i].Reason = str(v, 0), str(v, 1)
	}
	return rows, nil
}

func str(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	return fmt.Sprint(v[i])
}

// Git is the resolver's one seam onto the base: the repo's mirror.
type Git interface {
	// Tip resolves base ("" = the repo's default base) and returns it and
	// its tip sha.
	Tip(ctx context.Context, repo, base string) (resolved, tip string, err error)
	// Landed returns the commit on base that lands #n ("" when none): head
	// (when known) is an ancestor of base, or a commit on base merges,
	// closes or squashes #n.
	Landed(ctx context.Context, repo, base string, n int, head string) (sha string, err error)
}

// Release is one task moved waiting -> ready.
type Release struct {
	Stream, ID, Parent, SHA string
}

// Park is one task moved waiting -> parked because a parent can never land.
type Park struct {
	Stream, ID, Parent string
}

// Result is one pass.
type Result struct {
	Released []Release
	Parked   []Park
	Waiting  int        // rows left waiting
	Refused  []ws.IDWhy // rows the move primitive refused (they stay where they are)
	Unknown  []string   // parents this pass could not read (no mirror, git error)
}

// Resolver walks the waiting sets and moves what the facts allow.
type Resolver struct {
	Client redis.Cmdable
	Git    Git    // nil: <repo>#<n> parents are met only by Redis facts
	By     string // the actor on every ws:log receipt

	landed map[string]string // repo|base|n|head -> landing sha, for good
	misses map[string]string // repo|base|n|head -> the tip it was not landed at
}

// verdict is one condition's answer.
type verdict int

const (
	unmet verdict = iota
	met
	gone
)

type answer struct {
	v   verdict
	sha string
}

// Pass is one resolve pass over every stream (or the one named).
func (r *Resolver) Pass(ctx context.Context, stream string) (Result, error) {
	var res Result
	rows, err := List(ctx, r.Client, stream)
	if err != nil {
		return res, err
	}
	type pending struct {
		row   Row
		conds []Cond
	}
	var todo []pending
	taskKeys := map[string]*redis.SliceCmd{}
	prKeys := map[string]*redis.SliceCmd{}
	pipe := r.Client.Pipeline()
	for _, row := range rows {
		conds, err := Parse(row.On)
		if err != nil || len(conds) == 0 {
			res.Waiting++ // no dependency this resolver can read: not its to release
			continue
		}
		todo = append(todo, pending{row: row, conds: conds})
		for _, c := range conds {
			switch c.Kind {
			case KindTask:
				if taskKeys[c.ID] == nil {
					taskKeys[c.ID] = pipe.HMGet(ctx, "task:"+c.ID, "state", "cancelled", "head")
				}
			case KindPR:
				k := prkey.Key(c.Repo, c.N)
				if prKeys[k] == nil {
					prKeys[k] = pipe.HMGet(ctx, k, "state", "head", "base")
				}
			}
		}
	}
	if len(taskKeys)+len(prKeys) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return res, fmt.Errorf("read parents: %w", err)
		}
	}
	tips := map[string][2]string{} // repo|base -> resolved base, tip
	answers := map[string]answer{}
	judge := func(c Cond) answer {
		if a, ok := answers[c.Raw]; ok {
			return a
		}
		a := r.judge(ctx, c, taskKeys, prKeys, tips, &res)
		answers[c.Raw] = a
		return a
	}
	type group struct {
		to, why string
		first   int
	}
	groups := map[string]*group{}
	ids := map[string][]string{}
	var order []string
	add := func(to, why string, id string, at int) {
		k := to + "\x00" + why
		if groups[k] == nil {
			groups[k] = &group{to: to, why: why, first: at}
			order = append(order, k)
		}
		ids[k] = append(ids[k], id)
	}
	byID := map[string]pending{}
	shaOf := map[string]string{}
	for i, p := range todo {
		byID[p.row.ID] = p
		all, sha, goneOn := true, "", ""
		for _, c := range p.conds {
			a := judge(c)
			switch a.v {
			case gone:
				if goneOn == "" {
					goneOn = c.Raw
				}
				all = false
			case unmet:
				all = false
			case met:
				if a.sha != "" {
					sha = a.sha
				}
			}
		}
		switch {
		case goneOn != "":
			add("parked", "no-parent:"+goneOn, p.row.ID, i)
		case all:
			shaOf[p.row.ID] = short(sha)
			add("ready", "depends-on landed: "+joinRaw(p.conds)+" at "+short(sha), p.row.ID, i)
		default:
			res.Waiting++
		}
	}
	for _, k := range order {
		g := groups[k]
		mr, err := ws.MoveMany(ctx, r.Client, g.to, r.By, g.why, ids[k])
		if err != nil {
			return res, err
		}
		refused := map[string]bool{}
		for _, x := range mr.Refused {
			refused[x.ID] = true
			res.Refused = append(res.Refused, x)
		}
		for _, id := range ids[k] {
			if refused[id] {
				continue
			}
			p := byID[id]
			if g.to == "parked" {
				res.Parked = append(res.Parked, Park{Stream: p.row.Stream, ID: id, Parent: strings.TrimPrefix(g.why, "no-parent:")})
				continue
			}
			res.Released = append(res.Released, Release{Stream: p.row.Stream, ID: id, Parent: joinRaw(p.conds), SHA: shaOf[id]})
		}
	}
	return res, nil
}

// judge answers one condition from the pipelined reads and, for a
// <repo>#<n> Redis does not show landed, the mirror.
func (r *Resolver) judge(ctx context.Context, c Cond, taskKeys, prKeys map[string]*redis.SliceCmd,
	tips map[string][2]string, res *Result) answer {
	switch c.Kind {
	case KindTask:
		v := taskKeys[c.ID].Val()
		state, cancelled, head := str(v, 0), str(v, 1), str(v, 2)
		switch {
		case state == "landed":
			return answer{v: met, sha: head}
		case state == ws.Closed && cancelled == "1":
			return answer{v: gone}
		case state == ws.Closed:
			return answer{v: met, sha: head}
		}
		return answer{v: unmet}
	case KindPR:
		v := prKeys[prkey.Key(c.Repo, c.N)].Val()
		state, head, base := str(v, 0), str(v, 1), str(v, 2)
		if state == "landed" || state == "merged" {
			return answer{v: met, sha: head}
		}
		sha, ok := r.mirror(ctx, c, base, head, tips, res)
		switch {
		case sha != "":
			return answer{v: met, sha: sha}
		case ok && state == "closed":
			return answer{v: gone}
		}
		return answer{v: unmet}
	}
	return answer{v: unmet}
}

// mirror asks the Git seam whether base holds #n, through the two caches.
// ok is false when the mirror could not answer.
func (r *Resolver) mirror(ctx context.Context, c Cond, base, head string, tips map[string][2]string, res *Result) (string, bool) {
	if r.Git == nil {
		return "", false
	}
	if r.landed == nil {
		r.landed, r.misses = map[string]string{}, map[string]string{}
	}
	tk := c.Repo + "|" + base
	t, ok := tips[tk]
	if !ok {
		resolved, tip, err := r.Git.Tip(ctx, c.Repo, base)
		if err != nil {
			res.Unknown = append(res.Unknown, c.Raw+": "+err.Error())
			tips[tk] = [2]string{}
			return "", false
		}
		t = [2]string{resolved, tip}
		tips[tk] = t
	}
	if t[1] == "" {
		return "", false
	}
	key := c.Repo + "|" + t[0] + "|" + strconv.Itoa(c.N) + "|" + head
	if sha, ok := r.landed[key]; ok {
		return sha, true
	}
	if r.misses[key] == t[1] {
		return "", true
	}
	sha, err := r.Git.Landed(ctx, c.Repo, t[0], c.N, head)
	if err != nil {
		res.Unknown = append(res.Unknown, c.Raw+": "+err.Error())
		return "", false
	}
	if sha != "" {
		r.landed[key] = sha
		delete(r.misses, key)
		return sha, true
	}
	r.misses[key] = t[1]
	return "", true
}

func joinRaw(conds []Cond) string {
	parts := make([]string, len(conds))
	for i, c := range conds {
		parts[i] = c.Raw
	}
	return strings.Join(parts, ",")
}

func short(sha string) string {
	if sha == "" {
		return "-"
	}
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
