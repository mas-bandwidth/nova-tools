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

func init() {
	// the coordinator's, as stream remove is (the class test, coordinator.go)
	verbClasses["stream archive"] = classCoordinator
	verbClasses["stream unarchive"] = classCoordinator
}

// cmdStreamArchive takes the named streams off the work and merge tables, or
// with archive false draws them again (store.ArchiveStreams,
// store.UnarchiveStreams): their rows are hidden, and every landed card stays
// placed, counted in the folds and in where --json's archived,
// archived_cards and archived_landed, and no more in the summary, which counts
// the streams on the table. Refused, exit 1 and nothing
// written, for a stream that is no row, for one holding a card not landed
// (sprint.StreamArchive), or, unarchived, for one not archived, all or none.
// Archived, every open judgment and held condition that names one of the
// streams is retired, a NOTE line each (sprint.RetireStreams).
func (a *app) cmdStreamArchive(archive bool, args []string, stdout, stderr io.Writer) int {
	verb, word := "stream archive", "STREAM-ARCHIVE"
	do := (*store.Store).ArchiveStreams
	if !archive {
		verb, word = "stream unarchive", "STREAM-UNARCHIVE"
		do = (*store.Store).UnarchiveStreams
	}
	fs, c := a.verbSetup(verb)
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, verb, "wants at least one stream")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	refused, err := do(st, context.Background(), names)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run it again to finish\n", prog, verb, oneline.Escape(err.Error()))
		return 1
	}
	if len(refused) > 0 {
		var whys []string
		for _, r := range refused {
			whys = append(whys, r.Key+": "+r.Why)
		}
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help stream\n", prog, verb, oneline.Escape(strings.Join(whys, "; ")))
		return 1
	}
	var said []string
	if archive {
		if said, err = retireStreams(context.Background(), st, verb, names, "was archived (stream archive), so nothing can act on this"); err != nil {
			fmt.Fprintf(stderr, "%s %s: the streams are archived, and retiring what names them failed: %s; run: nova-sprint inbox\n", prog, verb, oneline.Escape(err.Error()))
			return 1
		}
	}
	facts := map[string]any{"streams": names}
	if len(said) > 0 {
		facts["retired"] = said
	}
	sayOK(stdout, c.json, verb, word+" OK streams="+strings.Join(names, ","), facts)
	if !c.json {
		for _, l := range said {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
		}
	}
	return 0
}

// retireStreams retires every open judgment and held condition that names one
// of the streams, in one step of the verb's (sprint.RetireStreams): the lines
// it says, one for each.
func retireStreams(ctx context.Context, st *store.Store, verb string, names []string, why string) ([]string, error) {
	res, err := st.Run(ctx, store.Step{Verb: verb, Load: []string{sprint.Work, sprint.Merge}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.RetireStreams(s, names, why)
	}})
	return res.Said, err
}
