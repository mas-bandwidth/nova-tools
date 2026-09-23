package swarm

import (
	"fmt"
	"testing"
)

// TestIssue1853 reproduces every validation escape in nova-tools#1853: Windows drive
// paths, more than eight PATHS: globs, comma-only PATHS: lines, unknown kinds, and
// TEST: none on gated kinds. Each subtest was a clean lint on the buggy code.
func TestIssue1853(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []string
		drift  string
		clean  bool
		wants  []string // tokens that must be present when clean is false
	}{
		{
			name: "windows drive letter in PATHS is absolute",
			header: []string{
				"KIND: fix-red",
				"PATHS: C:/Windows/system32/evil.go",
				"TEST: internal/swarm TestA",
			},
			drift: "paths-declared",
		},
		{
			name: "nine PATHS globs is over the eight-entry cap",
			header: []string{
				"KIND: fix-red",
				"PATHS: a1.go, a2.go, a3.go, a4.go, a5.go, a6.go, a7.go, a8.go, a9.go",
				"TEST: internal/swarm TestA",
			},
			drift: "paths-declared",
		},
		{
			name: "comma-only PATHS line declares nothing",
			header: []string{
				"KIND: fix-red",
				"PATHS: , , ",
				"TEST: internal/swarm TestA",
			},
			drift: "paths-declared",
		},
		{
			name: "unknown kind is not declared",
			header: []string{
				"KIND: completely-unknown-kind",
				"PATHS: internal/swarm/a.go",
				"TEST: internal/swarm TestA",
			},
			drift: "kind-declared",
		},
		{
			name: "TEST: none on a gated kind requires a reproducing test",
			header: []string{
				"KIND: fix-red",
				"PATHS: internal/swarm/a.go",
				"TEST: none",
			},
			drift: "test-named",
		},
		{
			name:   "TEST: none on an ungated kind is a declaration",
			header: []string{"KIND: read", "PATHS: internal/swarm/a.go", "TEST: none"},
			clean:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := headerCard(t, tc.header...)
			fs := findingsOn(raw)
			if tc.clean {
				for _, f := range fs {
					t.Errorf("unexpected %s: %s", f.Check, f.Excerpt)
				}
				return
			}
			if !hasCheck(fs, tc.drift) {
				t.Fatalf("expected %s drift, got:\n%s", tc.drift, dumpFindings(fs))
			}
		})
	}

	// Every gated kind named by SPEC-TOOLWORK.md §5 rule 2 refuses TEST: none;
	// every ungated kind accepts it.
	for _, kind := range []string{"fix-red", "transcript-test", "mutation-kill"} {
		t.Run(fmt.Sprintf("TEST: none on gated kind %s", kind), func(t *testing.T) {
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: none")
			fs := findingsOn(raw)
			if !hasCheck(fs, "test-named") {
				t.Fatalf("kind %q is gated and requires a test; got:\n%s", kind, dumpFindings(fs))
			}
		})
	}
	for _, kind := range []string{"read", "probe", "text", "tone"} {
		t.Run(fmt.Sprintf("TEST: none on ungated kind %s", kind), func(t *testing.T) {
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: none")
			fs := findingsOn(raw)
			for _, f := range fs {
				if f.Check == "test-named" {
					t.Fatalf("kind %q is ungated and allows TEST: none, but got test-named: %s", kind, f.Excerpt)
				}
			}
		})
	}
}
