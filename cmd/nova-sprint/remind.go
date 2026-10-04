package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// remind: a timer, a note for an actor at a time, fired once by the tick
// (docs/SPEC-SPRINT.md, "Timers"; internal/sprint/timer.go; tla/Timers.tla).
// The setter is told how it ended: fired (with its lateness), seen, or
// expired (with the reason).

// timerView is one timer for a program: the timer, its state and its end.
type timerView struct {
	sprint.Timer
	State   string    `json:"state"`
	Fired   time.Time `json:"fired,omitzero"`
	Late    string    `json:"late,omitempty"`
	Seen    time.Time `json:"seen,omitzero"`
	Expired time.Time `json:"expired,omitzero"`
	Reason  string    `json:"reason,omitempty"`
	Missed  bool      `json:"missed,omitempty"`
	// Ago is how long ago it fired, or was due when it never did.
	Ago string `json:"ago,omitempty"`
}

func timerViewOf(t sprint.Timer, e sprint.TimerEnd, now time.Time) timerView {
	v := timerView{Timer: t, State: sprint.TimerState(t, e), Fired: e.Fired, Seen: e.Seen, Expired: e.Expired, Reason: e.Reason, Missed: sprint.Missed(t, e)}
	if v.Seen.IsZero() && v.State == sprint.TimerSeen {
		v.Seen = t.Acked
	}
	if !e.Fired.IsZero() {
		v.Late = e.Lateness(t).Round(time.Second).String()
		v.Ago = ageWord(now.Sub(e.Fired)) + " ago"
	} else if v.State == sprint.TimerExpired {
		v.Ago = "due " + ageWord(now.Sub(t.Due)) + " ago"
	}
	return v
}

// line is the timer on one line.
func (v timerView) line() string {
	s := fmt.Sprintf("TIMER %s state=%s for=%s by=%s due=%s", v.ID, v.State, v.Actor, v.Setter, v.Due.UTC().Format(time.RFC3339))
	if !v.Fired.IsZero() {
		s += " fired=" + v.Fired.UTC().Format(time.RFC3339) + " late=" + v.Late
	}
	if v.Missed {
		s += " MISSED"
		if v.State == sprint.TimerFired {
			s += " unseen"
		}
	}
	if v.Ago != "" {
		s += " (" + v.Ago + ")"
	}
	if v.Reason != "" {
		s += " reason=" + oneline.Quote(v.Reason)
	}
	return s + " note=" + oneline.Quote(v.Note)
}

// missedLine is a missed timer as a returning session reads it first.
func (v timerView) missedLine() string {
	if v.State == sprint.TimerFired {
		return fmt.Sprintf("timer %s (%s) fired %s, unseen: run: nova-sprint remind --ack %s", v.ID, oneLine(v.Note), v.Ago, v.ID)
	}
	return fmt.Sprintf("timer %s (%s) expired: %s; run: nova-sprint remind --ack %s", v.ID, oneLine(v.Note), v.Reason, v.ID)
}

// parseAt is --at: an RFC3339 time, or a clock time in zone (7:30pm, 7pm,
// 19:30), the next one at or after now.
func parseAt(s string, now time.Time, zone *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	w := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	for _, layout := range []string{"3:04pm", "3pm", "15:04"} {
		c, err := time.Parse(layout, w)
		if err != nil {
			continue
		}
		n := now.In(zone)
		t := time.Date(n.Year(), n.Month(), n.Day(), c.Hour(), c.Minute(), 0, 0, zone)
		if !t.After(now) {
			t = time.Date(n.Year(), n.Month(), n.Day()+1, c.Hour(), c.Minute(), 0, 0, zone)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--at %q is no RFC3339 time (2026-10-04T19:30:00-04:00) and no clock time (7:30pm, 7pm, 19:30)", s)
}

func (a *app) cmdRemind(args []string, stdout, stderr io.Writer) int {
	const name = "remind"
	fs, c := a.verbSetup(name)
	in := fs.Duration("in", 0, "set a timer due this long from now (90s, 2m, 1h30m)")
	at := fs.String("at", "", "set a timer due at this time: RFC3339, or a clock time today or, when past, tomorrow (7:30pm, 19:30), in this machine's zone")
	note := fs.String("note", "", "what the timer is for: the text it raises")
	whom := fs.String("for", "", "the actor it is raised to: the seat's holder (a judgment) or a friend (a note); default the caller (--actor)")
	within := fs.Duration("within", sprint.TimerWithin, "the window after the due time: a timer not fired, or fired and not seen, by its end expires and the setter is told")
	list := fs.Bool("list", false, "list the timers: pending, fired (when, how late, seen or not), expired (why)")
	missed := fs.Bool("missed", false, "with --list: only the ones fired and unseen, or expired and not acknowledged")
	cancel := fs.String("cancel", "", "end the pending timer with this id unfired")
	ack := fs.String("ack", "", "mark the fired timer with this id seen (an expired one: acknowledged, off the missed list)")
	dry := fs.Bool("dry-run", false, "say what the set, --cancel or --ack would do and write nothing")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	forms := 0
	for _, on := range []bool{*in != 0 || *at != "", *list, *cancel != "", *ack != ""} {
		if on {
			forms++
		}
	}
	if forms != 1 {
		return refuse(stderr, name, "give one of: --in <duration> | --at <time> (with --note <text>), --list [--missed], --cancel <id>, --ack <id>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	switch {
	case *list:
		return a.remindList(ctx, st, *c, *whom, *missed, stdout, stderr)
	case *cancel != "":
		t, err := st.CancelTimer(ctx, *cancel, *dry)
		if err != nil {
			return a.remindFailed(err, stderr)
		}
		sayOK(stdout, c.json, name, fmt.Sprintf("REMIND OK cancelled=%s%s", t.ID, dryWord(*dry)), map[string]any{"cancelled": t.ID, "dry_run": *dry})
		return 0
	case *ack != "":
		t, e, changed, err := st.AckTimer(ctx, *ack, *dry)
		if err != nil {
			return a.remindFailed(err, stderr)
		}
		state := sprint.TimerState(t, e)
		sayOK(stdout, c.json, name, fmt.Sprintf("REMIND OK acked=%s state=%s changed=%t%s", t.ID, state, changed, dryWord(*dry)), map[string]any{"acked": t.ID, "state": state, "changed": changed, "dry_run": *dry})
		return 0
	}
	var problems []string
	if *in != 0 && *at != "" {
		problems = append(problems, "--in and --at both given: one due time")
	}
	if *in < 0 {
		problems = append(problems, "--in wants a duration above zero")
	}
	if strings.TrimSpace(*note) == "" {
		problems = append(problems, "--note <text> says what the timer is for")
	}
	if *within <= 0 {
		problems = append(problems, "--within wants a duration above zero")
	}
	now := a.now()
	due := now.Add(*in)
	if *at != "" {
		if due, err = parseAt(*at, now, a.zone()); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, name, strings.Join(problems, "; "))
	}
	actor := *whom
	if actor == "" {
		actor = c.actor
	}
	t, err := st.SetTimer(ctx, sprint.Timer{Actor: actor, Setter: c.actor, Note: *note, Due: due, Within: *within}, *dry)
	if err != nil {
		return a.remindFailed(err, stderr)
	}
	id := t.ID
	if *dry {
		id = "-"
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("REMIND OK id=%s for=%s due=%s in=%s within=%s%s; it fires once, on the first tick at or after its due time; run: nova-sprint remind --list",
		id, t.Actor, t.Due.UTC().Format(time.RFC3339), t.Due.Sub(now).Round(time.Second), t.Within, dryWord(*dry)),
		map[string]any{"id": t.ID, "for": t.Actor, "by": t.Setter, "due": t.Due, "within": t.Within.String(), "dry_run": *dry})
	return 0
}

func dryWord(dry bool) string {
	if dry {
		return " dry-run: nothing written"
	}
	return ""
}

// remindFailed says why a remind verb wrote nothing: a refusal of the timer
// (exit 1) or a store that did not answer (exit 2).
func (a *app) remindFailed(err error, stderr io.Writer) int {
	if errors.Is(err, store.ErrTimer) {
		fmt.Fprintf(stderr, "%s remind REFUSED: %s; run: nova-sprint remind --list\n", prog, oneline.Escape(strings.TrimPrefix(err.Error(), store.ErrTimer.Error()+": ")))
		return 1
	}
	return a.readFailed("remind", err, stderr)
}

// remindList is remind --list: every timer, or the missed ones, of one actor
// or all, in the order set, cut at --max with its MORE line.
func (a *app) remindList(ctx context.Context, st *store.Store, c common, whom string, missed bool, stdout, stderr io.Writer) int {
	views, err := a.timerViews(ctx, st, whom, missed)
	if err != nil {
		return a.readFailed("remind", err, stderr)
	}
	total := len(views)
	if c.max > 0 && len(views) > c.max {
		views = views[:c.max]
	}
	if c.json {
		if views == nil {
			views = []timerView{}
		}
		sayOK(stdout, true, "remind", "", map[string]any{"timers": views, "shown": len(views), "total": total})
		return 0
	}
	for _, v := range views {
		fmt.Fprintln(stdout, v.line())
	}
	if len(views) < total {
		fmt.Fprintf(stdout, "MORE shown=%d total=%d; run: nova-sprint remind --list --max 0\n", len(views), total)
	}
	fmt.Fprintf(stdout, "REMIND OK timers=%d missed=%t\n", total, missed)
	return 0
}

// timerViews is the timers of one actor ("" all), the missed ones alone when
// missed, in the order set.
func (a *app) timerViews(ctx context.Context, st *store.Store, whom string, missed bool) ([]timerView, error) {
	b, err := st.TimerBook(ctx)
	if err != nil {
		return nil, err
	}
	now := a.now()
	var out []timerView
	for _, t := range b.Timers.All {
		e := b.End(t.ID)
		if (whom == "" || t.Actor == whom) && (!missed || sprint.Missed(t, e)) {
			out = append(out, timerViewOf(t, e, now))
		}
	}
	return out, nil
}

// missedItems is the holder's missed timers as the coordinator view's first
// items: a session returning after a gap reads them before anything else.
func (a *app) missedItems(ctx context.Context, st *store.Store, holder string) []viewItem {
	views, err := a.timerViews(ctx, st, holder, true)
	if err != nil || holder == "" {
		return nil // a store that keeps no timers: none to show
	}
	var out []viewItem
	for _, v := range views {
		out = append(out, viewItem{K: "t:" + v.ID, T: itemTimer, W: "timer " + v.State, S: v.missedLine(), Next: "nova-sprint remind --ack " + v.ID})
	}
	return out
}

// remindWords is remind's part of the help.
func remindWords() string {
	return strings.TrimSpace(`
remind: a timer. remind --in 30m --note 'look at the lander' (or --at 7:30pm,
or --at an RFC3339 time) stores a timer for the caller, or for --for <actor>:
the seat's holder, or a friend. The tick fires it once, on its first tick at
or after the due time, whether the machine is RUNNING or STOPPED: to the seat's
holder as a judgment (type timer), to a friend as a note addressed to her;
it never fires early, and it is stored, so a restart of the server loses
none. Each timer ends one way, and its setter is told which: seen (remind
--ack <id>, or its judgment answered with ack), or expired, when its window
(--within, default 1h after the due time) closes with it unfired (no tick ran
in it, or its actor left the sprint) or fired and unseen; an expired timer
sends its setter a note with the reason. remind --list shows each timer's
state, when it fired and how late; --missed shows only the fired and unseen,
and the expired not acknowledged, which view coordinator lists first.
remind --cancel <id> ends a pending one. --dry-run on a set, --cancel or
--ack writes nothing.`) + "\n"
}
