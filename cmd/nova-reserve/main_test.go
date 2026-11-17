package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testEpoch = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

type testHarness struct {
	dir     string
	out     bytes.Buffer
	err     bytes.Buffer
	envs    map[string]string
	code    int
	lastCmd []string
	lastEnv []string
	lowered bool
	srv     *Server
	w       world
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	h := &testHarness{
		dir:  t.TempDir(),
		envs: map[string]string{"PATH": "/usr/bin", "USER": "alice"},
	}
	h.w = world{
		env: func(k string) string { return h.envs[k] },
		environ: func() []string {
			var out []string
			for k, v := range h.envs {
				out = append(out, k+"="+v)
			}
			return out
		},
		now:     func() time.Time { return testEpoch },
		resolve: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		exec: func(path string, argv, env []string, lower bool) int {
			h.lastCmd = append([]string{path}, argv...)
			h.lastEnv = slices.Clone(env)
			h.lowered = lower
			return h.code
		},
		waitEOF: func(in io.Reader) {
			if in != nil {
				_, _ = io.ReadAll(in)
			}
		},
		bench:  func(cores int, force bool) (int, int, error) { return cores, 16, nil },
		pid:    42,
		cpus:   8,
		goos:   "linux",
		goarch: "amd64",
		usable: func() ([]int, error) { return nil, nil },
		groups: smtGroups,
		pin:    func([]int) error { return nil },
		stdin:  strings.NewReader(""),
		stdout: &h.out,
		stderr: &h.err,
	}
	return h
}

func (h *testHarness) setCap(t *testing.T, cpu, mem, cores int) {
	t.Helper()
	var so, se bytes.Buffer
	w := h.w
	w.stdout, w.stderr = &so, &se
	require.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--cpu", itoa(cpu), "--memory", itoa(mem), "--cores", itoa(cores)}, w), se.String())
}

func (h *testHarness) startInMemoryLocalServer(t *testing.T, usable []int, groups [][]int) {
	t.Helper()
	c, ok, err := readCapacity(h.dir)
	require.NoError(t, err)
	require.True(t, ok)
	m := MachineSpec{
		Host: "local", Online: true, Tags: effectiveTags(c.Tags, "linux", "amd64"),
		UsableCores: c.UsableCores, Resources: c.Resources, UsableCPUs: usable, SMTGroups: groups,
	}
	srv := NewServer(ServerConfig{Local: true, Dir: h.dir}, []MachineSpec{m})
	if fi, serr := os.Stat(capPath(h.dir)); serr == nil {
		srv.capMtime = fi.ModTime().UnixNano()
	}
	t.Cleanup(srv.Close)
	h.srv = srv
	h.w.dial = func(ep endpoint, o options) (net.Conn, error) {
		cConn, sConn := net.Pipe()
		go srv.handleConn(sConn)
		return cConn, nil
	}
}

func (h *testHarness) startInMemoryFleetServer(t *testing.T, password string, machines []MachineSpec) *Server {
	t.Helper()
	srv := NewServer(ServerConfig{Local: false, Password: password}, machines)
	t.Cleanup(srv.Close)
	h.srv = srv
	h.envs["NOVA_RESERVE_ADDR"] = "studio:7680"
	h.envs["NOVA_RESERVE_PASSWORD"] = password
	h.w.dial = func(ep endpoint, o options) (net.Conn, error) {
		cConn, sConn := net.Pipe()
		go srv.handleConn(sConn)
		return cConn, nil
	}
	return srv
}

func (h *testHarness) sibling(pid int) *testHarness {
	s := &testHarness{
		dir:  h.dir,
		envs: map[string]string{"PATH": "/usr/bin", "USER": "bob"},
		srv:  h.srv,
		w:    h.w,
	}
	s.w.pid = pid
	s.w.stdout = &s.out
	s.w.stderr = &s.err
	return s
}

func (h *testHarness) startHold(t *testing.T, extraArgs ...string) func() {
	t.Helper()
	pr, pw := io.Pipe()
	ready := make(chan struct{})
	var once sync.Once
	w := h.w
	w.stdin = pr
	w.stdout = writerFunc(func(p []byte) (int, error) {
		n, err := h.out.Write(p)
		if strings.Contains(h.out.String(), "\n") {
			once.Do(func() { close(ready) })
		}
		return n, err
	})
	var done sync.WaitGroup
	done.Add(1)
	args := append([]string{"hold", "--dir", h.dir}, extraArgs...)
	go func() {
		defer done.Done()
		run(args, w)
	}()
	<-ready
	return func() {
		_ = pw.Close()
		done.Wait()
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func itoa(n int) string {
	return jsonNumber(n)
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestParseVerbsFlagsAndRefusals(t *testing.T) {
	t.Parallel()
	o, err := parse([]string{"--needs", "cpu 4, memory 8, linux", "--holder", "friend", "--source", "alice", "--", "go", "test", "./..."})
	require.NoError(t, err)
	assert.Equal(t, "run", o.verb)
	assert.Equal(t, "", o.rawVerb)
	assert.Equal(t, "friend", o.holder)
	assert.Equal(t, "alice", o.source)
	assert.Equal(t, "work", o.label)
	assert.Equal(t, []string{"go", "test", "./..."}, o.argv)

	// Alias calibrate / measure -> bench.
	o2, err := parse([]string{"calibrate", "--force", "--write-config"})
	require.NoError(t, err)
	assert.Equal(t, "bench", o2.verb)
	assert.True(t, o2.force)
	assert.True(t, o2.writeConfig)

	// Invalid combinations exit 2 with clean single-line refusal.
	badCases := [][]string{
		{"no-such-verb"},
		{"--exclusive", "--needs", "cpu 2", "--", "true"},
		{"--nowait", "--wait", "5s", "--", "true"},
		{"hold", "--", "true"},
		{"release"},
		{"set"},
		{"--holder", "invalid", "--", "true"},
		{"--priority", "urgent", "--", "true"},
		{"--needs", "cpu 0", "--", "true"},
		{"ssh"},
	}
	for _, tc := range badCases {
		h := newHarness(t)
		assert.Equal(t, 2, run(tc, h.w), "case %v", tc)
		assert.NotEmpty(t, h.err.String())
		assert.Empty(t, h.out.String())
	}
}

func TestEngineMultiHostFleetAnyAndHostPinnedAdmission(t *testing.T) {
	t.Parallel()
	eng := NewEngine([]MachineSpec{
		{Host: "b1", Address: "100.64.0.1", User: "op", Online: true, Tags: []string{"linux", "amd64", "ps5-sdk"}, UsableCores: 16, Resources: map[string]int{"cpu": 8, "memory": 16, "ps5": 1}},
		{Host: "b2", Address: "100.64.0.2", User: "op", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 32, Resources: map[string]int{"cpu": 24, "memory": 64}},
		{Host: "m1", Address: "100.64.0.3", User: "op", Online: true, Tags: []string{"mac", "arm64", "metal"}, UsableCores: 12, Resources: map[string]int{"cpu": 12, "memory": 32}},
	}, 1_000, 0)

	// Contradictory tags ("linux, metal") refuse immediately with code="impossible".
	msgs := eng.Request(1, ClientFrame{Op: "request", ReqID: "r0", Mode: "any", Needs: NeedsSpec{Raw: "cpu 2, linux, metal"}}, 1_000)
	require.Equal(t, "refused", msgs[0].Frame.Op)
	assert.Equal(t, "impossible", msgs[0].Frame.Code)

	// Fleet-any linux request picks b2 (highest CPU headroom: 24-4=20 vs 8-4=4).
	msgs = eng.Request(1, ClientFrame{Op: "request", ReqID: "r1", Mode: "any", Needs: NeedsSpec{Raw: "cpu 4, memory 8, linux"}, Holder: "friend", Source: "alice"}, 1_000)
	require.Equal(t, "granted", msgs[0].Frame.Op)
	assert.Equal(t, "b2", msgs[0].Frame.Host)
	assert.Equal(t, 5, msgs[0].Frame.Cores) // floor(4 * 32 / 24) = 5

	// Bare countable hardware resource "ps5" means amount 1 and routes to b1; a second ps5 queues!
	msgs = eng.Request(2, ClientFrame{Op: "request", ReqID: "r2", Mode: "any", Needs: NeedsSpec{Raw: "cpu 2, ps5"}, Holder: "friend", Source: "bob"}, 1_000)
	require.Equal(t, "granted", msgs[0].Frame.Op)
	assert.Equal(t, "b1", msgs[0].Frame.Host)
	assert.Equal(t, 1, msgs[0].Frame.Needs["ps5"])

	msgs = eng.Request(3, ClientFrame{Op: "request", ReqID: "r3", Mode: "any", Needs: NeedsSpec{Raw: "ps5"}, Holder: "friend", Source: "carol"}, 1_000)
	require.Equal(t, "queued", msgs[0].Frame.Op)

	// Releasing conn 2 immediately grants queued r3 on b1!
	out := eng.CloseConn(2, 1_500)
	require.True(t, slices.ContainsFunc(out, func(m OutMsg) bool {
		return m.ConnID == 3 && m.Frame.Op == "granted" && m.Frame.Host == "b1"
	}))
}

func TestEngineCIPauseVictimOrderAndPausedParentSubleaseGate(t *testing.T) {
	t.Parallel()
	eng := NewEngine([]MachineSpec{
		{Host: "h1", Online: true, Tags: []string{"linux"}, UsableCores: 16, Resources: map[string]int{"cpu": 8, "memory": 16}},
	}, 1_000, 0)

	// Conn 1 (high prio, cpu 4), Conn 2 (normal prio older, cpu 2), Conn 3 (normal prio younger, cpu 2).
	m1 := eng.Request(1, ClientFrame{Op: "request", ReqID: "r1", Host: "h1", Needs: NeedsSpec{Raw: "cpu 4, memory 4"}, Holder: "bench", Source: "w1", Priority: "high"}, 1_000)
	l1 := m1[0].Frame.LeaseID
	m2 := eng.Request(2, ClientFrame{Op: "request", ReqID: "r2", Host: "h1", Needs: NeedsSpec{Raw: "cpu 2, memory 4"}, Holder: "bench", Source: "w2", Priority: "normal"}, 1_000)
	l2 := m2[0].Frame.LeaseID
	m3 := eng.Request(3, ClientFrame{Op: "request", ReqID: "r3", Host: "h1", Needs: NeedsSpec{Raw: "cpu 2, memory 4"}, Holder: "bench", Source: "w3", Priority: "normal"}, 1_000)
	l3 := m3[0].Frame.LeaseID

	// Queued blocker request (Conn 4) arrives while h1 is full.
	m4 := eng.Request(4, ClientFrame{Op: "request", ReqID: "r4", Host: "h1", Needs: NeedsSpec{Raw: "cpu 2, memory 2"}, Holder: "friend", Source: "alice", Priority: "blocker"}, 1_000)
	require.Equal(t, "queued", m4[0].Frame.Op)

	// CI arrives needing cpu 3: MUST pause youngest normal victim (l3) then older normal victim (l2), keeping high-prio l1 unpaused!
	ciMsgs := eng.Request(5, ClientFrame{Op: "request", ReqID: "ci1", Host: "h1", Needs: NeedsSpec{Raw: "cpu 3, memory 4"}, Holder: "ci"}, 1_100)
	var pausedIDs []string
	var ciID string
	for _, m := range ciMsgs {
		if m.Frame.Op == "state" && m.Frame.State == "paused" {
			pausedIDs = append(pausedIDs, m.Frame.LeaseID)
		}
		if m.Frame.Op == "granted" {
			ciID = m.Frame.LeaseID
		}
	}
	assert.Equal(t, []string{l3, l2}, pausedIDs)
	assert.False(t, eng.findLease(l1).Paused)

	// While l2 is paused, an inner sub-step carrying ParentLease=l2 yields at the command boundary (queued, NOT granted and NOT deadlocked!).
	subMsgs := eng.Request(6, ClientFrame{Op: "request", ReqID: "sub2", Host: "h1", Needs: NeedsSpec{Raw: "cpu 1, memory 2"}, ParentLease: l2, Holder: "bench", Source: "w2"}, 1_200)
	require.Equal(t, "queued", subMsgs[0].Frame.Op)

	// When CI releases, l2 and l3 MUST resume ahead of queued blocker r4, AND sub2 wakes immediately as a 0-debit sublease of l2!
	relMsgs, ok := eng.Release(5, ciID, 1_500)
	require.True(t, ok)
	assert.False(t, eng.findLease(l2).Paused)
	assert.False(t, eng.findLease(l3).Paused)
	assert.True(t, slices.ContainsFunc(relMsgs, func(m OutMsg) bool {
		return m.ConnID == 6 && m.Frame.Op == "granted" && m.Frame.Sublease
	}))
	// Queued r4 is STILL waiting because resumed l1+l2+l3 occupy all 8 CPUs!
	assert.Len(t, eng.queue, 1)
	assert.Equal(t, "r4", eng.queue[0].ReqID)
}

func TestEngineSubleasePPIDAndTwoHostABBADeadlock(t *testing.T) {
	t.Parallel()
	eng := NewEngine([]MachineSpec{
		{Host: "b1", Online: true, Tags: []string{"linux"}, UsableCores: 8, Resources: map[string]int{"cpu": 4, "memory": 8, "ps5": 1}},
		{Host: "b2", Online: true, Tags: []string{"linux"}, UsableCores: 8, Resources: map[string]int{"cpu": 4, "memory": 8, "gpu": 1}},
	}, 1_000, 0)

	// Lease A on b1 holds ps5=1 (PID 500).
	aMsgs := eng.Request(1, ClientFrame{Op: "request", ReqID: "a", Host: "b1", Needs: NeedsSpec{Raw: "cpu 2, memory 4, ps5"}, Holder: "friend", Source: "alice", Why: "ps5 suite", PID: 500}, 1_000)
	la := aMsgs[0].Frame.LeaseID

	// Env-scrubbed descendant (ParentLease="", Ancestors=[501, 500]) asking for cpu 1, memory 2 on b1 recovers ParentLease via PPID chain and gets a 0-debit sublease!
	sub := eng.Request(10, ClientFrame{Op: "request", ReqID: "sub", Host: "b1", Needs: NeedsSpec{Raw: "cpu 1, memory 2"}, Holder: "script", Source: "alice", PID: 502, Ancestors: []int{501, 500}}, 1_050)
	require.Equal(t, "granted", sub[0].Frame.Op)
	assert.True(t, sub[0].Frame.Sublease)
	assert.Equal(t, 2, eng.sumUnpausedOn("b1")["cpu"]) // still 2 (0 extra host debit!)

	// Lease B on b2 holds gpu=1.
	bMsgs := eng.Request(2, ClientFrame{Op: "request", ReqID: "b", Host: "b2", Needs: NeedsSpec{Raw: "cpu 2, memory 4, gpu"}, Holder: "friend", Source: "bob", Why: "gpu bench"}, 1_100)
	lb := bMsgs[0].Frame.LeaseID

	// Child of A asks for gpu on b2 -> NOT a cycle yet (B is not waiting on anything), so it queues cleanly!
	aChild := eng.Request(11, ClientFrame{Op: "request", ReqID: "a-wants-gpu", Host: "b2", Needs: NeedsSpec{Raw: "gpu"}, ParentLease: la, Holder: "friend", Source: "alice"}, 1_200)
	require.Equal(t, "queued", aChild[0].Frame.Op)

	// Now child of B asks for ps5 on b1 -> closes 2-party AB-BA cycle (B -> b-wants-ps5 -> A -> a-wants-gpu -> B)! Refused immediately!
	bChild := eng.Request(12, ClientFrame{Op: "request", ReqID: "b-wants-ps5", Host: "b1", Needs: NeedsSpec{Raw: "ps5"}, ParentLease: lb, Holder: "friend", Source: "bob"}, 1_300)
	require.Equal(t, "refused", bChild[0].Frame.Op)
	assert.Equal(t, "deadlock", bChild[0].Frame.Code)
	assert.Contains(t, bChild[0].Frame.Message, "RESERVE DEADLOCK:")

	// Fleet-any non-deadlock check: if A holds all of b1 (cpu 4), and independent Conn 9 holds all of b2 (cpu 4),
	// a child of A asking for fleet-any "cpu 4, linux" does NOT deadlock because Conn 9 on b2 is unstuck and will finish!
	eng.CloseConn(11, 1_400)
	eng.CloseConn(2, 1_400)
	eng.Request(13, ClientFrame{Op: "request", ReqID: "fill-b1", Host: "b1", Needs: NeedsSpec{Raw: "cpu 2, memory 4"}, Holder: "bench", Source: "w1"}, 1_410)
	eng.Request(9, ClientFrame{Op: "request", ReqID: "fill-b2", Host: "b2", Needs: NeedsSpec{Raw: "cpu 4, memory 8"}, Holder: "bench", Source: "w2"}, 1_420)
	anyChild := eng.Request(14, ClientFrame{Op: "request", ReqID: "a-any", Mode: "any", Needs: NeedsSpec{Raw: "cpu 4, linux"}, ParentLease: la, Holder: "friend", Source: "alice"}, 1_430)
	require.Equal(t, "queued", anyChild[0].Frame.Op)
}

func TestEngineSoftClaimsDeadPeerExpiryAndShrinkSafeReload(t *testing.T) {
	t.Parallel()
	eng := NewEngine([]MachineSpec{
		{Host: "space", Online: true, Tags: []string{"linux"}, UsableCores: 16, Resources: map[string]int{"cpu": 16, "memory": 32}},
	}, 1_000, 0)
	eng.RegisterConn(1, 1_000)

	// Active lease holds cpu 12, memory 24.
	g := eng.Request(1, ClientFrame{Op: "request", ReqID: "r1", Host: "space", Needs: NeedsSpec{Raw: "cpu 12, memory 24"}, Holder: "bench", Source: "w1"}, 1_000)
	require.Equal(t, "granted", g[0].Frame.Op)

	// Operator places soft claim for cpu 12, memory 24 (10s TTL): succeeds immediately, free headroom goes negative (-8), active lease NOT killed!
	cl := eng.PlaceClaim(2, ClientFrame{Op: "claim", ReqID: "c1", Host: "space", Needs: NeedsSpec{Raw: "cpu 12, memory 24"}, TTLMs: 10_000, Source: "op", Why: "profiling"}, 1_500)
	require.Equal(t, "granted", cl[0].Frame.Op)
	snap := eng.Snapshot("space", 1_500)
	assert.Equal(t, -8, snap.Machines[0].Free["cpu"])

	// Live config reload shrinks space down to cpu 8, memory 16 while 12-CPU lease is still running: active lease preserved!
	eng.UpdateMachines([]MachineSpec{
		{Host: "space", Online: true, Tags: []string{"linux"}, UsableCores: 8, Resources: map[string]int{"cpu": 8, "memory": 16}},
	}, 2_000)
	assert.Len(t, eng.leases, 1)

	// Claim expires at t = 11_500; silent Conn 1 expires after 15s (t = 16_000) -> host completely free!
	eng.Tick(11_500)
	assert.Empty(t, eng.claims)
	eng.Tick(16_000)
	assert.Empty(t, eng.leases)
	snap2 := eng.Snapshot("space", 16_000)
	assert.Equal(t, 8, snap2.Machines[0].Free["cpu"])
}

func TestCLIFleetRunSSHClaimReleaseAndStatusOverPipe(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startInMemoryFleetServer(t, "s3cr3t", []MachineSpec{
		{Host: "space", Address: "100.64.0.9", User: "op", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 16, Resources: map[string]int{"cpu": 16, "memory": 32}},
	})

	// 1. nova-reserve ssh routes fleet-any to space, invokes ssh op@100.64.0.9 with env steering!
	require.Equal(t, 0, run([]string{"ssh", "--needs", "cpu 4, memory 8, linux", "--holder", "friend", "--source", "alice", "--", "go", "test", "./..."}, h.w), h.err.String())
	assert.Equal(t, "/usr/bin/ssh", h.lastCmd[0])
	assert.Equal(t, "op@100.64.0.9", h.lastCmd[4])
	assert.Contains(t, h.lastCmd[5], "NOVA_RESERVE_HOST=space")
	assert.Contains(t, h.lastCmd[5], "NOVA_RESERVE_CPUS=4")
	assert.Contains(t, h.lastCmd[5], "GOMAXPROCS=4")
	assert.Contains(t, h.lastCmd[5], "MAKEFLAGS=-j4")
	assert.False(t, h.lowered)

	// 2. nova-reserve claim + status --json + status + release.
	h.out.Reset()
	h.err.Reset()
	require.Equal(t, 0, run([]string{"claim", "--host", "space", "--needs", "cpu 8, memory 16", "--ttl", "1h", "--why", "kernel test"}, h.w), h.err.String())
	assert.Contains(t, h.out.String(), "CLAIMED lease_id=C-1 host=space")

	h.out.Reset()
	require.Equal(t, 0, run([]string{"status", "--json"}, h.w), h.err.String())
	var sf ServerFrame
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &sf))
	assert.Equal(t, "snapshot", sf.Op)
	require.Len(t, sf.Machines, 1)
	assert.Len(t, sf.Machines[0].Claims, 1)
	assert.Equal(t, 8, sf.Machines[0].Free["cpu"])

	h.out.Reset()
	require.Equal(t, 0, run([]string{"status"}, h.w), h.err.String())
	assert.Contains(t, h.out.String(), "claim C-1 host=space")

	h.out.Reset()
	require.Equal(t, 0, run([]string{"release", "C-1"}, h.w), h.err.String())
	assert.Equal(t, "RELEASED lease_id=C-1\n", h.out.String())

	// 3. Bad password refuses with exit 2.
	h.out.Reset()
	h.err.Reset()
	h.envs["NOVA_RESERVE_PASSWORD"] = "wrong-pass"
	assert.Equal(t, 2, run([]string{"status"}, h.w))
	assert.Contains(t, h.err.String(), "authentication failed")
}
