// Package friendwatch scopes sprint presence to a harness-owned blocking command.
// It is presence evidence, not native message delivery or business acceptance.
// Libraries considered: context, os/exec via subproc, and time.
package friendwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// Options names direct executable arguments; no shell interpolates message data.
// Parent is the actual invoking harness parent, never an arbitrary application PID.
// StdinLifetime explicitly transfers ownership of a pipe ReadCloser whose Close
// releases blocked reads. Run closes that pipe on return.
type Options struct {
	Sprint, Server, Friend string
	Argv                   []string
	Every, Timeout         time.Duration
	Parent                 int
	StdinLifetime          bool
	Stdin                  io.Reader
	Stdout, Stderr         io.Writer
}

// Run beats only while its owned child runs and its invoking parent remains.
// A failed beat cancels the wait, making store errors visible to the harness.
// STANDARD section 1: only this invocation's child is cancelled.
func Run(ctx context.Context, o Options) error {
	if o.Sprint == "" || o.Server == "" || o.Friend == "" || len(o.Argv) == 0 || o.Argv[0] == "" || o.Every <= 0 || o.Timeout <= 0 || o.Parent <= 1 {
		return errors.New("watch needs sprint executable, server, friend, direct child argv, positive durations and a live invoking parent")
	}
	if o.Parent != os.Getppid() {
		return errors.New("watch parent must be its actual invoking parent")
	}
	if o.StdinLifetime && o.Stdin == nil {
		return errors.New("stdin lifetime needs the invoking harness pipe")
	}
	var lifetime io.ReadCloser
	if o.StdinLifetime {
		var ok bool
		lifetime, ok = o.Stdin.(io.ReadCloser)
		if !ok {
			return errors.New("stdin lifetime transfers ownership of a closable harness pipe")
		}
		defer lifetime.Close() // ignored: closing the owned pipe releases its lifetime monitor; closure already ends presence.
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	child := subproc.Long(ctx, o.Argv[0], o.Argv[1:]...)
	child.Stdout, child.Stderr = o.Stdout, o.Stderr
	if !o.StdinLifetime {
		child.Stdin = o.Stdin
	}
	if err := child.Start(); err != nil {
		return fmt.Errorf("start owned wait: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	closed := make(chan struct{})
	if o.StdinLifetime {
		go func() {
			// ignored: EOF and read failure both cancel presence through the closed lifetime signal.
			_, _ = io.Copy(io.Discard, lifetime)
			close(closed)
		}()
	}
	stop := func(reason error) error { cancel(); <-done; return reason }
	beat := func() error {
		if os.Getppid() != o.Parent {
			return errors.New("invoking harness parent exited; presence stopped")
		}
		cmd, release := subproc.CommandFor(ctx, o.Timeout, o.Sprint, "friend", "beat", o.Friend)
		defer release()
		cmd.Env = beatEnvironment(os.Environ(), o.Server)
		var output bounded
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("presence beat failed: %w: %s", err, strings.TrimSpace(output.text))
		}
		return nil
	}
	// A completed wait returns immediately and never advertises continuing presence.
	select {
	case err := <-done:
		return err
	case <-closed:
		return stop(errors.New("invoking harness pipe closed; presence stopped"))
	default:
	}
	if err := beat(); err != nil {
		return stop(err)
	}
	tick := time.NewTicker(o.Every)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return stop(ctx.Err())
		case <-closed:
			return stop(errors.New("invoking harness pipe closed; presence stopped"))
		case <-tick.C:
			select {
			case err := <-done:
				return err
			default:
			}
			if err := beat(); err != nil {
				return stop(err)
			}
		}
	}
}

// beatEnvironment pins the explicit remote route without ambient local-store configuration.
func beatEnvironment(env []string, server string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, v := range env {
		name, _, _ := strings.Cut(v, "=")
		switch name {
		case "NOVA_SPRINT_SERVER", "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_SPRINT_REDIS_USER", "NOVA_SPRINT_REDIS_PASSWORD_ENV":
			continue
		}
		filtered = append(filtered, v)
	}
	return append(filtered, "NOVA_SPRINT_SERVER="+server)
}

// bounded retains only the diagnostic prefix while draining every child byte.
type bounded struct{ text string }

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	if room := 4096 - len(b.text); room > 0 {
		b.text += string(p[:min(room, len(p))])
	}
	return n, nil
}
