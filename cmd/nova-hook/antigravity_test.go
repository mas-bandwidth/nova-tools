package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return real
}

type fakeAntigravityHarness struct {
	t            *testing.T
	home         string
	workspaceDir string
	eventsDir    string
	pid          int
	token        string
	ports        []int
	activePort   int
	conversation string
	serverBin    string
	noServer     bool
	noPorts      bool
	noConv       bool
	probeErr     error
	sendErr      error
	sendTimeout  bool
	sentNotices  []string
	sentPorts    []int
	sentTokens   []string
}

func newFakeAntigravityHarness(t *testing.T) *fakeAntigravityHarness {
	t.Helper()
	base := canonicalTempDir(t)
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "emma-working")
	events := filepath.Join(home, ".gemini", "antigravity", "nova-hook", "emma")
	require.NoError(t, os.MkdirAll(home, 0700))
	require.NoError(t, os.MkdirAll(ws, 0700))
	return &fakeAntigravityHarness{
		t:            t,
		home:         home,
		workspaceDir: ws,
		eventsDir:    events,
		pid:          41200,
		token:        "csrf-tok-1",
		ports:        []int{61100, 61101},
		activePort:   61101,
		conversation: "conv-root-2",
		serverBin:    "/Applications/Antigravity.app/Contents/Resources/bin/language_server",
	}
}

func (f *fakeAntigravityHarness) summaryPayload(wsURI string, mtimeSec int64, isSubagent bool, parentID string) string {
	f.t.Helper()
	var fields []byte
	fields = appendProtoBytes(fields, 1, []byte("Summary"))
	var ts []byte
	ts = appendProtoVarint(ts, 1, uint64(mtimeSec))
	fields = appendProtoBytes(fields, 4, ts)
	fields = appendProtoBytes(fields, 9, []byte(wsURI))
	if isSubagent {
		fields = appendProtoVarint(fields, 14, 1)
	}
	if parentID != "" {
		fields = appendProtoBytes(fields, 16, []byte(parentID))
	}
	return base64.StdEncoding.EncodeToString(fields)
}

func appendProtoTag(b []byte, fieldNum int, wireType int) []byte {
	return appendRawVarint(b, uint64((fieldNum<<3)|wireType))
}

func appendProtoVarint(b []byte, fieldNum int, val uint64) []byte {
	b = appendProtoTag(b, fieldNum, 0)
	return appendRawVarint(b, val)
}

func appendProtoBytes(b []byte, fieldNum int, data []byte) []byte {
	b = appendProtoTag(b, fieldNum, 2)
	b = appendRawVarint(b, uint64(len(data)))
	return append(b, data...)
}

func appendRawVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func (f *fakeAntigravityHarness) exec(_ context.Context, _ string, env []string, name string, args ...string) ([]byte, error) {
	switch {
	case name == "ps":
		if f.noServer {
			return []byte("  100 /sbin/launchd\n"), nil
		}
		wsID := "file_" + strings.ReplaceAll(strings.TrimPrefix(f.workspaceDir, "/"), "/", "_")
		wsID = strings.ReplaceAll(wsID, "-", "_")
		cmd := f.serverBin
		if cmd == "" {
			cmd = "language_server"
		}
		if f.token != "" {
			cmd += " --csrf_token " + f.token
		}
		cmd += " --workspace_id " + wsID
		return []byte(fmt.Sprintf("  %d %s\n", f.pid, cmd)), nil

	case name == "lsof":
		if f.noPorts {
			return []byte("COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n"), nil
		}
		var b strings.Builder
		b.WriteString("COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n")
		for i, p := range f.ports {
			fmt.Fprintf(&b, "language_ %d emma %du IPv4 0x0 0t0 TCP 127.0.0.1:%d (LISTEN)\n", f.pid, i+10, p)
		}
		return []byte(b.String()), nil

	case name == "sqlite3":
		if f.noConv {
			return []byte("[]"), nil
		}
		wsURI := "file://" + f.workspaceDir
		rows := []map[string]string{
			{"key": "conversation_summaries.conv-other", "value": f.summaryPayload("file:///Users/glenn/other", 900, false, "")},
			{"key": "conversation_summaries.conv-root-1", "value": f.summaryPayload(wsURI, 100, false, "")},
			{"key": "conversation_summaries." + f.conversation, "value": f.summaryPayload(wsURI, 200, false, "")},
			{"key": "conversation_summaries.conv-subagent", "value": f.summaryPayload(wsURI, 500, true, f.conversation)},
		}
		raw, err := json.Marshal(rows)
		return raw, err

	case strings.HasSuffix(name, "agentapi"):
		port := envInt(env, "ANTIGRAVITY_AGENT_PORT")
		tok := envVal(env, "ANTIGRAVITY_CSRF_TOKEN")
		if len(args) >= 1 && args[0] == "get-conversation-metadata" {
			if f.probeErr != nil {
				return []byte(f.probeErr.Error()), f.probeErr
			}
			if port != f.activePort || tok != f.token {
				return []byte("connection refused"), errors.New("exit status 1")
			}
			return []byte(`{"id":"` + f.conversation + `"}`), nil
		}
		if len(args) >= 4 && args[0] == "send-message" {
			if port != f.activePort || tok != f.token {
				return []byte("connection refused"), errors.New("exit status 1")
			}
			if f.sendTimeout {
				return nil, context.DeadlineExceeded
			}
			if f.sendErr != nil {
				return []byte(f.sendErr.Error()), f.sendErr
			}
			conv := args[2]
			notice := args[3]
			f.sentNotices = append(f.sentNotices, notice)
			f.sentPorts = append(f.sentPorts, port)
			f.sentTokens = append(f.sentTokens, tok)
			msgDir := antigravityMailboxDir(f.home, conv)
			if err := os.MkdirAll(msgDir, 0700); err != nil {
				return nil, err
			}
			file := fmt.Sprintf("msg-%03d.json", len(f.sentNotices))
			if err := os.WriteFile(filepath.Join(msgDir, file), []byte(notice), 0600); err != nil {
				return nil, err
			}
			return []byte("Message sent successfully\n"), nil
		}
	}
	return nil, fmt.Errorf("unexpected command: %s %v", name, args)
}

func envVal(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

func envInt(env []string, key string) int {
	v := envVal(env, key)
	var n int
	_, _ = fmt.Sscanf(v, "%d", &n)
	return n
}

func fakeAntigravityWorld(r *rig, fh *fakeAntigravityHarness) world {
	require.NoError(fh.t, r.h.Client.SAdd(context.Background(), "friends", "emma", "rowan", "stella", "glenn").Err())
	w := r.world()
	w.codexLock = lockCodexRoute
	w.codexSave = saveCodexEvent
	w.antigravityExec = fh.exec
	w.antigravityHome = func() (string, error) { return fh.home, nil }
	w.antigravityInstall = installAntigravityRoute
	w.antigravityDeliver = deliverAntigravitySaved
	return w
}

func TestAntigravityRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*fakeAntigravityHarness) []string
		want  string
	}{
		{
			name: "unknown harness",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "bogus"}
			},
			want: "--harness wants muse or antigravity",
		},
		{
			name: "combine codex and antigravity flags",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--codex-socket", "/private/sock", "--antigravity-dir", fh.workspaceDir}
			},
			want: "cannot combine --codex-* and --antigravity-* flags",
		},
		{
			name: "harness codex with antigravity dir",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "codex", "--antigravity-dir", fh.workspaceDir}
			},
			want: "--harness codex cannot use --antigravity-* flags",
		},
		{
			name: "harness antigravity with codex socket",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--codex-socket", "/private/sock"}
			},
			want: "--harness antigravity cannot use --codex-* flags",
		},
		{
			name: "missing antigravity-dir",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity"}
			},
			want: "antigravity delivery wants --antigravity-dir",
		},
		{
			name: "relative antigravity-dir",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", "relative/dir"}
			},
			want: "clean absolute path",
		},
		{
			name: "bad session id",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir, "--antigravity-session", "../bad"}
			},
			want: "without path separators",
		},
		{
			name: "bad cutover",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir, "--antigravity-since", "now"}
			},
			want: "antigravity cutover wants --antigravity-since",
		},
		{
			name: "once flag",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir, "--once"}
			},
			want: "omit --once",
		},
		{
			name: "missing workspace directory",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", filepath.Join(fh.home, "nonexistent")}
			},
			want: "is not accessible",
		},
		{
			name: "existing insecure events directory",
			setup: func(fh *fakeAntigravityHarness) []string {
				require.NoError(t, os.MkdirAll(fh.eventsDir, 0755))
				require.NoError(t, os.Chmod(fh.eventsDir, 0755))
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "0700",
		},
		{
			name: "no language server",
			setup: func(fh *fakeAntigravityHarness) []string {
				fh.noServer = true
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "no Antigravity language_server running",
		},
		{
			name: "missing csrf token",
			setup: func(fh *fakeAntigravityHarness) []string {
				fh.token = ""
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "has no --csrf_token",
		},
		{
			name: "no listening ports",
			setup: func(fh *fakeAntigravityHarness) []string {
				fh.noPorts = true
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "no listening TCP ports",
		},
		{
			name: "no conversation found",
			setup: func(fh *fakeAntigravityHarness) []string {
				fh.noConv = true
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "no Antigravity conversation found",
		},
		{
			name: "probe fails on all ports",
			setup: func(fh *fakeAntigravityHarness) []string {
				fh.probeErr = errors.New("probe refused")
				return []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "did not answer get-conversation-metadata",
		},
		{
			name: "role without bus push",
			setup: func(fh *fakeAntigravityHarness) []string {
				return []string{"hook", "--as", "emma", "--role", "member", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir}
			},
			want: "Antigravity delivery wants a role with the bus push",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			fh := newFakeAntigravityHarness(t)
			args := tc.setup(fh)
			w := fakeAntigravityWorld(r, fh)
			var out bytes.Buffer
			code := run(args, strings.NewReader(""), &out, &out, w)
			assert.Equal(t, 2, code, out.String())
			assert.Contains(t, out.String(), tc.want)
			assert.Equal(t, hook.StateNone, r.h.State(t, "emma").State)
		})
	}
}

func TestAntigravityHarnessInstallsSetupProbesAndDeliversBootstrap(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fh := newFakeAntigravityHarness(t)
	w := fakeAntigravityWorld(r, fh)
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}

	var out bytes.Buffer
	args := []string{
		"hook", "--as", "emma", "--role", "friend",
		"--harness", "antigravity",
		"--antigravity-dir", fh.workspaceDir,
		"--antigravity-since", "2026-10-11T12:00:00Z",
	}
	code := run(args, strings.NewReader(""), &out, &out, w)
	require.Zero(t, code, out.String())

	// 1. Installed ~/.gemini/antigravity/bin/agentapi wrapper (0755)
	wrapper := filepath.Join(fh.home, ".gemini", "antigravity", "bin", "agentapi")
	wrapperInfo, err := os.Stat(wrapper)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), wrapperInfo.Mode().Perm())
	wrapperRaw, err := os.ReadFile(wrapper)
	require.NoError(t, err)
	assert.Contains(t, string(wrapperRaw), `exec "/Applications/Antigravity.app/Contents/Resources/bin/language_server" agentapi "$@"`)

	// 2. Installed ~/.gemini/antigravity/nova-hook/emma private 0700 directory
	dirInfo, err := os.Stat(fh.eventsDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	// 3. Emitted antigravity-installed state line and bootstrap push line
	assert.Contains(t, out.String(), "state=antigravity-installed conversation=conv-root-2 port=61101")
	assert.Contains(t, out.String(), "push=antigravity")
	assert.Contains(t, out.String(), "native=accepted")
	assert.Contains(t, out.String(), "turn=msg-001.json")
	assert.Contains(t, out.String(), "recovery-cutover")

	// 4. Sent fixed external-event notice into conv-root-2's mailbox
	require.Len(t, fh.sentNotices, 1)
	assert.Contains(t, fh.sentNotices[0], "nova-hook saved event recovery-")
	assert.Equal(t, []int{61101}, fh.sentPorts)
	assert.Equal(t, []string{"csrf-tok-1"}, fh.sentTokens)

	// 5. Native acceptance does not prove the hook until the model runs nova-hook prove
	assert.Equal(t, hook.StateLive, r.h.State(t, "emma").State)
}

func TestAntigravityIdleWakeAndMidTurnChallengeWhileCoalescingOrdinaryBurst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fh := newFakeAntigravityHarness(t)
	w := fakeAntigravityWorld(r, fh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

	saved := map[string]savedCodexEvent{}
	origSave := w.codexSave
	w.codexSave = func(dir, id string, data []byte) error {
		if err := origSave(dir, id, data); err != nil {
			return err
		}
		var e savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &e))
		saved[id] = e
		return nil
	}

	var deliveredIDs []string
	origDeliver := w.antigravityDeliver
	w.antigravityDeliver = func(dctx context.Context, ag *antigravityRoute, id, dir string) (string, error) {
		deliveredIDs = append(deliveredIDs, id)
		return origDeliver(dctx, ag, id, dir)
	}

	// Build a burst of 51 ordinary messages plus a mid-turn server challenge.
	burst := make([]bus.Entry, 0, 52)
	burst = append(burst, bus.Entry{
		Entry: "100-0",
		Fields: map[string]string{
			"id": "m-0", "from": "rowan", "to": "emma",
			"subject": "first task", "body": "initial ordinary message",
		},
	})
	senders := []string{"rowan", "stella", "glenn"}
	for i := 1; i <= 25; i++ {
		burst = append(burst, bus.Entry{
			Entry: fmt.Sprintf("%d-0", 100+i),
			Fields: map[string]string{
				"id":      fmt.Sprintf("m-%d", i),
				"from":    senders[(i-1)%len(senders)],
				"to":      "emma",
				"subject": fmt.Sprintf("ordinary %d", i),
				"body":    "burst payload",
			},
		})
	}
	// Mid-turn server challenge arrives in the middle of the 50-message burst!
	burst = append(burst, bus.Entry{
		Entry: "126-0",
		Fields: map[string]string{
			"id":      "c-mid",
			"from":    "rowan",
			"to":      "emma",
			"subject": "HOOK CHALLENGE nonce=mid-nonce",
			"body":    challengeText("emma", "mid-nonce", hook.ChallengeEvery),
		},
	})
	for i := 26; i <= 50; i++ {
		burst = append(burst, bus.Entry{
			Entry: fmt.Sprintf("%d-0", 101+i),
			Fields: map[string]string{
				"id":      fmt.Sprintf("m-%d", i),
				"from":    senders[(i-1)%len(senders)],
				"to":      "emma",
				"subject": fmt.Sprintf("ordinary %d", i),
				"body":    "burst payload",
			},
		})
	}

	epoch, _, err := r.h.Store.HookAs(ctx, "emma", hook.Friend, "test")
	require.NoError(t, err)
	require.NoError(t, r.h.Store.Renew(ctx, "emma", hook.Friend, hook.Bus, epoch, "test"))
	ag := &antigravityRoute{
		as:           "emma",
		workspaceDir: fh.workspaceDir,
		sessionID:    fh.conversation,
		eventsDir:    fh.eventsDir,
		homeDir:      fh.home,
		exec:         fh.exec,
	}
	require.NoError(t, installAntigravityRoute(ag))
	route := &codexRoute{directory: fh.eventsDir, antigravity: ag}

	step := 0
	st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		switch step {
		case 1:
			// Write the server challenge into the hook record before delivering burst.
			_, err := r.h.Store.Challenge(ctx, "emma", hook.Friend, epoch, []hook.Push{hook.Bus}, "mid-nonce")
			require.NoError(t, err)
			return burst
		case 2:
			// Model proves the mid-turn challenge while still working on m-0.
			// Proving a mid-turn challenge must NOT flush the 50 folded ordinary messages!
			_, err := r.h.Store.Answer(ctx, "emma", "mid-nonce", "session")
			require.NoError(t, err)
			return nil
		case 3:
			// Now the model finishes its ordinary turn by answering m-0 on the bus.
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("emma"), "m-0", "1700000000").Err())
			return nil
		default:
			cancel()
			return nil
		}
	}}

	s := &session{bus: st, hooks: r.h.Store, as: "emma", addr: "127.0.0.1:6379"}
	var out bytes.Buffer
	res := w.loop(&tool.Call{Stdout: &out}, s, hook.Friend, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
	require.Equal(t, tool.OK, res.Status, out.String())

	// m-0 + mid-turn challenge c-mid + 1 coalesced summary = 3 total deliveries in loop.
	require.Len(t, deliveredIDs, 3, "deliveredIDs=%v\nout=%s", deliveredIDs, out.String())
	assert.Equal(t, "m-0", deliveredIDs[0])
	assert.Equal(t, "c-mid", deliveredIDs[1], "mid-turn challenge must deliver immediately without waiting for m-0 turn boundary")
	assert.True(t, strings.HasPrefix(deliveredIDs[2], "summary-"), "50 ordinary messages must coalesce into one summary turn at turn boundary")

	summaryEvent := saved[deliveredIDs[2]]
	assert.Equal(t, "50 more messages from rowan, stella, glenn; read the bus", summaryEvent.Summary)
	assert.Contains(t, out.String(), `native=coalesced pending=50 summary="50 more messages from rowan, stella, glenn; read the bus"`)
	assert.Equal(t, hook.StateProven, r.h.State(t, "emma").State)
}

func TestAntigravityServerRestartMidRunReDiscoversPortAndToken(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fh := newFakeAntigravityHarness(t)
	w := fakeAntigravityWorld(r, fh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

	epoch, _, err := r.h.Store.HookAs(ctx, "emma", hook.Comms, "test")
	require.NoError(t, err)
	require.NoError(t, r.h.Store.Renew(ctx, "emma", hook.Comms, hook.Bus, epoch, "test"))
	ag := &antigravityRoute{
		as:           "emma",
		workspaceDir: fh.workspaceDir,
		eventsDir:    fh.eventsDir,
		homeDir:      fh.home,
		exec:         fh.exec,
	}
	require.NoError(t, installAntigravityRoute(ag))
	route := &codexRoute{directory: fh.eventsDir, antigravity: ag}

	step := 0
	st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		switch step {
		case 1:
			return []bus.Entry{{
				Entry:  "1-0",
				Fields: map[string]string{"id": "before-restart", "from": "rowan", "to": "emma", "subject": "ping", "body": "hello"},
			}}
		case 2:
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("emma"), "before-restart", "1700000000").Err())
			// Simulate language_server restarting with a new PID, CSRF token, and listening port.
			fh.pid = 49999
			fh.token = "csrf-tok-restarted"
			fh.ports = []int{62201, 62202}
			fh.activePort = 62202
			return []bus.Entry{{
				Entry:  "2-0",
				Fields: map[string]string{"id": "after-restart", "from": "rowan", "to": "emma", "subject": "ping2", "body": "hello2"},
			}}
		default:
			cancel()
			return nil
		}
	}}

	s := &session{bus: st, hooks: r.h.Store, as: "emma", addr: "127.0.0.1:6379"}
	var out bytes.Buffer
	res := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
	require.Equal(t, tool.OK, res.Status, out.String())
	require.Len(t, fh.sentPorts, 2)
	assert.Equal(t, 61101, fh.sentPorts[0])
	assert.Equal(t, 62202, fh.sentPorts[1])
	assert.Equal(t, "csrf-tok-restarted", fh.sentTokens[1])
}

func TestAntigravityDeliveryOutcomesAndReconciliation(t *testing.T) {
	t.Parallel()

	t.Run("unknown timeout revokes proof without unhook", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		fh := newFakeAntigravityHarness(t)
		w := fakeAntigravityWorld(r, fh)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

		epoch, _, err := r.h.Store.HookAs(ctx, "emma", hook.Comms, "test")
		require.NoError(t, err)
		require.NoError(t, r.h.Store.Renew(ctx, "emma", hook.Comms, hook.Bus, epoch, "test"))
		require.NoError(t, r.h.Store.Prove(ctx, "emma", hook.Comms, hook.Bus, epoch, "test"))
		ag := &antigravityRoute{
			as:           "emma",
			workspaceDir: fh.workspaceDir,
			eventsDir:    fh.eventsDir,
			homeDir:      fh.home,
			exec:         fh.exec,
		}
		require.NoError(t, installAntigravityRoute(ag))
		route := &codexRoute{directory: fh.eventsDir, antigravity: ag}

		step := 0
		st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
			step++
			if step == 1 {
				fh.sendTimeout = true
				return []bus.Entry{{
					Entry:  "1-0",
					Fields: map[string]string{"id": "msg-timeout", "from": "rowan", "to": "emma", "subject": "ping", "body": "hello"},
				}}
			}
			cancel()
			return nil
		}}

		s := &session{bus: st, hooks: r.h.Store, as: "emma", addr: "127.0.0.1:6379"}
		var out bytes.Buffer
		res := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
		require.NotNil(t, res)
		assert.Equal(t, tool.Failed, res.Status)
		res.Verb = "hook"
		res.Render(&out, false)
		assert.Contains(t, out.String(), "Antigravity local delivery unknown")
		assert.Contains(t, out.String(), "native=unknown")
		assert.Equal(t, hook.StateLive, r.h.State(t, "emma").State)
	})

	t.Run("unknown timeout reconciled by existing model answer", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		fh := newFakeAntigravityHarness(t)
		w := fakeAntigravityWorld(r, fh)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

		epoch, _, err := r.h.Store.HookAs(ctx, "emma", hook.Comms, "test")
		require.NoError(t, err)
		require.NoError(t, r.h.Store.Renew(ctx, "emma", hook.Comms, hook.Bus, epoch, "test"))
		ag := &antigravityRoute{
			as:           "emma",
			workspaceDir: fh.workspaceDir,
			eventsDir:    fh.eventsDir,
			homeDir:      fh.home,
			exec:         fh.exec,
		}
		require.NoError(t, installAntigravityRoute(ag))
		route := &codexRoute{directory: fh.eventsDir, antigravity: ag}

		step := 0
		st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
			step++
			if step == 1 {
				require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("emma"), "msg-reconciled", "1700000000").Err())
				return []bus.Entry{{
					Entry:  "1-0",
					Fields: map[string]string{"id": "msg-reconciled", "from": "rowan", "to": "emma", "subject": "ping", "body": "hello"},
				}}
			}
			cancel()
			return nil
		}}

		s := &session{bus: st, hooks: r.h.Store, as: "emma", addr: "127.0.0.1:6379"}
		var out bytes.Buffer
		res := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
		assert.Equal(t, tool.OK, res.Status, out.String())
		assert.Contains(t, out.String(), "push=antigravity id=msg-reconciled acknowledgment=model-answer")
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		fh := newFakeAntigravityHarness(t)
		w := fakeAntigravityWorld(r, fh)
		var out bytes.Buffer
		args := []string{"hook", "--as", "emma", "--role", "friend", "--harness", "antigravity", "--antigravity-dir", fh.workspaceDir, "--dry-run"}
		code := run(args, strings.NewReader(""), &out, &out, w)
		assert.Zero(t, code, out.String())
		assert.Contains(t, out.String(), "HOOK DRY-RUN")
		assert.Equal(t, hook.StateNone, r.h.State(t, "emma").State)
	})
}

func TestAntigravityPendingSummaryDoesNotHideCurrentChallenge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, nonce string
		malformed   bool
		wrongEpoch  bool
		wantDeliver bool
	}{
		{"current", "current-nonce", false, false, true},
		{"stale", "old-nonce", false, false, false},
		{"lookalike", "current-nonce", true, false, false},
		{"wrong epoch", "current-nonce", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			fh := newFakeAntigravityHarness(t)
			w := fakeAntigravityWorld(r, fh)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
			epoch, _, err := r.h.Store.HookAs(ctx, "emma", hook.Comms, "test")
			require.NoError(t, err)
			require.NoError(t, r.h.Store.Renew(ctx, "emma", hook.Comms, hook.Bus, epoch, "test"))
			created, err := r.h.Store.Challenge(ctx, "emma", hook.Comms, epoch, []hook.Push{hook.Bus}, "current-nonce")
			require.NoError(t, err)
			require.True(t, created)
			if tc.wrongEpoch {
				require.NoError(t, r.h.Client.HSet(ctx, hook.ChallengeKey("emma"), "epoch", epoch+1).Err())
			}
			s := &session{bus: bus.Redis{C: r.h.Client}, hooks: r.h.Store, as: "emma", addr: "127.0.0.1:6379"}
			ag := &antigravityRoute{
				as:           "emma",
				workspaceDir: fh.workspaceDir,
				eventsDir:    fh.eventsDir,
				homeDir:      fh.home,
				conversation: fh.conversation,
				port:         fh.activePort,
				csrfToken:    fh.token,
				exec:         fh.exec,
			}
			require.NoError(t, installAntigravityRoute(ag))
			route := &codexRoute{directory: fh.eventsDir, antigravity: ag}
			route.startTurn(s, "summary-old", "turn-old")
			route.fold(bus.Message{ID: "ordinary-old", From: "rowan", Subject: "ordinary"})
			message := bus.Message{ID: "challenge-new", From: "rowan", Subject: "HOOK CHALLENGE nonce=" + tc.nonce, Body: challengeText("emma", tc.nonce, hook.ChallengeEvery)}
			if tc.malformed {
				message.Body += "external appendix"
			}
			saved := map[string]savedCodexEvent{}
			w.codexSave = func(dir, id string, data []byte) error {
				var event savedCodexEvent
				require.NoError(t, json.Unmarshal(data, &event))
				saved[id] = event
				return saveCodexEvent(dir, id, data)
			}
			var delivered []string
			w.antigravityDeliver = func(dctx context.Context, agRoute *antigravityRoute, id, dir string) (string, error) {
				delivered = append(delivered, id)
				event := saved[id]
				require.NotNil(t, event.Message)
				assert.Equal(t, "HOOK CHALLENGE nonce=current-nonce", event.Message.Subject)
				assert.Contains(t, event.Message.Body, "nova-hook prove --as emma --nonce current-nonce")
				assert.Equal(t, "emma", event.LocalReceiver.Identity)
				return deliverAntigravitySaved(dctx, agRoute, id, dir)
			}
			reads := 0
			s.bus = codexWaiter{Store: s.bus, read: func() []bus.Entry {
				reads++
				if reads == 1 {
					return []bus.Entry{{Entry: "1-1", Fields: message.Fields()}}
				}
				cancel()
				return nil
			}}
			result := w.loop(&tool.Call{Stdout: io.Discard}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
			assert.Equal(t, tool.OK, result.Status)
			if tc.wantDeliver {
				assert.Equal(t, []string{"challenge-new"}, delivered, "a current challenge needs a fresh native notice while the summary is pending")
				assert.Equal(t, 1, route.foldedCount, "priority delivery preserves ordinary coalescing")
			} else {
				assert.Empty(t, delivered, "stale and external lookalikes remain ordinary traffic")
				assert.Equal(t, 2, route.foldedCount)
			}
			assert.Equal(t, "summary-old", route.pendingID)
			assert.Equal(t, "turn-old", route.pendingTurn)
			assert.Equal(t, hook.StateLive, r.h.State(t, "emma").State, "native delivery never proves the challenge")
		})
	}
}
