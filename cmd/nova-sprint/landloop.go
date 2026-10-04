package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The server lands what the readers passed (run --land): the last of the sprint's
// writers outside the server was a coordinator's `land` run beside it, racing the
// tick's accepts for the merge queue: cards queue up and landings are refused
// round after round while both write. Here one landing runs
// at a time, in the server's own process: land's reads and its report take the server's
// line of control (cmdLand, a.serial), its git runs outside it, and nothing else writes
// the merge queue between them but the server itself.

// LandEvery is how often the loop looks for cards queued to merge.
const LandEvery = 2 * time.Second

// landLoop runs landRound every LandEvery until ctx ends.
func (a *app) landLoop(ctx context.Context, addr string, stdout io.Writer) {
	fmt.Fprintf(stdout, "LANDING every %s: land runs here for every stream with cards queued to merge, one landing at a time\n", LandEvery)
	waiting := ""
	for ctx.Err() == nil {
		// one lander at a time (landlock.go): the loop holds the land root's lock for as
		// long as it runs, and waits, landing nothing, while another lander holds it
		if held, why := a.holdLanderLock(); !held {
			if why != waiting {
				fmt.Fprintf(stdout, "%s LAND WAITING %s; nothing is landed until it lets go\n", oneline.Field(a.now().Format("15:04:05")), oneline.Escape(why))
				waiting = why
			}
			a.sleep(LandEvery)
			continue
		}
		waiting = ""
		a.landRound(ctx, addr, a.landArgs, stdout)
		a.sleep(LandEvery)
	}
}

// landRound runs one land over every stream with cards queued, as the sprint's
// coordinator, and prints what it did: a landing, a failed report, a conflict or a red
// check, each with what land said of it; a stream with nothing queued prints nothing.
// A round that could not read the merge queue says so and fails: an unreadable queue is
// not an empty one. A failed round prints only when it begins: the same failure again
// prints nothing until it changes or clears (a.landFailed), so a store that is down or
// a landing refused round after round is said once, not every LandEvery. more is further
// arguments of land (none from the loop: the clone, the base and the check are land's
// defaults). It returns land's exit code.
//
// After the landing, and never during one, the round runs the cleanup (landprune.go)
// when it is due: a round with nothing queued to merge, or PruneEvery branches waiting,
// and no failed cleanup waiting out PruneRetry. Its PRUNE lines are printed as land's.
// A stop of the loop flushes nothing: what is still queued then stays on origin.
// Then, still outside the landing, it runs the promote step when one is armed
// (promote.go). Nil arms nothing, so run --land does not open a pull request.
func (a *app) landRound(ctx context.Context, addr string, more []string, stdout io.Writer) int {
	code, idle := a.landOnce(ctx, addr, more, stdout)
	if a.prune.due(idle, a.now()) {
		at := oneline.Field(a.now().Format("15:04:05"))
		for _, r := range a.flushPrune(ctx, false) {
			fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(r.line(false)))
		}
	}
	a.promoteOnTick(ctx, stdout)
	return code
}

// landOnce is a round's landing: land's exit code (the worst of its streams'), and idle
// when the merge queue was read and held nothing. Each stream with cards queued is landed
// by a land of its own, and what it said is printed when it ends, not when the round
// does: a round over many streams on a cold build cache runs for many minutes, and on
// 2026-10-04 (4:32-4:46 PM ET) the log said nothing for fourteen minutes while four
// cards landed.
func (a *app) landOnce(ctx context.Context, addr string, more []string, stdout io.Writer) (int, bool) {
	a.serial.Lock()
	streams, coordinator, err := a.queuedToMerge(ctx, addr)
	a.serial.Unlock()
	idle := err == nil && len(streams) == 0
	switch {
	case err != nil:
		return a.landSaid(2, []string{"LAND FAILED the merge queue could not be read: " + oneline.Err(err) + "; nothing was landed, and the next round tries again; run: nova-sprint where"}, "", stdout), idle
	case len(streams) == 0 || coordinator == "":
		a.landFailed = map[string]string{}
		return 0, idle
	}
	worst := 0
	for _, stream := range streams {
		if ctx.Err() != nil {
			break
		}
		var out, errb bytes.Buffer
		a.landLazy, a.landCtx = true, ctx
		code := a.cmdLand(append([]string{"--redis", addr, "--actor", coordinator, "--stream", stream}, more...), &out, &errb)
		a.landLazy, a.landCtx = false, nil
		// what landed (stdout's LAND lines, but its summary), and everything land said
		// was wrong (stderr: a refused or failed batch, a refusal before any batch, the
		// remedy)
		var lines []string
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "LAND ") && !strings.HasPrefix(line, "LAND DONE") {
				lines = append(lines, line)
			}
		}
		for _, line := range strings.Split(errb.String(), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		worst = max(worst, a.landSaid(code, lines, stream, stdout))
	}
	return worst, idle
}

// landSaid prints what one land said, stamped with the time, and returns its code: a
// failed land prints only when it begins, the same failure again (for the same stream)
// printing nothing until it changes or clears (a.landFailed), so a store that is down or
// a landing refused round after round is said once, not every LandEvery.
func (a *app) landSaid(code int, lines []string, stream string, stdout io.Writer) int {
	if a.landFailed == nil {
		a.landFailed = map[string]string{}
	}
	said := ""
	if code != 0 {
		said = strings.Join(lines, "\n")
		if said == a.landFailed[stream] {
			return code // said when it began
		}
	}
	a.landFailed[stream] = said
	at := oneline.Field(a.now().Format("15:04:05"))
	for _, line := range lines {
		fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(line))
	}
	return code
}

// queuedToMerge is the streams land would land, in stream order: each with a card queued
// to merge before its first stuck one, and not stopped (cmdLand's own choice when it is
// named none); and the sprint's coordinator, whose the landing is ("" when the sprint has
// none); err when the sprint could not be read, which says nothing of what is queued.
func (a *app) queuedToMerge(ctx context.Context, addr string) (streams []string, coordinator string, err error) {
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return nil, "", err
	}
	if coordinator, err = st.B.Coordinator(ctx); err != nil {
		return nil, "", err
	}
	s, err := st.Load(ctx, []string{sprint.Merge}, nil)
	if err != nil {
		return nil, "", err
	}
	for _, row := range s.Merge.Rows() {
		if ctl := s.StreamCtl(row); ctl != nil && ctl.F("state") != sprint.StreamStopped && len(landQueue(s, row)) > 0 {
			streams = append(streams, row)
		}
	}
	return streams, coordinator, nil
}
