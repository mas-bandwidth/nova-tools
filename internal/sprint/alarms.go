package sprint

import (
	"fmt"
	"strconv"
)

// The backlog alarms (docs/SPEC-SPRINT.md section 8, "Backlog alarms"): four conditions
// of the whole sprint the tick keeps, each off until the coordinator sets its threshold
// (`set --alarm-review`, `--alarm-merging`, `--alarm-fleet`, `--alarm-ready`, the work
// table's properties below). Each is one judgment while its condition stands, written
// once when it starts (notify: one per episode, so a push of new judgments pushes it
// once) and closed once when it ends, with one cleared note to the coordinator.
const (
	NAlarmReview  = "review above its alarm"
	NAlarmMerging = "merging above its alarm"
	NAlarmReady   = "nothing ready while cards wait"
	NAlarmFleet   = "the fleet works below its alarm"
	// NAlarmCleared is the happened note, to the coordinator, that an alarm's episode
	// ended: its what opens with the alarm's type and a colon.
	NAlarmCleared = "an alarm cleared"

	PropAlarmReview  = "alarm_review"  // a count: raised while review holds more
	PropAlarmMerging = "alarm_merging" // a count: raised while merging holds more
	PropAlarmFleet   = "alarm_fleet"   // a percent: raised while the up members work below it of their width
	PropAlarmReady   = "alarm_ready"   // on: raised while nothing is ready and a card waits
	// AlarmOff is the word that takes an alarm off.
	AlarmOff = "off"
)

// AlarmTypes is the backlog alarms' judgment types, in the order the tick checks them.
var AlarmTypes = []string{NAlarmReview, NAlarmMerging, NAlarmReady, NAlarmFleet, NAlarmDefect}

// alarmProps is each alarm's property, the flag that sets it and what its value wants.
var alarmProps = []struct{ typ, prop, flag, wants string }{
	{NAlarmReview, PropAlarmReview, "--alarm-review", "a whole number of primaries, 0 or more"},
	{NAlarmMerging, PropAlarmMerging, "--alarm-merging", "a whole number of primaries, 0 or more"},
	{NAlarmFleet, PropAlarmFleet, "--alarm-fleet", "a percent of the up members' width, 1 to 100"},
	{NAlarmReady, PropAlarmReady, "--alarm-ready", "on"},
	{NAlarmDefect, PropAlarmDefect, "--alarm-defect", "a whole number of primaries, 0 or more"},
}

// alarmValid says a value is one the alarm's property takes: its threshold, or off.
func alarmValid(prop, v string) bool {
	if v == AlarmOff {
		return true
	}
	if prop == PropAlarmReady {
		return v == "on"
	}
	n, err := strconv.Atoi(v)
	if prop == PropAlarmFleet {
		return err == nil && n >= 1 && n <= 100
	}
	return err == nil && n >= 0
}

// alarmSetting is an alarm's threshold as the work table's property holds it, ok false
// when it is off (no property, off, or a value it does not take). The defect alarm is on by
// default (columns.go, defectAlarmSetting), so it reads its own.
func (s *Snapshot) alarmSetting(prop string) (int, bool) {
	if prop == PropAlarmDefect {
		return defectAlarmSetting(s)
	}
	v, ok := s.Work.Prop(prop)
	if !ok || !alarmValid(prop, v) || v == AlarmOff {
		return 0, false
	}
	if prop == PropAlarmReady {
		return 0, true
	}
	n, _ := strconv.Atoi(v)
	return n, true
}

// alarmFacts is each alarm whose condition stands, with what it says
// (docs/SPEC-SPRINT.md section 8, "Backlog alarms"):
//   - review: more primaries in review than its count;
//   - merging: more primaries merging than its count;
//   - ready: none ready (work table) and one or more waiting;
//   - fleet: the up members working fewer work cards than its percent of their width,
//     while a primary is ready or waiting, so the fleet has work it does not run.
func alarmFacts(s *Snapshot) map[string]string {
	out := map[string]string{}
	review, merging := len(s.Work.Column(Review)), len(s.Work.Column(Merging))
	ready, waiting := len(s.Work.Column(Ready)), len(s.Work.Column(Waiting))
	if n, on := s.alarmSetting(PropAlarmReview); on && review > n {
		out[NAlarmReview] = fmt.Sprintf("%d primaries in review, above the alarm of %d: reads or accepts are behind; run: nova-sprint where", review, n)
	}
	if n, on := s.alarmSetting(PropAlarmMerging); on && merging > n {
		out[NAlarmMerging] = fmt.Sprintf("%d primaries merging, above the alarm of %d: landing is behind; run: nova-sprint land", merging, n)
	}
	if _, on := s.alarmSetting(PropAlarmReady); on && ready == 0 && waiting > 0 {
		out[NAlarmReady] = fmt.Sprintf("0 primaries ready and %d waiting: the deal has nothing to feed the fleet; run: nova-sprint where --all", waiting)
	}
	if pct, on := s.alarmSetting(PropAlarmFleet); on && ready+waiting > 0 && !s.FleetOff() {
		working, width := 0, 0
		for _, m := range s.UpMembers() {
			working += s.Fleet.Count(m, Working)
			width += s.Width(m)
		}
		if width > 0 && working*100 < pct*width {
			out[NAlarmFleet] = fmt.Sprintf("the members up work %d of their width %d, below the alarm of %d%%, with %d primaries ready or waiting; run: nova-sprint where", working, width, pct, ready+waiting)
		}
	}
	// the defect alarm (columns.go, DefectAlarm): more than its threshold in defect, or any
	// one over DefectAlarmAge, on by default; its what lists the oldest five with their
	// reasons.
	if what, raised := DefectAlarm(s); raised {
		out[NAlarmDefect] = what
	}
	return out
}

// tickAlarms is the tick's backlog alarms, planned with the deadlines (TickDeadlines;
// docs/SPEC-SPRINT.md section 8, "Backlog alarms"): a judgment for each alarm whose
// condition starts, none while it stands (notify keys an alarm by its type alone, so a
// count that moves is the same episode), and, for each whose judgment or hold the
// condition's end closes, one cleared note to the coordinator; and the members' open
// files over their alarm bounds the same way (tickFiles, fd.go). It writes notes, no
// table.
func tickAlarms(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	facts := alarmFacts(s)
	var conds []cond
	for _, typ := range AlarmTypes {
		if what, ok := facts[typ]; ok {
			conds = append(conds, cond{typ: typ, streamLevel: true, what: what})
		}
	}
	due := notify(&p, s, conds, AlarmTypes, r)
	for _, o := range p.Closes {
		typ := o.Note.Type
		if _, stands := facts[typ]; stands {
			continue // a wait run out on a condition that stands is raised again, not cleared
		}
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NAlarmCleared, Who: r.who(), To: s.Coordinator, At: s.Now,
			What: typ + ": " + alarmNow(s, typ)})
	}
	// each member's open files over its alarm bound, from its beat (fd.go)
	f, filesDue := tickFiles(s, r)
	p.Notes, p.Closes, p.Updates = append(p.Notes, f.Notes...), append(p.Closes, f.Closes...), append(p.Updates, f.Updates...)
	return p, due + filesDue
}

// alarmNow is what an alarm's cleared note says of the sprint now.
func alarmNow(s *Snapshot, typ string) string {
	for _, a := range alarmProps {
		if a.typ != typ {
			continue
		}
		if _, on := s.alarmSetting(a.prop); !on {
			return "its alarm is off"
		}
	}
	switch typ {
	case NAlarmReview:
		return fmt.Sprintf("%d primaries in review", len(s.Work.Column(Review)))
	case NAlarmMerging:
		return fmt.Sprintf("%d primaries merging", len(s.Work.Column(Merging)))
	case NAlarmReady:
		return fmt.Sprintf("%d primaries ready, %d waiting", len(s.Work.Column(Ready)), len(s.Work.Column(Waiting)))
	case NAlarmDefect:
		return fmt.Sprintf("%d primaries in defect", len(DefectCards(s)))
	}
	return "the fleet works at its alarm or above, or has no work ready or waiting"
}
