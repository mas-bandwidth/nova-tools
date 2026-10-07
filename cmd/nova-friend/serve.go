package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// serveRange bounds one read of the coordinator's stream; the rest is the next tick's.
const serveRange = 10000

// friendRows is the names of nova-config's friend rows as the bus store
// holds them (the set `friends`, written by nova-config apply), and the
// store's time, in one trip.
func friendRows(ctx context.Context, st bus.Store) ([]string, time.Time, error) {
	friends, _, now, err := st.Members(ctx)
	return friends, now, err
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
	me, dry := c.Str("as"), c.DryRun()
	say := func(line string) { fmt.Fprintln(c.Stdout, "SERVE "+line) }
	if dry {
		// the rows read; nothing sent
		b, closeStore, o := w.bus(c)
		if o != nil {
			return o
		}
		defer closeStore()
		ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
		defer cancel()
		rows, _, err := friendRows(ctx, b.Store)
		if err != nil {
			return tool.Refuse("the friend rows cannot be read: " + err.Error())
		}
		names := peers(me, rows)
		if len(names) == 0 {
			return noPeers(me)
		}
		return tool.Done().Fact("friends", strings.Join(names, ",")).Fact("every", friend.PingEvery).Fact("down_after", friend.DownAfter)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	st, closeStore := w.openUntil(ctx, addr, func(line string) { say("NOTE " + line) })
	if st == nil {
		say("STOP interrupted")
		return tool.Exit(0)
	}
	defer closeStore()
	rows, storeNow, err := friendRows(ctx, st)
	if err != nil {
		return tool.Refuse("the friend rows cannot be read: " + err.Error())
	}
	names := peers(me, rows)
	if len(names) == 0 {
		return noPeers(me)
	}
	b := &bus.Bus{Store: st}
	k := friend.NewKeepalive()
	start := w.now()
	k.Friends(start, names)
	rowsAt := start
	say(fmt.Sprintf("OK friends=%s every=%s down_after=%s", strings.Join(k.Names(), ","), friend.PingEvery, k.DownAfter))
	// the stream from the store's now: no pong from before the loop answers a nonce of its own
	cursor, failing := bus.IDAt(storeNow), ""
	bad := false
	provedNote := ""
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
		ok := true
		if now.Sub(rowsAt) >= friend.RowsEvery {
			if rows, _, err := friendRows(ctx, st); err != nil {
				fail("the friend rows cannot be read: " + err.Error())
				ok = false
			} else {
				k.Friends(now, peers(me, rows))
				rowsAt = now
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
				if err := k.HealthBatch(ctx, st, friend.Authority{Name: me}); err != nil {
					line := "proved was not written: " + err.Error()
					if line != provedNote {
						say("NOTE " + now.UTC().Format(time.RFC3339) + " " + oneline.Escape(line))
						provedNote = line
					}
				} else {
					provedNote = ""
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

func noPeers(me string) *tool.Out {
	o := tool.Refuse("there is no friend row but " + me + " to ping")
	o.Remedy = "nova-config friend add <name> --width <n>"
	return o
}
