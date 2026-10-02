package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The loop kind through the one grammar (docs/SPEC-CONFIG.md, "loop").

// loopHarness is a harness with the store, the actor and the Redis in the
// environment, and the machines named.
func loopHarness(t *testing.T, machines ...string) *harness {
	t.Helper()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	for _, m := range machines {
		h.machine(t, m, "4")
	}
	return h
}

func TestLoopVerbsEndToEndOnTheFake(t *testing.T) {
	t.Parallel()

	h := loopHarness(t, "m1", "m2")
	steps := []struct {
		name string
		args []string
		code int
		out  string // the whole stdout, or a prefix when prefix is set
		errs string // a phrase stderr must hold
		pre  bool
	}{
		{
			name: "add a member loop kept alive",
			args: []string{"loop", "add", "member-m1", "--machine", "m1", "--argv", `["/bin/member","--as","m1"]`, "--keepalive", "true", "--seat", "s-m1", "--keys", "B_KEY,A_KEY", "--width", "2"},
			out:  "CONFIG ADD kind=loop name=member-m1 rev=3\n",
		},
		{
			name: "add a periodic loop on the same machine",
			args: []string{"loop", "add", "refresh", "--machine", "m1", "--argv", `["/bin/refresh","--once"]`, "--every", "60"},
			out:  "CONFIG ADD kind=loop name=refresh rev=4\n",
		},
		{
			name: "list prints every field in declaration order",
			args: []string{"loop", "list"},
			out:  "LOOP name=member-m1 machine=m1 argv=[\"/bin/member\",\"--as\",\"m1\"] seat=s-m1 keys=A_KEY,B_KEY every=0 keepalive=true width=2 enabled=true\nLOOP name=refresh machine=m1 argv=[\"/bin/refresh\",\"--once\"] seat=- keys=- every=60 keepalive=false width=0 enabled=true\nCONFIG LIST kind=loop rows=2\n",
		},
		{
			name: "show carries the stamps",
			args: []string{"loop", "show", "refresh"},
			out:  "LOOP name=refresh machine=m1 argv=[\"/bin/refresh\",\"--once\"] seat=- keys=- every=60 keepalive=false width=0 enabled=true created=",
			pre:  true,
		},
		{
			name: "machine show lists the machine's loops",
			args: []string{"machine", "show", "m1"},
			out:  "MACHINE name=m1 user=u seat=s slots=160 runners=0 width=4 created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z loops=member-m1,refresh beat=none\n",
		},
		{
			name: "a machine with no loop says so",
			args: []string{"machine", "show", "m2"},
			out:  "MACHINE name=m2 user=u seat=s slots=160 runners=0 width=4 created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z loops=- beat=none\n",
		},
		{
			name: "set that leaves a loop both periodic and kept alive is refused",
			args: []string{"loop", "set", "refresh", "--keepalive", "true"},
			code: 1,
			errs: "a loop runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false; run: nova-config loop show refresh",
		},
		{
			name: "set moves a loop to another machine",
			args: []string{"loop", "set", "refresh", "--machine", "m2", "--enabled", "false"},
			out:  "CONFIG SET kind=loop name=refresh rev=5 changed=enabled,machine\n",
		},
		{
			name: "history names every change",
			args: []string{"loop", "history", "refresh"},
			out:  "HISTORY id=4 kind=loop name=refresh op=add actor=a1 ",
			pre:  true,
		},
		{
			name: "the machine a loop names is held",
			args: []string{"machine", "remove", "m1"},
			code: 1,
			errs: "machine m1 is the --machine of loop member-m1",
		},
		{
			name: "a loop on no machine row is refused",
			args: []string{"loop", "add", "stray", "--machine", "m9", "--argv", `["/bin/x"]`, "--every", "5"},
			code: 1,
			errs: "--machine m9 names no machine row; run: nova-config machine list",
		},
		{
			name: "remove",
			args: []string{"loop", "remove", "member-m1"},
			out:  "CONFIG REMOVE kind=loop name=member-m1 rev=6\n",
		},
	}
	for _, s := range steps {
		code, out, errs := h.run(t, s.args...)
		require.Equal(t, s.code, code, "%s: %v\nstdout: %s\nstderr: %s", s.name, s.args, out, errs)
		if s.pre {
			assert.True(t, strings.HasPrefix(out, s.out), "%s: %q, want the prefix %q", s.name, out, s.out)
		} else if s.code == 0 {
			assert.Equal(t, s.out, out, s.name)
		}
		if s.errs != "" {
			assert.Contains(t, errs, s.errs, s.name)
			assert.Equal(t, 1, strings.Count(errs, "\n"), "%s: one refusal line", s.name)
		}
	}
}

func TestLoopUsageRefusalsOpenNoStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		errs []string
	}{
		{
			name: "argv as a shell line, no machine, no schedule",
			args: []string{"loop", "add", "l1", "--argv", "/bin/prog --loop"},
			errs: []string{"--machine is required", "--argv: want the command as a JSON array of strings"},
		},
		{
			name: "both schedules",
			args: []string{"loop", "add", "l1", "--machine", "m1", "--argv", `["/bin/prog"]`, "--every", "5", "--keepalive", "true"},
			errs: []string{"a loop runs every n seconds or is kept alive"},
		},
		{
			name: "a secret by value",
			args: []string{"loop", "add", "l1", "--machine", "m1", "--argv", `["/bin/prog"]`, "--every", "5", "--seat", "s1", "--keys", "A_KEY=v"},
			errs: []string{"never by value"},
		},
		{
			name: "a field the kind has not",
			args: []string{"loop", "add", "l1", "--machine", "m1", "--argv", `["/bin/prog"]`, "--every", "5", "--log", "/tmp/x"},
			errs: []string{"REFUSED: unknown flag --log", "this verb takes --argv, --as"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := loopHarness(t)
			before := h.opens
			code, out, errs := h.run(t, tc.args...)
			assert.Equal(t, 2, code, errs)
			assert.Empty(t, out)
			for _, want := range tc.errs {
				assert.Contains(t, errs, want)
			}
			assert.Equal(t, before, h.opens, "a usage refusal opened the store")
		})
	}
}

func TestApplyKindLoopWritesTheViewAndStatusShowsParity(t *testing.T) {
	t.Parallel()

	h := loopHarness(t, "m1")
	code, _, errs := h.run(t, "loop", "add", "l1", "--machine", "m1", "--argv", `["/bin/prog"]`, "--keepalive", "true")
	require.Equal(t, 0, code, errs)

	code, out, _ := h.run(t, "apply", "--kind", "loop", "--check")
	require.Equal(t, 0, code)
	assert.Equal(t, "CHECK ADD kind=loop name=l1\nCONFIG CHECK kind=loop add=1 set=0 remove=0 rev=2 applied=0\n", out)
	assert.Empty(t, h.redis.log, "--check writes nothing")

	code, out, errs = h.run(t, "status")
	assert.Equal(t, 1, code, "Redis is behind for the machine and the loop")
	assert.Contains(t, out, " loop=1 loop_rev=2 ")
	assert.Contains(t, out, " loop_applied=0")
	assert.Contains(t, errs, "run: nova-config apply")

	code, out, _ = h.run(t, "apply", "--kind", "loop")
	require.Equal(t, 0, code)
	assert.Equal(t, "APPLY ADD kind=loop name=l1\nCONFIG APPLY kind=loop add=1 set=0 remove=0 rev=2 ms=0\n", out)
	assert.Equal(t, []string{"write loop l1"}, h.redis.log)
	assert.Equal(t, `["/bin/prog"]`, h.redis.views["loop"]["l1"]["argv"])
	assert.Equal(t, int64(2), h.redis.revs["loop"])

	code, out, _ = h.run(t, "apply", "--kind", "machine")
	require.Equal(t, 0, code, out)
	code, out, errs = h.run(t, "status")
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, " loop_applied=2 ")
}

// A reader loop's width is one value: loop set <name> --width <n> changes the
// command loop show prints and the argv the inventory hands the plays, with
// the argv as typed left alone (config.LoopCommand).
func TestAReaderLoopsWidthIsSetAsOneValue(t *testing.T) {
	t.Parallel()

	h := loopHarness(t, "m1")
	h.env["NOVA_MACHINE"] = "m1"
	code, _, errs := h.run(t, "loop", "add", "reader-1", "--machine", "m1", "--argv", `["nova-swarm","member","--reader","--width","8"]`, "--keepalive", "true")
	require.Equal(t, 0, code, errs)
	code, out, errs := h.run(t, "loop", "show", "reader-1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, ` width=0 `)
	assert.Contains(t, out, ` command=["nova-swarm","member","--reader","--width","8"]`, "width 0 runs the argv as typed")

	code, out, errs = h.run(t, "loop", "set", "reader-1", "--width", "16")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG SET kind=loop name=reader-1 rev=3 changed=width\n", out)
	code, out, errs = h.run(t, "loop", "show", "reader-1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, ` argv=["nova-swarm","member","--reader","--width","8"] `, "the argv as typed is kept")
	assert.Contains(t, out, ` command=["nova-swarm","member","--reader","--width","16"]`, "the command runs the field")

	code, _, errs = h.run(t, "fleet", "set", "--redis_port", "6380", "--pg_dsn", dsn)
	require.Equal(t, 0, code, errs)
	for _, kind := range []string{"machine", "fleet", "loop"} {
		code, out, errs = h.run(t, "apply", "--kind", kind)
		require.Equal(t, 0, code, "%s\n%s", out, errs)
	}
	// the fake Redis writes a row's fields; apply's derived log field is the real applier's (redis.go)
	h.redis.views["loop"]["reader-1"]["log"] = config.LoopLog("reader-1")
	code, out, errs = h.run(t, "inventory", "--host", "m1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, strings.Join(strings.Fields(out), ""), `"argv":["nova-swarm","member","--reader","--width","16"]`, "the plays render the field's width:\n%s", out)
}
