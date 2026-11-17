package main

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Deterministic walks of Engine (cmd/nova-reserve/engine.go) bound directly to
// tla/Arbitrator.tla via tla/ArbitratorTrace.tla and tla/ArbitratorLog.tla.
// Every state variable is read off the live Engine fields after each step.

var traceRequiredOps = []string{
	"RequestWork", "RequestCI", "UnpauseStep", "AdmitQueued", "Release",
	"ClearRefused", "PlaceClaim", "ExpireClaim", "ServerRestart", "Reconnect", "EndReconnect",
}

type actorMeta struct {
	refusedDeadlock bool
	mode            string
	reqHost         int
	needsCPU        int
	needsMem        int
	prio            int
	parent          int
	lastSeq         uint64
	starts          int
}

type pendingReconnect struct {
	rl      ReconnectLease
	mode    string
	reqHost int
	prio    int
	parent  int
	seq     uint64
	paused  bool
	sub     bool
}

type arbWalk struct {
	t          *testing.T
	eng        *Engine
	nowMs      int64
	actors     [6]actorMeta
	inFlight   map[int]pendingReconnect
	claimsUsed int
	restartSeq uint64
	restarts   int
	recs       []string
}

func newArbWalk(t *testing.T) *arbWalk {
	t.Helper()
	machines := []MachineSpec{
		{Host: "h1", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 2, Resources: map[string]int{"cpu": 2, "memory": 2}},
		{Host: "h2", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 2, Resources: map[string]int{"cpu": 2, "memory": 2}},
	}
	eng := NewEngine(machines, 1_000, 0)
	for cid := uint64(1); cid <= 5; cid++ {
		eng.RegisterConn(cid, 1_000)
	}
	w := &arbWalk{
		t:        t,
		eng:      eng,
		nowMs:    1_000,
		inFlight: map[int]pendingReconnect{},
	}
	for p := 1; p <= 4; p++ {
		w.actors[p] = actorMeta{mode: "none", prio: 1}
	}
	w.actors[5] = actorMeta{mode: "none", prio: 3}
	w.record("Init", 0, "none", 0, 0, 0, 1, 0)
	return w
}

func hostName(h int) string {
	switch h {
	case 1:
		return "h1"
	case 2:
		return "h2"
	default:
		return ""
	}
}

func hostIdx(name string) int {
	switch name {
	case "h1":
		return 1
	case "h2":
		return 2
	default:
		return 0
	}
}

func actorSourceNum(p int) int {
	if p == 5 {
		return 0
	}
	if p == 1 || p == 3 {
		return 1
	}
	return 2
}

func actorSourceStr(p int) string {
	if p == 5 {
		return "ci"
	}
	return "s" + strconv.Itoa(actorSourceNum(p))
}

func prioStr(pr int) string {
	if pr == 2 {
		return "high"
	}
	return "normal"
}

func srcNumFromStr(s string) int {
	switch s {
	case "s1":
		return 1
	case "s2":
		return 2
	default:
		return 0
	}
}

func tlaBool(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func (w *arbWalk) findLeaseByActor(p int) *leaseEntry {
	for _, l := range w.eng.leases {
		if l.ConnID == uint64(p) {
			return l
		}
	}
	return nil
}

func (w *arbWalk) findWaiterByActor(p int) *waiterEntry {
	for _, q := range w.eng.queue {
		if q.ConnID == uint64(p) {
			return q
		}
	}
	return nil
}

func (w *arbWalk) actorOfLeaseID(lid string) int {
	if lid == "" {
		return 0
	}
	for p, pr := range w.inFlight {
		if pr.rl.LeaseID == lid {
			return p
		}
	}
	if l := w.eng.findLease(lid); l != nil {
		return int(l.ConnID)
	}
	return 0
}

func (w *arbWalk) record(op string, p int, m string, rh, c, mm, pr, par int) {
	var stParts, mdParts, rhParts, hoParts, ndParts, prParts, srParts, paParts, sbParts, psParts, sqParts, sttParts []string
	for id := 1; id <= 5; id++ {
		st := "idle"
		md := "none"
		reqH := 0
		ho := 0
		nc, nm := 0, 0
		prio := w.actors[id].prio
		src := actorSourceNum(id)
		parID := 0
		sub := false
		psd := false
		sq := uint64(0)

		if infl, ok := w.inFlight[id]; ok {
			st = "held"
			md = infl.mode
			reqH = infl.reqHost
			ho = hostIdx(infl.rl.Host)
			nc, nm = infl.rl.Needs["cpu"], infl.rl.Needs["memory"]
			prio = infl.prio
			parID = infl.parent
			sub = infl.sub
			psd = infl.paused
			sq = infl.seq
		} else if l := w.findLeaseByActor(id); l != nil {
			st = "held"
			md = w.actors[id].mode
			reqH = w.actors[id].reqHost
			ho = hostIdx(l.Host)
			nc, nm = l.Needs["cpu"], l.Needs["memory"]
			if id == 5 {
				prio = 3
			} else {
				prio = priorityRank(l.Priority)
			}
			parID = w.actorOfLeaseID(l.ParentLease)
			sub = l.Sublease
			psd = l.Paused
			sq = l.Seq
			w.actors[id].lastSeq = sq
		} else if q := w.findWaiterByActor(id); q != nil {
			st = "queued"
			md = q.Mode
			reqH = hostIdx(q.Host)
			ho = 0
			nc, nm = q.Needs["cpu"], q.Needs["memory"]
			prio = priorityRank(q.Priority)
			parID = w.actorOfLeaseID(q.ParentLease)
			sub = false
			psd = false
			sq = q.Seq
			w.actors[id].lastSeq = sq
		} else if w.actors[id].refusedDeadlock {
			st = "refused_deadlock"
			md = w.actors[id].mode
			reqH = w.actors[id].reqHost
			nc, nm = w.actors[id].needsCPU, w.actors[id].needsMem
			prio = w.actors[id].prio
			parID = w.actors[id].parent
		}

		stParts = append(stParts, fmt.Sprintf("%q", st))
		mdParts = append(mdParts, fmt.Sprintf("%q", md))
		rhParts = append(rhParts, strconv.Itoa(reqH))
		hoParts = append(hoParts, strconv.Itoa(ho))
		ndParts = append(ndParts, fmt.Sprintf("[cpu |-> %d, mem |-> %d]", nc, nm))
		prParts = append(prParts, strconv.Itoa(prio))
		srParts = append(srParts, strconv.Itoa(src))
		paParts = append(paParts, strconv.Itoa(parID))
		sbParts = append(sbParts, tlaBool(sub))
		psParts = append(psParts, tlaBool(psd))
		sqParts = append(sqParts, strconv.FormatUint(sq, 10))
		sttParts = append(sttParts, strconv.Itoa(w.actors[id].starts))
	}

	cl := [3][2]int{}
	for _, cEntry := range w.eng.claims {
		hi := hostIdx(cEntry.Host)
		if hi >= 1 && hi <= 2 {
			cl[hi][0] += cEntry.Needs["cpu"]
			cl[hi][1] += cEntry.Needs["memory"]
		}
	}
	claimStr := fmt.Sprintf("<<[cpu |-> %d, mem |-> %d], [cpu |-> %d, mem |-> %d]>>",
		cl[1][0], cl[1][1], cl[2][0], cl[2][1])

	rrStr := fmt.Sprintf("<<%d, %d>>",
		srcNumFromStr(w.eng.rrLast[1]), srcNumFromStr(w.eng.rrLast[2]))

	reconn := w.eng.reconnectUntilMs > w.nowMs
	var infNums []int
	for id := range w.inFlight {
		infNums = append(infNums, id)
	}
	slices.Sort(infNums)
	var infStrs []string
	for _, id := range infNums {
		infStrs = append(infStrs, strconv.Itoa(id))
	}
	inFlightStr := "{" + strings.Join(infStrs, ", ") + "}"

	rec := fmt.Sprintf("R(%q, %d, %q, %d, %d, %d, %d, %d, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, <<%s>>, %d, <<%s>>, %s, %d, %s, %s, %s, %d, %d)",
		op, p, m, rh, c, mm, pr, par,
		strings.Join(stParts, ", "),
		strings.Join(mdParts, ", "),
		strings.Join(rhParts, ", "),
		strings.Join(hoParts, ", "),
		strings.Join(ndParts, ", "),
		strings.Join(prParts, ", "),
		strings.Join(srParts, ", "),
		strings.Join(paParts, ", "),
		strings.Join(sbParts, ", "),
		strings.Join(psParts, ", "),
		strings.Join(sqParts, ", "),
		w.eng.nextSeq,
		strings.Join(sttParts, ", "),
		claimStr,
		w.claimsUsed,
		rrStr,
		tlaBool(reconn),
		inFlightStr,
		w.restartSeq,
		w.restarts,
	)
	w.recs = append(w.recs, rec)
}

func (w *arbWalk) drainSchedule() {
	for {
		l, _ := w.eng.unpauseOne(w.nowMs)
		if l == nil {
			break
		}
		p := int(l.ConnID)
		w.record("UnpauseStep", p, "none", 0, 0, 0, 1, 0)
	}
	for {
		l, _ := w.eng.admitOne(w.nowMs)
		if l == nil {
			break
		}
		p := int(l.ConnID)
		w.record("AdmitQueued", p, "none", hostIdx(l.Host), 0, 0, 1, 0)
	}
}

func (w *arbWalk) stepRequestWork(p int, m string, rh, c, mm, pr, par int) string {
	parentLease := ""
	if par != 0 {
		pl := w.findLeaseByActor(par)
		require.NotNil(w.t, pl, "parent actor %d must hold a lease", par)
		parentLease = pl.LeaseID
	}
	w.actors[p].mode = m
	w.actors[p].reqHost = rh
	w.actors[p].needsCPU = c
	w.actors[p].needsMem = mm
	w.actors[p].prio = pr
	w.actors[p].parent = par
	w.actors[p].starts++

	msgs := w.eng.Request(uint64(p), ClientFrame{
		Op:          "request",
		ReqID:       fmt.Sprintf("r-%d-%d", p, w.actors[p].starts),
		Mode:        m,
		Host:        hostName(rh),
		Needs:       NeedsSpec{Map: map[string]int{"cpu": c, "memory": mm}},
		ParentLease: parentLease,
		Holder:      "friend",
		Source:      actorSourceStr(p),
		Priority:    prioStr(pr),
	}, w.nowMs)

	outcome := ""
	for _, msg := range msgs {
		if msg.Broadcast || msg.ConnID != uint64(p) {
			continue
		}
		outcome = msg.Frame.Op
		if msg.Frame.Op == "refused" && msg.Frame.Code == "deadlock" {
			w.actors[p].refusedDeadlock = true
			outcome = "refused_deadlock"
		}
	}
	w.record("RequestWork", p, m, rh, c, mm, pr, par)
	return outcome
}

func (w *arbWalk) stepClearRefused(p int) {
	require.True(w.t, w.actors[p].refusedDeadlock)
	w.actors[p].refusedDeadlock = false
	w.actors[p].mode = "none"
	w.actors[p].reqHost = 0
	w.actors[p].needsCPU = 0
	w.actors[p].needsMem = 0
	w.actors[p].parent = 0
	w.record("ClearRefused", p, "none", 0, 0, 0, 1, 0)
}

func (w *arbWalk) stepRequestCI(h, c, mm int) {
	w.actors[5].mode = "host"
	w.actors[5].reqHost = h
	w.actors[5].needsCPU = c
	w.actors[5].needsMem = mm
	w.actors[5].prio = 3
	w.actors[5].starts++

	msgs := w.eng.Request(5, ClientFrame{
		Op:     "request",
		ReqID:  fmt.Sprintf("ci-%d", w.actors[5].starts),
		Mode:   "host",
		Host:   hostName(h),
		Needs:  NeedsSpec{Map: map[string]int{"cpu": c, "memory": mm}},
		Holder: "ci",
		Source: "ci",
	}, w.nowMs)
	require.True(w.t, slices.ContainsFunc(msgs, func(m OutMsg) bool { return m.Frame.Op == "granted" }))
	w.record("RequestCI", 5, "host", h, c, mm, 3, 0)
}

func (w *arbWalk) stepRelease(p int) {
	l := w.findLeaseByActor(p)
	require.NotNil(w.t, l)
	w.eng.removeLeaseCascade(l.LeaseID, w.nowMs, "released")
	w.actors[p].mode = "none"
	w.actors[p].reqHost = 0
	w.actors[p].needsCPU = 0
	w.actors[p].needsMem = 0
	w.actors[p].parent = 0
	w.record("Release", p, "none", 0, 0, 0, 1, 0)
	w.drainSchedule()
}

func (w *arbWalk) stepPlaceClaim(h, amt int) string {
	w.claimsUsed++
	msgs := w.eng.PlaceClaim(99, ClientFrame{
		Op:    "claim",
		ReqID: fmt.Sprintf("cl-%d", w.claimsUsed),
		Host:  hostName(h),
		Needs: NeedsSpec{Map: map[string]int{"cpu": amt, "memory": amt}},
		TTLMs: 60_000,
	}, w.nowMs)
	var cid string
	for _, m := range msgs {
		if m.Frame.Op == "granted" {
			cid = m.Frame.LeaseID
		}
	}
	require.NotEmpty(w.t, cid)
	w.record("PlaceClaim", 0, "host", h, amt, amt, 1, 0)
	return cid
}

func (w *arbWalk) stepExpireClaim(h int) {
	hn := hostName(h)
	w.eng.claims = slices.DeleteFunc(w.eng.claims, func(c *claimEntry) bool { return c.Host == hn })
	w.record("ExpireClaim", 0, "host", h, 0, 0, 1, 0)
	w.drainSchedule()
}

func (w *arbWalk) stepServerRestart() {
	require.Empty(w.t, w.eng.queue)
	require.Empty(w.t, w.eng.claims)
	require.Nil(w.t, w.findLeaseByActor(5))

	savedSeq := w.eng.nextSeq
	w.restartSeq = savedSeq
	w.restarts++
	w.inFlight = map[int]pendingReconnect{}
	for p := 1; p <= 4; p++ {
		l := w.findLeaseByActor(p)
		if l == nil {
			continue
		}
		w.inFlight[p] = pendingReconnect{
			rl: ReconnectLease{
				LeaseID:     l.LeaseID,
				Host:        l.Host,
				Needs:       maps.Clone(l.Needs),
				ParentLease: l.ParentLease,
				Holder:      l.Holder,
				Source:      l.Source,
				Label:       l.Label,
				Priority:    l.Priority,
				Cores:       l.Cores,
			},
			mode:    w.actors[p].mode,
			reqHost: w.actors[p].reqHost,
			prio:    priorityRank(l.Priority),
			parent:  w.actorOfLeaseID(l.ParentLease),
			seq:     l.Seq,
			paused:  l.Paused,
			sub:     l.Sublease,
		}
	}
	require.NotEmpty(w.t, w.inFlight)

	w.nowMs += 1_000
	machines := []MachineSpec{
		{Host: "h1", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 2, Resources: map[string]int{"cpu": 2, "memory": 2}},
		{Host: "h2", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 2, Resources: map[string]int{"cpu": 2, "memory": 2}},
	}
	w.eng = NewEngine(machines, w.nowMs, defaultReconnectMs)
	w.eng.SetNextSeq(savedSeq)
	for cid := uint64(1); cid <= 5; cid++ {
		w.eng.RegisterConn(cid, w.nowMs)
	}
	w.record("ServerRestart", 0, "none", 0, 0, 0, 1, 0)
}

func (w *arbWalk) stepReconnect(p int) {
	pr, ok := w.inFlight[p]
	require.True(w.t, ok)
	delete(w.inFlight, p)
	msgs := w.eng.Reconnect(uint64(p), ClientFrame{
		Op:     "reconnect",
		Leases: []ReconnectLease{pr.rl},
	}, w.nowMs)
	require.True(w.t, slices.ContainsFunc(msgs, func(m OutMsg) bool { return m.Frame.Op == "granted" }))
	w.record("Reconnect", p, "none", 0, 0, 0, 1, 0)
}

func (w *arbWalk) stepEndReconnect() {
	require.Empty(w.t, w.inFlight)
	w.eng.reconnectUntilMs = 0
	w.record("EndReconnect", 0, "none", 0, 0, 0, 1, 0)
	w.drainSchedule()
}

// walkVictimAndUnpause exercises multi-host routing, CI victim selection
// (lowest-priority / youngest paused first), and unpause ahead of queued work.
func walkVictimAndUnpause(t *testing.T) []string {
	w := newArbWalk(t)
	// Actor 1 (high prio) & Actor 2 (normal prio) each take cpu 1 on h1; Actor 3 queues on h1.
	w.stepRequestWork(1, "host", 1, 1, 1, 2, 0)
	w.stepRequestWork(2, "host", 1, 1, 1, 1, 0)
	w.stepRequestWork(3, "host", 1, 1, 1, 2, 0) // queued despite prio 2 because h1 is full
	// CI arrives on h1 needing cpu 1: MUST pause Actor 2 (prio 1), keeping Actor 1 (prio 2) active!
	w.stepRequestCI(1, 1, 1)
	require.False(t, w.findLeaseByActor(1).Paused)
	require.True(t, w.findLeaseByActor(2).Paused)
	// CI finishes: Actor 2 MUST unpause ahead of queued Actor 3!
	w.stepRelease(5)
	require.False(t, w.findLeaseByActor(2).Paused)
	require.NotNil(w.t, w.findWaiterByActor(3))
	// Release Actor 1 -> Actor 3 admitted; then clean up.
	w.stepRelease(1)
	w.stepRelease(2)
	w.stepRelease(3)
	return w.recs
}

// walkRoundRobinAndHoL exercises --source round-robin alternation and
// head-of-line capacity reservation preventing starvation of large requests.
func walkRoundRobinAndHoL(t *testing.T) []string {
	w := newArbWalk(t)
	// Actor 1 (source s1) takes full h1 (cpu 2, mem 2), setting rrLast[1] = s1.
	w.stepRequestWork(1, "host", 1, 2, 2, 1, 0)
	// Actor 3 (source s1) queues first, then Actor 2 (source s2) queues at same priority.
	w.stepRequestWork(3, "host", 1, 2, 2, 1, 0)
	w.stepRequestWork(2, "host", 1, 2, 2, 1, 0)
	// When Actor 1 releases, round-robin MUST pick Actor 2 (s2) ahead of Actor 3 (s1)!
	w.stepRelease(1)
	require.NotNil(t, w.findLeaseByActor(2))
	require.NotNil(t, w.findWaiterByActor(3))
	// Actor 2 releases -> Actor 3 (cpu 2, mem 2) runs; then test HoL reservation with partial host usage.
	w.stepRelease(2)
	w.stepRelease(3)

	// HoL check: Actor 1 holds cpu 1 on h1; Actor 2 queues needing cpu 2 on h1; Actor 4 arrives needing cpu 1 on h1.
	// Even though cpu 1 is free right now, Actor 4 MUST NOT cut in front of HoL Actor 2!
	w.stepRequestWork(1, "host", 1, 1, 1, 1, 0)
	w.stepRequestWork(2, "host", 1, 2, 2, 1, 0)
	w.stepRequestWork(4, "host", 1, 1, 1, 1, 0)
	require.NotNil(t, w.findWaiterByActor(4))
	w.stepRelease(1)
	w.stepRelease(2)
	w.stepRelease(4)
	return w.recs
}

// walkSubleaseDeadlockClaimAndRestart exercises 0-debit sub-leases, wait-for
// cycle refusal (RESERVE DEADLOCK), timed soft claims, and 15s reconnect window.
func walkSubleaseDeadlockClaimAndRestart(t *testing.T) []string {
	w := newArbWalk(t)
	// Actor 1 holds cpu 2, mem 2 on h1; Actor 2 takes a 0-debit sub-lease (cpu 1, mem 1) inside Actor 1!
	w.stepRequestWork(1, "host", 1, 2, 2, 1, 0)
	w.stepRequestWork(2, "host", 1, 1, 1, 1, 1)
	require.True(t, w.findLeaseByActor(2).Sublease)
	// Actor 3 (also child of Actor 1) asks for cpu 2, mem 2 on h1 (exceeds remaining sublease budget 1,1
	// and h1 is blocked by parent Actor 1) -> immediate RESERVE DEADLOCK refusal!
	out := w.stepRequestWork(3, "host", 1, 2, 2, 1, 1)
	require.Equal(t, "refused_deadlock", out)
	w.stepClearRefused(3)
	w.stepRelease(2)

	// Server restarts while Actor 1 holds h1!
	w.stepServerRestart()
	// Actor 4 arrives during reconnect window asking for h2 -> queued (held back by reconnect window).
	w.stepRequestWork(4, "host", 2, 1, 1, 1, 0)
	require.NotNil(t, w.findWaiterByActor(4))
	// Actor 1 reconnects its live lease on h1; reconnect window ends -> Actor 4 admitted on h2!
	w.stepReconnect(1)
	w.stepEndReconnect()
	require.NotNil(t, w.findLeaseByActor(4))
	w.stepRelease(1)
	w.stepRelease(4)

	// Soft claim on h1 (cpu 2, mem 2) blocks new work until expired.
	w.stepPlaceClaim(1, 2)
	w.stepRequestWork(1, "host", 1, 1, 1, 1, 0)
	require.NotNil(t, w.findWaiterByActor(1))
	w.stepExpireClaim(1)
	require.NotNil(t, w.findLeaseByActor(1))
	w.stepRelease(1)
	return w.recs
}

// runSeededArbWalk runs a pseudo-random multi-actor scenario within MaxStarts=4, MaxRestarts=2.
func runSeededArbWalk(t *testing.T, seed uint64) []string {
	w := newArbWalk(t)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	for range 32 {
		// Clear any deadlock refusal first.
		cleared := false
		for p := 1; p <= 4; p++ {
			if w.actors[p].refusedDeadlock {
				w.stepClearRefused(p)
				cleared = true
				break
			}
		}
		if cleared {
			continue
		}
		// If reconnecting, reconnect an in-flight lease or close the window.
		if w.eng.reconnectUntilMs > w.nowMs {
			if len(w.inFlight) > 0 {
				var ids []int
				for id := range w.inFlight {
					ids = append(ids, id)
				}
				slices.Sort(ids)
				w.stepReconnect(ids[rng.IntN(len(ids))])
			} else {
				w.stepEndReconnect()
			}
			continue
		}

		choice := rng.IntN(10)
		switch {
		case choice < 4:
			var idle []int
			for p := 1; p <= 4; p++ {
				if w.findLeaseByActor(p) == nil && w.findWaiterByActor(p) == nil && w.actors[p].starts < 4 {
					idle = append(idle, p)
				}
			}
			if len(idle) == 0 {
				break
			}
			p := idle[rng.IntN(len(idle))]
			m := "host"
			rh := 1 + rng.IntN(2)
			if rng.IntN(3) == 0 {
				m, rh = "any", 0
			}
			c := 1 + rng.IntN(2)
			mm := 1 + rng.IntN(2)
			pr := 1 + rng.IntN(2)
			par := 0
			if (p == 2 || p == 3) && rng.IntN(3) == 0 {
				if l1 := w.findLeaseByActor(1); l1 != nil && !l1.Sublease {
					par = 1
				}
			}
			w.stepRequestWork(p, m, rh, c, mm, pr, par)

		case choice == 4:
			if w.findLeaseByActor(5) == nil && w.actors[5].starts < 4 {
				w.stepRequestCI(1+rng.IntN(2), 1+rng.IntN(2), 1+rng.IntN(2))
			}

		case choice == 5:
			if w.claimsUsed < 4 && len(w.eng.claims) == 0 {
				w.stepPlaceClaim(1, 1+rng.IntN(2))
			} else if len(w.eng.claims) > 0 {
				w.stepExpireClaim(1)
			}

		case choice == 6:
			if w.restarts < 2 && len(w.eng.queue) == 0 && len(w.eng.claims) == 0 && w.findLeaseByActor(5) == nil {
				hasLive := false
				for p := 1; p <= 4; p++ {
					if w.findLeaseByActor(p) != nil {
						hasLive = true
					}
				}
				if hasLive {
					w.stepServerRestart()
				}
			}

		default:
			var releasable []int
			for p := 1; p <= 5; p++ {
				if w.findLeaseByActor(p) == nil {
					continue
				}
				hasChild := false
				for c := 1; c <= 4; c++ {
					if cl := w.findLeaseByActor(c); cl != nil && w.actorOfLeaseID(cl.ParentLease) == p {
						hasChild = true
					}
					if cq := w.findWaiterByActor(c); cq != nil && w.actorOfLeaseID(cq.ParentLease) == p {
						hasChild = true
					}
				}
				if !hasChild {
					releasable = append(releasable, p)
				}
			}
			if len(releasable) > 0 {
				w.stepRelease(releasable[rng.IntN(len(releasable))])
			}
		}
	}

	// Cleanly drain any remaining state so every walk ends idle.
	for p := 1; p <= 4; p++ {
		if w.actors[p].refusedDeadlock {
			w.stepClearRefused(p)
		}
	}
	if w.eng.reconnectUntilMs > w.nowMs {
		var ids []int
		for id := range w.inFlight {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			w.stepReconnect(id)
		}
		w.stepEndReconnect()
	}
	if len(w.eng.claims) > 0 {
		w.stepExpireClaim(1)
	}
	for range 10 {
		progressed := false
		for _, p := range []int{5, 2, 3, 4, 1} {
			if w.findLeaseByActor(p) == nil {
				continue
			}
			hasChild := false
			for c := 1; c <= 4; c++ {
				if cl := w.findLeaseByActor(c); cl != nil && w.actorOfLeaseID(cl.ParentLease) == p {
					hasChild = true
				}
				if cq := w.findWaiterByActor(c); cq != nil && w.actorOfLeaseID(cq.ParentLease) == p {
					hasChild = true
				}
			}
			if !hasChild {
				w.stepRelease(p)
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return w.recs
}

const arbitratorLogHead = `(* Execution walks of cmd/nova-reserve/trace_test.go (TestTracesAreBehavioursOfTheModels), *)
(* read directly off the deterministic Go Engine. ArbitratorTrace.tla verifies every walk  *)
(* is a valid behaviour of Arbitrator.tla and rejects BrokenVictimOrder, BrokenRoundRobin, *)
(* and BrokenDeadlockQueue. Rewrite with RESERVE_UPDATE_LOG=1.                             *)
R(op, p, m, rh, c, mm, pr, par, state, mode, reqHost, hostOf, needs, prio, source, parent,
  sublease, paused, seq, nextSeq, starts, claim, claimsUsed, rrLast, reconnecting,
  inFlight, restartSeq, restarts) ==
    [op |-> op, p |-> p, m |-> m, rh |-> rh, c |-> c, mm |-> mm, pr |-> pr, par |-> par,
     state |-> state, mode |-> mode, reqHost |-> reqHost, hostOf |-> hostOf, needs |-> needs,
     prio |-> prio, source |-> source, parent |-> parent, sublease |-> sublease, paused |-> paused,
     seq |-> seq, nextSeq |-> nextSeq, starts |-> starts, claim |-> claim, claimsUsed |-> claimsUsed,
     rrLast |-> rrLast, reconnecting |-> reconnecting, inFlight |-> inFlight,
     restartSeq |-> restartSeq, restarts |-> restarts]`

func renderTraceModule(module, head string, walks [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s MODULE %s %s\n%s\n", strings.Repeat("-", 28), module, strings.Repeat("-", 29), head)
	var rendered []string
	for _, recs := range walks {
		rendered = append(rendered, "<<\n        "+strings.Join(recs, ",\n        ")+"\n    >>")
	}
	fmt.Fprintf(&b, "TraceWalks == <<\n    %s\n>>\n", strings.Join(rendered, ",\n    "))
	b.WriteString(strings.Repeat("=", 77) + "\n")
	return b.String()
}

func TestTracesAreBehavioursOfTheModels(t *testing.T) {
	t.Parallel()
	walks := [][]string{
		walkVictimAndUnpause(t),
		walkRoundRobinAndHoL(t),
		walkSubleaseDeadlockClaimAndRestart(t),
	}
	for s := uint64(1); s <= 6; s++ {
		walks = append(walks, runSeededArbWalk(t, s))
	}
	for _, op := range traceRequiredOps {
		found := slices.ContainsFunc(walks, func(recs []string) bool {
			return slices.ContainsFunc(recs, func(r string) bool {
				return strings.HasPrefix(r, fmt.Sprintf("R(%q,", op))
			})
		})
		require.True(t, found, "no walk exercised action %s", op)
	}

	got := renderTraceModule("ArbitratorLog", arbitratorLogHead, walks)
	const file = "tla/ArbitratorLog.tla"
	if os.Getenv("RESERVE_UPDATE_LOG") == "1" {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	if string(want) != got {
		t.Errorf("%s drifted from the code; rewrite and check with TLC: RESERVE_UPDATE_LOG=1 go test -run TestTracesAreBehavioursOfTheModels ./cmd/nova-reserve && tests/tla/check.sh <jar> cmd/nova-reserve/tla", file)
	}
}
