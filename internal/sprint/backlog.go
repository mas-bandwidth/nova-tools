package sprint

import (
	"fmt"
	"time"
)

// The backlog alarms (docs/SPEC-SPRINT.md section 14, "The backlog alarms"). The review
// and merging backlogs were the coordinator view's alarms only (a:review, a:merging), so
// inbox --wait --push seat never carried them. When the oldest result in review, or
// merging, has waited past its alarm, the tick pushes one note to the coordinator naming
// the count, the oldest's age and its stream; once an episode, and one note more, "an
// alarm cleared", when it ends. The episode is the idle alarm's (idle.go): kept on the
// fleet table's properties, so it lives across ticks and run loops; its window is the
// alarm's age itself, so the note goes with the episode's start. The model is
// tla/SprintRules.tla, part idle (AlarmOncePerEpisode, ClearFollowsAlarm).

// The backlog alarms' notes: happened, addressed to the coordinator (the tick end wakes
// them, and inbox --push carries them to the bus). NAlarmCleared's what opens with the
// alarm's type and a colon.
const (
	NBacklogReview  = "review backlog"
	NBacklogMerging = "merging backlog"
	NAlarmCleared   = "an alarm cleared"
)

// The backlog alarms' settings and state.
const (
	// BacklogAgeDefault is how long the oldest result waits in review or merging before
	// its backlog is an alarm, when the sprint set no other.
	BacklogAgeDefault = 30 * time.Minute
	// PropAlarmReviewAge and PropAlarmMergingAge are the sprint's settings (`set
	// --alarm-review-age`, `--alarm-merging-age`), the work table's properties: a
	// duration above zero, default (BacklogAgeDefault), or off.
	PropAlarmReviewAge  = "alarm_review_age"
	PropAlarmMergingAge = "alarm_merging_age"
	// AlarmOff is the word that takes an alarm off.
	AlarmOff = "off"
	// PartBacklog is the tick part's name.
	PartBacklog = "backlog"
)

// BacklogCols is the columns with a backlog alarm, in the order the tick checks them.
var BacklogCols = []State{Review, Merging}

// backlogType is the column's alarm note; backlogProp its setting; backlogSaid the
// fleet table's property that holds when its episode's note was pushed ("" or absent:
// none).
func backlogType(col State) string {
	if col == Merging {
		return NBacklogMerging
	}
	return NBacklogReview
}

func backlogProp(col State) string {
	if col == Merging {
		return PropAlarmMergingAge
	}
	return PropAlarmReviewAge
}

func backlogSaid(col State) string { return "backlog_" + string(col) + "_said" }

// validAlarmAge says a value is one an alarm's age setting takes: a duration above
// zero, default, or off.
func validAlarmAge(v string) bool {
	if v == AlarmOff || v == ReadTierDefault {
		return true
	}
	d, err := time.ParseDuration(v)
	return err == nil && d > 0
}

// BacklogAge is the column's alarm: the sprint's setting, else BacklogAgeDefault; on is
// false when the setting is off.
func (s *Snapshot) BacklogAge(col State) (age time.Duration, on bool) {
	if s.Work != nil {
		if v, ok := s.Work.Prop(backlogProp(col)); ok {
			if v == AlarmOff {
				return 0, false
			}
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d, true
			}
		}
	}
	return BacklogAgeDefault, true
}

// ResultAge is how long a primary's result has waited: since the newest finish of its
// work cards on the fleet table; zero when none finished.
func ResultAge(s *Snapshot, pr *Card, now time.Time) time.Duration {
	var newest time.Time
	for _, c := range s.Fleet.Of(pr.ID) {
		if t, err := time.Parse(time.RFC3339, c.F("finished")); err == nil && t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return 0
	}
	return now.Sub(newest)
}

// Backlog is the column's primaries: how many, the oldest result's age (ResultAge) and
// its stream.
func Backlog(s *Snapshot, col State, now time.Time) (count int, oldest time.Duration, stream string) {
	for _, c := range s.Work.Column(col) {
		count++
		if age := ResultAge(s, c, now); age > oldest {
			oldest, stream = age, c.Row
		}
	}
	return count, oldest, stream
}

// TickBacklog is the backlog alarms, the tick's part after the idle alarm
// (TickReq.BacklogAlarm, run --backlog-alarm; docs/SPEC-SPRINT.md section 14, "The backlog
// alarms"): for review and for merging, when the oldest result has waited past the
// column's alarm (BacklogAge) the episode begins, one note goes to the coordinator, and
// the fleet table's property marks it said; while it stands nothing more; when the
// oldest is under the alarm again, the column is empty, or the alarm is off, the episode
// ends with one note, NAlarmCleared, that says so. It writes notes and properties, no
// table.
func TickBacklog(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if !r.BacklogAlarm || s.Fleet == nil || s.Work == nil {
		return p, 0
	}
	for _, col := range BacklogCols {
		count, oldest, stream := Backlog(s, col, s.Now)
		age, on := s.BacklogAge(col)
		stands := on && count > 0 && oldest >= age
		prop := backlogSaid(col)
		said, _ := s.Fleet.Prop(prop)
		write := func(value string) {
			was, had := s.Fleet.Prop(prop)
			p.Props = append(p.Props, PropWrite{Table: Fleet, Name: prop, Value: value, Was: was, WasAbsent: !had})
		}
		typ := backlogType(col)
		switch {
		case stands && said == "":
			n := happened(typ, "", s.Now)
			n.Who, n.To = r.who(), s.Coordinator
			n.What = fmt.Sprintf("%d in %s, the oldest result waiting %s (stream %s), past the alarm of %s", count, col, oldest.Round(time.Second), stream, age)
			n.Hint = "run: nova-sprint ask --stream " + stream
			if col == Merging {
				n.Hint = "run: nova-sprint land --stream " + stream
			}
			write(stamp(s.Now))
			p.Units = append(p.Units, Unit{Key: PartBacklog + ":" + string(col), Notes: []Note{n}, Moved: typ + ": told " + orDash(s.Coordinator)})
		case !stands && said != "":
			n := happened(NAlarmCleared, "", s.Now)
			n.Who, n.To = r.who(), s.Coordinator
			n.What = typ + ": its alarm is off"
			if on {
				n.What = fmt.Sprintf("%s: %d in %s", typ, count, col)
				if t, err := time.Parse(time.RFC3339, said); err == nil {
					n.What += ", after " + s.Now.Sub(t).Round(time.Second).String()
				}
			}
			write("")
			p.Units = append(p.Units, Unit{Key: PartBacklog + ":" + string(col), Notes: []Note{n}, Moved: typ + ": the episode since " + said + " ended"})
		}
	}
	return p, 0
}
