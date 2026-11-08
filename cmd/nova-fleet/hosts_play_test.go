package main

import (
	"io/fs"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// hosts_play_test.go pins the hosts each play targets (D13, 2026-11-07: install reached a seat-only machine and the
// VPN server, machine rows with roles=- and runners=0, because the software plays targeted all). A play that puts
// software or units on a machine targets the fleet's worker groups, bench, runner and services; only the tailnet,
// the seat, the join and the push of the secrets store may reach a managed-only host, which needs those and nothing
// else. A new play adds its row here.
func TestPlaysTargetTheirGroups(t *testing.T) {
	t.Parallel()
	const workers = "bench:runner:services"
	const joinHost = "{{ nova_join_host | default('!all') }}"
	want := map[string][]string{
		"ci-rules":        {workers},
		"github-runners":  {"runner"},
		"runner-packages": {"runner"},
		"health":          {workers},
		"netwatch":        {workers},
		"install":         {workers, "bench"}, // then the pinned opencode, bench only (roles/opencode)
		"services":        {workers},
		"postgres":        {"{{ nova_store_hosts | default('services') }}", "localhost"},
		"redis":           {"{{ nova_store_hosts | default('services') }}"},
		"join":            {joinHost, "localhost"},
		"seat":            {"{{ nova_join_host | default('all') }}"},
		"tailscale":       {"{{ nova_join_host | default('all') }}"},
		"secrets-refresh": {"all"},
		"windows-runners": {workers},
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
