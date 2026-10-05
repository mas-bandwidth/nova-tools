package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// serveRange bounds one read of the coordinator's stream; the rest is the next tick's.
const serveRange = 10000

// readFriendRows is the real friends: the names of nova-config's friend rows,
// the store by config.ResolveDSN (--pg, else NOVA_PG_DSN), bounded.
func (w world) readFriendRows(ctx context.Context, pg string) ([]string, error) {
	dsn, err := config.ResolveDSN(pg, w.getenv)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := config.OpenPG(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer st.Close() // ignored: closing a read of the config store, nothing was written
	rows, err := st.List(ctx, config.KindFriend)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names, nil
}

// peers is the friend rows but the coordinator's own, sorted.
func peers(me string, rows []string) []string {
	out := slices.DeleteFunc(slices.Clone(rows), func(n string) bool { return n == me })
	slices.Sort(out)
	return slices.Compact(out)
}

// serve is the coordinator's ping loop (docs/SPEC-FRIEND.md, "The coordinator's
// ping"; friend.Keepalive holds the rules): each PingEvery it reads the pongs
// on its own stream, says each friend whose state changed, and sends every
// friend row a PING with a fresh nonce; the rows are read again each RowsEvery.
func (w world) serve(c *tool.Call) *tool.Out {
	addr := c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return o
	}
	me, pg, dry := c.Str("as"), c.Str("pg"), c.DryRun()
	ctx, stop := w.signals(context.Background())
	defer stop()
	rows, err := w.friends(ctx, pg)
	if err != nil {
		return tool.Refuse("the friend rows cannot be read: " + err.Error())
	}
	names := peers(me, rows)
	if len(names) == 0 {
		o := tool.Refuse("there is no friend row but " + me + " to ping")
		o.Remedy = "nova-config friend add <name> --width <n>"
		return o
	}
	if dry {
		// the rows read; no store opened, nothing sent
		return tool.Done().Fact("friends", strings.Join(names, ",")).Fact("every", friend.PingEvery).Fact("down_after", friend.DownAfter)
	}
	say := func(line string) { fmt.Fprintln(c.Stdout, "SERVE "+line) }
	st, closeStore := w.openUntil(ctx, addr, func(line string) { say("NOTE " + line) })
	if st == nil {
		say("STOP interrupted")
		return tool.Exit(0)
	}
	defer closeStore()
	b := &bus.Bus{Store: st}
	k := friend.NewKeepalive()
	start := w.now()
	k.Friends(start, names)
	rowsAt := start
	say(fmt.Sprintf("OK friends=%s every=%s down_after=%s", strings.Join(k.Names(), ","), friend.PingEvery, k.DownAfter))
	cursor, failing := "", ""
	bad := false
	fail := func(what string) {
		bad = true
		if what != failing {
			say("NOTE " + w.now().UTC().Format(time.RFC3339) + " " + oneline.Escape(what) + "; trying again each tick")
		}
		failing = what
	}
	for ctx.Err() == nil {
		now := w.now()
		bad = false
		if now.Sub(rowsAt) >= friend.RowsEvery {
			if rows, err := w.friends(ctx, pg); err != nil {
				fail("the friend rows cannot be read: " + err.Error())
			} else {
				k.Friends(now, peers(me, rows))
				rowsAt = now
			}
		}
		ok := true
		if cursor == "" {
			// the stream from the store's now: no pong from before the loop answers a nonce of its own
			if _, storeNow, err := st.Roster(ctx); err != nil {
				fail("the store did not answer: " + err.Error())
				ok = false
			} else {
				cursor = bus.IDAt(storeNow)
			}
		}
		if ok {
			if es, err := st.Range(ctx, bus.StreamOf(me), cursor, "+", serveRange); err != nil {
				fail("the store did not answer: " + err.Error())
				ok = false
			} else {
				for _, e := range es {
					m := e.Message()
					if nonce, isPong := friend.PongNonce(m.Body); isPong {
						k.Pong(m.From, nonce, now) // the sender is the message's from, never its body
					}
					cursor = "(" + e.Entry
				}
			}
		}
		for _, ch := range k.Step(now) {
			line := strings.ToUpper(ch.State) + " friend=" + oneline.Field(ch.Friend) + " at=" + now.UTC().Format(time.RFC3339)
			if ch.State == friend.PeerDown {
				last := "never"
				if !ch.LastPong.IsZero() {
					last = ch.LastPong.UTC().Format(time.RFC3339)
				}
				line += " last_pong=" + last + " reason=" + oneline.Quote(ch.Reason)
			}
			say(line)
		}
		for _, n := range k.Names() {
			nonce := w.random()
			ping := bus.Message{From: me, To: []string{n}, Subject: friend.PingPrefix + nonce, Body: friend.PingText(me, start, nonce) + "\n"}
			if _, err := b.Send(ctx, ping); err != nil {
				k.Failed(n, err.Error())
				continue
			}
			k.Sent(n, nonce, now)
		}
		if !bad && failing != "" {
			say("NOTE " + now.UTC().Format(time.RFC3339) + " the rows and the store answer again")
			failing = ""
		}
		w.sleep(ctx, friend.PingEvery)
	}
	say("STOP interrupted")
	return tool.Exit(0)
}
