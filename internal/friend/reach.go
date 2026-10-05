package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ReachStep is one rung of the ladder that gets a silent friend's attention,
// each more direct than the last (docs/SPEC-FRIEND.md, Reach; tla/Reach.tla).
type ReachStep string

const (
	ReachBus    ReachStep = "bus"    // a message on the friend's stream
	ReachPush   ReachStep = "push"   // a message the friend's daemon, up, pushes into the session as a turn
	ReachWindow ReachStep = "window" // the friend's own window: a tmux pane or a GUI harness's composer
)

// ReachSteps is the whole ladder, in order.
var ReachSteps = []ReachStep{ReachBus, ReachPush, ReachWindow}

// DefaultStepTimeout is how long one step waits for proof.
const DefaultStepTimeout = time.Minute

// ReachPoll is how often a step reads the bus for proof.
const ReachPoll = time.Second

// ReachPrefix opens the subject of every reach message: REACH <nonce>.
const ReachPrefix = "REACH "

// ReachFrom is the ladder from step on; ok is false when step is no step.
func ReachFrom(step string) (steps []ReachStep, ok bool) {
	for i, s := range ReachSteps {
		if string(s) == step {
			return ReachSteps[i:], true
		}
	}
	return nil, false
}

// The kinds of ReachEvent, one line each.
const (
	ReachEventStep  = "step"  // the step's effect is done: REACH STEP step= sent= nonce=
	ReachEventSkip  = "skip"  // the step cannot be taken here: REACH SKIP step= reason=
	ReachEventProof = "proof" // the session answered: REACH PROOF step= after= by=
	ReachEventNone  = "none"  // no answer within the step's timeout: REACH NONE step= waited=
)

// ReachEvent is one line of the ladder, recorded before the next step starts.
type ReachEvent struct {
	Kind   string
	Step   ReachStep
	Sent   string        // step: what was sent, a bus id or the window's name
	Nonce  string        // step: the nonce the message carries
	Reason string        // skip: why the step was not taken
	After  time.Duration // proof: from the step's effect to the proof
	Waited time.Duration // none: how long the step waited
	By     string        // proof: pong or message
}

// Fields is the event's fields in line order, as key, value pairs.
func (e ReachEvent) Fields() []any {
	switch e.Kind {
	case ReachEventStep:
		return []any{"step", string(e.Step), "sent", e.Sent, "nonce", e.Nonce}
	case ReachEventSkip:
		return []any{"step", string(e.Step), "reason", e.Reason}
	case ReachEventProof:
		return []any{"step", string(e.Step), "after", e.After.Round(time.Second).String(), "by", e.By}
	default:
		return []any{"step", string(e.Step), "waited", e.Waited.String()}
	}
}

// Line is the event as the verb prints it: REACH <KIND> k=v ...
func (e ReachEvent) Line() string {
	f := e.Fields()
	var b strings.Builder
	b.WriteString(ReachPrefix + strings.ToUpper(e.Kind))
	for i := 0; i+1 < len(f); i += 2 {
		v := f[i+1].(string)
		if f[i] == "reason" {
			v = oneline.Quote(v)
		} else {
			v = oneline.Field(v)
		}
		b.WriteString(" " + f[i].(string) + "=" + v)
	}
	return b.String()
}

// ReachEffects is every side effect of the ladder, injected, so the machine
// runs under a fake bus, a fake window and a fake clock.
type ReachEffects struct {
	// Do takes one step: what it sent, or the reason it cannot be taken here
	// (skip), or an error when it could not run.
	Do func(ctx context.Context, step ReachStep) (sent, skip string, err error)
	// Proof reads the bus once: whether the session has answered, and by what.
	Proof func(ctx context.Context) (by string, ok bool, err error)
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration)
	Event func(ReachEvent)
}

// ReachResult is how the ladder ended: OK at Step, or failed with every step
// tried or skipped.
type ReachResult struct {
	OK      bool
	Step    ReachStep
	Tried   []ReachStep
	Skipped []ReachStep
}

// Reach is the ladder over steps (tla/Reach.tla): each step's effect, then a
// wait of at most Timeout for proof read every Poll; proof ends the ladder and
// no step is taken after it (NoStepAfterProof); no proof climbs to the next
// step (EveryStepBounded); it fails only once every step was tried or skipped
// (FailedOnlyAfterAllThree). An error from an effect ends it: it could not run.
type Reach struct {
	Steps   []ReachStep
	Nonce   string
	Timeout time.Duration
	Poll    time.Duration
}

// Run climbs the ladder.
func (r Reach) Run(ctx context.Context, fx ReachEffects) (ReachResult, error) {
	var res ReachResult
	for _, step := range r.Steps {
		sent, skip, err := fx.Do(ctx, step)
		if err != nil {
			return res, fmt.Errorf("step %s: %w", step, err)
		}
		if skip != "" {
			res.Skipped = append(res.Skipped, step)
			fx.Event(ReachEvent{Kind: ReachEventSkip, Step: step, Reason: skip})
			continue
		}
		res.Tried = append(res.Tried, step)
		fx.Event(ReachEvent{Kind: ReachEventStep, Step: step, Sent: sent, Nonce: r.Nonce})
		began := fx.Now()
		for {
			by, ok, err := fx.Proof(ctx)
			if err != nil {
				return res, fmt.Errorf("step %s: reading the bus: %w", step, err)
			}
			waited := fx.Now().Sub(began)
			if ok {
				res.OK, res.Step = true, step
				fx.Event(ReachEvent{Kind: ReachEventProof, Step: step, After: waited, By: by})
				return res, nil
			}
			if waited >= r.Timeout {
				fx.Event(ReachEvent{Kind: ReachEventNone, Step: step, Waited: r.Timeout})
				break
			}
			fx.Sleep(ctx, min(r.Poll, r.Timeout-waited))
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// StepNames is steps joined by commas, "-" for none.
func StepNames(steps []ReachStep) string {
	if len(steps) == 0 {
		return "-"
	}
	s := make([]string, len(steps))
	for i, step := range steps {
		s[i] = string(step)
	}
	return strings.Join(s, ",")
}

// ReachText is the message every step carries from coordinator to friend:
// the nonce, who is asking, and the one command that answers it, the
// friend's own nova-friend pong. It is one line, so a window step submits it
// whole.
func ReachText(nonce, coordinator, friend string) string {
	return ReachPrefix + nonce + ": " + coordinator + " is trying to reach this session. Answer now, before anything else, with one command: nova-friend pong --as " + friend + " --nonce " + nonce + " --to " + coordinator + " (a real message to " + coordinator + " on the bus answers too)"
}

// ReachProof is the session's answer among entries: a pong from friend
// carrying nonce, or a real message from friend; the daemon's own notices
// (ping, pong for another nonce, daemon-pong, keepalive, a session check)
// never count.
func ReachProof(entries []bus.Entry, friend, nonce string) (by string, ok bool) {
	for _, e := range entries {
		m := e.Message()
		if m.From != friend {
			continue
		}
		if n, _, _, _, pong := ParsePong(strings.TrimSpace(m.Body)); pong {
			if n == nonce {
				return "pong", true
			}
			continue
		}
		if IsRealMessage(m.Subject) && !strings.HasPrefix(m.Subject, SessionCheckPrefix) {
			return "message", true
		}
	}
	return "", false
}

// ReachDaemon is why the push step cannot be taken, from the friend's daemon
// status file: "" when the daemon is up (its status under DaemonStale old).
func ReachDaemon(s Status, found bool, now time.Time) string {
	if !found {
		return "the daemon is not up: it has written no status file"
	}
	if age := now.Sub(s.At); age >= DaemonStale {
		return fmt.Sprintf("the daemon is not up: its status file is %s old (up while under %s)", age.Round(time.Second), DaemonStale)
	}
	return ""
}

// ErrNoAccessibility is the window step on a GUI harness when the binary has
// not been granted the accessibility permission; the tool never asks for it.
var ErrNoAccessibility = errors.New("the accessibility permission is not granted to this program; a person grants it in System Settings > Privacy & Security > Accessibility, then runs reach again")

// TmuxSend types text into the tmux pane target and submits it: send-keys of
// the text as literal keys, then Enter.
func TmuxSend(ctx context.Context, run Exec, target, text string) error {
	for _, args := range [][]string{{"send-keys", "-t", target, "-l", text}, {"send-keys", "-t", target, "Enter"}} {
		out, exit, err := run(ctx, "", "tmux", args, "")
		if err != nil {
			return fmt.Errorf("tmux %s: %w", strings.Join(args[:3], " "), err)
		}
		if exit != 0 {
			return fmt.Errorf("tmux %s exited %d: %s", strings.Join(args[:3], " "), exit, oneline.Cap(strings.TrimSpace(out), 200))
		}
	}
	return nil
}

// appScript is the JXA that types text into the composer of the app whose
// bundle id is argv[0] and submits it: it checks the accessibility permission
// without prompting (NOT-TRUSTED), finds the app by its bundle
// (NOT-RUNNING), brings it to the front, types and presses return.
const appScript = `ObjC.import('ApplicationServices');
function run(argv) {
  if (!$.AXIsProcessTrusted()) { return 'NOT-TRUSTED'; }
  var se = Application('System Events');
  if (se.applicationProcesses.whose({bundleIdentifier: argv[0]}).length === 0) { return 'NOT-RUNNING'; }
  Application(argv[0]).activate();
  delay(0.5);
  se.keystroke(argv[1]);
  se.keyCode(36);
  return 'OK';
}`

// appSend runs appScript through osascript and reads its one word.
func appSend(ctx context.Context, run Exec, bundle, text string) error {
	out, exit, err := run(ctx, "", "osascript", []string{"-l", "JavaScript", "-e", appScript, bundle, text}, "")
	if err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	switch got := strings.TrimSpace(out); {
	case exit == 0 && got == "OK":
		return nil
	case got == "NOT-TRUSTED":
		return ErrNoAccessibility
	case got == "NOT-RUNNING":
		return fmt.Errorf("no running app has the bundle id %s", bundle)
	default:
		return fmt.Errorf("osascript exited %d: %s", exit, oneline.Cap(got, 200))
	}
}
