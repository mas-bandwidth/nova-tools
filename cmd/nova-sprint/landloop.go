package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
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
// Before the landing the round resumes, by itself, a stream the remote's refusal stopped
// (resumeRejected). Then, still outside the landing, it runs the promote step when one is armed
// (promote.go). Nil arms nothing, so run --land does not open a pull request.
func (a *app) landRound(ctx context.Context, addr string, more []string, stdout io.Writer) int {
	a.resumeRejected(ctx, addr, stdout)
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

// rejectedRetry is the loop's count of the resumes it gave one stream: n of them since the
// stream last landed a batch (its `moved` stamp), the stop it last saw (`since`), and when
// that stop is resumed (due; zero once the retries are spent).
type rejectedRetry struct {
	moved, since string
	n            int
	due          time.Time
}

// rejectedRetries is the loop's count for each app (the land loop's own, one a process; a
// test's app is its own), kept beside the loop rather than on the app, and by stream.
var (
	rejectedRetriesMu sync.Mutex
	rejectedRetries   = map[*app]map[string]*rejectedRetry{}
)

// resumeRejected resumes, as the sprint's coordinator, each stream stopped by the remote's
// refusal of its push (cause rejected) once its wait is over: sprint.RejectedRetries[n]
// after the stop (its `since`, so a loop that starts late does not wait again). The landing that follows pushes again; a push that goes through lands
// the batch and the stream is merging again, and the resume closed the stop's judgment,
// so none is left open. A refusal that does not clear stops the stream again, up to
// len(sprint.RejectedRetries) resumes; after them the stream stays stopped with its one
// judgment, the coordinator's (docs/SPEC-SPRINT.md section 4, the stopped stream). A
// stream that landed a batch since starts its count again. A conflict, a red branch and
// a cross-stream stop are never resumed here: they have a cause only a mind resolves.
func (a *app) resumeRejected(ctx context.Context, addr string, stdout io.Writer) {
	a.serial.Lock()
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	var coordinator string
	var s *sprint.Snapshot
	if err == nil {
		if coordinator, err = st.B.Coordinator(ctx); err == nil {
			s, err = st.Load(ctx, []string{sprint.Merge}, nil)
		}
	}
	a.serial.Unlock()
	if err != nil || coordinator == "" {
		return // unreadable: the landing says so, and the next round tries again
	}
	rejectedRetriesMu.Lock()
	defer rejectedRetriesMu.Unlock()
	if rejectedRetries[a] == nil {
		rejectedRetries[a] = map[string]*rejectedRetry{}
	}
	seen := rejectedRetries[a]
	now := a.now()
	for _, stream := range s.Merge.Rows() {
		ctl := s.StreamCtl(stream)
		if ctl == nil || ctl.F("state") != sprint.StreamStopped || ctl.F("cause") != "rejected" {
			continue
		}
		rt := seen[stream]
		if rt == nil {
			rt = &rejectedRetry{}
			seen[stream] = rt
		}
		if rt.moved != ctl.F("moved") {
			*rt = rejectedRetry{moved: ctl.F("moved")}
		}
		if rt.since != ctl.F("since") {
			rt.since, rt.due = ctl.F("since"), time.Time{}
			if rt.n < len(sprint.RejectedRetries) {
				stopped, perr := time.Parse(time.RFC3339, rt.since)
				if perr != nil {
					stopped = now
				}
				rt.due = stopped.Add(sprint.RejectedRetries[rt.n])
			}
		}
		if rt.due.IsZero() || now.Before(rt.due) {
			continue
		}
		var out, errb bytes.Buffer
		did := fmt.Sprintf("the push was refused and is tried again by the land loop (retry %d of %d)", rt.n+1, len(sprint.RejectedRetries))
		code := a.cmdResume([]string{"--redis", addr, "--actor", coordinator, "--stream", stream, "--did", did}, &out, &errb)
		rt.n++
		rt.due = time.Time{}
		at := oneline.Field(now.Format("15:04:05"))
		if code != 0 {
			fmt.Fprintf(stdout, "%s RESUME FAILED stream=%s: %s\n", at, stream, oneline.Escape(strings.TrimSpace(errb.String())))
			continue
		}
		fmt.Fprintf(stdout, "%s RESUMED stream %s: %s\n", at, stream, oneline.Escape(did))
	}
}
