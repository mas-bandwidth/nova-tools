package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func mockHealthyOutside() outside {
	return outside{
		serverAddr: func() (string, bool) {
			return "127.0.0.1:7399", false
		},
		roundTrip: func(ctx context.Context, addr string) (time.Duration, error) {
			return 5 * time.Millisecond, nil
		},
		listenerPID: func(port string) int {
			return 4242
		},
		dbsize: func(ctx context.Context, addr string) int64 {
			return 100
		},
		httpGet: func(ctx context.Context, url string) (int, []byte, error) {
			return 200, []byte(`{"build":"nova-sprint dev"}`), nil
		},
		ping: func(ctx context.Context, addr string) error {
			return nil
		},
		hostname: func() string {
			return "test-host"
		},
		devMergeQueue: func(ctx context.Context) (sprint.QueueM, error) {
			return sprint.QueueM{Entries: []string{"pr-1"}, Green: 1}, nil
		},
		machineVersions: func(ctx context.Context, machines []string) (sprint.VersionsM, error) {
			return sprint.VersionsM{Dev: "abc1234", Machines: []sprint.MachineVersionM{{Machine: "m1", Version: "abc1234"}}}, nil
		},
	}
}

func TestSeatCheckVerbAllOK(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	ta.a.outside = mockHealthyOutside()

	out := ta.ok("seat check")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.NotEmpty(t, lines)

	// Every line must start with MACHINERY and have OK status.
	for _, l := range lines {
		assert.True(t, strings.HasPrefix(l, sprint.SeatCheckToken+" "), "line must start with MACHINERY: %s", l)
		assert.Contains(t, l, " OK", "line must indicate OK: %s", l)
	}
}

func TestSeatCheckVerbDownExits1(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	o := mockHealthyOutside()
	o.roundTrip = func(ctx context.Context, addr string) (time.Duration, error) {
		return 0, fmt.Errorf("connection refused")
	}
	ta.a.outside = o

	code, out, _ := ta.do("seat check")
	assert.Equal(t, 1, code, "seat check must exit 1 when a check is DOWN")
	assert.Contains(t, out, "MACHINERY server DOWN", "server check must be reported DOWN")
}

func TestSeatCheckJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	ta.a.outside = mockHealthyOutside()

	out := ta.ok("seat check --json")
	var report sprint.SeatCheckReport
	err := json.Unmarshal([]byte(out), &report)
	require.NoError(t, err)
	assert.Equal(t, 0, report.Down)
	assert.Equal(t, 0, report.ExitCode)
	assert.NotEmpty(t, report.Lines)
}

func TestMachineryVerbAlias(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	ta.a.outside = mockHealthyOutside()

	out := ta.ok("machinery")
	assert.Contains(t, out, "MACHINERY server OK")
}

func TestSeatCheckRefusesPositionalArgs(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.outside = mockHealthyOutside()

	code, _, errb := ta.do("seat check unexpected")
	assert.Equal(t, 2, code)
	assert.Contains(t, errb, "takes no words")
}

func TestRealOutsideQueueAndVersionsOmitted(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	o := ta.a.realOutside()
	assert.Nil(t, o.devMergeQueue, "dev merge queue probe must be omitted in realOutside to prevent always-error DOWN")
	assert.Nil(t, o.machineVersions, "machine versions probe must be omitted in realOutside to prevent always-error DOWN")

	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	// Test that nil probes yield "not measured", not false OK entries=0 or machines=0
	healthy := mockHealthyOutside()
	healthy.devMergeQueue = nil
	healthy.machineVersions = nil
	ta.a.outside = healthy

	out := ta.ok("seat check")
	assert.Contains(t, out, `MACHINERY queue OK note="not measured: probe not configured"`)
	assert.NotContains(t, out, "entries=0")
	assert.Contains(t, out, `MACHINERY versions OK note="not measured: probe not configured"`)
	assert.NotContains(t, out, "machines=0")
}

// The seat check reads this machine's process table and prints a STOPGAP line
// for every stopgap alive (docs/STOPGAPS.md); the server's own check reads none.
func TestSeatCheckPrintsAStopgapStillRunning(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	o := mockHealthyOutside()
	o.processes = func(ctx context.Context) ([]sprint.Proc, error) {
		return sprint.ProcsFromPS(" 501 /bin/zsh /buds/rowan-space/runner.zsh\n 502 python3 /scratch/finish-loop.py\n 503 /bin/ps -axww\n"), nil
	}
	ta.a.outside = o

	out := ta.ok("seat check")
	assert.Contains(t, out, "\nSTOPGAP runner.zsh still running pids=501 state=landed card=claude-oneshot-lanes ")
	assert.Contains(t, out, "\nSTOPGAP finish-loop.py still running pids=502 state=landed card=collect-is-a-verb-and-the-daemons-duty ")
	assert.Contains(t, ta.ok("seat check --json"), `"stopgaps":[{"stopgap":{"name":"runner.zsh"`)

	o.serverAddr = func() (string, bool) { return "127.0.0.1:7399", true }
	o.processes = func(ctx context.Context) ([]sprint.Proc, error) {
		t.Error("the server's own check read the process table")
		return nil, nil
	}
	ta.a.outside = o
	assert.NotContains(t, ta.ok("seat check"), sprint.StopgapToken)
}

func TestSeatCheckReportsFailedStopgapProcessScan(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	o := mockHealthyOutside()
	o.processes = func(context.Context) ([]sprint.Proc, error) {
		return nil, fmt.Errorf("ps unavailable")
	}
	ta.a.outside = o
	code, out, errs := ta.do("seat check")
	require.Equal(t, 1, code, errs)
	assert.Contains(t, out, `MACHINERY stopgaps DOWN why="process scan failed: ps unavailable" remedy="ps -axww -o pid=,args="`)
	assert.Contains(t, out, "MACHINERY DOWN")
	code, out, errs = ta.do("seat check --json")
	require.Equal(t, 1, code, errs)
	assert.Contains(t, out, `"exit_code":1`)
	assert.Contains(t, out, `"thing":"stopgaps"`)
}
