// Command fardelay makes a TCP server, usually a store, look far away, as a
// process: a TCP proxy that listens on the loopback, forwards every connection
// to a target, and holds each write of the client back by a fixed delay before
// it forwards it. Point a tool at the address it prints and every command costs
// the delay, the way it would against a server that far away. It is the proxy
// tests use (testredis.Far), run by hand for an interactive drive; both run
// internal/delayproxy.
//
//	example:
//	  fardelay --listen 127.0.0.1:7001 --target 127.0.0.1:7000 --delay 64ms
//	  LISTEN OK addr=127.0.0.1:7001 target=127.0.0.1:7000 delay=64ms
//	  (a client of 127.0.0.1:7001 now sees the store at 7000 a round trip of 64ms away)
//	  ^C
//	  STOP OK writes=12 shortest=64.1ms
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/delayproxy"
)

// The exit codes: 0 stopped as asked or help, 2 could not run.
const (
	exitOK        = 0
	exitCannotRun = 2
)

// kib and mib are a kibibyte and a mebibyte, for the help's sizes.
const (
	kib = 1 << 10
	mib = 1 << 20
)

// usage is the help. What it says of the limits is read from the proxy's own
// constants, so the help and the proxy cannot disagree.
var usage = fmt.Sprintf(usageFormat, delayproxy.MaxDelay, delayproxy.ChunkBytes/kib, delayproxy.WindowBytes/mib, delayproxy.WindowBytes/mib, delayproxy.DefaultMaxConns)

const usageFormat = `fardelay: make a TCP server look far away, without leaving this machine

usage:
  fardelay --target HOST:PORT --delay DURATION [--listen HOST:PORT]
  fardelay -h | --help

  fardelay is a proxy. It listens on the loopback, connects every client that
  comes in to --target, and holds what the client sends back by --delay before
  it passes it on. Point the client at the address fardelay prints, instead of
  at the server, and every command costs --delay more, as if the server stood
  that far away. It speaks no protocol, so any TCP server will do; a Redis
  server is the usual one. One fardelay serves one target: run another, with
  its own --listen, for a second. It runs until interrupted (SIGINT, SIGTERM),
  then stops and prints what it held.

flags:
  --target HOST:PORT   the server to forward to. Required. Any host and a port;
                       it is dialled once for each client connection, so it may
                       start after fardelay does.
  --delay DURATION     how long each write of the client is held, a Go duration
                       with a unit (64ms, 1s), 0s to %v. Required. Only what
                       the client sends is held: the answer comes straight
                       back, so one command and its answer cost --delay once
                       and --delay 128ms is a server 128ms away by round trip.
  --listen HOST:PORT   where to listen. It must be on the loopback: 127.0.0.1,
                       ::1 or localhost, so this is never a way in from the
                       network. Default 127.0.0.1:0, a port the kernel picks;
                       the LISTEN line says which.

what is held: a write is what one read of the client's connection returns,
which for a write of up to %d KiB on the loopback is the write. A pipeline that
a client sends in one write pays --delay once, however many commands it holds,
up to about %d MiB. The proxy holds no more than that in flight, and a client
that writes more waits until it has gone, so each further %d MiB pays --delay
once more. Three commands sent one after the other, each waiting for its
answer, pay --delay three times. Order is kept, every client connection is held
on its own clock, and up to %d client connections are served at once; a client
past that waits until one ends.

example:
  fardelay --target 127.0.0.1:7000 --delay 128ms
  LISTEN OK addr=127.0.0.1:41873 target=127.0.0.1:7000 delay=128ms
  (in another terminal, redis-cli -p 41873 PING answers 128ms later than
  redis-cli -p 7000 PING does)
  ^C
  STOP OK writes=12 shortest=128.1ms

output (stdout, one line each):
  LISTEN OK addr=<host:port> target=<host:port> delay=<duration>
      once it is listening: the address to give the client
  STOP OK writes=<n> shortest=<duration>
      at the end: the writes it held and forwarded, and the least time any was
      held (never less than --delay). Trouble the client cannot see, such as a
      target that would not answer, is one line on stderr.

exit: 0 stopped as asked (or this help). 2 could not run: the invocation was
refused or the address could not be listened on; one line on stderr, then
run: fardelay -h.
`

// errHelp is parse's answer to -h and --help.
var errHelp = errors.New("help")

// config is what the flags say.
type config struct {
	listen, target string
	delay          time.Duration
}

// parse reads the flags. It refuses a missing --target or --delay, naming every
// one that is missing, and a value the proxy would refuse, before anything
// listens.
func parse(args []string) (config, error) {
	cfg := config{}
	fs := flag.NewFlagSet("fardelay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.listen, "listen", "127.0.0.1:0", "")
	fs.StringVar(&cfg.target, "target", "", "")
	fs.DurationVar(&cfg.delay, "delay", 0, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, errHelp
		}
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected argument %q: every input is a flag", fs.Arg(0))
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	var missing []string
	for _, name := range []string{"target", "delay"} {
		if !given[name] {
			missing = append(missing, "--"+name)
		}
	}
	switch len(missing) {
	case 0:
	case 1:
		return cfg, fmt.Errorf("missing required flag %s", missing[0])
	default:
		return cfg, fmt.Errorf("missing required flags %s", strings.Join(missing, ", "))
	}
	if err := delayproxy.Target(cfg.target); err != nil {
		return cfg, fmt.Errorf("--target %q: %v", cfg.target, err)
	}
	if cfg.delay < 0 || cfg.delay > delayproxy.MaxDelay {
		return cfg, fmt.Errorf("--delay %v is outside 0s to %v", cfg.delay, delayproxy.MaxDelay)
	}
	if err := delayproxy.Loopback(cfg.listen); err != nil {
		return cfg, fmt.Errorf("--listen %q: %v", cfg.listen, err)
	}
	return cfg, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the tool: the flags, the proxy until ctx ends, the line for each. It
// returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parse(args)
	switch {
	case errors.Is(err, errHelp):
		fmt.Fprint(stdout, usage)
		return exitOK
	case err != nil:
		fmt.Fprintf(stderr, "fardelay: %v; run: fardelay -h\n", err)
		return exitCannotRun
	}
	p, err := delayproxy.Listen(cfg.listen, cfg.target, cfg.delay, delayproxy.Options{
		Logf: func(format string, args ...any) { fmt.Fprintf(stderr, "fardelay: "+format+"\n", args...) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "fardelay: %v; run: fardelay -h\n", err)
		return exitCannotRun
	}
	fmt.Fprintf(stdout, "LISTEN OK addr=%s target=%s delay=%v\n", p.Addr(), cfg.target, cfg.delay)
	<-ctx.Done()
	p.Stop()
	fmt.Fprintf(stdout, "STOP OK writes=%d shortest=%v\n", p.Writes(), p.Shortest())
	return exitOK
}
