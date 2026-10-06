package main

import (
	"context"
	"fmt"
	"io"
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
dealt round the fleet, a reader's reads begun asked of another, a friend's and
a stream's working cards withdrawn to ready). Its status reads held, the reason
beside it (where --json --cards: holds; handover), and the hold is a line
of the log.
unhold <name>... [--reason <text>] releases it: a member that beats is up at
once, a reader's and a friend's state is their beat's, a stream is dealt again.
A reader no process serves (no beat: a bud's reader loop or a friend's harness
runs queue --as <reader>) is refused, the whole call, nothing written.
fleet down <member> (hold --return), reader away <reader>... (hold --return),
friend down <friend> (hold), reader up and friend up (unhold) are the old words
for them, kept for one release; fleet up still adds a member and sets a width.`) + "\n"
}

// cmdHold is hold (release false) and unhold (release true), docs/SPEC-SPRINT.md section
// 11: every name resolved to a fleet member, a reader, a friend or a stream, all or none,
// in one step (sprint.HoldNames), the reason required of a hold.
func (a *app) cmdHold(release bool, args []string, stdout, stderr io.Writer) int {
	name := map[bool]string{false: "hold", true: "unhold"}[release]
	fs, c := a.verbSetup(name)
	reason := fs.String("reason", "", "why, in words: shown beside the held status and kept in the log; a hold wants one")
	ret := false
	if !release {
		fs.BoolVar(&ret, "return", false, "hand back the work begun now too: a member's working cards dealt round the fleet, a reader's reads begun asked of another, a friend's and a stream's working cards withdrawn to ready (default: what is begun finishes)")
	}
	dry := fs.Bool("dry-run", false, "check the names and the reason, print what would be held or released, and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	var probs []string
	if len(pos) == 0 {
		probs = append(probs, "wants at least one name: a fleet member, a reader, a friend or a stream")
	}
	for _, n := range pos {
		if !sprint.ValidID(n) {
			probs = append(probs, "a name wants letters, digits, _ and -: "+n)
		}
	}
	if !release && strings.TrimSpace(*reason) == "" {
		probs = append(probs, "--reason <text> is required: why it is held, shown beside its held status and kept in the log")
	}
	if len(probs) > 0 {
		return refuse(stderr, name, strings.Join(probs, "; "))
	}
	if *dry {
		fmt.Fprintf(stdout, "%s DRY-RUN names=%s return=%t; nothing was written\n", strings.ToUpper(name), strings.Join(pos, ","), ret)
		return 0
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runHold(name, "", *c, st, sprint.HoldReq{Names: pos, Release: release, Return: ret, Reason: *reason, Who: c.actor}, nil, stdout, stderr)
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
	if code, refused := a.refuseUnserved(ctx, verbName, st, r, stderr); refused {
		return code
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

// refuseUnserved refuses a release (unhold, reader up) whole when a name it resolves to
// a reader is one no process serves (no beat within sprint.ReaderBeatBound): the release
// adds no capacity, and the reader is away or down again on the next tick
// (docs/SPEC-SPRINT.md section 6). It names the beat that would serve each; refused says
// it wrote the refusal, with code the exit.
func (a *app) refuseUnserved(ctx context.Context, verbName string, st *store.Store, r sprint.HoldReq, stderr io.Writer) (code int, refused bool) {
	if !r.Release || (r.Kind != "" && r.Kind != sprint.HoldReader) {
		return 0, false
	}
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed(verbName, err, stderr), true
	}
	var readers []string
	for _, n := range r.Names {
		if slices.Contains(rows, n) {
			readers = append(readers, n)
		}
	}
	if len(readers) == 0 {
		return 0, false
	}
	served, err := st.ReadersServed(ctx, readers, a.now())
	if err != nil {
		return a.readFailed(verbName, err, stderr), true
	}
	var bad, cmds []string
	for _, n := range readers {
		if served != nil && !served[n] {
			bad = append(bad, n)
			cmds = append(cmds, "nova-sprint queue --as "+n)
		}
	}
	if len(bad) == 0 {
		return 0, false
	}
	fmt.Fprintf(stderr, "%s %s: no process serves %s (no beat within %s: reads are served by a bud's reader loop or a friend's harness, not by the row, so it would be away or down again on the next tick); nothing was changed; start the reader loop that beats it (%s), then run this again\n", prog, verbName, strings.Join(bad, ","), sprint.ReaderBeatBound, strings.Join(cmds, "; "))
	return 1, true
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
