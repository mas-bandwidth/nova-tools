package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// A VERB IS ANSWERED WITHIN A SECOND (docs/SPEC-SPRINT.md section 14, The server, "A verb
// is answered within a second"; tla/ServerLine.tla). Measured 2026-10-04 from 12:52 PM ET
// (tick-stall-cause, from the server's log): the ticks ran in 0.1 to 2.1 s and began 11 to
// 40 s apart, land's one-read queue phase took 14.9 to 19.4 s and its report 8.9 to 25.6 s,
// and POST /verbs took 5 to 15 s or ran past the client's 8 s, from 2:13 PM failing every
// friend's beat: every holder of the server's one line of control (a.serial) waited behind
// every other. A batch held the line for its whole verb list; every friend's beat, every
// second, and every worker's queue, each pass, took the line though none moves a card; and a
// batch whose sender had given up at 8 s still waited, then ran, adding to the queue the
// next sends joined. So:
//
//   - the beats (fleet beat, friend beat) and the reads (queue, where, card, log, needs) run
//     on two lanes beside the line, each on its own view of the store (its own connection
//     and read twin), so no tick, landing step, lane or worker's write holds them, and they
//     hold none;
//   - every other verb takes the line for itself alone, not for its batch;
//   - a verb waits for its lane or the line at most ServeWait, on the server's own clock
//     (a.after); past it the verb is answered busy, and the rest of its batch with it, having
//     run nothing;
//   - a batch whose sender has gone (its request's context done) runs nothing more.

// ServeWait is how long a verb waits for its lane or the line of control before it is
// answered busy, run nothing and changed nothing: its sender sends it again.
const ServeWait = time.Second

// laneVerbs are the verbs that run on a lane and never take the line: the beats, each a
// write of one record outside every table, and the reads, which write nothing a tick reads
// in its plan (a reader's queue writes its beat).
var laneVerbs = map[string]string{
	"fleet beat": laneBeats, "friend beat": laneBeats,
	"queue": laneReads, "where": laneReads, "card": laneReads, "log": laneReads, "needs": laneReads,
}

const (
	laneBeats = "beats"
	laneReads = "reads"
)

// lane is one of the server's lanes: its view of the store, which only its verbs use, and
// the lock they take in turn.
type lane struct {
	name string
	mu   sync.Mutex
	a    *app
}

// openLanes opens the beats and reads lanes of the server on a.serveAddr. A twin's server
// opens none: the twin is one file this process saves after each verb, and the twin is for
// learning and tests, not for a fleet.
func (a *app) openLanes() {
	if a.twinOpen(a.serveAddr) {
		return
	}
	a.beats, a.reads = a.newLane(laneBeats), a.newLane(laneReads)
}

// newLane is a lane over a view of the store of its own: a test's backend is shared (its
// store keeps its own lock), and redisBackend dials a connection of the lane's own, so the
// lane's round trips are not counted in a tick's TIMES.
func (a *app) newLane(name string) *lane {
	l := newApp(a.getenv)
	l.backend = a.backend
	l.now, l.sleep, l.after, l.exit = a.now, a.sleep, a.after, a.exit
	l.meter, l.loc, l.home, l.transport, l.checkTwin = a.meter, a.loc, a.home, a.transport, a.checkTwin
	l.serveAddr = a.serveAddr // the lane is the server: it forwards nothing
	return &lane{name: name, a: l}
}

// laneOf is the lane a verb runs on, nil for the line.
func (a *app) laneOf(verb string) *lane {
	switch laneVerbs[verb] {
	case laneBeats:
		return a.beats
	case laneReads:
		return a.reads
	}
	return nil
}

// locker is a lock a verb waits for at most ServeWait: the line (a.serial) or a lane's.
type locker interface {
	Lock()
	Unlock()
	TryLock() bool
}

// errBusy and errGone are why a verb was not run: its lock was held past ServeWait, or its
// sender went while it waited.
var (
	errBusy = errors.New("busy")
	errGone = errors.New("gone")
)

// notRun is why a verb of a batch was not run: busy or gone, and what it waited for.
type notRun struct {
	err error
	on  string
}

func (n *notRun) Error() string { return n.err.Error() + ": " + n.on }
func (n *notRun) Unwrap() error { return n.err }

// lockWithin takes l, unless the caller goes (ctx) or wait fires first: then it takes
// nothing and says why. A free lock is taken at once, on no clock. The take it asked for
// stays in l's order and, when it comes, frees l at once, having run nothing.
func lockWithin(ctx context.Context, l locker, wait func() <-chan time.Time) error {
	if ctx.Err() != nil {
		return errGone
	}
	if l.TryLock() {
		return nil
	}
	var mu sync.Mutex
	gaveUp := false
	got := make(chan struct{})
	go func() {
		l.Lock()
		mu.Lock()
		defer mu.Unlock()
		if gaveUp {
			l.Unlock()
			return
		}
		close(got)
	}()
	var why error
	select {
	case <-got:
		return nil
	case <-ctx.Done():
		why = errGone
	case <-wait():
		why = errBusy
	}
	mu.Lock()
	defer mu.Unlock()
	select {
	case <-got: // taken as it gave up: freed, so a verb past its bound never runs
		l.Unlock()
	default:
		gaveUp = true
	}
	return why
}

// serveOne runs one verb of a batch on its lane, or on the line, waiting for it at most
// ServeWait; serving is what a.serving says while it runs (serveFrom).
func (a *app) serveOne(ctx context.Context, verb string, args []string, serving bool) (sprintwire.Result, error) {
	on, lock, what := a, locker(&a.serial), "the server's line of control (a tick, a landing's step, the decide or balance lane, or another worker's write)"
	if l := a.laneOf(verb); l != nil {
		on, lock, what = l.a, &l.mu, "the "+l.name+" lane"
	}
	if err := lockWithin(ctx, lock, func() <-chan time.Time { return a.after(ServeWait) }); err != nil {
		return sprintwire.Result{}, &notRun{err: err, on: what}
	}
	defer lock.Unlock()
	on.serving = serving
	defer func() { on.serving = false }()
	var stdout, stderr strings.Builder
	code := on.run(args, &stdout, &stderr)
	return sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

// unrun is the answer of a verb the server did not run: exit 2, why, nothing changed.
func unrun(argv []string, err error) sprintwire.Result {
	verb := ""
	if len(argv) > 0 {
		verb = argv[0]
	}
	var n *notRun
	var why string
	switch {
	case errors.As(err, &n) && errors.Is(err, errBusy):
		why = fmt.Sprintf("busy: %s was held past %s; nothing was run or changed; send it again", n.on, ServeWait)
	case errors.Is(err, errBusy):
		why = "busy: a verb before it in the batch was; nothing was run or changed; send it again"
	default:
		why = "not run: its sender has gone; nothing was run or changed"
	}
	return sprintwire.Result{Code: 2, Stderr: fmt.Sprintf("%s server: %s: %s\n", prog, oneline.Escape(verb), oneline.Escape(why))}
}
