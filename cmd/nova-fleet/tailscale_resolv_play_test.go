package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A machine whose /etc/resolv.conf is a plain file naming only the MagicDNS resolver cannot resolve names outside the
// tailnet. The role puts resolved's stub back, only where resolved runs, the stub exists and resolv.conf does not
// already lead to it, and restarts tailscaled only when it changed the link.
func TestTailscaleRoleRestoresResolvedStubOnlyWhenWrong(t *testing.T) {
	t.Parallel()
	const file = "roles/tailscale/tasks/dns.yml"
	link := roleTask(t, file, "resolv.conf is resolved's stub")
	when := asText(link["when"])
	assert.Contains(t, when, "ts_resolved.stdout | trim == 'active'")
	assert.Contains(t, when, "ts_stub.stat.exists")
	assert.Contains(t, when, "ts_resolv.stdout | trim != nova_resolved_stub")
	restart := roleTask(t, file, "tailscaled restarted")
	assert.Contains(t, asText(restart["when"]), "ts_resolv_link is changed")
	assert.Equal(t, "ts_os == 'debian'", asText(roleTask(t, "roles/tailscale/tasks/main.yml", "resolv.conf is resolved's stub")["when"]))
}
