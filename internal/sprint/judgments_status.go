package sprint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// Status transitions (docs/SPEC-SPRINT.md section 8, "Status transitions"; the owner,
// 2026-10-06 2:55 PM ET, after a friend was brought up on a stale daemon binary and flooded
// with yesterday's notes: "Please make this process mechanical. You cannot remember to do
// this reliably on your own in my experience, so if it is mechanical, make the machine
// prompt you when a friend changes their status"). Every change of a friend's or a fleet
// member's status, as the server computes it at the tick, is told to the seat as a judgment
// of type NStatus on the row, pushed as every judgment is: anything to up, up to down (a
// friend whose daemon answers and whose session does not is down, and her judgment says so:
// docs/TERMINOLOGY.md, anything but up is down), held and unheld, and, for a friend, a new
// generation of her daemon (a start her beat names that the transitions have not seen). The
// text names the transition, the clock time and the exact verbs; for a friend come up, the
// four steps in order with what the machine already did.
//
// A change counts only once it has held for StatusDwell of running time: a row that leaves
// its recorded status and comes back inside the dwell raises nothing, and the flap is counted
// on the record and shown on the judgment ("flapped n times since <t>"). A row has at most
// one open status judgment: a further transition replaces its text in place (the same id and
// alias) and tells the seat it changed (NStatusChanged, a happened note to the coordinator,
// which the push loop delivers); an answered one is closed and a new one raised. So a friend
// flipping every minute is one push, and one more for each change that outlasts the dwell.
// The record is the fleet table's one property PropStatusSeen, written in the same
// operation as the judgment: a restart reads it back and replays none, and the first sight of
// a row records it and raises nothing. With run --answer-rules a judgment whose every step is
// already done is answered by rule (RuleStatus), never as it is raised: StatusRuleAfter of
// running time after its last push at the soonest, so the seat has it first.

const (
	// NStatus is the status transition's judgment type.
	NStatus = "status"
	// RuleStatus is the rule that answers a status judgment whose every step is done. It
	// is not in RuleNames (nova-config's answer_rules_off enum does not name it yet), so
	// only run --answer-rules=false turns it off.
	RuleStatus = "status"
	// TakeWithin is how recent a take of hers must be for a friend come up to be in the
	// sprint (the fourth step).
	TakeWithin = 10 * time.Minute
	// StatusDwell is how long a row must hold a status other than its recorded one, in running
	// time, before the change counts as a transition.
	StatusDwell = 2 * time.Minute
	// StatusFlapsRaise is how many flaps (a departure back inside the dwell) raise the row's
	// "flapping" judgment when none is open: once, until a transition counts.
	StatusFlapsRaise = 3
	// StatusFlapWindow is how long, in running time, the flaps that raise it may take: a count
	// short of StatusFlapsRaise that began longer ago starts again at the next flap.
	StatusFlapWindow = 10 * time.Minute
	// StatusRuleAfter is how long after a status judgment's last push the rule may answer it.
	StatusRuleAfter = 60 * time.Second
	// NStatusChanged is the push of a status judgment whose text a further transition
	// replaced: a happened note to the coordinator naming it.
	NStatusChanged = "a status judgment changed"
)

// PropStatusSeen is the fleet property that keeps every row's status as the transitions
// last saw it, one property for the whole fleet (a table holds at most
// ntable.LimitTableProps): for each row "<row>=<word>,<transition>,<daemon start>,<away
// since>,<flaps>,<flapping since>", times RFC3339 or "-", one after another with a blank
// between, in row order. Away since is when the row first left the recorded status (zero
// while it holds it); the flaps are the departures that came back inside the dwell since the
// last transition counted.
const PropStatusSeen = "status_seen"

func init() {
	TickDecisions[NStatus] = []string{"ack", "wait"}
}

// statusSeen is a row's PropStatusSeen record.
type statusSeen struct {
	Word      string
	N         int
	Started   time.Time
	Away      time.Time
	Flaps     int
	FlapSince time.Time
}

func stampOrDash(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return stamp(t)
}

func (r statusSeen) String() string {
	return fmt.Sprintf("%s,%d,%s,%s,%d,%s", r.Word, r.N, stampOrDash(r.Started), stampOrDash(r.Away), r.Flaps, stampOrDash(r.FlapSince))
}

// parseStatusSeen is the record by row; an entry that cannot be read is no record, and its
// row is seen again for the first time. An entry of three fields (word, transition, start) is
// read with no departure and no flaps.
func parseStatusSeen(v string) map[string]statusSeen {
	out := map[string]statusSeen{}
	at := func(x string) (time.Time, bool) {
		if x == "-" {
			return time.Time{}, true
		}
		t, err := time.Parse(time.RFC3339, x)
		return t, err == nil
	}
	for _, e := range strings.Fields(v) {
		row, rest, ok := strings.Cut(e, "=")
		f := strings.Split(rest, ",")
		if !ok || row == "" || (len(f) != 3 && len(f) != 6) || f[0] == "" {
			continue
		}
		r := statusSeen{Word: f[0]}
		var err error
		var good bool
		if r.N, err = strconv.Atoi(f[1]); err != nil || r.N < 0 {
			continue
		}
		if r.Started, good = at(f[2]); !good {
			continue
		}
		if len(f) == 6 {
			if r.Away, good = at(f[3]); !good {
				continue
			}
			if r.Flaps, err = strconv.Atoi(f[4]); err != nil || r.Flaps < 0 {
				continue
			}
			if r.FlapSince, good = at(f[5]); !good {
				continue
			}
		}
		out[row] = r
	}
	return out
}

func formatStatusSeen(m map[string]statusSeen) string {
	rows := slices.Sorted(maps.Keys(m))
	parts := make([]string, len(rows))
	for i, row := range rows {
		parts[i] = row + "=" + m[row].String()
	}
	return strings.Join(parts, " ")
}

// statusRow is one row the transitions watch: a fleet member or a friend, its status word
// now, and for a friend her seat (her daemon's start is her beat's).
type statusRow struct {
	row    string
	name   string
	word   string
	friend *FriendSeat
	beat   Beat
}

func (r statusRow) started() time.Time {
	if r.friend == nil || r.friend.Beat.Friend == nil {
		return time.Time{}
	}
	return r.friend.Beat.Friend.Started.UTC()
}

// statusRows is every fleet member with a control card, by the presence rule (MemberStatus,
// the status the presence part applies in the same tick), then every friend of the roster
// by the friends' rule (her seat's status).
func statusRows(s *Snapshot, r TickReq) []statusRow {
	var out []statusRow
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		if ctl == nil {
			continue
		}
		out = append(out, statusRow{row: m, name: m, word: MemberStatus(ctl, r.Beats[m], s.Now), beat: r.Beats[m]})
	}
	for i := range r.Friends {
		f := &r.Friends[i]
		word := f.Status
		if word == "" {
			word = Down
		}
		out = append(out, statusRow{row: FriendRow(f.Name), name: f.Name, word: word, friend: f, beat: f.Beat})
	}
	return out
}

// StatusTransitions is the plan of the status transitions at this tick (the comment above):
// for each row, its departure from the record started, counted as a flap when it came back
// inside the dwell, or counted as a transition once it outlasted it, raising the row's one
// judgment or replacing its text; a row with no record recorded silently; an open judgment
// whose steps are all done answered by rule once its push is StatusRuleAfter old. With no
// beats read it does nothing, as the presence part does.
func StatusTransitions(s *Snapshot, r TickReq) Plan {
	var p Plan
	if s == nil || s.Fleet == nil || r.Beats == nil {
		return p
	}
	was, had := s.Fleet.Prop(PropStatusSeen)
	if !had && len(s.Fleet.Props()) >= ntable.LimitTableProps {
		return p // a fleet table at its properties' cap keeps no record: nothing is raised
	}
	seen := parseStatusSeen(was)
	onRow := map[string][]Open{}
	for _, o := range append(append([]Open(nil), s.Open...), s.Acked...) {
		if o.Note.Type == NStatus {
			onRow[o.Subject()] = append(onRow[o.Subject()], o)
		}
	}
	rule := r.AnswerRules && !s.RuleOff(RuleStatus)
	to := coordinatorName(s)
	if s.Coordinator == "" {
		to = "coordinator"
	}
	next := map[string]statusSeen{}
	var moved []string
	for _, row := range statusRows(s, r) {
		rec, ok := seen[row.row]
		started := row.started()
		if !ok {
			next[row.row] = statusSeen{Word: row.word, Started: started} // the first sight: recorded, nothing raised
			moved = append(moved, row.row+" "+row.word)
			continue
		}
		if rec.Started.IsZero() && !started.IsZero() && rec.Word == row.word && rec.Away.IsZero() {
			rec.Started = started // her daemon's first report of its start: its generation, recorded
		}
		changed := rec.Word != row.word
		generation := !started.IsZero() && !rec.Started.IsZero() && !started.Equal(rec.Started)
		judgment := openJudgment(onRow[row.row])
		if !changed && !generation {
			if !rec.Away.IsZero() {
				// back inside the dwell: a flap, counted. Flaps short of StatusFlapsRaise that
				// began more than StatusFlapWindow ago start the count again from this one.
				if d, ok := r.running(s.Now, stamp(rec.FlapSince)); rec.FlapSince.IsZero() || rec.Flaps < StatusFlapsRaise && ok && d > StatusFlapWindow {
					rec.FlapSince, rec.Flaps = rec.Away, 0
				}
				rec.Flaps++
				rec.Away = time.Time{}
				switch {
				case judgment != nil:
					n := judgment.Note
					n.What = withFlaps(n.What, rec)
					p.Updates = append(p.Updates, n) // the count shown, no push
				case rec.Flaps == StatusFlapsRaise:
					// a row that keeps leaving its status and coming back inside the dwell never
					// makes a transition: its third flap inside the window is the one judgment
					// "flapping", raised once until a transition counts (the flaps start again)
					p.Closes = append(p.Closes, onRow[row.row]...)
					p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NStatus, Primaries: []string{row.row}, Count: 1, What: flappingText(s, row, rec), Who: r.who(), At: s.Now,
						Marked: true, Decisions: append([]string(nil), TickDecisions[NStatus]...)})
					moved = append(moved, fmt.Sprintf("%s flapping (%d flaps)", row.row, rec.Flaps))
				}
			}
			next[row.row] = rec
			if rec.Flaps < StatusFlapsRaise {
				ruleAnswer(&p, s, r, row, judgment, rule) // a row still flapping is never answered by rule
			}
			continue
		}
		if rec.Away.IsZero() {
			rec.Away = s.Now // left its status: counted once it holds for the dwell
			next[row.row] = rec
			continue
		}
		if d, ok := r.running(s.Now, stamp(rec.Away)); ok && d < StatusDwell {
			next[row.row] = rec
			continue
		}
		now := statusSeen{Word: row.word, N: rec.N + 1, Started: started}
		next[row.row] = now
		moved = append(moved, fmt.Sprintf("%s %s -> %s (transition %d)", row.row, rec.Word, row.word, now.N))
		what, _ := statusText(s, r, row, rec, now, generation && !changed)
		if rec.Flaps > 0 {
			what = withFlaps(what, rec)
		}
		if judgment != nil {
			// the row's one open judgment: its text replaced, its id and alias kept, and the
			// seat told it changed
			n := judgment.Note
			n.What, n.At, n.Who = what, s.Now, r.who()
			p.Updates = append(p.Updates, n)
			for _, o := range onRow[row.row] {
				if o.Note.ID != n.ID {
					p.Closes = append(p.Closes, o)
				}
			}
			ref := n.ID
			if n.Alias != "" {
				ref = n.Alias + " (" + n.ID + ")"
			}
			p.Notes = append(p.Notes, Note{Kind: Happened, Type: NStatusChanged, Primaries: []string{row.row}, Count: 1, Who: r.who(), To: to, At: s.Now,
				What: ref + " changed: " + what, Hint: "run: nova-sprint inbox; ack it once its steps are run"})
			continue
		}
		p.Closes = append(p.Closes, onRow[row.row]...) // an answered one: closed, a new one raised
		p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NStatus, Primaries: []string{row.row}, Count: 1, What: what, Who: r.who(), At: s.Now,
			Marked: true, Decisions: append([]string(nil), TickDecisions[NStatus]...)})
	}
	if value := formatStatusSeen(next); value != was {
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropStatusSeen, Value: value, Was: was, WasAbsent: !had})
		// the record's own unit: a store that refuses the fleet's write refuses this unit, and
		// the part moves nothing, as any part refused whole on a bound
		what := "status seen"
		if len(moved) > 0 {
			what += ": " + Preview(moved, ", ")
		}
		p.Units = append(p.Units, Unit{Key: PropStatusSeen, Moved: what})
	}
	return p
}

// openJudgment is the row's open status judgment (not one answered and kept), nil for none.
func openJudgment(open []Open) *Open {
	for i := range open {
		if open[i].Note.Kind == Judgment {
			return &open[i]
		}
	}
	return nil
}

var flapsSuffix = regexp.MustCompile(`; flapped \d+ times since \S+ \(each back inside \S+\)$`)

// withFlaps is the text with the record's flaps said at its end, in place of any said before.
func withFlaps(what string, rec statusSeen) string {
	what = flapsSuffix.ReplaceAllString(what, "")
	if rec.Flaps == 0 {
		return what
	}
	return fmt.Sprintf("%s; flapped %d times since %s (each back inside %s)", what, rec.Flaps, stamp(rec.FlapSince), StatusDwell)
}

// ruleAnswer answers by rule the row's open judgment once every step it names is done and its
// last push is StatusRuleAfter of running time old: closed, with the decided note. A status
// judgment is never answered as it is raised: the seat has it first.
func ruleAnswer(p *Plan, s *Snapshot, r TickReq, row statusRow, judgment *Open, rule bool) {
	if !rule || judgment == nil {
		return
	}
	if d, ok := r.running(s.Now, stamp(judgment.Note.At)); !ok || d < StatusRuleAfter {
		return
	}
	done, steps := statusDone(s, r, row)
	if !done {
		return
	}
	p.Closes = append(p.Closes, *judgment)
	p.Notes = append(p.Notes, decided(*judgment, RuleSaid(RuleStatus, "every step is done: "+steps), ruleWho(RuleStatus), s.Now, row.row))
}

// statusDone says every step of the row's status now is done, and names them: a hold (the
// coordinator's own act), a fleet member the presence part has brought up, and a friend come
// up whose four steps are done.
func statusDone(s *Snapshot, r TickReq, row statusRow) (bool, string) {
	switch {
	case row.word == Held:
		return true, "the coordinator's hold"
	case row.friend == nil && row.word == Up:
		return s.MemberCtl(row.name).F("status") == Up, "the member is up and dealt to"
	case row.friend != nil && row.word == Up:
		steps, done := friendSteps(s, r, row)
		return done, steps
	}
	return false, ""
}

// statusText is the judgment's text, with the exact verbs, and whether every step it names
// is already done (the rule's answer). Its decisions are ack and wait (TickDecisions): the
// verbs are the coordinator's to run, and the next transition of the row closes it.
func statusText(s *Snapshot, r TickReq, row statusRow, prev, next statusSeen, generation bool) (string, bool) {
	at := stamp(s.Now)
	if row.friend == nil {
		return memberText(s, row, prev, next, at)
	}
	f := row.name
	head := fmt.Sprintf("friend %s is %s (was %s) at %s, transition %d", f, row.word, prev.Word, at, next.N)
	if generation {
		head = fmt.Sprintf("friend %s's daemon started again at %s (its start before: %s), she is %s, at %s, transition %d",
			f, stamp(next.Started), stamp(prev.Started), row.word, at, next.N)
	}
	coord := coordinatorName(s)
	wake := "nova-friend ping --as " + coord + " --to " + f + " --wake"
	restart := "launchctl kickstart -k gui/$(id -u)/com.nova.friend-" + f
	switch row.word {
	case Up:
		steps, done := friendSteps(s, r, row)
		return head + ", on " + orDash(row.friend.Evidence) + "; bring her up in four steps: " + steps, done
	case Held:
		return head + ": the coordinator's hold (" + orDash(row.friend.Why) + "); her started cards finish, the rest went back to ready; release: nova-sprint unhold " + f, true
	}
	what := head + ": " + orDash(row.friend.Evidence)
	if row.friend.DaemonOnly {
		what += "; her daemon answers and her session does not"
	}
	what += "; " + lastBeat(row.beat, s.Now) + "; wake her: " + wake + "; if her daemon is gone, on her machine: " + restart + "; or hold her: nova-sprint hold " + f + " --reason <text>"
	return what, false
}

// friendSteps is the four steps of a friend come up, in order, each with what the machine
// already did, and whether all four are done.
func friendSteps(s *Snapshot, r TickReq, row statusRow) (string, bool) {
	f, seat := row.name, row.friend
	var rep FriendReport
	if seat.Beat.Friend != nil {
		rep = *seat.Beat.Friend
	}
	var parts []string
	all := true
	step := func(text string, done bool) {
		all = all && done
		word := "to do"
		if done {
			word = "done"
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(parts)+1, text, word))
	}
	// 1. update: the daemon runs the current build
	installed := rep.Build
	if installed == "" {
		installed = "a build it did not report"
	}
	match := BuildMatch(rep.Build, seat.Current)
	text := fmt.Sprintf("update: her daemon runs %s, the current build is %s", installed, orDash(seat.Current))
	if match == BuildUnknown && rep.Build != "" && seat.Current != "" {
		text += ": an unstamped build cannot be compared"
	}
	if match != BuildSame {
		text += "; run: nova-update, then launchctl kickstart -k gui/$(id -u)/com.nova.friend-" + f
	}
	step(text, match == BuildSame)
	// 2. check: what her daemon reported of itself, and her session's evidence. No beat word
	// reports a harness check yet, so the step is the coordinator's: nova-friend check.
	present := !rep.Present.IsZero() && !rep.Started.IsZero() && !rep.Present.Before(rep.Started)
	text = fmt.Sprintf("check: daemon %s, %s, presence %s; run: nova-friend check %s and read its CHECK DAEMON, CHECK HARNESS and presence lines",
		beatWord(seat.Beat, s.Now), daemonWord(rep), orDash(seat.Evidence), f)
	step(text, false)
	// 3. snap to present: the note her daemon sent on its start
	if present {
		text = "snap to present: her daemon sent the present on its start at " + stamp(rep.Present)
	} else {
		text = "snap to present: her daemon sent no present since its start; run: " + presentSend(f, s.Now)
	}
	step(text, present)
	// 4. into the sprint: not held, the server's evidence, a take within TakeWithin
	held := seat.Status == Held
	take := lastTake(s, f)
	took := !take.IsZero() && s.Now.Sub(take) <= TakeWithin && !take.After(s.Now)
	text = "into the sprint: "
	if held {
		text += "held"
	} else {
		text += "not held"
	}
	text += ", evidence " + orDash(seat.Evidence)
	if took {
		text += ", a take at " + stamp(take)
	} else {
		text += fmt.Sprintf(", no take within %s; run: nova-sprint where, and %s", TakeWithin, "nova-friend ping --as "+coordinatorName(s)+" --to "+f+" --wake")
	}
	step(text, !held && took)
	return strings.Join(parts, "; "), all
}

// flappingText is the judgment of a row that left its status StatusFlapsRaise times inside
// StatusFlapWindow and came back inside the dwell each time: no transition counted, so
// nothing else would tell the seat. It ends with the flaps (withFlaps), which its later flaps
// rewrite in place.
func flappingText(s *Snapshot, row statusRow, rec statusSeen) string {
	at := stamp(s.Now)
	var what string
	if row.friend == nil {
		m := row.name
		what = fmt.Sprintf("fleet member %s is flapping at %s: it is %s and keeps leaving it for less than %s, so no transition counts; %s, width %d; "+
			"its beat on %s (nova-sprint fleet beat %s) is unsteady; or hold it: nova-sprint hold %s --reason <text>",
			m, at, row.word, StatusDwell, lastBeat(row.beat, s.Now), s.Width(m), m, m, m)
	} else {
		f := row.name
		what = fmt.Sprintf("friend %s is flapping at %s: she is %s and keeps leaving it for less than %s, so no transition counts; on %s; %s; "+
			"wake her: nova-friend ping --as %s --to %s --wake; or hold her: nova-sprint hold %s --reason <text>",
			f, at, row.word, StatusDwell, orDash(row.friend.Evidence), lastBeat(row.beat, s.Now), coordinatorName(s), f, f)
	}
	return withFlaps(what, rec)
}

// memberText is a fleet member's judgment: its last beat and its width, and the verbs.
func memberText(s *Snapshot, row statusRow, prev, next statusSeen, at string) (string, bool) {
	m := row.name
	head := fmt.Sprintf("fleet member %s is %s (was %s) at %s, transition %d: %s, width %d", m, row.word, prev.Word, at, next.N, lastBeat(row.beat, s.Now), s.Width(m))
	switch row.word {
	case Up:
		if s.MemberCtl(m).F("status") != Up {
			// past TickMaxMoves ups at once: the presence part brings it up at a coming tick
			return head + "; the presence part has not brought it up yet (more than " + strconv.Itoa(TickMaxMoves) + " members came up at once): it is dealt cards once it has; its width: nova-sprint fleet up " + m + " --width <n>", false
		}
		return head + "; the tick deals it cards from now; its width: nova-sprint fleet up " + m + " --width <n>", true
	case Held:
		return head + "; the coordinator's hold (hold " + m + "); release: nova-sprint unhold " + m, true
	}
	return head + "; its unfinished cards were dealt round the members up; bring it back: its beat on " + m + " (nova-sprint fleet beat " + m + ") has stopped, start its member loop again; or hold it: nova-sprint hold " + m + " --reason <text>", false
}

// lastBeat names the row's last beat and its age, with the beat windows missed.
func lastBeat(b Beat, now time.Time) string {
	if !b.Beaten() {
		return "it has never beaten"
	}
	text := "its last beat " + stamp(b.At) + " (" + ago(now.Sub(b.At))
	if k := b.Missed(now); k > 0 {
		text += fmt.Sprintf(", %d beat windows of %s missed", k, BeatDeadline)
	}
	return text + ")"
}

func beatWord(b Beat, now time.Time) string {
	if !b.Beaten() {
		return "never beat"
	}
	if b.Fresh(now) {
		return "beating (" + ago(now.Sub(b.At)) + ")"
	}
	return "silent since " + stamp(b.At)
}

// daemonWord is what her daemon reported of its own start: when, and that it has beat since,
// and that no harness check is reported (no beat word carries one yet).
func daemonWord(rep FriendReport) string {
	if rep.Started.IsZero() {
		return "her daemon reported no start; no harness check reported"
	}
	return "her daemon started at " + stamp(rep.Started) + " and has beat since; no harness check reported"
}

// presentSend is the nova-bus line that sends her the present by hand.
func presentSend(f string, now time.Time) string {
	return fmt.Sprintf("nova-bus send --to %s --subject present --body \"PRESENT %s: skip every older message and card; your work is your row in nova-sprint where\"", f, stamp(now))
}

// lastTake is the newest take of a card on her row: working, or done.
func lastTake(s *Snapshot, f string) time.Time {
	var last time.Time
	row := FriendRow(f)
	for _, col := range []string{Working, DoneOK, DoneFailed} {
		for _, c := range s.Fleet.Cell(row, col) {
			if t := stampAt(c, "taken"); t.After(last) {
				last = t
			}
		}
	}
	return last
}

func coordinatorName(s *Snapshot) string {
	if s.Coordinator == "" {
		return "<coordinator>"
	}
	return s.Coordinator
}

var buildRevision = regexp.MustCompile(`[0-9a-f]{12}`)

// The answers of BuildMatch.
const (
	BuildSame      = "same"
	BuildDifferent = "different"
	BuildUnknown   = "unknown"
)

// BuildMatch compares two builds' version lines: the same twelve hex of a revision when both
// carry one (buildinfo's vcs stamp, whatever else each says) is the same build, and two
// revisions that differ are different; a build with no revision that is unstamped ("devel",
// buildinfo's word for a build with no origin recorded) or empty is unknown, never the same,
// whatever the other is; else two release tags compare as text.
func BuildMatch(a, b string) string {
	ra, rb := buildRevision.FindAllString(a, -1), buildRevision.FindAllString(b, -1)
	switch {
	case len(ra) > 0 && len(rb) > 0:
		if slices.ContainsFunc(ra, func(x string) bool { return slices.Contains(rb, x) }) {
			return BuildSame
		}
		return BuildDifferent
	case unstamped(a, ra) || unstamped(b, rb):
		return BuildUnknown
	case a == b:
		return BuildSame
	}
	return BuildDifferent
}

// unstamped says a build line with these revisions names no build that can be compared:
// empty, or devel with no revision.
func unstamped(build string, revs []string) bool {
	return strings.TrimSpace(build) == "" || len(revs) == 0 && strings.Contains(build, "devel")
}
