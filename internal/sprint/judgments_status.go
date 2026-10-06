package sprint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// Status transitions (docs/SPEC-SPRINT.md section 8, "Status transitions"; the owner,
// 2026-10-06 2:55 PM ET, after a friend was brought up on a stale daemon binary and flooded
// with yesterday's notes: "Please make this process mechanical. You cannot remember to do
// this reliably on your own in my experience, so if it is mechanical, make the machine
// prompt you when a friend changes their status"). Every change of a friend's or a fleet
// member's status, as the server computes it at the tick, raises one judgment of type
// NStatus on the row, pushed to the seat inbox as every judgment is: anything to up, up to
// down (a friend whose daemon answers and whose session does not is down, and her judgment
// says so: docs/TERMINOLOGY.md, anything but up is down), held and unheld, and, for a friend, a new generation of her daemon (a start her beat names that
// the transitions have not seen). The text names the transition, the clock time and the
// exact verbs; for a friend come up, the four steps in order with what the machine already
// did. The status the transitions last saw is kept on the fleet table (PropStatusSeen: the
// word, the transition's number, her daemon's start), so a judgment is raised once a
// transition, never once a tick, and a restart of the server reads the record back and
// replays none; the first sight of a row records its status and raises nothing. The next
// transition of a row closes the judgment of the one before, answered or not. A judgment
// whose every step is already done is answered by rule (RuleStatus, run --answer-rules): at
// once when it is raised, or at the first tick that finds its steps done.

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
)

// PropStatusSeen is the fleet property that keeps every row's status as the transitions
// last saw it, one property for the whole fleet (a table holds at most
// ntable.LimitTableProps): "<row>=<word>,<transition>,<daemon start RFC3339 or ->" for each
// row, one after another with a blank between, in row order.
const PropStatusSeen = "status_seen"

func init() {
	TickDecisions[NStatus] = []string{"ack", "wait"}
}

// statusSeen is a row's PropStatusSeen record.
type statusSeen struct {
	Word    string
	N       int
	Started time.Time
}

func (r statusSeen) String() string {
	started := "-"
	if !r.Started.IsZero() {
		started = stamp(r.Started)
	}
	return fmt.Sprintf("%s,%d,%s", r.Word, r.N, started)
}

// parseStatusSeen is the record by row; an entry that cannot be read is no record, and its
// row is seen again for the first time.
func parseStatusSeen(v string) map[string]statusSeen {
	out := map[string]statusSeen{}
	for _, e := range strings.Fields(v) {
		row, rest, ok := strings.Cut(e, "=")
		f := strings.Split(rest, ",")
		if !ok || row == "" || len(f) != 3 {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 0 || f[0] == "" {
			continue
		}
		r := statusSeen{Word: f[0], N: n}
		if f[2] != "-" {
			t, err := time.Parse(time.RFC3339, f[2])
			if err != nil {
				continue
			}
			r.Started = t
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
// for each row whose status, or whose daemon's start, differs from the record, the record
// moved on, the judgments of the row's last transition closed, and one judgment raised; a
// row with no record recorded silently; an open judgment of a friend come up whose steps
// are all done now answered by rule. With no beats read it does nothing, as the presence
// part does.
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
		changed := rec.Word != row.word
		generation := !started.IsZero() && !rec.Started.IsZero() && !started.Equal(rec.Started)
		if !changed && !generation {
			if rec.Started.IsZero() && !started.IsZero() {
				rec.Started = started // her daemon's first report of its start: its generation, recorded
			}
			next[row.row] = rec
			ruleAnswer(&p, s, r, row, onRow[row.row], rule)
			continue
		}
		now := statusSeen{Word: row.word, N: rec.N + 1, Started: started}
		next[row.row] = now
		moved = append(moved, fmt.Sprintf("%s %s -> %s (transition %d)", row.row, rec.Word, row.word, now.N))
		p.Closes = append(p.Closes, onRow[row.row]...)
		what, done := statusText(s, r, row, rec, now, generation && !changed)
		n := Note{Kind: Judgment, Type: NStatus, Primaries: []string{row.row}, Count: 1, What: what, Who: r.who(), At: s.Now,
			Marked: true, Decisions: append([]string(nil), TickDecisions[NStatus]...)}
		if done && rule {
			n.Kind, n.Who, n.What = Acknowledged, ruleWho(RuleStatus), RuleSaid(RuleStatus, "every step is done")+"; "+what
		}
		p.Notes = append(p.Notes, n)
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

// ruleAnswer answers by rule the open judgment of a friend's coming up once every one of
// its four steps is done: closed, with the decided note naming the steps.
func ruleAnswer(p *Plan, s *Snapshot, r TickReq, row statusRow, open []Open, rule bool) {
	if !rule || row.friend == nil || row.word != Up {
		return
	}
	var judged []Open
	for _, o := range open {
		if o.Note.Kind == Judgment {
			judged = append(judged, o)
		}
	}
	if len(judged) == 0 {
		return
	}
	steps, done := friendSteps(s, r, row)
	if !done {
		return
	}
	p.Closes = append(p.Closes, judged...)
	p.Notes = append(p.Notes, decided(judged[0], RuleSaid(RuleStatus, "every step is done: "+steps), ruleWho(RuleStatus), s.Now, row.row))
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
	same := SameBuild(rep.Build, seat.Current)
	text := fmt.Sprintf("update: her daemon runs %s, the current build is %s", installed, orDash(seat.Current))
	if !same {
		text += "; run: nova-update, then launchctl kickstart -k gui/$(id -u)/com.nova.friend-" + f
	}
	step(text, same)
	// 2. check: her daemon beats, her session's evidence, and her daemon's start check
	fresh := seat.Beat.Fresh(s.Now)
	present := !rep.Present.IsZero() && !rep.Started.IsZero() && !rep.Present.Before(rep.Started)
	text = fmt.Sprintf("check: daemon %s, harness %s, presence %s", beatWord(seat.Beat, s.Now), harnessWord(present, rep), orDash(seat.Evidence))
	if !fresh || !present {
		text += "; run: nova-friend check " + f + " and read its CHECK DAEMON, CHECK HARNESS and presence lines"
	}
	step(text, fresh && present)
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

// memberText is a fleet member's judgment: its last beat and its width, and the verbs.
func memberText(s *Snapshot, row statusRow, prev, next statusSeen, at string) (string, bool) {
	m := row.name
	head := fmt.Sprintf("fleet member %s is %s (was %s) at %s, transition %d: %s, width %d", m, row.word, prev.Word, at, next.N, lastBeat(row.beat, s.Now), s.Width(m))
	switch row.word {
	case Up:
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

func harnessWord(present bool, rep FriendReport) string {
	switch {
	case rep.Started.IsZero():
		return "unreported (her daemon names no start)"
	case present:
		return "checked by her daemon's start at " + stamp(rep.Started)
	}
	return "not checked since her daemon's start at " + stamp(rep.Started)
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

// SameBuild says two builds' version lines name the same build: the same twelve hex of a
// revision when both carry one (buildinfo's vcs stamp, whatever else each says), else the
// same text. An empty build is never the same as any.
func SameBuild(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, rb := buildRevision.FindAllString(a, -1), buildRevision.FindAllString(b, -1)
	if len(ra) > 0 && len(rb) > 0 {
		return slices.ContainsFunc(ra, func(x string) bool { return slices.Contains(rb, x) })
	}
	return a == b
}
