package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MachineSpec describes one host in the arbitrator's inventory.
type MachineSpec struct {
	Host        string
	Address     string
	User        string
	Online      bool
	Tags        []string
	UsableCores int
	Resources   map[string]int
	UsableCPUs  []int
	SMTGroups   [][]int
}

type leaseEntry struct {
	Seq         uint64
	LeaseID     string
	ReqID       string
	ConnID      uint64
	Sublease    bool
	ParentLease string
	Host        string
	Needs       map[string]int
	Exclusive   bool
	Holder      string
	Source      string
	Consumer    string
	Label       string
	Priority    string
	Why         string
	PID         int
	Ancestors   []int
	Cores       int
	CPUs        []int
	Paused      bool
	SinceMs     int64
}

type claimEntry struct {
	Seq         uint64
	LeaseID     string
	Host        string
	Needs       map[string]int
	Exclusive   bool
	Source      string
	Why         string
	SinceMs     int64
	ExpiresAtMs int64
}

type waiterEntry struct {
	Seq         uint64
	ReqID       string
	ConnID      uint64
	Mode        string
	Host        string
	Needs       map[string]int
	ReqTags     []string
	Exclusive   bool
	ParentLease string
	Holder      string
	Source      string
	Consumer    string
	Label       string
	Priority    string
	Why         string
	PID         int
	Ancestors   []int
	UsableCPUs  []int
	SMTGroups   [][]int
	SinceMs     int64
	WaitMs      int64
	DeadlineMs  int64
}

// OutMsg is an outbound message emitted by Engine; ConnID == 0 with Broadcast
// true targets subscribed observers matching EventHost.
type OutMsg struct {
	ConnID    uint64
	Broadcast bool
	EventHost string
	Frame     ServerFrame
}

// Engine is the deterministic central/local resource arbitrator (Arbitrator.tla).
type Engine struct {
	machines         map[string]*MachineSpec
	hostOrder        []string
	localAlias       string
	leases           []*leaseEntry
	claims           []*claimEntry
	queue            []*waiterEntry
	connLastMs       map[uint64]int64
	rrLast           map[int]string
	nextSeq          uint64
	nextClaimSeq     uint64
	reconnectUntilMs int64
	connDeadMs       int64
}

func NewEngine(machines []MachineSpec, startMs, reconnectGraceMs int64) *Engine {
	e := &Engine{
		machines:     map[string]*MachineSpec{},
		connLastMs:   map[uint64]int64{},
		rrLast:       map[int]string{},
		nextSeq:      1,
		nextClaimSeq: 1,
		connDeadMs:   defaultConnDeadMs,
	}
	if reconnectGraceMs > 0 {
		e.reconnectUntilMs = startMs + reconnectGraceMs
	}
	e.UpdateMachines(machines, startMs)
	return e
}

func fmtTimeMs(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func (e *Engine) allocSeq() uint64 {
	s := e.nextSeq
	e.nextSeq++
	return s
}

func (e *Engine) SetNextSeq(seq uint64) {
	if seq > e.nextSeq {
		e.nextSeq = seq
	}
}

func (e *Engine) bumpSeqPast(id string) uint64 {
	if rest, ok := strings.CutPrefix(id, "L-"); ok {
		if n, err := strconv.ParseUint(rest, 10, 64); err == nil && n > 0 {
			if n >= e.nextSeq {
				e.nextSeq = n + 1
			}
			return n
		}
	}
	if rest, ok := strings.CutPrefix(id, "C-"); ok {
		if n, err := strconv.ParseUint(rest, 10, 64); err == nil && n >= e.nextClaimSeq {
			e.nextClaimSeq = n + 1
		}
	}
	return 0
}

// UpdateMachines refreshes the machine inventory while keeping active leases intact.
func (e *Engine) UpdateMachines(machines []MachineSpec, nowMs int64) []OutMsg {
	seen := map[string]bool{}
	for _, in := range machines {
		if in.Host == "" {
			continue
		}
		seen[in.Host] = true
		cp := &MachineSpec{
			Host:        in.Host,
			Address:     in.Address,
			User:        in.User,
			Online:      in.Online,
			Tags:        slices.Clone(in.Tags),
			UsableCores: max(1, in.UsableCores),
			Resources:   maps.Clone(in.Resources),
		}
		if cp.Resources == nil {
			cp.Resources = map[string]int{}
		}
		slices.Sort(cp.Tags)
		cp.Tags = slices.Compact(cp.Tags)
		if prev, ok := e.machines[in.Host]; ok && len(in.UsableCPUs) == 0 {
			cp.UsableCPUs = prev.UsableCPUs
			cp.SMTGroups = prev.SMTGroups
		} else {
			cp.UsableCPUs = slices.Clone(in.UsableCPUs)
			cp.SMTGroups = slices.Clone(in.SMTGroups)
		}
		e.machines[in.Host] = cp
	}
	for h, m := range e.machines {
		if !seen[h] {
			hasActive := slices.ContainsFunc(e.leases, func(l *leaseEntry) bool { return l.Host == h })
			if hasActive {
				m.Online = false
			} else {
				delete(e.machines, h)
			}
		}
	}
	e.hostOrder = slices.Sorted(maps.Keys(e.machines))
	return e.reconcileAndSchedule(nowMs)
}

func (e *Engine) RegisterConn(connID uint64, nowMs int64) {
	e.connLastMs[connID] = nowMs
}

func (e *Engine) TouchConn(connID uint64, nowMs int64) {
	if _, ok := e.connLastMs[connID]; ok {
		e.connLastMs[connID] = nowMs
	}
}

// CloseConn drops a disconnected or expired peer and releases all its leases and queued waiters.
func (e *Engine) CloseConn(connID uint64, nowMs int64) []OutMsg {
	delete(e.connLastMs, connID)
	var out []OutMsg
	e.queue = slices.DeleteFunc(e.queue, func(w *waiterEntry) bool { return w.ConnID == connID })

	var dropIDs []string
	for _, l := range e.leases {
		if l.ConnID == connID {
			dropIDs = append(dropIDs, l.LeaseID)
		}
	}
	for _, id := range dropIDs {
		out = append(out, e.removeLeaseCascade(id, nowMs, "connection closed")...)
	}
	if len(dropIDs) > 0 {
		out = append(out, e.reconcileAndSchedule(nowMs)...)
	}
	return out
}

// Tick advances timers: expires silent connections, expires claims past TTL,
// closes the reconnect grace window, fails timed-out waiters, and schedules.
func (e *Engine) Tick(nowMs int64) []OutMsg {
	var out []OutMsg
	var deadConns []uint64
	for cid, last := range e.connLastMs {
		if e.connDeadMs > 0 && nowMs-last >= e.connDeadMs {
			deadConns = append(deadConns, cid)
		}
	}
	slices.Sort(deadConns)
	for _, cid := range deadConns {
		out = append(out, e.CloseConn(cid, nowMs)...)
	}

	changed := false
	if e.reconnectUntilMs > 0 && nowMs >= e.reconnectUntilMs {
		e.reconnectUntilMs = 0
		changed = true
	}

	var keptClaims []*claimEntry
	for _, c := range e.claims {
		if nowMs >= c.ExpiresAtMs {
			changed = true
			out = append(out, e.event("released", c.LeaseID, c.Host, "claim expired ("+c.LeaseID+")", nowMs))
		} else {
			keptClaims = append(keptClaims, c)
		}
	}
	e.claims = keptClaims

	var keptQueue []*waiterEntry
	for _, w := range e.queue {
		if w.DeadlineMs > 0 && nowMs >= w.DeadlineMs {
			changed = true
			msg := fmt.Sprintf("no hold within %s: %s", (time.Duration(w.WaitMs) * time.Millisecond).String(), e.blockerSummary(w))
			out = append(out, OutMsg{
				ConnID: w.ConnID,
				Frame:  ServerFrame{Op: "refused", ReqID: w.ReqID, Code: "timeout", Message: msg, Blockers: e.blockerList(w)},
			})
		} else {
			keptQueue = append(keptQueue, w)
		}
	}
	e.queue = keptQueue

	if changed {
		out = append(out, e.reconcileAndSchedule(nowMs)...)
	}
	return out
}

func (e *Engine) EndReconnectWindow(nowMs int64) []OutMsg {
	if e.reconnectUntilMs == 0 {
		return nil
	}
	e.reconnectUntilMs = 0
	return e.reconcileAndSchedule(nowMs)
}

func (e *Engine) Reconnect(connID uint64, f ClientFrame, nowMs int64) []OutMsg {
	e.TouchConn(connID, nowMs)
	var out []OutMsg
	for _, rl := range f.Leases {
		if rl.LeaseID == "" || rl.Host == "" {
			continue
		}
		m := e.machines[rl.Host]
		if m == nil {
			continue
		}
		seq := e.bumpSeqPast(rl.LeaseID)
		if existing := e.findLease(rl.LeaseID); existing != nil {
			existing.ConnID = connID
			out = append(out, OutMsg{ConnID: connID, Frame: e.grantedFrame(existing, m, "")})
			continue
		}
		if seq == 0 {
			seq = e.allocSeq()
		}
		needs := maps.Clone(rl.Needs)
		if len(needs) == 0 {
			needs = map[string]int{"cpu": 1, "memory": 1}
		}
		isSub := rl.ParentLease != ""
		paused := false
		if !isSub && rl.Holder != "ci" {
			used := e.sumUnpausedOn(rl.Host)
			for r, v := range needs {
				if used[r]+v > m.Resources[r] {
					paused = true
					break
				}
			}
		}
		cores := cmp.Or(rl.Cores, coresForHold(needs["cpu"], rl.Exclusive, m))
		l := &leaseEntry{
			Seq: seq, LeaseID: rl.LeaseID, ConnID: connID, Sublease: isSub,
			ParentLease: rl.ParentLease, Host: rl.Host, Needs: needs,
			Exclusive: rl.Exclusive, Holder: cmp.Or(rl.Holder, "person"),
			Source: cmp.Or(rl.Source, rl.Holder, "peer"), Consumer: rl.Consumer,
			Label: cmp.Or(rl.Label, "work"), Priority: cmp.Or(rl.Priority, "normal"),
			Why: rl.Why, PID: rl.PID, Ancestors: slices.Clone(rl.Ancestors),
			Cores: cores, CPUs: slices.Clone(rl.CPUs), Paused: paused, SinceMs: nowMs,
		}
		e.leases = append(e.leases, l)
		out = append(out, OutMsg{ConnID: connID, Frame: e.grantedFrame(l, m, "")})
		if paused {
			out = append(out, OutMsg{ConnID: connID, Frame: ServerFrame{
				Op: "state", LeaseID: l.LeaseID, State: "paused", Reason: "displaced during reconnect",
			}})
		}
		out = append(out, e.event("granted", l.LeaseID, l.Host, fmt.Sprintf("reconnected %s for %s", formatNeedsKV(l.Needs), l.Source), nowMs))
	}
	return out
}

func (e *Engine) Release(connID uint64, targetID string, nowMs int64) ([]OutMsg, bool) {
	e.TouchConn(connID, nowMs)
	if targetID == "" {
		return nil, false
	}
	if idx := slices.IndexFunc(e.claims, func(c *claimEntry) bool { return c.LeaseID == targetID }); idx >= 0 {
		c := e.claims[idx]
		e.claims = slices.Delete(e.claims, idx, idx+1)
		out := []OutMsg{
			{ConnID: connID, Frame: ServerFrame{Op: "state", LeaseID: c.LeaseID, State: "released", Reason: "released"}},
			e.event("released", c.LeaseID, c.Host, "released claim "+c.LeaseID, nowMs),
		}
		out = append(out, e.reconcileAndSchedule(nowMs)...)
		return out, true
	}
	if idx := slices.IndexFunc(e.queue, func(w *waiterEntry) bool { return w.ReqID == targetID && w.ConnID == connID }); idx >= 0 {
		e.queue = slices.Delete(e.queue, idx, idx+1)
		out := []OutMsg{{ConnID: connID, Frame: ServerFrame{Op: "state", ReqID: targetID, State: "released", Reason: "cancelled"}}}
		out = append(out, e.reconcileAndSchedule(nowMs)...)
		return out, true
	}
	if l := e.findLease(targetID); l != nil {
		out := e.removeLeaseCascade(targetID, nowMs, "released")
		out = append(out, OutMsg{ConnID: connID, Frame: ServerFrame{Op: "state", LeaseID: targetID, State: "released", Reason: "released"}})
		out = append(out, e.reconcileAndSchedule(nowMs)...)
		return out, true
	}
	return nil, false
}

func (e *Engine) SetLocalAlias(alias string) {
	e.localAlias = alias
}

func (e *Engine) normHost(h string) string {
	if h != "" && e.localAlias != "" && strings.EqualFold(h, e.localAlias) && e.machines["local"] != nil {
		return "local"
	}
	return h
}

func (e *Engine) PlaceClaim(connID uint64, f ClientFrame, nowMs int64) []OutMsg {
	e.TouchConn(connID, nowMs)
	reqID := cmp.Or(f.ReqID, "claim")
	host := e.normHost(f.Host)
	if host == "" && len(e.hostOrder) == 1 {
		host = e.hostOrder[0]
	}
	m := e.machines[host]
	if m == nil || !m.Online {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{
			Op: "refused", ReqID: reqID, Code: "impossible",
			Message: fmt.Sprintf("unknown or offline host %q", host),
		}}}
	}
	needs, _, code, msg := e.resolveNeeds("host", host, f.Needs, f.Exclusive)
	if code != "" {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{Op: "refused", ReqID: reqID, Code: code, Message: msg}}}
	}
	ttl := f.TTLMs
	if ttl <= 0 {
		ttl = defaultClaimTTLMs
	}
	seq := e.nextClaimSeq
	e.nextClaimSeq++
	cid := "C-" + strconv.FormatUint(seq, 10)
	exp := nowMs + ttl
	c := &claimEntry{
		Seq: seq, LeaseID: cid, Host: host, Needs: needs, Exclusive: f.Exclusive,
		Source: cmp.Or(f.Source, "person"), Why: f.Why, SinceMs: nowMs, ExpiresAtMs: exp,
	}
	e.claims = append(e.claims, c)
	cores := coresForHold(needs["cpu"], f.Exclusive, m)
	return []OutMsg{
		{ConnID: connID, Frame: ServerFrame{
			Op: "granted", ReqID: reqID, LeaseID: cid, Sublease: false, Host: host,
			Address: m.Address, User: m.User, Needs: maps.Clone(needs), Cores: cores,
			Env: buildLeaseEnv(cid, host, cores), ExpiresAt: fmtTimeMs(exp),
		}},
		e.event("claimed", cid, host, fmt.Sprintf("claimed %s on %s for %s", formatNeedsKV(needs), host, c.Source), nowMs),
	}
}

func (e *Engine) Request(connID uint64, f ClientFrame, nowMs int64) []OutMsg {
	e.TouchConn(connID, nowMs)
	reqID := cmp.Or(f.ReqID, "r"+strconv.FormatUint(e.nextSeq, 10))
	mode := f.Mode
	host := e.normHost(f.Host)
	if mode == "" {
		if host != "" {
			mode = "host"
		} else if len(e.hostOrder) == 1 && e.hostOrder[0] == "local" {
			mode, host = "host", "local"
		} else {
			mode = "any"
		}
	}
	if mode == "host" && host == "" && len(e.hostOrder) == 1 {
		host = e.hostOrder[0]
	}
	if mode != "any" && mode != "host" {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{
			Op: "refused", ReqID: reqID, Code: "bad_request", Message: fmt.Sprintf("invalid mode %q", mode),
		}}}
	}
	holder := cmp.Or(f.Holder, "person")
	if !slices.Contains([]string{"friend", "bench", "ci", "person", "sprint", "script"}, holder) {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{
			Op: "refused", ReqID: reqID, Code: "bad_request", Message: fmt.Sprintf("invalid holder %q", holder),
		}}}
	}
	prio := cmp.Or(f.Priority, "normal")
	if _, ok := prioRanks[prio]; !ok {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{
			Op: "refused", ReqID: reqID, Code: "bad_request", Message: fmt.Sprintf("invalid priority %q", prio),
		}}}
	}
	label := f.Label
	if label == "" {
		if holder == "ci" {
			label = "ci"
		} else {
			label = "work"
		}
	}
	source := cmp.Or(f.Source, holder)

	if mode == "host" && len(f.UsableCPUs) > 0 {
		if m := e.machines[host]; m != nil && len(m.UsableCPUs) == 0 {
			m.UsableCPUs = slices.Clone(f.UsableCPUs)
			m.SMTGroups = slices.Clone(f.SMTGroups)
		}
	}

	needs, reqTags, code, msg := e.resolveNeeds(mode, host, f.Needs, f.Exclusive)
	if code != "" {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{Op: "refused", ReqID: reqID, Code: code, Message: msg}}}
	}

	parentID := e.discoverParentLease(f.ParentLease, mode, host, f.PID, f.Ancestors)

	// 1. Sub-lease check: same-host request fitting within parent's remaining un-subleased grant (0 extra host debit).
	pausedSublease := false
	if parentID != "" && holder != "ci" {
		if par := e.findLease(parentID); par != nil && (mode == "any" || host == par.Host) {
			if m := e.machines[par.Host]; m != nil && m.Online && hasAllTags(m.Tags, reqTags) {
				reqN := e.needsForHost(needs, f.Exclusive, m)
				if e.fitsSublease(par, reqN) {
					if !par.Paused {
						return e.grantSublease(connID, reqID, par, m, reqN, f.Exclusive, holder, source, f.Consumer, label, prio, f.Why, f.PID, f.Ancestors, nowMs)
					}
					pausedSublease = true
				}
			}
		}
	}

	// 2. CI immediate grant: never waits, pauses overlapping active non-CI leases in victim order.
	if holder == "ci" {
		targetHost := host
		if targetHost == "" {
			cands := e.candidateMachines(mode, host, needs, reqTags, f.Exclusive)
			if len(cands) > 0 {
				targetHost = cands[0].Host
			}
		}
		m := e.machines[targetHost]
		if m == nil {
			return []OutMsg{{ConnID: connID, Frame: ServerFrame{
				Op: "refused", ReqID: reqID, Code: "impossible", Message: fmt.Sprintf("unknown host %q for ci hold", targetHost),
			}}}
		}
		ciNeeds := e.needsForHost(needs, f.Exclusive, m)
		var out []OutMsg
		out = append(out, e.displaceForCI(m, ciNeeds, nowMs)...)
		l := e.createTopLease(0, connID, reqID, m, ciNeeds, f.Exclusive, parentID, holder, source, f.Consumer, label, prio, f.Why, f.PID, f.Ancestors, nowMs)
		out = append(out,
			OutMsg{ConnID: connID, Frame: e.grantedFrame(l, m, "")},
			e.event("granted", l.LeaseID, m.Host, fmt.Sprintf("granted ci %s on %s", formatNeedsKV(ciNeeds), m.Host), nowMs),
		)
		return out
	}

	w := &waiterEntry{
		Seq: e.nextSeq, ReqID: reqID, ConnID: connID, Mode: mode, Host: host,
		Needs: needs, ReqTags: reqTags, Exclusive: f.Exclusive, ParentLease: parentID,
		Holder: holder, Source: source, Consumer: f.Consumer, Label: label, Priority: prio,
		Why: f.Why, PID: f.PID, Ancestors: slices.Clone(f.Ancestors),
		UsableCPUs: slices.Clone(f.UsableCPUs), SMTGroups: slices.Clone(f.SMTGroups),
		SinceMs: nowMs,
	}
	if f.WaitMs > 0 {
		w.WaitMs = f.WaitMs
		w.DeadlineMs = nowMs + f.WaitMs
	}

	// 3. Immediate top-level grant when not in reconnect window and a candidate host fits without HoL/pause conflict.
	if e.reconnectUntilMs == 0 && !pausedSublease {
		if m := e.pickImmediateHostForWaiter(w); m != nil {
			reqN := e.needsForHost(needs, f.Exclusive, m)
			l := e.createTopLease(0, connID, reqID, m, reqN, f.Exclusive, parentID, holder, source, f.Consumer, label, prio, f.Why, f.PID, f.Ancestors, nowMs)
			e.rrLast[priorityRank(prio)] = source
			return []OutMsg{
				{ConnID: connID, Frame: e.grantedFrame(l, m, "")},
				e.event("granted", l.LeaseID, m.Host, fmt.Sprintf("granted %s on %s to %s", formatNeedsKV(reqN), m.Host, source), nowMs),
			}
		}
	}

	// 4. Wait-for cycle check before queueing (even on --nowait when nested).
	if parentID != "" && !pausedSublease {
		if cycle, detail := e.detectDeadlock(w); len(cycle) > 0 {
			return []OutMsg{{ConnID: connID, Frame: ServerFrame{
				Op: "refused", ReqID: reqID, Code: "deadlock",
				Message: "RESERVE DEADLOCK: " + detail,
				Cycle:   cycle,
			}}}
		}
	}

	if f.Nowait {
		return []OutMsg{{ConnID: connID, Frame: ServerFrame{
			Op: "refused", ReqID: reqID, Code: "no_room",
			Message:  "NO ROOM: " + e.blockerSummary(w),
			Blockers: e.blockerList(w),
		}}}
	}

	w.Seq = e.allocSeq()
	e.queue = append(e.queue, w)
	pos := len(e.queue)
	return []OutMsg{
		{ConnID: connID, Frame: ServerFrame{
			Op: "queued", ReqID: reqID, Position: pos,
			Reason:   e.waitReason(w),
			Blockers: e.blockerList(w),
		}},
		e.event("queued", reqID, cmp.Or(host, "*"), fmt.Sprintf("queued %s for %s (pos %d)", formatNeedsKV(needs), source, pos), nowMs),
	}
}

func (e *Engine) resolveNeeds(mode, host string, spec NeedsSpec, excl bool) (map[string]int, []string, string, string) {
	items, err := spec.items()
	if err != nil {
		return nil, nil, "bad_request", err.Error()
	}
	if excl && len(items) > 0 {
		return nil, nil, "bad_request", "--exclusive takes every resource; --needs does not apply"
	}

	var scope []*MachineSpec
	if mode == "host" {
		m := e.machines[host]
		if m == nil || !m.Online {
			return nil, nil, "impossible", fmt.Sprintf("machine %q is unknown or offline", host)
		}
		scope = []*MachineSpec{m}
	} else {
		for _, h := range e.hostOrder {
			if m := e.machines[h]; m != nil && m.Online {
				scope = append(scope, m)
			}
		}
		if len(scope) == 0 {
			return nil, nil, "impossible", "no online machines in fleet"
		}
	}

	knownRes, knownTags := map[string]bool{}, map[string]bool{}
	for _, m := range scope {
		for r := range m.Resources {
			knownRes[r] = true
		}
		for _, t := range m.Tags {
			knownTags[t] = true
		}
	}

	needs := map[string]int{}
	var reqTags []string
	for _, it := range items {
		if it.hasNum {
			if knownTags[it.name] && !knownRes[it.name] {
				return nil, nil, "bad_request", fmt.Sprintf("tag %q takes no number", it.name)
			}
			if !knownRes[it.name] {
				return nil, nil, "impossible", fmt.Sprintf("machine has no resource %q", it.name)
			}
			needs[it.name] = it.amount
		} else if it.name == "cpu" || it.name == "memory" {
			needs[it.name] = 1
		} else if knownRes[it.name] && !knownTags[it.name] {
			needs[it.name] = 1
		} else if knownTags[it.name] {
			reqTags = append(reqTags, it.name)
		} else {
			return nil, nil, "impossible", fmt.Sprintf("machine lacks tag or resource %q", it.name)
		}
	}
	needs["cpu"] = cmp.Or(needs["cpu"], 1)
	needs["memory"] = cmp.Or(needs["memory"], 1)
	slices.Sort(reqTags)

	if excl {
		if mode == "host" {
			return maps.Clone(scope[0].Resources), nil, "", ""
		}
		return map[string]int{"cpu": 1, "memory": 1}, nil, "", ""
	}

	cands := e.candidateMachines(mode, host, needs, reqTags, false)
	if len(cands) == 0 {
		if mode == "host" {
			m := scope[0]
			for _, t := range reqTags {
				if !slices.Contains(m.Tags, t) {
					return nil, nil, "impossible", fmt.Sprintf("machine %s lacks tag %q", m.Host, t)
				}
			}
			for _, r := range slices.Sorted(maps.Keys(needs)) {
				if needs[r] > m.Resources[r] {
					return nil, nil, "impossible", fmt.Sprintf("needs %s %d exceeds machine capacity %d", r, needs[r], m.Resources[r])
				}
			}
		}
		return nil, nil, "impossible", fmt.Sprintf("no machine in fleet satisfies needs (%s) and tags (%s)", formatNeedsKV(needs), strings.Join(reqTags, ","))
	}
	return needs, reqTags, "", ""
}

func hasAllTags(have, want []string) bool {
	for _, t := range want {
		if !slices.Contains(have, t) {
			return false
		}
	}
	return true
}

func (e *Engine) needsForHost(needs map[string]int, excl bool, m *MachineSpec) map[string]int {
	if excl {
		return maps.Clone(m.Resources)
	}
	return maps.Clone(needs)
}

func (e *Engine) candidateMachines(mode, host string, needs map[string]int, reqTags []string, excl bool) []*MachineSpec {
	var out []*MachineSpec
	check := func(m *MachineSpec) {
		if m == nil || !m.Online || !hasAllTags(m.Tags, reqTags) {
			return
		}
		if !excl {
			for r, v := range needs {
				if v > m.Resources[r] {
					return
				}
			}
		}
		out = append(out, m)
	}
	if mode == "host" {
		check(e.machines[host])
		return out
	}
	for _, h := range e.hostOrder {
		check(e.machines[h])
	}
	return out
}

func (e *Engine) findLease(id string) *leaseEntry {
	for _, l := range e.leases {
		if l.LeaseID == id {
			return l
		}
	}
	return nil
}

func (e *Engine) discoverParentLease(explicitParent, mode, host string, pid int, ancestors []int) string {
	if explicitParent != "" && e.findLease(explicitParent) != nil {
		return explicitParent
	}
	checkHost := host
	if checkHost == "" && len(e.hostOrder) == 1 {
		checkHost = e.hostOrder[0]
	}
	if checkHost == "" {
		return ""
	}
	pids := make([]int, 0, 1+len(ancestors))
	if pid > 0 {
		pids = append(pids, pid)
	}
	pids = append(pids, ancestors...)
	for _, candPID := range pids {
		if candPID <= 1 {
			continue
		}
		// Match deepest enclosing lease on this host (highest Seq matching candPID).
		var best *leaseEntry
		for _, l := range e.leases {
			if l.Host == checkHost && l.PID > 0 && l.PID == candPID {
				if best == nil || l.Seq > best.Seq {
					best = l
				}
			}
		}
		if best != nil {
			return best.LeaseID
		}
	}
	return ""
}

func (e *Engine) fitsSublease(par *leaseEntry, reqN map[string]int) bool {
	subUsed := map[string]int{}
	for _, l := range e.leases {
		if l.Sublease && l.ParentLease == par.LeaseID {
			for r, v := range l.Needs {
				subUsed[r] += v
			}
		}
	}
	for r, v := range reqN {
		if subUsed[r]+v > par.Needs[r] {
			return false
		}
	}
	return true
}

func (e *Engine) grantSublease(connID uint64, reqID string, par *leaseEntry, m *MachineSpec, reqN map[string]int, excl bool, holder, source, consumer, label, prio, why string, pid int, ancestors []int, nowMs int64) []OutMsg {
	seq := e.allocSeq()
	lid := "L-" + strconv.FormatUint(seq, 10)
	cores := max(1, min(par.Cores, coresForHold(reqN["cpu"], excl, m)))
	var cpus []int
	if len(par.CPUs) > 0 {
		inUse := map[int]int{}
		for _, l := range e.leases {
			if l.Sublease && l.ParentLease == par.LeaseID {
				for _, c := range l.CPUs {
					inUse[c]++
				}
			}
		}
		cpus = chooseCPUs(par.CPUs, nil, cores, inUse)
	}
	l := &leaseEntry{
		Seq: seq, LeaseID: lid, ReqID: reqID, ConnID: connID, Sublease: true,
		ParentLease: par.LeaseID, Host: m.Host, Needs: reqN, Exclusive: excl,
		Holder: holder, Source: source, Consumer: consumer, Label: label, Priority: prio,
		Why: why, PID: pid, Ancestors: slices.Clone(ancestors), Cores: cores, CPUs: cpus, SinceMs: nowMs,
	}
	e.leases = append(e.leases, l)
	return []OutMsg{
		{ConnID: connID, Frame: e.grantedFrame(l, m, "")},
		e.event("granted", l.LeaseID, m.Host, fmt.Sprintf("granted sublease %s under %s to %s", formatNeedsKV(reqN), par.LeaseID, source), nowMs),
	}
}

func (e *Engine) sumUnpausedOn(host string) map[string]int {
	used := map[string]int{}
	for _, l := range e.leases {
		if l.Host == host && !l.Sublease && !l.Paused {
			for r, v := range l.Needs {
				used[r] += v
			}
		}
	}
	return used
}

func (e *Engine) sumClaimsOn(host string) map[string]int {
	claimed := map[string]int{}
	for _, c := range e.claims {
		if c.Host == host {
			for r, v := range c.Needs {
				claimed[r] += v
			}
		}
	}
	return claimed
}

func (e *Engine) hasPausedOn(host string) bool {
	return slices.ContainsFunc(e.leases, func(l *leaseEntry) bool {
		return l.Host == host && !l.Sublease && l.Paused
	})
}

func (e *Engine) fitsNewOn(m *MachineSpec, reqN map[string]int, excl bool) bool {
	if m == nil || !m.Online {
		return false
	}
	used := e.sumUnpausedOn(m.Host)
	claimed := e.sumClaimsOn(m.Host)
	if excl && (len(used) > 0 || len(claimed) > 0) {
		return false
	}
	for _, l := range e.leases {
		if l.Host == m.Host && !l.Sublease && !l.Paused && l.Exclusive {
			return false
		}
	}
	for r, v := range reqN {
		if used[r]+claimed[r]+v > m.Resources[r] {
			return false
		}
	}
	return true
}

func (e *Engine) fitsUnpauseOn(m *MachineSpec, l *leaseEntry) bool {
	if m == nil || !m.Online {
		return false
	}
	used := e.sumUnpausedOn(m.Host)
	if l.Exclusive && len(used) > 0 {
		return false
	}
	for _, o := range e.leases {
		if o.Host == m.Host && o.Seq != l.Seq && !o.Sublease && !o.Paused && o.Exclusive {
			return false
		}
	}
	for r, v := range l.Needs {
		if used[r]+v > m.Resources[r] {
			return false
		}
	}
	return true
}

func (e *Engine) hostScore(m *MachineSpec, reqN map[string]int) (int, int) {
	used := e.sumUnpausedOn(m.Host)
	claimed := e.sumClaimsOn(m.Host)
	activeCount := 0
	for _, l := range e.leases {
		if l.Host == m.Host && !l.Sublease && !l.Paused {
			activeCount++
		}
	}
	freeCPU := max(0, m.Resources["cpu"]-used["cpu"]-claimed["cpu"]-reqN["cpu"])
	return freeCPU, -activeCount
}

func (e *Engine) pickImmediateHostForWaiter(w *waiterEntry) *MachineSpec {
	cands := e.candidateMachines(w.Mode, w.Host, w.Needs, w.ReqTags, w.Exclusive)
	var best *MachineSpec
	bestScoreA, bestScoreB := -1, -1_000_000
	for _, m := range cands {
		if e.hasPausedOn(m.Host) {
			continue
		}
		reqN := e.needsForHost(w.Needs, w.Exclusive, m)
		if !e.fitsNewOn(m, reqN, w.Exclusive) || e.blockedByHoL(w, m) {
			continue
		}
		sa, sb := e.hostScore(m, reqN)
		if best == nil || sa > bestScoreA || (sa == bestScoreA && sb > bestScoreB) {
			best, bestScoreA, bestScoreB = m, sa, sb
		}
	}
	return best
}

func coresForHold(cpuNeed int, excl bool, m *MachineSpec) int {
	if excl || m.Resources["cpu"] <= 0 {
		return max(1, m.UsableCores)
	}
	return max(1, min(m.UsableCores, cpuNeed*m.UsableCores/m.Resources["cpu"]))
}

func (e *Engine) assignCPUs(m *MachineSpec, cores int, excl bool, skipSeq uint64) []int {
	if len(m.UsableCPUs) == 0 {
		return nil
	}
	if excl {
		return slices.Clone(m.UsableCPUs)
	}
	inUse := map[int]int{}
	for _, l := range e.leases {
		if l.Host == m.Host && !l.Sublease && !l.Paused && l.Seq != skipSeq {
			for _, cpu := range l.CPUs {
				inUse[cpu]++
			}
		}
	}
	return chooseCPUs(m.UsableCPUs, m.SMTGroups, cores, inUse)
}

func (e *Engine) createTopLease(presetSeq, connID uint64, reqID string, m *MachineSpec, reqN map[string]int, excl bool, parentID, holder, source, consumer, label, prio, why string, pid int, ancestors []int, nowMs int64) *leaseEntry {
	seq := presetSeq
	if seq == 0 {
		seq = e.allocSeq()
	}
	lid := "L-" + strconv.FormatUint(seq, 10)
	cores := coresForHold(reqN["cpu"], excl, m)
	cpus := e.assignCPUs(m, cores, excl, seq)
	l := &leaseEntry{
		Seq: seq, LeaseID: lid, ReqID: reqID, ConnID: connID, Sublease: false,
		ParentLease: parentID, Host: m.Host, Needs: maps.Clone(reqN), Exclusive: excl,
		Holder: holder, Source: source, Consumer: consumer, Label: label, Priority: prio,
		Why: why, PID: pid, Ancestors: slices.Clone(ancestors), Cores: cores, CPUs: cpus, SinceMs: nowMs,
	}
	e.leases = append(e.leases, l)
	return l
}

func (e *Engine) grantedFrame(l *leaseEntry, m *MachineSpec, expiresAt string) ServerFrame {
	return ServerFrame{
		Op: "granted", ReqID: l.ReqID, LeaseID: l.LeaseID, Sublease: l.Sublease,
		Host: l.Host, Address: m.Address, User: m.User, Needs: maps.Clone(l.Needs),
		Cores: l.Cores, CPUs: slices.Clone(l.CPUs),
		Env: buildLeaseEnv(l.LeaseID, l.Host, l.Cores), ExpiresAt: expiresAt,
	}
}

func (e *Engine) displaceForCI(m *MachineSpec, ciNeeds map[string]int, nowMs int64) []OutMsg {
	var out []OutMsg
	for {
		used := e.sumUnpausedOn(m.Host)
		var over []string
		for r, tot := range m.Resources {
			if used[r]+ciNeeds[r] > tot {
				over = append(over, r)
			}
		}
		var victim *leaseEntry
		for _, l := range e.leases {
			if l.Host != m.Host || l.Sublease || l.Paused || l.Holder == "ci" {
				continue
			}
			contributes := l.Exclusive || slices.ContainsFunc(over, func(r string) bool { return l.Needs[r] > 0 })
			if !contributes {
				continue
			}
			if victim == nil || priorityRank(l.Priority) < priorityRank(victim.Priority) ||
				(priorityRank(l.Priority) == priorityRank(victim.Priority) && l.Seq > victim.Seq) {
				victim = l
			}
		}
		if victim == nil {
			break
		}
		victim.Paused = true
		reason := fmt.Sprintf("displaced by ci on %s", m.Host)
		out = append(out,
			OutMsg{ConnID: victim.ConnID, Frame: ServerFrame{Op: "state", LeaseID: victim.LeaseID, State: "paused", Reason: reason}},
			e.event("paused", victim.LeaseID, m.Host, fmt.Sprintf("paused %s (%s) for ci", victim.LeaseID, victim.Source), nowMs),
		)
	}
	return out
}

func (e *Engine) removeLeaseCascade(leaseID string, nowMs int64, reason string) []OutMsg {
	var out []OutMsg
	var subIDs []string
	for _, l := range e.leases {
		if l.ParentLease == leaseID {
			subIDs = append(subIDs, l.LeaseID)
		}
	}
	for _, sid := range subIDs {
		out = append(out, e.removeLeaseCascade(sid, nowMs, reason)...)
	}
	idx := slices.IndexFunc(e.leases, func(l *leaseEntry) bool { return l.LeaseID == leaseID })
	if idx < 0 {
		return out
	}
	l := e.leases[idx]
	e.leases = slices.Delete(e.leases, idx, idx+1)
	out = append(out, e.event("released", l.LeaseID, l.Host, fmt.Sprintf("released %s on %s (%s)", l.LeaseID, l.Host, reason), nowMs))
	return out
}

func (e *Engine) betterWaiter(a, b *waiterEntry) bool {
	pa, pb := priorityRank(a.Priority), priorityRank(b.Priority)
	if pa != pb {
		return pa > pb
	}
	last := e.rrLast[pa]
	if last != "" && a.Source != b.Source {
		if a.Source != last && b.Source == last {
			return true
		}
		if a.Source == last && b.Source != last {
			return false
		}
	}
	return a.Seq < b.Seq
}

func (e *Engine) designateDrainingHost(w *waiterEntry) string {
	if w.Mode == "host" {
		return w.Host
	}
	cands := e.candidateMachines(w.Mode, w.Host, w.Needs, w.ReqTags, w.Exclusive)
	if len(cands) == 0 {
		return ""
	}
	return cands[0].Host
}

func (e *Engine) hasActiveAncestorOn(parentID, host string) bool {
	cur := parentID
	for range 16 {
		if cur == "" {
			return false
		}
		l := e.findLease(cur)
		if l == nil {
			return false
		}
		if !l.Sublease {
			return l.Host == host && !l.Paused
		}
		cur = l.ParentLease
	}
	return false
}

func (e *Engine) blockedByHoL(q *waiterEntry, m *MachineSpec) bool {
	// Never block a descendant of an active lease already occupying m.Host behind a
	// head-of-line waiter on m.Host: that HoL waiter cannot run until the ancestor
	// finishes, which requires its descendant to run first.
	if q.ParentLease != "" && e.hasActiveAncestorOn(q.ParentLease, m.Host) {
		return false
	}
	qNeeds := e.needsForHost(q.Needs, q.Exclusive, m)
	used := e.sumUnpausedOn(m.Host)
	claimed := e.sumClaimsOn(m.Host)
	for _, lead := range e.queue {
		if lead.Seq == q.Seq || !e.betterWaiter(lead, q) {
			continue
		}
		if e.designateDrainingHost(lead) != m.Host {
			continue
		}
		leadNeeds := e.needsForHost(lead.Needs, lead.Exclusive, m)
		if e.fitsNewOn(m, leadNeeds, lead.Exclusive) {
			continue
		}
		for r, tot := range m.Resources {
			if used[r]+claimed[r]+qNeeds[r]+leadNeeds[r] > tot {
				return true
			}
		}
	}
	return false
}

func (e *Engine) unpauseOne(nowMs int64) (*leaseEntry, []OutMsg) {
	var best *leaseEntry
	for _, l := range e.leases {
		if !l.Paused || l.Sublease {
			continue
		}
		m := e.machines[l.Host]
		if m == nil || !m.Online || !e.fitsUnpauseOn(m, l) {
			continue
		}
		if best == nil || priorityRank(l.Priority) > priorityRank(best.Priority) ||
			(priorityRank(l.Priority) == priorityRank(best.Priority) && l.Seq < best.Seq) {
			best = l
		}
	}
	if best == nil {
		return nil, nil
	}
	m := e.machines[best.Host]
	best.Paused = false
	best.CPUs = e.assignCPUs(m, best.Cores, best.Exclusive, best.Seq)
	out := []OutMsg{
		{ConnID: best.ConnID, Frame: ServerFrame{Op: "state", LeaseID: best.LeaseID, State: "active", Reason: "resources available"}},
		e.event("resumed", best.LeaseID, best.Host, fmt.Sprintf("resumed %s (%s) on %s", best.LeaseID, best.Source, best.Host), nowMs),
	}
	// Immediately wake any queued inner sub-step waiting on this parent lease's unpause.
	e.queue = slices.DeleteFunc(e.queue, func(q *waiterEntry) bool {
		if q.ParentLease != best.LeaseID || (q.Mode != "any" && q.Host != m.Host) || !hasAllTags(m.Tags, q.ReqTags) {
			return false
		}
		reqN := e.needsForHost(q.Needs, q.Exclusive, m)
		if !e.fitsSublease(best, reqN) {
			return false
		}
		out = append(out, e.grantSublease(q.ConnID, q.ReqID, best, m, reqN, q.Exclusive, q.Holder, q.Source, q.Consumer, q.Label, q.Priority, q.Why, q.PID, q.Ancestors, nowMs)...)
		return true
	})
	return best, out
}

func (e *Engine) admitOne(nowMs int64) (*leaseEntry, []OutMsg) {
	if e.reconnectUntilMs > 0 && nowMs < e.reconnectUntilMs {
		return nil, nil
	}
	var winIdx = -1
	var winHost *MachineSpec
	for i, q := range e.queue {
		if q.ParentLease != "" {
			if par := e.findLease(q.ParentLease); par != nil && par.Paused && (q.Mode == "any" || q.Host == par.Host) {
				if m := e.machines[par.Host]; m != nil && m.Online && hasAllTags(m.Tags, q.ReqTags) && e.fitsSublease(par, e.needsForHost(q.Needs, q.Exclusive, m)) {
					continue
				}
			}
		}
		if winIdx >= 0 && !e.betterWaiter(q, e.queue[winIdx]) {
			continue
		}
		cands := e.candidateMachines(q.Mode, q.Host, q.Needs, q.ReqTags, q.Exclusive)
		var bestM *MachineSpec
		bestA, bestB := -1, -1_000_000
		for _, m := range cands {
			if e.hasPausedOn(m.Host) {
				continue
			}
			reqN := e.needsForHost(q.Needs, q.Exclusive, m)
			if !e.fitsNewOn(m, reqN, q.Exclusive) || e.blockedByHoL(q, m) {
				continue
			}
			sa, sb := e.hostScore(m, reqN)
			if bestM == nil || sa > bestA || (sa == bestA && sb > bestB) {
				bestM, bestA, bestB = m, sa, sb
			}
		}
		if bestM != nil {
			winIdx, winHost = i, bestM
		}
	}
	if winIdx < 0 {
		return nil, nil
	}
	q := e.queue[winIdx]
	e.queue = slices.Delete(e.queue, winIdx, winIdx+1)
	if q.Mode == "host" && len(q.UsableCPUs) > 0 && len(winHost.UsableCPUs) == 0 {
		winHost.UsableCPUs = slices.Clone(q.UsableCPUs)
		winHost.SMTGroups = slices.Clone(q.SMTGroups)
	}
	reqN := e.needsForHost(q.Needs, q.Exclusive, winHost)
	l := e.createTopLease(q.Seq, q.ConnID, q.ReqID, winHost, reqN, q.Exclusive, q.ParentLease, q.Holder, q.Source, q.Consumer, q.Label, q.Priority, q.Why, q.PID, q.Ancestors, nowMs)
	e.rrLast[priorityRank(q.Priority)] = q.Source
	out := []OutMsg{
		{ConnID: q.ConnID, Frame: e.grantedFrame(l, winHost, "")},
		e.event("granted", l.LeaseID, winHost.Host, fmt.Sprintf("granted %s on %s to %s", formatNeedsKV(reqN), winHost.Host, q.Source), nowMs),
	}
	return l, out
}

func (e *Engine) reconcileAndSchedule(nowMs int64) []OutMsg {
	var out []OutMsg
	for {
		l, msgs := e.unpauseOne(nowMs)
		if l == nil {
			break
		}
		out = append(out, msgs...)
	}
	for {
		l, msgs := e.admitOne(nowMs)
		if l == nil {
			break
		}
		out = append(out, msgs...)
	}
	return out
}

// detectDeadlock performs fixed-point AND-OR reduction over active leases and
// queued waiters (including hypothetical waiter hyp) to detect wait-for cycles.
func (e *Engine) detectDeadlock(hyp *waiterEntry) ([]string, string) {
	allWaiters := make([]*waiterEntry, len(e.queue)+1)
	copy(allWaiters, e.queue)
	allWaiters[len(e.queue)] = hyp

	topAncestor := func(id string) string {
		cur := id
		for range 16 {
			l := e.findLease(cur)
			if l == nil || !l.Sublease || l.ParentLease == "" {
				return cur
			}
			cur = l.ParentLease
		}
		return cur
	}

	leaseUnstuck := map[string]bool{}
	waiterUnstuck := make([]bool, len(allWaiters))

	for {
		progressed := false
		for _, l := range e.leases {
			if l.Sublease || leaseUnstuck[l.LeaseID] {
				continue
			}
			hasStuckChild := false
			for wi, w := range allWaiters {
				if !waiterUnstuck[wi] && w.ParentLease != "" && topAncestor(w.ParentLease) == l.LeaseID {
					hasStuckChild = true
					break
				}
			}
			if !hasStuckChild {
				leaseUnstuck[l.LeaseID] = true
				progressed = true
			}
		}
		for wi, w := range allWaiters {
			if waiterUnstuck[wi] {
				continue
			}
			cands := e.candidateMachines(w.Mode, w.Host, w.Needs, w.ReqTags, w.Exclusive)
			canFit := false
			for _, m := range cands {
				stuckUsed := map[string]int{}
				anyStuckExcl := false
				for _, l := range e.leases {
					if l.Host == m.Host && !l.Sublease && !leaseUnstuck[l.LeaseID] {
						anyStuckExcl = anyStuckExcl || l.Exclusive
						for r, v := range l.Needs {
							stuckUsed[r] += v
						}
					}
				}
				reqN := e.needsForHost(w.Needs, w.Exclusive, m)
				if anyStuckExcl || (w.Exclusive && len(stuckUsed) > 0) {
					continue
				}
				ok := true
				for r, v := range reqN {
					if stuckUsed[r]+v > m.Resources[r] {
						ok = false
						break
					}
				}
				if ok {
					canFit = true
					break
				}
			}
			if canFit {
				waiterUnstuck[wi] = true
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	if waiterUnstuck[len(allWaiters)-1] {
		return nil, ""
	}

	// Extract a concrete stuck cycle starting at hyp for diagnostics.
	rootParent := topAncestor(hyp.ParentLease)
	var blockers []*leaseEntry
	for _, m := range e.candidateMachines(hyp.Mode, hyp.Host, hyp.Needs, hyp.ReqTags, hyp.Exclusive) {
		for _, l := range e.leases {
			if l.Host == m.Host && !l.Sublease && !leaseUnstuck[l.LeaseID] {
				blockers = append(blockers, l)
			}
		}
	}
	blocker := e.findLease(rootParent)
	if len(blockers) > 0 && !slices.ContainsFunc(blockers, func(l *leaseEntry) bool { return l.LeaseID == rootParent }) {
		blocker = blockers[0]
	}
	if blocker == nil {
		return []string{hyp.ReqID, hyp.ParentLease}, fmt.Sprintf("request %s by %s (%s) deadlocks with ancestor %s", hyp.ReqID, hyp.Source, formatNeedsKV(hyp.Needs), hyp.ParentLease)
	}
	cycle := []string{blocker.LeaseID, hyp.ReqID}
	if blocker.LeaseID != rootParent && rootParent != "" {
		cycle = []string{rootParent, hyp.ReqID, blocker.LeaseID}
	}
	why := cmp.Or(blocker.Why, blocker.Label)
	detail := fmt.Sprintf("request by %s (%s) waits on lease %s (holder=%s, source=%s, why=%s) which waits on descendant %s",
		hyp.Source, formatNeedsKV(hyp.Needs), blocker.LeaseID, blocker.Holder, blocker.Source, why, hyp.ReqID)
	return cycle, detail
}

func (e *Engine) blockerList(w *waiterEntry) []string {
	cands := e.candidateMachines(w.Mode, w.Host, w.Needs, w.ReqTags, w.Exclusive)
	candSet := map[string]bool{}
	for _, m := range cands {
		candSet[m.Host] = true
	}
	var out []string
	for _, l := range e.leases {
		if candSet[l.Host] && !l.Sublease {
			tag := cmp.Or(l.Why, l.Consumer, l.Label)
			out = append(out, fmt.Sprintf("%s (%s:%s on %s)", l.LeaseID, l.Source, tag, l.Host))
		}
	}
	for _, c := range e.claims {
		if candSet[c.Host] {
			out = append(out, fmt.Sprintf("%s (claim:%s on %s)", c.LeaseID, c.Source, c.Host))
		}
	}
	return out
}

func (e *Engine) blockerSummary(w *waiterEntry) string {
	bs := e.blockerList(w)
	if len(bs) == 0 && e.reconnectUntilMs > 0 {
		return "server reconnect window active"
	}
	return cmp.Or(strings.Join(bs, ", "), "resources busy")
}

func (e *Engine) waitReason(w *waiterEntry) string {
	if e.reconnectUntilMs > 0 {
		return "waiting for 15s server reconnect window"
	}
	target := cmp.Or(w.Host, "fleet")
	return fmt.Sprintf("waiting for %s on %s", formatNeedsKV(w.Needs), target)
}

func (e *Engine) event(typ, leaseID, host, summary string, nowMs int64) OutMsg {
	return OutMsg{
		Broadcast: true,
		EventHost: host,
		Frame: ServerFrame{
			Op: "event", Type: typ, LeaseID: leaseID, Host: host,
			Summary: summary, At: fmtTimeMs(nowMs),
		},
	}
}

// Snapshot builds the §8.2 snapshot frame, optionally filtered to one host.
func (e *Engine) Snapshot(hostFilter string, nowMs int64) ServerFrame {
	recUntil := ""
	if e.reconnectUntilMs > nowMs {
		recUntil = fmtTimeMs(e.reconnectUntilMs)
	}
	var machines []SnapshotMachine
	for _, h := range e.hostOrder {
		if hostFilter != "" && h != hostFilter {
			continue
		}
		m := e.machines[h]
		if m == nil {
			continue
		}
		used := e.sumUnpausedOn(h)
		claimed := e.sumClaimsOn(h)
		free := map[string]int{}
		for r, tot := range m.Resources {
			free[r] = tot - used[r] - claimed[r]
		}
		leases := []LeaseView{}
		for _, l := range e.leases {
			if l.Host == h {
				leases = append(leases, LeaseView{
					LeaseID: l.LeaseID, Sublease: l.Sublease, ParentLease: l.ParentLease,
					Host: l.Host, Needs: maps.Clone(l.Needs), Exclusive: l.Exclusive,
					Holder: l.Holder, Source: l.Source, Consumer: l.Consumer,
					Label: l.Label, Priority: l.Priority, Paused: l.Paused,
					Cores: l.Cores, CPUs: slices.Clone(l.CPUs), PID: l.PID,
					Since: fmtTimeMs(l.SinceMs), Why: l.Why,
				})
			}
		}
		claims := []ClaimView{}
		for _, c := range e.claims {
			if c.Host == h {
				claims = append(claims, ClaimView{
					LeaseID: c.LeaseID, Host: c.Host, Needs: maps.Clone(c.Needs),
					Exclusive: c.Exclusive, Source: c.Source,
					Since: fmtTimeMs(c.SinceMs), ExpiresAt: fmtTimeMs(c.ExpiresAtMs), Why: c.Why,
				})
			}
		}
		hQueue := []WaiterView{}
		for idx, w := range e.queue {
			if (w.Mode == "host" && w.Host == h) || (w.Mode == "any" && hostFilter == "") {
				if w.Mode == "any" && h != e.designateDrainingHost(w) {
					continue
				}
				hQueue = append(hQueue, WaiterView{
					ReqID: w.ReqID, Position: idx + 1, Mode: w.Mode, Host: w.Host,
					Needs: maps.Clone(w.Needs), Tags: slices.Clone(w.ReqTags),
					Exclusive: w.Exclusive, ParentLease: w.ParentLease,
					Holder: w.Holder, Source: w.Source, Consumer: w.Consumer,
					Label: w.Label, Priority: w.Priority, Since: fmtTimeMs(w.SinceMs), Why: w.Why,
				})
			}
		}
		machines = append(machines, SnapshotMachine{
			Host: m.Host, Address: m.Address, User: m.User, Online: m.Online,
			Tags: slices.Clone(m.Tags), UsableCores: m.UsableCores,
			Resources: maps.Clone(m.Resources), Free: free,
			Leases: leases, Claims: claims, Queue: hQueue,
		})
	}
	var allQueue []WaiterView
	for idx, w := range e.queue {
		if hostFilter != "" && w.Mode == "host" && w.Host != hostFilter {
			continue
		}
		allQueue = append(allQueue, WaiterView{
			ReqID: w.ReqID, Position: idx + 1, Mode: w.Mode, Host: w.Host,
			Needs: maps.Clone(w.Needs), Tags: slices.Clone(w.ReqTags),
			Exclusive: w.Exclusive, ParentLease: w.ParentLease,
			Holder: w.Holder, Source: w.Source, Consumer: w.Consumer,
			Label: w.Label, Priority: w.Priority, Since: fmtTimeMs(w.SinceMs), Why: w.Why,
		})
	}
	return ServerFrame{Op: "snapshot", ReconnectUntil: recUntil, Machines: machines, Queue: allQueue}
}

func (e *Engine) IsIdle() bool {
	return len(e.leases) == 0 && len(e.claims) == 0 && len(e.queue) == 0
}

func formatNeedsKV(needs map[string]int) string {
	var parts []string
	for _, r := range slices.Sorted(maps.Keys(needs)) {
		parts = append(parts, fmt.Sprintf("%s=%d", r, needs[r]))
	}
	return strings.Join(parts, " ")
}
