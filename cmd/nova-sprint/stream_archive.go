package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// stream archive and stream unarchive (store/stream_archive.go; the owner, 2026-10-05: "I
// would like you to remove all the already landed work streams"): a stream whose every
// card landed leaves the drawn work and merge tables and keeps its record, and comes
// back. Both are the coordinator's, and neither waits for a STOPPED machine: neither
// moves a card.
func init() {
	verbClasses["stream archive"] = classCoordinator
	verbClasses["stream unarchive"] = classCoordinator
}

// cmdStreamArchive takes the named streams off the drawn work and merge tables, each
// with every card it holds landed (sprint.StreamArchive), all or none.
func (a *app) cmdStreamArchive(args []string, stdout, stderr io.Writer) int {
	return a.streamArchive("stream archive", "STREAM-ARCHIVE", args, stdout, stderr)
}

// cmdStreamUnarchive shows the named archived streams on the tables again
// (sprint.StreamUnarchive), all or none.
func (a *app) cmdStreamUnarchive(args []string, stdout, stderr io.Writer) int {
	return a.streamArchive("stream unarchive", "STREAM-UNARCHIVE", args, stdout, stderr)
}

func (a *app) streamArchive(verbName, word string, args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup(verbName)
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, verbName, "wants at least one stream")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	ctx := context.Background()
	apply := st.ArchiveStreams
	if verbName == "stream unarchive" {
		apply = st.UnarchiveStreams
	}
	refused, err := apply(ctx, names)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run it again to finish\n", prog, verbName, oneline.Escape(err.Error()))
		return 1
	}
	if len(refused) > 0 {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help stream\n", prog, verbName, oneline.Escape(refusalText(refused)))
		return 1
	}
	sayOK(stdout, c.json, verbName, word+" OK streams="+strings.Join(names, ","), map[string]any{"streams": names})
	return 0
}

// refusalText is each refusal as "<key>: <why>", joined.
func refusalText(refused []sprint.Refusal) string {
	whys := make([]string, 0, len(refused))
	for _, r := range refused {
		whys = append(whys, r.Key+": "+r.Why)
	}
	return strings.Join(whys, "; ")
}
