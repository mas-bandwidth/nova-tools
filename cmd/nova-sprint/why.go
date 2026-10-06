package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() { verbClasses["why"] = classRead }

// why <card>: for a ready primary, what keeps it from dealing this tick, one line each
// (sprint.WhyNotDealt; docs/SPEC-SPRINT.md, the deal: priority): its group's level and the
// groups ahead, its place in the deal's order, its stream's hold, its WHO line, its tier,
// the friends and their room, the machines and theirs, and a bound. A read: it writes
// nothing.
func (a *app) cmdWhy(args []string, stdout, stderr io.Writer) int {
	const name = "why"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one card (nova-sprint why <id>)", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if s.Routes, _, err = st.Routes(ctx); err != nil {
		return a.readFailed(name, err, stderr)
	}
	seats, err := st.FriendSeats(ctx, s.Now)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	lines, why := sprint.WhyNotDealt(s, pos[0], seats)
	if why != "" {
		return refuse(stderr, name, why)
	}
	if c.json {
		viewJSON(stdout, struct {
			Card  string           `json:"card"`
			Lines []sprint.WhyLine `json:"lines"`
		}{pos[0], lines})
		return 0
	}
	for _, l := range lines {
		fmt.Fprintf(stdout, "WHY %s %s: %s\n", oneline.Escape(pos[0]), l.Key, oneline.Escape(l.Text))
	}
	fmt.Fprintf(stdout, "WHY OK card=%s lines=%d\n", oneline.Escape(pos[0]), len(lines))
	return 0
}

// whyWords is the verb's -h paragraph.
var whyWords = strings.TrimSpace(`
why <card> prints, for a ready primary, exactly what keeps it from dealing
this tick, one line each: group (its level and the groups of a higher level
with a ready card, dealt first), order (how many ready cards the deal takes
before it), hold (its stream's hold), who (its WHO line), tier (its tier and
route), friends (each friend, eligible or why not, and its room), machines
(the members up and their room) and bound (a redeal or staging bound). A card
not ready gets one line that says what it is.
`)
