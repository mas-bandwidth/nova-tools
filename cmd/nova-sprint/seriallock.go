package main

import (
	"context"
	"sync"
)

// serialLock is the server's one line of control (app.serial): a tick of the run loop,
// a batch's writes, and the short store steps of the land, decide and balance lanes each
// hold it, so none runs during another. It is a mutex whose waiters wait in the order
// they came (a channel's senders queue first in, first out) and whose wait can be given
// up: a batch waits for it under its caller's request (LockCtx), so a caller that has
// gone stops waiting and its verbs are never run (serve.go, serveCtx). On 2026-10-04 a
// sync.Mutex here kept every timed-out friend beat in the line, each run long after
// its caller had gone, and the line never drained (docs/SPEC-SPRINT.md section 14, The
// server; tla/ServerLanes.tla, AbandonedNeverRuns). The zero value is unlocked.
type serialLock struct {
	once sync.Once
	ch   chan struct{}
	// waiting, when set (a test), is called by a LockCtx that finds the line taken,
	// before it waits: how a test sees, with no clock, that a verb would wait.
	waiting func()
}

func (l *serialLock) line() chan struct{} {
	l.once.Do(func() { l.ch = make(chan struct{}, 1) })
	return l.ch
}

// Lock waits for the line for as long as it takes.
func (l *serialLock) Lock() { l.line() <- struct{}{} }

// LockCtx waits for the line until ctx is done; it holds the line only when it returns
// nil. A line free at the moment ctx ends may be taken and given straight back: a caller
// that has gone never holds it.
func (l *serialLock) LockCtx(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.TryLock() {
		return nil
	}
	if l.waiting != nil {
		l.waiting()
	}
	select {
	case l.line() <- struct{}{}:
		if err := ctx.Err(); err != nil {
			l.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryLock takes the line when it is free and says whether it did.
func (l *serialLock) TryLock() bool {
	select {
	case l.line() <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock gives the line back; giving back a line not held is a bug, and panics.
func (l *serialLock) Unlock() {
	select {
	case <-l.line():
	default:
		panic("nova-sprint: serial unlocked while not held")
	}
}
