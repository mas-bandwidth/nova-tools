package sprint

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// A member's open files (docs/SPEC-SPRINT.md section 8, "Open files"). A sprint once ran
// its machine out of file descriptors, and only a workshop tool of one coordinator
// watched the count; a stranger's machine would fail the same way unseen. Every fleet
// beat measures the machine's open descriptors beside its load (hostload.MeasureFiles),
// against the member's own warn and alarm bounds (fleet beat --fd-warn and --fd-alarm),
// and carries the reading in the beat's measuring state. Over the warn bound the
// member's files word says warn; over the alarm bound the tick writes one judgment of
// the member, an episode: written when the count passes the alarm, never again while it
// stays over whatever it does (its line updated in place with the latest count and
// holders), closed with one cleared note when it falls under or the beat goes stale. Its
// decisions are the member's (halve its width, or hold it down), ack (seen: quiet for
// the episode) and wait 15m (quiet for that running time, raised again if still over). It names the count, the top holders and, as an alarm is an effect on
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

// filesDecisions are the judgment's: halve the member's width, hold it and deal its
// cards elsewhere, acknowledge it (seen: quiet for the episode), or wait 15m (quiet
// for that running time, raised again if the count is still over). The judgment
// closes itself when the count falls under the alarm.
func filesDecisions(s *Snapshot, m string) []string {
	return []string{fmt.Sprintf("fleet up %s --width %d", m, max(1, s.Width(m)/2)), "fleet down " + m, "ack", "wait 15m"}
}

// filesConds is the tick's open-files condition of every member over its alarm bound.
// An episode is keyed by its type and member alone (condKey), so a count that moves
// while it stays over is the same episode, and its judgment's line is updated in place
// with the latest count and holders; an acknowledgement or a wait of it holds the
// episode the same way.
func filesConds(s *Snapshot, r TickReq) []cond {
	var out []cond
	for _, m := range s.Members() {
		f, ok := FilesAt(r.Beats[m], s.Now)
		if !ok || f.Level() != hostload.LevelAlarm {
			continue
		}
		out = append(out, cond{typ: NFilesAlarm, stream: MemberSubject(m), streamLevel: true, what: filesWhat(s, m, f), decisions: filesDecisions(s, m)})
	}
	return out
}

// tickFiles is the tick's open-files alarms, planned with the backlog alarms
// (tickAlarms): a judgment for each member whose count passes its alarm, none while it
// stays over or the coordinator's ack or wait holds it, and, for each the tick closes
// because the count fell under (or the beat went stale), one cleared note to the
// coordinator.
func tickFiles(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	conds := filesConds(s, r)
	stands := map[string]bool{}
	for _, c := range conds {
		stands[c.stream] = true
	}
	due := notify(&p, s, conds, []string{NFilesAlarm}, r)
	for _, o := range p.Closes {
		if stands[o.Note.Stream] {
			continue // a wait run out on a count still over the alarm is raised again, not cleared
		}
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
