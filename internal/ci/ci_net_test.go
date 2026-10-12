package ci

import (
	"path/filepath"
	"testing"
)

// ci_net_test.go is the net rule's cases, run through the harness in
// ci_templates_test.go. Each top-level test keeps its old name and delegates to
// the one shared helper, so `go test -run` and the docs that name it still find
// it; the rule's inputs and wants are the netRule row below.

// netRule is the net row of the harness.
var netRule = ciRule{
	key:           "net",
	prefix:        "CI-NET",
	allowlist:     "net-allowlist.txt",
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
		{name: "TestNetRefusesRealURLHost", fixture: "realurl.go.txt", kind: "url", remedy: NetRemedy, host: "api.acme.com", contains: "api.acme.com/v1/status"},
		{name: "TestNetRefusesBareHostPort", fixture: "hostport.go.txt", kind: "hostport", host: "metrics.acme.com"},
	},
	allows: []ciAllow{
		{name: "TestNetAllowsLocalAndReservedHosts", fixture: "allowed.go.txt", tests: 1},
		{name: "TestNetAllowsNightlyBuildTag", fixture: "nightly.go.txt", tests: -1},
		{name: "TestNetAllowsSoakBuildTag", fixture: "soak.go.txt", tests: -1},
		{name: "TestNetAllowsLocalHostPort", fixture: "allowedhostport.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		scheme := "http" + "s://"
		return "\nfunc TestSecondURL(t *testing.T) { _ = \"" + scheme + host + "/again\" }\n"
	},
}

// 1. A test carrying a real URL host is refused with its file, line and host.
func TestNetRefusesRealURLHost(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, netRule, "TestNetRefusesRealURLHost")
}

// 2. A test whose only hosts are localhost, the loopback IPs and the reserved
// test domains is allowed.
func TestNetAllowsLocalAndReservedHosts(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, netRule, "TestNetAllowsLocalAndReservedHosts")
}

// 3. A file carrying //go:build nightly is exempt.
func TestNetAllowsNightlyBuildTag(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, netRule, "TestNetAllowsNightlyBuildTag")
}

// 4. A file carrying //go:build soak is exempt, the same way.
func TestNetAllowsSoakBuildTag(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, netRule, "TestNetAllowsSoakBuildTag")
}

// 5. A bare host:port literal with a real host is refused.
func TestNetRefusesBareHostPort(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, netRule, "TestNetRefusesBareHostPort")
}

// 6. A bare host:port on a local or reserved host is allowed.
func TestNetAllowsLocalHostPort(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, netRule, "TestNetAllowsLocalHostPort")
}

// 7. Adding an entry to the allowlist is refused; removing one is allowed.
func TestNetAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()
	ciAllowlistGrowsRefused(t, netRule)
}

// 8. A row allows one offender of its kind in its file wherever that offender
// now stands.
func TestNetAllowlistSurvivesShiftedLines(t *testing.T) {
	t.Parallel()
	ciAllowlistShiftedLines(t, netRule)
}

// TestNetOutputMatchesTheSpec pins the one-line grammar of the section.
func TestNetOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciOutputMatchesTheSpec(t, netRule)
}

// TestNetVerbLineMatchesTheSpec pins the help line the class test is entered
// under, word for word, to the section that prints it.
func TestNetVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciVerbLineMatchesTheSpec(t, netRule)
}

// TestNoRealNetworkHostsOnTheCIPath is the class test itself: every _test.go
// under internal/ and cmd/ is read, and the only real hosts that pass are the
// ones the allowlist already names.
func TestNoRealNetworkHostsOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, netRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "net-allowlist.txt"))
}
