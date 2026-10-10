package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The cover tests reach the sprintwire client and the sprint views through an
// in-process RoundTripper installed as http.DefaultTransport: the unit tier
// opens no socket (docs/SPEC-CI.md, `unit-sockets`), and both sprintwire.Client,
// which builds its own client, and readSprintView, which uses
// http.DefaultClient, resolve their transport there. A host no fake names falls
// through to the real transport, so the rest of the package's tests are
// unchanged.
var (
	coverReal  = http.DefaultTransport
	coverMu    sync.Mutex
	coverFakes = map[string]func(*http.Request) (*http.Response, error){}
)

func init() { http.DefaultTransport = coverTransport{} }

// coverTransport serves the fakes coverAt registered, else the real transport.
type coverTransport struct{}

func (coverTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	coverMu.Lock()
	fn := coverFakes[req.URL.Host]
	coverMu.Unlock()
	if fn == nil {
		return coverReal.RoundTrip(req)
	}
	return fn(req)
}

// coverHost is the host one test's fake answers on: the test's own name, so
// parallel tests never share a fake. A .test name is reserved and routable
// nowhere (RFC 6761), which the net rule allows.
func coverHost(t *testing.T) string {
	t.Helper()
	return strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()) + ".test:1"
}

// coverAt answers every request to addr with fn until the test ends.
func coverAt(t *testing.T, addr string, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	coverMu.Lock()
	coverFakes[addr] = fn
	coverMu.Unlock()
	t.Cleanup(func() {
		coverMu.Lock()
		delete(coverFakes, addr)
		coverMu.Unlock()
	})
}

// coverWire answers one sprintwire request with one result and records the
// verbs it carried; a non-200 status answers the request itself instead.
func coverWire(t *testing.T, addr string, status, code int, stdout, stderr string, sent *[][]string) {
	t.Helper()
	coverAt(t, addr, func(r *http.Request) (*http.Response, error) {
		var req sprintwire.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if sent != nil {
			*sent = append(*sent, req.Verbs...)
		}
		rec := httptest.NewRecorder()
		if status != http.StatusOK {
			rec.WriteHeader(status)
			_, _ = rec.WriteString("server said no\n")
			return rec.Result(), nil
		}
		require.NoError(t, json.NewEncoder(rec).Encode(sprintwire.Response{Results: []sprintwire.Result{{Code: code, Stdout: stdout, Stderr: stderr}}}))
		return rec.Result(), nil
	})
}

// coverBody answers every request to addr with body at status.
func coverBody(t *testing.T, addr string, status int, body string) {
	t.Helper()
	coverAt(t, addr, func(*http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(status)
		_, _ = rec.WriteString(body)
		return rec.Result(), nil
	})
}

// coverDown is a server that does not answer: the transport fails the dial.
func coverDown(t *testing.T, addr string) {
	t.Helper()
	coverAt(t, addr, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp " + addr + ": connect: connection refused")
	})
}

// TestNovaFriendMainCoverProofArgs pins proofArgs: each proof word is its flag,
// --run rides only with a check or a pong, and a stop-returns count stands alone.
func TestNovaFriendMainCoverProofArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		words friend.BeatWords
		want  []string
	}{
		{"empty", friend.BeatWords{}, nil},
		{"check alone", friend.BeatWords{Check: "c1"}, []string{"--check", "c1"}},
		{"pong alone", friend.BeatWords{Pong: "p1"}, []string{"--pong", "p1"}},
		{"check and pong with run", friend.BeatWords{Run: "r1", Check: "c1", Pong: "p1"}, []string{"--check", "c1", "--pong", "p1", "--run", "r1"}},
		{"check with run", friend.BeatWords{Run: "r1", Check: "c1"}, []string{"--check", "c1", "--run", "r1"}},
		{"run alone", friend.BeatWords{Run: "r1"}, nil},
		{"stop returns", friend.BeatWords{StopReturns: 3}, []string{"--stop-returns", "3"}},
		{"stop returns with check", friend.BeatWords{Check: "c1", StopReturns: 2}, []string{"--check", "c1", "--stop-returns", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, proofArgs(tc.words))
		})
	}
}

// TestNovaFriendMainCoverSprintVerb pins sprintVerb: a verb with code 0 is nil,
// a refusal names the verb and the trimmed stderr, a 500 and a server that does
// not answer are errors too.
func TestNovaFriendMainCoverSprintVerb(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		status  int
		code    int
		stderr  string
		down    bool
		wantErr string
	}{
		{"ok", http.StatusOK, 0, "", false, ""},
		{"refused", http.StatusOK, 1, "  boom\n", false, "progress refused: boom"},
		{"500", http.StatusInternalServerError, 0, "", false, "refused the request (500"},
		{"no answer", 0, 0, "", true, "did not answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := coverHost(t)
			if tc.down {
				coverDown(t, addr)
			} else {
				coverWire(t, addr, tc.status, tc.code, "", tc.stderr, nil)
			}
			err := sprintVerb(context.Background(), addr, []string{"progress", "w1"})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestNovaFriendMainCoverSprintAsk pins sprintAsk: code 0 returns what was
// printed; a refusal is a *friend.Refused naming at most the first two argv
// words and the trimmed stderr; a 500 and no answer are errors.
func TestNovaFriendMainCoverSprintAsk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		status      int
		code        int
		stdout      string
		stderr      string
		down        bool
		argv        []string
		wantOut     string
		wantErr     string
		wantRefused bool
	}{
		{"ok", http.StatusOK, 0, "QUEUE OK\n", "", false, []string{"friend", "cards"}, "QUEUE OK\n", "", false},
		{"refused names two words", http.StatusOK, 1, "", "  nope  \n", false, []string{"friend", "cards", "--as", "bob"}, "", "friend cards refused: nope", true},
		{"500", http.StatusInternalServerError, 0, "", "", false, []string{"queue", "--as", "bob"}, "", "refused the request (500", false},
		{"no answer", 0, 0, "", "", true, []string{"queue"}, "", "did not answer", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := coverHost(t)
			if tc.down {
				coverDown(t, addr)
			} else {
				coverWire(t, addr, tc.status, tc.code, tc.stdout, tc.stderr, nil)
			}
			out, err := sprintAsk(context.Background(), addr, tc.argv)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.wantOut, out)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.Empty(t, out)
			if !tc.wantRefused {
				return
			}
			var refused *friend.Refused
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, tc.wantErr, refused.Why)
			assert.NotContains(t, refused.Why, "--as", "the refusal names at most the first two argv words")
		})
	}
}

// TestNovaFriendMainCoverSprintBeat pins sprintBeat: code 0 returns the
// FRIEND-BEAT line, a refusal names the trimmed stderr, and a 500 and no answer
// are errors.
func TestNovaFriendMainCoverSprintBeat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		status  int
		code    int
		stdout  string
		stderr  string
		down    bool
		wantOut string
		wantErr string
	}{
		{"ok", http.StatusOK, 0, "FRIEND-BEAT bob\n", "", false, "FRIEND-BEAT bob\n", ""},
		{"refused", http.StatusOK, 2, "", "  no beat  \n", false, "", "friend beat refused: no beat"},
		{"500", http.StatusInternalServerError, 0, "", "", false, "", "refused the request (500"},
		{"no answer", 0, 0, "", "", true, "", "did not answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := coverHost(t)
			if tc.down {
				coverDown(t, addr)
			} else {
				coverWire(t, addr, tc.status, tc.code, tc.stdout, tc.stderr, nil)
			}
			out, err := sprintBeat(context.Background(), addr, []string{"friend", "beat", "bob"})
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.wantOut, out)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.Empty(t, out)
		})
	}
}

// TestNovaFriendMainCoverSprintHolders pins sprintHolders: a cards view schema 1
// gives the holder map with no empty holder, and a wrong view, invalid JSON and
// a duplicate card id are all refused.
func TestNovaFriendMainCoverSprintHolders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		want    map[string]string
		wantErr string
	}{
		{"schema 1", `{"view":"cards","schema":1,"cards":[{"id":"c1","holder":"bob"},{"id":"c2","holder":""}]}`, map[string]string{"c1": "bob"}, ""},
		{"wrong view", `{"view":"worker","schema":1,"cards":[]}`, nil, "expected view cards schema 1"},
		{"invalid json", `{`, nil, "not valid JSON"},
		{"duplicate id", `{"view":"cards","schema":1,"cards":[{"id":"c1","holder":"bob"},{"id":"c1","holder":"eve"}]}`, nil, "duplicate card id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := coverHost(t)
			coverBody(t, addr, http.StatusOK, tc.body)
			got, err := sprintHolders(context.Background(), addr)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestNovaFriendMainCoverReadSprintView pins readSprintView's two bounds: a body
// over maxView and a non-200 status are refused.
func TestNovaFriendMainCoverReadSprintView(t *testing.T) {
	t.Parallel()
	t.Run("over maxView", func(t *testing.T) {
		addr := coverHost(t)
		coverBody(t, addr, http.StatusOK, strings.Repeat("x", maxView+1))
		_, err := readSprintView(context.Background(), addr, "/api/view/cards")
		require.ErrorContains(t, err, fmt.Sprintf("its view is over %d bytes", maxView))
	})
	t.Run("non-200", func(t *testing.T) {
		addr := coverHost(t)
		coverBody(t, addr, http.StatusInternalServerError, "nope")
		_, err := readSprintView(context.Background(), addr, "/api/view/cards")
		require.ErrorContains(t, err, "refused the view")
	})
}

// TestNovaFriendMainCoverRealWorldHooks pins that realWorld wires the sprint
// verbs and views the daemon calls.
func TestNovaFriendMainCoverRealWorldHooks(t *testing.T) {
	t.Parallel()
	w := realWorld()
	assert.NotNil(t, w.progress)
	assert.NotNil(t, w.finish)
	assert.NotNil(t, w.down)
	assert.NotNil(t, w.cards)
	assert.NotNil(t, w.view)
	assert.NotNil(t, w.holders)
}

// TestNovaFriendMainCoverRealWorldBeat pins realWorld's beat and beatDown: the
// FRIEND-BEAT line carries --active only when active is set, beatDown adds
// --until and --reason, and both carry the proof flags.
func TestNovaFriendMainCoverRealWorldBeat(t *testing.T) {
	t.Parallel()
	w := realWorld()
	active := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 4, 4, 30, 0, 0, time.UTC)
	cases := []struct {
		name   string
		down   bool
		active time.Time
		reason string
		proof  friend.BeatWords
		want   []string
	}{
		{"beat no active", false, time.Time{}, "", friend.BeatWords{}, []string{"friend", "beat", "bob"}},
		{"beat active and proof", false, active, "", friend.BeatWords{Run: "r1", Check: "c1", Pong: "p1"}, []string{"friend", "beat", "bob", "--active", "2026-10-04T03:00:00Z", "--check", "c1", "--pong", "p1", "--run", "r1"}},
		{"beatDown no active", true, time.Time{}, "no session answer", friend.BeatWords{Check: "c1"}, []string{"friend", "beat", "bob", "--until", "2026-10-04T04:30:00Z", "--reason", "no session answer", "--check", "c1"}},
		{"beatDown active", true, active, "no session answer", friend.BeatWords{Pong: "p1"}, []string{"friend", "beat", "bob", "--until", "2026-10-04T04:30:00Z", "--reason", "no session answer", "--active", "2026-10-04T03:00:00Z", "--pong", "p1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := coverHost(t)
			var sent [][]string
			coverWire(t, addr, http.StatusOK, 0, "FRIEND-BEAT bob\n", "", &sent)
			if tc.down {
				require.NoError(t, w.beatDown(context.Background(), addr, "bob", tc.active, until, tc.reason, tc.proof))
			} else {
				out, err := w.beat(context.Background(), addr, "bob", tc.active, tc.proof)
				require.NoError(t, err)
				assert.Equal(t, "FRIEND-BEAT bob\n", out)
			}
			require.Len(t, sent, 1)
			assert.Equal(t, tc.want, sent[0])
		})
	}
}

// TestNovaFriendMainCoverRealWorldSleep pins that the real sleep returns at once
// on a cancelled context, so a stopped daemon does not wait out its delay.
func TestNovaFriendMainCoverRealWorldSleep(t *testing.T) {
	t.Parallel()
	w := realWorld()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.sleep(ctx, time.Hour)
}

// TestNovaFriendMainCoverRealWorldRandom pins the six-character [a-z0-9] nonce.
func TestNovaFriendMainCoverRealWorldRandom(t *testing.T) {
	t.Parallel()
	w := realWorld()
	assert.Regexp(t, `^[a-z0-9]{6}$`, w.random())
}

// TestNovaFriendMainCoverRealWorldBinary pins that binary gives an absolute path.
func TestNovaFriendMainCoverRealWorldBinary(t *testing.T) {
	t.Parallel()
	w := realWorld()
	p, err := w.binary()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p), p)
}

// TestNovaFriendMainCoverRoute pins world.route: no model id or no cards func is
// not found and asks nothing, and a row the server answers is cached.
func TestNovaFriendMainCoverRoute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		model     string
		nilCards  bool
		wantFound bool
		wantCalls int
	}{
		{"no model", "", false, false, 0},
		{"no slash", "justmodel", false, false, 0},
		{"nil cards", "inception/mercury-2.5", true, false, 0},
		{"found and cached", "inception/mercury-2.5", false, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			w := world{}
			if !tc.nilCards {
				w.cards = func(_ context.Context, _ string, argv []string) (string, error) {
					calls++
					require.Equal(t, []string{"routes", "--json"}, argv)
					return `{"routes":[{"name":"flash-mercury","provider":"inception","model":"mercury-2.5","prices":{"input":"0.25","cache_read":"0.025","output":"1"}}]}`, nil
				}
			}
			route := w.route("cover.test:1", tc.model)
			first, second := route(), route()
			assert.Equal(t, tc.wantCalls, calls)
			assert.Equal(t, tc.wantFound, first.Found)
			assert.Equal(t, tc.wantFound, second.Found)
			if tc.wantFound {
				assert.Equal(t, "flash-mercury", first.Name)
				assert.Equal(t, "0.25", first.Prices.Input)
			}
		})
	}
}

// TestNovaFriendMainCoverDaemonStagerPrune pins that an empty daemonStager prunes
// nothing rather than reaching a nil stager.
func TestNovaFriendMainCoverDaemonStagerPrune(t *testing.T) {
	t.Parallel()
	assert.Nil(t, daemonStager{}.prune())
}

// TestNovaFriendMainCoverDirThere pins dirThere: a directory is fine, a regular
// file and a missing path are refused.
func TestNovaFriendMainCoverDirThere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"directory", dir, ""},
		{"regular file", file, "is not a directory"},
		{"missing", filepath.Join(dir, "gone"), "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := dirThere(tc.path)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
