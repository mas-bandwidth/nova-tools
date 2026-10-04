//go:build !darwin && !linux

package friendwatch

import (
	"context"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// Other targets retain direct-child cancellation; S36 group cleanup is Unix only.
type ownedCommand struct {
	child  *exec.Cmd
	waited chan struct{}
	cancel context.CancelFunc
}

func startOwnedCommand(ctx context.Context, o Options) (*ownedCommand, <-chan error, error) {
	ctx, cancel := context.WithCancel(ctx)
	child := subproc.Long(ctx, o.Argv[0], o.Argv[1:]...)
	child.Stdout, child.Stderr = o.Stdout, o.Stderr
	if !o.StdinLifetime {
		child.Stdin = o.Stdin
	}
	if err := child.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	done := make(chan error, 1)
	owned := &ownedCommand{child: child, waited: make(chan struct{}), cancel: cancel}
	go func() { done <- child.Wait(); close(owned.waited) }()
	return owned, done, nil
}

func (o *ownedCommand) cleanup() error { o.cancel(); <-o.waited; return nil }
func OwnedHelper([]string) (bool, int) { return false, 0 }
