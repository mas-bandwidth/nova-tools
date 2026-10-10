package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The streams verb is a read (docs/SPEC-SPRINT.md section 11): it changes nothing and
// needs no actor. It is one call where the coordinator once made a where and a card call
// per card: every stream with the repositories and bases its cards record, its release,
// its open and landed counts and, with --cards, every card's id, state, tier, title and
// needs, read from one snapshot of the work and merge tables.
func init() { verbClasses["streams"] = classRead }

// streamsView and streamsRow are the internal/sprint view under this package's
// names, so the text and the JSON are the one value (docs/STANDARD.md).
type (
	streamsView = sprint.StreamsView
	streamsRow  = sprint.StreamRow
)

// cmdStreams prints every stream, its repositories and bases, its release, its open and
// landed counts, and with --cards every card of it. --repo keeps the streams recording a
// repository and --release the streams of a release (docs/SPEC-SPRINT.md section 11).
func (a *app) cmdStreams(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("streams")
	var repo listFlag
	fs.Var(&repo, "repo", "only the streams recording this repository (owner/name), comma separated or repeated; a stream whose cards name more than one repository is listed by each")
	release := fs.String("release", "", "only the streams of this release (stream set <s> --release <name>)")
	cards := fs.Bool("cards", false, "every card of each stream: its id, state, tier, the first sentence of its THE TASK and its needs")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "streams", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "streams", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	if err != nil {
		return a.readFailed("streams", err, stderr)
	}
	v := sprint.StreamsOf(s, sprint.StreamsReq{Repos: []string(repo), Release: *release, Cards: *cards})
	if c.json {
		b, _ := json.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, streamsText(v))
	return 0
}

// streamsText is the view in lines: the closing count, a STREAM line a stream, a CARD
// line a card with --cards, and a NOTE line for each stream whose cards name more than
// one repository (the finding the verb prints).
func streamsText(v streamsView) string {
	var b strings.Builder
	cards := 0
	for _, s := range v.Streams {
		cards += len(s.Cards)
	}
	fmt.Fprintf(&b, "STREAMS OK streams=%d cards=%d\n", len(v.Streams), cards)
	for _, s := range v.Streams {
		fmt.Fprintf(&b, "STREAM %s repos=%s bases=%s release=%s open=%d landed=%d ok=%d failed=%d ok%%=%s state=%s\n",
			s.Stream, streamsDash(strings.Join(s.Repos, ",")), streamsDash(strings.Join(s.Bases, ",")),
			streamsDash(s.Release), s.Open, s.Landed, s.OK, s.Failed, streamsPct(s.OKPct), streamsDash(s.State))
		for _, c := range s.Cards {
			fmt.Fprintf(&b, "CARD %s stream=%s state=%s tier=%s title=%s needs=%s\n",
				c.ID, s.Stream, c.State, streamsDash(c.Tier), streamsDash(c.Title), streamsDash(strings.Join(c.Needs, ",")))
		}
	}
	for _, m := range v.Mixed {
		var repos []string
		for _, s := range v.Streams {
			if s.Stream == m {
				repos = s.Repos
			}
		}
		fmt.Fprintf(&b, "NOTE stream %s names more than one repository (%s): its cards are not all for one repository; run: nova-sprint streams --repo <owner/name> --cards\n",
			m, strings.Join(repos, ", "))
	}
	return b.String()
}

// streamsDash is a field's value as the line prints it: "-" when it names none.
func streamsDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// streamsPct is a stream's ok% as the line prints it: one decimal, "-" with no
// attempt a worker ran to an end.
func streamsPct(p float64) string {
	return pctOf(p)
}

// pctOf is an ok% figure as the lines print it: one decimal and a percent sign.
func pctOf(p float64) string {
	return strconv.FormatFloat(p, 'f', 1, 64) + "%"
}

// repoStreams is the streams recording any of the repositories, read from the control
// cards in one snapshot of the merge table (the streams verb and drop and hold --repo).
func (a *app) repoStreams(ctx context.Context, st *store.Store, repos []string) ([]string, error) {
	s, err := st.Load(ctx, []string{sprint.Merge}, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, stream := range s.Merge.Rows() {
		ctl := s.StreamCtl(stream)
		if ctl == nil {
			continue
		}
		for _, have := range sprint.Split(ctl.F(sprint.FieldRepo)) {
			if repoWanted(repos, have) {
				out = append(out, stream)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// repoWanted says one of the wanted repositories is the recorded one, by owner/name.
func repoWanted(want []string, have string) bool {
	for _, w := range want {
		if sprint.RepoName(w) == sprint.RepoName(have) {
			return true
		}
	}
	return false
}
