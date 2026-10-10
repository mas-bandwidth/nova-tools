package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// THE SERVER'S LANES. The serial lock (app.serial) is the one line of control: a tick, a
// batch's writes and the land, decide and balance lanes' short store steps take it in
// turn. On 2026-10-04 everything the server answered took it too, and the line saturated:
// from 2:06 PM the run loop, which takes the same line for each tick, ticked every 10 to
// 50 s and from 2:14 PM every 36 to 151 s while each tick took 0.5 to 6 s of it; every
// friend's beat (two a second a friend: the daemon's and the beat loop's) and the two
// dashboards' `where --json --cards` (each once a second) waited behind it, the beats
// past their 10 s deadline, and each beat that timed out was still run when its turn
// came, so the line never drained. A verb is answered within a second whatever the tick
// or the lander is doing when it does not need the line:
//
//   - a friend's beat (`friend beat <friend>`, a worker's verb) writes one key outside
//     every table (the friend's beat record; store.FriendBeat: the roster read, the record
//     written): it runs on the beat lane, beside the line and beside every other beat,
//     never waiting for a tick, a batch or another beat;
//   - a read (where, card, log, check, routes, stats, needs, goal show, handover, and
//     inbox without --read) writes nothing: it runs on the read lane, one read at a time
//     on its own process state (its own read twin), beside the line, as a client reading
//     the store directly always has;
//   - every other verb (take, finish, read, queue, which records a reader's beat, fleet
//     beat, which can write the fleet table, and every coordinator's write) waits for the
//     line as before, and the line waits for its caller: a batch whose caller has gone is
//     not run (serialLock.LockCtx).
//
// The lanes run only on a store that is safe from more than one goroutine: Redis, and
// the in-memory store a test gives (store.Mem). A twin file (mem:<file>) is written
// whole after every verb, so on a twin file every verb takes the line, as before.
// docs/SPEC-SPRINT.md section 14, The server; tla/ServerLanes.tla.

// readLaneVerbs are the verbs the read lane runs: each reads the store and writes
// nothing (inbox only without --read, which moves the coordinator's cursor).
var readLaneVerbs = map[string]bool{
	"where": true, "card": true, "log": true, "check": true, "routes": true, "stats": true,
	"needs": true, "goal show": true, "handover": true, "inbox": true,
}

// onReadLane says the verb, as its flags read it, writes nothing and runs on the read lane.
func onReadLane(v verbArgs) bool {
	return readLaneVerbs[v.name] && (v.name != "inbox" || !v.on("read"))
}

// isFriendBeat says the worker's verb is a friend's beat (workerVerb has held it to
// `friend beat <friend>` and its report's flags).
func isFriendBeat(argv []string) bool {
	return len(argv) >= 3 && argv[0] == "friend" && argv[1] == "beat"
}

// serveLanes is the server's lanes beside the line, made once, on the line, at the first
// batch (app.lanesFor).
type serveLanes struct {
	// off says the store is a twin file: every verb takes the line.
	off bool
	// b is the lanes' backend: the server's store, on its own round-trip counter so a
	// tick's TIMES count the tick's trips alone.
	b   store.Backend
	now func() time.Time
	// line is the read lane's own line: one read at a time on read's state, given up by a
	// caller that has gone as the server's line is.
	line serialLock
	read *app
}

// lanesFor is the server's lanes, made on the first call that can take the line; nil
// when the store is a twin file or the lanes could not be made yet (every verb then takes
// the line, and the next batch tries again).
func (a *app) lanesFor(ctx context.Context) *serveLanes {
	a.lanesMu.Lock()
	defer a.lanesMu.Unlock()
	if a.lanes != nil {
		if a.lanes.off {
			return nil
		}
		return a.lanes
	}
	if a.serveAddr == "" {
		return nil
	}
	if err := a.serial.LockCtx(ctx); err != nil {
		return nil
	}
	b, err := a.backend(ctx, a.serveAddr, sprint.Names{})
	off := a.twinOpen(a.serveAddr)
	a.serial.Unlock()
	if err != nil {
		return nil
	}
	if off {
		a.lanes = &serveLanes{off: true}
		return nil
	}
	if rb, ok := b.(*store.Redis); ok {
		// the same connections (go-redis is safe from many goroutines), its own counter
		own := &store.Redis{C: rb.C, Names: rb.Names, Now: rb.Now}
		own.CountTrips()
		b = own
	}
	l := &serveLanes{b: b, now: a.now}
	r := newApp(a.getenv)
	r.now, r.sleep, r.after, r.meter, r.home, r.screen, r.transport, r.checkTwin = a.now, a.sleep, a.after, a.meter, a.home, a.screen, a.transport, a.checkTwin
	r.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return b, nil }
	r.serveAddr = a.serveAddr
	l.read = r
	a.lanes = l
	return l
}

// friendBeat is a friend's beat on the beat lane: no line taken, answered as friend beat
// answers on the store (a.friendBeat on args, the words after the verb with the server's
// store and actor, its report's flags read as the verb reads them; argv is as sent). A
// beat whose caller has gone is not run (tla/ServerLanes.tla, GoneNeverRuns; TLC found the
// first cut writing it anyway).
func (l *serveLanes) friendBeat(ctx context.Context, a *app, argv, args []string) sprintwire.Result {
	if ctx.Err() != nil {
		return goneResult(argv)
	}
	open := func(c common) (*store.Store, error) {
		return &store.Store{B: l.b, Names: sprint.Names{}, Actor: c.actor, Now: l.now}, nil
	}
	var stdout, stderr bytes.Buffer
	code := a.friendBeat(ctx, args, open, &stdout, &stderr)
	return sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// readVerbRun is a read on the read lane: it waits for the reads ahead of it alone, and
// not past its caller.
func (l *serveLanes) readVerbRun(ctx context.Context, args []string) sprintwire.Result {
	if err := l.line.LockCtx(ctx); err != nil {
		return goneResult(args)
	}
	defer l.line.Unlock()
	var stdout, stderr bytes.Buffer
	code := l.read.run(args, &stdout, &stderr)
	return sprintwire.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// goneResult is the answer of a verb not run because its caller went away while it
// waited: no one reads it, and nothing was changed.
func goneResult(argv []string) sprintwire.Result {
	verb := ""
	if len(argv) > 0 {
		verb = argv[0]
	}
	return sprintwire.Result{Code: 2, Stderr: fmt.Sprintf("%s server: %s: not run: its caller went away while it waited; nothing was changed\n", prog, oneline.Escape(verb))}
}

// ServeSayEvery is how often the server says what its batches cost (a SERVE line), when
// it answered any.
const ServeSayEvery = time.Minute

// serveTally is what the server's batches cost since its last SERVE line.
type serveTally struct {
	mu                    sync.Mutex
	since                 time.Time
	batches, beats, reads int
	serialVerbs, gone     int
	waitMax, holdMax      time.Duration
	holdWhat              string
}

// tally adds one batch: its verbs on the beat lane, on the read lane and on the line, the
// verbs not run because the caller went, how long it waited for the line and held it
// (what held it longest, named by its first verb); and says the SERVE line when one is
// due.
func (a *app) tally(beats, reads, serial, gone int, wait, hold time.Duration, first []string) {
	t := &a.served
	t.mu.Lock()
	defer t.mu.Unlock()
	now := a.now()
	if t.since.IsZero() {
		t.since = now
	}
	t.batches++
	t.beats += beats
	t.reads += reads
	t.serialVerbs += serial
	t.gone += gone
	t.waitMax = max(t.waitMax, wait)
	if hold > t.holdMax || t.holdWhat == "" && len(first) > 0 {
		t.holdMax = hold
		t.holdWhat = strings.Join(first[:min(len(first), 3)], "+")
	}
	if a.serveLog == nil || now.Sub(t.since) < ServeSayEvery {
		return
	}
	fmt.Fprintf(a.serveLog, "%s SERVE batches=%d beat-lane=%d read-lane=%d on-line=%d gone=%d wait-max=%s held-max=%s held-by=%s over=%s\n",
		now.Format("15:04:05"), t.batches, t.beats, t.reads, t.serialVerbs, t.gone,
		t.waitMax.Round(time.Millisecond), t.holdMax.Round(time.Millisecond), oneline.Field(orDashStr(t.holdWhat, "-")), now.Sub(t.since).Round(time.Second))
	t.since, t.batches, t.beats, t.reads, t.serialVerbs, t.gone, t.waitMax, t.holdMax, t.holdWhat = now, 0, 0, 0, 0, 0, 0, 0, ""
}
