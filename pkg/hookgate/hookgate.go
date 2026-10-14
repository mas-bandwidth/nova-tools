// Package hookgate is the hook gate as the nova-tools tools meet it
// (docs/SPEC-HOOK.md, the gate in every tool; tla/Hook.tla, GateMeansHooked):
// the hook module's Gate over the bus store a verb already opened, and the
// gate's answer as the verb's one result. Every verb that sends, reads or
// takes work as an identity calls Gate before it acts and acts only on nil.
package hookgate

import (
	"context"
	"errors"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// Reason is the stable code of a gate's no, a result's reason (tool.Out.Reasons).
const Reason = "unhooked"

// ErrNoRecord is the refusal of a bus store that is no Redis: no hook record
// can be read from it, so nothing runs.
var ErrNoRecord = errors.New("this bus store keeps no hook record (it is no Redis store), so nothing runs: fail closed")

// Store is the hook record's store over the bus store st: the same Redis.
func Store(st bus.Store) (hook.Store, error) {
	if r, ok := st.(bus.Redis); ok && r.C != nil {
		return hook.Store{C: r.C}, nil
	}
	return hook.Store{}, ErrNoRecord
}

// Gate is the gate for w over the bus store st: nil when w.Name is hooked
// in as a role that carries the pushes w.Need names, each live and proven (every
// push of the role when w names none); else the hook's Refusal, or
// the store's error (fail closed: only nil lets a verb act).
func Gate(ctx context.Context, st bus.Store, w hook.Want) error {
	hs, err := Store(st)
	if err != nil {
		return err
	}
	return hs.Gate(ctx, w)
}

// Out is a gate's no as a verb's result: an identity not hooked in is FAILED
// at exit 1, reason unhooked, the hook command its remedy and every other line
// of the breadcrumb a NOTE; a record that could not be read is REFUSED at exit
// 2, the gate unable to decide (fail closed). nil for nil.
func Out(err error) *tool.Out {
	if err == nil {
		return nil
	}
	var r *hook.Refusal
	if !errors.As(err, &r) {
		return tool.Refuse("the hook gate could not decide, so nothing runs: " + err.Error())
	}
	o := tool.Fail(r.Why)
	o.Reasons = []string{Reason}
	o.Remedy = r.Hook
	for _, l := range r.Lines {
		o.Note(l)
	}
	return o
}
