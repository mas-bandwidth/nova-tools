package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// whereRelease is where --release <name>: the streams of the release and the
// cards left in each, every card on its work row not landed, and their sum
// (sprint.ReleaseOf; docs/SPEC-SPRINT.md section 11, where). It reads what where
// reads, the work table's cells and each stream's control card, never a card's
// record. A release no stream is in is refused, naming the releases there are.
func (a *app) whereRelease(ctx context.Context, st *store.Store, r whereRun, stderr io.Writer) (string, int, bool) {
	failed := func(err error) (string, int, bool) {
		if ctx.Err() != nil {
			return "", 0, false // an interrupt cut the read short: the watch is over, not failed
		}
		return "", a.readFailed("where", err, stderr), false
	}
	st, err := st.Pinned(ctx)
	if err != nil {
		return failed(err)
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return failed(err)
	}
	clocks, err := st.StreamClocks(ctx)
	if err != nil {
		return failed(err)
	}
	rel, ok := sprint.ReleaseOf(shapes[0], clocks, r.release)
	if !ok {
		names := sprint.ReleaseNames(clocks)
		there := "no stream is in any release"
		if len(names) > 0 {
			there = "the releases: " + strings.Join(names, ", ")
		}
		return "", refuse(stderr, "where", fmt.Sprintf("no stream is in release %s (%s); run: nova-sprint stream set <stream> --release %s", r.release, there, r.release)), false
	}
	if r.c.json {
		b, _ := json.Marshal(rel)
		return string(b) + "\n", 0, true
	}
	return releaseFrame(rel), 0, true
}

// releaseFrame is a release as where --release prints it: its line, then a line
// a stream in the work table's order, the stream names padded to one width.
func releaseFrame(rel sprint.ReleaseCount) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RELEASE %s  left=%d  streams=%d\n", rel.Release, rel.Left, len(rel.Streams))
	w := 0
	for _, s := range rel.Streams {
		w = max(w, len(s.Stream))
	}
	for _, s := range rel.Streams {
		fmt.Fprintf(&b, "  %-*s  left=%d\n", w, s.Stream, s.Left)
	}
	return b.String()
}
