package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

// TestDependenciesNeedsIsASet is #1788's client half. internal/jobs seeds a repeated
// need as one edge already; the graph FILE the verb writes must hold it once too,
// because the ready set counts unmet needs and a file that grows a copy per re-run
// never lets the count reach zero. Needs are a set: a need named twice in one
// --needs list, a need re-sent by a re-run and a copy a broken graph already holds
// are each one edge in the file, and the DEPENDENCIES OK line agrees.
func TestDependenciesNeedsIsASet(t *testing.T) {
	t.Parallel()

	t.Run("a --needs list that names a need twice writes one edge", func(t *testing.T) {
		path := writeSeed(t, `{"nodes":[{"id":"y"}]}`)
		code, stdout, stderr := invoke("dependencies", "--graph", path, "--node", "x", "--needs", "y,y")
		if code != 0 {
			t.Fatalf("dependencies --node x --needs y,y exit = %d, want 0 (stderr=%q)", code, stderr)
		}
		if got, want := strings.TrimSpace(stdout), "DEPENDENCIES OK nodes=2 edges=1"; got != want {
			t.Fatalf("printed %q, want %q", got, want)
		}
		if got := fileNeeds(t, path)["x"]; !exactIDs(got, []string{"y"}) {
			t.Fatalf("the file holds x needs %v, want [y] once", got)
		}
	})

	t.Run("three repeated --node a --needs b each leave one edge", func(t *testing.T) {
		path := writeSeed(t, `{"nodes":[{"id":"b"}]}`)
		for i := 1; i <= 3; i++ {
			code, stdout, stderr := invoke("dependencies", "--graph", path, "--node", "a", "--needs", "b")
			if code != 0 {
				t.Fatalf("repeated call %d exit = %d, want 0 (stderr=%q)", i, code, stderr)
			}
			if got, want := strings.TrimSpace(stdout), "DEPENDENCIES OK nodes=2 edges=1"; got != want {
				t.Fatalf("repeated call %d printed %q, want %q", i, got, want)
			}
			if got := fileNeeds(t, path)["a"]; !exactIDs(got, []string{"b"}) {
				t.Fatalf("after call %d the file holds a needs %v, want [b] once", i, got)
			}
		}
	})

	t.Run("a copy the file already holds is not written a second time", func(t *testing.T) {
		path := writeSeed(t, `{"nodes":[{"id":"b"},{"id":"a","needs":["b","b","b","b"]}]}`)
		code, stdout, stderr := invoke("dependencies", "--graph", path, "--node", "a", "--needs", "b")
		if code != 0 {
			t.Fatalf("dependencies on the four-copy graph exit = %d, want 0 (stderr=%q)", code, stderr)
		}
		if got, want := strings.TrimSpace(stdout), "DEPENDENCIES OK nodes=2 edges=1"; got != want {
			t.Fatalf("printed %q, want %q", got, want)
		}
		if got := fileNeeds(t, path)["a"]; !exactIDs(got, []string{"b"}) {
			t.Fatalf("the file holds a needs %v, want [b] once: a re-run collapses the copies it found", got)
		}
	})

	t.Run("the checked-in cmd/nova-work/deps.json holds each edge once", func(t *testing.T) {
		raw, err := os.ReadFile("deps.json")
		if err != nil {
			t.Fatalf("read the checked-in graph: %v", err)
		}
		nodes, err := jobs.ParseNodes(raw)
		if err != nil {
			t.Fatalf("parse the checked-in graph: %v", err)
		}
		for _, n := range nodes {
			seen := map[string]bool{}
			for _, dep := range n.Needs {
				if seen[dep] {
					t.Fatalf("the checked-in graph holds node %s needing %s more than once: needs is a set (#1788)", n.ID, dep)
				}
				seen[dep] = true
			}
		}
		if _, err := jobs.Seed(nodes); err != nil {
			t.Fatalf("the checked-in graph does not seed: %v", err)
		}
	})
}

// fileNeeds reads the graph file the verb wrote and returns each node's needs as the
// file holds them -- its own bytes, not the seeded graph's deduplicated view, which
// is the whole difference #1788 names.
func fileNeeds(t *testing.T, path string) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the graph back: %v", err)
	}
	nodes, err := jobs.ParseNodes(raw)
	if err != nil {
		t.Fatalf("parse the graph back: %v", err)
	}
	needs := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		needs[n.ID] = n.Needs
	}
	return needs
}

// exactIDs reports whether got holds exactly want, in order and once each.
func exactIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
