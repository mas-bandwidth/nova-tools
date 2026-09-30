package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// goal: the people who work on the sprint and what each is to keep doing. The
// tick pushes each person's goal down its route every sprint.RemindEvery of
// running time while the machine is RUNNING (docs/SPEC-SPRINT.md,
// "Reminders").

// goalView is a person for a program: the goal and where it stands.
type goalView struct {
	Name  string    `json:"name"`
	Route string    `json:"route"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
	State string    `json:"state"` // "ok", "waiting" (not pushed yet) or "failed"
	Error string    `json:"error,omitempty"`
	Text  string    `json:"text,omitempty"`
}

func viewOf(g sprint.Goal, withText bool) goalView {
	v := goalView{Name: g.Name, Route: g.Route, Last: g.Last, Count: g.Count, State: "ok", Error: g.Fail}
	switch {
	case g.Fail != "":
		v.State = "failed"
	case g.Last.IsZero():
		v.State = "waiting"
	}
	if withText {
		v.Text = g.Text
	}
	return v
}

// line is the person on one line: the last push, the count and whether the
// route failed.
func (v goalView) line() string {
	last := "never"
	if !v.Last.IsZero() {
		last = v.Last.Format(time.RFC3339)
	}
	s := fmt.Sprintf("%s route=%s last=%s count=%d state=%s", v.Name, oneline.Escape(v.Route), last, v.Count, v.State)
	if v.Error != "" {
		s += " error=" + oneline.Escape(v.Error)
	}
	return s
}

// defaultGoalRoute is the file a person's reminder goes to when the route is
// not given.
func (a *app) defaultGoalRoute(name string) string {
	dir := a.getenv("NOVA_SPRINT_REMINDER_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "nova-sprint-reminders")
	}
	return "file:" + filepath.Join(dir, name+".txt")
}

// readGoalText reads the text file, refusing one over the bound without
// reading it all.
func readGoalText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, store.MaxCardTextBytes+1))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a *app) cmdGoalSet(args []string, stdout, stderr io.Writer) int {
	const name = "goal set"
	fs, c := a.verbSetup(name)
	file := fs.String("file", "", "the file that holds the goal text: what this person is to keep doing")
	to := fs.String("to", "", "how to reach the person: file:<absolute path> (default: a file this verb prints); bus is not built")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, name, "takes one name")
	}
	if *file == "" && *to == "" {
		return refuse(stderr, name, "give --file <path> (the text), --to <route>, or both")
	}
	var text *string
	if *file != "" {
		t, err := readGoalText(*file)
		if err != nil {
			return refuse(stderr, name, err.Error())
		}
		text = &t
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	route := *to
	if route == "" {
		if g, err := st.Goals(ctx); err == nil && g.Find(pos[0]) < 0 {
			route = a.defaultGoalRoute(pos[0])
		}
	}
	g, created, err := st.SetGoal(ctx, pos[0], text, route)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if c.json {
		// ignored: json.Marshal of goal struct cannot fail
		b, _ := json.Marshal(struct {
			goalView
			New bool `json:"new"`
		}{viewOf(g, false), created})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "%s OK name=%s new=%t route=%s bytes=%d; pushed on the first tick of a RUNNING machine, then every %s of running time\n",
		token(name), oneline.Escape(g.Name), created, oneline.Escape(g.Route), len(g.Text), sprint.RemindEvery)
	return 0
}

func (a *app) cmdGoalShow(args []string, stdout, stderr io.Writer) int {
	const name = "goal show"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 1 {
		return refuse(stderr, name, "takes one name or none")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	g, err := st.Goals(context.Background())
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	var views []goalView
	for _, p := range g.People {
		if len(pos) == 0 || p.Name == pos[0] {
			views = append(views, viewOf(p, true))
		}
	}
	if len(pos) == 1 && len(views) == 0 {
		return refuse(stderr, name, "no goal for "+oneline.Escape(pos[0])+"; run: nova-sprint goal show")
	}
	if c.json {
		if views == nil {
			views = []goalView{}
		}
		// ignored: json.Marshal of goal views cannot fail
		b, _ := json.Marshal(views)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(views) == 0 {
		fmt.Fprintln(stdout, "GOAL none; run: nova-sprint goal set <name> --file <path>")
	}
	for _, v := range views {
		fmt.Fprintf(stdout, "GOAL %s\n%s\n", v.line(), strings.TrimRight(v.Text, "\n"))
	}
	return 0
}

func (a *app) cmdGoalDrop(args []string, stdout, stderr io.Writer) int {
	const name = "goal drop"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, name, "takes one name")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	found, err := st.DropGoal(context.Background(), pos[0])
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if c.json {
		// ignored: json.Marshal of goal map cannot fail
		b, _ := json.Marshal(map[string]any{"name": pos[0], "dropped": found})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	what := "dropped"
	if !found {
		what = "unchanged: no goal for that name"
	}
	fmt.Fprintf(stdout, "%s OK name=%s %s\n", token(name), oneline.Escape(pos[0]), what)
	return 0
}

// goalWords is the goal verbs' part of the help.
func goalWords() string {
	return strings.TrimSpace(`
goal: the people who work on the sprint, each with a goal (what to keep
doing) and a route. While the machine is RUNNING the tick delivers each
person's goal down its route once when the machine starts and then every 5
minutes of running time (time STOPPED does not count); nothing is delivered
while it is STOPPED. The route file:<absolute path> gets the text, after one
header line REMINDER <n> to <name> at <time>, epoch <e>, in
place of what the file held, so a watcher of the file sees one current
reminder. The text is at most 8 KiB and is set from a file, and can be
different for each person. A route that fails is one judgment on the inbox
(goal set <name> --to <route> changes it, goal drop <name> removes the
person), closed when a delivery arrives. nova-sprint goal show says each
person's last push and whether it failed (where --json carries them). A clear
keeps the people and their goals.`) + "\n"
}

// goalsView sets the people in the view: each person's last push and whether
// the route failed. The frame does not show them; goal show does.
func (a *app) goalsView(ctx context.Context, st *store.Store, v *whereView) {
	g, err := st.Goals(ctx)
	if err != nil {
		return
	}
	for _, p := range g.People {
		v.Goals = append(v.Goals, viewOf(p, false))
	}
}
