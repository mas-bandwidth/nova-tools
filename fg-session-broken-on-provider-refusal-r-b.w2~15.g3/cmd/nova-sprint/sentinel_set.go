package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["sentinel set"] = classCoordinator
}

// sentinel set <id> --needs a,b: a sentinel's needs replaced in one step, its id, stream,
// score and log kept (sprint.SentinelSet; docs/SPEC-SPRINT.md section 16). Before it, a
// sentinel whose cards were deferred was dropped and added again, which lost its place, its
// log and its id. The coordinator's alone.
func (a *app) cmdSentinelSet(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("sentinel set")
	needs := fs.String("needs", "", "the sentinel's needs, comma separated, in place of the ones it has: each a card on the table; a sentinel with nothing to wait on is released, not emptied")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "sentinel set", err.Error())
	}
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "needs" })
	switch {
	case len(pos) != 1 || !given:
		return refuse(stderr, "sentinel set", "wants <id> --needs <a,b>: the sentinel and the cards it waits for")
	case len(sprint.Split(*needs)) == 0:
		return refuse(stderr, "sentinel set", "--needs names no card: a sentinel with nothing to wait on is released, not emptied: nova-sprint release "+pos[0]+" --reason '<why>'")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "sentinel set", err.Error())
	}
	return a.runStep("sentinel set", *c, st, sentinelSetStep(sprint.SentinelSetReq{ID: pos[0], Needs: sprint.Split(*needs), Who: c.actor}), stdout, stderr)
}

// sentinelSetStep reads the work table and the records of the needs named, placed or not,
// so a refusal says what became of a need off the table.
func sentinelSetStep(r sprint.SentinelSetReq) store.Step {
	return store.Step{Named: true, Args: store.ArgsOf(r), Verb: "sentinel set", Load: []string{sprint.Work},
		Extras: func(*sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: append([]string{r.ID}, r.Needs...)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.SentinelSet(s, r) }}
}

// sentinelSetWords is the verb's -h paragraph.
var sentinelSetWords = strings.TrimSpace(`
sentinel set replaces a sentinel's needs in one step: its id, stream, score
and log stay, and one log line names the needs before and after. Each need is
a card on the table; every one that is not is named in one refusal, and
nothing changes. A sentinel the new needs leave waiting for nothing is marked
reached; release lands it. --needs "" is refused: a sentinel with nothing to
wait on is released, not emptied.
`)
