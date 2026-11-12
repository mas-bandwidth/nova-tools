package main

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hosts_play_test.go pins the hosts each play targets (D13, 2026-11-07: install reached a seat-only machine and the
// VPN server, machine rows with roles=- and runners=0, because the software plays targeted all). A play that puts
// software or units on a machine targets the fleet's worker groups, bench, runner and services; only the tailnet,
// the seat, the join, the push of the secrets store and the login's ssh names may reach a managed-only host, which
// needs those and nothing else. A new play adds its row here.
func TestPlaysTargetTheirGroups(t *testing.T) {
	t.Parallel()
	const workers = "bench:runner:services"
	const joinHost = "{{ nova_join_host | default('!all') }}"
	// the toolchain plays: bench and runner, or, run by join, the machine joining when it is one
	const toolchain = "{{ ('bench:runner:&' ~ nova_join_host) if nova_join_host is defined else 'bench:runner' }}"
	want := map[string][]string{
		"ci-rules":        {workers},
		"github-runners":  {"runner"},
		"audit":           {toolchain},
		"runner-packages": {toolchain},
		"sdk-toolchain":   {toolchain},
		"health":          {workers},
		"netwatch":        {workers},
		"install":         {workers, "bench"}, // then the pinned opencode, bench only (roles/opencode)
		"services":        {workers},
		"postgres":        {"{{ nova_store_hosts | default('services') }}", "localhost"},
		"redis":           {"{{ nova_store_hosts | default('services') }}"},
		"join":            {joinHost, "localhost"},
		"seat":            {"{{ nova_join_host | default('all:!windows') }}"},
		"tailscale":       {"{{ nova_join_host | default('all:!windows') }}"},
		"secrets-refresh": {"all:!windows"},
		"ssh-names":       {"all:!windows"},
		"windows-runners": {workers + ":windows"}, // and plain Windows hosts, which are in group windows only
	}
	for _, name := range plays(tree) {
		allowed, ok := want[name]
		if !ok {
			t.Errorf("%s.yml has no row in TestPlaysTargetTheirGroups: say which hosts it may target", name)
			continue
		}
		b, err := fs.ReadFile(tree, name+".yml")
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]any
		if err := yaml.Unmarshal(b, &entries); err != nil {
			t.Fatalf("%s.yml: %v", name, err)
		}
		var hosts []string
		for _, e := range entries {
			if h, ok := e["hosts"].(string); ok && !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
		slices.Sort(hosts)
		slices.Sort(allowed)
		if !slices.Equal(hosts, allowed) {
			t.Errorf("%s.yml targets hosts %q, want %q", name, hosts, allowed)
		}
	}
}

// TestSSHNamesListLeavesOutWindows: ssh-names runs on all:!windows, and its write asserts the run covers every machine
// of its list, so the list leaves group windows out too; with it in, one plain Windows host refuses every write.
func TestSSHNamesListLeavesOutWindows(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, "ssh-names.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ssh_names_hosts: \"{{ groups['all'] | difference(groups['windows'] | default([])) | sort }}\"") {
		t.Error("ssh-names.yml: ssh_names_hosts must be groups all less groups windows, as its hosts are all:!windows")
	}
}
