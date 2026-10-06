package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// the coordinator's, as stream remove is (the class test, coordinator.go)
	verbClasses["stream archive"] = classCoordinator
	verbClasses["stream unarchive"] = classCoordinator
}

// cmdStreamArchive takes the named streams off the work and merge tables, or
// with archive false draws them again (store.ArchiveStreams,
// store.UnarchiveStreams): their rows are hidden, and every landed card stays
// placed, counted in the folds and the summary. Refused, exit 1 and nothing
// written, for a stream that is no row, for one holding a card not landed
// (sprint.StreamArchive), or, unarchived, for one not archived, all or none.
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
	sayOK(stdout, c.json, verb, word+" OK streams="+strings.Join(names, ","), map[string]any{"streams": names})
	return 0
}
