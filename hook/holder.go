package hook

import (
	"context"
	"errors"
)

// Holder holds some pushes of one identity for a long-running process (a
// friend daemon, a member loop): it hooks in at start, renews each push it
// holds every RenewEvery, proves or unproves each as its owner's own round
// trip says, and answers the gate, so the process acts only while the record
// says proven (docs/SPEC-HOOK.md, holders; tla/Hook.tla, Renew and ProveDaemon).
type Holder struct {
	Store  Store
	Name   string
	Role   Role
	Pushes []Push // the pushes this process holds: some or all of the role's
	By     string // who holds them, as the lease shows it
	epoch  int64
}

// Epoch is the record's epoch the holder writes under, 0 before Start.
func (h *Holder) Epoch() int64 { return h.epoch }

// Start hooks the identity in as the holder's role (joining a record already
// hooked as it) and renews every push it holds once: the process's start is
// its hook. An unhooked record is hooked again: starting the holder is hooking in.
func (h *Holder) Start(ctx context.Context) error {
	e, _, err := h.Store.Arm(ctx, h.Name, h.Role, h.By, false)
	if err != nil {
		return err
	}
	h.epoch = e
	for _, p := range h.Pushes {
		if err := h.Store.Renew(ctx, h.Name, p, h.epoch, h.By); err != nil {
			return err
		}
	}
	return nil
}

// Beat renews each push the holder holds and proves the ones proven names,
// unproving the rest. When the record moved since (an unhook, or a hook as
// another role), the holder rejoins only a record hooked as its own role
// again; else it answers ErrNotHooked and holds nothing until one is, so an
// unhook stands until someone hooks in again.
func (h *Holder) Beat(ctx context.Context, proven map[Push]bool) error {
	err := h.beat(ctx, proven)
	if !errors.Is(err, ErrNotHooked) {
		return err
	}
	st, _, rerr := h.Store.Read(ctx, h.Name)
	if rerr != nil {
		return rerr
	}
	if st[0].State == StateNone || st[0].State == StateUnhooked || st[0].Role != h.Role || st[0].Epoch == h.epoch {
		return ErrNotHooked
	}
	h.epoch = st[0].Epoch
	return h.beat(ctx, proven)
}

func (h *Holder) beat(ctx context.Context, proven map[Push]bool) error {
	for _, p := range h.Pushes {
		if err := h.Store.Renew(ctx, h.Name, p, h.epoch, h.By); err != nil {
			return err
		}
	}
	for _, p := range h.Pushes {
		var err error
		if proven[p] {
			err = h.Store.Prove(ctx, h.Name, p, h.epoch, h.By)
		} else {
			err = h.Store.Unprove(ctx, h.Name, p, h.epoch)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Gate is the gate for the holder's identity: nil when every push of its role
// is live and proven, else the refusal or the store's error (fail closed).
func (h *Holder) Gate(ctx context.Context, need ...Push) error {
	return h.Store.Gate(ctx, Want{Name: h.Name, Need: need, Role: h.Role})
}
