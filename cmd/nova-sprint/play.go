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
	simulation := fs.Bool("simulation", false, "play the locked simulation: "+simulationWords()+"; a chance flag beside it sets that one chance")
	chance := map[string]*float64{}
	for _, r := range driver.ChanceRows {
		chance[r.Flag] = fs.Float64(r.Flag, r.Plain, r.Usage)
	}
	chance["flap"] = fs.Float64("flap", 0, "the chance, per member and second, that a member's machine falls silent, and the same chance that a silent one beats again: --down and --up with the one chance")
	red := fs.Float64("red", 0.0, "the chance a batch turns the stream branch red")
	batch := fs.Int("batch", 100, "a merge step's batch")
	hold := fs.Bool("hold", false, "play the downs as the coordinator's hold (fleet down, fleet up) instead of a machine falling silent; holds it took are released before it stops")
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
	given := map[string]float64{} // the chances the person named, by flag
	fs.Visit(func(f *flag.Flag) {
		if p, ok := chance[f.Name]; ok {
			given[f.Name] = *p
		}
	})
	chances, err := driver.Set(*simulation, given)
	if err == nil {
		err = driver.Valid("red", *red)
	}
	if err != nil {
		return refuse(stderr, "play", err.Error())
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
	base = append(base, "--actor", c.actor) // the driver plays the coordinator's part as this actor
	facts := driver.NewSeeded(*seed)
	facts.Use(chances, *every)
	facts.Red = *red
	// the chances the source will draw with, printed once the loop may play
	header := fmt.Sprintf("chances: %s red=%g seed=%d every=%s", facts.Drawn(), facts.Red, *seed, *every)
	d := &driver.Driver{Run: a.run, Base: base, Facts: facts, Clock: appClock{a}, Out: stdout, Header: header,
		Config: driver.Config{Every: *every, Batch: *batch, TakeLimit: *take, ReadLimit: *reads, Ticks: *ticks, Hold: *hold, Silent: silent}}
	why, err := d.Loop()
	if err != nil {
		fmt.Fprintf(stderr, "%s play: %s\n", prog, oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintf(stdout, "PLAY OK stopped=%s seed=%d\n", why, *seed)
	return 0
}

// simulationWords is the locked simulation's chances, as the flags that set them.
func simulationWords() string {
	c, _ := driver.Set(true, nil)
	return c.String()
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
