package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventory(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.env["NOVA_SPRINT_REDIS"] = "store:1"
	h.beats = map[string]*Beat{"bench-b": {OS: "linux", Arch: "amd64", Cores: "12"}}
	code, out, errs := h.run(t, "inventory", "--list")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "NOTE machine bench-a: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts\n", errs)
	var inv map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &inv))
	groups := map[string]string{}
	for k, v := range inv {
		groups[k] = string(v)
	}
	assert.JSONEq(t, `{"hosts":["bench-a","bench-b"],"vars":{"nova_store":"bench-a","nova_config_rev":{"machine":2,"loop":3,"fleet":4}}}`, groups["all"])
	assert.JSONEq(t, `{"hosts":["bench-b","bench-a"]}`, groups["adopt"], "the canary, the first non-coordinator, leads")
	assert.JSONEq(t, `{"hosts":["bench-a","bench-b"]}`, groups["benches"])
	assert.JSONEq(t, `{"hosts":["bench-a"]}`, groups["store"])
	assert.JSONEq(t, `{"hosts":["bench-a"]}`, groups["coordinator"])
	assert.JSONEq(t, `{"hosts":["bench-a"]}`, groups["store_deployer"])
	assert.JSONEq(t, `{"hosts":["bench-b"]}`, groups["runners"])
	assert.JSONEq(t, `{"hosts":["bench-b"]}`, groups["tla"])
	assert.True(t, strings.HasPrefix(out, "{\n  \"_meta\": {\n    \"hostvars\": {\n      \"bench-a\": {\n        \"ansible_host\": \"bench-a\","), out)

	code, out, _ = h.run(t, "inventory", "--host", "bench-b")
	require.Equal(t, 0, code)
	assert.JSONEq(t, `{
		"ansible_host": "bench-b", "ansible_user": "nova", "kind": "machine", "slots": 16, "slots_capacity": 16,
		"slots_share": 6, "cores": 12, "runners": 2, "nova_tla": true, "registry_seat": "s-b", "nova_seat": "s-b",
		"registry_roles": "bench", "bench_os": "linux", "nova_os": "linux", "nova_arch": "amd64",
		"nova_redis_port": 6380, "nova_redis_addr": "bench-a:6380", "nova_pg_dsn": "postgres://u@bench-a/x",
		"nova_loops": [{"name": "member-b", "argv": ["/usr/bin/env", "A=b", "nova-swarm", "member", "--as", "b"],
			"seat": "s-b", "keys": ["A_KEY", "B_KEY"], "every": 0, "keepalive": true, "enabled": true,
			"log": "~/nova-bench/loops/member-b.log"}]}`, out)

	code, out, _ = h.run(t, "inventory", "--host", "bench-a")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `"nova_loops": []`, "a machine with no loops has an empty list, never none")
	assert.NotContains(t, out, "ansible_connection")
}

func TestInventoryLocalMachine(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.host = "Bench-A.tail.net"
	_, out, _ := h.run(t, "inventory", "--host", "bench-a")
	assert.Contains(t, out, `"ansible_connection": "local"`, "the host name's first label, lower-cased")
	h.env["NOVA_MACHINE"] = "bench-b"
	_, out, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, out, "ansible_connection", "NOVA_MACHINE wins over the host name")
	h.env["NOVA_MACHINE"] = "nope"
	code, _, errs := h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "NOVA_MACHINE nope names no machine row")
}

func TestInventoryRefusals(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	code, _, errs := h.run(t, "inventory", "--host", "nope")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no machine row named nope; the machines are bench-a,bench-b")
	code, _, errs = h.run(t, "inventory", "--host", "x", "--list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--list or --host")

	h.ok(t, "machine", "set", "bench-b", "--roles", "bench,services", "--actor", "a")
	code, _, errs = h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "machines with role services are [bench-a bench-b], but the fleet row's store is \"bench-a\"")

	h = fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--width", "17", "--actor", "a")
	code, _, errs = h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "width 17 exceeds slots 16")

	h = newHarness()
	code, _, errs = h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "redis_port and pg_dsn are unset")
}

func TestWithoutSprintRedis(t *testing.T) {
	t.Parallel()
	member := []string{"/usr/bin/env", "NOVA_SPRINT_REDIS=a:1", "B=c", "/x/nova-swarm", "member", "--as", "m", "NOVA_SPRINT_REDIS=keep"}
	assert.Equal(t, []string{"/usr/bin/env", "B=c", "/x/nova-swarm", "member", "--as", "m", "NOVA_SPRINT_REDIS=keep"}, withoutSprintRedis(member))
	for _, argv := range [][]string{
		{"/usr/bin/env", "NOVA_SPRINT_REDIS=a:1", "nova-sprint", "run"},
		{"nova-swarm", "member"},
		{"/usr/bin/env", "NOVA_SPRINT_REDIS=a:1"},
		{},
	} {
		assert.Equal(t, argv, withoutSprintRedis(argv), "%v is no env-prefixed member", argv)
	}
}

func TestMergeBeat(t *testing.T) {
	t.Parallel()
	assert.Nil(t, mergeBeat(nil, ""))
	assert.Nil(t, mergeBeat(map[string]string{"os": "linux", "at": "soon"}, "not json"), "both records unreadable is no beat")
	assert.Equal(t, &Beat{OS: "linux", Arch: "arm64", Cores: "8"}, mergeBeat(map[string]string{"os": "linux", "arch": "arm64", "ncpu": "8", "at": "1"}, ""))
	assert.Equal(t, &Beat{OS: "linux", Cores: "32"}, mergeBeat(map[string]string{"os": "linux", "ncpu": "8"}, `{"at":"2026-11-05T19:12:45Z","cores":32}`), "the member's cores win")
	assert.Equal(t, &Beat{Cores: "32"}, mergeBeat(map[string]string{"os": "linux", "at": "x"}, `{"cores":32}`), "an unreadable hash counts as none")
	assert.Equal(t, &Beat{}, mergeBeat(nil, `{"at":"2026-11-05T19:12:45Z"}`))
}

// rw is a connection whose replies are given up front.
type rw struct {
	io.Reader
	sent bytes.Buffer
}

func (c *rw) Write(p []byte) (int, error) { return c.sent.Write(p) }

func TestPipeline(t *testing.T) {
	t.Parallel()
	c := &rw{Reader: strings.NewReader("+OK\r\n*4\r\n$2\r\nos\r\n$5\r\nlinux\r\n$4\r\nncpu\r\n$1\r\n8\r\n$-1\r\n$12\r\n{\"cores\":16}\r\n:7\r\n")}
	got, err := pipeline(c, [][]string{{"AUTH", "u", "p w"}, {"HGETALL", "k"}, {"GET", "a"}, {"GET", "b"}, {"INCR", "n"}})
	require.NoError(t, err)
	assert.Equal(t, []any{"OK", []any{"os", "linux", "ncpu", "8"}, nil, `{"cores":16}`, "7"}, got)
	assert.True(t, strings.HasPrefix(c.sent.String(), "*3\r\n$4\r\nAUTH\r\n$1\r\nu\r\n$3\r\np w\r\n*2\r\n$7\r\nHGETALL\r\n$1\r\nk\r\n"), c.sent.String())

	for reply, want := range map[string]string{
		"-WRONGPASS invalid\r\n": "WRONGPASS invalid",
		"\r\n":                   "an empty reply",
		"?x\r\n":                 "an unknown reply",
		"$99999999\r\n":          "a reply of 99999999 bytes",
		"$5\r\nab":               "EOF",
	} {
		_, err := pipeline(&rw{Reader: strings.NewReader(reply)}, [][]string{{"GET", "k"}})
		assert.ErrorContains(t, err, want, "%q", reply)
	}
	_, err = reply(bufio.NewReader(strings.NewReader("*1\r\n-ERR inner\r\n")))
	assert.ErrorContains(t, err, "ERR inner")
}
