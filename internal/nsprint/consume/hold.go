package consume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// GroupHoldToFix names the hold-to-fix rule's proc line (proc:hold-to-fix).
const GroupHoldToFix = RuleHoldToFix

// HoldRoute is the hold-to-fix rule (#2756 4.5; nova-tools #3092, #3799):
// one disposition.RouteOnce tick of the sprint's hold router (group
// hold-route on s:<S>:hold:events). A typed HOLD at head becomes one fix
// task on the policy's fix_to; a note or a REPAIR becomes its read. It routes
// only once s:<S>:policy has fix_to and release_reader: until then a pass
// moves nothing and prints one HOLDROUTE WAIT line. Its callers hold
// lease:route:<S> (the Router, PRRead.OnceN via PRReadSprints, Once).
type HoldRoute struct {
	Store       *store.Store
	Sprint      string
	Consumer    string
	Actor       string
	Out         io.Writer
	Block       time.Duration // wait after a pass that routed nothing; -1 none, 0 means 1 s
	ReclaimIdle int64         // ms, XAUTOCLAIM min-idle; 0 means disposition.DefaultReclaimIdle

	waiting bool
}

// Start is a no-op: RouteOnce creates the group and claims what a dead
// consumer left pending on every tick.
func (h *HoldRoute) Start(context.Context) error {
	if h == nil || h.Store == nil || h.Sprint == "" || h.Consumer == "" {
		return errors.New("hold-to-fix: store, sprint and consumer are required")
	}
	return nil
}

// Pass is one hold router tick; it returns the actions it handled and
// writes proc:hold-to-fix.
func (h *HoldRoute) Pass(ctx context.Context) (int, error) {
	if err := h.Start(ctx); err != nil {
		return 0, err
	}
	began := time.Now()
	actor := h.Actor
	if actor == "" {
		actor = GroupHoldToFix
	}
	idle := h.ReclaimIdle
	if idle <= 0 {
		idle = disposition.DefaultReclaimIdle
	}
	// The policy first, so a sprint that has none gets no group, no stream
	// key and no event left pending on this consumer.
	var lines []string
	pol, err := h.Store.Client().HMGet(ctx, disposition.PolicyKey(h.Sprint), "fix_to", "release_reader").Result()
	switch {
	case err != nil:
		err = fmt.Errorf("hold-to-fix: policy: %w", err)
	case len(pol) < 2 || pol[0] == nil || pol[1] == nil || pol[0] == "" || pol[1] == "":
		err = disposition.ErrNoPolicy
	default:
		lines, err = disposition.RouteOnce(ctx, h.Store.Client(), disposition.RouteConfig{
			Sprint: h.Sprint, Consumer: h.Consumer, ReclaimIdle: idle, Actor: actor,
		})
	}
	if errors.Is(err, disposition.ErrNoPolicy) {
		if !h.waiting {
			h.say("HOLDROUTE WAIT sprint=%s no fix_to/release_reader in %s", h.Sprint, disposition.PolicyKey(h.Sprint))
		}
		h.waiting, err = true, nil
	} else {
		h.waiting = false
	}
	for _, l := range lines {
		h.say("%s", l)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := h.Store.Client().FCall(ctx, FunctionOkFriendPass, nil, GroupHoldToFix,
		strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(len(lines)), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("hold-to-fix: proc: %w", perr)
	}
	if err == nil && len(lines) == 0 && h.Block >= 0 {
		select {
		case <-ctx.Done():
		case <-time.After(durationOr(h.Block, time.Second)):
		}
	}
	return len(lines), err
}

// Once runs one pass under lease:route:<S> (`consume hold-to-fix once`); a
// live lease under another instance is a *LeaseHeldError.
func (h *HoldRoute) Once(ctx context.Context) (int, error) {
	if h == nil || h.Store == nil || h.Sprint == "" {
		return 0, errors.New("hold-to-fix: store and sprint are required")
	}
	if h.Consumer == "" {
		inst, err := NewInstance()
		if err != nil {
			return 0, err
		}
		h.Consumer = inst
	}
	block := h.Block
	h.Block = -1
	defer func() { h.Block = block }()
	return underRouteLease(ctx, h.Store, h.Sprint, h.Consumer, h.Pass)
}

func (h *HoldRoute) say(format string, args ...any) {
	if h.Out != nil {
		fmt.Fprintf(h.Out, format+"\n", args...)
	}
}
