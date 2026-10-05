package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The ladder's steps, in the order it climbs (docs/SPEC-FRIEND.md, Reach;
// tla/Reach.tla). ok and failed are the terminals. A step after a proof is
// not a state this function enters.
const (
	reachBus    = "bus"
	reachPush   = "push"
	reachWindow = "window"
)

// reachPoll is how often a step reads the bus for proof. Tests inject a
// sleep that moves a fake clock, so the wait takes no real time.
const reachPoll = time.Second

// reachSteps is the ladder. --from names the one to start at.
var reachSteps = []string{reachBus, reachPush, reachWindow}

// withReachFriend rewrites `reach --as <coordinator> <friend>` into
// `--friend <friend>`. The skeleton accepts a positional argument only on
// the default verb, and reach is not it; the usage line still shows the
// argument, and this is the only place that moves it. -h is left alone.
func withReachFriend(args []string) []string {
	if len(args) == 0 || args[0] != "reach" {
		return args
	}
	for _, a := range args[1:] {
		if a == "-h" || a == "--help" || a == "-help" {
			return args
		}
	}
	takes := map[string]bool{"as": true, "friend": true, "step-timeout": true, "from": true, "redis": true, "state-dir": true}
	out := []string{"reach"}
	var positional string
	friendSet := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				if name[:eq] == "friend" && strings.TrimSpace(name[eq+1:]) != "" {
					friendSet = true
				}
				out = append(out, a)
				continue
			}
			out = append(out, a)
			if takes[name] && i+1 < len(args) {
				i++
				out = append(out, args[i])
				if name == "friend" && strings.TrimSpace(args[i]) != "" {
					friendSet = true
				}
			}
			continue
		}
		if positional == "" {
			positional = a
			continue
		}
		out = append(out, a) // a second argument stays, so the verb refuses it
	}
	if positional != "" && !friendSet {
		out = append(out, "--friend", positional)
	} else if positional != "" {
		out = append(out, positional)
	}
	return out
}

// reachIn is one climb: who, how long a step waits, and where it starts.
type reachIn struct {
	coordinator string
	friend      string
	timeout     time.Duration
	from        string
	dry         bool
}

// reachFX is every side effect the ladder takes. Tests pass fakes; the verb
// passes the bus, the deliver adapter and the window. The machine itself
// only moves (step, proof, clock).
type reachFX struct {
	now      func() time.Time
	sleep    func(context.Context, time.Duration)
	nonce    func() string
	send     func(ctx context.Context, nonce, body string) (id string, err error)
	push     func(ctx context.Context, nonce, body string) error
	window   func(ctx context.Context, nonce, body string) error
	daemonUp func(ctx context.Context) (up bool, reason string)
	floor    func(ctx context.Context) (string, error)
	proof    func(ctx context.Context, nonce, after string) (by string, err error)
	failed   func(ctx context.Context, line string) error
	onStep   func(step, nonce string) // a test plants proof after the side effect
	poll     time.Duration
}

type reachLine struct {
	kind string
	kv   []any
}

// reachResult is the climb: the lines in order, the terminal, and the steps
// that ran. status 2 is a refusal (err), and nothing was said on the bus for it.
type reachResult struct {
	status int
	step   string
	tried  []string
	lines  []reachLine
	err    error
}

func (r reachResult) line(kind string, kv ...any) reachResult {
	r.lines = append(r.lines, reachLine{kind, kv})
	return r
}

// reachText is the message a step carries: the nonce and the exact pong line,
// which is a real message, never a PING (the daemon's own).
func reachText(coordinator, friendName, nonce string) string {
	pong := fmt.Sprintf("nova-friend pong --as %s --nonce %s --to %s", friendName, nonce, coordinator)
	return fmt.Sprintf("REACH nonce=%s\nAnswer before anything else, with one command, then end the turn: %s\n", nonce, pong)
}

// climb is the ladder over (step, proof, clock). It stops at the first proof.
// A step waits at most in.timeout. failed is only after every step from
// `from` through window has run or been skipped; the bus is told once.
// tla/Reach.tla is this machine with from=bus and no skip: no step after a
// proof, every step bounded, failed only after all three.
func climb(ctx context.Context, in reachIn, fx reachFX) reachResult {
	if fx.poll <= 0 {
		fx.poll = reachPoll
	}
	var res reachResult
	start := 0
	for i, s := range reachSteps {
		if s == in.from {
			start = i
		}
	}
	for _, step := range reachSteps[start:] {
		if err := ctx.Err(); err != nil {
			res.status, res.err = 2, err
			return res
		}
		nonce := fx.nonce()
		body := reachText(in.coordinator, in.friend, nonce)
		if in.dry {
			res = res.line("step", "step", step, "sent", "-", "nonce", nonce)
			continue
		}
		if step == reachPush {
			up, reason := fx.daemonUp(ctx)
			if !up {
				if reason == "" {
					reason = "daemon-down"
				}
				res = res.line("skip", "step", step, "reason", reason)
				continue
			}
		}
		var sent string
		var act error
		switch step {
		case reachBus:
			sent, act = fx.send(ctx, nonce, body)
		case reachPush:
			act = fx.push(ctx, nonce, body)
		default:
			act = fx.window(ctx, nonce, body)
		}
		if act != nil {
			if errors.Is(act, friend.ErrNoAccessibility) {
				res.status, res.err = 2, act
				return res
			}
			var skip friend.WindowSkip
			if errors.As(act, &skip) {
				res = res.line("skip", "step", step, "reason", skip.Reason)
				continue
			}
			res.status, res.err = 2, act
			return res
		}
		if sent == "" {
			sent = "-"
		}
		res = res.line("step", "step", step, "sent", sent, "nonce", nonce)
		res.tried = append(res.tried, step)
		floor := "-"
		if fx.floor != nil {
			f, err := fx.floor(ctx)
			if err != nil {
				res.status, res.err = 2, err
				return res
			}
			if f != "" {
				floor = f
			}
		}
		if fx.onStep != nil {
			fx.onStep(step, nonce)
		}
		by, waited, err := waitProof(ctx, fx, nonce, floor, in.timeout)
		if err != nil {
			res.status, res.err = 2, err
			return res
		}
		if by != "" {
			res = res.line("proof", "step", step, "after", int(waited/time.Second), "by", by)
			res.status, res.step = 0, step
			return res
		}
		res = res.line("none", "step", step, "waited", in.timeout.String())
	}
	if in.dry {
		res.status = 0
		return res
	}
	tried := strings.Join(res.tried, ",")
	if tried == "" {
		tried = "-"
	}
	line := fmt.Sprintf("REACH FAILED friend=%s tried=%s", in.friend, tried)
	if err := fx.failed(ctx, line); err != nil {
		res.status, res.err = 2, err
		return res
	}
	res.status = 1
	return res
}

// waitProof reads until proof or the step's bound. The clock is fx.now and
// fx.sleep, never time.Now and never a real sleep the caller did not inject.
func waitProof(ctx context.Context, fx reachFX, nonce, floor string, timeout time.Duration) (by string, waited time.Duration, err error) {
	start := fx.now()
	for {
		if err = ctx.Err(); err != nil {
			return "", fx.now().Sub(start), err
		}
		by, err = fx.proof(ctx, nonce, floor)
		waited = fx.now().Sub(start)
		if err != nil || by != "" {
			return by, waited, err
		}
		if waited >= timeout {
			return "", timeout, nil
		}
		fx.sleep(ctx, fx.poll)
	}
}

func (w world) reach(c *tool.Call) *tool.Out {
	in := reachIn{
		coordinator: c.Str("as"),
		friend:      c.Str("friend"),
		timeout:     c.Dur("step-timeout"),
		from:        c.Str("from"),
		dry:         c.DryRun(),
	}
	if in.from == "" {
		in.from = reachBus
	}
	var b *bus.Bus
	if !in.dry {
		opened, closeStore, refused := w.bus(c)
		if refused != nil {
			return refused
		}
		defer closeStore()
		b = opened
	}
	fx := w.reachFX(c, b, in)
	res := climb(c.Ctx, in, fx)
	o := reachOut(in, res)
	return o
}

func reachOut(in reachIn, res reachResult) *tool.Out {
	if res.err != nil {
		o := tool.Refuse(res.err.Error())
		if errors.Is(res.err, friend.ErrNoAccessibility) {
			o.Remedy = "grant Accessibility to this binary in System Settings > Privacy & Security > Accessibility"
		}
		return o
	}
	var o *tool.Out
	switch res.status {
	case 0:
		step := res.step
		if step == "" {
			step = "-"
		}
		o = tool.Done().Fact("friend", in.friend).Fact("step", step)
	default:
		tried := strings.Join(res.tried, ",")
		if tried == "" {
			tried = "-"
		}
		o = tool.Fail().Fact("friend", in.friend).Fact("tried", tried)
	}
	for _, l := range res.lines {
		o.Item(l.kind, l.kv...)
	}
	return o
}

func (w world) reachFX(c *tool.Call, b *bus.Bus, in reachIn) reachFX {
	name := in.friend
	return reachFX{
		now:   w.now,
		sleep: w.sleep,
		nonce: w.random,
		poll:  reachPoll,
		onStep: func(step, nonce string) {
			if w.reachOnStep != nil {
				w.reachOnStep(step, nonce)
			}
		},
		send: func(ctx context.Context, nonce, body string) (string, error) {
			m, err := b.Send(ctx, bus.Message{
				From: in.coordinator, To: []string{name}, Subject: "REACH " + nonce, Body: body,
			})
			if err != nil {
				return "", err
			}
			return m.ID, nil
		},
		push: func(ctx context.Context, nonce, body string) error {
			if w.reachPush != nil {
				return w.reachPush(ctx, name, body)
			}
			return w.pushTurn(ctx, c, name, body)
		},
		window: func(ctx context.Context, _, body string) error {
			if w.reachWindow != nil {
				return w.reachWindow(ctx, name, body)
			}
			return w.reachFriendWindow(ctx, c, name, body)
		},
		daemonUp: func(context.Context) (bool, string) {
			if w.reachDaemonUp != nil {
				return w.reachDaemonUp(name)
			}
			return w.daemonUp(c, name)
		},
		floor: func(ctx context.Context) (string, error) {
			if b == nil {
				return "", nil
			}
			es, err := b.Log(ctx, "-")
			if err != nil || len(es) == 0 {
				return "", err
			}
			return es[len(es)-1].Entry, nil
		},
		proof: func(ctx context.Context, nonce, after string) (string, error) {
			if b == nil {
				return "", nil
			}
			from := "-"
			if after != "" && after != "-" {
				from = "(" + after
			}
			es, err := b.Log(ctx, from)
			if err != nil {
				return "", err
			}
			for _, e := range es {
				if by := proofBy(e.Message(), name, nonce); by != "" {
					return by, nil
				}
			}
			return "", nil
		},
		failed: func(ctx context.Context, line string) error {
			_, err := b.Send(ctx, bus.Message{
				From: in.coordinator, To: []string{in.coordinator}, Subject: "REACH FAILED", Body: line + "\n",
			})
			return err
		},
	}
}

// proofBy is the step's proof: a pong for its nonce, or any other real
// message, from the friend. The daemon's own lines are not the session.
func proofBy(m bus.Message, friendName, nonce string) string {
	if m.From != friendName {
		return ""
	}
	if n, _, _, _, ok := friend.ParsePong(strings.TrimSpace(m.Body)); ok && n == nonce {
		return "pong"
	}
	sub := strings.ToLower(strings.TrimSpace(m.Subject))
	body := strings.TrimSpace(m.Body)
	switch {
	case sub == "pong" || strings.HasPrefix(sub, "pong "):
		return ""
	case sub == "daemon-pong" || strings.HasPrefix(sub, "daemon-pong"):
		return ""
	case sub == "ping" || strings.HasPrefix(sub, "ping ") || strings.HasPrefix(sub, "ping"):
		return ""
	case sub == "keepalive" || strings.HasPrefix(sub, "keepalive"):
		return ""
	case strings.HasPrefix(body, "PING ") || strings.HasPrefix(body, "daemon-pong"):
		return ""
	}
	return "message"
}

// daemonUp is the push step's guard: the daemon's status file is fresh.
// Presence of the session is not it; a stale or missing file is daemon-down
// and the step is skipped (docs/SPEC-FRIEND.md, Reach).
func (w world) daemonUp(c *tool.Call, name string) (bool, string) {
	st, found, err := friend.ReadStatus(w.friendState(c, name))
	if err != nil {
		return false, "daemon-down"
	}
	if !found || w.now().Sub(st.At) > friend.DaemonStale {
		return false, "daemon-down"
	}
	return true, ""
}

func (w world) friendState(c *tool.Call, name string) string {
	if s := c.Str("state-dir"); s != "" {
		return s
	}
	return friend.DefaultStateDir(w.home, name)
}

// pushTurn is the push step when no test seam is set: the harness's deliver
// command, the same one the daemon uses, and never a PING.
func (w world) pushTurn(ctx context.Context, c *tool.Call, name, text string) error {
	if strings.HasPrefix(strings.TrimSpace(text), "PING ") {
		return errors.New("push refuses a PING; that line is the daemon's own")
	}
	st, found, err := friend.ReadStatus(w.friendState(c, name))
	if err != nil {
		return err
	}
	if !found || st.Harness == "" {
		return fmt.Errorf("no harness in %s; the push step needs the daemon's status", w.friendState(c, name))
	}
	dir := agentDir(w.home, name)
	if dir == "" {
		return fmt.Errorf("no working directory for %s; the agent's plist has no --dir", name)
	}
	d, err := friend.NewDeliverer(st.Harness, dir, st.SessionID, w.exec, nil)
	if err != nil {
		return err
	}
	exit, err := d.Deliver(ctx, text)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("the deliver command exited %d", exit)
	}
	return nil
}

func (w world) reachFriendWindow(ctx context.Context, c *tool.Call, name, text string) error {
	st, _, _ := friend.ReadStatus(w.friendState(c, name)) // ignored: no status means a TUI with no harness name, and FindWindow says so
	target, err := friend.FindWindow(ctx, w.exec, st.Harness, name)
	if err != nil {
		return err
	}
	return friend.SubmitWindow(ctx, w.exec, target, text)
}

// agentDir is the --dir the friend's launchd agent was installed with.
func agentDir(home, name string) string {
	b, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.nova.friend-"+name+".plist"))
	if err != nil {
		return ""
	}
	var args []string
	rest := string(b)
	for {
		i := strings.Index(rest, "<string>")
		if i < 0 {
			break
		}
		rest = rest[i+len("<string>"):]
		j := strings.Index(rest, "</string>")
		if j < 0 {
			break
		}
		args = append(args, rest[:j])
		rest = rest[j+len("</string>"):]
	}
	for i, a := range args {
		if a == "--dir" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
