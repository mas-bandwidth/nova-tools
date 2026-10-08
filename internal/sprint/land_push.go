package sprint

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A push the remote rejected because the base moved is a retry, never a stop
// (docs/SPEC-SPRINT.md section 7). The lander fetches the base, rebuilds the
// batch on the new tip and pushes again, up to a bound of attempts in one
// pass. Past the bound the batch stays queued for the next pass. The lander
// stops on a fact the next pass cannot change: a tree-gate failure it was
// handed as one, or a push rejected for a reason other than the base moving.

// PropLandPushRebuilds is the work-table property that sets how many times one
// pass pushes a batch the remote rejected because the base moved. Empty uses
// PushRebuildsDefault.
const PropLandPushRebuilds = "land_push_rebuilds"

// PushRebuildsDefault is the bound when the work table names none: the first
// push plus the rebuilds after a fetch-first rejection.
const PushRebuildsDefault = 5

// PushRebuildWait is the short wait between a fetch-first rejection and the
// rebuild. The lander sleeps it on its own clock (a test's fake clock, the
// process clock in a real run).
const PushRebuildWait = time.Second

// PushRebuildBound is how many push attempts one pass may make. An empty, bad
// or non-positive property is the default.
func PushRebuildBound(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return PushRebuildsDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return PushRebuildsDefault
	}
	return n
}

// FetchFirst says the remote rejected the push because the base moved: git's
// "fetch first", or a non-fast-forward update. Any other rejection is a stop.
func FetchFirst(err string) bool {
	e := strings.ToLower(err)
	return strings.Contains(e, "fetch first") || strings.Contains(e, "non-fast-forward")
}

// BaseMovingNote is the batch's note when the bound is spent and the batch
// stays queued: n is how many rebuilds followed the first push.
func BaseMovingNote(n int) string {
	if n < 0 {
		n = 0
	}
	return fmt.Sprintf("base moving: %d rebuilds", n)
}

// PushChoice is what one failed push asks the lander to do.
type PushChoice struct {
	Kind   string // retry, leave, stop, refuse
	Cause  string // the stream cause when Kind is stop
	Reason string
	Row    string // the batch note "stopped: <reason>" when Kind is stop
}

const (
	kindRetry  = "retry"
	kindLeave  = "leave"
	kindStop   = "stop"
	kindRefuse = "refuse"
)

// ChoosePush classifies one failed push. A fetch-first rejection under the
// bound is a retry; at the bound the batch is left queued and the stream is
// not stopped. A rejection for any other reason stops and names that reason.
// Anything else (a push that never reached the remote) is a refusal, and the
// stream is not stopped.
func ChoosePush(attempt, bound int, pushErr string) PushChoice {
	if bound < 1 {
		bound = PushRebuildsDefault
	}
	text := strings.TrimSpace(pushErr)
	if FetchFirst(text) {
		if attempt < bound {
			return PushChoice{Kind: kindRetry}
		}
		return PushChoice{Kind: kindLeave, Reason: BaseMovingNote(attempt - 1)}
	}
	if remoteRejected(text) {
		return PushChoice{Kind: kindStop, Cause: "rejected", Reason: text, Row: StoppedRow(text)}
	}
	return PushChoice{Kind: kindRefuse, Reason: text}
}

// remoteRejected says the remote refused the update for a reason other than
// the base moving: a rejected update, or an auth or protection failure. The
// caller has already taken the fetch-first path.
func remoteRejected(err string) bool {
	e := strings.ToLower(err)
	if strings.Contains(e, "[rejected]") || strings.Contains(e, "[remote rejected]") {
		return true
	}
	for _, p := range []string{"authentication failed", "permission denied", "protected branch", "not authorized", "gh006"} {
		if strings.Contains(e, p) {
			return true
		}
	}
	return false
}

// pushStop is a rebuild the lander stops on, rather than a refusal the next
// pass can retry. kind is "gate".
type pushStop struct {
	kind, reason string
}

func (p *pushStop) Error() string { return p.reason }

// GateFailure is a rebuild whose tree gate failed. The lander stops the stream
// on it and names the reason; land does not resume that stop.
func GateFailure(reason string) error {
	return &pushStop{kind: "gate", reason: reason}
}

func stopKind(err error) (kind, reason string, ok bool) {
	var ps *pushStop
	if err == nil || !errors.As(err, &ps) {
		return "", "", false
	}
	return ps.kind, ps.Error(), true
}

// PushDrive is one batch's phase-2 push: the tip phase 1 built, whether a
// batch already landed in this pass moved the base, and the bound.
type PushDrive struct {
	Base  string
	Tip   string
	Bound int
	Wait  time.Duration
	Moved bool
}

// PushRepo is the lander's git and store, one call each, so the policy can be
// run with a fake and no socket. Rebuild fetches the base and merges the batch
// onto it again: the new tip, whether a head merged, or an error. A GateFailure
// stops the stream. Queue is why the queue no longer holds the batch, "" when
// it does. Push pushes tip to the base. Leave queues the batch with note and
// stops nothing. StopPush and StopGate each record one stop.
type PushRepo interface {
	Rebuild() (tip string, merged bool, err error)
	Push(tip string) error
	Queue() string
	Wait(d time.Duration)
	Before(attempt int)
	Landed(tip string)
	StopPush(reason string)
	StopGate(reason string)
	Leave(note string)
	Refuse(why string)
	EndedNoMerge()
}

// DrivePush pushes one batch. A fetch-first rejection waits, rebuilds and
// pushes again until the bound; the batch is then left queued with
// BaseMovingNote and the stream is not stopped. A rejection for any other
// reason stops at once and names it. A tree-gate failure from Rebuild stops
// and is not resumed by the next land.
func DrivePush(d PushDrive, r PushRepo) {
	if d.Bound < 1 {
		d.Bound = PushRebuildsDefault
	}
	if d.Wait <= 0 {
		d.Wait = PushRebuildWait
	}
	tip := d.Tip
	rebuilds := 0
	for attempt := 1; attempt <= d.Bound; attempt++ {
		if d.Moved || attempt > 1 {
			newTip, merged, err := r.Rebuild()
			if err != nil {
				if kind, reason, ok := stopKind(err); ok && kind == "gate" {
					r.StopGate(reason)
					return
				}
				r.Refuse(err.Error())
				return
			}
			if attempt > 1 {
				rebuilds++
			}
			if !merged {
				r.EndedNoMerge()
				return
			}
			tip = newTip
			d.Moved = false
		}
		if why := r.Queue(); why != "" {
			r.Refuse(why)
			return
		}
		r.Before(attempt)
		err := r.Push(tip)
		if err == nil {
			r.Landed(tip)
			return
		}
		choice := ChoosePush(attempt, d.Bound, err.Error())
		switch choice.Kind {
		case kindRetry:
			r.Wait(d.Wait)
		case kindLeave:
			r.Leave(BaseMovingNote(rebuilds))
			return
		case kindStop:
			r.StopPush(choice.Reason)
			return
		default:
			r.Refuse("the push to " + d.Base + " failed: " + pushFailLine(choice.Reason) + "; nothing was reported")
			return
		}
	}
}

// pushFailLine is one failed push's words on one line, hints dropped, as the
// lander already reports a push that never reached the remote.
func pushFailLine(err string) string {
	var lines []string
	for _, l := range strings.Split(err, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "hint:") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "no output"
	}
	return oneline.Cap(strings.Join(lines, " | "), 400)
}
