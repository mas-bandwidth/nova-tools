// The egress wall is the card's OUTBOUND half: the filesystem wall this package builds
// everywhere else says what a card may read and write, and this file says what it may
// talk to. Johnny's design page of 2026-09-18 is the contract:
//
//	Where: nftables on the bench, applied to the slirp/pasta veth of each `podman run`.
//	Not an env list the worker applies (the worker is the adversary). Not --network=host.
//	Policy file in git: infra/image/egress.txt (one hostname per line, comments #).
//	Default deny. Allow TCP 443 only: github.com, api.github.com,
//	objects.githubusercontent.com, the one model host named in the worker description
//	(resolved at run start, pinned for the run). Deny: 169.254.169.254/32, 127.0.0.0/8 as
//	a destination, other benches, UDP except DNS to the resolver for those names.
//
// Three things in this file are the page's silences, read the SAFER way and said out loud
// here and in docs/SPEC-SANDBOX.md, because a silence read the loose way is a hole:
//
//  1. `--model-host` may only name a host the policy file already carries. The page says an
//     update is "a PR to egress.txt, reviewed by this sitting. Not a runtime flag", and a
//     flag that could name ANY host would be exactly that runtime flag. The file is the
//     reviewed universe; the flag picks the ONE model host out of it for this run.
//  2. A pinned address inside a denied range — loopback, link-local, a bench — refuses the
//     whole plan. That answer comes from a resolver, the resolver is not ours, and a name
//     that resolves to 127.0.0.1 is either poisoned or a rebinding. Fail closed.
//  3. Every rule is scoped to the card's own traffic (`meta skuid <n>` or
//     `iifname "<veth>"`) and a plan with neither selector REFUSES. An unscoped default
//     deny in the output hook would firewall the bench itself, which is a far worse
//     failure than the one it was meant to prevent.
//
// Nothing here executes anything: BuildEgress renders text, and the tool's egress verbs are
// what hand that text to nft. The resolver is an interface so the tests pin addresses
// without a packet (unit tests never touch the network).
package sandbox

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// EgressBaseNames are the three names EVERY card gets, fixed by Johnny's page. They are
// named here AND asserted present in infra/image/egress.txt: the file is the contract, and
// this constant is checked against it rather than trusted beside it.
var EgressBaseNames = []string{"github.com", "api.github.com", "objects.githubusercontent.com"}

// egressTablePrefix is the one table name this tool makes and the one it deletes. A table
// that does not begin with it was not made here and is never touched.
const egressTablePrefix = "nova_egress_"

// EgressTableName is the nft table one run's wall lives in.
func EgressTableName(run string) string { return egressTablePrefix + run }

// egressDeniedRanges are the destinations denied outright, before any allow is considered.
// The metadata address is listed FIRST and by itself even though the link-local /16 below
// covers it: it is the one destination a stolen card most wants, and a reader of the plan —
// and the audit — should find it by name rather than by arithmetic.
var egressDeniedRanges = []netip.Prefix{
	netip.MustParsePrefix("169.254.169.254/32"), // cloud metadata: the credential vending machine
	netip.MustParsePrefix("169.254.0.0/16"),     // the rest of link-local
	netip.MustParsePrefix("127.0.0.0/8"),        // the bench's own loopback services
	netip.MustParsePrefix("::1/128"),            // loopback again, v6: the page says loopback, not "loopback in one family"
	netip.MustParsePrefix("fe80::/10"),          // link-local again, v6
}

// egressMetadata is the address the audit insists on finding denied by name.
var egressMetadata = netip.MustParseAddr("169.254.169.254")

// EgressResolver is the whole of the plan's contact with DNS. The production body asks the
// bench's resolver (the same one the card is then allowed to reach); the tests put a table
// behind it, so a plan is built and audited without a packet.
type EgressResolver interface {
	LookupHost(name string) ([]netip.Addr, error)
}

// EgressInput is one plan's worth of input. Every field comes from a flag or from the
// policy file; none of it is guessed.
type EgressInput struct {
	Run        string         // the run id; the table is nova_egress_<run>
	PolicyPath string         // named in the plan's header, for the reader
	Names      []string       // the policy file's entries, in file order
	ModelHost  string         // the ONE model host of this run; must be in Names
	Resolver   netip.Addr     // the resolver DNS is allowed to
	BenchCIDRs []netip.Prefix // the other benches, denied
	UID        string         // `meta skuid <n>` — the container's uid on the host
	Veth       string         // `iifname "<veth>"` — the container's interface
	Lookup     EgressResolver
}

// EgressPlan is the rendered ruleset and the receipt that goes with it.
type EgressPlan struct {
	Run   string                  // the run id
	Table string                  // nova_egress_<run>
	Names []string                // the names this run allows, in order
	Addrs map[string][]netip.Addr // what each name was pinned to
	Allow int                     // accept rules rendered
	Deny  int                     // drop rules rendered, the default deny included
	Text  string                  // the nft ruleset, the thing `nft -f` is handed
}

// egressChain is one selector's chain: the hook it sits in and the prefix every one of its
// rules carries.
type egressChain struct {
	name, hook, selector string
}

// BuildEgress resolves the allowed names, pins the addresses and renders the ruleset. It
// returns EVERY independent problem rather than the first (rule 16).
func BuildEgress(in EgressInput) (EgressPlan, []Refusal) {
	var bad []Refusal
	if !OKEgressRun(in.Run) {
		bad = append(bad, refuse("no_name", "--run wants one short id out of letters, digits and _ : it becomes the nft table %s<run>", egressTablePrefix))
	}
	chains, chainBad := egressChains(in)
	bad = append(bad, chainBad...)

	have := map[string]bool{}
	for _, n := range in.Names {
		have[n] = true
	}
	for _, n := range EgressBaseNames {
		if !have[n] {
			bad = append(bad, refuse("bad_policy", "the policy file does not carry %s, one of the three names every card needs; add it in a PR to the file rather than widening the plan at run time", n))
		}
	}
	if in.ModelHost == "" {
		bad = append(bad, refuse("bad_model_host", "--model-host wants the one model host of this run, and it must already be a line in the policy file: --model-host api.deepseek.com"))
	} else if !have[in.ModelHost] {
		bad = append(bad, refuse("bad_model_host", "%s is not in the policy file; an update to the allowlist is a PR to that file reviewed by the security lane, never a flag on one run", in.ModelHost))
	}

	// The run's allow set: the three base names in the page's order, then the one model
	// host. Any OTHER name in the file belongs to another run's model and is held back.
	names := append([]string{}, EgressBaseNames...)
	if in.ModelHost != "" && have[in.ModelHost] && !isBaseName(in.ModelHost) {
		names = append(names, in.ModelHost)
	}

	denied := append(append([]netip.Prefix{}, egressDeniedRanges...), in.BenchCIDRs...)
	if !in.Resolver.IsValid() {
		bad = append(bad, refuse("bad_resolver", "--resolver wants the bench resolver's address, the one destination UDP 53 is allowed to: --resolver 10.1.0.1"))
	} else if p, ok := coveredBy(in.Resolver, denied); ok {
		bad = append(bad, refuse("bad_resolver", "the resolver %s is inside %s, which this plan denies; the deny rules come first, so DNS would be dropped and every name would fail with nothing saying why — name the bench's own resolver", in.Resolver, p))
	}

	addrs := map[string][]netip.Addr{}
	if in.Lookup == nil {
		bad = append(bad, refuse("resolve_failed", "no resolver was given to the plan; the names are resolved at run start and pinned, and a plan built from no answers would allow nothing"))
	} else {
		for _, n := range names {
			got, err := in.Lookup.LookupHost(n)
			if err != nil {
				bad = append(bad, refuse("resolve_failed", "%s could not be resolved: %s; a name that cannot be pinned is a name the run cannot reach", n, err))
				continue
			}
			if len(got) == 0 {
				bad = append(bad, refuse("resolve_failed", "%s resolved to no address at all", n))
				continue
			}
			for _, a := range got {
				if p, ok := coveredBy(a, denied); ok {
					bad = append(bad, refuse("bad_address", "%s resolved to %s, which is inside the denied range %s; that answer is a poisoned one or a rebinding, and the plan fails closed rather than punching a hole for it", n, a, p))
				}
			}
			addrs[n] = sortAddrs(got)
		}
	}
	if len(bad) > 0 {
		return EgressPlan{}, bad
	}

	plan := EgressPlan{Run: in.Run, Table: EgressTableName(in.Run), Names: names, Addrs: addrs}
	plan.Text, plan.Allow, plan.Deny = renderEgress(in, plan, chains)
	return plan, nil
}

// egressChains is the selector half of the input: a uid becomes a rule in the OUTPUT hook
// (rootless podman's slirp/pasta sends the card's packets from the host as that uid) and a
// veth becomes a rule in the FORWARD hook (the packets arrive on the container's interface
// and are routed). Either alone is a wall; neither is a refusal.
func egressChains(in EgressInput) ([]egressChain, []Refusal) {
	var out []egressChain
	var bad []Refusal
	if in.UID != "" {
		if !allDigits(in.UID) || len(in.UID) > 10 {
			bad = append(bad, refuse("bad_uid", "--uid wants the container's uid on the host, a whole number: --uid 10001"))
		} else {
			out = append(out, egressChain{name: "output", hook: "output", selector: "meta skuid " + in.UID})
		}
	}
	if in.Veth != "" {
		if !okIfname(in.Veth) {
			bad = append(bad, refuse("bad_veth", "--veth wants an interface name out of letters, digits, - _ . and @ , at most 15 bytes: --veth veth-j1"))
		} else {
			out = append(out, egressChain{name: "forward", hook: "forward", selector: `iifname "` + in.Veth + `"`})
		}
	}
	if in.UID == "" && in.Veth == "" {
		bad = append(bad, refuse("no_selector", "a plan needs --uid or --veth (or both): every rule is scoped to the card's own traffic, and an unscoped default deny in the output hook would firewall the bench itself"))
	}
	return out, bad
}

// renderEgress writes the ruleset. The ORDER inside a chain is the contract: the denies
// first, so that nothing below can undo them; then the one DNS allow and the pinned TCP 443
// allows; then the bare selector drop, which is the default deny and the last word.
func renderEgress(in EgressInput, plan EgressPlan, chains []egressChain) (string, int, int) {
	var b strings.Builder
	allow, deny := 0, 0
	fmt.Fprintf(&b, "# nova-sandbox egress plan — the card's outbound wall (docs/SPEC-SANDBOX.md)\n")
	fmt.Fprintf(&b, "# run=%s policy=%s\n", plan.Run, in.PolicyPath)
	fmt.Fprintf(&b, "# names=%s\n", strings.Join(plan.Names, ","))
	fmt.Fprintf(&b, "# DEFAULT DENY: every rule below is scoped to the card's own traffic and the\n")
	fmt.Fprintf(&b, "# last rule of each chain drops whatever the allows above did not name. The\n")
	fmt.Fprintf(&b, "# bench's own traffic never reaches that drop.\n")
	fmt.Fprintf(&b, "table inet %s {\n", plan.Table)
	for _, c := range chains {
		fmt.Fprintf(&b, "\tchain %s {\n", c.name)
		// policy accept, not policy drop: the chain's base policy is the BENCH's traffic
		// too, and a drop there would take the machine off the network. The default deny
		// this wall promises is the selector drop at the bottom.
		fmt.Fprintf(&b, "\t\ttype filter hook %s priority 0; policy accept;\n", c.hook)
		for _, p := range append(append([]netip.Prefix{}, egressDeniedRanges...), in.BenchCIDRs...) {
			fmt.Fprintf(&b, "\t\t%s %s daddr %s drop\n", c.selector, famOf(p.Addr()), p)
			deny++
		}
		fmt.Fprintf(&b, "\t\t%s %s daddr %s udp dport 53 accept\n", c.selector, famOf(in.Resolver), in.Resolver)
		allow++
		for _, n := range plan.Names {
			for _, a := range plan.Addrs[n] {
				fmt.Fprintf(&b, "\t\t%s %s daddr %s tcp dport 443 accept\n", c.selector, famOf(a), a)
				allow++
			}
		}
		fmt.Fprintf(&b, "\t\t%s drop\n", c.selector)
		deny++
		fmt.Fprintf(&b, "\t}\n")
	}
	fmt.Fprintf(&b, "}\n")
	return b.String(), allow, deny
}

// ParseEgressPolicy reads infra/image/egress.txt: one hostname per line, `#` starts a
// comment, blank lines are nothing. Everything else is a refusal — the file is the reviewed
// contract, and a line nobody can read as a hostname is a line nobody reviewed as one.
func ParseEgressPolicy(data []byte) ([]string, []Refusal) {
	var names []string
	var bad []Refusal
	seen := map[string]bool{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := raw
		if at := strings.IndexByte(line, '#'); at >= 0 {
			line = line[:at]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.ContainsAny(line, " \t") {
			bad = append(bad, refuse("bad_policy", "line %d of the policy is not one hostname: %q; one hostname per line, comments start with #", i+1, line))
			continue
		}
		name := strings.ToLower(line)
		if !okHostname(name) {
			bad = append(bad, refuse("bad_policy", "line %d of the policy is not a hostname: %q; the file holds names, never URLs, ports, addresses or wildcards", i+1, line))
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 && len(bad) == 0 {
		bad = append(bad, refuse("bad_policy", "the policy file carries no hostname; a plan built from it would allow nothing, and a card would learn that from a silent wall instead of this line"))
	}
	return names, bad
}

// EgressAudit is what CheckEgressPlan read back out of a rendered plan.
type EgressAudit struct {
	Table  string
	Chains []string
	Allow  int
	Deny   int
	Rules  int
}

// CheckEgressPlan parses a rendered plan back and asserts the invariants: one nova_egress
// table, every chain based on `policy accept`, every rule scoped to that chain's selector,
// every accept naming ONE address and port 443 or 53, the metadata address denied by name,
// and the last rule of every chain the bare selector drop. It is the test of the tests —
// a plan that passes this is a plan whose promises can be read off it, by a reader or by
// CI, without trusting the renderer that wrote it.
//
// Anything it cannot parse is a refusal, not a shrug: an nft line this grammar does not
// know is a line whose effect the audit cannot judge.
func CheckEgressPlan(text string) (EgressAudit, []Refusal) {
	var audit EgressAudit
	var bad []Refusal
	var chain *egressChain
	chainRules := 0
	lastRule := ""
	sawMetadataDeny := false
	depth := 0
	tables := 0

	closeChain := func() {
		if chain == nil {
			return
		}
		if chainRules == 0 || lastRule != chain.selector+" drop" {
			bad = append(bad, refuse("no_default_deny", "chain %s does not END in the default deny `%s drop`; the last rule is what everything the allows did not name falls to", chain.name, chain.selector))
		}
		if !sawMetadataDeny {
			bad = append(bad, refuse("no_metadata_deny", "chain %s does not deny %s by name; the metadata address is denied explicitly so a reader finds it without arithmetic", chain.name, egressMetadata))
		}
		chain, chainRules, lastRule, sawMetadataDeny = nil, 0, "", false
	}

	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "table "):
			tables++
			name, ok := strings.CutPrefix(line, "table inet ")
			name = strings.TrimSpace(strings.TrimSuffix(name, "{"))
			if !ok || !strings.HasPrefix(name, egressTablePrefix) {
				bad = append(bad, refuse("bad_table", "line %d is %q; a plan this tool wrote holds one `table inet %s<run>` and nothing else", i+1, line, egressTablePrefix))
				continue
			}
			audit.Table = name
			depth++
		case strings.HasPrefix(line, "chain "):
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "chain "), "{"))
			chain = &egressChain{name: name}
			audit.Chains = append(audit.Chains, name)
			depth++
		case strings.HasPrefix(line, "type filter hook "):
			rest, ok := strings.CutPrefix(line, "type filter hook ")
			hook, tail, _ := strings.Cut(rest, " ")
			if chain == nil || !ok || (hook != "output" && hook != "forward") || !strings.HasSuffix(strings.TrimSpace(tail), "policy accept;") {
				bad = append(bad, refuse("bad_chain", "line %d is %q; every chain of a plan is a filter chain in the output or forward hook with `policy accept` — the default deny is the selector drop at the bottom, never the chain's base policy", i+1, line))
				continue
			}
			chain.hook = hook
		case line == "}":
			closeChain()
			depth--
		default:
			if chain == nil {
				bad = append(bad, refuse("bad_rule", "line %d is outside any chain: %q", i+1, line))
				continue
			}
			r, err := parseEgressRule(line)
			if err != nil {
				bad = append(bad, refuse("bad_rule", "line %d is not a rule this audit can judge: %q (%s)", i+1, line, err))
				continue
			}
			if r.selector == "" {
				bad = append(bad, refuse("unscoped_rule", "line %d is not scoped to the card: %q; an unscoped rule in this chain is a rule about the bench's own traffic", i+1, line))
				continue
			}
			if chain.selector == "" {
				chain.selector = r.selector
			} else if r.selector != chain.selector {
				bad = append(bad, refuse("unscoped_rule", "line %d carries the selector %q and its chain's is %q; one chain is one card", i+1, r.selector, chain.selector))
				continue
			}
			audit.Rules++
			chainRules++
			lastRule = line
			if r.accept {
				audit.Allow++
				if !r.hasDst || r.dst.Bits() != r.dst.Addr().BitLen() {
					bad = append(bad, refuse("allow_any", "line %d allows more than one address: %q; every allow names ONE pinned address, and a prefix is how a wall becomes a suggestion", i+1, line))
				}
				switch {
				case r.proto == "tcp" && r.port == 443, r.proto == "udp" && r.port == 53:
				default:
					bad = append(bad, refuse("allow_port", "line %d allows %q; the only allows are TCP 443 to a pinned address and UDP 53 to the resolver", i+1, line))
				}
			} else {
				audit.Deny++
				if r.hasDst && r.dst == netip.PrefixFrom(egressMetadata, egressMetadata.BitLen()) {
					sawMetadataDeny = true
				}
			}
		}
	}
	closeChain()
	if tables != 1 || audit.Table == "" {
		bad = append(bad, refuse("bad_table", "the plan holds %d `table` lines, want exactly one `table inet %s<run>`", tables, egressTablePrefix))
	}
	if len(audit.Chains) == 0 {
		bad = append(bad, refuse("bad_chain", "the plan holds no chain; a table with no chain enforces nothing at all"))
	}
	if depth != 0 {
		bad = append(bad, refuse("bad_rule", "the plan's braces do not balance; a half-written ruleset is not one nft would apply"))
	}
	return audit, bad
}

// egressRule is one parsed rule of the restricted grammar this tool renders.
type egressRule struct {
	selector string
	hasDst   bool
	dst      netip.Prefix
	proto    string
	port     int
	accept   bool
}

// parseEgressRule reads ONE rule of the grammar and nothing else:
//
//	[meta skuid <n> | iifname "<name>"] [ip|ip6 daddr <addr|prefix>] [tcp|udp dport <n>] accept|drop
func parseEgressRule(line string) (egressRule, error) {
	var r egressRule
	f := strings.Fields(line)
	switch {
	case len(f) >= 3 && f[0] == "meta" && f[1] == "skuid":
		r.selector, f = strings.Join(f[:3], " "), f[3:]
	case len(f) >= 2 && f[0] == "iifname":
		r.selector, f = strings.Join(f[:2], " "), f[2:]
	}
	if len(f) >= 3 && (f[0] == "ip" || f[0] == "ip6") && f[1] == "daddr" {
		p, err := parseAddrOrPrefix(f[2])
		if err != nil {
			return r, fmt.Errorf("%q is not an address or a prefix", f[2])
		}
		if famOf(p.Addr()) != f[0] {
			return r, fmt.Errorf("%s is not an %s address", f[2], f[0])
		}
		r.hasDst, r.dst, f = true, p, f[3:]
	}
	if len(f) >= 3 && (f[0] == "tcp" || f[0] == "udp") && f[1] == "dport" {
		if _, err := fmt.Sscanf(f[2], "%d", &r.port); err != nil {
			return r, fmt.Errorf("%q is not a port", f[2])
		}
		r.proto, f = f[0], f[3:]
	}
	if len(f) != 1 {
		return r, fmt.Errorf("the rule does not end in one verdict")
	}
	switch f[0] {
	case "accept":
		r.accept = true
	case "drop":
	default:
		return r, fmt.Errorf("%q is neither accept nor drop", f[0])
	}
	return r, nil
}

func parseAddrOrPrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func famOf(a netip.Addr) string {
	if a.Is4() {
		return "ip"
	}
	return "ip6"
}

func coveredBy(a netip.Addr, ranges []netip.Prefix) (netip.Prefix, bool) {
	for _, p := range ranges {
		if p.Contains(a) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

// sortAddrs puts the pinned addresses in one order — v4 before v6, each ascending, no
// duplicate — so that two plans built from the same answers are the same bytes.
func sortAddrs(in []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(in))
	seen := map[netip.Addr]bool{}
	for _, a := range in {
		a = a.Unmap()
		if a.IsValid() && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Is4() != out[j].Is4() {
			return out[i].Is4()
		}
		return out[i].Less(out[j])
	})
	return out
}

func isBaseName(n string) bool {
	for _, b := range EgressBaseNames {
		if b == n {
			return true
		}
	}
	return false
}

// OKEgressRun is the shape a --run may take. It is narrow because the value becomes an
// nft table name, which is an identifier and not a string.
func OKEgressRun(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// okIfname is the shape an interface name may take: Linux's own limit is 15 bytes, and the
// alphabet is narrow because the value goes inside quotes in a generated rule.
func okIfname(s string) bool {
	if s == "" || len(s) > 15 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
		default:
			return false
		}
	}
	return true
}

// okHostname is one DNS name: labels of letters, digits and hyphens, no empty label, no
// label over 63 bytes, at most 253 bytes, and a final label that begins with a letter —
// which is what keeps an address out of a file of names.
func okHostname(s string) bool {
	if s == "" || len(s) > 253 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, r := range l {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return last[0] >= 'a' && last[0] <= 'z'
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
