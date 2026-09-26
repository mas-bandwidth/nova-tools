package land_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestPolicyRefusesWhatItCannotRead is finding 2's extras (Stella's audit
// stella-e6353bf80360): beyond a malformed number, an unknown key, an empty
// policy, a field under no base, a list item under no list, a bad deadline,
// a duplicate base, a tab indent, a policy with no repo and one with no base
// are each refused naming the line and the shape wanted, never reinterpreted.
func TestPolicyRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, policy, want string }{
		{"empty", "\n# only a comment\n", "policy is empty"},
		{"unknown top-level key", "repo: r\nbasis:\n", "line 2: basis:: unknown top-level key \"basis\""},
		{"unknown base field", "repo: r\nbases:\n  dev:\n    reader: 2\n", "line 4: reader: 2: unknown base field \"reader\""},
		{"field under no base", "repo: r\nbases:\n    readers: 2\n", "line 3: readers: 2: a base field under no base"},
		{"base before bases", "repo: r\n  dev:\n", "line 2: dev:: a base before bases:"},
		{"duplicate base", "repo: r\nbases:\n  dev:\n  dev:\n", "line 4: dev:: base dev is declared twice"},
		{"negative number", "repo: r\nbases:\n  dev:\n    land_bar: -1\n", "line 4: land_bar: -1: field land_bar: value \"-1\" is not a whole number"},
		{"list with a scalar", "repo: r\nbases:\n  dev:\n    required_steps: build\n", "line 4: required_steps: build: field required_steps: is a list"},
		{"item under a scalar", "repo: r\nbases:\n  dev:\n    readers: 2\n      - build\n", "line 5: - build: a list item under \"readers\", which is not a list field"},
		{"item under an empty list", "repo: r\nbases:\n  dev:\n    build_tags: []\n      - x\n", "line 5: - x: a list item under build_tags: []"},
		{"bad deadline", "repo: r\nbases:\n  dev:\n    deadlines:\n      step: soon\n", "line 5: step: soon: deadline step: value \"soon\" is not a duration"},
		{"unknown deadline", "repo: r\nbases:\n  dev:\n    deadlines:\n      steps: 10m\n", "line 5: steps: 10m: unknown deadline \"steps\""},
		{"deadline under no deadlines", "repo: r\nbases:\n  dev:\n    readers: 1\n      step: 10m\n", "line 5: step: 10m: under \"readers\""},
		{"tab indent", "repo: r\nbases:\n\tdev:\n", "line 3: dev:: indented with a tab"},
		{"odd indent", "repo: r\nbases:\n   dev:\n", "line 3: dev:: indent of 3 spaces"},
		{"no repo", "bases:\n  dev:\n    readers: 1\n", "policy names no repo"},
		{"no base", "repo: r\nbases:\n", "policy declares no base"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, err := land.ParsePolicy(c.policy)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("policy %q: want an error containing %q, got policy=%+v err=%v", c.policy, c.want, p, err)
			}
		})
	}
}

// TestPolicyParsesTheFleetFile keeps the grammar the fleet's own policy
// uses: fleet/land/nova-tools.yml parses to its declared fields, and an
// empty list written as [] is an empty list. LoadPolicy names the path.
func TestPolicyParsesTheFleetFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "fleet", "land", "nova-tools.yml")
	rp, err := land.LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	dev := rp.Bases["dev"]
	if rp.Repo != "nova-tools" || dev == nil || dev.LandBar != 10 || dev.Readers != 0 || dev.MergeStyle != "merge" ||
		len(dev.RequiredSteps) != 6 || len(dev.SecurityPaths) != 3 || len(dev.AlonePaths) != 5 || len(dev.SelGOOS) != 3 ||
		len(dev.BuildTags) != 0 || dev.StepDeadline != "10m" || dev.GateDeadline != "20m" {
		t.Fatalf("fleet policy parsed to %+v", dev)
	}
	bad := filepath.Join(t.TempDir(), "policy.yml")
	if err := os.WriteFile(bad, []byte("repo: r\nbases:\n  dev:\n    readers: two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := land.LoadPolicy(bad); err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "field readers") {
		t.Fatalf("LoadPolicy of a malformed file: %v", err)
	}
}
