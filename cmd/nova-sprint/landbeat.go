package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE LAND LOOP'S BEAT (docs/SPEC-SPRINT.md section 7, the land loop's beat). On
// 2026-10-05 the server's lander landed its last card at 5:24 PM; by 6:05 PM nine streams
// held queued cards and no LAND line had been printed for 40 minutes while the lander's
// `go build ./...` ran on a machine at load 40, and nothing in the log said the lander was
// stuck or on what. So the loop's landing runs beside the loop, one at a time as before,
// and every cycle (LandEvery) writes a line: the round's LAND lines when a round ended in
// the cycle, else one LAND IDLE line with the cards queued, the step the landing is in, how
// long it has been in it and the process it waits on. A landing that has run past the land
// deadline with cards queued raises one judgment naming the step and the process.

// LandDeadline is how long the land loop's landing may run with cards queued before it is
// a judgment (run --land-deadline): a whole tree gate (landGoBudget) of one head.
const LandDeadline = 15 * time.Minute

// landBeat is what the land loop knows of its landing, read by every cycle: the cards
// queued when the round last read the queue, the step the round is in and since when, the
// process it waits on, when the round began (zero while none runs), and whether its stuck
// judgment is raised. The round's goroutine writes it; the cycle reads it.
type landBeat struct {
	mu       sync.Mutex
	queued   int
	step     string
	proc     string
	since    time.Time
	began    time.Time
	judged   bool
	deadline time.Duration
	// done is the running round's end (what it printed), nil while none runs; the cycle's
	// own, never the round's
	done chan string
}

// landBeatKey carries the beat on the landing's context, so the lander's runs say their
// step to it wherever they are.
type landBeatKey struct{}

// withLandBeat is ctx carrying b (ctx itself for nil).
func withLandBeat(ctx context.Context, b *landBeat) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, landBeatKey{}, b)
}

// landBeatOf is the beat ctx carries, nil for none (a land run by hand, or a nil ctx).
func landBeatOf(ctx context.Context) *landBeat {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(landBeatKey{}).(*landBeat)
	return b
}

// landStep says the landing on ctx is in step ("" keeps the step it is in), waiting on proc
// ("" for none): since moves only when the step changes, so a merge of each head in turn is
// one merge step.
func landStep(ctx context.Context, now time.Time, step, proc string) {
	b := landBeatOf(ctx)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if step != "" && step != b.step {
		b.step, b.since = step, now
	}
	b.proc = proc
}

// landProc says the landing on ctx waits on proc in step, and returns the call that says
// it no longer does.
func (l *lander) landProc(ctx context.Context, step, proc string) func() {
	landStep(ctx, l.clock(), step, proc)
	return func() { landStep(ctx, l.clock(), step, "") }
}

// landQueued records the cards queued to merge as the round read them.
func landQueued(ctx context.Context, n int) {
	if b := landBeatOf(ctx); b != nil {
		b.mu.Lock()
		b.queued = n
		b.mu.Unlock()
	}
}

// landSaid is a LAND line a round prints of a batch: OK, REFUSED or FAILED.
var landSaid = regexp.MustCompile(`(?m)^\S+ LAND (OK|REFUSED|FAILED) `)

// landCycle is one cycle of the land loop: a round is started when none runs, the cycle
// waits up to wait for it, and says one thing. A round that ended in the cycle prints what
// it said (its LAND lines; a round that landed nothing and failed as before says nothing
// new, landOnce); when that holds no LAND line, or the round is still running, the cycle
// says the beat (LAND IDLE). A running round past the deadline with cards queued raises
// its one judgment. more is land's further arguments (a test's clone and check). A cycle
// whose round ended early sleeps out the rest of wait (a.sleep), so the loop looks for
// cards every wait.
func (a *app) landCycle(ctx context.Context, addr string, more []string, b *landBeat, wait time.Duration, stdout io.Writer) {
	end := time.Now().Add(wait)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	b.mu.Lock()
	if b.done == nil {
		b.done = make(chan string, 1)
		b.began, b.judged = a.now(), false
		done := b.done
		b.mu.Unlock()
		go func() {
			var out bytes.Buffer
			a.landRound(withLandBeat(ctx, b), addr, more, &out)
			done <- out.String()
		}()
		b.mu.Lock()
	}
	done := b.done
	b.mu.Unlock()
	select {
	case said := <-done:
		b.mu.Lock()
		b.done, b.began = nil, time.Time{}
		b.mu.Unlock()
		landStep(withLandBeat(ctx, b), a.now(), "wait", "")
		fmt.Fprint(stdout, said)
		if !landSaid.MatchString(said) {
			fmt.Fprintln(stdout, a.landIdle(b, ""))
		}
		if rest := time.Until(end); rest > 0 {
			a.sleep(rest)
		}
	case <-timer.C:
		fmt.Fprintln(stdout, a.landIdle(b, a.landStuck(ctx, addr, b)))
	}
}

// landIdle is the beat's line: the cards queued, the step and how long it has run, the
// process waited on, and what the stuck judgment did this cycle ("" for nothing).
func (a *app) landIdle(b *landBeat, judgment string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	step, age := b.step, "0s"
	if step == "" {
		step = "wait"
	}
	if !b.since.IsZero() {
		age = a.now().Sub(b.since).Round(time.Second).String()
	}
	l := fmt.Sprintf("%s LAND IDLE queued=%d step=%s since=%s", oneline.Field(a.now().Format("15:04:05")), b.queued, oneline.Field(step), age)
	if b.proc != "" {
		l += " proc=" + oneline.Escape(b.proc)
	}
	if judgment != "" {
		l += " judgment=" + judgment
	}
	return l
}

// landStuck raises the running round's one judgment when it has run past the deadline
// with cards queued: "raised" when it did, "failed:<why>" when the store refused it (the
// next cycle tries again), "" when none is due.
func (a *app) landStuck(ctx context.Context, addr string, b *landBeat) string {
	b.mu.Lock()
	deadline := b.deadline
	if deadline <= 0 {
		deadline = LandDeadline
	}
	ran := a.now().Sub(b.began)
	due := !b.began.IsZero() && !b.judged && b.queued > 0 && ran >= deadline
	queued, step, proc, since := b.queued, b.step, b.proc, a.now().Sub(b.since).Round(time.Second)
	b.mu.Unlock()
	if !due {
		return ""
	}
	if proc == "" {
		proc = "no process (the lander's own work between them)"
	}
	what := fmt.Sprintf("the land loop's landing has run %s with %d cards queued and finished none (the land deadline is %s): it is in step %s since %s ago, waiting on %s; nothing lands until it ends; run: nova-sprint where, and look at that process on the server's machine",
		ran.Round(time.Second), queued, deadline, step, since, proc)
	if err := a.landJudgment(ctx, addr, what); err != nil {
		return "failed:" + oneline.Field(oneline.Err(err))
	}
	b.mu.Lock()
	b.judged = true
	b.mu.Unlock()
	return "raised"
}

// landJudgment writes the stuck landing's judgment as the sprint's coordinator: an
// operation stuck (sprint.NOpStuck), the land loop's.
func (a *app) landJudgment(ctx context.Context, addr, what string) error {
	a.serial.Lock()
	defer a.serial.Unlock()
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return err
	}
	who, err := st.B.Coordinator(ctx)
	if err != nil {
		return err
	}
	if who == "" {
		return errors.New("the sprint has no coordinator to raise it as")
	}
	if st, err = a.storeCtx(ctx, common{verb: "land", redis: addr, actor: who}); err != nil {
		return err
	}
	n := sprint.Note{Kind: sprint.Judgment, Type: sprint.NOpStuck, StreamLevel: true, Who: who, Marked: true,
		Decisions: append([]string(nil), sprint.Decisions[sprint.NOpStuck]...), What: what}
	res, err := st.Run(ctx, store.NoteStep("land", n))
	if err == nil && len(res.Refused) > 0 {
		err = errors.New(res.Refused[0].Why)
	}
	return err
}

// stepOfGit is the landing step a git command is: fetch, merge or push; "" leaves the
// step as it is (a rev-parse in the merge step is still the merge step).
func stepOfGit(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "fetch", "clone":
		return "fetch"
	case "merge":
		return "merge"
	case "push":
		return "push"
	}
	return ""
}

// procOf is a process as the beat names it: the command and where it runs.
func procOf(run []string, dir string) string {
	return strings.Join(run, " ") + " in " + dir
}
