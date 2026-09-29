package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
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
	fail := fs.Float64("fail", 0.1, "the chance a work card comes back failed")
	broken := fs.Float64("broken", 0.05, "the chance a read finds it broken")
	batch := fs.Int("batch", 100, "a merge step's batch")
	stuck := fs.Float64("stuck", 0.10, "the chance a batch has a card that does not merge")
	cross := fs.Float64("cross", 0.01, "the chance a batch has a card that needs a card of another stream first")
	red := fs.Float64("red", 0.0, "the chance a batch turns the stream branch red")
	flap := fs.Float64("flap", 0, "the chance, per member and tick, that a member's machine falls silent (stops beating), and the same chance that a silent one beats again")
	hold := fs.Bool("hold", false, "play --flap's downs as the coordinator's hold (fleet down, fleet up) instead of a machine falling silent; holds it took are released before it stops")
	var silent silenceFlag
	fs.Var(&silent, "silent", "<member>@<from>+<for>: the member's machine stops beating <from> after play starts, for <for> (e.g. m3@30s+20s); repeatable")
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
		Config: driver.Config{Every: *every, Batch: *batch, TakeLimit: *take, ReadLimit: *reads, Ticks: *ticks, Hold: *hold, Silent: silent}}
	why, err := d.Loop()
	if err != nil {
		fmt.Fprintf(stderr, "%s play: %s\n", prog, oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintf(stdout, "PLAY OK stopped=%s seed=%d\n", why, *seed)
	return 0
}

// silenceFlag is --silent <member>@<from>+<for>, repeatable.
type silenceFlag []driver.Silence

func (f *silenceFlag) String() string {
	var out []string
	for _, s := range *f {
		out = append(out, s.Member+"@"+s.From.String()+"+"+s.For.String())
	}
	return strings.Join(out, ",")
}

func (f *silenceFlag) Set(v string) error {
	member, rest, ok := strings.Cut(v, "@")
	from, dur, ok2 := strings.Cut(rest, "+")
	if !ok || !ok2 || member == "" {
		return fmt.Errorf("--silent wants <member>@<from>+<for>, e.g. m3@30s+20s, got %q", v)
	}
	a, err := time.ParseDuration(from)
	if err != nil {
		return fmt.Errorf("--silent %q: %v", v, err)
	}
	b, err := time.ParseDuration(dur)
	if err != nil {
		return fmt.Errorf("--silent %q: %v", v, err)
	}
	*f = append(*f, driver.Silence{Member: member, From: a, For: b})
	return nil
}
