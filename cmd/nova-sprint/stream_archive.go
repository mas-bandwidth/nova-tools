package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	verbClasses["stream archive"] = classCoordinator
	verbClasses["stream unarchive"] = classCoordinator
}

// cmdStreamArchive takes the named streams off the drawn work and merge tables and
// keeps their record (sprint.StreamArchive, store.ArchiveStreams): each stream's rows
// hidden as the table layer hides a row, every card, count and cost kept, on a RUNNING
// machine or a STOPPED one. Refused, exit 1 and nothing written, for a stream that is no
// row, or for one that holds a card not landed, named; all or none for the streams named.
func (a *app) cmdStreamArchive(args []string, stdout, stderr io.Writer) int {
	return a.streamArchive("stream archive", true, args, stdout, stderr)
}

// cmdStreamUnarchive draws the named streams' rows again (sprint.StreamUnarchive); the
// tick leaves a stream unarchived so drawn until another of its cards lands.
func (a *app) cmdStreamUnarchive(args []string, stdout, stderr io.Writer) int {
	return a.streamArchive("stream unarchive", false, args, stdout, stderr)
}

func (a *app) streamArchive(verb string, archive bool, args []string, stdout, stderr io.Writer) int {
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
	refused, err := st.ArchiveStreams(context.Background(), names, archive)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	if len(refused) > 0 {
		var whys []string
		for _, r := range refused {
			whys = append(whys, r.Key+": "+r.Why)
		}
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help stream\n", prog, verb, oneline.Escape(strings.Join(whys, "; ")))
		return 1
	}
	fmt.Fprintf(stdout, "%s OK streams=%s\n", strings.ToUpper(strings.ReplaceAll(verb, " ", "-")), strings.Join(names, ","))
	return 0
}
