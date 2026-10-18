package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServiceManager is an in-memory supervisor for unit tests with no real
// time or OS sockets. Killing a supervised client immediately respawns it
// until Remove (via nova-hook unhook) stops the service.
type fakeServiceManager struct {
	mu      sync.Mutex
	goos    string
	active  map[string]*fakeServiceUnit
	makeRun func(as string, args []string, ctx context.Context, ready func())
}

type fakeServiceUnit struct {
	name      string
	args      []string
	runs      int
	stopSuper context.CancelFunc
	killProc  context.CancelFunc
	done      chan struct{}
	readyCh   chan struct{}
}

func newFakeServiceManager(goos string, r *rig, baseWorld func() world) *fakeServiceManager {
	m := &fakeServiceManager{
		goos:   goos,
		active: make(map[string]*fakeServiceUnit),
	}
	m.makeRun = func(as string, args []string, procCtx context.Context, onArmed func()) {
		w := baseWorld()
		origGetenv := w.getenv
		w.getenv = func(k string) string {
			if k == serviceRunEnv {
				return "1"
			}
			if origGetenv != nil {
				return origGetenv(k)
			}
			return ""
		}
		w.service = m
		w.signals = func(context.Context) (context.Context, context.CancelFunc) {
			return procCtx, func() {}
		}
		w.wait = func(ctx context.Context, d time.Duration) {
			r.advance(d)
			select {
			case <-ctx.Done():
			default:
			}
		}
		notified := false
		out := &streamOut{
			each: func(l string) {
				if !notified && strings.HasPrefix(l, "HOOK ARMED") {
					notified = true
					onArmed()
				}
			},
		}
		_ = run(append([]string{"hook"}, args...), strings.NewReader(""), out, io.Discard, w)
	}
	return m
}

func (m *fakeServiceManager) Held(as string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.active[as]
	if !ok {
		return "", false
	}
	return u.name, true
}

func (m *fakeServiceManager) Install(as string, args []string) (string, error) {
	m.mu.Lock()
	if existing, ok := m.active[as]; ok {
		m.mu.Unlock()
		return "", fmt.Errorf("%s already held by %s", as, existing.name)
	}
	superCtx, stopSuper := context.WithCancel(context.Background())
	u := &fakeServiceUnit{
		name:      hookServiceName(m.goos, as),
		args:      append([]string(nil), args...),
		stopSuper: stopSuper,
		done:      make(chan struct{}),
		readyCh:   make(chan struct{}, 16),
	}
	m.active[as] = u
	m.mu.Unlock()

	go func() {
		defer close(u.done)
		for {
			if superCtx.Err() != nil {
				return
			}
			procCtx, killProc := context.WithCancel(superCtx)
			m.mu.Lock()
			u.killProc = killProc
			u.runs++
			m.mu.Unlock()

			m.makeRun(as, u.args, procCtx, func() {
				select {
				case u.readyCh <- struct{}{}:
				default:
				}
			})
			killProc()
			if superCtx.Err() != nil {
				return
			}
		}
	}()

	<-u.readyCh
	return u.name, nil
}

// Kill simulates an unexpected crash/kill of the supervised hook client
// process and waits for the supervisor to respawn it.
func (m *fakeServiceManager) Kill(as string) {
	m.mu.Lock()
	u, ok := m.active[as]
	var kill context.CancelFunc
	if ok {
		kill = u.killProc
	}
	m.mu.Unlock()
	if kill != nil {
		kill()
		<-u.readyCh
	}
}

func (m *fakeServiceManager) Runs(as string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.active[as]; ok {
		return u.runs
	}
	return 0
}

func (m *fakeServiceManager) Remove(as string) error {
	m.mu.Lock()
	u, ok := m.active[as]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.active, as)
	stop := u.stopSuper
	kill := u.killProc
	done := u.done
	m.mu.Unlock()
	if kill != nil {
		kill()
	}
	if stop != nil {
		stop()
	}
	<-done
	return nil
}

func TestSupervisedServiceKillRespawnsAndInteractiveHookRefuses(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	svc := newFakeServiceManager("darwin", r, r.world)
	t.Cleanup(func() {
		// ignored: test cleanup after test finishes
		_ = svc.Remove("ada")
	})

	w := r.world()
	w.service = svc

	// 1. nova-hook hook --harness grok --as ada installs and starts ONE supervised service.
	var out bytes.Buffer
	code := run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home}, strings.NewReader(""), &out, io.Discard, w)
	require.Zero(t, code, out.String())
	assert.Contains(t, out.String(), "service=nova-hook.client.ada")
	assert.Equal(t, 1, svc.Runs("ada"))
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)

	// 2. An interactive nova-hook hook for a name a service holds refuses and names the service.
	var refuseOut, refuseErr bytes.Buffer
	code = run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), &refuseOut, &refuseErr, w)
	assert.Equal(t, 2, code)
	assert.Contains(t, refuseOut.String()+refuseErr.String(), "ada is already held by supervised service nova-hook.client.ada")

	// A second hook with --harness for the same name also refuses and names the service.
	refuseOut.Reset()
	refuseErr.Reset()
	code = run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home}, strings.NewReader(""), &refuseOut, &refuseErr, w)
	assert.Equal(t, 2, code)
	assert.Contains(t, refuseOut.String()+refuseErr.String(), "nova-hook.client.ada")

	// 3. Kill the client and it's back automatically under supervision.
	r.h.MR.FlushAll()
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "ada", "bob").Err())
	svc.Kill("ada")
	assert.GreaterOrEqual(t, svc.Runs("ada"), 2)
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State, "supervised client respawned and re-armed ada")

	// 4. Only nova-hook unhook --as ada stops the service and makes ada unhooked.
	var unhookOut bytes.Buffer
	code = run([]string{"unhook", "--as", "ada"}, strings.NewReader(""), &unhookOut, io.Discard, w)
	require.Zero(t, code, unhookOut.String())
	_, held := svc.Held("ada")
	assert.False(t, held, "unhook removed the supervised service")
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "ada").State)
}

func TestBigBacklogCutsOverAndCarriesOnWithoutRefusing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	ctx := context.Background()

	// Populate a backlog larger than maxCodexRecovery (128).
	total := maxCodexRecovery + 45
	var lastID string
	for i := 0; i < total; i++ {
		m := recoveryMessage(t, r, fmt.Sprintf("backlog-%d", i))
		lastID = m.ID
	}

	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var saved notice.Envelope
	w.files = filesFunc(func(_ string, data []byte, _ bool) error {
		return json.Unmarshal(data, &saved)
	})

	var out, errb bytes.Buffer
	code := run(codexArgs, strings.NewReader(""), &out, &errb, w)
	require.Zero(t, code, out.String()+errb.String())
	assert.Contains(t, out.String(), "state=recovery-cutover")
	assert.Contains(t, out.String(), "archived=45")
	require.Len(t, saved.Recovery, maxCodexRecovery)
	assert.Equal(t, lastID, saved.Recovery[len(saved.Recovery)-1].ID, "kept newest suffix after cutover")
	assert.NotEmpty(t, saved.RecoverySince)
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
	_ = ctx
}

func TestLostRedisConnectionRetriesWithBackoffInsteadOfExiting(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	origGetenv := w.getenv
	w.getenv = func(k string) string {
		if k == serviceRunEnv {
			return "1"
		}
		return origGetenv(k)
	}
	var waitSteps atomic.Int32
	w.wait = func(_ context.Context, d time.Duration) {
		waitSteps.Add(1)
		r.advance(d)
	}

	// Simulate initial Redis connection failures followed by recovery.
	var openCalls atomic.Int32
	origOpen := w.open
	w.open = func(ctx context.Context, addr string) (bus.Store, string, func(), error) {
		if openCalls.Add(1) <= 3 {
			return nil, "", nil, errors.New("connection refused")
		}
		return origOpen(ctx, addr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

	out := &streamOut{
		each: func(l string) {
			if strings.HasPrefix(l, "HOOK CHALLENGE") {
				cancel()
			}
		},
	}
	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, io.Discard, w)
	require.Zero(t, code, out.all.String())
	assert.Equal(t, int32(4), openCalls.Load())
	assert.GreaterOrEqual(t, waitSteps.Load(), int32(3))
	assert.Contains(t, out.all.String(), "HOOK STATE state=store-retry")
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
}

func TestOSServiceManagerWritesLaunchdPlistAndSystemdUnit(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			var ops []string
			mgr := newOSServiceManager(
				goos,
				func() (string, error) { return home, nil },
				func() (string, error) { return "/opt/nova/bin/nova-hook", nil },
				func(string) string { return "" },
				func(g, op, path string) error {
					ops = append(ops, g+":"+op+":"+filepath.Base(path))
					return nil
				},
			)
			_, held := mgr.Held("ada")
			assert.False(t, held)

			svc, err := mgr.Install("ada", []string{"--as", "ada", "--role", "friend", "--harness", "grok"})
			require.NoError(t, err)
			assert.Equal(t, hookServiceName(goos, "ada"), svc)

			gotSvc, held := mgr.Held("ada")
			assert.True(t, held)
			assert.Equal(t, svc, gotSvc)

			unitPath, err := mgr.(*osServiceManager).unitPath("ada")
			require.NoError(t, err)
			raw, err := os.ReadFile(unitPath)
			require.NoError(t, err)
			if goos == "darwin" {
				assert.Contains(t, string(raw), "<key>KeepAlive</key>")
				assert.Contains(t, string(raw), "NOVA_HOOK_SERVICE_RUN")
			} else {
				assert.Contains(t, string(raw), "Restart=always")
				assert.Contains(t, string(raw), "NOVA_HOOK_SERVICE_RUN=1")
			}

			require.NoError(t, mgr.Remove("ada"))
			_, held = mgr.Held("ada")
			assert.False(t, held)
			assert.Len(t, ops, 2)
		})
	}
}

func TestRecordServiceFailureRemovesInstalledService(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	svc := newFakeServiceManager("darwin", r, r.world)
	t.Cleanup(func() {
		// ignored: test cleanup after test finishes
		_ = svc.Remove("ada")
	})

	svc.makeRun = func(_ string, _ []string, procCtx context.Context, onArmed func()) {
		r.h.MR.SetError("ERR store write failed")
		onArmed()
		<-procCtx.Done()
	}

	w := r.world()
	w.service = svc
	var out, errb bytes.Buffer
	code := run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home}, strings.NewReader(""), &out, &errb, w)
	r.h.MR.SetError("")
	assert.Equal(t, 2, code)
	assert.Contains(t, out.String()+errb.String(), "the hook record could not be written")
	_, held := svc.Held("ada")
	assert.False(t, held, "compensating Remove must clean up installed service on recordService failure")
}

// TestMidLoopLeaseRenewalErrorRetriesWithBackoff pins the hook loop's renewal
// retry: a renewal that fails is tried again after retryBackoff(attempt)
// (tick, then 2 ticks), and a renewal that succeeds clears the fault. Time is
// the rig's clock, moved only by the loop's own waits (w.wait): no real sleep.
// The first wait after the fault jumps the clock to the renewal deadline
// (hook.RenewEvery), so the loop does not walk 30s of 1ms ticks against the
// store to get there.
func TestMidLoopLeaseRenewalErrorRetriesWithBackoff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }

	faulted := false
	unrenewedLines := 0
	var unrenewedAt []time.Time
	waitsAfterSecond := 0
	w.wait = func(_ context.Context, d time.Duration) {
		if faulted && unrenewedLines == 0 {
			r.advance(hook.RenewEvery) // the loop's next renewal falls due inside this wait
			return
		}
		r.advance(d)
		if unrenewedLines >= 2 {
			waitsAfterSecond++
			if waitsAfterSecond >= 2 {
				r.h.MR.SetError("")
			}
		}
	}

	out := &streamOut{
		each: func(l string) {
			if strings.HasPrefix(l, "HOOK CHALLENGE") {
				faulted = true
				r.h.MR.SetError("ERR transient redis renewal fault")
			}
			if strings.Contains(l, "state=unrenewed") {
				unrenewedLines++
				unrenewedAt = append(unrenewedAt, r.now())
			}
			if strings.Contains(l, "state=read") {
				cancel()
			}
		},
	}

	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, io.Discard, w)
	r.h.MR.SetError("")
	require.Zero(t, code, out.all.String())
	assert.Equal(t, 2, unrenewedLines)
	require.Len(t, unrenewedAt, 2)
	assert.Equal(t, w.retryBackoff(0), unrenewedAt[1].Sub(unrenewedAt[0]), "the first retry came one backoff (1 tick) after the first failure")
	assert.Equal(t, 2*w.tick, w.retryBackoff(1), "the second backoff is 2 ticks (exponential)")
	assert.Equal(t, 2, waitsAfterSecond, "second retry waited 2 ticks (exponential backoff)")
	assert.Contains(t, out.all.String(), "state=unrenewed")
	assert.Contains(t, out.all.String(), "state=read")
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
}

// TestTheClientServiceRestartsNoFasterThanTenSecondsAndNamesNoTempPath pins
// finding 6 of 2026-10-17: aurora's client exited 2 on a refusal of its own
// configuration (a /tmp muse bundle that was gone) and launchd restarted it
// once a second for ever. The unit waits ten seconds between starts, and a
// login item naming a path under a temporary directory is refused at install,
// the binary's own path included; nothing is written.
func TestTheClientServiceRestartsNoFasterThanTenSecondsAndNamesNoTempPath(t *testing.T) {
	t.Parallel()
	assert.Contains(t, hookServiceText("darwin", "ada", "/opt/nova/bin/nova-hook", nil, ""), "<key>ThrottleInterval</key>\n\t<integer>10</integer>")
	assert.Contains(t, hookServiceText("linux", "ada", "/opt/nova/bin/nova-hook", nil, ""), "RestartSec=10\n")
	for _, tc := range []struct {
		exe  string
		args []string
		bad  string
	}{
		{"/opt/nova/bin/nova-hook", []string{"--as", "aurora", "--harness", "Muse", "--muse-bundle", "/tmp/muse-bundle-x"}, "/tmp/muse-bundle-x"},
		{"/opt/nova/bin/nova-hook", []string{"--muse-bundle", "/private/tmp/b"}, "/private/tmp/b"},
		{"/var/folders/zz/T/go-build1/exe/nova-hook", []string{"--as", "ada"}, "/var/folders/zz/T/go-build1/exe/nova-hook"},
		{"/opt/nova/bin/nova-hook", []string{"--as", "ada", "--muse-bundle", "/opt/nova/muse-bundle", "--codex-since", "tmp"}, ""},
		{"/opt/nova/bin/nova-hook", []string{"--grok-home", "/tmpx/home"}, ""},
	} {
		home := t.TempDir()
		wrote := 0
		mgr := newOSServiceManager("darwin", func() (string, error) { return home, nil }, func() (string, error) { return tc.exe, nil },
			func(string) string { return "" }, func(string, string, string) error { wrote++; return nil })
		_, err := mgr.Install("ada", tc.args)
		if tc.bad == "" {
			assert.NoError(t, err, "%v", tc.args)
			continue
		}
		require.Error(t, err, "%v", tc.args)
		assert.Contains(t, err.Error(), tc.bad+" is under a temporary directory")
		_, held := mgr.Held("ada")
		assert.False(t, held, "a refused install writes no unit")
		assert.Zero(t, wrote, "and loads nothing")
	}
}
