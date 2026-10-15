package notice

import (
	"context"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
)

// Dsh is the DeepSeek Harness shim. The mounted receiver (pkg/dshdesktop,
// receiver.mjs) takes one queued record: it steers a running turn and
// follows up an idle session, which is its wake. The receiver reports no
// turn boundary to the client, so consumption is the model's own act on the
// bus. The Core queues one record at a time, so the receiver never holds two.
type Dsh struct {
	Route dshdesktop.Route
	// Deliver is dshdesktop.Deliver; a test passes its fake harness.
	Deliver func(r dshdesktop.Route, id, notice string) (string, error)
	// Follow, when set, repoints the receiver at the friend's newest live
	// session before each push (dshdesktop.Follower). Nil keeps a pinned route.
	Follow func() error
}

// Name is the harness word.
func (d *Dsh) Name() string { return "dsh" }

// Dir is the private events directory.
func (d *Dsh) Dir() string { return d.Route.EventsDir }

// Push queues one immutable record naming the saved file. A re-push of a
// notice whose record the receiver has not taken yet is that record: it is
// still queued, and the saved file it names holds the newest challenge.
func (d *Dsh) Push(_ context.Context, n Notice) (string, error) {
	if d.Follow != nil {
		_ = d.Follow() // ignored: a failed rediscovery keeps the last target, which still holds the queue
	}
	if path, err := dshdesktop.PendingPath(d.Route.EventsDir, n.ID); err == nil {
		if _, err := os.Lstat(path); err == nil {
			return "queued", nil
		}
	}
	kind := dshdesktop.BusNotice
	if n.Challenge != nil {
		kind = dshdesktop.ChallengeNotice
	}
	if _, err := d.Deliver(d.Route, n.ID, kind); err != nil {
		return "", err
	}
	return "queued", nil
}

// Consumed is false: the receiver reports no boundary to the client.
func (d *Dsh) Consumed(context.Context, Notice) (bool, error) { return false, nil }

// ValidateDsh checks a DSH route before any lease.
func ValidateDsh(r dshdesktop.Route) error {
	if err := dshdesktop.ValidateRoute(r); err != nil {
		return err
	}
	return PrivateDir(r.EventsDir)
}
