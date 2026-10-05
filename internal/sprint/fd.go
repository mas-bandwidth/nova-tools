package sprint

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// A member's open files (docs/SPEC-SPRINT.md section 8, "Open files"). A sprint once ran
// its machine out of file descriptors, and only a workshop tool of one coordinator
// watched the count; a stranger's machine would fail the same way unseen. Every fleet
// beat measures the machine's open descriptors beside its load (hostload.MeasureFiles),
// against the member's own warn and alarm bounds (fleet beat --fd-warn and --fd-alarm),
// and carries the reading in the beat's measuring state. Over the warn bound the
// member's files word says warn; over the alarm bound the tick writes one judgment of
// the member, an episode: written when the count passes the alarm, never again while it
// stays over whatever it does, closed with one cleared note when it falls under or the
// beat goes stale. It names the count, the top holders and, as an alarm is an effect on
// cards, the member's cards that ended on a timeout within OverloadWindow.

// NFilesAlarm is the judgment of a member whose open file descriptors are above its
// alarm bound.
const NFilesAlarm = "open files above the alarm"

// FilesAt is the member's open-files reading while its beat is fresh and the reading was
// taken within BeatDeadline of now: a beat that gave its load and could not count its
// descriptors carries none, and an older reading is no reading.
func FilesAt(b Beat, now time.Time) (hostload.Files, bool) {
	f := b.Meter.Files
	if f == nil || !b.Fresh(now) || now.Sub(f.At) > BeatDeadline {
		return hostload.Files{}, false
	}
	return *f, true
}

// FilesText is the member's files word: the count and warn or alarm while a fresh
// reading is over its warn bound, else empty.
func FilesText(b Beat, now time.Time) string {
	f, ok := FilesAt(b, now)
	if !ok || f.Level() == hostload.LevelOK {
		return ""
	}
	return strconv.Itoa(f.Open) + " " + f.Level()
}

// filesWhat is the judgment's line: the member, its count over its alarm, the system's
// limit, the top holders, and its cards that ended on a timeout within OverloadWindow.
func filesWhat(s *Snapshot, m string, f hostload.Files) string {
	what := fmt.Sprintf("%s has %d open files, above its alarm of %d", m, f.Open, f.Alarm)
	if f.Max > 0 {
		what += fmt.Sprintf(" (the system's limit %d)", f.Max)
	}
	what += "; the top holders: " + f.TopText()
	if ts := MemberTimeouts(s, m); len(ts) > 0 {
		cards := make([]string, len(ts))
		for i, t := range ts {
			cards[i] = t.Card + " (" + t.Kind + ")"
		}
		what += fmt.Sprintf("; its cards that ended on a timeout in the last %s: %s", OverloadWindow, strings.Join(cards, ", "))
	} else {
		what += fmt.Sprintf("; none of its cards ended on a timeout in the last %s", OverloadWindow)
	}
	return what
}

// filesDecisions are the judgment's: halve the member's width, or hold it and deal its
// cards elsewhere. The judgment closes itself when the count falls under the alarm.
func filesDecisions(s *Snapshot, m string) []string {
	return []string{fmt.Sprintf("fleet up %s --width %d", m, max(1, s.Width(m)/2)), "fleet down " + m}
}

// filesConds is the tick's open-files condition of every member over its alarm bound.
// An episode keeps the line its judgment was written with (the condition is keyed by
// its line), so a count that moves while it stays over is the same episode.
func filesConds(s *Snapshot, r TickReq) []cond {
	judged := map[string]string{}
	for _, o := range s.Open {
		if o.Note.Type == NFilesAlarm && o.Note.Kind == Judgment {
			judged[o.Note.Stream] = o.Note.What
		}
	}
	var out []cond
	for _, m := range s.Members() {
		f, ok := FilesAt(r.Beats[m], s.Now)
		if !ok || f.Level() != hostload.LevelAlarm {
			continue
		}
		subject := MemberSubject(m)
		what, open := judged[subject]
		if !open {
			what = filesWhat(s, m, f)
		}
		out = append(out, cond{typ: NFilesAlarm, stream: subject, streamLevel: true, what: what, decisions: filesDecisions(s, m)})
	}
	return out
}

// tickFiles is the tick's open-files alarms, planned with the backlog alarms
// (tickAlarms): a judgment for each member whose count passes its alarm, none while it
// stays over, and, for each the tick closes, one cleared note to the coordinator.
func tickFiles(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := notify(&p, s, filesConds(s, r), []string{NFilesAlarm}, r)
	for _, o := range p.Closes {
		m := strings.TrimPrefix(o.Note.Stream, MemberSubject(""))
		now := m + " gives no fresh reading of its open files"
		if f, ok := FilesAt(r.Beats[m], s.Now); ok {
			now = fmt.Sprintf("%s has %d open files, at or under its alarm of %d", m, f.Open, f.Alarm)
		}
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NAlarmCleared, Who: r.who(), To: s.Coordinator, At: s.Now,
			What: NFilesAlarm + ": " + now})
	}
	return p, due
}
