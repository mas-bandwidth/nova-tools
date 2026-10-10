package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The install probation (docs/SPEC-SPRINT.md section 14,
// install-rollback-on-missed-ticks-b.w2; the model is tla/ServerInstall.tla).
// After a swap the new server is on probation for its first DefaultProbation
// ticks. A tick that misses its deadline, or the process exiting, puts the
// previous binary back, restarts it through the supervisor, logs the tick that
// failed and pushes the seat one note; the rolled-back binary is refused by
// server switch until a new binary is named. A swap that passes its probation
// is kept. The supervisor (launchd or systemd) sits behind installSupervisor,
// faked in tests; the clock is injected.

// DefaultProbation is how many ticks a freshly swapped server is watched
// before it is kept: server switch --probation.
const DefaultProbation = 5

// installSupervisor is the process supervisor behind an interface, faked in
// tests: it stops the server holding the rolled-back binary and starts the
// binary at target instead (which the controller has just restored to the
// previous binary).
type installSupervisor interface {
	Restart(ctx context.Context, target, previous string) error
}

// serviceSupervisor is the real supervisor seam: it ends this process with a
// non-zero code so the service manager (launchd KeepAlive, systemd
// Restart=always) starts the binary now on disk, which after the rollback is
// the previous binary.
type serviceSupervisor struct{ exit func(int) }

// exitRollback is the code run leaves with after a probation rollback: not 0,
// so the supervisor restarts the loop, which now runs the previous binary.
const exitRollback = 5

func (s serviceSupervisor) Restart(ctx context.Context, target, previous string) error {
	s.exit(exitRollback)
	return nil
}

// probationOutcome is what the last event the guard saw did.
type probationOutcome int

const (
	probationWatching   probationOutcome = iota // still inside the first n ticks
	probationKept                               // n good ticks: the binary is kept
	probationRolledBack                         // the previous binary is back
)

// probation watches a swapped server through its first n ticks. It is not safe
// for concurrent use: one run loop drives it, one event at a time.
type probation struct {
	target     string
	candidate  string
	previous   string
	n          int
	seen       int
	supervisor installSupervisor
	push       func(ctx context.Context, note string) error
	log        io.Writer
	now        func() time.Time
	rollback   func(ctx context.Context, target string) error
	done       bool
	rolledBack bool
}

// newProbation makes a guard over a swap: target is the binary on disk,
// candidate the binary just switched in, previous the binary kept beside it,
// and n the first ticks watched.
func newProbation(target, candidate, previous string, n int, sup installSupervisor, push func(context.Context, string) error, log io.Writer, now func() time.Time, rollback func(context.Context, string) error) *probation {
	if n <= 0 {
		n = DefaultProbation
	}
	if now == nil {
		now = time.Now
	}
	if rollback == nil {
		rollback = sprint.ServerRollback
	}
	return &probation{target: target, candidate: candidate, previous: previous, n: n, supervisor: sup, push: push, log: log, now: now, rollback: rollback}
}

// tickOK records a tick that met its deadline within the probation; the nth
// ends the probation and keeps the binary.
func (p *probation) tickOK(ctx context.Context) (probationOutcome, error) {
	if p.done {
		return probationKept, nil
	}
	p.seen++
	if p.seen >= p.n {
		p.done = true
		fmt.Fprintf(p.log, "%s INSTALL PROBATION OK target=%s binary=%s ticks=%d; the new binary is kept\n", p.now().Format("15:04:05"), p.target, p.candidate, p.seen)
		return probationKept, nil
	}
	return probationWatching, nil
}

// tickMissed records a tick that missed its deadline; within the probation it
// rolls the previous binary back.
func (p *probation) tickMissed(ctx context.Context) (probationOutcome, error) {
	if p.done || p.seen >= p.n {
		return probationKept, nil
	}
	return p.rollBack(ctx, "missed the tick deadline", p.seen+1)
}

// exited records the server exiting within the probation: the previous binary
// is rolled back, since a process that exited is not watched by its own loop.
func (p *probation) exited(ctx context.Context) (probationOutcome, error) {
	if p.done || p.seen >= p.n {
		return probationKept, nil
	}
	return p.rollBack(ctx, "the process exited", p.seen+1)
}

// rollBack restores the previous binary, marks the candidate refused, logs the
// tick that failed, pushes the seat one note and restarts through the
// supervisor. It runs once: a rollback never loops
// (tla/ServerInstall.tla, RolledBackNeverServed).
func (p *probation) rollBack(ctx context.Context, reason string, tick int) (probationOutcome, error) {
	if p.rolledBack {
		return probationRolledBack, nil
	}
	if err := p.rollback(ctx, p.target); err != nil {
		return probationRolledBack, err
	}
	p.rolledBack = true
	p.done = true
	// the previous binary starts with no probation owed, so a rollback never
	// loops (tla/ServerInstall.tla, RolledBackNeverServed)
	// ignored: no probation record is the state the previous binary must find
	_ = os.Remove(p.target + ".probation.json")
	if err := writeRollbackRecord(p.target, p.candidate, p.previous, reason, tick, p.now()); err != nil {
		fmt.Fprintf(p.log, "%s INSTALL ROLLBACK target=%s binary=%s tick=%d reason=%s; the rollback record was not written (%s), the previous binary is still restarted\n", p.now().Format("15:04:05"), p.target, p.candidate, tick, reason, oneline.Escape(err.Error()))
	} else {
		fmt.Fprintf(p.log, "%s INSTALL ROLLBACK target=%s binary=%s tick=%d reason=%s; the previous binary is restarted and switch will not serve %s again until a new binary is named\n", p.now().Format("15:04:05"), p.target, p.candidate, tick, reason, p.candidate)
	}
	if p.push != nil {
		if err := p.push(ctx, fmt.Sprintf("install rollback: %s was rolled back to %s after tick %d %s", p.candidate, p.previous, tick, reason)); err != nil {
			return probationRolledBack, err
		}
	}
	if p.supervisor != nil {
		if err := p.supervisor.Restart(ctx, p.target, p.previous); err != nil {
			return probationRolledBack, err
		}
	}
	return probationRolledBack, nil
}

// rollbackRecord is the durable mark of a probation rollback, at
// <target>.rollback.json: the binary that was rolled back and the tick that
// failed, so server switch refuses that binary until a new one is named.
type rollbackRecord struct {
	Target   string    `json:"target"`
	Binary   string    `json:"binary"`
	Previous string    `json:"previous"`
	Reason   string    `json:"reason"`
	Tick     int       `json:"tick"`
	At       time.Time `json:"at"`
}

// writeRollbackRecord keeps the rollback record atomically beside the target.
func writeRollbackRecord(target, binary, previous, reason string, tick int, now time.Time) error {
	b, err := json.Marshal(rollbackRecord{Target: target, Binary: binary, Previous: previous, Reason: reason, Tick: tick, At: now})
	if err != nil {
		return err
	}
	tmp := target + ".rollback.json.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target+".rollback.json")
}

// rolledBackRefusal is why server switch refuses candidate, "" when it does
// not: the binary recorded as rolled back is refused until a different binary
// is named (tla/ServerInstall.tla, RolledBackNeverServed).
func rolledBackRefusal(target, candidate string) string {
	b, err := os.ReadFile(target + ".rollback.json")
	if err != nil {
		return ""
	}
	var rec rollbackRecord
	if json.Unmarshal(b, &rec) != nil || rec.Binary == "" || rec.Binary != candidate {
		return "" // no record, or a different binary was named: the refusal is lifted
	}
	return fmt.Sprintf("the binary %s was rolled back after tick %d (%s) and is refused until a new binary is named; build a new candidate and switch that, or run: nova-sprint server switch --rollback --target %s", candidate, rec.Tick, rec.Reason, target)
}

// probationRecord is the durable probation of a swapped server, at
// <target>.probation.json: the binary switched in, the previous binary, the
// ticks watched, the good ticks seen and the starts of the run loop, so a run
// that begins inside the probation after one before it exited rolls back.
type probationRecord struct {
	Target   string    `json:"target"`
	Binary   string    `json:"binary"`
	Previous string    `json:"previous"`
	N        int       `json:"n"`
	Good     int       `json:"good"`
	Starts   int       `json:"starts"`
	Changed  time.Time `json:"changed"`
}

// writeProbationRecord keeps the probation record atomically beside the target.
func writeProbationRecord(target string, rec probationRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := target + ".probation.json.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target+".probation.json")
}

// readProbationRecord reads the probation record beside the target; false when
// there is none.
func readProbationRecord(target string) (probationRecord, bool) {
	b, err := os.ReadFile(target + ".probation.json")
	if err != nil {
		return probationRecord{}, false
	}
	var rec probationRecord
	if json.Unmarshal(b, &rec) != nil {
		return probationRecord{}, false
	}
	return rec, true
}

// serverTargetPath is the binary the run loop runs and server switch swaps:
// NOVA_SPRINT_SERVER_BIN, else this process's executable.
func (a *app) serverTargetPath() string {
	if p := a.getenv("NOVA_SPRINT_SERVER_BIN"); p != "" {
		return p
	}
	exe := a.executable
	if exe == nil {
		exe = os.Executable
	}
	p, err := exe()
	if err != nil {
		return ""
	}
	return p
}

// installSupervisor is the supervisor of this machine behind the interface: it
// exits the process so launchd or systemd starts the binary now on disk.
func (a *app) installSupervisor() installSupervisor {
	return serviceSupervisor{exit: a.exit}
}

// The run loop's probation lives beside the app rather than in the app struct,
// so the install stays in the switch files; one process runs one loop, so one
// guard an app is enough.
var (
	probationMu sync.Mutex
	probations  = map[*app]*probation{}
)

// probationOf is the running binary's probation while the loop is inside its
// first N ticks; nil when it is not on probation.
func probationOf(a *app) *probation {
	probationMu.Lock()
	defer probationMu.Unlock()
	return probations[a]
}

// setProbation records the running binary's probation; nil clears it.
func setProbation(a *app, p *probation) {
	probationMu.Lock()
	defer probationMu.Unlock()
	if p == nil {
		delete(probations, a)
		return
	}
	probations[a] = p
}
