package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// briefCheck is one brief of an add held to the card checks, under the id it is admitted as.
type briefCheck struct {
	id, brief string
}

// holdCardChecks holds each brief of an add to the card checks nova-card generate holds
// it to before it leaves (card.Checks): a card brief names a tier on line 1 and a
// TEST whose package PATHS names; no brief carries the name of the sprint's coordinator,
// its owner or a friend outside double-quoted words, nor names a card dropped off the
// table. One red brief refuses the whole call, exit 2, nothing written, every finding on
// its own LINT DRIFT line.
func (a *app) holdCardChecks(verbName string, st *store.Store, stderr io.Writer, briefs ...briefCheck) int {
	var tokens []string
	given := false
	for _, b := range briefs {
		given = given || b.brief != ""
		tokens = append(tokens, idTokenRE.FindAllString(b.brief, -1)...)
	}
	if !given {
		return 0
	}
	ctx := context.Background()
	names, err := personalNames(ctx, st)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	// a dropped card is kept off the table, so it is read by id: the briefs' words that
	// could be a card's id, and no more
	s, err := st.Load(ctx, []string{sprint.Work}, func(*sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Work: uniquify(tokens)}
	})
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	opts := card.Options{Names: personal(names), Dropped: droppedIDs(s)}
	var red []cardgen.LintFinding
	for _, b := range briefs {
		red = append(red, card.Checks(b.id, b.brief, opts)...)
	}
	if len(red) == 0 {
		return 0
	}
	for _, f := range red {
		fmt.Fprintln(stderr, oneline.Escape(f.String()))
	}
	return refuse(stderr, verbName, fmt.Sprintf("%d brief finding(s), the first %s: %s; nothing was written, every finding is a LINT DRIFT line above", len(red), red[0].Check, red[0].Excerpt))
}

// idTokenRE is a word of a brief that could be a card's id: letters and digits joined by
// at least one hyphen (s1-1, fix-x.w2, the ids --count and the twins make); a dropped card
// whose id has no hyphen is not looked for.
var idTokenRE = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._~]*(?:-[A-Za-z0-9._~]*[A-Za-z0-9])+`)

// droppedIDs is every card of the snapshot kept off the table with the outcome dropped.
func droppedIDs(s *sprint.Snapshot) []string {
	var out []string
	for _, c := range s.Work.Cards() {
		if !c.Placed() && c.F("outcome") == "dropped" {
			out = append(out, c.ID)
		}
	}
	return out
}

// roleWords are the words a brief names a role by; a sprint whose coordinator or owner is
// recorded under one (an actor left at its default) has no name there to keep out.
var roleWords = []string{"coordinator", "owner", "friend", "member", "reader", "child", "worker"}

// personal is names without the role words.
func personal(names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(roleWords, strings.ToLower(n)) {
			out = append(out, n)
		}
	}
	return out
}
