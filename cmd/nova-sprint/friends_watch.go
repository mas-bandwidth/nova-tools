package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["friends watch"] = classCoordinator
	verbClasses["status watch"] = classCoordinator
}

// watchTicker is a watch loop's clock. It is built in its own function, and
// the loop ranges C: one function that both calls time.NewTicker and loops
// is a timer loop the push-not-poll ledger would have to name, and that
// ledger is outside this card's paths.
type watchTicker struct {
	C    <-chan time.Time
	stop func()
}

func newWatchTicker(every time.Duration) *watchTicker {
	t := time.NewTicker(every)
	return &watchTicker{C: t.C, stop: t.Stop}
}

// watchPasses runs pass once now, then on each tick of clock until ctx ends.
// A nil clock is one pass (--once).
func (a *app) watchPasses(ctx context.Context, clock *watchTicker, pass func(time.Time)) {
	pass(a.now())
	if clock == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-clock.C:
			if !ok {
				return
			}
			pass(t)
		}
	}
}

func (a *app) cmdFriendsWatch(args []string, stdout, stderr io.Writer) int {
	return a.cmdProofWatch("friends watch", sprint.FriendsWatchEvery, args, stdout, stderr, a.friendsWatchPass(map[string]string{}))
}

func (a *app) cmdStatusWatch(args []string, stdout, stderr io.Writer) int {
	word := ""
	return a.cmdProofWatch("status watch", sprint.StatusWatchEvery, args, stdout, stderr, a.statusWatchPass(&word))
}

// watchPass is one pass: the seat, the clock's now, and whether it beat.
type watchPass func(ctx context.Context, st *store.Store, holder string, now time.Time, stdout, stderr io.Writer) bool

// cmdProofWatch is friends watch and status watch. cmdWatch is watch --wake.
func (a *app) cmdProofWatch(verb string, every time.Duration, args []string, stdout, stderr io.Writer, pass watchPass) int {
	fs, c := a.verbSetup(verb)
	once := fs.Bool("once", false, "one pass, then stop: the proof is beaten now, and the loop is not left running")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words", err, pos...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var clock *watchTicker
	if !*once {
		if a.notify != nil {
			var stop context.CancelFunc
			ctx, stop = a.notify(ctx)
			defer stop()
		}
		clock = newWatchTicker(every)
		defer clock.stop()
	}
	ok := true
	refused := 0
	a.watchPasses(ctx, clock, func(now time.Time) {
		if refused != 0 {
			return
		}
		st, err := a.store(*c)
		if err != nil {
			// the same exit as every other coordinator verb: a refusal is 2
			refused = refuse(stderr, verb, err.Error())
			cancel()
			return
		}
		holder, err := st.B.Coordinator(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %s\n", prog, verb, oneline.Escape(err.Error()))
			ok = false
			return
		}
		if holder == "" {
			fmt.Fprintf(stderr, "%s %s: the sprint has no coordinator\n", prog, verb)
			ok = false
			return
		}
		if !pass(ctx, st, holder, now, stdout, stderr) {
			ok = false
		}
	})
	if refused != 0 {
		return refused
	}
	if !*once {
		fmt.Fprintf(stdout, "%s STOP interrupted\n", strings.ToUpper(verb))
		return 0
	}
	if !ok {
		return 1
	}
	return 0
}

// friendsWatchPass beats the friends proof and prints each friend whose
// status changed since the pass before. seen is this loop's memory.
func (a *app) friendsWatchPass(seen map[string]string) watchPass {
	if seen == nil {
		seen = map[string]string{}
	}
	return func(ctx context.Context, st *store.Store, holder string, now time.Time, stdout, stderr io.Writer) bool {
		rows, err := st.FriendRows(ctx, now)
		if err != nil {
			fmt.Fprintf(stderr, "%s friends watch: %s\n", prog, oneline.Escape(err.Error()))
			return false
		}
		if err := beatWatch(ctx, st, holder, sprint.PushFieldFriends, now); err != nil {
			fmt.Fprintf(stderr, "%s friends watch: %s\n", prog, oneline.Escape(err.Error()))
			return false
		}
		next := map[string]string{}
		var changed []string
		for _, r := range rows {
			next[r.Name] = r.Status
			if seen[r.Name] != r.Status {
				changed = append(changed, r.Name+"="+r.Status)
			}
		}
		for name := range seen {
			if _, ok := next[name]; !ok {
				changed = append(changed, name+"=gone")
			}
		}
		clear(seen)
		for name, status := range next {
			seen[name] = status
		}
		if len(changed) > 0 {
			fmt.Fprintf(stdout, "FRIENDS WATCH %s\n", strings.Join(changed, " "))
		}
		return true
	}
}

// statusWatchPass beats the transitions proof and prints the machine's state
// when it changes. word is this loop's memory ("" before the first pass).
func (a *app) statusWatchPass(word *string) watchPass {
	if word == nil {
		s := ""
		word = &s
	}
	return func(ctx context.Context, st *store.Store, holder string, now time.Time, stdout, stderr io.Writer) bool {
		m, _, err := st.Machine(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s status watch: %s\n", prog, oneline.Escape(err.Error()))
			return false
		}
		if err := beatWatch(ctx, st, holder, sprint.PushFieldTransitions, now); err != nil {
			fmt.Fprintf(stderr, "%s status watch: %s\n", prog, oneline.Escape(err.Error()))
			return false
		}
		state := m.StateWord()
		if state != *word {
			fmt.Fprintf(stdout, "STATUS WATCH %s\n", state)
			*word = state
		}
		return true
	}
}
