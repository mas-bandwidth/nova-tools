package main

// The friend heartbeat of #2610. `beat` is the process a friend's window
// starts and forgets: it writes one hash with a TTL every 30 seconds (its
// stamp, and its child count when --width is passed, #2673) and spends nothing
// else, so presence costs no tokens and is never the thing dropped under load.
// `presence` is the read: one line, up or down per friend, for the swarm table
// and for anyone about to hand a friend work. Presence is Redis only; the git
// bus carries notes and never beats (#3144).
//
// The two failures this replaces both happened on 2026-09-22: four tasks were
// handed to a friend who had been gone ten hours, and a friend who had never
// had a window up was given three. Both facts existed; nothing surfaced them.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/presence"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The hints for the new flags, in the shape every other refusal here keeps:
// what the input WANTS, not just what was wrong with it.
const (
	storeHint  = `--store <host:port> is the fleet Redis the heartbeat lives on, the same address the other verbs spell --redis; the password is never a flag -- it reaches this process as NOVA_REDIS_BENCH_PASSWORD through nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`
	beatAsHint = `--as <name> is the friend whose window this is, spelled as the bus roster spells it; the key it writes is friend:<name> and there is no flag that beats for somebody else`
	rosterHint = `the roster is the friends SET in the store when no flag names one; otherwise --friends <a,b,c>, --participants <file> (the bus participants.json) or --bus <dir> (its checkout, whose participants.json is read), exactly one`
	ttlHint    = `--ttl <duration> is how long one beat keeps the friend up, and it must be longer than --every or the key lapses between beats and a friend who is here reads as AWAY; the default is three beats, 90s for a 30s cadence`
	widthHint  = `--width <n> is how many children are in use now, a whole number written to the width field of friend:<name> with the beat's TTL; zero is a real count, and leaving --width off writes no field and is not a failure`
)

// storeOpener is the seam the tests enter through: the live verbs dial Redis,
// a test hands in a fake with a clock it moves.
type storeOpener func(ctx context.Context, addr, user string) (presence.Store, func() error, error)

// dialStore is the live opener.
func dialStore(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
	r, err := presence.Open(ctx, addr, user)
	if err != nil {
		return nil, nil, err
	}
	return r, r.Close, nil
}

// cmdBeat is the friend's own process: HSET friend:<name> at <utc> [width <n>]
// plus PEXPIRE <ttl>, in one MULTI, every --every, for as long as the window
// lives. A friend whose width changes re-runs beat with the new number. It ends when the window does, and
// that ending IS the signal: no shutdown hook, no goodbye note, nothing to
// forget to run.
func cmdBeat(args []string, stdout, stderr io.Writer, clock wake.Clock, open storeOpener) int {
	fs := flag.NewFlagSet("beat", flag.ContinueOnError)
	var (
		as     = fs.String("as", "", "")
		store  = fs.String("store", "", "")
		user   = fs.String("user", presence.DefaultUser, "")
		every  = fs.String("every", "", "")
		ttl    = fs.String("ttl", "", "")
		window = fs.String("window", "", "")
		width  = fs.String("width", "", "")
		once   = fs.Bool("once", false, "")
	)
	if !parseFlags(fs, args, stderr) {
		return 2
	}

	var p problems
	if strings.TrimSpace(*as) == "" {
		p.add("--as is required; refusing to guess", "  "+beatAsHint+"\n")
	}
	if strings.TrimSpace(*store) == "" {
		p.add("--store is required; refusing to guess", "  "+storeHint+"\n")
	}
	period := presence.DefaultEvery
	if *every != "" {
		period = parseDur(&p, "every", *every)
	}
	lifetime := presence.DefaultTTL
	if *ttl != "" {
		lifetime = parseDur(&p, "ttl", *ttl)
	}
	if !p.any() && lifetime <= period {
		p.add(fmt.Sprintf("--ttl %s is not longer than --every %s", oneline.Field(lifetime.String()), oneline.Field(period.String())), "  "+ttlHint+"\n")
	}
	// Window is the cap's reset time as the caller spelled it. This verb
	// does not read a clock to invent one. Empty, including a flag of only
	// spaces, is the flag not passed: no field, and not a failure.
	var widthN int64
	side := presence.Side{Window: strings.TrimSpace(*window)}
	if raw := strings.TrimSpace(*width); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 63)
		if err != nil {
			p.add("--width "+*width+" is not a count of children", "  "+widthHint+"\n")
		} else {
			widthN = int64(n)
			side.Width = &widthN
		}
	}
	if !p.any() {
		if _, err := presence.Addr(*store); err != nil {
			p.add(err.Error(), "  "+storeHint+"\n")
		}
	}
	if p.any() {
		return p.print(stderr, "beat")
	}

	ctx := context.Background()
	st, closeStore, err := open(ctx, *store, *user)
	if err != nil {
		return refuse(stderr, " beat", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	defer func() { _ = closeStore() }()

	name := presence.Normalize(*as)
	addr, _ := presence.Addr(*store)
	fmt.Fprintf(stdout, "beat %s key=%s every=%s ttl=%s store=%s",
		oneline.Field(name), oneline.Field(presence.Key(name)),
		oneline.Field(period.String()), oneline.Field(lifetime.String()), oneline.Field(addr))
	if side.Window != "" {
		fmt.Fprintf(stdout, " window=%s", oneline.Field(side.Window))
	}
	if side.Width != nil {
		fmt.Fprintf(stdout, " width=%s", oneline.Field(strconv.FormatInt(*side.Width, 10)))
	}
	fmt.Fprintln(stdout)

	if *once {
		if err := presence.BeatSide(ctx, st, name, clock.Now(), lifetime, side); err != nil {
			return refuse(stderr, " beat", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		return 0
	}

	beatLoop(ctx, st, name, period, lifetime, clock, stderr, side, 0)
	return 0
}

// beatLoop is the beat itself, forever when rounds is 0. It never dies of a
// store that blinked: a beat that exited on the first timeout would report its
// friend as gone for the rest of the day, which is the failure this whole verb
// exists to end. It says what went wrong once on stderr, keeps beating, and
// says so again only when the answer changes -- a line every 30 seconds in a
// friend's window would be the next thing anybody turned off.
//
// rounds is the tests' door and no flag's: a caller's "stop after n" is --once.
func beatLoop(ctx context.Context, st presence.Store, name string, period, ttl time.Duration, clock wake.Clock, stderr io.Writer, side presence.Side, rounds int) {
	var failing string
	for i := 0; rounds <= 0 || i < rounds; i++ {
		err := presence.BeatSide(ctx, st, name, clock.Now(), ttl, side)
		switch {
		case err != nil && err.Error() != failing:
			failing = err.Error()
			fmt.Fprintf(stderr, "nova-wake beat: %s; still beating every %s\n",
				oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)), oneline.Field(period.String()))
		case err == nil && failing != "":
			failing = ""
			fmt.Fprintf(stderr, "nova-wake beat: store answering again\n")
		}
		clock.Sleep(period)
	}
}

// cmdPresence is the read: one line naming every friend and whether they are
// up or down, with the width a live beat carries. The roster is the store's
// `friends` SET unless a flag names one; the hashes are read in one pipeline,
// never by a scan. It reports what the store says and nothing else -- never
// what the reader expects, which is the report that was wrong twice in one day.
func cmdPresence(args []string, stdout, stderr io.Writer, clock wake.Clock, open storeOpener) int {
	fs := flag.NewFlagSet("presence", flag.ContinueOnError)
	var (
		store        = fs.String("store", "", "")
		user         = fs.String("user", presence.DefaultUser, "")
		friends      = fs.String("friends", "", "")
		participants = fs.String("participants", "", "")
		busDir       = fs.String("bus", "", "")
	)
	if !parseFlags(fs, args, stderr) {
		return 2
	}

	var p problems
	if strings.TrimSpace(*store) == "" {
		p.add("--store is required; refusing to guess", "  "+storeHint+"\n")
	} else if _, err := presence.Addr(*store); err != nil {
		p.add(err.Error(), "  "+storeHint+"\n")
	}
	names := rosterFrom(&p, *friends, *participants, *busDir)
	if p.any() {
		return p.print(stderr, "presence")
	}

	ctx := context.Background()
	st, closeStore, err := open(ctx, *store, *user)
	if err != nil {
		return refuse(stderr, " presence", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	defer func() { _ = closeStore() }()

	if names == nil {
		names, err = presence.Friends(ctx, st)
		if err != nil {
			return refuse(stderr, " presence", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	now := clock.Now()
	sts, err := presence.Read(ctx, st, names, now)
	if err != nil {
		return refuse(stderr, " presence", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintln(stdout, presence.Line(sts, now))
	return 0
}

// rosterFrom resolves the friends to report on: the names a caller listed, the
// participants file they named, or the participants.json of the bus checkout
// they named. At most one source, and no built-in list: a roster hard-coded
// here would be a second copy, and the copy is what goes stale. No source at
// all is nil and no problem: the caller reads the store's friends SET.
func rosterFrom(p *problems, friends, participants, busDir string) []string {
	given := 0
	for _, s := range []string{friends, participants, busDir} {
		if strings.TrimSpace(s) != "" {
			given++
		}
	}
	if given == 0 {
		return nil
	}
	if given > 1 {
		p.add("--friends, --participants and --bus are three spellings of one roster; give one", "  "+rosterHint+"\n")
		return nil
	}

	if strings.TrimSpace(friends) != "" {
		var out []string
		for _, n := range strings.Split(friends, ",") {
			if n = presence.Normalize(n); n != "" {
				out = append(out, n)
			}
		}
		if len(out) == 0 {
			p.add("--friends "+friends+" names nobody", "  "+rosterHint+"\n")
		}
		return out
	}

	path := participants
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(busDir, "participants.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		p.add("cannot read "+path, "  "+rosterHint+"\n")
		return nil
	}
	names, err := presence.ParticipantNames(data)
	if err != nil {
		p.add(path+": "+err.Error(), "  "+rosterHint+"\n")
		return nil
	}
	return names
}
