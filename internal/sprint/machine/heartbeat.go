package machine

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The heartbeat and what is computed from it with no loop running (1.4.1). The
// loop that holds the lease writes its fields by field, through the lease
// part of the next tick's RT1 step (A3), so the heartbeat costs no round trip
// of its own; a loop that does not hold it writes only idle_loop and idle_at.
// `inbox`, `where` and the sprint line of every verb read the heartbeat and
// the clock and compute, at read time, the three groups and the one line
// below. No rule reads the heartbeat.

// The spans of 1.4.1's table and the view's line.
const (
	// NotTickingAfter is how old tick_at is when a RUNNING machine "is not
	// ticking", and looked_at when a STOPPED one has "no run loop looking".
	NotTickingAfter = 15 * time.Second
	// NotTickingLine is how old tick_at is when the view's line reads NOT
	// TICKING for a RUNNING machine.
	NotTickingLine = 5 * time.Second
	// FailingAfter is the failed ticks in a row at which "the tick keeps
	// failing".
	FailingAfter = 3
)

// The groups' types (1.4.1), as the inbox shows them.
const (
	GroupNotTicking = "the machine is not ticking"
	GroupNoLoop     = "no run loop is looking"
	GroupFailing    = "the tick keeps failing"
)

// Heartbeat is {p}heartbeat as read (1.4.1). Times are the store's wall ms; a
// field never written is zero, or empty.
type Heartbeat struct {
	TickAt, LookedAt, IdleAt int64
	Ticks                    uint64
	Failures                 int
	Error                    string
	Backlog, DueNow          uint64
	Agenda, HeldQ            string // the head sizes read, with "+" when the queue held more
	Owner, Gen, IdleLoop     string
	Rules                    string
}

// ParseHeartbeat reads the heartbeat's fields. A field that is not a number
// where one is written is left zero: the heartbeat is for display, and no
// rule reads it.
func ParseHeartbeat(f map[string]string) Heartbeat {
	i := func(name string) int64 { n, _ := strconv.ParseInt(f[name], 10, 64); return n }
	u := func(name string) uint64 { n, _ := strconv.ParseUint(f[name], 10, 64); return n }
	return Heartbeat{TickAt: i("tick_at"), LookedAt: i("looked_at"), IdleAt: i("idle_at"), Ticks: u("ticks"),
		Failures: int(i("failures")), Error: f["error"], Backlog: u("backlog"), DueNow: u("due_now"),
		Agenda: f["agenda"], HeldQ: f["heldq"], Owner: f["owner"], Gen: f["gen"], IdleLoop: f["idle_loop"], Rules: f["rules"]}
}

// Clock is the running clock as read ({p}clock, 1.2): STOPPED since
// StoppedSince when Stopped, and R, the running ms, at the read.
type Clock struct {
	Stopped      bool
	StoppedSince int64
	StoppedMS    int64
	R            int64
}

// ClockOf is the clock of a clock read (IT30's sprint-key read).
func ClockOf(c sprintfn.ClockResult) Clock {
	out := Clock{}
	num := func(p *string) int64 {
		if p == nil {
			return 0
		}
		n, _ := strconv.ParseInt(*p, 10, 64)
		return n
	}
	if !tset.ClockRunning(c.Clock.StoppedSinceMS) {
		out.Stopped, out.StoppedSince = true, num(c.Clock.StoppedSinceMS)
	}
	out.StoppedMS = num(c.Clock.StoppedMS)
	out.R, _ = strconv.ParseInt(c.R, 10, 64)
	return out
}

// MachineLine is the view's one line under the title (1.4.1, T3): STOPPED;
// for a RUNNING machine that has not ticked for 5 s, NOT TICKING and the
// seconds since the last tick (its state is RUNNING, so the line never says
// STOPPED for it); running and catching up while the backlog, the agenda or
// the due entries are not empty (DECISIONS 6); running otherwise. wall is the
// store's wall ms at the read. The summary of landed work is the view's own,
// from the tables.
func MachineLine(hb Heartbeat, c Clock, wall int64) string {
	if c.Stopped {
		return "STOPPED"
	}
	if since := time.Duration(wall-hb.TickAt) * time.Millisecond; hb.TickAt == 0 || since >= NotTickingLine {
		if hb.TickAt == 0 {
			return "NOT TICKING"
		}
		return fmt.Sprintf("NOT TICKING %ds", int64(since/time.Second))
	}
	if keys := headCount(hb.Agenda) + headCount(hb.HeldQ); hb.Backlog > 0 || keys > 0 || hb.DueNow > 0 {
		return fmt.Sprintf("running (catching up: %d lines, %s keys, %d due)", hb.Backlog, keysText(hb), hb.DueNow)
	}
	return "running"
}

// headCount is a head size as the heartbeat holds it.
func headCount(s string) int {
	n, _ := strconv.Atoi(trimPlus(s))
	return n
}

func trimPlus(s string) string {
	if len(s) > 0 && s[len(s)-1] == '+' {
		return s[:len(s)-1]
	}
	return s
}

// keysText is the agenda's and the held queue's keys, "n+" when a head was
// full.
func keysText(hb Heartbeat) string {
	n := headCount(hb.Agenda) + headCount(hb.HeldQ)
	s := strconv.Itoa(n)
	if (len(hb.Agenda) > 0 && hb.Agenda[len(hb.Agenda)-1] == '+') || (len(hb.HeldQ) > 0 && hb.HeldQ[len(hb.HeldQ)-1] == '+') {
		s += "+"
	}
	return s
}

// InboxGroups are the groups 1.4.1's table computes from the heartbeat and
// the clock at read time, with no loop running: RUNNING and tick_at older than
// 15 s, the machine is not ticking (start the run loop; stop); STOPPED and
// looked_at older than 15 s, no run loop is looking, so R17 cannot name moves
// due (start the run loop); failures at least 3, the tick keeps failing,
// showing the error (where, log --since, stop).
func InboxGroups(hb Heartbeat, c Clock, wall int64) []sprint.Group {
	var out []sprint.Group
	old := func(at int64) bool { return at == 0 || time.Duration(wall-at)*time.Millisecond > NotTickingAfter }
	group := func(id, what string, cmds ...sprint.Command) sprint.Group {
		return sprint.Group{ID: id, Kind: sprint.Judgment, Type: id, Count: 1, What: what, Commands: cmds}
	}
	run := sprint.Command{Decision: "start the run loop", Lines: []string{"nova-sprint run"}}
	if !c.Stopped && old(hb.TickAt) {
		out = append(out, group(GroupNotTicking, sinceText("the last tick", hb.TickAt, wall), run,
			sprint.Command{Decision: "stop", Lines: []string{"nova-sprint stop"}}))
	}
	if c.Stopped && old(hb.LookedAt) {
		out = append(out, group(GroupNoLoop, sinceText("the last look", hb.LookedAt, wall), run))
	}
	if hb.Failures >= FailingAfter {
		out = append(out, group(GroupFailing, fmt.Sprintf("%d failed ticks in a row: %s", hb.Failures, hb.Error),
			sprint.Command{Decision: "where", Lines: []string{"nova-sprint where"}},
			sprint.Command{Decision: "log", Lines: []string{"nova-sprint log --since 10m"}},
			sprint.Command{Decision: "stop", Lines: []string{"nova-sprint stop"}}))
	}
	return out
}

// sinceText says how long ago a time was, or that it never was.
func sinceText(what string, at, wall int64) string {
	if at == 0 {
		return what + ": never"
	}
	return fmt.Sprintf("%s: %ds ago", what, (wall-at)/1000)
}
