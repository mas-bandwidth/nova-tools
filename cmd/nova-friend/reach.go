package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// reach is deliberately a thin transport binding around friend.Reach. Its
// clock, bus and window effects are all injected through world, so the ladder
// is unit-tested without a socket, process or wall clock (SPEC-FRIEND Reach).
func (w world) reach(c *tool.Call) *tool.Out {
	me, name, nonce := c.Str("as"), c.Str("friend"), w.random()
	from := friend.ReachStep(c.Str("from"))
	if c.DryRun() {
		for i, step := range friend.ReachSteps {
			if i >= reachStart(from) && !c.Bool("json") {
				fmt.Fprintf(c.Stdout, "REACH PLAN step=%s friend=%s nonce=%s\n", step, name, nonce)
			}
		}
		return tool.Done().Fact("friend", name).Fact("from", from)
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	start := w.now()
	floor := bus.IDAt(start)
	state := c.Str("state-dir")
	if state == "" {
		state = friend.DefaultStateDir(w.home, name)
	}
	pong := w.pongCommand(name, nonce, state, c.Str("redis"))
	message := "REACH " + nonce + "\nRun this exact command first, then reply with a real message: " + pong
	r := friend.Reach{
		Now:  w.now,
		Wait: w.sleep,
		Line: func(line string) {
			if !c.Bool("json") {
				fmt.Fprintln(c.Stdout, line)
			}
		},
		Do: func(ctx context.Context, step friend.ReachStep, n string) (string, string, error) {
			switch step {
			case friend.ReachBus:
				m, err := b.Send(ctx, bus.Message{From: me, To: []string{name}, Subject: "REACH STEP " + string(step), Body: message})
				if err != nil {
					return "", "", err
				}
				return m.ID, "", nil
			case friend.ReachPush:
				s, found, err := friend.ReadStatus(state)
				if err != nil {
					return "", "", err
				}
				if !found || w.now().Sub(s.At) >= friend.DaemonStale {
					return "", "daemon is not up", nil
				}
				p, present, err := friend.ReadPresence(state)
				if err != nil {
					return "", "", err
				}
				if present && p.Presence != friend.PresenceUp {
					return "", "daemon presence is down", nil
				}
				m, err := b.Send(ctx, bus.Message{From: me, To: []string{name}, Subject: "REACH PUSH", Body: message})
				if err != nil {
					return "", "", err
				}
				return m.ID, "", nil
			case friend.ReachWindow:
				window := w.window
				if window == nil { window = friend.WindowReach }
				if err := window(ctx, w.exec, c.Str("window-bundle"), c.Str("window-title"), c.Str("composer-id"), message); err != nil {
					return "", "", err
				}
				return "window", "", nil
			default:
				return "", "", fmt.Errorf("unknown reach step %q", step)
			}
		},
		Proof: func(ctx context.Context, want string) (string, bool, error) {
			entries, err := b.Log(ctx, floor)
			if err != nil {
				return "", false, err
			}
			for _, entry := range entries {
				m := entry.Message()
				if m.From != name || m.At.Before(start) {
					continue
				}
				if got, _, _, _, pong := friend.ParsePong(strings.TrimSpace(m.Body)); pong {
					if got == want { return "pong", true, nil }
					continue
				}
				fields := strings.Fields(m.Subject)
				if len(fields) == 0 { continue }
				word := strings.ToLower(fields[0])
				if !friend.IsRealMessage(word) || strings.HasPrefix(m.Subject, "SESSION CHECK ") {
					continue
				}
				if m.From != me {
					return "message", true, nil
				}
			}
			return "", false, nil
		},
	}
	step, ok, err := r.Run(c.Ctx, from, nonce, c.Dur("step-timeout"))
	if err != nil {
		return tool.Refuse("reach " + string(step) + " could not run: " + err.Error())
	}
	if ok {
		return tool.Done().Fact("friend", name).Fact("step", step).Fact("took", w.now().Sub(start).Round(time.Millisecond).String())
	}
	_, err = b.Send(context.Background(), bus.Message{From: me, To: []string{me}, Subject: "REACH FAILED", Body: "REACH FAILED friend=" + name + " tried=bus,push,window"})
	if err != nil {
		return answer(err)
	}
	return tool.Fail("friend=" + name + " tried=bus,push,window")
}

func reachStart(from friend.ReachStep) int {
	for i, step := range friend.ReachSteps {
		if step == from {
			return i
		}
	}
	return 0
}
