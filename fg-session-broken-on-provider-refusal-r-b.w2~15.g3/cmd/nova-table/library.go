package main

// Every table verb is an FCALL into the nova_sprint function library
// (internal/nsprint/fn's lua/), so a store that holds no library answers the
// first one with "ERR Function not found". nova-table puts the library on
// such a store itself, on first contact, with redisfn's LoadMissing: the
// load of every caller that is not the deployer, which never replaces a
// library of the name, whatever its code. Upgrading a
// store's library is the deployer's, with nova-redis fn load.
//
// The load costs nothing on a store that holds the library: no FUNCTION
// LIST goes ahead of a verb, which stays one round trip. Only an FCALL the
// store answered "Function not found" sets it off, and that answer means the
// function ran nothing, so the command is sent again, once, after the load
// (a command whose reply was lost is never sent again: AtMostOnce). The load
// is made at most once per process: after a load that reached an outcome, a
// function the store still lacks is the store's library being older than
// this build, and the verb's refusal names the deployer's remedy. A load
// that failed (the store could not be reached) is tried again by the next
// verb that meets the answer.
//
// tla/TableFirstContact.tla models it: the library is never replaced, a verb
// is sent again only after a load and only when its first send ran nothing,
// and a process loads at most once.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// functionNotFound is the store's answer to an FCALL of a function no
// library on it registers.
const functionNotFound = "ERR Function not found"

// deployRemedy is the deployer's load of this build's library.
const deployRemedy = "run: nova-redis fn load --addr <host:port> (the deployer's load of this build's library)"

// libraryLoad puts the library on the store the hook's client talks to when
// the store holds none (redisfn.Library.LoadMissing).
type libraryLoad func(ctx context.Context) (redisfn.Receipt, error)

// firstContact is the once-per-process state of the load: whether a load has
// reached an outcome (LOADED, UNCHANGED or SKIPPED), and the outcome.
type firstContact struct {
	mu      sync.Mutex
	done    bool
	receipt redisfn.Receipt
	loads   int // loads attempted, for the tests
}

// processLibrary is this process's first contact: one per process, shared by
// every connection the process opens (a shell reopens its connection).
var processLibrary = &firstContact{}

// ensure makes the load when no load has reached an outcome yet in this
// process, and says whether the command that met "Function not found" is to
// be sent again: only after a load in this call that left a library on the
// store. A load that failed is returned as its error and leaves the state as
// it was, so a later verb tries again.
func (f *firstContact) ensure(ctx context.Context, load libraryLoad) (again bool, receipt redisfn.Receipt, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return false, f.receipt, nil
	}
	f.loads++
	receipt, err = load(ctx)
	if err != nil {
		return false, receipt, err
	}
	f.done, f.receipt = true, receipt
	return receipt.Outcome == redisfn.Loaded || receipt.Outcome == redisfn.Unchanged, receipt, nil
}

// libraryHook is the go-redis hook that does it for one client. It is the
// client's first hook, so the commands of the load and the command sent
// again pass the hooks after it (the trip counter among them), and a verb's
// trips= counts them.
type libraryHook struct {
	state *firstContact
	load  libraryLoad
}

// tableLibrary is the library nova-table's verbs call.
func tableLibrary() redisfn.Library {
	lib := fn.Spec()
	lib.Remedy = "nova-redis fn load --addr <host:port>"
	return lib
}

// withLibrary adds the first-contact hook to client, loading
// tableLibrary through client itself.
func withLibrary(client *redis.Client) {
	lib := tableLibrary()
	client.AddHook(libraryHook{state: processLibrary, load: func(ctx context.Context) (redisfn.Receipt, error) {
		return lib.LoadMissing(ctx, client)
	}})
}

// missing reports a command that is an FCALL the store answered "Function
// not found": it ran nothing.
func missing(cmd redis.Cmder, err error) bool {
	if err == nil {
		return false
	}
	name := cmd.Name()
	if name != "fcall" && name != "fcall_ro" {
		return false
	}
	var reply redis.Error
	if !errors.As(err, &reply) {
		return false
	}
	return strings.HasPrefix(reply.Error(), functionNotFound)
}

func (h libraryHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h libraryHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if !missing(cmd, err) {
			return err
		}
		again, receipt, lerr := h.state.ensure(ctx, h.load)
		if lerr != nil {
			return &libraryError{cmd: cmd, err: err, load: lerr}
		}
		if again {
			err = next(ctx, cmd)
			if !missing(cmd, err) {
				return err
			}
		}
		return &libraryError{cmd: cmd, err: err, receipt: receipt}
	}
}

// ProcessPipelineHook loads as ProcessHook does, and sends a pipeline again
// only when every command in it is an FCALL the store answered "Function not
// found", so none of them ran anything; a pipeline with any other command, or
// a transaction (its MULTI and EXEC), is never sent again, and its missing
// FCALLs carry the remedy.
func (h libraryHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		if !anyMissing(cmds) {
			return err
		}
		again, receipt, lerr := h.state.ensure(ctx, h.load)
		if again && allMissing(cmds) {
			err = next(ctx, cmds)
			if !anyMissing(cmds) {
				return err
			}
		}
		var first error
		for _, cmd := range cmds {
			if missing(cmd, cmd.Err()) {
				cmd.SetErr(&libraryError{cmd: cmd, err: cmd.Err(), receipt: receipt, load: lerr})
			}
			if first == nil && cmd.Err() != nil {
				first = cmd.Err()
			}
		}
		if first != nil {
			return first
		}
		return err
	}
}

func anyMissing(cmds []redis.Cmder) bool {
	return slices.ContainsFunc(cmds, func(cmd redis.Cmder) bool { return missing(cmd, cmd.Err()) })
}

func allMissing(cmds []redis.Cmder) bool {
	for _, cmd := range cmds {
		if !missing(cmd, cmd.Err()) {
			return false
		}
	}
	return len(cmds) > 0
}

// libraryError is "Function not found" in words that say why and what to do.
// It unwraps to the store's reply, so its class (redisconn.Classify) is the
// store's own refusal, exit 1; when the load itself failed it unwraps to the
// load's error, so a store that stopped answering is still unreachable.
type libraryError struct {
	cmd     redis.Cmder
	err     error
	receipt redisfn.Receipt
	load    error
}

func (e *libraryError) Error() string {
	function := "(unnamed)"
	if args := e.cmd.Args(); len(args) > 1 {
		if s, ok := args[1].(string); ok {
			function = s
		}
	}
	text := e.err.Error() + ": function " + function
	switch {
	case e.load != nil:
		text += "; the store holds no nova_sprint library and loading it failed: " + e.load.Error()
	case e.receipt.Outcome == redisfn.Skipped:
		text += "; the store holds no nova_sprint library and would not let this login load it (" + e.receipt.Why + ")"
	default:
		text += "; the store's nova_sprint library does not register it: it is older than this nova-table, or was removed after this process loaded it"
	}
	return text + "; " + deployRemedy
}

func (e *libraryError) Unwrap() error {
	if e.load != nil {
		return e.load
	}
	return e.err
}
