package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// streams is the one listing (docs/SPEC-SPRINT.md section 11, streams):
// nova-sprint streams [--repo <owner/name>] [--release <name>] [--cards] [--json].
//
// This file does not register the verb. verbs.go's init assigns the verb
// table and runs after this file (streams.go sorts first), so an append here
// is replaced. verbClasses is not set: a class with no verb fails
// TestEveryVerbHasAClass. cmdStreams is the verb. drop and hold by repository
// are cmdDropByRepo and cmdHoldByRepo, the same way: the flag parsers of the
// dispatched drop and hold live in verbs.go and hold.go.
func init() {
	verbEffect["streams"] = "inspection: reads the work and merge tables once and lists every stream with the repositories and bases its cards name, writes nothing"
	verbEffect["drop by repo"] = "local write: takes off the table the open cards whose briefs name --repo, when --expect is that count; a mismatch writes nothing"
	verbEffect["hold by repo"] = "local write: holds each stream that has an open card whose brief names --repo, when --expect is that count of streams; a mismatch writes nothing"
}

const streamsWords = `streams lists every drawn stream from one read of the work and merge tables.
Each line is the repositories and bases the stream's cards name on REPO: and BASE:, the release stream set --release recorded, and the open and landed counts.
A stream whose cards name more than one repository or base keeps them all, and a FINDING line says so.
--repo keeps the streams that name that owner/name. --release keeps the streams tagged with that release. --cards adds each card: id, state, tier, the first sentence of THE TASK, and its needs.
`

// cmdStreams prints the listing. It writes nothing.
func (a *app) cmdStreams(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("streams")
	repo := fs.String("repo", "", "keep streams whose cards name this owner/name")
	release := fs.String("release", "", "keep streams whose control card records this release (stream set --release)")
	cards := fs.Bool("cards", false, "with each stream, every card: id, state, tier, title, needs")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "streams", argErr("takes no words", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "streams", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed("streams", err, stderr)
	}
	rows := sprint.StreamsList(s, sprint.StreamListFilter{Repo: *repo, Release: *release, Cards: *cards})
	open, landed := 0, 0
	for _, row := range rows {
		open += row.Open
		landed += row.Landed
	}
	if c.json {
		b, _ := json.Marshal(struct {
			Streams []sprint.StreamView `json:"streams"`
			Open    int                 `json:"open"`
			Landed  int                 `json:"landed"`
		}{rows, open, landed})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "STREAMS %s repos=%s bases=%s release=%s open=%d landed=%d\n",
			oneline.Field(row.Stream), oneline.Field(joinOrDash(row.Repos)), oneline.Field(joinOrDash(row.Bases)), oneline.Field(dashed(row.Release)), row.Open, row.Landed)
		if row.Finding != "" {
			fmt.Fprintf(stdout, "FINDING %s\n", oneline.Escape(row.Finding))
		}
		for _, card := range row.Cards {
			fmt.Fprintf(stdout, "CARD %s state=%s tier=%s title=%s needs=%s\n",
				oneline.Field(card.ID), oneline.Field(card.State), oneline.Field(card.Tier), oneline.Escape(card.Title), oneline.Field(joinOrDash(card.Needs)))
		}
	}
	fmt.Fprintf(stdout, "STREAMS OK streams=%d open=%d landed=%d\n", len(rows), open, landed)
	return 0
}

func joinOrDash(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return strings.Join(ss, ",")
}

// cmdDropByRepo is drop --repo (docs/SPEC-SPRINT.md section 11, streams).
// It refuses without --expect equal to the open cards that name the repository.
func (a *app) cmdDropByRepo(args []string, stdout, stderr io.Writer) int {
	return a.repoAct("drop", args, stdout, stderr, true)
}

// cmdHoldByRepo is hold --repo (docs/SPEC-SPRINT.md section 11, streams).
// It refuses without --expect equal to the streams that name the repository.
// A stream that also names another repository is held whole.
func (a *app) cmdHoldByRepo(args []string, stdout, stderr io.Writer) int {
	return a.repoAct("hold", args, stdout, stderr, false)
}

func (a *app) repoAct(kind string, args []string, stdout, stderr io.Writer, drop bool) int {
	fs, c := a.verbSetup(kind)
	var streams listFlag
	fs.Var(&streams, "stream", "only these streams (repeat the flag for more than one)")
	repo := fs.String("repo", "", "the owner/name the cards' REPO: lines name")
	expect := fs.Int("expect", 0, "the count read before this runs: open cards for drop, streams for hold")
	reason := fs.String("reason", "", "why")
	saw := false
	pos, err := parse(fs, args)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expect" {
			saw = true
		}
	})
	if err != nil || len(pos) > 0 {
		return refuse(stderr, kind, argErr("takes no words with --repo", err, pos...))
	}
	if *repo == "" {
		return refuse(stderr, kind, "--repo <owner/name> is required")
	}
	if !saw || *expect <= 0 {
		return refuse(stderr, kind, "--expect <n> is required: read the count, then run it with that count")
	}
	if *reason == "" {
		return refuse(stderr, kind, "--reason <text> is required")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, kind, err.Error())
	}
	act := sprint.RepoAct{Repo: *repo, Streams: append([]string(nil), streams...), Expect: *expect, Reason: *reason, Who: c.actor}
	step := store.Step{
		Verb: kind, Load: []string{sprint.Work, sprint.Merge, sprint.Fleet, sprint.Readers}, Mirrors: true, Named: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			if drop {
				return sprint.DropByRepo(s, act)
			}
			return sprint.HoldByRepo(s, act)
		},
	}
	return a.runStep(kind, *c, st, step, stdout, stderr)
}
