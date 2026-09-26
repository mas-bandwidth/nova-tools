package swarm

import (
	"strings"
	"testing"
)

// TestSwarmReadsTESTThroughParseTest is the nova-tools#4401 read's item 3:
// the swarm's TEST readers (testPackageGlobs, the wall terms' package glob,
// and LintCardTestGate, the test-gate lint) read TEST through
// cardhdr.ParseTest, the one grammar the copy wrapper's gate runs. A tagged
// line's package is the one after -tags, `none <why>` names no package and
// carries no gate obligation, and a `...` pattern ParseTest refuses names no
// package.
func TestSwarmReadsTESTThroughParseTest(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"./internal/x TestY":                  "internal/x/*_test.go",
		"-tags functional ./internal/x TestY": "internal/x/*_test.go",
		"-tags=functional internal/x/ TestY":  "internal/x/*_test.go",
		"none the change is one docs page":    "",
		"none":                                "",
		"./internal/... TestY":                "",
		"":                                    "",
	} {
		if got := strings.Join(testPackageGlobs(line), ","); got != want {
			t.Errorf("testPackageGlobs(%q) = %q, want %q", line, got, want)
		}
	}
	for _, tc := range []struct {
		test, run string
		drew      bool
	}{
		// the tagged line names ./internal/pulse, which the gate does not run
		{"-tags functional ./internal/pulse/ TestThing", "go test ./internal/swarm/ -count=1", true},
		{"-tags functional ./internal/pulse/ TestThing", "go test -tags functional ./internal/pulse/ -count=1", false},
		{"none the change is one docs page", "go test ./internal/swarm/ -count=1", false},
	} {
		raw := gateCard("", "KIND: fix-red", "PATHS: internal/pulse/thing.go, internal/pulse/thing_test.go", "TEST: "+tc.test, "RUN: "+tc.run)
		if got := testGateDrew(LintCardTestGate(raw)); got != tc.drew {
			t.Errorf("TEST: %s with RUN: %s drew test-gate=%v, want %v: %v", tc.test, tc.run, got, tc.drew, LintCardTestGate(raw))
		}
	}
}
