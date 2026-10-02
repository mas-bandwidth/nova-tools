package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// A caller supplies the epoch it observed. No retry silently enters a newer one.
func (app *application) writeFlags(fs *flag.FlagSet) (*ntable.WriteOptions, *bool) {
	opts := &ntable.WriteOptions{Receipt: &ntable.Receipt{}}
	fs.Uint64Var(&opts.Epoch, "epoch", app.defaults.Epoch, "the epoch this write observed (default 0)")
	fs.StringVar(&opts.Actor, "actor", app.defaults.Actor, "actor recorded with the change")
	fs.StringVar(&opts.Fence, "fence", app.defaults.Fence, "coordinator fence recorded with the change")
	fs.StringVar(&opts.Idem, "idem", app.defaults.Idem, "attempt identifier recorded with the change; does not deduplicate")
	dryRunFlag(fs, app.dryRun)
	return opts, fs.Bool("receipt", app.receipts, "print the committed event ID, epoch and revision")
}

// dryRunFlag declares --dry-run on a verb that writes (preflight answers it).
func dryRunFlag(fs *flag.FlagSet, value bool) {
	fs.Bool("dry-run", value, "check the arguments, print the call the verb would send and stop: nothing is dialled or written")
}

// preflight is THE REAL RUN'S OWN PLAN, run before anything is dialled. call
// is the verb's one call to internal/ntable, the very call the real run makes;
// here it runs against a client that sends nothing (planClient), so every
// check the call makes before its first command (a name, a column spec, a
// manifest's outgoing bytes, a place) runs exactly as it will for real, and
// the dry run and the real run refuse in the same words where the real run
// would refuse before sending, with no store dialled for it. Under --dry-run
// the verb then stops with its plan: the verb, its arguments, the flags given,
// the first command it would send and the store it would go to. Else the real
// run goes on to dial. What the store decides (the table, its epoch, a bound
// cell) is left to the real run. It reports whether the verb is done, and the
// exit.
func (app *application) preflight(stdout, stderr io.Writer, fs *flag.FlagSet, verb, addr string, args []string, call func(redis.Cmdable) error) (int, bool) {
	return app.preflightWith(stderr, fs, verb, call, func(p *planClient) int { return plan(stdout, fs, verb, addr, args, p) })
}

// preflightWith is preflight with the verb's own plan line (batch prints its
// manifest's counts).
func (app *application) preflightWith(stderr io.Writer, fs *flag.FlagSet, verb string, call func(redis.Cmdable) error, show func(*planClient) int) (int, bool) {
	if code, refused := app.overridden(stderr, fs, verb); refused {
		return code, true
	}
	p := newPlanClient()
	// ignored: closing a client that never opened a connection
	defer func() { _ = p.Close() }()
	if err := call(p.Client); err != nil && p.sends == "" {
		return (*connection)(nil).refusal(stderr, verb, err), true
	}
	if f := fs.Lookup("dry-run"); f == nil || f.Value.String() != "true" {
		return 0, false
	}
	return show(p), true
}

// plan prints a dry run's one line; its exit is 0, or 1 when the line did not
// reach stdout (a closed pipe), as a verb's own output does.
func plan(stdout io.Writer, fs *flag.FlagSet, verb, addr string, args []string, p *planClient) int {
	var b strings.Builder
	b.WriteString("TABLE DRY-RUN verb=" + strings.Join(strings.Fields(verb), "-"))
	for i, a := range args {
		fmt.Fprintf(&b, " arg%d=%s", i+1, field(a))
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "dry-run" && f.Name != "redis" {
			fmt.Fprintf(&b, " %s=%s", f.Name, field(f.Value.String()))
		}
	})
	if strings.TrimSpace(addr) == "" {
		addr = "-"
	}
	fmt.Fprintf(&b, " sends=%s redis=%s dialled=%d written=0\n", field(cmp.Or(p.sends, "nothing")), field(addr), p.dials)
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return 1
	}
	return 0
}

// sent is a call that also returns a value, as preflight takes it.
func sent[T any](call func(redis.Cmdable) (T, error)) func(redis.Cmdable) error {
	return func(c redis.Cmdable) error { _, err := call(c); return err }
}

// errNotSent is what a planClient answers every command: nothing was sent.
var errNotSent = errors.New("not sent: a dry run's plan")

// planClient is a go-redis client that sends nothing: its hooks answer every
// command and pipeline with errNotSent before a connection is asked for, and
// any dial is counted and refused. sends is the first command a call would
// have sent (MULTI aside), named by its verb and, for FCALL, its function.
type planClient struct {
	*redis.Client
	sends string
	dials int
}

func newPlanClient() *planClient {
	p := &planClient{}
	p.Client = redis.NewClient(&redis.Options{Addr: "plan.invalid:0", MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) { p.dials++; return nil, errNotSent }})
	p.AddHook(p)
	return p
}

func (p *planClient) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) { p.dials++; return nil, errNotSent }
}

func (p *planClient) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		p.saw(cmd)
		cmd.SetErr(errNotSent)
		return errNotSent
	}
}

func (p *planClient) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			p.saw(cmd)
			cmd.SetErr(errNotSent)
		}
		return errNotSent
	}
}

// saw keeps the first command a call would send.
func (p *planClient) saw(cmd redis.Cmder) {
	name := strings.ToUpper(cmd.Name())
	if p.sends != "" || name == "MULTI" || name == "EXEC" {
		return
	}
	p.sends = name
	if a := cmd.Args(); (name == "FCALL" || name == "FCALL_RO") && len(a) > 1 {
		p.sends += " " + fmt.Sprint(a[1])
	}
}

// overridden refuses a write line that turns the dry run off inside a shell
// entered with --dry-run. A dry shell is dry for every line, whatever the
// line says: refusing --dry-run=false (rather than planning the line anyway
// with a note) is the answer that cannot surprise, because a reader who
// wrote --dry-run=false meant to write, and a plan printed in place of the
// write would read like a write that happened; the refusal says the line did
// nothing and how to write for real.
func (app *application) overridden(stderr io.Writer, fs *flag.FlagSet, verb string) (int, bool) {
	if !app.dryRun {
		return 0, false
	}
	off := false
	fs.Visit(func(f *flag.Flag) { off = off || (f.Name == "dry-run" && f.Value.String() != "true") })
	if !off {
		return 0, false
	}
	return refuse(stderr, verb, "this shell was entered with --dry-run, so every write line is planned and none is written; "+
		"--dry-run=false does not turn that off; to write, leave the shell (quit) and run the verb on its own; run: nova-table help shell"), true
}

func printReceipt(out io.Writer, opts *ntable.WriteOptions, enabled bool) {
	if enabled {
		r := opts.Receipt
		fmt.Fprintf(out, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n", r.ID, r.Epoch, r.Before, r.After, r.Outcome)
	}
}
