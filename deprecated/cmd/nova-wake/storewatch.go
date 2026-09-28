package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/presence"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// watch --store (nova-tools #3876): the fact that a pull request moved is
// already an entry on ev:github, written by the webhook receiver, so a watch on
// a --pr blocks on that stream with XREAD BLOCK instead of polling GitHub. It
// starts no gh and no git fetch, has no poll cadence, and returns the moment a
// matching entry is served.
//
// The cursor is the last stream id this state has read, kept in --state under
// storeCursorKey, so an entry added between two calls wakes the second one. A
// cold state starts at the stream's tip: what was there before the first
// watch is history, not news. The cursor is saved after the lines are printed,
// so a kill in between is a repeated wake and never a lost one.

// storeCursorKey is the --state key holding the last ev:github id read.
const storeCursorKey = "store:ev:github"

// storeBlockSlice bounds one XREAD BLOCK, so the caller's stop and the
// deadline are read at least this often; an XADD still ends a block at once.
// A var so a test can make one block outlast its whole wait.
var storeBlockSlice = 5 * time.Second

// storeReadCount is the most entries one XREAD returns.
const storeReadCount = 100

// storeFlags are the flags a store watch takes; any other flag names a polled
// source or a poll cadence, and a watch that blocks on the stream has neither.
var storeFlags = map[string]bool{"store": true, "user": true, "state": true, "max": true, "on-deadline": true, "pr": true}

const storeWatchHint = "  a store watch blocks on ev:github for the --pr it names: it polls nothing, so it runs no gh and no git fetch, and a polled source or a poll cadence has no meaning beside it\n"

// cmdWatchStore is `nova-wake watch --store`. fs is the parsed watch flag set.
func cmdWatchStore(fs *flag.FlagSet, verb, store, user, state, maxDur, onDeadline string, prs []string, stdout, stderr io.Writer, clock wake.Clock) int {
	var p problems
	if verb != "watch" {
		p.add("--store is a watch flag", storeWatchHint)
	}
	fs.Visit(func(f *flag.Flag) {
		if !storeFlags[f.Name] {
			p.add("--"+f.Name+" does not go with --store", storeWatchHint)
		}
	})
	if _, err := presence.Addr(store); err != nil {
		p.add(oneline.Err(err), "  "+storeHint+"\n")
	}
	if state == "" {
		p.missing("state")
	}
	if maxDur == "" {
		p.missing("max")
	}
	if onDeadline == "" {
		p.missing("on-deadline")
	}
	max := parseDur(&p, "max", maxDur)
	if max > MaxCeiling {
		p.add("--max "+maxDur+" is over the "+wake.Dur(MaxCeiling)+" ceiling", "  a watch runs inside a tool call; sit under your harness's limit\n")
	}
	if len(prs) == 0 {
		p.add("--store needs at least one --pr <owner>/<repo>#<n>", storeWatchHint)
	}
	want := map[string]string{} // "<repo lowercased>#<n>" -> the name as spelled
	for _, name := range prs {
		repo, number, err := wake.SplitPR(name)
		if err != nil {
			p.add("--pr "+name+" is not a pull request", "  "+oneline.Err(err)+"\n")
			continue
		}
		want[strings.ToLower(repo)+"#"+number] = name
	}
	if p.any() {
		return p.print(stderr, verb)
	}

	release, holder, err := wake.LockState(state)
	if err != nil {
		return refused(stderr, oneline.Err(err))
	}
	if release == nil {
		return refused(stderr, "another nova-wake holds "+wake.LockName(state)+" (pid "+holder+"); give this watch a state file of its own")
	}
	defer release()
	st, err := wake.Load(state)
	if err != nil {
		return refused(stderr, "the state file "+state+" could not be read: "+oneline.Err(err)+
			"; repair it, or pass a new --state path and accept a cold start on purpose")
	}

	ctx := context.Background()
	if watchStopHook != nil {
		stopCh := watchStopHook()
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-stopCh:
				cancel()
			case <-ctx.Done():
			}
		}()
	} else {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	addr, _ := presence.Addr(store)
	if strings.TrimSpace(user) == "" {
		user = presence.DefaultUser
	}
	user, password, err := presence.Login(user)
	if err != nil {
		return refused(stderr, oneline.Err(err))
	}
	ev := wake.OpenEvGithub(addr, user, password)
	defer ev.Close()

	start := clock.Now()
	deadline := start.Add(max)
	cold := st.Cold()
	cursor, haveCursor := st.Get(storeCursorKey)
	if !haveCursor || cursor == "" {
		tip, terr := ev.Tip(ctx)
		if terr != nil {
			return storeBroken(stdout, st, state, terr, start)
		}
		cursor = tip
		st.Set(storeCursorKey, cursor)
	}
	fmt.Fprintf(stdout, "WAKE at=%s max=%s on-deadline=%s sources=store stream=%s prs=%d state=%s cold=%t cursor=%s\n",
		oneline.Field(wake.Stamp(start)), oneline.Field(wake.Dur(max)), oneline.Field(onDeadline),
		oneline.Field(wake.EvGithubStream), len(want), oneline.Field(state), cold, oneline.Field(cursor))

	polls := 0
	for {
		now := clock.Now()
		if ctx.Err() != nil {
			saveStore(stderr, st, state, cursor)
			fmt.Fprintf(stdout, "WAKE STOPPED after=%s polls=%d pending=0: stopped by the caller\n",
				oneline.Field(wake.Dur(now.Sub(start))), polls)
			return 0
		}
		if !now.Before(deadline) {
			saveStore(stderr, st, state, cursor)
			fmt.Fprintf(stdout, "WAKE QUIET after=%s polls=%d default=%s sources-failing=0: deadline, default taken\n",
				oneline.Field(wake.Dur(now.Sub(start))), polls, oneline.Field(onDeadline))
			return 0
		}
		block := deadline.Sub(now)
		if block > storeBlockSlice {
			block = storeBlockSlice
		}
		if block < time.Millisecond {
			block = time.Millisecond
		}
		polls++
		events, rerr := ev.Read(ctx, cursor, storeReadCount, block)
		if _, real := clock.(wake.Real); !real && rerr == nil && len(events) == 0 {
			// On an injected clock the block's real wait is not on the clock's
			// hands; move them, or a test's deadline would never arrive.
			clock.Sleep(block)
		}
		if rerr != nil {
			if ctx.Err() != nil {
				continue
			}
			n, since, _ := st.Fail("store", oneLine(rerr.Error()), now)
			fmt.Fprintf(stderr, "WAKE POLL store: %s (failure %d of 3 in a row, since %s)\n",
				oneline.Escape(oneline.Cap(oneLine(rerr.Error()), oneline.TailBytes)), n, oneline.Field(since))
			if n >= 3 {
				saveStore(stderr, st, state, cursor)
				fmt.Fprintf(stdout, "WAKE BROKEN source=store failures=%d since=%s: %s\n",
					n, oneline.Field(since), oneline.Escape(oneline.Cap(oneLine(rerr.Error()), oneline.TailBytes)))
				return 2
			}
			rest := time.Second
			if left := deadline.Sub(now); left < rest {
				rest = left
			}
			clock.Sleep(rest)
			continue
		}
		st.ClearFail("store")
		hits := 0
		for _, e := range events {
			cursor = e.ID
			name, ok := want[strings.ToLower(e.Repo)+"#"+e.Number]
			if !ok {
				continue
			}
			hits++
			fmt.Fprintf(stdout, "WAKE EVENT pr=%s kind=%s action=%s head=%s sender=%s at=%s id=%s\n",
				oneline.Field(name), oneline.Field(dash(e.Kind)), oneline.Field(dash(e.Action)),
				oneline.Field(dash(e.Head)), oneline.Field(dash(e.Sender)),
				oneline.Field(dash(e.At)), oneline.Field(e.ID))
		}
		if hits > 0 {
			saveStore(stderr, st, state, cursor)
			fmt.Fprintf(stdout, "WAKE CHANGE after=%s polls=%d prs=%d pending=0\n",
				oneline.Field(wake.Dur(clock.Now().Sub(start))), polls, hits)
			return 0
		}
	}
}

// saveStore writes the cursor into the state; a failed write is said, because
// the next call would re-read from the older cursor (a repeated wake).
func saveStore(stderr io.Writer, st *wake.State, path, cursor string) {
	st.Set(storeCursorKey, cursor)
	if err := st.Save(path); err != nil {
		fmt.Fprintf(stderr, "WAKE POLL state: %s\n", oneline.Err(err))
	}
}

// storeBroken is a store that could not be read before the first block: the
// streak is recorded and the call ends BROKEN, exit 2, rather than CALM.
func storeBroken(stdout io.Writer, st *wake.State, path string, err error, now time.Time) int {
	n, since, _ := st.Fail("store", oneLine(err.Error()), now)
	_ = st.Save(path)
	fmt.Fprintf(stdout, "WAKE BROKEN source=store failures=%d since=%s: %s\n",
		n, oneline.Field(since), oneline.Escape(oneline.Cap(oneLine(err.Error()), oneline.TailBytes)))
	return 2
}
