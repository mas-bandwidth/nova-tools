package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// remind: a timer an actor sets for itself or for another on the sprint
// machine, so it is woken at a time it chose. One timer is one row of the
// store's timer record (id, actor, due time, note, who set it); the tick of a
// RUNNING machine raises a timer whose due time has come as one judgment of
// kind "timer" addressed to its actor, once, and closes it in the same step
// (docs/SPEC-SPRINT.md, "Timers").

// remindView is one timer for a program: what remind writes and --list reads.
type remindView struct {
	ID   string    `json:"id,omitempty"`
	For  string    `json:"for"`
	Due  time.Time `json:"due"`
	Note string    `json:"note"`
	By   string    `json:"by,omitempty"`
	Set  time.Time `json:"set,omitempty"`
	// DryRun says the verb printed what it would write and wrote nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

func timerView(t sprint.Timer) remindView {
	return remindView{ID: t.ID, For: t.For, Due: t.Due, Note: t.Note, By: t.By, Set: t.Set}
}

// line is the timer on one line.
func (v remindView) line() string {
	return fmt.Sprintf("id=%s for=%s due=%s note=%s by=%s", orDashID(v.ID), oneline.Escape(v.For),
		v.Due.UTC().Format(time.RFC3339), oneline.Escape(v.Note), oneline.Escape(v.By))
}

func orDashID(id string) string {
	if id == "" {
		return "-" // a dry run names no id: the store assigns one when it writes
	}
	return id
}

// timerAt is the time --at names: RFC3339, or a local date and time, a local
// date, or a local time of day today.
func timerAt(text string, now time.Time) (time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "15:04:05", "15:04"}
	for _, l := range layouts {
		if t, err := time.Parse(l, text); err == nil {
			if l == time.RFC3339 {
				return t, nil
			}
			y, m, d := t.Year(), t.Month(), t.Day()
			if l == "15:04:05" || l == "15:04" {
				y, m, d = now.Year(), now.Month(), now.Day()
			}
			due := time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			if (l == "15:04:05" || l == "15:04") && !due.After(now) {
				return time.Time{}, fmt.Errorf("the --at time %s today is not after now (%s); give a time in the future or use --in <duration>",
					oneline.Escape(text), now.Format(time.RFC3339))
			}
			return due, nil
		}
	}
	return time.Time{}, fmt.Errorf("the --at time %s is none of RFC3339 (2006-01-02T15:04:05Z07:00), a local date and time (2006-01-02 15:04:05), a local date (2006-01-02) or a local time of day today (15:04)", oneline.Escape(text))
}

func (a *app) cmdRemind(args []string, stdout, stderr io.Writer) int {
	const name = "remind"
	fs, c := a.verbSetup(name)
	in := fs.Duration("in", 0, "wake the actor this long from now (a duration: 90s, 30m, 4h)")
	at := fs.String("at", "", "wake the actor at this time: RFC3339, or a local date and time, date or time of day")
	note := fs.String("note", "", "the text the judgment carries: what the actor is woken to")
	forWho := fs.String("for", "", "the actor woken (default: the caller, --actor or NOVA_SPRINT_ACTOR)")
	list := fs.Bool("list", false, "print the open timers (id, for, due, note)")
	cancel := fs.String("cancel", "", "take this timer off the record: its id, from remind --list")
	dry := fs.Bool("dry-run", false, "say what would be written and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	forms := 0
	for _, on := range []bool{*list, *cancel != "", *in != 0 || *at != "" || *note != "" || *forWho != ""} {
		if on {
			forms++
		}
	}
	if len(pos) != 0 {
		return refuse(stderr, name, "takes no positional argument; run: nova-sprint remind --in 30m --note <text>")
	}
	if forms != 1 {
		return refuse(stderr, name, "wants exactly one form: --in <duration> --note <text> or --at <time> --note <text> (a timer), --list (the open timers), --cancel <id> (one off); it was given "+fmt.Sprint(forms))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	switch {
	case *list:
		return a.remindList(name, *c, st, stdout, stderr)
	case *cancel != "":
		return a.remindCancel(ctx, name, *c, st, *cancel, *dry, stdout, stderr)
	}
	return a.remindSet(ctx, name, *c, st, *in, *at, *note, *forWho, *dry, stdout, stderr)
}

// remindSet writes one timer, or with --dry-run says what it would write.
func (a *app) remindSet(ctx context.Context, name string, c common, st *store.Store, in time.Duration, at, note, forWho string, dry bool, stdout, stderr io.Writer) int {
	caller := st.Actor
	var problems []string
	if (in != 0) == (at != "") {
		problems = append(problems, "give one of --in <duration> (from now) or --at <time> (an absolute time), not both and not neither")
	}
	if err := sprint.ValidTimerNote(note); err != nil {
		problems = append(problems, "--note: "+err.Error())
	}
	who := forWho
	if who == "" {
		who = caller
	}
	if err := sprint.ValidGoalName(who); err != nil {
		problems = append(problems, "the actor to wake (--for, else --actor or NOVA_SPRINT_ACTOR): "+err.Error())
	}
	var due time.Time
	if len(problems) == 0 {
		due = a.now().Add(in)
		if at != "" {
			t, err := timerAt(at, a.now())
			if err != nil {
				problems = append(problems, err.Error())
			} else if !t.After(a.now()) {
				problems = append(problems, fmt.Sprintf("the timer's due time %s is not after now %s; give --in <duration> or an --at in the future",
					t.UTC().Format(time.RFC3339), a.now().UTC().Format(time.RFC3339)))
			} else {
				due = t
			}
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, name, strings.Join(problems, "; "))
	}
	t := sprint.Timer{For: who, Due: due, Note: note, By: caller, Set: a.now()}
	if dry {
		v := timerView(t)
		v.Set, v.DryRun = time.Time{}, true
		return a.remindPrint(name, c, stdout, v, "nothing was written; the store assigns the id when it writes")
	}
	wrote, err := st.AddTimer(ctx, t)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.remindPrint(name, c, stdout, timerView(wrote),
		"raised as one judgment of kind "+oneline.Quote(sprint.NTimer)+" to "+wrote.For+" by the next tick that finds its due time reached, once")
}

// remindCancel takes one timer off the record, or with --dry-run says which.
func (a *app) remindCancel(ctx context.Context, name string, c common, st *store.Store, id string, dry bool, stdout, stderr io.Writer) int {
	timers, err := st.Timers(ctx)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	i := timers.Find(id)
	if i < 0 {
		return refuse(stderr, name, fmt.Sprintf("no open timer is %s; run: nova-sprint remind --list", id))
	}
	t := timers.Open[i]
	if dry {
		v := timerView(t)
		v.DryRun = true
		return a.remindPrint(name, c, stdout, v, "nothing was written; the timer stays open")
	}
	gone, err := st.CancelTimer(ctx, id)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	v := timerView(gone)
	return a.remindPrint(name, c, stdout, v, "cancelled: it is off the record and will not be raised")
}

// remindList prints the open timers, bounded by --max with its MORE line.
func (a *app) remindList(name string, c common, st *store.Store, stdout, stderr io.Writer) int {
	t, err := st.Timers(context.Background())
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	views := make([]remindView, 0, len(t.Open))
	for _, x := range t.Open {
		views = append(views, timerView(x))
	}
	if c.json {
		b, _ := json.Marshal(struct {
			Items []remindView `json:"items"`
			More  *moreView    `json:"more,omitempty"`
		}{cut(views, c.max), moreOf(len(views), c.max)})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(views) == 0 {
		fmt.Fprintln(stdout, "REMIND none; run: nova-sprint remind --in 30m --note <text>")
		return 0
	}
	lines := make([]string, len(views))
	for i, v := range views {
		lines[i] = v.line()
	}
	listed(stdout, "REMINDER", lines, c.max, "remind --list")
	return 0
}

// remindPrint is the one value on one line, or as JSON.
func (a *app) remindPrint(name string, c common, stdout io.Writer, v remindView, next string) int {
	if c.json {
		b, _ := json.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	word := "OK"
	if v.DryRun {
		word = "DRY-RUN"
	}
	fmt.Fprintf(stdout, "%s %s %s; %s\n", token(name), word, v.line(), next)
	return 0
}

// moreView is the MORE line's counts under --json.
type moreView struct {
	Shown int `json:"shown"`
	Total int `json:"total"`
}

func moreOf(total, max int) *moreView {
	if max > 0 && total > max {
		return &moreView{Shown: max, Total: total}
	}
	return nil
}

func cut(views []remindView, max int) []remindView {
	if max > 0 && len(views) > max {
		return views[:max]
	}
	return views
}
