package taskcard

// Work as a hierarchy (nova-tools #4317; Glenn 2026-09-26 ~11:05 AM ET:
// "You are fable, you deploy to children, you review their work at the end,
// and stitch it together. Can you do all this within the worker system?").
//
// A PLAN is a parent card (kind plan) the coordinator cuts into CHILD cards
// on the same stream, plus one STITCH card at the end that DEPENDS-ON every
// child (the one edge form of #3409 and the sentinel pattern of #4318: a
// comma list of task ids in blocked_on, released by the waiting resolver
// when each is landed). The parent DEPENDS-ON its stitch, so the whole plan
// is one edge graph and nothing here is a second kind of dependency.
//
// The record fields, none of them a pointer:
//
//	parent   kind=plan  children=<id id ...>  stitch=<id>  blocked_on=<stitch>
//	child    parent=<parent>  phase=child
//	stitch   parent=<parent>  phase=stitch  blocked_on=<child,child,...>
//
// The parent is never dealt (TM.leg: a plan's children are dealt, its
// stitch lands it) and its state is DERIVED (Plan.State): working while any
// child works, review when the stitch is in review, landed when the stitch
// lands. Its record lands from waiting or ready at the stitch's merge sha
// (TK.edge's one exception, taken by ns_tcard_land_stream when the stitch
// lands, or by task land --id <parent> by hand).
//
// The stitch's brief is GENERATED (StitchBrief): every child's PR, RESULT.md
// summary (its line 2 and finding) and read score, so the coordinator's
// stitch child starts with the whole picture. It is written onto the stitch's
// body at the cut (BindPlan) and again when the waiting resolver releases
// the stitch to ready, which is after every child landed.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// The hierarchy's kinds, phases and field names.
const (
	KindPlan    = cardhdr.KindPlan   // the one-invariant lint's plan exemption (#4396)
	KindStitch  = cardhdr.KindStitch // the one-invariant lint's stitch exemption (#4396)
	PhaseChild  = "child"
	PhaseStitch = "stitch"

	FieldParent   = "parent"
	FieldPhase    = "phase"
	FieldChildren = "children"
	FieldStitch   = "stitch"

	// BriefMarker heads the generated section of a stitch's body; a rewrite
	// replaces everything from it to the end.
	BriefMarker = "## Children"
)

// StitchID is the stitch card's id for a parent: <parent>-stitch (a task id
// in the one id form, so every parser and the resolver take it as is).
func StitchID(parent string) string { return parent + "-stitch" }

// NextStitchID is the id of the stitch re-cut after old ended done
// (nova-tools#4317 fix): <parent>-stitch-2 after <parent>-stitch, then -3,
// and so on. The old record stays done; the parent's stitch field moves on.
func NextStitchID(parent, old string) string {
	base := StitchID(parent)
	n := 1
	if rest, ok := strings.CutPrefix(old, base+"-"); ok {
		if k, err := strconv.Atoi(rest); err == nil && k > 1 {
			n = k
		}
	}
	return base + "-" + strconv.Itoa(n+1)
}

// IsStitchOf reports whether id is one of parent's stitch ids: StitchID or
// a re-cut's NextStitchID.
func IsStitchOf(parent, id string) bool {
	base := StitchID(parent)
	if id == base {
		return true
	}
	rest, ok := strings.CutPrefix(id, base+"-")
	if !ok {
		return false
	}
	k, err := strconv.Atoi(rest)
	return err == nil && k > 1 && strconv.Itoa(k) == rest
}

// Child is one card of a plan as the stitch's brief reads it: its pointer
// and the result fields a card end wrote on it.
type Child struct {
	ID, Title, Where, WhereOK       string
	Ref, Repo, PR, Head             string
	Line2, Finding, Evidence, Score string
	Paths                           string
}

// PRRef is the child's PR as repo#n, its ref when it has no PR, or "".
func (c Child) PRRef() string {
	if c.PR != "" && c.PR != "0" {
		repo := c.Repo
		if i := strings.IndexByte(repo, '/'); i >= 0 {
			repo = repo[i+1:]
		}
		if repo == "" {
			return "#" + c.PR
		}
		return repo + "#" + c.PR
	}
	return c.Ref
}

// Plan is a parent card with its children and stitch, read from the records.
type Plan struct {
	ID, Title, Stream, Where, WhereOK string
	Ref, DoneWhen                     string
	Children                          []Child
	Stitch                            Child // ID "" when the parent has no stitch yet
}

// Stuck is the derived state of a plan that cannot move on its own, with
// Plan.Remedy naming the way on (nova-tools#4317, and its fix: a plan never
// sits in a state with no way on). Two causes:
//
//   - the stitch ended done (cancelled, done/fail, or done without a PR) or
//     has no record: the plan lands only with its stitch, so it never
//     lands; `card cut --parent <parent>` re-cuts the stitch (NextStitchID,
//     DEPENDS-ON every child) and the plan moves on with it;
//   - a child ended done/fail: its edge is never met, so the stitch is never
//     released; `card stitch --drop <child>` drops the child
//     from the plan (DropChild: the stitch's edge with it), or `card cut
//     --parent` cuts a replacement.
const Stuck = "stuck"

// StitchEnded reports whether the plan's stitch can never land: it ended
// done (any outcome) or its record is missing. A plan with no stitch yet
// (ID "") has not ended one.
func (p Plan) StitchEnded() bool {
	return p.Stitch.ID != "" && (p.Stitch.Where == ws.Done || p.Stitch.Where == "")
}

// State is the parent's derived state: its own terminal set when it is in
// one; else the stitch's once the stitch has moved (landed, review, merging,
// working, ready); else stuck when the stitch ended done (StitchEnded) or a
// child ended done/fail, parked when a child or the stitch is parked,
// working while any child is in flight (working, review, merging), ready
// when one is ready, and waiting otherwise. It is never waiting for a plan
// that cannot move: every stuck plan has a Remedy.
func (p Plan) State() string {
	switch p.Where {
	case ws.Landed, ws.Done, ws.Parked:
		return p.Where
	}
	switch p.Stitch.Where {
	case ws.Landed, ws.Review, ws.Merging, ws.Working, ws.Ready:
		return p.Stitch.Where
	}
	if p.StitchEnded() {
		return Stuck
	}
	for _, c := range p.Children {
		if c.Where == ws.Done && c.WhereOK == "fail" {
			return Stuck
		}
	}
	if p.Stitch.Where == ws.Parked {
		return ws.Parked
	}
	ready := false
	for _, c := range p.Children {
		switch c.Where {
		case ws.Parked:
			return ws.Parked
		case ws.Working, ws.Review, ws.Merging:
			return ws.Working
		case ws.Ready:
			ready = true
		}
	}
	if ready {
		return ws.Ready
	}
	return ws.Waiting
}

// Failed is the children that ended done/fail: the ones Stuck is about.
func (p Plan) Failed() []string {
	var out []string
	for _, c := range p.Children {
		if c.Where == ws.Done && c.WhereOK == "fail" {
			out = append(out, c.ID)
		}
	}
	return out
}

// Remedy is the one line a stuck plan needs, "" for any other state. A
// stitch that ended is re-cut (card cut --parent); a failed child is
// dropped first when there is one, so the re-cut stitch is not stuck on it.
func (p Plan) Remedy() string {
	if p.State() != Stuck {
		return ""
	}
	f := p.Failed()
	if !p.StitchEnded() {
		return fmt.Sprintf("stuck: %s ended done/fail and the stitch waits on it; nova-sprint card stitch --drop %s drops it from the plan, or nova-sprint card cut --parent %s --from <children.tsv> cuts a replacement (then drop the failed one)",
			strings.Join(f, ","), f[0], p.ID)
	}
	ended := "has no record"
	if p.Stitch.Where == ws.Done {
		ended = "ended done/" + dashOf(p.Stitch.WhereOK)
	}
	why := fmt.Sprintf("stuck: stitch %s %s and the plan lands only with its stitch", p.Stitch.ID, ended)
	if len(f) > 0 {
		return fmt.Sprintf("%s, and %s ended done/fail; nova-sprint card stitch --drop %s drops it, then nova-sprint card cut --parent %s re-cuts the stitch (%s, DEPENDS-ON every child)",
			why, strings.Join(f, ","), f[0], p.ID, NextStitchID(p.ID, p.Stitch.ID))
	}
	return fmt.Sprintf("%s; nova-sprint card cut --parent %s re-cuts the stitch (%s, DEPENDS-ON every child)",
		why, p.ID, NextStitchID(p.ID, p.Stitch.ID))
}

// Counts folds the children by where (the table's six live states, then
// done and parked); a child with no record counts under "".
func (p Plan) Counts() map[string]int {
	m := map[string]int{}
	for _, c := range p.Children {
		m[c.Where]++
	}
	return m
}

// Line is the plan as one table line: its derived state and the children's
// counts folded in, no new column.
func (p Plan) Line() string {
	m := p.Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "plan %s %s children=%d", p.ID, p.State(), len(p.Children))
	for _, w := range ws.Wheres {
		fmt.Fprintf(&b, " %s=%d", w, m[w])
	}
	stitch := "-"
	if p.Stitch.ID != "" {
		stitch = p.Stitch.ID + ":" + dashOf(p.Stitch.Where)
	}
	fmt.Fprintf(&b, " stitch=%s", stitch)
	return b.String()
}

func dashOf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// childOf reads a Child from its record; Where "" is no record.
func childOf(id string, rec map[string]string) Child {
	return Child{ID: id, Title: rec["title"], Where: rec["where"], WhereOK: rec["where_ok"],
		Ref: rec["ref"], Repo: rec["repo"], PR: rec["pr"], Head: rec["head"],
		Line2: rec["line2"], Finding: rec["finding"], Evidence: rec["evidence"], Score: rec["score"], Paths: rec["paths"]}
}

// planOf reads a Plan's own fields from the parent's record.
func planOf(id string, rec map[string]string) Plan {
	return Plan{ID: id, Title: rec["title"], Stream: rec["stream"], Where: rec["where"], WhereOK: rec["where_ok"],
		Ref: rec["ref"], DoneWhen: rec["done_when"]}
}

// ReadPlan reads a parent and its children and stitch: one HGETALL, then one
// pipeline over the children and the stitch. A parent with no record is an
// error naming it; a parent that is not a plan (no children, no stitch)
// reads as a Plan with none.
func ReadPlan(ctx context.Context, c redis.Cmdable, parent string) (Plan, error) {
	rec, err := c.HGetAll(ctx, Key(parent)).Result()
	if err != nil {
		return Plan{}, fmt.Errorf("task:%s: %w", parent, err)
	}
	if len(rec) == 0 {
		return Plan{}, fmt.Errorf("no task:%s", parent)
	}
	plans, err := fill(ctx, c, []planRec{{id: parent, rec: rec}})
	if err != nil {
		return Plan{}, err
	}
	return plans[0], nil
}

type planRec struct {
	id  string
	rec map[string]string
}

// fill reads every plan's children and stitch in one pipeline.
func fill(ctx context.Context, c redis.Cmdable, parents []planRec) ([]Plan, error) {
	type want struct {
		plan   int
		id     string
		stitch bool
	}
	var wants []want
	plans := make([]Plan, len(parents))
	for i, p := range parents {
		plans[i] = planOf(p.id, p.rec)
		for _, id := range strings.Fields(p.rec[FieldChildren]) {
			wants = append(wants, want{plan: i, id: id})
		}
		if s := p.rec[FieldStitch]; s != "" {
			wants = append(wants, want{plan: i, id: s, stitch: true})
		}
	}
	if len(wants) == 0 {
		return plans, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(wants))
	for i, w := range wants {
		cmds[i] = pipe.HGetAll(ctx, Key(w.id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plan records: %w", err)
	}
	for i, w := range wants {
		ch := childOf(w.id, cmds[i].Val())
		if w.stitch {
			plans[w.plan].Stitch = ch
		} else {
			plans[w.plan].Children = append(plans[w.plan].Children, ch)
		}
	}
	return plans, nil
}

// Plans reads every plan of the named streams, in the streams' order and,
// within a stream, oldest first: the sprint epoch, one pipeline over the
// streams' sets under it (nova-tools#4238: a plan of an older epoch is not
// shown, like its cells), one HMGET pipeline for the members' kind, then
// fill. No SCAN.
func Plans(ctx context.Context, c redis.Cmdable, streams []string) ([]Plan, error) {
	if len(streams) == 0 {
		return nil, nil
	}
	epoch, err := ws.Epoch(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("plans: %w", err)
	}
	pipe := c.Pipeline()
	var sets []*redis.StringSliceCmd
	for _, s := range streams {
		for _, w := range ws.Wheres {
			sets = append(sets, pipe.ZRange(ctx, StreamKeyAt(epoch, s, w), 0, -1))
		}
	}
	if len(sets) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plans: stream sets: %w", err)
	}
	type member struct {
		stream int
		id     string
	}
	var members []member
	seen := map[string]bool{}
	for i, cmd := range sets {
		for _, id := range cmd.Val() {
			if IsCopy(id) || seen[id] {
				continue
			}
			seen[id] = true
			members = append(members, member{stream: i / len(ws.Wheres), id: id})
		}
	}
	if len(members) == 0 {
		return nil, nil
	}
	pipe = c.Pipeline()
	recs := make([]*redis.MapStringStringCmd, len(members))
	kinds := make([]*redis.StringCmd, len(members))
	for i, m := range members {
		kinds[i] = pipe.HGet(ctx, Key(m.id), "kind")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plans: kinds: %w", err)
	}
	var parents []planRec
	var order []member
	pipe = c.Pipeline()
	for i, m := range members {
		if kinds[i].Val() != KindPlan {
			continue
		}
		recs[i] = pipe.HGetAll(ctx, Key(m.id))
		order = append(order, m)
	}
	if len(order) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plans: records: %w", err)
	}
	for i, m := range members {
		if recs[i] != nil {
			parents = append(parents, planRec{id: m.id, rec: recs[i].Val()})
		}
	}
	// The streams' order, then created_at within a stream (the sets' order
	// is per where; the record's created_at orders across them).
	plans, err := fill(ctx, c, parents)
	if err != nil {
		return nil, err
	}
	created := map[string]string{}
	streamOf := map[string]int{}
	for i, p := range parents {
		created[p.id] = fmt.Sprintf("%020s", p.rec["created_at"])
		streamOf[p.id] = order[i].stream
	}
	sort.SliceStable(plans, func(i, j int) bool {
		a, b := plans[i].ID, plans[j].ID
		if streamOf[a] != streamOf[b] {
			return streamOf[a] < streamOf[b]
		}
		return created[a] < created[b]
	})
	return plans, nil
}

// FnStitchBrief is the store's brief function: ns_stitch_brief(id, write)
// -> OK <brief> | REFUSED <why> (internal/nsprint/fn/lua/stitch_brief.lua).
// The brief is generated in the store since nova-tools #4449 (Stella's
// read, 2026-09-27): one implementation, one call, and the waiting-resolve
// pass writes a released stitch's brief inside its own call.
const FnStitchBrief = "ns_stitch_brief"

func stitchBrief(ctx context.Context, c redis.Cmdable, id string, write bool) (string, error) {
	w := "0"
	if write {
		w = "1"
	}
	reply, err := c.FCall(ctx, FnStitchBrief, nil, id, w).StringSlice()
	if err != nil {
		return "", fmt.Errorf("%s: %w", FnStitchBrief, err)
	}
	if len(reply) != 2 {
		return "", fmt.Errorf("%s: unexpected reply %v", FnStitchBrief, reply)
	}
	if reply[0] != "OK" {
		return "", errors.New(reply[1])
	}
	return reply[1], nil
}

// RenderStitchBrief is the generated section of a stitch's body, from the
// plan's records, written nowhere: the marker line, the plan line with its
// state and the stitch's DONE-WHEN, the remedy when stuck, one entry per
// child in the plan's order with PR, head, score, result, finding, evidence
// and paths. id is the plan's id or its stitch's. One call.
func RenderStitchBrief(ctx context.Context, c redis.Cmdable, id string) (string, error) {
	return stitchBrief(ctx, c, id, false)
}

// WriteStitchBrief regenerates a stitch's body from its plan's records and
// writes it through the one writer (a move to the stitch's own where
// carrying the body field; the text before BriefMarker is kept, the section
// after it replaced). id is the stitch's id (or its parent's). It returns
// the brief written. A stitch with no parent field, or a parent with no
// record or no stitch, is an error naming the remedy. One call.
func WriteStitchBrief(ctx context.Context, c redis.Cmdable, id string) (string, error) {
	return stitchBrief(ctx, c, id, true)
}

// setFields writes record fields (never a pointer field) through the one
// writer: a move to the task's own where, which changes nothing but the
// fields (TK.move's same-where path).
func setFields(ctx context.Context, c redis.Cmdable, id, by, why string, fields ...string) error {
	where, err := c.HGet(ctx, Key(id), "where").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("task:%s: %w", id, err)
	}
	if where == "" {
		return fmt.Errorf("task:%s has no where; nothing written", id)
	}
	if _, err := Move(ctx, c, id, where, Opts{By: by, Why: why, Fields: fields}); err != nil {
		return fmt.Errorf("task:%s %s: %w", id, why, err)
	}
	return nil
}

// BindPlan makes parent a plan over children with stitch (nova-tools#4317):
// a parent whose old stitch ended done (Plan.StitchEnded) takes the new
// stitch in its place (the re-cut; the old record stays done);
// the parent's record gains kind plan, children (appended to any it has),
// stitch and DEPENDS-ON the stitch (blocked_on, the one edge form), through
// the one move (waiting stays, ready goes back to waiting; anything else is
// a *Refused naming where it is); a stitch still waiting gains an edge to
// every new child (its blocked_on grows, never shrinks), and the stitch's
// brief is written. A bind with nothing new changes nothing but the brief.
// The children and the stitch are pushed before this is called.
func BindPlan(ctx context.Context, c redis.Cmdable, parent string, children []string, stitch, by string) (Result, error) {
	rec, err := c.HMGet(ctx, Key(parent), "where", FieldChildren, FieldStitch).Result()
	if err != nil {
		return Result{}, fmt.Errorf("task:%s: %w", parent, err)
	}
	str := func(i int) string {
		if s, ok := rec[i].(string); ok {
			return s
		}
		return ""
	}
	where := str(0)
	switch where {
	case ws.Waiting, ws.Ready:
	case "":
		return Result{}, &Refused{Why: "no task:" + parent + ": push the parent first (card cut --issue <n>, or task push)"}
	default:
		return Result{}, &Refused{Why: fmt.Sprintf("task:%s is %s; a plan is cut while its parent waits (waiting or ready)", parent, where)}
	}
	if old := str(2); old != "" && old != stitch {
		// A re-cut (the fix of #4317): the old stitch ended done (or has no
		// record), so the plan takes a new one; a live old stitch is kept.
		ow, err := c.HGet(ctx, Key(old), "where").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return Result{}, fmt.Errorf("task:%s: %w", old, err)
		}
		if (ow != ws.Done && ow != "") || !IsStitchOf(parent, stitch) {
			return Result{}, &Refused{Why: fmt.Sprintf("task:%s is a plan whose stitch is %s, not %s: %s is %s, and a stitch is re-cut (as %s) only once the old one ended done", parent, old, stitch, old, dashOf(ow), NextStitchID(parent, old))}
		}
	}
	all := strings.Fields(str(1))
	seen := map[string]bool{}
	for _, id := range all {
		seen[id] = true
	}
	for _, id := range children {
		if !seen[id] {
			seen[id] = true
			all = append(all, id)
		}
	}
	r, err := Move(ctx, c, parent, ws.Waiting, Opts{By: by, Why: "card cut --parent: depends-on " + stitch,
		Fields: []string{"kind", KindPlan, FieldChildren, strings.Join(all, " "), FieldStitch, stitch,
			"blocked_on", stitch, "depends_on", stitch}})
	if err != nil {
		return r, err
	}
	// The stitch's edges: every child, the ones it had first. Only a
	// waiting stitch takes new edges (a released stitch is dealt as is).
	sv, err := c.HMGet(ctx, Key(stitch), "where", "blocked_on").Result()
	if err != nil {
		return r, fmt.Errorf("task:%s: %w", stitch, err)
	}
	sw, _ := sv[0].(string)
	sb, _ := sv[1].(string)
	if sw == ws.Waiting {
		edges := strings.FieldsFunc(sb, func(c rune) bool { return c == ',' || c == ';' || c == ' ' })
		has := map[string]bool{}
		for _, e := range edges {
			has[strings.TrimPrefix(e, "task:")] = true
		}
		grew := false
		for _, id := range all {
			if !has[id] {
				has[id] = true
				edges = append(edges, id)
				grew = true
			}
		}
		if grew || sb == "" {
			joined := strings.Join(edges, ",")
			if err := setFields(ctx, c, stitch, by, "card cut --parent: depends-on "+joined, "blocked_on", joined, "depends_on", joined); err != nil {
				return r, err
			}
		}
	}
	if _, err := WriteStitchBrief(ctx, c, stitch); err != nil {
		return r, err
	}
	return r, nil
}

// planCascade is what a cancel of id must end before id itself: nothing for
// a card that is not a plan; for a plan, its live children then its live
// stitch, oldest first as the plan lists them. A child in flight (working,
// review, merging) is a *Refused naming every such child, before any write.
func planCascade(ctx context.Context, c redis.Cmdable, id string) ([]string, error) {
	rec, err := c.HMGet(ctx, Key(id), "kind", FieldChildren, FieldStitch).Result()
	if err != nil {
		return nil, fmt.Errorf("task:%s: %w", id, err)
	}
	get := func(i int) string {
		s, _ := rec[i].(string)
		return s
	}
	if get(0) != KindPlan {
		return nil, nil
	}
	ids := strings.Fields(get(1))
	if s := get(2); s != "" {
		ids = append(ids, s)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, cid := range ids {
		cmds[i] = pipe.HGet(ctx, Key(cid), "where")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plan %s members: %w", id, err)
	}
	var live, flight []string
	for i, cid := range ids {
		switch cmds[i].Val() {
		case "", ws.Landed, ws.Done:
		case ws.Working, ws.Review, ws.Merging:
			flight = append(flight, cid+" ("+cmds[i].Val()+")")
		default:
			live = append(live, cid)
		}
	}
	if len(flight) > 0 {
		return nil, &Refused{Why: fmt.Sprintf("PLAN task:%s has children in flight: %s; end or cancel them first (card end, card cancel), then cancel the plan", id, strings.Join(flight, ", "))}
	}
	return live, nil
}

// DropChild drops a child that ended done/fail (a cancelled child) from its
// plan (nova-tools#4317): the parent's children lose it, the stitch's edges
// (blocked_on) lose it, both through the one move, and the stitch's brief is
// rewritten, so the plan is no longer stuck on it. A child still live, one
// that landed (its edge is met and it is part of the plan) or one with no
// plan is a *Refused naming the remedy. It returns the plan as it is now.
func DropChild(ctx context.Context, c redis.Cmdable, child, by string) (Plan, error) {
	rec, err := c.HMGet(ctx, Key(child), FieldParent, FieldPhase, "where", "where_ok").Result()
	if err != nil {
		return Plan{}, fmt.Errorf("task:%s: %w", child, err)
	}
	get := func(i int) string {
		s, _ := rec[i].(string)
		return s
	}
	parent, where := get(0), get(2)
	switch {
	case where == "":
		return Plan{}, &Refused{Why: "no task:" + child}
	case parent == "" || get(1) != PhaseChild:
		return Plan{}, &Refused{Why: "task:" + child + " is not a plan's child (no parent, or phase " + dashOf(get(1)) + ")"}
	case where == ws.Landed:
		return Plan{}, &Refused{Why: "task:" + child + " landed: its edge is met and it stays in plan " + parent}
	case where != ws.Done:
		return Plan{}, &Refused{Why: "task:" + child + " is " + where + ", not done: nova-sprint task cancel --id " + child + " --why <why> first, then drop it"}
	}
	p, err := ReadPlan(ctx, c, parent)
	if err != nil {
		return Plan{}, err
	}
	var keep []string
	for _, ch := range p.Children {
		if ch.ID != child {
			keep = append(keep, ch.ID)
		}
	}
	if len(keep) == len(p.Children) {
		return p, &Refused{Why: "task:" + child + " is not among plan " + parent + "'s children (" + strings.Join(keep, " ") + ")"}
	}
	why := "card stitch --drop " + child
	if err := setFields(ctx, c, parent, by, why, FieldChildren, strings.Join(keep, " ")); err != nil {
		return p, err
	}
	if p.Stitch.ID != "" {
		sb, err := c.HGet(ctx, Key(p.Stitch.ID), "blocked_on").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return p, fmt.Errorf("task:%s: %w", p.Stitch.ID, err)
		}
		var edges []string
		for _, e := range strings.FieldsFunc(sb, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
			if strings.TrimPrefix(e, "task:") != child {
				edges = append(edges, e)
			}
		}
		joined := strings.Join(edges, ",")
		if joined == "" {
			joined = "none"
		}
		if err := setFields(ctx, c, p.Stitch.ID, by, why, "blocked_on", joined, "depends_on", joined); err != nil {
			return p, err
		}
		if _, err := WriteStitchBrief(ctx, c, p.Stitch.ID); err != nil {
			return p, err
		}
	}
	return ReadPlan(ctx, c, parent)
}
