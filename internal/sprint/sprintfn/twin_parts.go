package sprintfn

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tick's parts on the twin (upper design version 2.1, 1.0 to 1.4 and
// 8.1's IT16, as errata 2, item 3 fixes the names): lease with the heartbeat
// as its field, pop, ingest, beat, clock and sprint with the tick end. Each is
// a Part of IT12's registry and has a Lua twin in sprint_parts.lua.
//
// Every part obeys the step's own rule (A4): its Pre reads what it needs
// through State.Keys with a check of each key's type, decides, and builds the
// whole list of its commands, which it validates with the validator prepare
// runs; its Cmds only hands that list back. A refusal can therefore come from
// Pre alone, before the table plan: the twin cannot find one after the Mem has
// written. Where two writes differ in kind, the write that records owed work
// comes first and the write that forgets its trigger last (A1): the agenda's
// ZADDs before the cursor's HSET and the due set's ZREM, the park before the
// agenda's ZREM.
//
// What a part leaves to the design's silence is the narrower reading, and is
// listed in the pull request. A step that carries a lease part and a part only
// the lease holder may run (pop, ingest) lets the lease part decide: the holder
// runs them, and another loop's step writes the idle fields alone (1.4.2, RT1:
// "lease held by another: done"). Without a lease part the step's generation is
// compared to the stored one (E4, T1).

// The spans, caps and bounds of the parts, each with the section that fixes it.
const (
	// BeatFreshMS is how long a beat keeps a member fresh: beat:<m> is due at
	// R + 15 s (1.2, 1.4.4).
	BeatFreshMS int64 = 15 * 1000
	// SprintMembersMax is the most members a beat carries, and the most
	// streams a drop marks: members and streams together are at most 250
	// (1.0 and 3, F1-20), so neither is more.
	SprintMembersMax = 250
	// HeldQueueCap is the most keys the held queue holds; ingest past it
	// drops the oldest (1.1, Order; the one exception to E3).
	HeldQueueCap = 100000
	// HeldDropMax is the most held keys one ingest drops. The oldest are read
	// as one bounded range head, which L1 7 caps at 2,000; a queue still over
	// the cap loses the rest at the next ingest.
	HeldDropMax = 2000
	// BeatLoadBytesMax is the longest load sample a beat carries: 1.4.4 gives
	// none, so the narrower reading is L1 6's bound on a name.
	BeatLoadBytesMax = tset.MaxIdentifierBytes
	// HeartbeatValueBytesMax is the longest value of a heartbeat field: 1.4.1
	// gives none, so the narrower reading is 1.0's "one field value" bound.
	HeartbeatValueBytesMax = tset.MaxFieldValueBytes

	// maxExactMS and maxExactSeq keep a time or a seq exact as a sorted-set
	// score (L2 2: 2^53 - 1).
	maxExactMS  int64 = 1<<53 - 1
	maxExactSeq       = 1<<53 - 1

	// partCommandsShare and partArgvShare are the half of L1 6's shared bounds
	// (65,536 planned commands, 8 MiB of argv, the table's, the log's, the
	// receipt's and the sprint's together) that the parts of one step may use;
	// the design fixes no share, so the narrower reading is the half.
	partCommandsShare = tset.MaxPlannedCommands / 2
	partArgvShare     = tset.MaxPlannedArgvBytes / 2
)

// The names of the sprint's keys the parts touch (1.1, 1.2, 1.3.1, 1.4.1). A
// name without "@" is a sprint key with no epoch; the per-epoch keys are named
// by epochKey.
const (
	keyLease       = "lease"
	keyHeartbeat   = "heartbeat"
	keyClock       = "clock"
	keyStrangers   = "strangers"
	keyCoordinator = "coordinator"
	keyBeatPrefix  = "beat:"

	keyAgenda     = "agenda"
	keyHeldQ      = "heldq"
	keyTick       = "tick"
	keyDue        = "due"
	keyCut        = "cut"
	keyParked     = "parked"
	keyNext       = "next"
	keyDropping   = "dropping"
	keyQuarantine = "quarantine"
)

// The fields of the lease hash (1.1).
const (
	leaseFieldOwner = "owner"
	leaseFieldName  = "name"
	leaseFieldUntil = "until_ms"
	leaseFieldGen   = "gen"
)

// The fields of the tick hash (1.1).
const (
	tickFieldCur    = "cur"
	tickFieldBehind = "behind_n"
)

// heartbeatHolderFields are the fields of the heartbeat the loop that holds the
// lease writes (1.4.1), and the only fields a lease part may carry. owner and
// gen are the lease part's own: it writes the lease's owner and generation
// whatever the loop sent, so a loop can never write another's identity, and
// looked_at is the call's time when the loop runs STOPPED (1.4.5). The idle
// loop's two fields, idle_loop and idle_at, are not in the list: a loop that
// does not hold the lease writes only those two (A3), and the part writes them.
var heartbeatHolderFields = []string{"agenda", "backlog", "due_now", "error", "failures", "gen", "heldq", "looked_at", "owner", "rules",
	"swept", "tick_at", "ticks"}

// HeartbeatFields are the fields of the heartbeat the loop that holds the
// lease may send in LeasePart.Heartbeat, in sorted order (1.4.1). The lease
// part replaces owner and gen with its own values (and looked_at, when the loop
// runs STOPPED); a loop that does not hold the lease writes only idle_loop and
// idle_at (A3).
func HeartbeatFields() []string { return append([]string(nil), heartbeatHolderFields...) }

// Heartbeat fields the part itself writes.
const (
	heartbeatOwner    = "owner"
	heartbeatGen      = "gen"
	heartbeatLookedAt = "looked_at"
	heartbeatIdleLoop = "idle_loop"
	heartbeatIdleAt   = "idle_at"
)

var callerHeartbeat = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range heartbeatHolderFields {
		m[f] = true
	}
	return m
}()

// RegisterTickParts registers the six parts on r, each under its name in
// 1.0. The write path's own registry has them from this package's init; a twin
// built with its own registry takes them with this call. It stops at the first
// refusal.
func RegisterTickParts(r *PartRegistry) error {
	for _, p := range []struct {
		name string
		part Part
	}{
		{PartLease, leasePart{}},
		{PartPop, popPart{}},
		{PartIngest, ingestPart{}},
		{PartBeat, beatPart{}},
		{PartClock, clockPart{}},
		{PartSprint, sprintPart{tickEnd: requestTickEnd}},
	} {
		if err := r.Register(p.name, p.part); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	if err := RegisterTickParts(defaultParts); err != nil {
		panic(err)
	}
}

// ---- shared helpers ----

// sprintKey is a sprint key with no epoch: {p} and the name (1.0).
func sprintKey(st *State, name string) string { return st.Names.Key(name) }

// epochKey is a per-epoch sprint key: {p}, the name and @<e>, "@0" included
// (0, row 1).
func epochKey(st *State, name string, epoch tset.Decimal) string {
	return st.Names.Key(name + "@" + string(epoch))
}

// partEpoch is the epoch the per-epoch keys of a step lie at: the request's,
// or its successor in a step that advances (L1 1.2's write_epoch).
func partEpoch(req *Request) tset.Decimal {
	for _, e := range req.Body.Entries {
		if e.Kind == "advance" {
			if next, err := tset.NextDecimal(req.Epoch); err == nil {
				return next
			}
		}
	}
	return req.Epoch
}

// partRefusal is a refusal of the parts' phase with its own words.
func partRefusal(code string, detail RefusalDetail, format string, args ...any) *Refusal {
	r := refuse(PhaseParts, code, detail)
	r.Message = fmt.Sprintf(format, args...) + "; nothing was changed"
	return r
}

// requestRefusal is the caller's fault: a malformed part.
func requestRefusal() *Refusal { return refuse(PhaseParts, CodeRequest, RefusalDetail{}) }

// storedRefusal is a stored value that is not what the parts write: the
// library and the store disagree, which refuses CONFIG.
func storedRefusal() *Refusal { return refuse(PhaseParts, CodeConfig, RefusalDetail{}) }

// typedKey is a key the part touches and the type it must have.
type typedKey struct{ key, kind string }

// guardTypes refuses WRONGTYPE when a key the part reads or writes holds
// another type than its own, before anything is planned (the pre stage reads
// every key with a command of its type, 1.0).
func guardTypes(st *State, keys ...typedKey) *Refusal {
	for _, k := range keys {
		if t := st.Keys.Type(k.key); t != kindNone && t != k.kind {
			return refuse(PhaseParts, CodeWrongType, RefusalDetail{})
		}
	}
	return nil
}

// partText says a name, key, note or cell the parts store is non-empty, at
// most one name long (L1 6), valid UTF-8 and free of control characters, so
// that the delimiters of a stored record can never be part of a value.
func partText(s string) bool {
	if s == "" || len(s) > tset.MaxIdentifierBytes || !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// partNow is the call's one TIME in ms.
func partNow(st *State) (int64, *Refusal) {
	now, err := strconv.ParseInt(string(st.NowMS), 10, 64)
	if err != nil || now < 0 || now > maxExactMS {
		return 0, storedRefusal()
	}
	return now, nil
}

// canonicalInt reads a non-negative integer in its canonical spelling.
func canonicalInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != s {
		return 0, false
	}
	return n, true
}

// hashInt is a non-negative integer field of a hash; an absent or empty field
// is zero, and a field that is not an integer is CONFIG.
func hashInt(st *State, key, field string) (int64, *Refusal) {
	s, ok := st.Keys.HGet(key, field)
	if !ok || s == "" {
		return 0, nil
	}
	n, ok := canonicalInt(s)
	if !ok {
		return 0, storedRefusal()
	}
	return n, nil
}

// hashDecimal is an exact decimal field of a hash; absent or empty is "0".
func hashDecimal(st *State, key, field string) (tset.Decimal, *Refusal) {
	s, ok := st.Keys.HGet(key, field)
	if !ok || s == "" {
		return "0", nil
	}
	if !tset.ValidDecimal(tset.Decimal(s)) {
		return "", storedRefusal()
	}
	return tset.Decimal(s), nil
}

// readClock is {p}clock as the pre stage reads it.
func readClock(st *State) (clockRecord, *Refusal) {
	key := sprintKey(st, keyClock)
	if ref := guardTypes(st, typedKey{key, kindHash}); ref != nil {
		return clockRecord{}, ref
	}
	var c clockRecord
	var ref *Refusal
	for _, f := range []struct {
		field string
		to    *int64
	}{{clockFieldStopped, &c.StoppedMs}, {clockFieldSince, &c.StoppedSinceMs}, {clockFieldHold, &c.StopHoldMs},
		{clockFieldDueSince, &c.DueSinceMs}, {clockFieldStopRaised, &c.StopRaisedMs}} {
		if *f.to, ref = hashInt(st, key, f.field); ref != nil {
			return clockRecord{}, ref
		}
	}
	return c, nil
}

// runningTime is R of the call: the clock's R at the call's one TIME (1.2).
func runningTime(st *State) (r, now int64, ref *Refusal) {
	if now, ref = partNow(st); ref != nil {
		return 0, 0, ref
	}
	c, ref := readClock(st)
	if ref != nil {
		return 0, 0, ref
	}
	return clockRunning(c, now), now, nil
}

// decimalOf is the canonical spelling of a time or a score.
func decimalOf(n int64) string { return strconv.FormatInt(n, 10) }

// flatPairs is a flat argv tail of the pairs of one HSET or ZADD.
type flatPairs []string

func (p flatPairs) count() int { return len(p) / 2 }

// hsetCommands writes the pairs of a hash in commands of at most maxPieces
// pairs (L1 1.4).
func hsetCommands(key string, p flatPairs) []Cmd {
	var out []Cmd
	for i := 0; i < len(p); i += 2 * maxPieces {
		out = append(out, Command("HSET", key, kindHash, p[i:min(i+2*maxPieces, len(p))]...))
	}
	return out
}

// zaddCommands adds the (score, member) pairs of a sorted set in pieces.
func zaddCommands(key string, p flatPairs) []Cmd {
	var out []Cmd
	for i := 0; i < len(p); i += 2 * maxPieces {
		out = append(out, Command("ZADD", key, kindZSet, p[i:min(i+2*maxPieces, len(p))]...))
	}
	return out
}

// zremCommands removes members of a sorted set in pieces of at most
// maxPieces.
func zremCommands(key string, members []string) []Cmd {
	var out []Cmd
	for i := 0; i < len(members); i += maxPieces {
		out = append(out, Command("ZREM", key, kindZSet, members[i:min(i+maxPieces, len(members))]...))
	}
	return out
}

// hdelCommands removes fields of a hash in pieces of at most maxPieces.
func hdelCommands(key string, fields []string) []Cmd {
	var out []Cmd
	for i := 0; i < len(fields); i += maxPieces {
		out = append(out, Command("HDEL", key, kindHash, fields[i:min(i+maxPieces, len(fields))]...))
	}
	return out
}

// checkCommands runs prepare's own validator over the part's commands, in the
// pre stage: a command the twin's prepare would refuse is refused here, before
// the table plan (A4), and the part's share of L1 6's shared bounds is held.
func checkCommands(st *State, cmds []Cmd) *Refusal {
	cost, ref := st.Keys.ks.check(st.Prefix, cmds)
	if ref != nil {
		ref.Phase = PhaseParts
		return ref
	}
	switch {
	case cost.commands > partCommandsShare:
		return refuse(PhaseParts, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "planned_commands"}})
	case cost.argvBytes > partArgvShare:
		return refuse(PhaseParts, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "planned_argv_bytes"}})
	}
	return nil
}

// planCmds is the list of commands a part built in its Pre.
type planCmds interface{ commands() []Cmd }

// cmdsOf hands back the commands of a part's plan; a plan of another type is
// the twin assembled wrong and refuses CONFIG (never reached by a twin that
// calls Cmds with the plan Pre returned).
func cmdsOf(plan any) ([]Cmd, *Refusal) {
	p, ok := plan.(planCmds)
	if !ok {
		return nil, storedRefusal()
	}
	return p.commands(), nil
}

// ---- the lease part (1.1) ----

// leaseDecision is what the lease part decides: who holds the lease after the
// step, at which generation and until when.
type leaseDecision struct {
	held    bool // this loop holds the lease after the step
	took    bool // the generation moved: a take, not a renewal
	gen     tset.Decimal
	owner   string
	name    string
	untilMS int64
}

// checkLeasePart holds a lease part to its shape: a token, a display name, a
// positive span that stays exact, and heartbeat fields the loop may write.
func checkLeasePart(l *LeasePart) *Refusal {
	if !partText(l.Owner) || (l.Name != "" && !partText(l.Name)) || l.HoldMS < 1 || l.HoldMS > maxExactMS {
		return requestRefusal()
	}
	for f, v := range l.Heartbeat {
		if !callerHeartbeat[f] || len(v) > HeartbeatValueBytesMax || !utf8.ValidString(v) {
			return requestRefusal()
		}
	}
	return nil
}

// decideLease is the lease part's whole decision, a pure function of the
// request and the lease as the pre stage reads it (1.1): the lease is taken
// when it is free or expired (gen + 1) and renewed for its own token; held by
// another token it is left alone. The parts that only the holder may run call
// it too, so they know, without the lease part's plan, whether this step's loop
// holds the lease.
func decideLease(st *State, l *LeasePart) (leaseDecision, *Refusal) {
	if ref := checkLeasePart(l); ref != nil {
		return leaseDecision{}, ref
	}
	key := sprintKey(st, keyLease)
	if ref := guardTypes(st, typedKey{key, kindHash}); ref != nil {
		return leaseDecision{}, ref
	}
	now, ref := partNow(st)
	if ref != nil {
		return leaseDecision{}, ref
	}
	owner, _ := st.Keys.HGet(key, leaseFieldOwner)
	name, _ := st.Keys.HGet(key, leaseFieldName)
	until, ref := hashInt(st, key, leaseFieldUntil)
	if ref != nil {
		return leaseDecision{}, ref
	}
	gen, ref := hashDecimal(st, key, leaseFieldGen)
	if ref != nil {
		return leaseDecision{}, ref
	}
	if owner == "" || until <= now { // free or expired: a take, at the next generation
		next, err := tset.NextDecimal(gen)
		if err != nil {
			return leaseDecision{}, refuse(PhaseParts, "OVERFLOW", RefusalDetail{})
		}
		return leaseDecision{held: true, took: true, gen: next, owner: l.Owner, name: l.Name, untilMS: now + l.HoldMS}, nil
	}
	if owner == l.Owner { // its own token, still held: a renewal at the same generation
		return leaseDecision{held: true, gen: gen, owner: l.Owner, name: l.Name, untilMS: now + l.HoldMS}, nil
	}
	return leaseDecision{gen: gen, owner: owner, name: name, untilMS: until}, nil
}

// tickAuthority says whether a part only the lease holder may run is to run in
// this step (E4, T1). With a lease part in the request the lease part decides:
// the holder runs it, and another loop's step is left to write the idle fields
// alone. Without one, the step's generation must be the stored one, and the
// part is refused STALEGEN otherwise; generation 0 is never current, since the
// first take is generation 1.
func tickAuthority(st *State, req *Request) (bool, *Refusal) {
	if req.Lease != nil {
		d, ref := decideLease(st, req.Lease)
		if ref != nil {
			return false, ref
		}
		return d.held, nil
	}
	key := sprintKey(st, keyLease)
	if ref := guardTypes(st, typedKey{key, kindHash}); ref != nil {
		return false, ref
	}
	gen, ref := hashDecimal(st, key, leaseFieldGen)
	if ref != nil {
		return false, ref
	}
	if req.Meta.Gen != 0 && strconv.FormatUint(req.Meta.Gen, 10) == string(gen) {
		return true, nil
	}
	holder, _ := st.Keys.HGet(key, leaseFieldName)
	return false, partRefusal(CodeStaleGen, RefusalDetail{},
		"the tick's lease is at generation %s, held by %s; this loop is not the tick", gen, holder)
}

// leasePlan is the lease part's decision and its reply: who holds the lease.
type leasePlan struct {
	Held    bool         `json:"held"`
	Took    bool         `json:"took"`
	Gen     tset.Decimal `json:"gen"`
	Owner   string       `json:"owner"`
	Name    string       `json:"name"`
	UntilMS tset.Decimal `json:"until_ms"`
	cmds    []Cmd
}

func (p *leasePlan) commands() []Cmd { return p.cmds }

// leasePart is the lease part (1.1, 1.4.1).
type leasePart struct{}

// Pre decides the lease and builds its commands: the holder writes the lease
// hash and its heartbeat fields by field (A3); another loop's step writes
// idle_loop and idle_at and nothing else, and its reply says who holds.
func (leasePart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	l := req.Lease
	d, ref := decideLease(st, l)
	if ref != nil {
		return nil, ref
	}
	now, _ := partNow(st)
	hb := sprintKey(st, keyHeartbeat)
	if ref := guardTypes(st, typedKey{hb, kindHash}); ref != nil {
		return nil, ref
	}
	plan := &leasePlan{Held: d.held, Took: d.took, Gen: d.gen, Owner: d.owner, Name: d.name, UntilMS: tset.Decimal(decimalOf(d.untilMS))}
	if !d.held {
		plan.cmds = hsetCommands(hb, flatPairs{heartbeatIdleLoop, l.Name, heartbeatIdleAt, decimalOf(now)})
	} else {
		plan.cmds = hsetCommands(sprintKey(st, keyLease), flatPairs{leaseFieldOwner, d.owner, leaseFieldName, d.name,
			leaseFieldUntil, decimalOf(d.untilMS), leaseFieldGen, string(d.gen)})
		fields := map[string]string{}
		for f, v := range l.Heartbeat {
			fields[f] = v
		}
		fields[heartbeatOwner], fields[heartbeatGen] = d.owner, string(d.gen)
		if l.Stopped {
			fields[heartbeatLookedAt] = decimalOf(now)
		}
		names := make([]string, 0, len(fields))
		for f := range fields {
			names = append(names, f)
		}
		sort.Strings(names)
		var p flatPairs
		for _, f := range names {
			p = append(p, f, fields[f])
		}
		plan.cmds = append(plan.cmds, hsetCommands(hb, p)...)
	}
	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// Cmds hands back the commands Pre built.
func (leasePart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }

// ---- the pop part (1.2) ----

// popPlan is the pop part's reply: what was due and is now queued.
type popPlan struct {
	Skipped bool         `json:"skipped"`
	R       tset.Decimal `json:"r,omitempty"`
	Popped  int          `json:"popped"`
	Due     int          `json:"due"`
	Cut     int          `json:"cut"`
	cmds    []Cmd
}

func (p *popPlan) commands() []Cmd { return p.cmds }

// lateKinds are the due kinds whose key is late:<kind>:<id> (1.2); beat:<m>
// queues down:<m>, and every other kind keeps its name.
var lateKinds = map[string]bool{"untaken": true, "unfinished": true, "unbegun": true, "unreported": true,
	"mergeidle": true, "idle": true}

// popKey is the agenda key a due entry queues (1.2): a cut entry cut:<op> of
// {p}cut@e queues late:cut:<op>.
func popKey(member string, cut bool) string {
	if cut {
		return "late:" + member
	}
	kind, id, ok := strings.Cut(member, ":")
	switch {
	case !ok:
		return member // behind
	case lateKinds[kind]:
		return "late:" + member
	case kind == "beat":
		return "down:" + id
	}
	return member // overdue, hold, remind and seen, and any kind 1.2 does not know
}

// popPart is the pop part (1.2, 1.1's order, A1).
type popPart struct{}

// Pre takes the due entries at or below R and the cut entries at or below
// wall time, at most the limit together, the cut entries first (they are few,
// so the due entries cannot starve them and they cannot starve the due
// entries for more than a tick), and builds the commands in A1's order: every
// key added to the agenda, scored by cur, then the due and cut entries removed.
// Each key is added only when it is not there (ZADD NX, decided here, since
// Layer 1's registry has no NX), so the earliest order stays.
func (popPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	pop := req.Pop
	if pop.Limit < 1 || pop.Limit > PopMax {
		return nil, requestRefusal()
	}
	run, ref := tickAuthority(st, req)
	if ref != nil {
		return nil, ref
	}
	epoch := partEpoch(req)
	agenda, due, cut, tick := epochKey(st, keyAgenda, epoch), epochKey(st, keyDue, epoch), epochKey(st, keyCut, epoch), epochKey(st, keyTick, epoch)
	if ref := guardTypes(st, typedKey{agenda, kindZSet}, typedKey{due, kindZSet}, typedKey{cut, kindZSet},
		typedKey{tick, kindHash}); ref != nil {
		return nil, ref
	}
	if !run {
		return &popPlan{Skipped: true}, nil
	}
	r, wall, ref := runningTime(st)
	if ref != nil {
		return nil, ref
	}
	cur, ref := hashDecimal(st, tick, tickFieldCur)
	if ref != nil {
		return nil, ref
	}
	cutEntries := st.Keys.ZRangeByScore(cut, math.Inf(-1), float64(wall), pop.Limit)
	var dueEntries []ZMember
	if room := pop.Limit - len(cutEntries); room > 0 { // a limit of 0 would read every entry
		dueEntries = st.Keys.ZRangeByScore(due, math.Inf(-1), float64(r), room)
	}
	plan := &popPlan{R: tset.Decimal(decimalOf(r)), Popped: len(cutEntries) + len(dueEntries), Due: len(dueEntries), Cut: len(cutEntries)}
	var add flatPairs
	queued := map[string]bool{}
	enqueue := func(entries []ZMember, cutSet bool) {
		for _, e := range entries {
			k := popKey(e.Member, cutSet)
			if _, there := st.Keys.ZScore(agenda, k); there || queued[k] {
				continue
			}
			queued[k] = true
			add = append(add, string(cur), k)
		}
	}
	enqueue(cutEntries, true)
	enqueue(dueEntries, false)
	plan.cmds = zaddCommands(agenda, add) // the keys first (A1)
	plan.cmds = append(plan.cmds, zremCommands(due, membersOf(dueEntries))...)
	plan.cmds = append(plan.cmds, zremCommands(cut, membersOf(cutEntries))...) // the entries last
	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

func membersOf(z []ZMember) []string {
	out := make([]string, len(z))
	for i, m := range z {
		out[i] = m.Member
	}
	return out
}

// Cmds hands back the commands Pre built.
func (popPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }

// ---- the ingest part (1.1) ----

// ingestPlan is the ingest part's reply: the new cursor (1.1: "its reply
// carries the new cur").
type ingestPlan struct {
	Skipped bool         `json:"skipped"`
	Cur     tset.Decimal `json:"cur"`
	Added   int          `json:"added"`
	Dropped int          `json:"dropped"`
	cmds    []Cmd
}

func (p *ingestPlan) commands() []Cmd { return p.cmds }

// ingestPart is the ingest part (1.1, 1.0's A1).
type ingestPart struct{}

// heldRule is the rule whose keys lie in the held queue (2.1, R16).
const heldRule = "held"

// checkIngest holds an ingest to its shape: decimals, a page that moves the
// cursor forward, and keys that a line of the page queued.
func checkIngest(in *IngestPart) (map[string]uint64, *Refusal) {
	if !tset.ValidDecimal(in.From) || !tset.ValidDecimal(in.To) || compareDecimal(in.To, in.From) < 0 ||
		compareDecimal(in.To, tset.Decimal(strconv.FormatUint(maxExactSeq, 10))) > 0 {
		return nil, requestRefusal()
	}
	if in.From == in.To && len(in.Keys) != 0 {
		return nil, requestRefusal() // keys with no line to have queued them
	}
	from, _ := strconv.ParseUint(string(in.From), 10, 64)
	to, _ := strconv.ParseUint(string(in.To), 10, 64)
	first := map[string]uint64{}
	for _, k := range in.Keys {
		if !partText(k.Key) || k.Seq <= from || k.Seq > to {
			return nil, requestRefusal()
		}
		if s, ok := first[k.Key]; !ok || k.Seq < s {
			first[k.Key] = k.Seq // a key named twice keeps its earliest order
		}
	}
	return first, nil
}

// Pre refuses unless the loop may ingest (STALEGEN) and cur equals From
// (INGESTAT, whose detail carries cur as an exact decimal), then builds the
// commands in A1's order: every key added to the agenda or the held queue, the
// oldest held keys dropped past the queue's cap, and only then the cursor moved
// to To, so an error between the writes leaves the keys owed twice and never
// lost (E7). A key is added only when absent (ZADD NX decided here).
func (ingestPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	in := req.Ingest
	keys, ref := checkIngest(in)
	if ref != nil {
		return nil, ref
	}
	run, ref := tickAuthority(st, req)
	if ref != nil {
		return nil, ref
	}
	epoch := partEpoch(req)
	agenda, heldq, tick := epochKey(st, keyAgenda, epoch), epochKey(st, keyHeldQ, epoch), epochKey(st, keyTick, epoch)
	if ref := guardTypes(st, typedKey{agenda, kindZSet}, typedKey{heldq, kindZSet}, typedKey{tick, kindHash}); ref != nil {
		return nil, ref
	}
	cur, ref := hashDecimal(st, tick, tickFieldCur)
	if ref != nil {
		return nil, ref
	}
	if !run {
		return &ingestPlan{Skipped: true, Cur: cur}, nil
	}
	if cur != in.From {
		return nil, partRefusal(CodeIngestAt, RefusalDetail{Cur: cur},
			"the cursor is at %s and this ingest starts at %s: another loop ingested", cur, in.From)
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if keys[names[i]] != keys[names[j]] {
			return keys[names[i]] < keys[names[j]]
		}
		return names[i] < names[j]
	})
	var addAgenda, addHeld flatPairs
	newHeld := 0
	for _, k := range names {
		score := strconv.FormatUint(keys[k], 10)
		if sprint.RuleOf(k) == heldRule {
			if _, there := st.Keys.ZScore(heldq, k); !there {
				addHeld = append(addHeld, score, k)
				newHeld++
			}
		} else if _, there := st.Keys.ZScore(agenda, k); !there {
			addAgenda = append(addAgenda, score, k)
		}
	}
	plan := &ingestPlan{Cur: in.To, Added: addAgenda.count() + addHeld.count()}
	var drop []string
	if over := st.Keys.ZCard(heldq) + newHeld - HeldQueueCap; over > 0 {
		for _, m := range st.Keys.ZRangeByScore(heldq, math.Inf(-1), math.Inf(1), min(over, HeldDropMax)) {
			drop = append(drop, m.Member)
		}
		plan.Dropped = len(drop)
	}
	plan.cmds = zaddCommands(agenda, addAgenda) // the keys first (A1)
	plan.cmds = append(plan.cmds, zaddCommands(heldq, addHeld)...)
	plan.cmds = append(plan.cmds, zremCommands(heldq, drop)...)
	if in.To != in.From {
		plan.cmds = append(plan.cmds, hsetCommands(tick, flatPairs{tickFieldCur, string(in.To)})...) // the cursor last
	}
	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// Cmds hands back the commands Pre built.
func (ingestPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }

// ---- the beat part (1.4.4) ----

// beatPlan is the beat part's reply: when the beat stays fresh until, and the
// members it entered in seen:<m> and the strangers it noticed.
type beatPlan struct {
	FreshUntil tset.Decimal `json:"fresh_until"`
	Members    int          `json:"members"`
	Seen       []string     `json:"seen"`
	Strangers  []string     `json:"strangers"`
	cmds       []Cmd
}

func (p *beatPlan) commands() []Cmd { return p.cmds }

// beatPart is the beat part (1.4.4).
type beatPart struct{}

// memberStatus is the field of a member's control card that says whether the
// member is up, down or held (1.3.1).
const memberStatus = "status"

// storedControlID is the id of a member's control card as the fleet table
// holds it at an epoch (errata E1; L1 1.2).
func storedControlID(epoch tset.Decimal, member string) (string, bool) {
	e, err := strconv.ParseUint(string(epoch), 10, 64)
	if err != nil {
		return "", false
	}
	return sprint.StoredID(sprint.CtlID(member), e), true
}

// Pre writes, for each member of the beat, its record (store ms and load),
// moves beat:<m> in the due set to R + 15 s, and, when the member's control
// card says down (read in the pre stage; held does not count) or the member has
// no fleet row, enters seen:<m> at R; a stranger is also noted in {p}strangers
// once (HSETNX). Both "only when absent" decisions are made here from the state
// the pre stage read, as ZADD NX and HSETNX would make them. The control cards
// come from S.before: PartsBefore names them, and a step whose before hook did
// not ask for them is refused CONFIG, never read as "no fleet row".
func (beatPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	b := req.Beat
	if len(b.Members) == 0 || len(b.Members) > SprintMembersMax {
		return nil, requestRefusal()
	}
	members := append([]BeatMember(nil), b.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].Member < members[j].Member })
	for i, m := range members {
		if !sprint.ValidID(m.Member) || len(m.Load) > BeatLoadBytesMax || !utf8.ValidString(m.Load) ||
			strings.ContainsAny(m.Load, "\x00\r\n") || (i > 0 && members[i-1].Member == m.Member) {
			return nil, requestRefusal()
		}
	}
	epoch := partEpoch(req)
	due, strangers := epochKey(st, keyDue, epoch), sprintKey(st, keyStrangers)
	guards := []typedKey{{due, kindZSet}, {strangers, kindHash}}
	for _, m := range members {
		guards = append(guards, typedKey{sprintKey(st, keyBeatPrefix+m.Member), kindHash})
	}
	if ref := guardTypes(st, guards...); ref != nil {
		return nil, ref
	}
	r, now, ref := runningTime(st)
	if ref != nil {
		return nil, ref
	}
	plan := &beatPlan{FreshUntil: tset.Decimal(decimalOf(r + BeatFreshMS)), Members: len(members), Seen: []string{}, Strangers: []string{}}
	var zadd, noticed flatPairs
	for _, m := range members {
		id, ok := storedControlID(req.Epoch, m.Member)
		if !ok {
			return nil, requestRefusal()
		}
		ctl, asked := obs.Record(sprint.Fleet, id)
		if !asked {
			return nil, storedRefusal() // the control card was not asked for in the pre stage
		}
		plan.cmds = append(plan.cmds, hsetCommands(sprintKey(st, keyBeatPrefix+m.Member), flatPairs{"at_ms", decimalOf(now), "load", m.Load})...)
		zadd = append(zadd, decimalOf(r+BeatFreshMS), "beat:"+m.Member)
		stranger := !ctl.Exists
		if status := ctl.Fields[memberStatus]; stranger || (status.Present && status.Value == sprint.Down) {
			if _, there := st.Keys.ZScore(due, "seen:"+m.Member); !there {
				zadd = append(zadd, decimalOf(r), "seen:"+m.Member)
				plan.Seen = append(plan.Seen, m.Member)
			}
		}
		if stranger {
			if _, there := st.Keys.HGet(strangers, m.Member); !there {
				noticed = append(noticed, m.Member, decimalOf(now))
				plan.Strangers = append(plan.Strangers, m.Member)
			}
		}
	}
	plan.cmds = append(plan.cmds, zaddCommands(due, zadd)...)
	plan.cmds = append(plan.cmds, hsetCommands(strangers, noticed)...)
	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// Cmds hands back the commands Pre built.
func (beatPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }
