package redisconn

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/redis/go-redis/v9"
)

// explainer is the go-redis hook Open installs on its connection's client,
// after the handshake: a command or pipeline whose error is Unreachable,
// AuthRefused or Unconfirmed comes back explained (Conn.Explain), both as
// the error the call returns and as the error each command holds. Every
// other error (redis.Nil, a refusal the store wrote, a cancelled context, a
// closed client) and every success is untouched.
//
// It adds no state to the connection and changes nothing that is sent or
// read. What it adds is where a transport failure stands against the
// command, which Classify cannot see from the error alone: go-redis dials
// and sets a connection up (HELLO, with the login) inside the command that
// needs it, with that command's context and through the same hooks, so a
// dial that failed or a setup that failed inside the call is met before the
// command was written, and a transport failure that is one of them is
// Unreachable. Any other transport failure of the call is after the command
// was written, or cannot be placed, and is Unconfirmed. tla/FirstConn.tla
// states this in its header as the classification of the failure edge
// after OpenReturns, the event after which the hook is installed; it is no
// state or transition of the model.
//
// The setup's own commands are not the caller's: they are passed through as
// they came, so go-redis reads the store's answer to decide what to try
// next, and only the caller's command is explained, once.
type explainer struct{ c *Conn }

// call is one call of the caller's through the hook: what failed inside it
// before its command was written.
type call struct {
	mu     sync.Mutex
	before []error
}

// callKey marks the context of a call the explainer of one connection has
// entered.
type callKey struct{ c *Conn }

// note records an error met before the call's command was written.
func (k *call) note(err error) {
	k.mu.Lock()
	k.before = append(k.before, err)
	k.mu.Unlock()
}

// unsent reports whether err is one of the errors met before the command
// was written.
func (k *call) unsent(err error) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, b := range k.before {
		if errors.Is(err, b) {
			return true
		}
	}
	return false
}

// enter returns ctx marked with the call it is inside, and whether it was
// already inside one: a command that arrives inside a call is a
// connection's setup.
func (e explainer) enter(ctx context.Context) (context.Context, *call, bool) {
	if k, ok := ctx.Value(callKey{e.c}).(*call); ok {
		return ctx, k, true
	}
	k := &call{}
	return context.WithValue(ctx, callKey{e.c}, k), k, false
}

// explain is Explain with the class the call knows: a transport failure met
// before the command was written is Unreachable, whatever Classify says of
// the error alone. The three classes of the connection are explained, and
// ok is true; any other error comes back as it is, and ok is false. (ok,
// not a comparison of the two errors: an error whose type is not comparable
// would make that panic.)
func (e explainer) explain(k *call, err error) (_ error, ok bool) {
	if err == nil || isFailure(err) {
		return err, false
	}
	class := Classify(err)
	switch class {
	case Unconfirmed:
		if k.unsent(err) {
			class = Unreachable
		}
	case Unreachable, AuthRefused:
	default:
		return err, false
	}
	return explain(e.c.login, e.c.hide, err, class, false), true
}

// DialHook notes a dial that failed inside a call: it is before the
// command.
func (e explainer) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		nc, err := next(ctx, network, addr)
		if err != nil {
			if k, ok := ctx.Value(callKey{e.c}).(*call); ok {
				k.note(err)
			}
		}
		return nc, err
	}
}

// ProcessHook explains a single command's error, and notes a connection's
// setup that failed.
func (e explainer) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, k, inside := e.enter(ctx)
		err := next(ctx, cmd)
		if err == nil {
			return nil
		}
		if inside {
			k.note(err)
			return err
		}
		// go-redis's Client.Process sets the command's error to what the
		// hook returns (v9.22.0, redis.go:2146), so the command holds the
		// explained error too.
		explained, _ := e.explain(k, err)
		return explained
	}
}

// ProcessPipelineHook explains a pipeline's or a transaction's error and
// the error of each of its commands, and notes a connection's setup that
// failed.
func (e explainer) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, k, inside := e.enter(ctx)
		err := next(ctx, cmds)
		if inside {
			if err != nil {
				k.note(err)
			}
			return err
		}
		for _, cmd := range cmds {
			if cerr := cmd.Err(); cerr != nil {
				if explained, ok := e.explain(k, cerr); ok {
					cmd.SetErr(explained)
				}
			}
		}
		explained, _ := e.explain(k, err)
		return explained
	}
}
