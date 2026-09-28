package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

var psNow = time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

// psStore is an in-memory store holding three registered benches: hulk
// beating with a sample (a stray loop four days old, a young worker, an
// undeclared unit) and a deploy an hour ago; batman beating from a build
// with no sample; dead not beating at all. No process starts and no host is
// reached: the verb reads the beats only.
func psStore(t *testing.T) string {
	t.Helper()
	m := miniredis.RunT(t)
	at := psNow.Add(-3 * time.Second).Unix()
	sample := fleet.PSSample{
		At: at,
		Top: []fleet.PSProc{
			{PID: 90, CPU: 97.5, User: "nova", Start: at - 20, Cmd: "/home/nova/.local/bin/nova-card copy c1"},
		},
		Old: []fleet.PSProc{
			{PID: 11, CPU: 0, User: "nova", Start: at - 4*86400, Cmd: "bash /home/nova/deal-status --watch"},
			{PID: 90, CPU: 97.5, User: "nova", Start: at - 20, Cmd: "/home/nova/.local/bin/nova-card copy c1"},
		},
		OldN:  2,
		Units: []fleet.PSUnit{{Name: "nova-grok-heartbeat", State: fleet.UnitUndeclared}, {Name: "nova-loop-mirror-refresh", State: fleet.UnitDeclared}},
		UnitN: 2,
	}
	m.SAdd("benches", "hulk", "batman", "dead")
	m.HSet("bench:hulk:beat", "ps", sample.Encode(), "load1", "7.10", "ncpu", "16", "cpu", "44.0")
	m.HSet("bench:hulk", "build_at", psNow.Add(-time.Hour).Format(time.RFC3339))
	m.HSet("bench:batman:beat", "load1", "0.20", "build", "nova-sprint-old")
	return m.Addr()
}

func runPS(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runFleetPSAt(context.Background(), args, &out, &errOut, func() time.Time { return psNow })
	return code, out.String(), errOut.String()
}

// TestFleetPSFromTheBeats is #4338's DONE-WHEN for ps: every registered
// bench in name order from its beat, load and top processes and the
// undeclared units; a bench with no beat or no sample prints its line and
// the verb exits 1.
func TestFleetPSFromTheBeats(t *testing.T) {
	t.Parallel()
	addr := psStore(t)
	code, out, errOut := runPS(t, "--redis", addr)
	want := strings.Join([]string{
		"PS batman NOSAMPLE the beat carries no ps (build nova-sprint-old)",
		"PS dead NOBEAT bench:dead:beat is absent",
		"hulk load1=7.10 ncpu=16 cpu=44.0 sample=3s",
		"hulk top pid=90 cpu=97.5 user=nova age=20s cmd=/home/nova/.local/bin/nova-card copy c1",
		"hulk unit nova-grok-heartbeat undeclared",
		"PS hulk top=1 units=2 undeclared=1",
	}, "\n") + "\n"
	if code != 1 || out != want || errOut != "" {
		t.Fatalf("exit %d stderr %q\n%s\nwant\n%s", code, errOut, out, want)
	}
	code, out, _ = runPS(t, "--redis", addr, "--bench", "hulk")
	if code != 0 || !strings.HasSuffix(out, "PS hulk top=1 units=2 undeclared=1\n") || strings.Contains(out, "batman") {
		t.Fatalf("--bench hulk: exit %d\n%s", code, out)
	}
}

// TestFleetPSStray is #4338's DONE-WHEN for --stray: strays are visible in
// one command from the beats: the undeclared unit and the process that
// started before the last play (bench:<b> build_at), not the young one;
// --since moves the play for every bench.
func TestFleetPSStray(t *testing.T) {
	t.Parallel()
	addr := psStore(t)
	code, out, _ := runPS(t, "--redis", addr, "--stray", "--bench", "hulk")
	want := "hulk unit nova-grok-heartbeat undeclared\n" +
		"hulk old pid=11 cpu=0.0 user=nova age=4d0h cmd=bash /home/nova/deal-status --watch\n" +
		"STRAY hulk units=1 old=1 play=2026-09-26T15:00:00Z\n"
	if code != 1 || out != want {
		t.Fatalf("--stray: exit %d\n%s\nwant\n%s", code, out, want)
	}
	code, out, _ = runPS(t, "--redis", addr, "--stray", "--bench", "hulk", "--since", "10s")
	if code != 1 || !strings.Contains(out, "hulk old pid=90 ") || !strings.HasSuffix(out, "STRAY hulk units=1 old=2 play=2026-09-26T15:59:50Z\n") {
		t.Fatalf("--since 10s: exit %d\n%s", code, out)
	}
	code, out, _ = runPS(t, "--redis", addr, "--stray")
	if code != 1 || !strings.Contains(out, "STRAY batman NOSAMPLE") || !strings.Contains(out, "STRAY dead NOBEAT") {
		t.Fatalf("--stray every bench: exit %d\n%s", code, out)
	}
}

// TestFleetPSRefusals: every refusal prints one line, exit 2.
func TestFleetPSRefusals(t *testing.T) {
	t.Parallel()
	addr := psStore(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--redis", addr, "--bench", "nope"}, "unregistered bench nope"},
		{[]string{"--redis", addr, "--since", "1h"}, "--since is the last play for --stray"},
		{[]string{"--redis", addr, "--stray", "--since", "soon"}, `--since "soon": want an RFC 3339 time or a positive duration`},
		{[]string{"--redis", addr, "extra"}, "takes no positional arguments"},
	} {
		code, out, errOut := runPS(t, c.args...)
		if code != 2 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit 2 naming %q", c.args, code, out, errOut, c.want)
		}
	}
	empty := miniredis.RunT(t)
	if code, _, errOut := runPS(t, "--redis", empty.Addr()); code != 2 || !strings.Contains(errOut, "no bench is registered") {
		t.Errorf("empty registry: exit %d stderr %q", code, errOut)
	}
}
