package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession is a friend's session behind a harness: when a session check
// reaches it, it runs the pong line the check carries, as the pong verb does
// (the pong file in the state directory, then the note on the bus). Each
// nonce is acted on once. Lost drops the note: the line ran, nothing reached
// the bus. Deaf never acts.
type fakeSession struct {
	t           *testing.T
	store       bus.Store
	state       string
	acted       map[string]bool
	lost, deaf  bool
	poll        func() // what the session reads each poll interval (a wake file), nil reads nothing
	deliverText string // the last text an adapter handed in
}

// act reads text for a session check and runs the one pong line it carries.
func (s *fakeSession) act(text string) {
	s.deliverText = text
	if s.deaf || !strings.Contains(text, SessionCheckPrefix) {
		return
	}
	_, line, ok := strings.Cut(text, "then end this turn: ")
	require.True(s.t, ok, "the check carries its one line: %q", text)
	line, _, _ = strings.Cut(line, "\n")
	line, _, _ = strings.Cut(line, " ⏎")
	args := strings.Fields(line)
	require.Equal(s.t, "pong", args[1], "the line runs the pong verb: %q", line)
	flag := func(name string) string {
		i := slices.Index(args, "--"+name)
		require.True(s.t, i > 0 && i+1 < len(args), "the pong line names --%s: %q", name, line)
		return args[i+1]
	}
	as, nonce, to := flag("as"), flag("nonce"), flag("to")
	require.Equal(s.t, s.state, flag("state-dir"))
	if s.acted[nonce] {
		return
	}
	s.acted[nonce] = true
	require.NoError(s.t, WritePong(s.state, Pong{Nonce: nonce, To: to}))
	if s.lost {
		return
	}
	_, err := (&bus.Bus{Store: s.store}).Send(context.Background(), bus.Message{From: as, To: []string{to}, Subject: PongSubject, Body: PongLine(nonce, 0, 0, 0) + "\n"})
	require.NoError(s.t, err)
}

// conformanceRig is one harness's adapter over a fake Exec (and, where the
// harness reads files, a temporary home), the session behind it, bus's Fake
// for the store and a clock moved by the check's own waits: no socket, no
// real time.
type conformanceRig struct {
	session *fakeSession
	deliver Deliverer
	check   *Conformance
	now     time.Time
}

// exec is the Exec seam with answer standing in for the harness's commands.
func (s *fakeSession) exec(answer func(name string, args []string, stdin string) (string, int, error)) Exec {
	return func(_ context.Context, _, name string, args []string, stdin string) (string, int, error) {
		return answer(name, args, stdin)
	}
}

// harnessRigs are the adapters with a deliver command, each over the Exec
// seam: the command the adapter runs is answered by the session acting on
// the text it carries.
var harnessRigs = map[string]func(t *testing.T, s *fakeSession, dir string) Deliverer{
	"opencode": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		return &OpenCode{Dir: dir, Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
			if args[0] == "session" {
				return fmt.Sprintf(`[{"id":"ses_1","directory":%q,"updated":5}]`, dir), 0, nil
			}
			require.Equal(t, []string{"run", "--session", "ses_1"}, args[:3])
			s.act(args[3])
			return "done\n", 0, nil
		})}
	},
	"codex": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		return &Codex{Dir: dir, Session: "thr_1", Home: t.TempDir(), Held: func(string) bool { return false },
			Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
				require.Equal(t, ResumeArgs("thr_1", args[len(args)-1]), args)
				s.act(args[len(args)-1])
				return "done\n", 0, nil
			})}
	},
	"gemini": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		return &Gemini{Dir: dir, Session: "g1", Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
			text, ok := strings.CutPrefix(args[len(args)-1], "--prompt=")
			require.True(t, ok)
			s.act(text)
			return "done\n", 0, nil
		})}
	},
	"dsh": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		return &DSH{Dir: dir, Session: "session-1", Program: "dsh", Run: s.exec(func(name string, args []string, stdin string) (string, int, error) {
			require.Equal(t, DSHArgs("session-1"), args)
			s.act(stdin)
			return "done\n", 0, nil
		})}
	},
	"grok": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		home := t.TempDir()
		wake := filepath.Join(home, "bob.wake")
		require.NoError(t, os.WriteFile(wake, nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"), fmt.Appendf(nil, `[{"pid":100,"cwd":%q}]`, dir), 0o644))
		// the window's monitor: each new line of the wake file is a turn in the session
		s.poll = func() {
			raw, err := os.ReadFile(wake)
			require.NoError(t, err)
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				if line != "" {
					s.act(line)
				}
			}
		}
		return &Grok{Dir: dir, Home: home, Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
			require.Equal(t, "ps", name)
			return "100 1 grok\n200 100 tail -n 0 -F " + wake + "\n", 0, nil
		})}
	},
	"claude": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		// the session's wait on the wake file: each new line wakes the session for a turn
		s.poll = func() {
			raw, err := os.ReadFile(ClaudeWakePath(dir, "bob"))
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			require.NoError(t, err)
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				if line != "" {
					s.act(line)
				}
			}
		}
		return &ClaudeWake{Dir: dir, Name: "bob", Now: func() time.Time { return t0 }}
	},
	"tmux": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		// the pane shows its prompt until a line is typed and entered; the typed line is a turn of the session
		var typed string
		turn := false
		return &Tmux{Dir: dir, Session: "friend-bob", Prompt: PromptPattern(`^>$`),
			Sleep: func(context.Context, time.Duration) {},
			Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
				require.Equal(t, "tmux", name)
				switch args[0] {
				case "capture-pane":
					if turn {
						return "working\n", 0, nil
					}
					return "output\n>\n", 0, nil
				case "send-keys":
					if args[len(args)-1] == "Enter" {
						turn = true
						s.act(strings.ReplaceAll(typed, " ⏎ ", "\n"))
					} else {
						typed = args[len(args)-1]
					}
				}
				return "", 0, nil
			})}
	},
	"antigravity": func(t *testing.T, s *fakeSession, dir string) Deliverer {
		mailbox := ".gemini/antigravity/brain/conv1/.system_generated/messages"
		home := fstest.MapFS{mailbox: &fstest.MapFile{Mode: fs.ModeDir}}
		return &Antigravity{Dir: dir, Session: "conv1", Home: "/h", User: "bob", FS: home,
			Wait: func(context.Context) bool { return true },
			Run: s.exec(func(name string, args []string, _ string) (string, int, error) {
				switch name {
				case "ps":
					return "bob 300 /app/language_server antigravity --csrf_token tok\n", 0, nil
				case "lsof":
					return "p300\nn127.0.0.1:4242\n", 0, nil
				}
				require.Equal(t, "/usr/bin/env", name)
				if slices.Contains(args, "send-message") {
					// the server writes the message into the mailbox; the session takes it and marks it read
					text := args[len(args)-1]
					home[mailbox+"/m1.json"] = &fstest.MapFile{Data: []byte(`{"renderDetails":{"messageTitle":"` + AntigravityTitle + `"}}`)}
					home[mailbox+"/read.json"] = &fstest.MapFile{Data: []byte(`{"m1":true}`)}
					s.act(text)
				}
				return `{"response":{}}`, 0, nil
			})}
	},
}

func newConformanceRig(t *testing.T, harness string) *conformanceRig {
	t.Helper()
	store := bustest.NewFake(t0, "ada", "bob")
	r := &conformanceRig{now: t0, session: &fakeSession{t: t, store: store, state: t.TempDir(), acted: map[string]bool{}}}
	dir := t.TempDir()
	if mk, ok := harnessRigs[harness]; ok {
		r.deliver = mk(t, r.session, dir)
	} else {
		d, err := NewDeliverer(harness, dir, "", nil, nil)
		require.NoError(t, err)
		r.deliver = d
	}
	state := r.session.state
	r.check = &Conformance{
		Friend: "bob", Harness: harness, Deliver: r.deliver, Store: store, Within: DefaultCheckWithin,
		Now:   func() time.Time { return r.now },
		Nonce: func() string { return "c0ffee" },
		Wait: func(context.Context) bool {
			r.now = r.now.Add(CheckPoll)
			if r.session.poll != nil {
				r.session.poll()
			}
			return true
		},
		Text: func(nonce string) string {
			return SessionCheckText(nonce, "nova-friend pong --as bob --nonce "+nonce+" --state-dir "+state+" --redis store.test:6379", "ada")
		},
		Pong: func() (Pong, bool, error) { return ReadPong(state) },
	}
	return r
}

// The one promise every adapter makes (docs/SPEC-FRIEND.md,
// delivery-conformance-r.w1): a session check delivered on the friend's
// stream is acted on by the session, and its pong carrying the nonce is on
// the bus within the window. Every harness in Harnesses runs the same check:
// an adapter with a deliver command passes over its fake Exec; a Stub fails
// at stage=deliver with its surveyed reason, never skipped.
func TestEveryAdapterPassesDeliveryConformance(t *testing.T) {
	t.Parallel()
	for harness := range harnessRigs {
		require.True(t, Known(harness), "a rig for %s, which is no harness", harness)
	}
	assert.Len(t, harnessRigs, 8, "the eight adapters with a deliver command each run the check")
	for _, harness := range Harnesses {
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			d, err := NewDeliverer(harness, t.TempDir(), "", nil, nil)
			require.NoError(t, err)
			if stub, passive := d.(Stub); passive {
				r := newConformanceRig(t, harness)
				got := r.check.Run(context.Background())
				assert.Equal(t, StageDeliver, got.Stage)
				if stub.Reason != "" {
					assert.Contains(t, got.Why, stub.Reason)
				} else {
					assert.Contains(t, got.Why, "no deliver command for "+harness)
				}
				assert.True(t, strings.HasPrefix(got.Line(), "CHECK FAIL harness="+harness+" stage=deliver why="), got.Line())
				return
			}
			_, rigged := harnessRigs[harness]
			require.True(t, rigged, "%s has a deliver command and no conformance rig: add it to harnessRigs", harness)
			r := newConformanceRig(t, harness)
			got := r.check.Run(context.Background())
			require.Empty(t, got.Stage, "%s: %s", harness, got.Line())
			assert.Equal(t, "CHECK OK harness="+harness+" took="+got.Took.String(), got.Line())
			assert.Less(t, got.Took, DefaultCheckWithin)
			assert.Contains(t, r.session.deliverText, SessionCheckPrefix+"c0ffee")
		})
	}
}

// Each stage fails where it should: a delivery the adapter refuses is
// deliver; a session that never runs the line is act; a line that ran but
// whose pong never reached the bus is reply. Neither waits past the window.
func TestDeliveryConformanceNamesTheStageThatFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, stage, why string
		breakIt          func(r *conformanceRig)
	}{
		{"the harness refuses", StageDeliver, "opencode's deliver command exited 1", func(r *conformanceRig) {
			r.check.Deliver = &OpenCode{Dir: "/w", Session: "ses_1", Run: func(context.Context, string, string, []string, string) (string, int, error) { return "", 1, nil }}
		}},
		{"the harness defers", StageDeliver, "deferred: no monitor", func(r *conformanceRig) {
			r.check.Deliver = deferring{}
		}},
		{"the session never acts", StageAct, "no pong c0ffee from bob within 5m0s", func(r *conformanceRig) { r.session.deaf = true }},
		{"the pong never reaches the bus", StageReply, "the pong file holds c0ffee", func(r *conformanceRig) { r.session.lost = true }},
		{"another friend's pong does not count", StageAct, "no pong c0ffee from bob", func(r *conformanceRig) {
			r.session.deaf = true
			_, err := (&bus.Bus{Store: r.check.Store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"ada"}, Subject: PongSubject, Body: PongLine("c0ffee", 0, 0, 0)})
			require.NoError(t, err)
		}},
		{"a pong from before the check does not count", StageAct, "no pong c0ffee from bob", func(r *conformanceRig) {
			r.session.deaf = true
			_, err := (&bus.Bus{Store: r.check.Store}).Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: PongLine("c0ffee", 0, 0, 0)})
			require.NoError(t, err)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newConformanceRig(t, "opencode")
			c.breakIt(r)
			got := r.check.Run(context.Background())
			assert.Equal(t, c.stage, got.Stage, got.Line())
			assert.Contains(t, got.Why, c.why)
			assert.True(t, strings.HasPrefix(got.Line(), "CHECK FAIL harness=opencode stage="+c.stage+" why="), got.Line())
			assert.LessOrEqual(t, r.now.Sub(t0), DefaultCheckWithin+CheckPoll)
		})
	}
}

// The window is a flag: a session that answers after it has failed.
func TestDeliveryConformanceHoldsItsWindow(t *testing.T) {
	t.Parallel()
	r := newConformanceRig(t, "grok")
	poll := r.session.poll
	r.session.poll = func() {
		if r.now.Sub(t0) > 3*time.Minute { // the monitor wakes the session late
			poll()
		}
	}
	r.check.Within = 2 * time.Minute
	got := r.check.Run(context.Background())
	assert.Equal(t, StageAct, got.Stage, got.Line())
	r = newConformanceRig(t, "grok")
	poll2 := r.session.poll
	r.session.poll = func() {
		if r.now.Sub(t0) > 3*time.Minute {
			poll2()
		}
	}
	got = r.check.Run(context.Background())
	assert.Empty(t, got.Stage, got.Line())
	assert.Greater(t, got.Took, 3*time.Minute)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"harness":"grok"`)
}

type deferring struct{}

func (deferring) Deliver(context.Context, string) (int, error) {
	return 0, Deferred{Reason: "no monitor"}
}
