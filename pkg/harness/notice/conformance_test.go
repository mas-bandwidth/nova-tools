package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const as = "ada"

// fakeStore is the hook record and the bus as the Core reads them: no
// socket, no clock but the one a test sets.
type fakeStore struct {
	sum      string
	epoch    int64
	answered map[string]bool
	act      Activity
	unproved int
	clock    time.Duration // how far the store clock has moved from its start
}

func (f *fakeStore) Active(context.Context, string) (string, int64, error) {
	return f.sum, f.epoch, nil
}
func (f *fakeStore) Answered(_ context.Context, _ string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = f.answered[id]
	}
	return out, nil
}
func (f *fakeStore) Activity(context.Context, string) (Activity, error) { return f.act, nil }
func (f *fakeStore) Now(context.Context) (time.Time, error) {
	return time.Date(2026, 10, 14, 23, 0, 0, 0, time.UTC).Add(f.clock), nil
}
func (f *fakeStore) Unprove(context.Context, string, hook.Role, int64) error {
	f.unproved++
	return nil
}

// challenge is a server challenge the store holds as active.
func (f *fakeStore) challenge(nonce string) bus.Message {
	f.sum, f.epoch = hook.Sum(nonce), 1
	return bus.Message{ID: "c-" + nonce, From: "serve", Subject: challengePrefix + nonce, Body: ChallengeText(as, nonce, hook.ChallengeEvery)}
}

// prove is the model's own act: the record drops the challenge, the proof moves.
func (f *fakeStore) prove(nonce string) bool {
	if f.sum != hook.Sum(nonce) {
		return false
	}
	f.sum = ""
	f.act.Proof += "p"
	return true
}

// read is the model reading its group.
func (f *fakeStore) read() { f.act.Group += "r" }

// fakeFiles is the saved notice files by path.
type fakeFiles map[string]Envelope

func (f fakeFiles) Save(path string, data []byte, replace bool) error {
	if _, ok := f[path]; ok != replace {
		return fmt.Errorf("save %s: exists=%v replace=%v", path, ok, replace)
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return err
	}
	f[path] = e
	return nil
}

// harness is one shim over its fake harness: busy and idle are the
// session's own state as the shim's mechanism shows it.
type harness struct {
	shim   Shim
	busy   func(t *testing.T)
	idle   func(t *testing.T) // a turn boundary, where the harness has one
	pushes []string           // receipts, one per native push
}

func ordinary(i int) bus.Message {
	return bus.Message{ID: fmt.Sprintf("m-%02d", i), From: "bob", Subject: "note", Body: "payload-secret"}
}

func shims(t *testing.T) map[string]func(t *testing.T) *harness {
	noop := func(*testing.T) {}
	return map[string]func(t *testing.T) *harness{
		"codex": func(t *testing.T) *harness {
			h := &harness{busy: noop, idle: noop}
			h.shim = &Codex{Socket: "/s", Thread: "t", Directory: "/private/events", Deliver: func(_ context.Context, _, _, id, _ string) (string, error) {
				return "turn-" + id, nil
			}}
			return h
		},
		"dsh": func(t *testing.T) *harness {
			h := &harness{busy: noop, idle: noop}
			h.shim = &Dsh{Route: dshdesktop.Route{EventsDir: "/private/events"}, Deliver: func(_ dshdesktop.Route, id, kind string) (string, error) {
				return kind, nil
			}}
			return h
		},
		"antigravity": func(t *testing.T) *harness {
			h := &harness{busy: noop, idle: noop}
			h.shim = &Antigravity{Directory: "/private/events", Token: func() string { return "csrf-fake-token" }, Deliver: func(_ context.Context, id, _ string) (string, error) {
				return "msg-" + id, nil
			}}
			return h
		},
		"muse": func(t *testing.T) *harness {
			h := &harness{busy: noop, idle: noop}
			dir, err := os.MkdirTemp("", "muse")
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, os.RemoveAll(dir)) })
			h.shim = &Muse{Notices: dir}
			return h
		},
		"grok": func(t *testing.T) *harness {
			home := t.TempDir()
			wake, err := InstallGrok(home)
			require.NoError(t, err)
			script := filepath.Join(home, "hooks", "nova-hook-grok.sh")
			state := filepath.Join(home, "nova-hook")
			run := func(word string) func(t *testing.T) {
				return func(t *testing.T) {
					require.NoError(t, runScript(script, word, state))
				}
			}
			return &harness{shim: &Grok{Home: home, Wake: wake, Notices: "/private/events"}, busy: run("busy"), idle: run("idle")}
		},
		"claude": func(t *testing.T) *harness {
			state := t.TempDir()
			gen := 0
			return &harness{shim: &Claude{State: state, Notices: "/private/events"}, busy: noop, idle: func(t *testing.T) {
				gen++
				require.NoError(t, os.WriteFile(filepath.Join(state, "generation"), []byte(strconv.Itoa(gen)+"\n"), 0o600))
			}}
		},
	}
}

type rig struct {
	t     *testing.T
	store *fakeStore
	files fakeFiles
	h     *harness
	core  *Core
	ids   int
}

func newRig(t *testing.T, make func(t *testing.T) *harness) *rig {
	r := &rig{t: t, store: &fakeStore{answered: map[string]bool{}}, files: fakeFiles{}, h: make(t)}
	r.core = &Core{As: as, Role: hook.Friend, Epoch: 1, Redis: "127.0.0.1:6379", Store: r.store, Shim: r.h.shim, Files: r.files,
		NewID: func() string { r.ids++; return fmt.Sprintf("n%03d", r.ids) },
		Say: func(o *tool.Out) {
			for _, f := range o.Facts {
				if f.K == "receipt" {
					r.h.pushes = append(r.h.pushes, fmt.Sprint(f.V))
				}
			}
		}}
	return r
}

// arrive is one bus read and the tick after it.
func (r *rig) arrive(ms ...bus.Message) {
	require.NoError(r.t, r.core.Offer(context.Background(), ms))
	require.NoError(r.t, r.core.Step(context.Background()))
}

// file is what the model reads when it acts on the pending notice.
func (r *rig) file() Envelope {
	n, ok := r.core.Pending()
	require.True(r.t, ok, "a notice is pending")
	return r.files[n.Path]
}

// act is the model acting on its one notice: it reads the saved file and
// proves the challenge there, then reads its group.
func (r *rig) act() string {
	e := r.file()
	nonce := ""
	if e.Challenge != nil {
		nonce = strings.TrimPrefix(e.Challenge.Subject, challengePrefix)
		r.store.prove(nonce)
	}
	r.store.read()
	require.NoError(r.t, r.core.Step(context.Background()))
	return nonce
}

func TestEveryShimConforms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T, r *rig)
	}{
		{"burst of ten challenges and fifty messages in one busy turn is one push holding the newest nonce", func(t *testing.T, r *rig) {
			r.arrive(r.store.challenge("nonce-01"))
			r.h.busy(t)
			for i := 2; i <= 10; i++ {
				r.arrive(r.store.challenge(fmt.Sprintf("nonce-%02d", i)))
				for j := 0; j < 5; j++ {
					r.arrive(ordinary(i*10 + j))
				}
			}
			for j := 0; j < 5; j++ {
				r.arrive(ordinary(j))
			}
			assert.Len(t, r.h.pushes, 1, "one native push")
			e := r.file()
			require.NotNil(t, e.Challenge)
			assert.Equal(t, challengePrefix+"nonce-10", e.Challenge.Subject)
			assert.Equal(t, 50, e.Messages)
			assert.Equal(t, "nonce-10", r.act(), "the model proves the newest nonce from the one notice")
			assert.Empty(t, r.store.sum)
			assert.Len(t, r.h.pushes, 1, "a proved challenge and a consumed count make no second push")
		}},
		{"a stale challenge after the current one never replaces it", func(t *testing.T, r *rig) {
			stale := r.store.challenge("nonce-old")
			r.arrive(r.store.challenge("nonce-new"))
			r.h.busy(t)
			r.arrive(stale)
			e := r.file()
			require.NotNil(t, e.Challenge)
			assert.Equal(t, challengePrefix+"nonce-new", e.Challenge.Subject)
			assert.Equal(t, 1, e.Messages, "the stale challenge counts as a message")
			assert.Len(t, r.h.pushes, 1)
		}},
		{"an altered body is a message, never a challenge", func(t *testing.T, r *rig) {
			m := r.store.challenge("nonce-x")
			m.Body += "external appendix"
			r.arrive(m)
			e := r.file()
			assert.Nil(t, e.Challenge)
			assert.Equal(t, 1, e.Messages)
		}},
		{"a wrong epoch is a message, never a challenge", func(t *testing.T, r *rig) {
			m := r.store.challenge("nonce-x")
			r.store.epoch = 2
			r.arrive(m)
			assert.Nil(t, r.file().Challenge)
		}},
		{"consumed then new: each notice is consumed once and the next push waits for it", func(t *testing.T, r *rig) {
			r.arrive(ordinary(1))
			r.h.busy(t)
			r.arrive(ordinary(2))
			assert.Len(t, r.h.pushes, 1)
			r.h.idle(t)
			r.act()
			_, pending := r.core.Pending()
			assert.False(t, pending, "consumed")
			require.NoError(t, r.core.Step(context.Background()))
			assert.Len(t, r.h.pushes, 1, "nothing new, nothing pushed")
			r.arrive(ordinary(3))
			assert.Len(t, r.h.pushes, 2, "the next message makes the next push")
			assert.Equal(t, 1, r.file().Messages)
		}},
		{"an idle session is woken by one push", func(t *testing.T, r *rig) {
			r.arrive(ordinary(1))
			require.Len(t, r.h.pushes, 1)
			assert.Equal(t, 1, r.file().Messages)
		}},
		{"a busy turn of five minutes proves every minute's challenge within the minute", func(t *testing.T, r *rig) {
			r.h.busy(t)
			for minute := 1; minute <= 5; minute++ {
				nonce := fmt.Sprintf("minute-%d", minute)
				r.arrive(r.store.challenge(nonce))
				assert.Equal(t, nonce, r.act(), "minute %d", minute)
			}
			assert.Len(t, r.h.pushes, 5, "one push a minute, each after the last was consumed")
		}},
		{"a model restoring its context misses challenges, then proves the newest from its one notice", func(t *testing.T, r *rig) {
			r.arrive(r.store.challenge("lost-1"))
			r.h.busy(t)
			r.arrive(r.store.challenge("lost-2"), ordinary(1))
			r.arrive(r.store.challenge("lost-3"))
			assert.Len(t, r.h.pushes, 1)
			assert.Equal(t, "lost-3", r.act())
		}},
		{"a message that arrives with the consumption is told by the next push", func(t *testing.T, r *rig) {
			r.arrive(ordinary(1))
			r.h.idle(t)
			r.store.read()
			r.arrive(ordinary(2))
			require.Len(t, r.h.pushes, 2)
			assert.Equal(t, 1, r.file().Messages)
		}},
		{"a failed push drops the route proof and is not retried", func(t *testing.T, r *rig) {
			r.core.Shim = failing{r.h.shim}
			err := r.core.Offer(context.Background(), []bus.Message{ordinary(1)})
			require.NoError(t, err)
			err = r.core.Step(context.Background())
			require.Error(t, err)
			out := r.core.Fail(context.Background(), err)
			assert.Equal(t, tool.Failed, out.Status)
			assert.Equal(t, 1, r.store.unproved)
			_, pending := r.core.Pending()
			assert.False(t, pending)
		}},
		{"an unknown push keeps the notice pending and the route standing; only rule 3a pushes it again", func(t *testing.T, r *rig) {
			u := &unknownShim{Shim: r.h.shim}
			r.core.Shim = u
			r.arrive(r.store.challenge("first"))
			assert.Equal(t, 1, u.calls)
			assert.Zero(t, r.store.unproved, "an unknown outcome never drops the route proof")
			n, pending := r.core.Pending()
			require.True(t, pending, "the unknown notice may have landed: it is pending")
			r.arrive(ordinary(1))
			assert.Equal(t, 1, u.calls, "never pushed again blindly")
			r.store.clock += hook.ChallengeEvery / 2
			r.arrive(r.store.challenge("second"))
			assert.Equal(t, 1, u.calls, "no re-push inside one interval")
			r.store.clock += hook.ChallengeEvery / 2
			require.NoError(t, r.core.Step(context.Background()))
			assert.Equal(t, 2, u.calls, "rule 3a re-pushes the same notice once a newer challenge waited one interval")
			again, _ := r.core.Pending()
			assert.Equal(t, n.ID, again.ID, "still one pending notice")
			assert.Equal(t, "second", r.act(), "the model proves the newest from the one notice")
			assert.Equal(t, int64(1), r.core.Epoch, "the hook epoch is untouched")
		}},
		{"the model answers once, then is busy: the next two challenges each reach it", func(t *testing.T, r *rig) {
			r.arrive(r.store.challenge("first"))
			assert.Equal(t, "first", r.act(), "the model answers the first")
			r.h.busy(t)
			r.store.clock += hook.ChallengeEvery
			r.arrive(r.store.challenge("second"))
			require.Len(t, r.h.pushes, 2, "the second challenge is pushed: the first notice was consumed")
			first, _ := r.core.Pending()
			r.store.clock += hook.ChallengeEvery / 2
			require.NoError(t, r.core.Step(context.Background()))
			assert.Len(t, r.h.pushes, 2, "no re-push inside one interval")
			r.store.clock += hook.ChallengeEvery / 2
			r.arrive(r.store.challenge("third"))
			require.Len(t, r.h.pushes, 3, "the unanswered notice is re-pushed after one interval, holding the third")
			again, _ := r.core.Pending()
			assert.Equal(t, first.ID, again.ID, "a re-push of the one notice, never a second pending one")
			assert.Equal(t, challengePrefix+"third", r.file().Challenge.Subject)
			require.NoError(t, r.core.Step(context.Background()))
			r.store.clock += 2 * hook.ChallengeEvery
			require.NoError(t, r.core.Step(context.Background()))
			assert.Len(t, r.h.pushes, 3, "the same challenge is never re-pushed")
			assert.Equal(t, "third", r.act(), "the model proves the newest from the one notice")
			_, pending := r.core.Pending()
			assert.False(t, pending, "consumed")
		}},
		{"an answered message is not counted", func(t *testing.T, r *rig) {
			r.store.answered["m-01"] = true
			r.arrive(ordinary(1))
			assert.Empty(t, r.h.pushes)
		}},
	}
	for name, make := range shims(t) {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.run(t, newRig(t, make))
			})
		}
	}
}

// failing is a harness whose mechanism refuses every push.
type failing struct{ Shim }

// unknownShim is a native harness whose every push ends with an unknown outcome.
type unknownShim struct {
	Shim
	calls int
}

func (u *unknownShim) Push(context.Context, Notice) (string, error) {
	u.calls++
	return "", fmt.Errorf("%w: start turn: thread-follower-start-turn native error after the request was handed on: \"request-timeout\"", codexdesktop.ErrOutcomeUnknown)
}

func (failing) Push(context.Context, Notice) (string, error) {
	return "", errors.New("harness refused")
}

func TestChallengeWireVersionsRemainReadable(t *testing.T) {
	t.Parallel()
	for _, body := range []string{legacyChallengeText("bob", "abc", hook.AnswerBound), ChallengeText("bob", "abc", hook.AnswerBound)} {
		m := bus.Message{Subject: "HOOK CHALLENGE nonce=abc", Body: body}
		nonce, ok := Canonical(m, "bob")
		require.True(t, ok)
		require.Equal(t, "abc", nonce)
		m.Body += "forged"
		_, ok = Canonical(m, "bob")
		require.False(t, ok)
	}
}
