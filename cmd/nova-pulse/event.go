package main

// The event verb's flags: one XADD to cards:done per card transition (nova-tools #2563).
// --tokens-in, --tokens-out and --usd that are not given are ABSENT from the entry, never 0:
// a writer that does not know the cost has not reported a zero cost.
// The card path is still bash in places -- the launcher, harvest, the lander -- so this verb
// is the one line each of them writes until it is Go, and the Go writers call
// internal/events directly.
//
// THE PASSWORD IS NEVER A FLAG. It is read from the environment `nova-secrets exec --only`
// put it in, named by --password-env, so it never reaches an argv a `ps` can read and
// nothing here ever prints it.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// defaultPasswordEnv is the variable `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`
// leaves the fleet store's password in.
const defaultPasswordEnv = events.DefaultPasswordEnv

func cmdEvent(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("event")
	store := f.fs.String("store", "", "")
	user := f.fs.String("user", "", "")
	passwordEnv := f.fs.String("password-env", defaultPasswordEnv, "")
	stream := f.fs.String("stream", events.Stream, "")
	label := f.fs.String("label", "", "")
	kind := f.fs.String("event", "", "")
	attempt := f.fs.Int("attempt", 0, "")
	bench := f.fs.String("bench", "", "")
	model := f.fs.String("model", "", "")
	route := f.fs.String("route", "", "")
	tokensIn := f.fs.Int64("tokens-in", 0, "")
	tokensOut := f.fs.Int64("tokens-out", 0, "")
	usd := f.fs.Float64("usd", 0, "")
	pr := f.fs.String("pr", "", "")
	head := f.fs.String("head", "", "")
	at := f.fs.String("at", "", "")
	timeout := f.fs.Int("timeout", 10, "")
	print := f.fs.Bool("print", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*label, "label", "the card label this transition belongs to, the id the stream joins on")
	f.want(*kind, "event", "one of "+events.KindList())
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	stamp := now.UTC()
	if *at != "" {
		parsed, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			f.add(fmt.Sprintf("--at wants an RFC3339 stamp such as 2026-09-22T14:05:00Z, got %q", *at))
		} else {
			stamp = parsed.UTC()
		}
	}
	if f.refused(stderr) {
		return 2
	}
	given := map[string]bool{}
	f.fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
	var inPtr, outPtr *int64
	var usdPtr *float64
	if given["tokens-in"] {
		inPtr = events.Int64(*tokensIn)
	}
	if given["tokens-out"] {
		outPtr = events.Int64(*tokensOut)
	}
	if given["usd"] {
		usdPtr = events.Float64(*usd)
	}
	return events.EmitOne(events.EmitInput{
		Addr:     *store,
		Username: *user,
		Password: os.Getenv(*passwordEnv),
		Stream:   *stream,
		Event: events.Event{
			Label:     *label,
			Attempt:   *attempt,
			Bench:     *bench,
			Model:     *model,
			Route:     *route,
			Kind:      events.Kind(*kind),
			TokensIn:  inPtr,
			TokensOut: outPtr,
			USD:       usdPtr,
			PR:        *pr,
			Head:      *head,
			At:        stamp,
		},
		Print:   *print,
		Now:     stamp,
		Timeout: time.Duration(*timeout) * time.Second,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}
