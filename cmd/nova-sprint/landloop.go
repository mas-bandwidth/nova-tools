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
	for ctx.Err() == nil {
		a.landRound(ctx, addr, nil, stdout)
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

// landOnce is a round's landing: land's exit code, and idle when the merge queue was
// read and held nothing.
func (a *app) landOnce(ctx context.Context, addr string, more []string, stdout io.Writer) (int, bool) {
	a.serial.Lock()
	queued, coordinator, err := a.queuedToMerge(ctx, addr)
	a.serial.Unlock()
	var resumeErr error
	if err == nil && queued {
		resumeErr = a.resumeRejected(ctx, addr, coordinator)
	}
	idle := err == nil && !queued
	var lines []string
	code := 0
	switch {
	case err != nil:
		lines, code = []string{"LAND FAILED the merge queue could not be read: " + oneline.Err(err) + "; nothing was landed, and the next round tries again; run: nova-sprint where"}, 2
	case queued && coordinator != "":
		var out, errb bytes.Buffer
		a.landLazy, a.landCtx = true, ctx
		code = a.cmdLand(append([]string{"--redis", addr, "--actor", coordinator}, more...), &out, &errb)
		a.landLazy, a.landCtx = false, nil
		// what landed (stdout's LAND lines, but its summary), and everything land said
		// was wrong (stderr: a refused or failed batch, a refusal before any batch, the
		// remedy)
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
	}
	if resumeErr != nil {
		lines = append(lines, "NOTE the stream stopped by a rejected push could not be resumed: "+oneline.Err(resumeErr)+"; the next round tries again")
	}
	said := ""
	if code != 0 {
		said = strings.Join(lines, "\n")
		if said == a.landFailed {
			return code, idle // said when it began
		}
	}
	a.landFailed = said
	at := oneline.Field(a.now().Format("15:04:05"))
	for _, line := range lines {
		fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(line))
	}
	return code, idle
}

// queuedToMerge says a stream has a card queued to merge, and names the sprint's
// coordinator, whose the landing is ("" when the sprint has none); err when the sprint
// could not be read, which says nothing of what is queued.
func (a *app) queuedToMerge(ctx context.Context, addr string) (queued bool, coordinator string, err error) {
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return false, "", err
	}
	if coordinator, err = st.B.Coordinator(ctx); err != nil {
		return false, "", err
	}
	s, err := st.Load(ctx, []string{sprint.Merge}, nil)
	if err != nil {
		return false, "", err
	}
	for _, row := range s.Merge.Rows() {
		if s.Merge.Count(row, sprint.Queued) > 0 {
			return true, coordinator, nil
		}
	}
	return false, coordinator, nil
}

// resumeRejected resumes each stream the merge queue's rejection stopped, so the next
// landing pushes again: a refusal that was transient (a protected-branch hook, a base
// that moved twice) clears by itself, where it would otherwise wait for a person. A
// stream is resumed at most sprint.RejectedResumes times in a row (a.rejectResumes,
// cleared when the stream is no longer stopped with cards queued), after which it stays
// stopped with its one open judgment. A failed resume is returned, said by the round, and tried again by the next.
func (a *app) resumeRejected(ctx context.Context, addr, coordinator string) error {
	a.serial.Lock()
	defer a.serial.Unlock()
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return err
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return err
	}
	for _, name := range s.Streams() {
		ctl := s.StreamCtl(name)
		if ctl == nil || ctl.F("state") != sprint.StreamStopped || ctl.F("cause") != "rejected" {
			if ctl != nil && ctl.F("state") != sprint.StreamMerging {
				delete(a.rejectResumes, name)
			}
			continue
		}
		if a.rejectResumes[name] >= sprint.RejectedResumes || s.Merge.Count(name, sprint.Queued) == 0 {
			continue
		}
		if a.rejectResumes == nil {
			a.rejectResumes = map[string]int{}
		}
		a.rejectResumes[name]++
		var out, errb bytes.Buffer
		if a.cmdResume([]string{"--redis", addr, "--actor", coordinator, "--stream", name, "--did", "the land loop retries the push after the merge queue rejected it"}, &out, &errb) != 0 {
			return fmt.Errorf("resume --stream %s: %s", name, strings.TrimSpace(errb.String()))
		}
	}
	return nil
}
