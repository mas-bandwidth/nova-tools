package main

import (
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["priority set"] = classCoordinator
}

// priority set <stream>... --level <n> | --release <name> --level <n>: a group's priority
// (sprint.PrioritySet; docs/SPEC-SPRINT.md, the deal: priority). The level is written on
// each stream's control card; the deal takes the groups in descending level, and inside a
// level the stream turns as before. The coordinator's alone.
func (a *app) cmdPrioritySet(args []string, stdout, stderr io.Writer) int {
	const name = "priority set"
	fs, c := a.verbSetup(name)
	level := fs.Int("level", -1, "the group's level: a whole number, the higher dealt first; 0 takes the level off")
	release := fs.String("release", "", "every stream of this release (stream set --release) in place of the streams named")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	var probs []string
	if *level < 0 {
		probs = append(probs, "--level <n> is required: a whole number from 0, the higher dealt first")
	}
	if len(pos) == 0 && *release == "" {
		probs = append(probs, "names no group: name streams, or --release <name> for every stream of a release")
	}
	if len(pos) > 0 && *release != "" {
		probs = append(probs, "names streams and --release: name one group")
	}
	if len(probs) > 0 {
		return refuse(stderr, name, strings.Join(probs, "; "))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, prioritySetStep(sprint.PriorityReq{Streams: pos, Release: *release, Level: *level, Who: c.actor}), stdout, stderr)
}

// prioritySetStep reads the work table (the streams) and the merge table (their control
// cards).
func prioritySetStep(r sprint.PriorityReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "priority set", Load: []string{sprint.Work, sprint.Merge},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.PrioritySet(s, r) }}
}

// priorityWords is the verb's -h paragraph.
var priorityWords = strings.TrimSpace(`
priority set gives a group of streams a level: the streams named, or with
--release every stream of that release. The deal takes the groups in
descending level and, inside a level, the streams in turn as before; within a
stream the order stays the stream's own (rank), so a priority never reorders
a stream. A group with nothing ready to deal yields to the next level. There
is no per-card priority. --level 0 takes the level off. where prints each
level and the group the deal takes now; why <card> says what keeps a ready
card from dealing.
`)
