package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One locked-memory limit for every Linux service unit, from the host layout: the kernel charges io_uring's locked
// pages to the login across all its processes, and systemd's 8 MiB default refuses concurrent rings.
func TestServiceUnitSetsTheLockedMemoryLimit(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("templates/nova-service.service.j2")
	require.NoError(t, err)
	s := string(b)
	service := s[strings.Index(s, "\n[Service]\n"):strings.Index(s, "\n[Install]\n")]
	assert.Contains(t, service, "\nLimitMEMLOCK={{ nova_service_memlock }}\n")
	assert.Equal(t, 1, strings.Count(s, "LimitMEMLOCK="))
	g, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	assert.Contains(t, string(g), "\nnova_service_memlock: 1G\n")
}

// A user unit cannot lock past its user manager's limit, so health gives the login's manager the same size: a drop-in
// for its next start, and prlimit on the running one, so none of its units is restarted.
func TestHealthLetsTheUserManagerLockTheServiceLimit(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/health/tasks/main.yml")
	require.NoError(t, err)
	s := string(b)
	for _, want := range []string{
		`dest: "/etc/systemd/system/user@{{ ansible_facts['user_uid'] }}.service.d/90-nova-memlock.conf"`,
		"LimitMEMLOCK={{ nova_service_memlock }}",
		`health_memlock_bytes: "{{ nova_service_memlock | human_to_bytes }}"`,
		`prlimit --pid "$pid" --memlock={{ health_memlock_bytes }}:{{ health_memlock_bytes }}`,
		"user_memlock=$l",
	} {
		assert.Contains(t, s, want)
	}
	assert.NotContains(t, s, "user@{{ ansible_facts['user_uid'] }}.service, state: restarted",
		"the user manager is never restarted: its units would go with it")
}

// What older tooling left is removed by the role, each kind named, and only where it is a leftover: a pg-dump timer
// only without its service (the postgres play writes both), a runner directory only when unregistered.
func TestHealthRemovesOlderToolingLeftovers(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/health/tasks/main.yml")
	require.NoError(t, err)
	s := string(b)
	for _, want := range []string{
		"/etc/systemd/system/{ssh.service.d,tailscaled.service.d,user.slice.d}/90-nova-host-memory.conf",
		"{{ nova_home }}/.config/systemd/user/service.d/90-nova-memlock.conf",
		"{{ nova_home }}/.config/systemd/user/nova-{loop,service}-{member,reader}-*.service.d",
		"/etc/systemd/system/actions.runner.mas-bandwidth-nova-sprint.*",
		"[ -e /etc/systemd/system/nova-pg-dump.timer ] && [ ! -e /etc/systemd/system/nova-pg-dump.service ]",
		`[ -e "$d/.runner" ] || echo "$d"`,
		"state: stopped, enabled: false",
	} {
		assert.Contains(t, s, want)
	}
	end := strings.Index(s, "ansible.builtin.meta: end_host")
	require.Positive(t, end)
	assert.Greater(t, strings.Index(s, "the leftovers of older tooling"), end, "Linux only")
}

// The fleet login's ssh sessions lock what its runners may: pam_limits' file for that login, soft and hard, from
// nova_login_memlock, which may only be unlimited, as runner.service.j2's LimitMEMLOCK=infinity (any other value is
// refused by name). --tags memlock writes it alone, and still only on Linux.
func TestHealthLetsTheLoginsSessionsLockWhatTheRunnersMay(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/health/tasks/main.yml")
	require.NoError(t, err)
	s := string(b)
	for _, want := range []string{
		"dest: /etc/security/limits.d/90-nova-memlock.conf",
		"{{ ansible_facts['user_id'] }} - memlock unlimited",
		"HEALTH-MEMLOCK-REFUSED host=",
		"tags: [memlock]",
	} {
		assert.Contains(t, s, want)
	}
	gate := strings.Index(s, "ansible.builtin.meta: end_host")
	require.Positive(t, gate)
	assert.Contains(t, s[gate:gate+200], "tags: [always]", "--tags memlock still ends a machine that is not Linux")
	g, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	assert.Contains(t, string(g), "\nnova_login_memlock: unlimited\n")
	r, err := os.ReadFile("roles/github-runner/templates/runner.service.j2")
	require.NoError(t, err)
	assert.Contains(t, string(r), "\nLimitMEMLOCK=infinity\n", "the sessions' limit is the runners'")
}
