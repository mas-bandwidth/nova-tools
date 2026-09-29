package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/driver"
)

// appClock is the app's clock for the driver.
type appClock struct{ a *app }

func (c appClock) Now() time.Time        { return c.a.now() }
func (c appClock) Sleep(d time.Duration) { c.a.sleep(d) }

// cmdPlay plays the world outside the table through this command's own verbs.
func (a *app) cmdPlay(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("play")
	seed := fs.Uint64("seed", 1, "the seed: the same seed plays the same run")
	every := fs.Duration("every", time.Second, "between ticks")
	start := fs.Bool("start", false, "start ready primaries each tick")
	fail := fs.Float64("fail", 0.1, "the chance a work card comes back failed")
	broken := fs.Float64("broken", 0.05, "the chance a read finds it broken")
	batch := fs.Int("batch", 100, "a merge step's batch")
	stuck := fs.Float64("stuck", 0.10, "the chance a batch has a card that does not merge")
	cross := fs.Float64("cross", 0.01, "the chance a batch has a card that needs a card of another stream first")
	red := fs.Float64("red", 0.0, "the chance a batch turns the stream branch red")
	flap := fs.Float64("flap", 0, "the chance, per member and tick, that an up member goes down, and the same chance that a down member comes up; members it took down are brought up before it stops")
	ticks := fs.Int("ticks", 0, "stop after n ticks; 0 is until every stream lands")
	take := fs.Int("take", 10, "work cards a member takes a tick")
	reads := fs.Int("reads", 10, "read cards a reader reports a tick")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "play", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "play", "takes no words, found "+pos[0])
	}
	if _, err := a.store(*c); err != nil {
		return refuse(stderr, "play", err.Error())
	}
	var base []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "redis" || f.Name == "prefix" {
			base = append(base, "--"+f.Name, f.Value.String())
		}
	})
	facts := driver.NewSeeded(*seed)
	facts.Fail, facts.Broken, facts.Stuck, facts.Cross, facts.Red, facts.Flap = *fail, *broken, *stuck, *cross, *red, *flap
	d := &driver.Driver{Run: a.run, Base: base, Facts: facts, Clock: appClock{a}, Out: stdout,
		Config: driver.Config{Every: *every, Start: *start, Batch: *batch, TakeLimit: *take, ReadLimit: *reads, Ticks: *ticks}}
	why, err := d.Loop()
	if err != nil {
		fmt.Fprintf(stderr, "%s play: %s\n", prog, oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintf(stdout, "PLAY OK stopped=%s seed=%d\n", why, *seed)
	return 0
}
