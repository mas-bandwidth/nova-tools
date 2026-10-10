package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// TestFleetBeatMeasuresOpenFilesBesideTheLoad: fleet beat measures the machine's open
// file descriptors beside its load, given or measured, says the level against its warn
// and alarm bounds (--fd-warn, --fd-alarm, else NOVA_FD_WARN and NOVA_FD_ALARM, else the
// defaults) and, over the warn bound, the top holders; --json carries the reading; a
// bound that is not a count, or an alarm under the warn, is refused.
func TestFleetBeatMeasuresOpenFilesBesideTheLoad(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = nil
	open := 1000
	ta.a.meter.OpenFiles = func() (int, int, error) { return open, 491520, nil }
	ta.a.meter.Holders = func() ([]hostload.Holder, error) {
		return []hostload.Holder{{PID: 200, Command: "redis-server", User: "build", Open: 9000}, {PID: 300, Command: "node", User: "build", Open: 60000}}, nil
	}

	out := ta.ok("fleet beat m1")
	require.Contains(t, out, "how=load1 cores=4 fds=1000 fds-max=491520 fds-level=ok", "under the default warn bound")
	require.NotContains(t, out, "pid=", "no holders under the warn bound")

	open = 60000
	out = ta.ok("fleet beat m1 --load 12.5")
	require.Contains(t, out, "load=12.5% last=12.5% how=given cores=4 fds=60000 fds-max=491520 fds-level=warn", "a given load: the files measured still")
	require.Contains(t, out, "  node pid=300 user=build fds=60000\n  redis-server pid=200 user=build fds=9000\n", "the top holders, most first")

	out = ta.ok("fleet beat m1 --fd-warn 100000 --fd-alarm 200000")
	require.Contains(t, out, "fds=60000 fds-max=491520 fds-level=ok", "the bounds given")
	out = ta.ok("fleet beat m1 --fd-warn 10 --fd-alarm 50000")
	require.Contains(t, out, "fds-level=alarm", "over the alarm bound given")

	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		switch k {
		case "NOVA_FD_WARN":
			return "70000"
		case "NOVA_FD_ALARM":
			return "80000"
		}
		return env(k)
	}
	out = ta.ok("fleet beat m1")
	require.Contains(t, out, "fds-level=ok", "the bounds from the environment")

	var b beatReport
	ta.json("fleet beat m1 --fd-warn 50000", &b)
	require.NotNil(t, b.Files, "--json carries the reading: %+v", b)
	require.Equal(t, 60000, b.Files.Open)
	require.Equal(t, 50000, b.Files.Warn)
	require.Equal(t, 80000, b.Files.Alarm, "the alarm from the environment, the warn given")
	require.Equal(t, hostload.LevelWarn, b.Files.Level())
	require.Len(t, b.Files.Top, 2)

	for _, line := range []string{"fleet beat m1 --fd-warn 0", "fleet beat m1 --fd-alarm -1", "fleet beat m1 --fd-warn 100 --fd-alarm 50"} {
		code, _, errs := ta.do(line)
		require.Equal(t, 2, code, "%s: exit %d %q", line, code, errs)
		require.Contains(t, errs, "--fd-", "%s: %q", line, errs)
	}
	ta.a.getenv = func(k string) string {
		if k == "NOVA_FD_WARN" {
			return "lots"
		}
		return env(k)
	}
	code, _, errs := ta.do("fleet beat m1")
	require.Equal(t, 2, code, "a bound in the environment that is not a count: exit %d %q", code, errs)
	require.Contains(t, errs, "NOVA_FD_WARN wants a count", "%q", errs)

	// a machine that cannot count its descriptors says nothing of them
	ta.a.getenv = env
	ta.a.meter.OpenFiles = nil
	out = ta.ok("fleet beat m1")
	require.NotContains(t, out, "fds=", "no reading, no word")
}

// A served beat must never attach the server's descriptors to a remote member
// (docs/SPEC-SPRINT.md section 5, fleet).
func TestServedBeatDoesNotMeasureTheServersFiles(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	r.a.meter.OpenFiles = func() (int, int, error) { return 1000, 491520, nil }
	r.a.meter.Holders = nil
	r.boss("fleet beat m1 --load 5") // seed a reading that the served beat must clear
	st, err := r.a.store(common{redis: r.a.serveAddr, actor: "m1"})
	require.NoError(t, err)
	beats, err := st.Beats(context.Background(), []string{"m1"})
	require.NoError(t, err)
	require.NotNil(t, beats["m1"].Meter.Files)
	require.Equal(t, 1000, beats["m1"].Meter.Files.Open)
	r.a.meter.OpenFiles = func() (int, int, error) { return 200000, 491520, nil }
	r.a.meter.Holders = func() ([]hostload.Holder, error) {
		t.Error("the server must not read its holders for a member")
		return nil, nil
	}
	res := r.one("fleet", "beat", "m1", "--load", "5")
	require.Equal(t, 0, res.Code, res.Stderr)
	require.NotContains(t, res.Stdout, "fds=", "no caller reading was sent")
	beats, err = st.Beats(context.Background(), []string{"m1"})
	require.NoError(t, err)
	require.Nil(t, beats["m1"].Meter.Files, "the stored beat clears the previous server reading")
}
