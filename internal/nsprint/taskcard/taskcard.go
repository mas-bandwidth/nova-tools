// Package taskcard is the typed face of the sprint task as a card
// (nova-tools #3778; contract: rowan-new specs/ws-index.md, "The card model"
// and "Tasks are cards"). A task is one record, task:<id>, never deleted,
// whose pointer `where` names the one set it is in (or none):
//
//	where      "" | waiting | ready | working | merging | landed | done | parked
//	where_ok   ok | fail once done (landed is ok), - before
//	stream     ws:<stream>:<where> holds the id while it has a stream
//	friend     friend:<friend>:cards:<where> holds the id while a friend has it
//
// Every set is a ZSET scored by the task's created_at (ms), so the tables are
// ZCARDs and every list is one ZRANGE, oldest first. The one writer is the
// Lua function TK.move in fn/lua/02_card_move.lua (NS.task in Lua); every
// function here is one FCALL of it (or of its ns_tcard_* entry points),
// never a scan of task:*. Migrate's SCAN is the one-time exception.
//
// The graph: "" -> waiting|ready (push); waiting -> ready|parked|done;
// ready -> waiting|working|parked|done; working -> merging|done|landed|ready
// (ready: the lease lapsed); merging -> landed|done; parked ->
// waiting|ready|done; landed -> done/ok (table clear). landed needs the
// merge sha; a done that is not from working needs a why.
package taskcard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The library functions (fn/lua/02_card_move.lua).
const (
	FnMove       = "ns_tcard_move"
	FnPush       = "ns_tcard_push"
	FnTake       = "ns_tcard_take"
	FnDone       = "ns_tcard_done"
	FnBeat       = "ns_tcard_beat"
	FnExpire     = "ns_tcard_expire"
	FnPlace      = "ns_tcard_place"
	FnLandStream = "ns_tcard_land_stream"
	FnFsck       = "ns_tcard_fsck"
)

// Wheres are the sets a task can be in, in the fsck reply's order.
var Wheres = []string{"waiting", "ready", "working", "merging", "landed", "done", "parked"}

// Key is a task's record.
func Key(id string) string { return "task:" + id }

// StreamKey is a stream's set of tasks at one where.
func StreamKey(stream, where string) string { return "ws:" + stream + ":" + where }

// FriendKey is a friend's set of tasks (and cards) at one where.
func FriendKey(friend, where string) string { return "friend:" + friend + ":cards:" + where }

// Opts are a move's options. Friend and Stream change the task's dimension
// only when their Set flag is true (an empty friend drops it).
type Opts struct {
	By, Why string
	OK      string // ok | fail, entering done
	Sha     string // the merge sha, entering landed
	Sprint  string // the legacy idx sets' sprint when the record names none
	Front   bool   // requeue on q:<friend>:front
	As      string // a take: the friend taking
	Friend  string
	Stream  string
	// SetFriend and SetStream make Friend and Stream apply.
	SetFriend, SetStream bool
	// Fields are more record fields, name then value (never a pointer field).
	Fields []string
}

func (o Opts) args() []any {
	var a []any
	kv := func(k, v string) { a = append(a, k, v) }
	if o.OK != "" {
		kv("ok", o.OK)
	}
	if o.Sha != "" {
		kv("sha", o.Sha)
	}
	if o.Sprint != "" {
		kv("sprint", o.Sprint)
	}
	if o.Front {
		kv("front", "1")
	}
	if o.As != "" {
		kv("as", o.As)
	}
	if o.SetFriend {
		kv("friend", o.Friend)
	}
	if o.SetStream {
		kv("stream", o.Stream)
	}
	for i := 0; i+1 < len(o.Fields); i += 2 {
		kv(o.Fields[i], o.Fields[i+1])
	}
	return a
}

// Refused is a REFUSED reply: the call wrote nothing.
type Refused struct{ Why string }

func (r *Refused) Error() string { return "REFUSED " + r.Why }

// IsRefused reports whether err is a Refused reply, and its why.
func IsRefused(err error) (string, bool) {
	var r *Refused
	if errors.As(err, &r) {
		return r.Why, true
	}
	return "", false
}

// Result is one move: Status MOVED (From -> To) or SAME (already there,
// nothing to change).
type Result struct {
	Status, From, To string
}

func str(reply any) (string, error) {
	s, ok := reply.(string)
	if !ok {
		return "", fmt.Errorf("unexpected reply %T %v", reply, reply)
	}
	return s, nil
}

func list(reply any) ([]string, error) {
	rows, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected reply %T %v", reply, reply)
	}
	out := make([]string, len(rows))
	for i, v := range rows {
		out[i] = fmt.Sprint(v)
	}
	return out, nil
}

func parseMove(fn, s string) (Result, error) {
	f := strings.Fields(s)
	switch {
	case strings.HasPrefix(s, "REFUSED "):
		return Result{}, &Refused{Why: strings.TrimPrefix(s, "REFUSED ")}
	case len(f) == 3 && f[0] == "MOVED":
		from := f[1]
		if from == "-" {
			from = ""
		}
		return Result{Status: f[0], From: from, To: f[2]}, nil
	case len(f) == 2 && f[0] == "SAME":
		return Result{Status: f[0], From: f[1], To: f[1]}, nil
	}
	return Result{}, fmt.Errorf("%s: unexpected reply %q", fn, s)
}

// Move is the one move: task id to where `to`. A refusal is a *Refused and
// wrote nothing.
func Move(ctx context.Context, c redis.Cmdable, id, to string, o Opts) (Result, error) {
	args := append([]any{id, to, o.By, o.Why}, o.args()...)
	reply, err := c.FCall(ctx, FnMove, nil, args...).Result()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FnMove, err)
	}
	s, err := str(reply)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FnMove, err)
	}
	return parseMove(FnMove, s)
}

// PushRequest is a new task. Where is waiting or ready (empty: ready, or
// waiting when DependsOn is set). Stream empty takes the title's
// "STREAM: <s> |" prefix.
type PushRequest struct {
	ID, Where, Stream, Friend, Sprint string
	Kind, Ref, Origin, Title          string
	Head, PR, Repo, DependsOn         string
	Front                             bool
	By, Why                           string
}

// PushResult is where the task was placed and its ready entry (q:<friend>).
type PushResult struct {
	Where, XID string
}

// Push creates the record and makes its first move, in one call.
func Push(ctx context.Context, c redis.Cmdable, r PushRequest) (PushResult, error) {
	where := r.Where
	if where == "" {
		where = "ready"
		if r.DependsOn != "" {
			where = "waiting"
		}
	}
	why := r.Why
	if why == "" {
		why = "push"
	}
	args := []any{r.ID, where, r.By, why}
	o := Opts{Sprint: r.Sprint, Front: r.Front}
	if r.Friend != "" {
		o.Friend, o.SetFriend = r.Friend, true
	}
	if r.Stream != "" {
		o.Stream, o.SetStream = r.Stream, true
	}
	for _, kv := range [][2]string{{"kind", r.Kind}, {"ref", r.Ref}, {"origin", r.Origin}, {"title", r.Title},
		{"head", r.Head}, {"pr", r.PR}, {"repo", r.Repo}, {"blocked_on", r.DependsOn}} {
		if kv[1] != "" {
			o.Fields = append(o.Fields, kv[0], kv[1])
		}
	}
	front := "0"
	if r.Front {
		front = "1"
	}
	o.Fields = append(o.Fields, "front", front)
	args = append(args, o.args()...)
	reply, err := c.FCall(ctx, FnPush, nil, args...).Result()
	if err != nil {
		return PushResult{}, fmt.Errorf("%s: %w", FnPush, err)
	}
	s, err := str(reply)
	if err != nil {
		return PushResult{}, fmt.Errorf("%s: %w", FnPush, err)
	}
	if why, ok := strings.CutPrefix(s, "REFUSED "); ok {
		return PushResult{}, &Refused{Why: why}
	}
	f := strings.Fields(s)
	if len(f) != 3 || f[0] != "PUSHED" {
		return PushResult{}, fmt.Errorf("%s: unexpected reply %q", FnPush, s)
	}
	xid := f[2]
	if xid == "-" {
		xid = ""
	}
	return PushResult{Where: f[1], XID: xid}, nil
}

// Take moves the named ids (or, with none, the n oldest of
// friend:<as>:cards:ready) to working for friend as; each take starts a
// lease the child renews with Beat. A named id the move refuses is a
// *Refused.
func Take(ctx context.Context, c redis.Cmdable, as string, n int, by string, ids ...string) ([]string, error) {
	if n < 1 {
		n = 1
	}
	args := []any{as, n, by}
	for _, id := range ids {
		args = append(args, id)
	}
	reply, err := c.FCall(ctx, FnTake, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FnTake, err)
	}
	out, err := list(reply)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FnTake, err)
	}
	if len(out) == 2 && out[0] == "REFUSED" {
		return nil, &Refused{Why: out[1]}
	}
	if len(out) < 2 || out[0] != "TAKEN" {
		return nil, fmt.Errorf("%s: unexpected reply %v", FnTake, out)
	}
	return out[2:], nil
}

// Done ends a working task: working -> merging when it names a PR (its pr
// field, or pr), else working -> done/ok, with the evidence on the record.
func Done(ctx context.Context, c redis.Cmdable, id, by, evidence, pr string) (Result, error) {
	reply, err := c.FCall(ctx, FnDone, nil, id, by, evidence, pr).Result()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FnDone, err)
	}
	s, err := str(reply)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FnDone, err)
	}
	return parseMove(FnDone, s)
}

// Land moves a merged task to landed at the merge sha (merging, or working
// when the merge came before a done). The stream lander calls it.
func Land(ctx context.Context, c redis.Cmdable, id, by, sha, why string) (Result, error) {
	if why == "" {
		why = "merged " + sha
	}
	return Move(ctx, c, id, "landed", Opts{By: by, Why: why, Sha: sha})
}

// Cancel ends a task that will not be done: done/fail with the why.
func Cancel(ctx context.Context, c redis.Cmdable, id, by, why string) (Result, error) {
	return Move(ctx, c, id, "done", Opts{By: by, Why: why, OK: "fail", Fields: []string{"evidence", why}})
}

// Beat renews the lease of a working task its holder as still works on; it
// returns the new lease_until (ms).
func Beat(ctx context.Context, c redis.Cmdable, id, as string) (int64, error) {
	reply, err := c.FCall(ctx, FnBeat, nil, id, as).Result()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", FnBeat, err)
	}
	s, err := str(reply)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", FnBeat, err)
	}
	if why, ok := strings.CutPrefix(s, "REFUSED "); ok {
		return 0, &Refused{Why: why}
	}
	v, ok := strings.CutPrefix(s, "BEAT ")
	if !ok {
		return 0, fmt.Errorf("%s: unexpected reply %q", FnBeat, s)
	}
	return strconv.ParseInt(v, 10, 64)
}

// Expire moves every working task of the friends (every member of friends
// when none is named) whose lease lapsed back to ready, why=lease lapsed,
// and returns their ids.
func Expire(ctx context.Context, c redis.Cmdable, by string, friends ...string) ([]string, error) {
	args := []any{by}
	for _, f := range friends {
		args = append(args, f)
	}
	reply, err := c.FCall(ctx, FnExpire, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FnExpire, err)
	}
	out, err := list(reply)
	if err != nil || len(out) < 2 || out[0] != "EXPIRED" {
		return nil, fmt.Errorf("%s: unexpected reply %v %v", FnExpire, out, err)
	}
	return out[2:], nil
}

// Ls is one ZRANGE: a stream's tasks at a where, oldest first.
func Ls(ctx context.Context, c redis.Cmdable, stream, where string) ([]string, error) {
	return c.ZRange(ctx, StreamKey(stream, where), 0, -1).Result()
}

// LsFriend is one ZRANGE: a friend's tasks (and cards) at a where.
func LsFriend(ctx context.Context, c redis.Cmdable, friend, where string) ([]string, error) {
	return c.ZRange(ctx, FriendKey(friend, where), 0, -1).Result()
}

// FsckResult is ns_tcard_fsck's reply.
type FsckResult struct {
	Sprint   string
	Tasks    int64
	Null     int64
	Counts   map[string]int64 // per where
	Unplaced int64            // records with no where field: task migrate owes them
	Drift    int64
	Lines    []string // up to 100 drift lines
}

// Fsck walks both directions of the double link, read-only, over the ws
// sets of every stream, the friend sets of every friend and the roster
// sprint:<sprint>:tasks: every id in a set has a record naming that set,
// every record with a where is in exactly its sets, the state mirrors where
// and the legacy idx sets agree. Drift is a bug in the one writer.
func Fsck(ctx context.Context, c redis.Cmdable, sprint string) (FsckResult, error) {
	reply, err := c.FCallRO(ctx, FnFsck, nil, sprint).Result()
	if err != nil {
		return FsckResult{}, fmt.Errorf("%s: %w", FnFsck, err)
	}
	out, err := list(reply)
	n := 4 + len(Wheres) + 2
	if err != nil || len(out) < n || out[0] != "FSCK" {
		return FsckResult{}, fmt.Errorf("%s: unexpected reply %v %v", FnFsck, out, err)
	}
	num := func(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
	r := FsckResult{Sprint: out[1], Tasks: num(out[2]), Null: num(out[3]), Counts: map[string]int64{}}
	for i, w := range Wheres {
		r.Counts[w] = num(out[4+i])
	}
	r.Unplaced = num(out[4+len(Wheres)])
	r.Drift = num(out[5+len(Wheres)])
	r.Lines = out[n:]
	return r, nil
}

// Member is one task a stream merge landed: its id, its PR or issue
// (repo#n) and origin (the URL it came from), what the lander closes with
// the CLOSE line.
type Member struct {
	ID, Ref, Origin string
}

// StreamLanding is ns_tcard_land_stream's reply: the members moved to landed
// and the ones the move refused (id -> why), which stay where they are.
type StreamLanding struct {
	Landed  []Member
	Refused map[string]string
}

// LandStream is the stream lander's step (Glenn 2026-09-25 08:50 ET): every
// member of ws:<stream>:merging moves to landed at the merge sha, in one
// call, oldest first. The caller closes each Landed member's PR and origin
// issue with the CLOSE line; nothing here calls GitHub.
func LandStream(ctx context.Context, c redis.Cmdable, stream, sha, by, why string) (StreamLanding, error) {
	reply, err := c.FCall(ctx, FnLandStream, nil, stream, sha, by, why).Result()
	if err != nil {
		return StreamLanding{}, fmt.Errorf("%s: %w", FnLandStream, err)
	}
	out, err := list(reply)
	if err != nil || len(out) < 3 || out[0] != "LANDED" {
		return StreamLanding{}, fmt.Errorf("%s: unexpected reply %v %v", FnLandStream, out, err)
	}
	n, _ := strconv.Atoi(out[1])
	m, _ := strconv.Atoi(out[2])
	if len(out) != 3+3*n+2*m {
		return StreamLanding{}, fmt.Errorf("%s: reply of %d cells for %d landed, %d refused", FnLandStream, len(out), n, m)
	}
	r := StreamLanding{Refused: map[string]string{}}
	for i := 0; i < n; i++ {
		r.Landed = append(r.Landed, Member{ID: out[3+3*i], Ref: out[4+3*i], Origin: out[5+3*i]})
	}
	for i := 0; i < m; i++ {
		r.Refused[out[3+3*n+2*i]] = out[4+3*n+2*i]
	}
	return r, nil
}
