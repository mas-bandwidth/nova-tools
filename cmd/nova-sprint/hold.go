package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// holdWords is hold and unhold in nova-sprint help (docs/SPEC-SPRINT.md section 11).
func holdWords() string {
	return strings.TrimSpace(`
Hold and unhold: hold <name>... --reason <text> [--return] holds fleet members,
readers, friends and streams, one verb for the four, the coordinator's alone:
a name held takes no new cards; what is dealt and not begun is handed back now
(a member's ready cards dealt round the fleet, a reader's reads asked and not
begun asked of another, a stream's ready work cards withdrawn); what is begun
finishes, or with --return is handed back now too (a member's working cards
dealt round the fleet, a reader's reads begun asked of another, a stream's
working cards withdrawn to ready). A held friend keeps no card, with
--return or without: every card on her row goes back to ready, begun or not, and
a started one with a push carries its pushed head to the next taker. Its status reads held, the reason
beside it (where --json --cards: holds; handover), and the hold is a line
of the log.
unhold <name>... [--reason <text>] releases it: a member that beats is up at
once, a reader's and a friend's state is their beat's, a stream is dealt again.
fleet down <member> (hold --return), reader away <reader>... (hold --return),
friend down <friend> (hold), reader up and friend up (unhold) are the old words
for them, kept for one release; fleet up still adds a member and sets a width.
fleet hold <member>... and fleet unhold <member>... are hold and unhold of fleet
members alone, friend hold <friend>... and friend unhold <friend>... of friends
alone: the same step, the same writes and the same lines, a name of another kind
refused with the verb that holds it.`) + "\n"
}

// kindHoldWords is what fleet hold, fleet unhold, friend hold and friend unhold say on
// -h: hold or unhold of one kind (cmdHoldKind).
func kindHoldWords(name string) string {
	group, verb, _ := strings.Cut(name, " ")
	kind, other, otherVerb := "fleet member", "friend", "friend "+verb
	if group == "friend" {
		kind, other, otherVerb = "friend", "fleet member", "fleet "+verb
	}
	return name + " is " + verb + " of " + kind + "s alone: the same step, the same writes and the same " + strings.ToUpper(verb) + " lines as nova-sprint " + verb + " <name>, every name a " + kind + " or the whole call refused, nothing written (a " + other + " is named with nova-sprint " + otherVerb + "; a reader or a stream with nova-sprint " + verb + ").\n\n" + holdWords()
}

// cmdHold is hold (release false) and unhold (release true), docs/SPEC-SPRINT.md section
// 11: every name resolved to a fleet member, a reader, a friend or a stream, all or none,
// in one step (sprint.HoldNames), the reason required of a hold.
func (a *app) cmdHold(release bool, args []string, stdout, stderr io.Writer) int {
	return a.cmdHoldKind(map[bool]string{false: "hold", true: "unhold"}[release], "", release, args, stdout, stderr)
}

// cmdHoldKind is hold and unhold under the verb named (verb), its names held to one kind
// when kind is set: fleet hold and fleet unhold (sprint.HoldMember), friend hold and
// friend unhold (sprint.HoldFriend) are hold and unhold of that kind alone, aliases on
// the same request, step and store writes (the owner, 2026-10-10: "you can add a fleet
// hold if you want. it's a good idea. also a friend hold"; "even if just an alias"). A
// name of another kind is refused with the verb that holds it (sprint.HoldTargets). The
// OK and FAILED lines are hold's and unhold's own.
func (a *app) cmdHoldKind(verb, kind string, release bool, args []string, stdout, stderr io.Writer) int {
	name := map[bool]string{false: "hold", true: "unhold"}[release]
	fs, c := a.verbSetup(verb)
	reason := fs.String("reason", "", "why, in words: shown beside the held status and kept in the log; a hold wants one")
	ret := false
	if !release && kind != sprint.HoldFriend {
		fs.BoolVar(&ret, "return", false, "hand back the work begun now too: a member's working cards dealt round the fleet, a reader's reads begun asked of another, a stream's working cards withdrawn to ready (default: what is begun finishes, but a held friend keeps no card either way)")
	}
	dry := fs.Bool("dry-run", false, "check the names and the reason, print what would be held or released, and write nothing")
	var repo listFlag
	expect := new(int)
	if kind == "" {
		fs.Var(&repo, "repo", "also hold or release the streams recording this repository (owner/name), comma separated or repeated; needs --expect <n>, the number of streams it selects")
		expect = fs.Int("expect", 0, "with --repo: the number of streams it selects, as nova-sprint streams --repo <owner/name> printed it")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	var probs []string
	switch {
	case len(pos) == 0 && kind == sprint.HoldMember:
		probs = append(probs, "wants at least one fleet member")
	case len(pos) == 0 && kind == sprint.HoldFriend:
		probs = append(probs, "wants at least one friend")
	case len(pos) == 0 && len(repo) == 0:
		probs = append(probs, "wants at least one name: a fleet member, a reader, a friend or a stream (or --repo <owner/name> with --expect)")
	}
	for _, n := range pos {
		if !sprint.ValidID(n) {
			probs = append(probs, "a name wants letters, digits, _ and -: "+n)
		}
	}
	if !release && strings.TrimSpace(*reason) == "" {
		probs = append(probs, "--reason <text> is required: why it is held, shown beside its held status and kept in the log")
	}
	if len(repo) > 0 && *expect == 0 {
		probs = append(probs, "--repo wants --expect <n>, the number of streams it selects; run: nova-sprint streams --repo "+repo[0]+" to read it")
	}
	if len(probs) > 0 {
		return refuse(stderr, verb, strings.Join(probs, "; "))
	}
	if *dry && len(repo) == 0 {
		fmt.Fprintf(stdout, "%s DRY-RUN names=%s return=%t; nothing was written\n", strings.ToUpper(name), strings.Join(pos, ","), ret)
		return 0
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(repo) > 0 {
		streams, err := a.repoStreams(context.Background(), st, repo)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		if len(streams) == 0 {
			return refuse(stderr, name, "no stream records "+strings.Join(repo, ",")+" (read them with nova-sprint streams)")
		}
		if *expect != len(streams) {
			return refuse(stderr, name, fmt.Sprintf("--expect %d was printed for another set: the repository selects %d stream(s) (%s)", *expect, len(streams), strings.Join(streams, ",")))
		}
		pos = append(pos, streams...)
	}
	if *dry {
		fmt.Fprintf(stdout, "%s DRY-RUN names=%s return=%t; nothing was written\n", strings.ToUpper(name), strings.Join(pos, ","), ret)
		return 0
	}
	return a.runHold(name, "", *c, st, sprint.HoldReq{Names: pos, Release: release, Return: ret, Kind: kind, Reason: *reason, Who: c.actor}, nil, stdout, stderr)
}

// runHold runs one hold or unhold as verbName (an old verb's words when it aliases one:
// its step's verb stepVerb, "" for the request's own): the request completed from the
// store (HoldReqOf), the step, then the readers' and friends' hold records, a failure of
// which fails the verb (the step's line says what it did). ok, when set, is an old verb's
// own success line.
func (a *app) runHold(verbName, stepVerb string, c common, st *store.Store, r sprint.HoldReq, ok func() (string, map[string]any), stdout, stderr io.Writer) int {
	ctx := context.Background()
	r, err := st.HoldReqOf(ctx, r)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	// a held friend keeps no card: her started ones are read as friend down reads them, so
	// the hold takes them too and carries each push's head to the next taker
	if !r.Release {
		for _, n := range r.Names {
			if !slices.Contains(r.Friends, n) || (r.Kind != "" && r.Kind != sprint.HoldFriend) {
				continue
			}
			started, err := a.friendStarted(ctx, st, n)
			if err != nil {
				return a.readFailed(verbName, err, stderr)
			}
			if r.Started == nil {
				r.Started = map[string]string{}
			}
			maps.Copy(r.Started, started)
		}
	}
	step := store.HoldStep(r)
	if stepVerb != "" {
		step.Verb = stepVerb
	}
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
	if err == nil && len(res.Refused) == 0 {
		if werr := st.WriteHoldRecords(ctx, r); werr != nil {
			err = fmt.Errorf("the step committed and the hold records of its readers and friends were not written (%w); run the verb again", werr)
		}
	}
	if err == nil && len(res.Refused) == 0 && ok != nil {
		line, facts := ok()
		sayOK(stdout, c.json, verbName, line, facts)
		return 0
	}
	return a.report(ctx, verbName, c, st, res, err, stdout, stderr)
}

// cmdFleetDown is fleet down <member>, hold <member> --return in the old words (one
// release): the member's unfinished work, ready and working, dealt round the fleet now.
func (a *app) cmdFleetDown(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fleet down")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "fleet down", err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, "fleet down", "wants one member; run: nova-sprint hold <member> --reason <text> --return")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fleet down", err.Error())
	}
	return a.runHold("fleet down", "fleet hold", *c, st, sprint.HoldReq{Names: pos, Kind: sprint.HoldMember, Return: true, Who: c.actor}, nil, stdout, stderr)
}

// oldHoldWords is what fleet down, reader away and reader up say on -h: the hold verb
// they are the old words of, kept for one release (docs/SPEC-SPRINT.md section 11).
func oldHoldWords(name string) string {
	switch name {
	case "fleet down":
		return "fleet down <member> is hold <member> --return in the old words, kept for one release: run nova-sprint hold <member> --reason <text> --return; unhold <member> releases it. A member down by itself (no beat) is the machine's word, not a hold.\n"
	case "reader away":
		return "reader away <reader>... is hold <reader>... --return in the old words, kept for one release: run nova-sprint hold <reader> --reason <text> --return; unhold <reader> releases it.\n"
	}
	return name + " is unhold in the old words, kept for one release: run nova-sprint unhold <name>.\n"
}
