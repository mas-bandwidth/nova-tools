package notice

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
)

// Codex is the Codex desktop shim. Into a running turn and waking an idle
// one are the same mechanism: one start-turn call on the desktop socket whose
// fixed text names the saved file (pkg/codexdesktop). The desktop reports no
// turn boundary, so consumption is the model's own act on the bus.
type Codex struct {
	Socket, Thread, Directory string
	// Deliver is codexdesktop.DeliverSaved; a test passes its fake harness.
	Deliver func(ctx context.Context, socket, thread, id, directory string) (string, error)
	// Discover, when set, is the friend's current chat (codexdesktop.CurrentChat
	// over the Codex home): the route follows the friend to a new chat. It is
	// nil when the chat was named with --codex-thread.
	Discover func() (string, error)
}

// Follow moves the route to the friend's current chat. It answers the chat it
// left and the one it moved to, or moved false when the friend is where the
// route already points.
func (c *Codex) Follow(context.Context) (from, to string, moved bool, err error) {
	if c.Discover == nil {
		return c.Thread, c.Thread, false, nil
	}
	current, err := c.Discover()
	if err != nil {
		return c.Thread, c.Thread, false, err
	}
	if current == "" || current == c.Thread {
		return c.Thread, c.Thread, false, nil
	}
	if !codexdesktop.ValidThread(current) {
		return c.Thread, c.Thread, false, errors.New("the current chat's id is invalid")
	}
	from, c.Thread = c.Thread, current
	return from, current, true, nil
}

// Name is the harness word.
func (c *Codex) Name() string { return "codex" }

// Dir is the private events directory.
func (c *Codex) Dir() string { return c.Directory }

// Push is one native call; it is never retried.
func (c *Codex) Push(ctx context.Context, n Notice) (string, error) {
	return c.Deliver(ctx, c.Socket, c.Thread, n.ID, c.Directory)
}

// Consumed is false: the desktop has no turn boundary the client can read.
func (c *Codex) Consumed(context.Context, Notice) (bool, error) { return false, nil }

// ValidateCodex checks a Codex route before any lease: the grammar, a private
// canonical directory and a live owner of the thread.
func ValidateCodex(socket, thread, directory string) error {
	if err := codexdesktop.ValidateRoute(socket, thread, directory); err != nil {
		return err
	}
	if err := PrivateDir(directory); err != nil {
		return err
	}
	return codexdesktop.Probe(context.Background(), socket, thread)
}

// lockName is the one native client a directory serves at a time.
const lockName = ".native-client.lock"

// Lock holds a native route's directory for this process alone.
func Lock(directory string) (func() error, error) {
	if err := PrivateDir(directory); err != nil {
		return nil, err
	}
	lock, err := filelock.TryLock(filepath.Join(directory, lockName), "nova-hook native delivery")
	if err != nil {
		return nil, err
	}
	return lock.Unlock, nil
}
