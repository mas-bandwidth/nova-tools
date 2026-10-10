package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdPriority sets or prints a card's priority (docs/SPEC-SPRINT.md section 1, "Priority";
// sprint.SetPriority): with a level (--blocker, --critical, --fix, --high, --normal, --low) it sets
// each primary named, or in one call every card now in --stream (its own level overwritten)
// and the stream's default for cards added later, each change on the card's timeline (the
// default's on the stream's) with the actor and the required reason; with
// none it prints each card's level and where it comes from. reader is a read card's, never set.
func (a *app) cmdPriority(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("priority")
	stream := fs.String("stream", "", "one call sets every card now in the stream, whatever its column and whatever level it had (its own included), and the stream's default for cards added later (a later card's own PRIORITY line wins over the default); a held stream is set and the output says it is held")
	reason := fs.String("reason", "", "why (required to set), recorded on each card's timeline, and the stream's, with the actor")
	levels := map[string]*bool{}
	for _, l := range sprint.PrioritySettable {
		levels[l] = fs.Bool(l, false, "set the level "+l)
	}
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "priority", err.Error())
	}
	var level []string
	for _, l := range sprint.PrioritySettable {
		if *levels[l] {
			level = append(level, l)
		}
	}
	if len(level) > 1 {
		return refuse(stderr, "priority", "one level at a time: --"+strings.Join(level, ", --"))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "priority", err.Error())
	}
	if len(level) == 0 {
		if *stream != "" || len(ids) == 0 || *reason != "" {
			return refuse(stderr, "priority", "wants <id>... to print, or <id>... or --stream <s> with one of --"+strings.Join(sprint.PrioritySettable, ", --")+" to set; run: nova-sprint help priority")
		}
		return a.printPriority(st, ids, c.json, stdout, stderr)
	}
	if (len(ids) == 0) == (*stream == "") {
		return refuse(stderr, "priority", "wants <id>... or --stream <s>, not both; run: nova-sprint help priority")
	}
	if strings.TrimSpace(*reason) == "" {
		return refuse(stderr, "priority", "wants --reason <text>: why the level changes, recorded on each card's timeline")
	}
	return a.runStep("priority", *c, st, store.PriorityStep(sprint.PriorityReq{IDs: ids, Stream: *stream, Level: level[0], Reason: *reason, Who: c.actor}), stdout, stderr)
}

// printPriority prints each primary's level and its source (sprint.CardPriority), one line a
// card: "<id> priority=<level> source=<set|computed|default>"; a read card's is reader.
func (a *app) printPriority(st *store.Store, ids []string, asJSON bool, stdout, stderr io.Writer) int {
	ctx := context.Background()
	type row struct {
		ID       string `json:"id"`
		Priority string `json:"priority"`
		Source   string `json:"source"`
	}
	var rows []row
	var lines []string
	for _, id := range ids {
		if prID, _, _, ok := sprint.ParseReadCard(id); ok {
			// a read inherits its primary's level: the higher of reader and the primary's
			l, src := sprint.PriorityReader, "read"
			if v, err := st.CardOf(ctx, prID); err == nil && v.Primary != nil {
				if l = sprint.ReadPriority(v.Primary); l != sprint.PriorityReader {
					src = "read:inherited-from-" + prID
				}
			}
			rows = append(rows, row{id, l, src})
			lines = append(lines, oneline.Escape(id)+" priority="+l+" source="+oneline.Field(src))
			continue
		}
		v, err := st.CardOf(ctx, id)
		if err != nil {
			return a.readFailed("priority", err, stderr)
		}
		if v.Primary == nil {
			fmt.Fprintf(stderr, "%s priority: no primary %s; run: nova-sprint where\n", prog, oneline.Escape(id))
			return 1
		}
		l, src := sprint.CardPriority(v.Primary)
		rows = append(rows, row{id, l, src})
		lines = append(lines, oneline.Escape(id)+" priority="+l+" source="+src)
	}
	sayOK(stdout, asJSON, "priority", strings.Join(lines, "\n"), map[string]any{"cards": rows})
	return 0
}
