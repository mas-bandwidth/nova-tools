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
// tick's accepts for the merge queue (the fleet pass of 2026-10-01 18:31 ET: 49 queued
// and 2 landed while landings were refused round after round). Here one landing runs
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
// not an empty one. more is further arguments of land (none from the loop: the clone,
// the base and the check are land's defaults). It returns land's exit code.
func (a *app) landRound(ctx context.Context, addr string, more []string, stdout io.Writer) int {
	a.serial.Lock()
	queued, coordinator, err := a.queuedToMerge(ctx, addr)
	a.serial.Unlock()
	at := oneline.Field(a.now().Format("15:04:05"))
	if err != nil {
		fmt.Fprintf(stdout, "%s LAND FAILED the merge queue could not be read: %s; nothing was landed, and the next round tries again; run: nova-sprint where\n", at, oneline.Err(err))
		return 2
	}
	if !queued || coordinator == "" {
		return 0
	}
	var out, errb bytes.Buffer
	code := a.cmdLand(append([]string{"--redis", addr, "--actor", coordinator}, more...), &out, &errb)
	// what landed (stdout's LAND lines, but its summary), and everything land said was
	// wrong (stderr: a refused or failed batch, a refusal before any batch, the remedy)
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
	for _, line := range lines {
		fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(line))
	}
	return code
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
