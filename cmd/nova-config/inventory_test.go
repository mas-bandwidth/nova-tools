package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventory(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	code, out, errs := h.run(t, "inventory", "--list")
	require.Equal(t, 0, code, errs)
	assert.Empty(t, errs)
	var inv map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &inv))
	groups := map[string]string{}
	for k, v := range inv {
		groups[k] = string(v)
	}
	assert.JSONEq(t, `{"hosts":["bench-a","bench-b"]}`, groups["all"])
	assert.JSONEq(t, `{"hosts":["bench-a","bench-b"]}`, groups["bench"])
	assert.JSONEq(t, `{"hosts":["bench-a"]}`, groups["services"])
	assert.JSONEq(t, `{"hosts":["bench-b"]}`, groups["runner"])
	assert.True(t, strings.HasPrefix(out, "{\n  \"_meta\": {\n    \"hostvars\": {\n      \"bench-a\": {\n        \"ansible_host\": \"bench-a\","), out)

	_, out, _ = h.run(t, "inventory", "--host", "bench-b")
	assert.JSONEq(t, `{
		"ansible_host": "bench-b.tail", "ansible_user": "nova", "cores": 12, "runners": 2, "nova_os": "linux", "nova_arch": "amd64",
		"nova_services": [{"name": "member-b", "argv": [{"__ansible_unsafe": "/usr/bin/env"}, {"__ansible_unsafe": "A=b"}, {"__ansible_unsafe": "prog"}],
			"seat": "s-b", "keys": ["A_KEY", "B_KEY"], "every": 0, "keepalive": true, "enabled": true,
			"env": {}, "log": {"__ansible_unsafe": "~/.local/state/nova/services/member-b.log"}}]}`, out)

	_, out, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.Contains(t, out, `"nova_services": []`, "a machine with no services has an empty list, never none")
	assert.NotContains(t, out, "ansible_connection")
	assert.NotContains(t, out, "cores", "an unknown fact is left out")
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
	assert.Contains(t, errs, "NOVA_MACHINE nope names no machine row; the machines are bench-a,bench-b; set NOVA_MACHINE to one of them, or run: nova-config machine add nope --user <user>\n")
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
	assert.Equal(t, 2, func() int { c, _, _ := h.run(t, "inventory", "--redis", "x:1"); return c }(), "the Redis path is gone")
	r := h.st.rows["service"]["member-b"]
	r.F["argv"] = "not-json"
	h.st.rows["service"]["member-b"] = r
	code, _, errs = h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "service member-b has corrupt argv")
}

// An empty store prints an empty inventory, not a refusal.
func TestInventoryEmpty(t *testing.T) {
	t.Parallel()
	code, out, errs := newHarness().run(t, "inventory")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, `"hosts": []`)
}

// nova_public is a host variable only when the row has a public address.
func TestInventoryPublic(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--public", "edge.example.com", "--actor", "a")
	_, out, _ := h.run(t, "inventory", "--host", "bench-b")
	assert.Contains(t, out, `"nova_public": "edge.example.com"`)
	assert.Contains(t, out, `"ansible_host": "bench-b.tail"`, "Ansible keeps the tailnet address")
	_, out, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, out, "nova_public", "unset is left out")
}

func TestInventoryDarwinInterpreter(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "add", "mac-a", "--user", "glenn", "--os", "darwin", "--arch", "arm64", "--actor", "t")
	_, out, _ := h.run(t, "inventory", "--host", "mac-a")
	assert.Contains(t, out, `"ansible_python_interpreter": "/usr/bin/python3"`, "a Mac names the system python, whose certificate bundle get_url needs")
	_, out, _ = h.run(t, "inventory", "--host", "bench-b")
	assert.NotContains(t, out, "ansible_python_interpreter", "Linux keeps ansible's discovery")
}
