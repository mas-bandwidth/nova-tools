package ci

import (
	"path/filepath"
	"testing"
)

// ci_net_test.go is the net rule's cases, run through the harness in
// ci_templates_test.go: TestCIScanRules runs each case as a subtest; the
// rule's inputs and wants are the netRule row below.

// netRule is the net row of the harness.
var netRule = ciRule{
	key:           "net",
	prefix:        "CI-NET",
	fixtures:      "net",
	allowKind:     "url",
	allowRemedy:   NetRemedyAllow,
	verbLine:      NetVerbLine,
	check:         runNet,
	outputFixture: "realurl.go.txt",
	wants: []string{
		"CI-NET file=internal/fixture/fixture_test.go line=",
		" host=api.acme.com ",
		`remedy="mock the endpoint with httptest or a local fake"`,
	},
	refusals: []ciRefusal{
		{name: "RefusesRealURLHost", fixture: "realurl.go.txt", kind: "url", remedy: NetRemedy, host: "api.acme.com", contains: "api.acme.com/v1/status"},
		{name: "RefusesBareHostPort", fixture: "hostport.go.txt", kind: "hostport", host: "metrics.acme.com"},
	},
	allows: []ciAllow{
		{name: "AllowsLocalAndReservedHosts", fixture: "allowed.go.txt", tests: 1},
		{name: "AllowsNightlyBuildTag", fixture: "nightly.go.txt", tests: -1},
		{name: "AllowsSoakBuildTag", fixture: "soak.go.txt", tests: -1},
		{name: "AllowsLocalHostPort", fixture: "allowedhostport.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		scheme := "http" + "s://"
		return "\nfunc TestSecondURL(t *testing.T) { _ = \"" + scheme + host + "/again\" }\n"
	},
}

// TestNoRealNetworkHostsOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only real hosts that pass are the
// ones the allowlist already names.
func TestNoRealNetworkHostsOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, netRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "net-allowlist.txt"))
}
