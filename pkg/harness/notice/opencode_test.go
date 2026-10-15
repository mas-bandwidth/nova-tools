package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpenCode is the opencode server as `opencode api` answers it: no socket,
// no process, and a clock that moves only when a request or a test moves it.
// A synthetic admitted while a turn runs waits in the inbox until the turn's
// next step (steer); one admitted to an idle session starts a turn (resume).
type fakeOpenCode struct {
	mu       sync.Mutex
	session  string
	clock    int64
	inbox    []openCodeInbox
	active   bool
	idle     *int64
	bodies   []map[string]any
	refuse   int   // a status the synthetic answers instead
	broken   error // a transport fault every request answers
	garble   bool  // the synthetic answers no item
	sessions []map[string]any
	query    url.Values
}

func newFakeOpenCode() *fakeOpenCode { return &fakeOpenCode{session: "ses_ada1"} }

func (f *fakeOpenCode) api(_ context.Context, method, path string, body []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken != nil {
		return nil, f.broken
	}
	answer := func(v any) ([]byte, error) { return json.Marshal(map[string]any{"data": v}) }
	base := "/api/session/" + f.session
	switch {
	case method == "POST" && path == base+"/synthetic":
		if f.refuse != 0 {
			return nil, &OpenCodeStatus{Code: f.refuse}
		}
		var b map[string]any
		if err := json.Unmarshal(body, &b); err != nil {
			return nil, &OpenCodeStatus{Code: 400}
		}
		f.bodies = append(f.bodies, b)
		if f.garble {
			return []byte("not json"), nil
		}
		f.clock++
		it := openCodeInbox{ID: fmt.Sprintf("msg_%03d", f.clock)}
		it.Time.Created = f.clock
		f.inbox = append(f.inbox, it)
		if !f.active && b["resume"] == true {
			f.active, f.inbox = true, nil // the wake: a turn starts and takes the inbox
		}
		return answer(it)
	case method == "GET" && path == base+"/inbox":
		return answer(append([]openCodeInbox{}, f.inbox...))
	case method == "GET" && path == "/api/session/active":
		out := map[string]any{}
		if f.active {
			out[f.session] = map[string]string{"type": "running"}
		}
		return answer(out)
	case method == "GET" && path == base:
		return answer(map[string]any{"id": f.session, "time": map[string]any{"created": 0, "updated": f.clock, "idle": f.idle}})
	case method == "GET" && strings.HasPrefix(path, "/api/session?"):
		f.query, _ = url.ParseQuery(strings.TrimPrefix(path, "/api/session?"))
		return answer(f.sessions)
	}
	return nil, &OpenCodeStatus{Code: 404}
}

// step is a running turn's next step boundary: steered items enter it.
func (f *fakeOpenCode) step() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active {
		f.inbox = nil
	}
}

// busy is a turn running.
func (f *fakeOpenCode) busy() {
	f.mu.Lock()
	f.active = true
	f.mu.Unlock()
	f.step()
}

// finish ends the turn: the session goes idle and stamps its marker.
func (f *fakeOpenCode) finish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inbox, f.active = nil, false
	f.clock++
	at := f.clock
	f.idle = &at
}

func TestOpenCodePushIsOneSyntheticNamingTheSavedFile(t *testing.T) {
	t.Parallel()
	f := newFakeOpenCode()
	o := &OpenCode{Session: f.session, Notices: "/private/n", API: f.api}
	n := Notice{ID: "n1", Path: "/private/n/n1.json", Count: 3}
	receipt, err := o.Push(context.Background(), n)
	require.NoError(t, err)
	assert.Equal(t, "msg_001", receipt)
	require.Len(t, f.bodies, 1)
	assert.Equal(t, n.Text(), f.bodies[0]["text"], "the text names the saved file only")
	assert.Equal(t, "steer", f.bodies[0]["delivery"])
	assert.Equal(t, true, f.bodies[0]["resume"], "resume wakes an idle session")
	assert.True(t, f.active, "the idle session was woken")
}

func TestOpenCodeConsumedIsAFinishedTurnAfterTheItemEnteredIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFakeOpenCode()
	f.busy()
	o := &OpenCode{Session: f.session, Notices: "/private/n", API: f.api}
	n := Notice{ID: "n1", Path: "/private/n/n1.json"}
	_, err := o.Push(ctx, n)
	require.NoError(t, err)
	done, err := o.Consumed(ctx, n)
	require.NoError(t, err)
	assert.False(t, done, "the item still waits in the inbox")

	_, err = o.Push(ctx, n)
	require.NoError(t, err)
	assert.Len(t, f.bodies, 1, "a re-push of a queued item is that item")

	f.step()
	done, err = o.Consumed(ctx, n)
	require.NoError(t, err)
	assert.False(t, done, "the turn holding it still runs")

	f.finish()
	done, err = o.Consumed(ctx, n)
	require.NoError(t, err)
	assert.True(t, done, "the turn ended after the item entered it")
}

func TestOpenCodeAnIdleMarkerOlderThanThePushIsNotConsumption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFakeOpenCode()
	f.finish() // idle at 1
	f.busy()
	o := &OpenCode{Session: f.session, Notices: "/private/n", API: f.api}
	n := Notice{ID: "n1", Path: "/private/n/n1.json"}
	_, err := o.Push(ctx, n)
	require.NoError(t, err)
	f.mu.Lock()
	f.inbox, f.active = nil, false // taken, and the drain not yet seen running
	f.mu.Unlock()
	done, err := o.Consumed(ctx, n)
	require.NoError(t, err)
	assert.False(t, done, "the idle marker predates the item")
	assert.False(t, (&OpenCode{Session: f.session, API: f.api}).mustConsumed(t), "nothing pushed is never consumed")
}

func (o *OpenCode) mustConsumed(t *testing.T) bool {
	done, err := o.Consumed(context.Background(), Notice{})
	require.NoError(t, err)
	return done
}

func TestOpenCodePushOutcomes(t *testing.T) {
	t.Parallel()
	n := Notice{ID: "n1", Path: "/private/n/n1.json"}
	t.Run("a refused synthetic is a certain failure", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpenCode()
		f.refuse = 404
		_, err := (&OpenCode{Session: f.session, API: f.api}).Push(context.Background(), n)
		require.Error(t, err)
		assert.False(t, errors.Is(err, codexdesktop.ErrOutcomeUnknown))
		assert.Contains(t, err.Error(), "HTTP 404")
	})
	t.Run("a CLI that cannot run is a certain failure", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpenCode()
		f.broken = ErrOpenCodeNotRun
		_, err := (&OpenCode{Session: f.session, API: f.api}).Push(context.Background(), n)
		require.Error(t, err)
		assert.False(t, errors.Is(err, codexdesktop.ErrOutcomeUnknown))
	})
	t.Run("an unreadable answer may have landed: unknown", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpenCode()
		f.garble = true
		_, err := (&OpenCode{Session: f.session, API: f.api}).Push(context.Background(), n)
		assert.ErrorIs(t, err, codexdesktop.ErrOutcomeUnknown)
	})
	t.Run("a broken exchange may have landed: unknown", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpenCode()
		f.broken = context.DeadlineExceeded
		_, err := (&OpenCode{Session: f.session, API: f.api}).Push(context.Background(), n)
		assert.ErrorIs(t, err, codexdesktop.ErrOutcomeUnknown)
	})
}

func TestOpenCodeFollowsTheNewestRootSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFakeOpenCode()
	f.sessions = []map[string]any{
		{"id": "ses_old", "time": map[string]any{"updated": 5}},
		{"id": "ses_child", "parentID": "ses_old", "time": map[string]any{"updated": 9}},
		{"id": "ses_gone", "time": map[string]any{"updated": 8, "archived": 8}},
		{"id": "ses_new", "time": map[string]any{"updated": 7}},
		{"id": "bad id", "time": map[string]any{"updated": 10}},
	}
	got, err := LatestOpenCodeSession(ctx, f.api, "/work/alex")
	require.NoError(t, err)
	assert.Equal(t, "ses_new", got)
	assert.Equal(t, "/work/alex", f.query.Get("directory"))
	assert.Equal(t, "null", f.query.Get("parentID"))

	o := &OpenCode{Session: "ses_old", API: f.api, Discover: func(ctx context.Context) (string, error) { return LatestOpenCodeSession(ctx, f.api, "/work/alex") }}
	from, to, moved, err := o.Follow(ctx)
	require.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, "ses_old", from)
	assert.Equal(t, "ses_new", to)
	_, _, moved, err = o.Follow(ctx)
	require.NoError(t, err)
	assert.False(t, moved)

	pinned := &OpenCode{Session: "ses_old"}
	_, _, moved, err = pinned.Follow(ctx)
	require.NoError(t, err)
	assert.False(t, moved, "a pinned session never moves")
}

func TestProbeOpenCodeSessionAndSessionShape(t *testing.T) {
	t.Parallel()
	f := newFakeOpenCode()
	require.NoError(t, ProbeOpenCodeSession(context.Background(), f.api, f.session))
	assert.Error(t, ProbeOpenCodeSession(context.Background(), f.api, "ses_other"))
	assert.Error(t, ProbeOpenCodeSession(context.Background(), f.api, "../ses_ada1"))
	assert.True(t, ValidOpenCodeSession("ses_0b1cAz"))
	for _, bad := range []string{"", "ses_", "msg_1", "ses_a/b", "ses_a?x=1", "ses_a b"} {
		assert.False(t, ValidOpenCodeSession(bad), bad)
	}
}

// fakeCLI is a stand-in opencode binary: it records its arguments and answers
// as `opencode api` does, the body on stdout and HTTP <status> on stderr.
func fakeCLI(t *testing.T, stdout, stderr string, code int) (bin, argsFile string) {
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	bin = filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + shQuote(argsFile) + "\n" +
		"printf '%s' " + shQuote(stdout) + "\nprintf '%s' " + shQuote(stderr) + " >&2\nexit " + fmt.Sprint(code) + "\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o700))
	return bin, argsFile
}

func TestCLIForOpenCode(t *testing.T) {
	t.Parallel()
	t.Run("a request is opencode api METHOD PATH -d BODY", func(t *testing.T) {
		t.Parallel()
		bin, args := fakeCLI(t, `{"data":{"id":"msg_1"}}`, "", 0)
		out, err := CLIForOpenCode(bin)(context.Background(), "POST", "/api/session/ses_a/synthetic", []byte(`{"text":"x"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"data":{"id":"msg_1"}}`, string(out))
		got, err := os.ReadFile(args)
		require.NoError(t, err)
		assert.Equal(t, "api\nPOST\n/api/session/ses_a/synthetic\n-d\n{\"text\":\"x\"}\n", string(got))
	})
	t.Run("a refused status is the status alone", func(t *testing.T) {
		t.Parallel()
		bin, _ := fakeCLI(t, `{"error":"bearer fake-credential-text"}`, "HTTP 401 Unauthorized\n", 1)
		_, err := CLIForOpenCode(bin)(context.Background(), "GET", "/api/session/active", nil)
		var status *OpenCodeStatus
		require.ErrorAs(t, err, &status)
		assert.Equal(t, 401, status.Code)
		assert.NotContains(t, err.Error(), "fake-credential", "nothing the CLI printed reaches the error")
	})
	t.Run("another failure never carries the CLI's output", func(t *testing.T) {
		t.Parallel()
		bin, _ := fakeCLI(t, "", "password=hunter2\n", 3)
		_, err := CLIForOpenCode(bin)(context.Background(), "GET", "/api/session/active", nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2")
		assert.Contains(t, err.Error(), "exit 3")
	})
	t.Run("a missing CLI never ran", func(t *testing.T) {
		t.Parallel()
		_, err := CLIForOpenCode(filepath.Join(t.TempDir(), "absent"))(context.Background(), "GET", "/api/session/active", nil)
		assert.ErrorIs(t, err, ErrOpenCodeNotRun)
	})
}
