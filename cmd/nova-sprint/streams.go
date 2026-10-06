package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// streamsRun is the streams handler. The verb table is one literal, so this
// file cannot add a row to it. Naming the handler here keeps the listing in
// the binary (docs/SPEC-SPRINT.md section 11).
var streamsRun func(*app, []string, io.Writer, io.Writer) int

func init() {
	streamsRun = (*app).cmdStreams
	if streamsRun == nil {
		return
	}
	streamsKeep(nil)
}

// streamsKeep references the handler from init. A nil app returns before the
// command runs.
func streamsKeep(a *app) {
	if a == nil {
		return
	}
	a.cmdStreams(nil, nil, nil)
}

// cmdStreams prints each stream from one read of the work and merge tables
// (docs/SPEC-SPRINT.md section 11): repositories and bases its cards' briefs
// name, the release on its control card, open and landed counts, and with
// --cards every placed card. --repo and --release keep streams. The listing
// prints every match; --max does not cap it. It writes nothing.
func (a *app) cmdStreams(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("streams")
	repo := fs.String("repo", "", "keep streams whose cards name this repository (owner/name)")
	release := fs.String("release", "", "keep streams whose control card records this release")
	cards := fs.Bool("cards", false, "print every placed card: id, state, tier, title, needs")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "streams", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "streams", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed("streams", err, stderr)
	}
	v := sprint.StreamsList(s, sprint.StreamsQuery{Repo: *repo, Release: *release, Cards: *cards})
	if c.json {
		b, err := json.Marshal(v)
		if err != nil {
			return refuse(stderr, "streams", err.Error())
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, streamsText(v))
	return 0
}

func streamsText(v sprint.StreamsView) string {
	var b strings.Builder
	for _, st := range v.Streams {
		fmt.Fprintf(&b, "STREAM %s repos=%s bases=%s release=%s open=%d landed=%d\n",
			oneline.Field(st.Stream), oneline.Field(joinOrDash(st.Repos)), oneline.Field(joinOrDash(st.Bases)),
			oneline.Field(dashed(st.Release)), st.Open, st.Landed)
		for _, c := range st.Cards {
			needs := "-"
			if len(c.Needs) > 0 {
				needs = strings.Join(c.Needs, ",")
			}
			fmt.Fprintf(&b, "CARD %s state=%s tier=%s needs=%s title=%s\n",
				oneline.Field(c.ID), oneline.Field(c.State), oneline.Field(dashed(c.Tier)), oneline.Field(needs), oneline.Escape(c.Title))
		}
		prefix := "stream " + st.Stream + " "
		for _, f := range v.Findings {
			if strings.HasPrefix(f, prefix) {
				fmt.Fprintf(&b, "NOTE %s\n", oneline.Escape(f))
			}
		}
	}
	fmt.Fprintf(&b, "STREAMS OK streams=%d\n", len(v.Streams))
	return b.String()
}

func joinOrDash(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ",")
}
