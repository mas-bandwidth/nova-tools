package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The ladder (docs/SPEC-FRIEND.md, Reach; tla/Reach.tla). A proof is a choice
// at the bound, so the model has no fairness that would force one.
const (
	reachBus    = "bus"
	reachPush   = "push"
	reachWindow = "window"
	reachPoll   = time.Second
)

func reachSteps(from string) []string {
	all := []string{reachBus, reachPush, reachWindow}
	if from == "" {
		from = reachBus
	}
	for i, s := range all {
		if s == from {
			return all[i:]
		}
	}
	return all
}

// ReachText is the body a step carries: the nonce and the pong command.
func ReachText(step, nonce, friendName, coordinator string) string {
	return fmt.Sprintf("REACH step=%s nonce=%s\nAnswer first, before anything else, with one command: nova-friend pong --as %s --nonce %s --to %s\n", step, nonce, friendName, nonce, coordinator)
}

// reachFX is every side effect of the ladder. The climb itself is a pure
// function of the step, whether a proof has been seen, and the clock.
type reachFX struct {
	coordinator string
	Send        func(ctx context.Context, nonce, body string) (id string, err error)
	DaemonUp    func() (up bool, why string)
	Push        func(ctx context.Context, body string) error
	Window      func(ctx context.Context, body string) error
	Arm         func(ctx context.Context) error
	Proof       func(ctx context.Context, nonce string) (by string, ok bool, err error)
	Tell        func(ctx context.Context, line string) error
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration)
	Nonce       func() string
}

func (w world) reachVerb() tool.Verb {
	return tool.Verb{
		Name:  "reach",
		Usage: "reach --as <coordinator> --to <friend> [--step-timeout <duration>] [--from <bus|push|window>] [--harness <h>] [--dir <d>] [--session <id>] [--state-dir <d>] [--redis <addr>] [--dry-run]",
		// The banner's example block is the first-run list. This verb's example
		// lives in the detail, where reach -h and the command reference carry it.
		Example:   "",
		Effect:    tool.Delivery + ": a bus message, then a push into the session, then the friend's window, stopping at the first proof",
		DryRun:    true,
		ExitTable: "0 a proof, 1 no proof, 2 could not run.",
		Detail: `The ladder that gets a silent friend's attention, stopping at the first proof. The friend is --to, the same shape as ping: a verb other than the default takes no bare word. This verb is reach. see also: nova-friend ping --wake is the coordinator's periodic wake check; reach is this escalation ladder. Each step has one --step-timeout budget (default 60s), including delivery and waiting for a proof: a pong for the nonce the step carries, or any other message from the friend. A daemon-pong is the daemon's own and is not a proof. A pong for another nonce, or a malformed pong, is not an ordinary-message proof.
1. bus: a bus message to the friend carrying the nonce and the exact pong line (REACH STEP step=bus sent=<id> nonce=<n>).
2. push: the daemon pushes a real message into the session as a turn, never a PING. The daemon must be up (its status file read at this step and newer than the stale bound), else the step is skipped (REACH NONE step=push waited=0s: daemon down: <reason>).
3. window: a tmux-hosted session is typed with send-keys only while the pane is idle. A GUI harness is the app's window, found by its bundle, the message typed into the composer and submitted. That needs the accessibility permission a person grants to this binary. When it is absent the step is refused and the tool does not ask: ` + friend.AccessibilityRemedy + `.
A proof ends the ladder: REACH PROOF step=<s> after=<duration> by=<pong|message> and REACH OK friend=<f> step=<s>. Exit 0 on that proof. No proof: REACH NONE step=<s> waited=<d> and the ladder climbs. No proof after the steps from --from: REACH FAILED friend=<f> tried=<steps>, Exit 1, and one note of that line on the coordinator's own stream. Exit 2 when it could not run (a flag, a store that did not answer, or the window step without the accessibility permission). --from bus|push|window starts partway up. --dry-run prints REACH DRY-RUN and one REACH STEP per planned step, and sends, pushes and types nothing. --json: facts friend, step (on OK), tried (on FAILED), from and step_timeout (on a dry run), dry_run; items STEP (step, sent, nonce), PROOF (step, after, by), NONE (step, waited, text when skipped). The result line is first, then one line per step in the order it happened.
example: nova-friend reach --as ada --to bob --dry-run`,
		Flags: func(f *tool.Flags) {
			f.Required("as", "your name, the coordinator")
			f.Required("to", "the friend to reach")
			f.Duration("step-timeout", time.Minute, "how long each step waits for a proof (default 60s)")
			f.String("from", reachBus, "the step to start at: bus, push or window (default bus)")
			f.String("harness", "", "the harness the push and the window use (default: the one the daemon's status names)")
			f.String("dir", "", "the friend's working directory")
			f.String("session", "", "the session to type into (default: the one host saved, else friend-<friend>; harness tmux)")
			f.String("state-dir", "", "where the friend's state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<friend>)")
			f.String("redis", w.getenv(RedisEnv), "the bus store's Redis address, host:port (default: "+RedisEnv+")")
			f.Check(func(c *tool.Call) {
				if c.Dur("step-timeout") <= 0 {
					c.Problem("--step-timeout wants a positive duration, such as 60s")
				}
				switch c.Str("from") {
				case "", reachBus, reachPush, reachWindow:
				default:
					c.Problem("--from wants bus, push or window")
				}
				if a, to := c.Str("as"), c.Str("to"); a != "" && a == to {
					c.Problem("--as and --to are the same name; the coordinator reaches a friend")
				}
			})
		},
		Run: w.reach,
	}
}

func (w world) reach(c *tool.Call) *tool.Out {
	me, to := c.Str("as"), c.Str("to")
	from := c.Str("from")
	if from == "" {
		from = reachBus
	}
	timeout := c.Dur("step-timeout")
	if c.DryRun() {
		o := tool.Done().As("DRY-RUN").Fact("friend", to).Fact("from", from).Fact("step_timeout", timeout.String())
		for _, step := range reachSteps(from) {
			o.Item("STEP", "step", step)
		}
		return o.Note("sends, pushes and types nothing")
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	ctx := c.Ctx
	state := w.reachState(c, to)
	status, found, _ := friend.ReadStatus(state) // ignored: DaemonUp reads and reports the current status at the push
	harness := c.Str("harness")
	if harness == "" && found {
		harness = status.Harness
	}
	var cursor string
	fx := reachFX{
		coordinator: me,
		Send: func(ctx context.Context, nonce, body string) (string, error) {
			m, err := b.Send(ctx, bus.Message{From: me, To: []string{to}, Subject: "reach " + nonce, Body: body})
			if err != nil {
				return "", err
			}
			return m.ID, nil
		},
		DaemonUp: func() (bool, string) {
			// SPEC-FRIEND Reach: bus can consume more than DaemonStale; readiness
			// belongs to the current push step, not the preflight snapshot.
			status, found, statusErr := friend.ReadStatus(state)
			if c.Str("harness") == "" && found {
				harness = status.Harness
			}
			if statusErr != nil {
				return false, statusErr.Error()
			}
			if !found {
				return false, "no daemon has run (no status file)"
			}
			if w.now().Sub(status.At) >= friend.DaemonStale {
				return false, "the status file is older than " + friend.DaemonStale.String()
			}
			return true, ""
		},
		Push:   func(ctx context.Context, body string) error { return w.reachPush(ctx, c, harness, to, state, body) },
		Window: func(ctx context.Context, body string) error { return w.reachWindow(ctx, c, harness, state, to, body) },
		Arm: func(ctx context.Context) error {
			// The log read is capped at its oldest entries. Arm at the tail
			// so a fresh pong past that cap is still in the window.
			c, err := b.LogCursor(ctx)
			if err != nil {
				return err
			}
			cursor = c
			return nil
		},
		Proof: func(ctx context.Context, nonce string) (string, bool, error) {
			return reachProof(ctx, b, &cursor, to, nonce)
		},
		Tell: func(ctx context.Context, line string) error {
			_, err := b.Send(ctx, bus.Message{From: me, To: []string{me}, Subject: "reach failed", Body: line + "\n"})
			return err
		},
		Now:   w.now,
		Sleep: w.sleep,
		Nonce: w.random,
	}
	return climb(ctx, fx, to, timeout, reachSteps(from))
}

// reachState is the friend's state directory. --as is the coordinator, so the
// shared stateDir (which reads --as) is the wrong name here.
func (w world) reachState(c *tool.Call, friendName string) string {
	if s := c.Str("state-dir"); s != "" {
		return s
	}
	return friend.FindStateDir(w.home, c.Str("dir"), friendName)
}

func (w world) reachPush(ctx context.Context, c *tool.Call, harness, friendName, state, body string) error {
	if harness == "" {
		return fmt.Errorf("--harness is required")
	}
	session := c.Str("session")
	if harness == "tmux" && session == "" {
		session = friend.TmuxSession(friendName)
	}
	d, err := friend.NewDeliverer(harness, c.Str("dir"), session, w.exec, nil)
	if err != nil {
		return err
	}
	if err := friend.TmuxFor(d, friendName, state); err != nil {
		return err
	}
	if t, ok := d.(*friend.Tmux); ok {
		t.Now = w.now
		t.Sleep = w.reachSleep
		if t.Session == "" {
			t.Session = friend.TmuxSession(friendName)
		}
	}
	_, err = d.Deliver(ctx, body)
	return err
}

func (w world) reachWindow(ctx context.Context, c *tool.Call, harness, state, friendName, body string) error {
	if harness == "tmux" {
		return w.reachTmux(ctx, c.Str("dir"), c.Str("session"), friendName, state, body)
	}
	if harness == "" {
		if _, found, err := friend.ReadHost(state); err != nil {
			return err
		} else if found {
			return w.reachTmux(ctx, c.Str("dir"), c.Str("session"), friendName, state, body)
		}
		return fmt.Errorf("--harness is required")
	}
	bundle := friend.AppBundles[harness]
	if bundle == "" {
		return fmt.Errorf("%s has no window", harness)
	}
	return (friend.GUIWindow{Bundle: bundle, Run: w.exec, Permitted: w.reachPermitted}).Deliver(ctx, body)
}

func (w world) reachTmux(ctx context.Context, dir, session, friendName, state, body string) error {
	h, found, err := friend.ReadHost(state)
	if err != nil {
		return err
	}
	if session == "" {
		session = friend.TmuxSession(friendName)
		if found && h.Session != "" {
			session = h.Session
		}
	}
	t := &friend.Tmux{Dir: dir, Session: session, Run: w.exec, Now: w.now, Sleep: w.reachSleep}
	if found && h.Prompt != "" {
		t.Prompt, err = regexp.Compile(h.Prompt)
		if err != nil {
			return fmt.Errorf("%s: the saved prompt %q is no regular expression: %w", friend.HostFile, h.Prompt, err)
		}
	}
	_, err = t.Deliver(ctx, body)
	return err
}

// reachBudget keeps the delivery's injected clock inside the step bound, as
// Reach.tla's EveryStepBounded requires. The context deadline also bounds I/O.
type reachBudgetKey struct{}
type reachBudget struct {
	until  time.Time
	cancel context.CancelFunc
}

func (w world) reachSleep(ctx context.Context, d time.Duration) {
	budget, bounded := ctx.Value(reachBudgetKey{}).(reachBudget)
	if bounded {
		left := budget.until.Sub(w.now())
		if left <= 0 {
			budget.cancel()
			return
		}
		if d > left {
			d = left
		}
	}
	w.sleep(ctx, d)
	if bounded && !w.now().Before(budget.until) {
		budget.cancel()
	}
}

// climb walks the ladder (tla/Reach.tla: Tick, Leave). A proof stops it. A
// window that lacks the accessibility permission refuses and says nothing on
// the bus. No proof after the last step is one failed note, on the
// coordinator's own stream.
func climb(ctx context.Context, fx reachFX, friendName string, timeout time.Duration, steps []string) *tool.Out {
	acc := tool.Done()
	var tried []string
	for _, step := range steps {
		start := fx.Now()
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		stepCtx = context.WithValue(stepCtx, reachBudgetKey{}, reachBudget{until: start.Add(timeout), cancel: cancel})
		if err := fx.Arm(stepCtx); err != nil {
			return reachWith(tool.Refuse("the store did not answer: "+err.Error()), acc)
		}
		nonce := fx.Nonce()
		body := ReachText(step, nonce, friendName, fx.coordinator)
		switch step {
		case reachBus:
			id, err := fx.Send(stepCtx, nonce, body)
			if err != nil {
				return reachWith(answer(err), acc)
			}
			acc.Item("STEP", "step", step, "sent", id, "nonce", nonce)
		case reachPush:
			up, why := fx.DaemonUp()
			if !up {
				tried = append(tried, step)
				acc.ItemText("NONE", "daemon down: "+why, "step", step, "waited", "0s")
				continue
			}
			acc.Item("STEP", "step", step, "nonce", nonce)
			if err := fx.Push(stepCtx, body); err != nil {
				tried = append(tried, step)
				acc.ItemText("NONE", err.Error(), "step", step, "waited", fx.Now().Sub(start).String())
				continue
			}
		case reachWindow:
			acc.Item("STEP", "step", step, "nonce", nonce)
			if err := fx.Window(stepCtx, body); err != nil {
				var refused friend.WindowRefused
				if errors.As(err, &refused) {
					o := tool.Refuse("accessibility permission absent")
					o.Remedy = refused.Remedy
					return reachWith(o, acc)
				}
				tried = append(tried, step)
				acc.ItemText("NONE", err.Error(), "step", step, "waited", fx.Now().Sub(start).String())
				continue
			}
		}
		ok, err := waitReach(stepCtx, fx, acc, step, nonce, start, timeout)
		if err != nil {
			return reachWith(tool.Refuse(err.Error()), acc)
		}
		if ok {
			return reachWith(tool.Done().Fact("friend", friendName).Fact("step", step), acc)
		}
		tried = append(tried, step)
	}
	line := fmt.Sprintf("REACH FAILED friend=%s tried=%s", friendName, strings.Join(tried, ","))
	if err := fx.Tell(ctx, line); err != nil {
		return reachWith(answer(err), acc)
	}
	return reachWith(tool.Fail().Fact("friend", friendName).Fact("tried", strings.Join(tried, ",")), acc)
}

func waitReach(ctx context.Context, fx reachFX, acc *tool.Out, step, nonce string, start time.Time, timeout time.Duration) (bool, error) {
	for {
		elapsed := fx.Now().Sub(start)
		if ctx.Err() != nil && (elapsed >= timeout || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			acc.Item("NONE", "step", step, "waited", elapsed.String())
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		by, ok, err := fx.Proof(ctx, nonce)
		if err != nil {
			return false, err
		}
		if ok {
			acc.Item("PROOF", "step", step, "after", fx.Now().Sub(start).String(), "by", by)
			return true, nil
		}
		elapsed = fx.Now().Sub(start)
		if elapsed >= timeout {
			acc.Item("NONE", "step", step, "waited", elapsed.String())
			return false, nil
		}
		d := reachPoll
		if remain := timeout - elapsed; remain < d {
			d = remain
		}
		fx.Sleep(ctx, d)
	}
}

func reachWith(res, acc *tool.Out) *tool.Out {
	res.Items = append(append([]tool.Item{}, acc.Items...), res.Items...)
	res.Notes = append(append([]string{}, acc.Notes...), res.Notes...)
	return res
}

// reachProof is a proof from the friend since Arm: a pong for nonce, or any
// other message. A daemon-pong, a ping, and a pong for another nonce are none.
// cursor is the log tail Arm captured; each poll reads forward from it and
// advances it, so a log longer than the oldest-window cap still yields a
// fresh pong.
func reachProof(ctx context.Context, b *bus.Bus, cursor *string, friendName, nonce string) (string, bool, error) {
	for {
		es, next, more, err := b.LogForward(ctx, *cursor)
		if err != nil {
			return "", false, err
		}
		*cursor = next
		for _, e := range es {
			m := e.Message()
			if m.From != friendName {
				continue
			}
			subject := strings.ToLower(strings.TrimSpace(m.Subject))
			if subject == strings.ToLower(friend.DaemonPongSubject) || strings.HasPrefix(subject, strings.ToLower(friend.PingPrefix)) || strings.HasPrefix(subject, "ping") {
				continue
			}
			if n, _, _, _, ok := friend.ParsePong(strings.TrimSpace(m.Body)); ok {
				if n == nonce {
					return "pong", true, nil
				}
				continue
			}
			if subject == strings.ToLower(friend.PongSubject) {
				continue // malformed pongs cannot become ordinary-message proof
			}
			return "message", true, nil
		}
		if !more {
			return "", false, nil
		}
	}
}
