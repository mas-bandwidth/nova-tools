package sandbox

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The egress wall is the card's OUTBOUND half, and these tests are the whole of it that can
// be proved without a bench: the policy file is parsed, the names are resolved through a
// FAKE resolver, the addresses are pinned, and the ruleset is rendered and then read back
// and audited. Nothing here touches the network and nothing here runs nft — the rule of the
// house (unit tests never touch the network) and the reason the resolver is an interface.

// fakeResolver is the resolver seam. It answers from a table and records the names it was
// asked for, because "resolved at run start, pinned for the run" means each allowed name is
// asked exactly once and nothing else is asked at all.
type fakeResolver struct {
	table map[string][]netip.Addr
	asked []string
	err   error
}

func (f *fakeResolver) LookupHost(name string) ([]netip.Addr, error) {
	f.asked = append(f.asked, name)
	if f.err != nil {
		return nil, f.err
	}
	got, ok := f.table[name]
	if !ok {
		return nil, errors.New("no such host")
	}
	return got, nil
}

func addrs(t *testing.T, ss ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("test fixture %q is not an address: %s", s, err)
		}
		out = append(out, a)
	}
	return out
}

func prefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatalf("test fixture %q is not a prefix: %s", s, err)
		}
		out = append(out, p)
	}
	return out
}

// testModelHost is the model host these tests plan for. It is a reserved name, like every
// other host named in this file: the four names a card really reaches live in
// infra/image/egress.txt, which is data the tests READ, and a test fixture never spells a
// host this suite could be pointed at.
const testModelHost = "model.example.test"

// testAddrs are documentation addresses (TEST-NET-3 and 2001:db8::/32), for the same
// reason: a pinned address in a fixture is an address, not a destination.
const (
	testGitHubV4  = "198.51.100.10"
	testAPIV4     = "198.51.100.11"
	testAPIV6     = "2001:db8::11"
	testObjectsV4 = "198.51.100.12"
	testModelV4   = "198.51.100.13"
)

// goodInput is one plan's worth of input: the three base names the CODE fixes plus one
// model host, a resolver on the bench's own network and one other bench denied. The base
// names come from EgressBaseNames rather than from three literals here, so this fixture
// cannot drift from the constant it is meant to exercise.
func goodInput(t *testing.T) EgressInput {
	t.Helper()
	res := &fakeResolver{table: map[string][]netip.Addr{
		EgressBaseNames[0]: addrs(t, testGitHubV4),
		EgressBaseNames[1]: addrs(t, testAPIV4, testAPIV6),
		EgressBaseNames[2]: addrs(t, testObjectsV4),
		testModelHost:      addrs(t, testModelV4),
	}}
	return EgressInput{
		Run:        "j1",
		PolicyPath: "infra/image/egress.txt",
		Names:      append(append([]string{}, EgressBaseNames...), testModelHost),
		ModelHost:  testModelHost,
		Resolver:   netip.MustParseAddr("10.9.0.53"),
		BenchCIDRs: prefixes(t, "10.1.0.0/24"),
		UID:        "10001",
		Lookup:     res,
	}
}

// allowedNames is the allow set a good plan carries: the base names, then the model host.
func allowedNames() string {
	return strings.Join(append(append([]string{}, EgressBaseNames...), testModelHost), ",")
}

func mustBuild(t *testing.T, in EgressInput) EgressPlan {
	t.Helper()
	p, bad := BuildEgress(in)
	if len(bad) > 0 {
		t.Fatalf("BuildEgress refused a good input: %v", bad)
	}
	return p
}

func TestParseEgressPolicyTakesNamesAndComments(t *testing.T) {
	names, bad := ParseEgressPolicy([]byte("# the header\n\none.example.test\n  two.example.test  # the api\n\n# a comment\nmodel.example.test\n"))
	if len(bad) > 0 {
		t.Fatalf("a well-formed policy was refused: %v", bad)
	}
	want := "one.example.test|two.example.test|model.example.test"
	if got := strings.Join(names, "|"); got != want {
		t.Errorf("the policy's names are %q, want %q (order is the file's)", got, want)
	}
}

func TestParseEgressPolicyRefusesWhatIsNotAHostname(t *testing.T) {
	for _, line := range []string{
		"https://example.com",             // a URL is not a hostname
		"example.com:443",                 // a port is not the file's business
		"198.51.100.10",                   // an address pins nothing and is reviewed nowhere
		"one two.example.test",            // two tokens on one line
		"-example.test",                   // a label may not begin with a hyphen
		"one..example.test",               // an empty label
		"*.example.test",                  // no wildcards: the wall is per name
		strings.Repeat("a", 64) + ".test", // a label over 63 bytes
	} {
		if _, bad := ParseEgressPolicy([]byte(line + "\n")); len(bad) == 0 {
			t.Errorf("ParseEgressPolicy accepted %q; the file is the reviewed contract and only a hostname belongs in it", line)
		}
	}
}

func TestParseEgressPolicyRefusesAnEmptyFile(t *testing.T) {
	if _, bad := ParseEgressPolicy([]byte("# only comments\n\n")); len(bad) == 0 {
		t.Error("a policy with no names was accepted; a plan built from it would allow nothing and the caller would learn that from a silent wall instead of a refusal")
	}
}

// The shipped file is the contract, so this reads IT: the three names Johnny's page fixes
// have to be in it, and so has at least one model host for a run to name. The names
// themselves are data in that file and are not spelled again here.
func TestTheShippedPolicyCarriesTheReviewedNames(t *testing.T) {
	names, bad := ParseEgressPolicy(readShippedPolicy(t))
	if len(bad) > 0 {
		t.Fatalf("infra/image/egress.txt does not parse: %v", bad)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range EgressBaseNames {
		if !have[want] {
			t.Errorf("infra/image/egress.txt does not carry %q; the card cannot reach a name that is not in the reviewed file", want)
		}
	}
	models := 0
	for _, n := range names {
		if !isBaseName(n) {
			models++
		}
	}
	if models == 0 {
		t.Error("infra/image/egress.txt carries no model host; --model-host may only name a host already in the file, so no run could reach a model at all")
	}
}

func TestBuildEgressPinsEveryAllowedNameOnceAndAsksNothingElse(t *testing.T) {
	in := goodInput(t)
	p := mustBuild(t, in)
	res := in.Lookup.(*fakeResolver)
	if got, want := strings.Join(res.asked, ","), allowedNames(); got != want {
		t.Errorf("the plan resolved %q, want %q: every allowed name is asked exactly once at plan time and pinned", got, want)
	}
	if got, want := strings.Join(p.Names, ","), allowedNames(); got != want {
		t.Errorf("the plan allows %q, want %q", got, want)
	}
	if n := len(p.Addrs[EgressBaseNames[1]]); n != 2 {
		t.Errorf("%s pinned %d addresses, want 2: every address the name resolves to is pinned, or the run fails on the one that was left out", EgressBaseNames[1], n)
	}
}

// The model host is the ONE per-run name, and it may only be a name the file already
// carries: Johnny's page says an update is a PR to egress.txt, "Not a runtime flag", so a
// flag that could name any host would be exactly the widening the page refuses.
func TestBuildEgressRefusesAModelHostThatIsNotInThePolicy(t *testing.T) {
	in := goodInput(t)
	in.ModelHost = "other-model.example.test"
	_, bad := BuildEgress(in)
	if !hasReason(bad, "bad_model_host") {
		t.Fatalf("a --model-host outside the policy file was accepted: %v", bad)
	}
}

// A second model host in the file is not a second model host in the run.
func TestBuildEgressAllowsExactlyOneModelHost(t *testing.T) {
	in := goodInput(t)
	const second, secondAddr = "second-model.example.test", "198.51.100.77"
	in.Names = append(in.Names, second)
	in.Lookup.(*fakeResolver).table[second] = addrs(t, secondAddr)
	p := mustBuild(t, in)
	for _, n := range p.Names {
		if n == second {
			t.Fatalf("the plan allows a second model host %q; a run allows the three base names and the ONE host named by --model-host", n)
		}
	}
	if !strings.Contains(p.Text, testModelHost) {
		t.Error("the plan does not name the model host it was given")
	}
	if strings.Contains(p.Text, secondAddr) {
		t.Error("the plan pinned an address of a name it does not allow")
	}
}

func TestBuildEgressRefusesAPolicyMissingABaseName(t *testing.T) {
	in := goodInput(t)
	in.Names = []string{EgressBaseNames[1], testModelHost}
	_, bad := BuildEgress(in)
	if !hasReason(bad, "bad_policy") {
		t.Fatalf("a policy missing %s was accepted: %v; the three base names are the card contract's and a plan that quietly left one out would fail inside the run instead", EgressBaseNames[0], bad)
	}
}

// Fail closed on a resolver that answers with an address inside a denied range: that is
// either a poisoned answer or a rebinding, and either way the safe move is no plan at all.
func TestBuildEgressRefusesAPinnedAddressInsideADeniedRange(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "169.254.169.254", "::1", "10.1.0.7"} {
		in := goodInput(t)
		in.Lookup.(*fakeResolver).table[EgressBaseNames[1]] = addrs(t, bad)
		_, refusals := BuildEgress(in)
		if !hasReason(refusals, "bad_address") {
			t.Errorf("a name resolved to %s and the plan was built anyway: %v; a pinned address inside a denied range is a poisoned answer and the plan fails closed", bad, refusals)
		}
	}
}

func TestBuildEgressRefusesAResolverThatIsItselfDenied(t *testing.T) {
	for _, r := range []string{"127.0.0.53", "169.254.169.254", "10.1.0.9"} {
		in := goodInput(t)
		in.Resolver = netip.MustParseAddr(r)
		_, bad := BuildEgress(in)
		if !hasReason(bad, "bad_resolver") {
			t.Errorf("--resolver %s was accepted: %v; the deny rules come first, so DNS would be dropped and every name would fail with no line saying why", r, bad)
		}
	}
}

func TestBuildEgressRefusesWithNoSelector(t *testing.T) {
	in := goodInput(t)
	in.UID, in.Veth = "", ""
	_, bad := BuildEgress(in)
	if !hasReason(bad, "no_selector") {
		t.Fatalf("a plan with neither --uid nor --veth was built: %v; every rule is scoped to the card's own traffic, and an unscoped default deny would firewall the bench itself", bad)
	}
}

func TestBuildEgressRefusesABadRunName(t *testing.T) {
	for _, run := range []string{"", "a b", "j1; drop", "../x", strings.Repeat("j", 40)} {
		in := goodInput(t)
		in.Run = run
		if _, bad := BuildEgress(in); !hasReason(bad, "no_name") {
			t.Errorf("--run %q was accepted; it becomes an nft table name", run)
		}
	}
}

func TestBuildEgressRefusesAResolverFailure(t *testing.T) {
	in := goodInput(t)
	in.Lookup.(*fakeResolver).err = errors.New("i/o timeout")
	_, bad := BuildEgress(in)
	if !hasReason(bad, "resolve_failed") {
		t.Fatalf("a resolver failure did not refuse the plan: %v; a name that cannot be pinned is a name the run cannot reach, and that is a refusal at plan time rather than a surprise inside the card", bad)
	}
}

// The rendered ruleset: the shape a reader checks, asserted line by line rather than as a
// blob, because each of these lines is a separate promise.
func TestRenderedPlanHasTheShapeThePageAsksFor(t *testing.T) {
	p := mustBuild(t, goodInput(t))
	for _, want := range []string{
		"table inet nova_egress_j1 {",
		"type filter hook output priority 0; policy accept;",
		"meta skuid 10001 ip daddr 169.254.169.254/32 drop",
		"meta skuid 10001 ip daddr 127.0.0.0/8 drop",
		"meta skuid 10001 ip6 daddr ::1/128 drop",
		"meta skuid 10001 ip daddr 10.1.0.0/24 drop",
		"meta skuid 10001 ip daddr 10.9.0.53 udp dport 53 accept",
		"meta skuid 10001 ip daddr 198.51.100.10 tcp dport 443 accept",
		"meta skuid 10001 ip6 daddr 2001:db8::11 tcp dport 443 accept",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("the rendered plan has no line %q:\n%s", want, p.Text)
		}
	}
	// The default deny is the LAST rule of the chain, and it is the whole point: anything
	// the allows above did not name is dropped.
	lines := ruleLines(p.Text)
	if last := lines[len(lines)-1]; last != "meta skuid 10001 drop" {
		t.Errorf("the last rule of the chain is %q, want the default deny `meta skuid 10001 drop`", last)
	}
	// Every rule carries the selector: one unscoped rule would be a rule about the bench.
	for _, l := range lines {
		if !strings.HasPrefix(l, "meta skuid 10001 ") {
			t.Errorf("rule %q is not scoped to the card; an unscoped rule in the output hook is a rule about the bench's own traffic", l)
		}
	}
}

func TestRenderedPlanCarriesAVethChainWhenAVethIsNamed(t *testing.T) {
	in := goodInput(t)
	in.UID, in.Veth = "", "veth-j1"
	p := mustBuild(t, in)
	for _, want := range []string{
		"type filter hook forward priority 0; policy accept;",
		`iifname "veth-j1" ip daddr 169.254.169.254/32 drop`,
		`iifname "veth-j1" drop`,
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("the veth plan has no line %q:\n%s", want, p.Text)
		}
	}
}

func TestBuildEgressRefusesAVethNameThatIsNotOne(t *testing.T) {
	in := goodInput(t)
	in.UID, in.Veth = "", `veth"; drop`
	if _, bad := BuildEgress(in); !hasReason(bad, "bad_veth") {
		t.Fatal("a veth name carrying a quote was accepted; it goes inside quotes in an nft rule")
	}
}

func TestBuildEgressRefusesAUIDThatIsNotANumber(t *testing.T) {
	in := goodInput(t)
	in.UID = "root"
	if _, bad := BuildEgress(in); !hasReason(bad, "bad_uid") {
		t.Fatal("a --uid that is not a number was accepted; it goes into `meta skuid <n>` verbatim")
	}
}

func TestPlanCountsAreTheRulesItRendered(t *testing.T) {
	p := mustBuild(t, goodInput(t))
	allow, deny := 0, 0
	for _, l := range ruleLines(p.Text) {
		switch {
		case strings.HasSuffix(l, " accept"):
			allow++
		case strings.HasSuffix(l, " drop"):
			deny++
		default:
			t.Errorf("rule %q ends in neither accept nor drop; the audit reads these lines and a third verdict would be one it cannot check", l)
		}
	}
	if p.Allow != allow || p.Deny != deny {
		t.Errorf("the plan counts allow=%d deny=%d, rendered allow=%d deny=%d; the receipt is what a reader compares with the file", p.Allow, p.Deny, allow, deny)
	}
}

// The test of the tests: the audit has to go RED on a plan that lost each invariant. A
// checker that only ever passes is a checker that checks nothing.
func TestCheckEgressPlanPassesTheRealPlan(t *testing.T) {
	p := mustBuild(t, goodInput(t))
	audit, bad := CheckEgressPlan(p.Text)
	if len(bad) > 0 {
		t.Fatalf("the plan this tool renders fails its own audit: %v", bad)
	}
	if audit.Allow != p.Allow || audit.Deny != p.Deny {
		t.Errorf("the audit counts allow=%d deny=%d, the plan says allow=%d deny=%d", audit.Allow, audit.Deny, p.Allow, p.Deny)
	}
	if audit.Table != "nova_egress_j1" {
		t.Errorf("the audit read the table as %q, want nova_egress_j1", audit.Table)
	}
}

func TestCheckEgressPlanGoesRedOnEachLostInvariant(t *testing.T) {
	good := mustBuild(t, goodInput(t)).Text
	cases := []struct {
		name, from, to, reason string
	}{
		{"the default deny is gone", "\tmeta skuid 10001 drop\n", "", "no_default_deny"},
		{"an allow to the whole internet", "meta skuid 10001 ip daddr 198.51.100.10 tcp dport 443 accept", "meta skuid 10001 ip daddr 0.0.0.0/0 tcp dport 443 accept", "allow_any"},
		{"an allow on a port that is neither 443 nor 53", "tcp dport 443 accept", "tcp dport 22 accept", "allow_port"},
		{"the metadata address is no longer denied", "meta skuid 10001 ip daddr 169.254.169.254/32 drop\n", "", "no_metadata_deny"},
		{"a rule that is not scoped to the card", "meta skuid 10001 ip daddr 198.51.100.10 tcp dport 443 accept", "ip daddr 198.51.100.10 tcp dport 443 accept", "unscoped_rule"},
		{"an allow with no port at all", "meta skuid 10001 ip daddr 198.51.100.10 tcp dport 443 accept", "meta skuid 10001 ip daddr 198.51.100.10 accept", "allow_port"},
		{"the chain's base policy is not accept", "policy accept;", "policy drop;", "bad_chain"},
		{"a line the grammar does not have", "meta skuid 10001 drop\n", "meta skuid 10001 drop\n\t\tcounter name whatever\n", "bad_rule"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			broken := strings.Replace(good, c.from, c.to, 1)
			if broken == good {
				t.Fatalf("the test did not change the plan: %q is not in it", c.from)
			}
			_, bad := CheckEgressPlan(broken)
			if !hasReason(bad, c.reason) {
				t.Errorf("the audit passed a plan whose %s (want reason=%s, got %v)", c.name, c.reason, bad)
			}
		})
	}
}

func TestCheckEgressPlanRefusesSomethingThatIsNotAPlan(t *testing.T) {
	for _, text := range []string{"", "# only a comment\n", "table ip nova_egress_j1 {\n}\n", "table inet other {\n}\n"} {
		if _, bad := CheckEgressPlan(text); len(bad) == 0 {
			t.Errorf("the audit passed %q, which is not a plan this tool wrote", text)
		}
	}
}

// readShippedPolicy reads the file in git, not a fixture: infra/image/egress.txt IS the
// reviewed contract, and a test that stood on a copy of it would go green on the day the
// two disagreed.
func readShippedPolicy(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "image", "egress.txt"))
	if err != nil {
		t.Fatalf("infra/image/egress.txt is the allowlist and it has to be readable: %s", err)
	}
	return raw
}

func hasReason(bad []Refusal, reason string) bool {
	for _, r := range bad {
		if r.Reason == reason {
			return true
		}
	}
	return false
}

// ruleLines is every rule of the rendered plan: the indented lines inside a chain that are
// not the chain header, the braces or a comment.
func ruleLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "table ") ||
			strings.HasPrefix(t, "chain ") || strings.HasPrefix(t, "type filter hook") || t == "}" {
			continue
		}
		out = append(out, t)
	}
	return out
}
