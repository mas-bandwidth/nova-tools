package main

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// reachHelp is the verb's help, which docs/CLI.md carries word for word.
const reachHelp = `The ladder that gets a silent friend's attention, each step more direct than the last. Every step
carries one message from --as, REACH <nonce>: ... with the exact command that answers it (nova-friend
pong --as <friend> --nonce <nonce> --to <coordinator>), and waits up to --step-timeout (default 60s)
for proof: a bus message from the friend's own stream after the ladder began, the pong carrying the
nonce or any real message (the daemon's daemon-pong, keepalive, pings and session checks never count).
  1. bus: the message on the friend's stream;
  2. push: the message again, for the friend's daemon to push into the session as a turn (never a
     PING, which is the daemon's own); skipped with the reason when the daemon is not up by its status
     file in --state-dir (written under 30s ago);
  3. window: the friend's own window: --tmux <pane>, the message typed into the TUI's pane by tmux
     send-keys and submitted with Enter; --app <bundle id>, a GUI harness on macOS, the app found by its
     bundle, brought to the front, the message typed into its composer and submitted, through the
     accessibility API, which needs the permission a person grants to the program that runs
     nova-friend (System Settings > Privacy & Security > Accessibility): without it the verb is
     refused with that remedy, and never asks for it; skipped with the reason when neither is given.
--from starts the ladder at a later step. Each step prints its lines before the next starts:
REACH STEP step=<bus|push|window> sent=<bus id|tmux:<pane>|app:<bundle id>> nonce=<nonce>
REACH SKIP step=<push|window> reason=<why>
REACH PROOF step=<step> after=<seconds>s by=<pong|message>   (the ladder stops: no step after a proof)
REACH NONE step=<step> waited=<step timeout>   (no proof in time: the next step)
then the last line:
REACH OK friend=<friend> step=<step>   exit 0
REACH FAILED friend=<friend> tried=<steps|-> skipped=<steps|-> id=<bus id>   exit 1, after every step
  was tried or skipped; one message, subject REACH FAILED <friend>, is sent to your own stream.
--json prints one object instead: result{verb, status, exit, remedy, why}, facts{friend, step} (OK) or
facts{friend, tried, skipped, id} (FAILED), and items[] of kind step{step, sent, nonce},
skip{step, reason}, proof{step, after, by} and none{step, waited}, in order.
--dry-run opens no store and sends nothing: REACH OK friend= nonce= dry_run=true and one
REACH PLAN step=<step>: <what it would do> per step (the push step reads the daemon's status file to
say whether it would be skipped), then a NOTE with the message.
see also: nova-friend ping --wake (one wake check), nova-friend check (is each friend's row true).
example: nova-friend reach --as ada --to bob --tmux bob:0 --step-timeout 30s`

// reachExits is the verb's exit table.
const reachExits = "0 the session answered (REACH OK), 1 no step was answered (REACH FAILED), 2 could not run (a flag, a store that did not answer, a window that could not be typed into, the accessibility permission not granted)."

// appWindow types text into a GUI harness's composer and submits it
// (friend.WindowApp; a test's fake).
type appWindow func(ctx context.Context, run friend.Exec, bundle, text string) error

// reachNoWindow is the window step's skip when no window is named.
const reachNoWindow = "no window named: --tmux <pane> for a TUI, --app <bundle id> for a GUI harness"

// reach is the escalation ladder that gets a silent friend's attention
// (docs/SPEC-FRIEND.md, Reach; tla/Reach.tla): friend.Reach is the machine,
// and this binds its effects to the bus, the friend's daemon status file and
// its window.
func (w world) reach(c *tool.Call) *tool.Out {
	me, to, tmux, app := c.Str("as"), c.Str("to"), c.Str("tmux"), c.Str("app")
	steps, _ := friend.ReachFrom(c.Str("from")) // checked with the flags
	state := c.Str("state-dir")
	if state == "" {
		state = friend.DefaultStateDir(w.home, to)
	}
	nonce := w.random()
	text := friend.ReachText(nonce, me, to)
	push := func() (string, error) {
		s, found, err := friend.ReadStatus(state)
		if err != nil {
			return "", err
		}
		return friend.ReachDaemon(s, found, w.now()), nil
	}
	window := func() string {
		switch {
		case tmux != "":
			return "tmux:" + tmux
		case app != "":
			return "app:" + app
		}
		return ""
	}
	o := tool.Done().Fact("friend", to)
	if c.DryRun() {
		// the plan from the same steps; the one read is the daemon's status file
		o.Fact("nonce", nonce)
		for _, step := range steps {
			does := "send " + friend.ReachPrefix + nonce + " to " + to + " on the bus"
			switch step {
			case friend.ReachPush:
				why, err := push()
				if err != nil {
					return tool.Refuse("the daemon's status file cannot be read: " + err.Error())
				}
				does += ", which the friend's daemon pushes into the session as a turn"
				if why != "" {
					does = "skip: " + why
				}
			case friend.ReachWindow:
				does = "type the message into " + window() + " and submit it"
				if window() == "" {
					does = "skip: " + reachNoWindow
				}
			}
			o.ItemText("plan", does, "step", string(step))
		}
		return o.Note("nothing was sent; the message: " + text)
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	_, storeNow, err := b.Store.Roster(c.Ctx)
	if err != nil {
		return answer(err)
	}
	floor := bus.IDAt(storeNow) // proof is what the friend says from here on
	send := func(ctx context.Context) (string, string, error) {
		m, err := b.Send(ctx, bus.Message{From: me, To: []string{to}, Subject: friend.ReachPrefix + nonce, Body: text + "\n"})
		return m.ID, "", err
	}
	fx := friend.ReachEffects{
		Do: func(ctx context.Context, step friend.ReachStep) (string, string, error) {
			switch step {
			case friend.ReachPush:
				why, err := push()
				if err != nil || why != "" {
					return "", why, err
				}
			case friend.ReachWindow:
				switch {
				case tmux != "":
					return window(), "", friend.TmuxSend(ctx, w.exec, tmux, text)
				case app != "":
					return window(), "", w.app(ctx, w.exec, app, text)
				}
				return "", reachNoWindow, nil
			}
			return send(ctx)
		},
		Proof: func(ctx context.Context) (string, bool, error) {
			entries, err := b.Log(ctx, floor)
			by, ok := friend.ReachProof(entries, to, nonce)
			return by, ok, err
		},
		Now:   w.now,
		Sleep: w.sleep,
		Event: func(e friend.ReachEvent) {
			if !c.Bool("json") {
				fmt.Fprintln(c.Stdout, e.Line()) // each step's line before the next starts
				return
			}
			f := e.Fields()
			if e.Kind == friend.ReachEventSkip {
				f[3] = tool.Text(e.Reason)
			}
			o.Item(e.Kind, f...)
		},
	}
	res, err := friend.Reach{Steps: steps, Nonce: nonce, Timeout: c.Dur("step-timeout"), Poll: friend.ReachPoll}.Run(c.Ctx, fx)
	if err != nil {
		r := tool.Refuse("reach could not run: " + err.Error())
		r.Items = o.Items
		return r
	}
	if res.OK {
		return o.Fact("step", string(res.Step))
	}
	tried, skipped := friend.StepNames(res.Tried), friend.StepNames(res.Skipped)
	line := fmt.Sprintf("REACH FAILED friend=%s tried=%s skipped=%s nonce=%s", to, tried, skipped, nonce)
	m, err := b.Send(context.WithoutCancel(c.Ctx), bus.Message{From: me, To: []string{me}, Subject: "REACH FAILED " + to, Body: line + "\n"})
	if err != nil {
		r := tool.Refuse("the ladder failed and the bus did not take its message: " + err.Error())
		r.Items = o.Items
		return r
	}
	f := tool.Fail().Fact("friend", to).Fact("tried", tried).Fact("skipped", skipped).Fact("id", m.ID)
	f.Items = o.Items
	return f
}
