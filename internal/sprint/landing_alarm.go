package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The landing alarm (docs/SPEC-SPRINT.md section 8, "Landing alarm"): cards queued
// to merge and nothing landed for a bound, while the lander is not inside a gate.
// One judgment of the sprint, pushed once when it starts, kept while it holds, and
// not raised again inside the bound after it is closed. The seat's inbox shape
// (SeatInbox) rides the same part: one happened note of the overdue line, once
// while that line holds.
const (
	NNoLanding = "no landing"
	// NInboxLead is the happened note to the coordinator whose text is the inbox's
	// opening line (SeatInbox). The inbox verb's own first line is outside this
	// card's paths.
	NInboxLead = "inbox overdue"

	// PropLandingBound is the work-table property of the bound, in whole minutes.
	// Absent is LandingBoundDefault. "off" or 0 raises nothing.
	PropLandingBound = "landing_alarm"
	// PropLandingRaised is when the alarm last raised a judgment, so a close inside
	// the bound does not raise it again.
	PropLandingRaised = "landing_alarm_at"
	// PropInboxLead is the overdue line last sent, so a tick does not send it again.
	PropInboxLead = "inbox_lead"

	LandingBoundDefault = 15 * time.Minute
)

// StoppedStream is one stream the landing alarm names: its stop reason and how
// many cards it still has queued.
type StoppedStream struct {
	Name, Reason string
	Queued       int
}

// LandingFacts is what the landing alarm judges. The clock is the snapshot's.
type LandingFacts struct {
	Now         time.Time
	LastLanding time.Time // zero: nothing has landed, and the alarm stays quiet
	Queued      int
	Stopped     []StoppedStream
	LanderStep  string
	LanderSince time.Time
	Bound       time.Duration
	Raised      time.Time // the last raise; zero is none
	InGate      bool
}

// LandingLine is the alarm's judgment, and whether the condition holds.
// A lander inside a gate, an empty queue, a bound of zero, or a dry time
// shorter than the bound holds nothing. Raised is not consulted: the tick
// uses it only to keep a closed alarm from starting again inside the bound.
// The line is "no landing for <n> min: <queued> queued; <k> in stopped streams
// (<names>: <reasons>); lander at <step> for <t>".
func LandingLine(f LandingFacts) (string, bool) {
	if f.InGate || landerInGate(f.LanderStep) || f.Queued <= 0 || f.Bound <= 0 || f.LastLanding.IsZero() {
		return "", false
	}
	dry := f.Now.Sub(f.LastLanding)
	if dry < f.Bound {
		return "", false
	}
	names := append([]StoppedStream(nil), f.Stopped...)
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	k := 0
	var parts []string
	for _, st := range names {
		k += st.Queued
		reason := st.Reason
		if reason == "" {
			reason = "stopped"
		}
		parts = append(parts, st.Name+": "+reason)
	}
	stopped := fmt.Sprintf("%d in stopped streams", k)
	if k > 0 {
		stopped += " (" + strings.Join(parts, ", ") + ")"
	}
	step := f.LanderStep
	if step == "" {
		step = "land"
	}
	since := f.LanderSince
	if since.IsZero() {
		since = f.LastLanding
	}
	return fmt.Sprintf("no landing for %s: %d queued; %s; lander at %s for %s",
		shortSpan(dry), f.Queued, stopped, step, landingSpan(f.Now, since)), true
}

// landerInGate says the lander's step is a gate. An empty or idle step is not.
func landerInGate(step string) bool {
	return strings.Contains(strings.ToLower(step), "gate")
}

// shortSpan is a duration as the alarm says it: whole minutes, truncated, then
// hours when it is at least one.
func shortSpan(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	min := int(d / time.Minute)
	if min < 60 {
		return fmt.Sprintf("%d min", min)
	}
	h, m := min/60, min%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func landingSpan(now, since time.Time) string {
	if since.IsZero() || now.Before(since) {
		return "0 min"
	}
	return shortSpan(now.Sub(since))
}

// landingBound is the bound the work table holds, or the default.
func landingBound(s *Snapshot) time.Duration {
	if s == nil || s.Work == nil {
		return LandingBoundDefault
	}
	v, ok := s.Work.Prop(PropLandingBound)
	if !ok || v == "" {
		return LandingBoundDefault
	}
	if v == AlarmOff || v == "0" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return LandingBoundDefault
	}
	return time.Duration(n) * time.Minute
}

func landingRaised(s *Snapshot) time.Time {
	if s == nil || s.Work == nil {
		return time.Time{}
	}
	v, ok := s.Work.Prop(PropLandingRaised)
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func lastLanding(s *Snapshot) time.Time {
	var last time.Time
	if s == nil || s.Work == nil {
		return last
	}
	for _, c := range s.Work.Cards() {
		if c.Col != Landed {
			continue
		}
		t, err := time.Parse(time.RFC3339, c.F("landed"))
		if err == nil && t.After(last) {
			last = t
		}
	}
	return last
}

func mergeRows(s *Snapshot) []string {
	if s == nil || s.Merge == nil {
		return nil
	}
	if rows := s.Merge.Rows(); len(rows) > 0 {
		return append([]string(nil), rows...)
	}
	seen := map[string]bool{}
	var rows []string
	for _, c := range s.Merge.Cards() {
		if c.Row == "" || seen[c.Row] {
			continue
		}
		seen[c.Row] = true
		rows = append(rows, c.Row)
	}
	sort.Strings(rows)
	return rows
}

// stopReason is the cause as the seat reads it. A rejected push is "push refused".
func stopReason(ctl *Card) string {
	cause := ctl.F("cause")
	if cause == "" {
		return "stopped"
	}
	if cause == "rejected" {
		return "push refused"
	}
	return cause
}

func queuedStopped(s *Snapshot) (int, []StoppedStream) {
	var queued int
	var stopped []StoppedStream
	for _, st := range mergeRows(s) {
		q := s.Merge.Count(st, Queued)
		queued += q
		ctl := s.StreamCtl(st)
		if ctl == nil || ctl.F("state") != StreamStopped {
			continue
		}
		stopped = append(stopped, StoppedStream{Name: st, Reason: stopReason(ctl), Queued: q})
	}
	sort.Slice(stopped, func(i, j int) bool { return stopped[i].Name < stopped[j].Name })
	return queued, stopped
}

// stuckStep is the step an NOpStuck judgment names ("step=<step>"), or "".
func stuckStep(what string) string {
	const key = "step="
	i := strings.Index(what, key)
	if i < 0 {
		return ""
	}
	rest := what[i+len(key):]
	rest, _, _ = strings.Cut(rest, " ")
	return rest
}

// landerNow is the lander's step. An open NOpStuck names it (a gate step is
// inside a gate). With none, the lander is at land since the last landing,
// which is the idle the seat called the bottleneck, and not a gate.
func landerNow(s *Snapshot, last time.Time) (step string, since time.Time, inGate bool) {
	step, since = "land", last
	if s == nil {
		return
	}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type != NOpStuck {
			continue
		}
		st := stuckStep(o.Note.What)
		if st == "" {
			continue
		}
		step = st
		if !o.Note.At.IsZero() {
			since = o.Note.At
		}
		inGate = landerInGate(st)
		return
	}
	return
}

func landingFacts(s *Snapshot) LandingFacts {
	last := lastLanding(s)
	queued, stopped := queuedStopped(s)
	step, since, inGate := landerNow(s, last)
	return LandingFacts{
		Now: s.Now, LastLanding: last, Queued: queued, Stopped: stopped,
		LanderStep: step, LanderSince: since, Bound: landingBound(s),
		Raised: landingRaised(s), InGate: inGate,
	}
}

func landingOpen(s *Snapshot) bool {
	if s == nil {
		return false
	}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NNoLanding {
			return true
		}
	}
	for _, o := range s.Acked {
		if o.Note.Type == NNoLanding {
			return true
		}
	}
	return false
}

// tickLandingAlarm is the deadlines part's landing alarm and the inbox's overdue
// line (docs/SPEC-SPRINT.md, "Landing alarm"). A condition that holds keeps its
// one judgment (notify keys it by type, so a count that moves is the same
// episode). A judgment closed inside the bound is not raised again until the
// bound has passed. The inbox line is one happened note to the coordinator.
func tickLandingAlarm(s *Snapshot, r TickReq) Plan {
	var p Plan
	facts := landingFacts(s)
	line, holds := LandingLine(facts)
	cadence := !facts.Raised.IsZero() && s.Now.Sub(facts.Raised) < facts.Bound
	var conds []cond
	if holds && (landingOpen(s) || !cadence) {
		conds = append(conds, cond{typ: NNoLanding, streamLevel: true, what: line, decisions: []string{"ack", "wait"}})
	}
	notify(&p, s, conds, []string{NNoLanding}, r)
	raised := false
	for _, n := range p.Notes {
		if n.Kind == Judgment && n.Type == NNoLanding {
			raised = true
		}
	}
	if raised && s.Work != nil {
		was, had := s.Work.Prop(PropLandingRaised)
		p.Props = append(p.Props, PropWrite{Table: Work, Name: PropLandingRaised, Value: stamp(s.Now), Was: was, WasAbsent: !had})
	}
	inboxLead(&p, s, r)
	return p
}

// inboxLead sends the overdue opening line when it changes.
func inboxLead(p *Plan, s *Snapshot, r TickReq) {
	if s == nil || s.Work == nil {
		return
	}
	lead, lines := SeatInbox(s.Open, s.Now, DeadlineJudgment)
	prev, had := s.Work.Prop(PropInboxLead)
	if lead == prev || (lead != "" && len(lines) == 0) {
		return
	}
	p.Props = append(p.Props, PropWrite{Table: Work, Name: PropInboxLead, Value: lead, Was: prev, WasAbsent: !had})
	if lead == "" {
		return
	}
	p.Notes = append(p.Notes, Note{Kind: Happened, Type: NInboxLead, Who: r.who(), To: s.Coordinator, At: s.Now, What: lead})
}

// SeatInbox is the seat inbox as a queue. Same kind on the same subject is one
// line with a count. Overdue groups come first. The opening line is
// "<n> overdue: <kind> <count>, ..." with kinds sorted, empty when nothing is
// overdue. n counts judgments, not groups. The tick sends the opening line as a
// happened note (inboxLead), and the inbox verb opens with it and lists the queue.
func SeatInbox(open []Open, now time.Time, deadline time.Duration) (lead string, lines []string) {
	type group struct {
		kind, subject string
		n, overdue    int
		oldest        time.Time
		anyOverdue    bool
	}
	var order []string
	by := map[string]*group{}
	for _, o := range open {
		if o.Note.Kind != Judgment || o.Note.Type == NSprintDone {
			continue
		}
		key := o.Note.Type + "\x00" + o.Subject()
		g := by[key]
		if g == nil {
			g = &group{kind: o.Note.Type, subject: o.Subject(), oldest: o.Note.At}
			by[key] = g
			order = append(order, key)
		}
		g.n++
		if !o.Note.At.IsZero() && (g.oldest.IsZero() || o.Note.At.Before(g.oldest)) {
			g.oldest = o.Note.At
		}
		if inboxOverdue(o.Note, now, deadline) {
			g.overdue++
			g.anyOverdue = true
		}
	}
	gs := make([]*group, 0, len(order))
	for _, k := range order {
		gs = append(gs, by[k])
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].anyOverdue != gs[j].anyOverdue {
			return gs[i].anyOverdue
		}
		if !gs[i].oldest.Equal(gs[j].oldest) {
			return gs[i].oldest.Before(gs[j].oldest)
		}
		if gs[i].kind != gs[j].kind {
			return gs[i].kind < gs[j].kind
		}
		return gs[i].subject < gs[j].subject
	})
	for _, g := range gs {
		lines = append(lines, fmt.Sprintf("%d %s %s", g.n, g.kind, g.subject))
	}
	counts := map[string]int{}
	var kinds []string
	n := 0
	for _, g := range gs {
		if g.overdue == 0 {
			continue
		}
		n += g.overdue
		if _, ok := counts[g.kind]; !ok {
			kinds = append(kinds, g.kind)
		}
		counts[g.kind] += g.overdue
	}
	if n == 0 {
		return "", lines
	}
	sort.Strings(kinds)
	bits := make([]string, 0, len(kinds))
	for _, k := range kinds {
		bits = append(bits, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return fmt.Sprintf("%d overdue: %s", n, strings.Join(bits, ", ")), lines
}

func inboxOverdue(n Note, now time.Time, deadline time.Duration) bool {
	if n.Type == NSprintDone {
		return false
	}
	if !n.Review.IsZero() {
		return !now.Before(n.Review)
	}
	if deadline <= 0 || n.At.IsZero() {
		return false
	}
	return now.Sub(n.At) > deadline
}
