package friend

import (
	"context"
	"errors"
	"strings"
)

// The push proof (docs/SPEC-FRIEND.md, The push proof): a friend nothing
// pushes into is deaf, and the bus waits on her unread. nova-friend install
// and run refuse, at the start, a harness whose adapter has no deliver
// command (a Stub), with its remedy; install also runs the first SESSION
// CHECK round trip (PushProof over Conformance) and refuses a session the
// adapter cannot drive (Deferred with a Remedy: dsh, a session under an agent
// preset). run never waits on the round trip: the daemon starts with its push
// unproven and its presence's first check (SessionCheck) is the proof, nothing
// delivered into the session until the session answers it (Daemon.Proof). The
// model is the presence model's Ask then Answer within the bound
// (tla/FriendPresence.tla).

// Undriven says nova-friend can push nothing into a session of harness
// through d: a Stub, with why and the remedy (AdapterRemedy); ok is false
// for an adapter with a deliver command.
func Undriven(d Deliverer, harness string) (why, remedy string, ok bool) {
	s, stub := d.(Stub)
	if !stub {
		return "", "", false
	}
	why = "no deliver command for " + harness
	if s.Reason != "" {
		why += " (" + s.Reason + ")"
	}
	return why + ": nothing the bus holds for the friend reaches her session", AdapterRemedy(harness), true
}

// AdapterRemedy is what a friend on a harness with no deliver command does:
// the adapter card that gives it one, or a harness that has one.
func AdapterRemedy(harness string) string {
	return "the adapter card: give pkg/friend a deliver command for " + harness + " (NewDeliverer), or run the friend under a harness that has one: " + strings.Join(Pushing(), ", ")
}

// Pushing is the harnesses whose adapter has a deliver command, in the
// order of Harnesses.
func Pushing() []string {
	var out []string
	for _, h := range Harnesses {
		if d, err := NewDeliverer(h, "", "", nil, nil); err == nil {
			if _, stub := d.(Stub); !stub {
				out = append(out, h)
			}
		}
	}
	return out
}

// PushProof runs the check c once as the push proof: an undriven harness is
// refused before anything is delivered; otherwise the round trip runs, and a
// failure whose delivery the adapter answered with a Deferred carrying a
// Remedy (a session it cannot drive) answers that remedy with undriven set.
// remedy is empty for any other failure: the caller names its own.
func PushProof(ctx context.Context, c Conformance) (res CheckResult, remedy string, undriven bool) {
	if why, remedy, ok := Undriven(c.Deliver, c.Harness); ok {
		return CheckResult{Harness: c.Harness, Stage: StageDeliver, Why: why}, remedy, true
	}
	seen := &deliverSeen{Deliverer: c.Deliver}
	c.Deliver = seen
	res = c.Run(ctx)
	var d Deferred
	if res.Stage == StageDeliver && errors.As(seen.err, &d) && d.Remedy != "" {
		return res, d.Remedy, true
	}
	return res, "", false
}

// deliverSeen keeps the adapter's own error from the check's one delivery.
type deliverSeen struct {
	Deliverer
	err error
}

func (s *deliverSeen) Deliver(ctx context.Context, text string) (int, error) {
	exit, err := s.Deliverer.Deliver(ctx, text)
	s.err = err
	return exit, err
}
