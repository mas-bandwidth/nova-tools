package pulse

// `nova-pulse fleet net` is the network half of the fleet: the tailnet a node runs, read
// against the machines registry that says what the node HAS.
//
// The design is Glenn's, 2026-09-18: every NODE -- a team of humans and AI seats -- has its
// OWN tailnet. Cross-node reach is a Tailscale node share, one machine offered into another
// node's tailnet and written down in the registry as `shared-from:<node>`; it is never a
// merged tailnet. The bus (git) is the federation. A node with no tailnet is still a node,
// which is why no verb here may hardcode a tailnet address and why the registry carries a
// `provider` column instead.
//
// And it is GENERIC. Our fleet is the first node, not the only one: everything below is
// driven by the registry a node keeps and by the node's own name, so anybody running
// nova-tools gets the same tailnet for the same one command.
//
// Two verbs are real here. `net status` is the read-only one certify needs, behind the
// Tailnet seam so every test drives a fake and no test opens a socket. `net init` is pure
// file generation: registry in, three files out, golden-tested. The rest --
// acl, ssh, join, expiry, names, share, serve -- are specified in docs/SPEC-FLEET-NET.md
// and refuse by name until they are written, because a stub that pretends to work is worse
// than one that says what it is.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// NetToken is the first word of every line these verbs print.
const NetToken = "NET"

// The refusal reasons, as tokens rather than prose, so a loop can branch on one and a
// person reading the line learns the same thing.
const (
	NetReasonNotImplemented = "not-implemented"
	NetReasonNoMachines     = "no-machines"
	NetReasonUnreadable     = "unreadable"
	NetReasonTailnet        = "tailnet-unreadable"
	NetReasonExists         = "exists"
	NetReasonUnknownNode    = "unknown-node"
)

// The finding reasons `net status` prints. A finding is not a refusal: the verb ran and the
// state it found is inconsistent, which is the tool saying NO (exit 1).
const (
	NetFindingNoNode        = "no-tailnet-node" // the registry has it, the tailnet does not
	NetFindingNotInRegistry = "not-in-registry" // the tailnet has it, the registry does not
	NetFindingOffline       = "offline"         // a tailnet machine the tailnet has not seen
)

// ---------------------------------------------------------------------------
// The seam
// ---------------------------------------------------------------------------

// TailnetNode is one node as the tailnet reports it. It is deliberately the small subset
// every implementation can answer: a name, an address, whether the coordination server has
// seen it lately, and when it last did.
type TailnetNode struct {
	Name     string    // the node's name, without the tailnet suffix
	Addr     string    // its first address; "" when the tailnet reports none
	Online   bool      // whether the tailnet considers it connected now
	LastSeen time.Time // zero when the tailnet reports none (a node that is online now)
}

// Tailnet is the seam. Everything that talks to a real tailnet is behind it, so the verbs
// are tested against a fake and no test in this repository opens a socket (the hard rule of
// 2026-09-17: unit tests never touch the network).
type Tailnet interface {
	// Status is every node of THIS node's tailnet, in whatever order the tailnet gives
	// them. The caller sorts.
	Status(ctx context.Context) ([]TailnetNode, error)
}

// NewTailnet is how a verb gets one, and the one place a test replaces. The default runs
// the tailscale CLI that is already on the machine -- Tailscale's own tool, parsed, not
// reimplemented.
var NewTailnet = func(program string) Tailnet { return cliTailnet{Program: program} }

// cliTailnet runs `tailscale status --json` and reads the fields the seam names. It is the
// only code here that runs a process.
type cliTailnet struct{ Program string }

func (c cliTailnet) Status(ctx context.Context) ([]TailnetNode, error) {
	program := strings.TrimSpace(c.Program)
	if program == "" {
		program = "tailscale"
	}
	out, err := exec.CommandContext(ctx, program, "status", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("%s status --json: %w", program, err)
	}
	return parseTailscaleStatus(out)
}

// tailscaleStatus is the shape of `tailscale status --json` this verb reads, and no more of
// it: Self plus Peer, each a PeerStatus. Fields we do not read are not declared, so the
// parse does not break when Tailscale adds one.
type tailscaleStatus struct {
	Self *tailscalePeer            `json:"Self"`
	Peer map[string]*tailscalePeer `json:"Peer"`
}

type tailscalePeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
	LastSeen     string   `json:"LastSeen"`
}

// parseTailscaleStatus folds the JSON into the seam's nodes. It is a function of its own so
// a test can read a recorded document without running anything.
func parseTailscaleStatus(raw []byte) ([]TailnetNode, error) {
	var st tailscaleStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("cannot read the tailscale status document: %w", err)
	}
	var out []TailnetNode
	add := func(p *tailscalePeer) {
		if p == nil {
			return
		}
		node := TailnetNode{Name: tailnetName(p), Online: p.Online}
		if len(p.TailscaleIPs) > 0 {
			node.Addr = p.TailscaleIPs[0]
		}
		if t, err := time.Parse(time.RFC3339, p.LastSeen); err == nil {
			node.LastSeen = t.UTC()
		}
		if node.Name != "" {
			out = append(out, node)
		}
	}
	add(st.Self)
	for _, p := range st.Peer {
		add(p)
	}
	return out, nil
}

// tailnetName is the node's short name: the first label of its MagicDNS name, or its
// hostname when the tailnet gives no DNS name. MagicDNS is on (SPEC-FLEET-NET.md R8), so
// the DNS name is the authority and the hostname is the fallback.
func tailnetName(p *tailscalePeer) string {
	if dns := strings.TrimSpace(p.DNSName); dns != "" {
		if label, _, ok := strings.Cut(dns, "."); ok && label != "" {
			return label
		}
		return dns
	}
	return strings.TrimSpace(p.HostName)
}

// ---------------------------------------------------------------------------
// net status
// ---------------------------------------------------------------------------

// FleetNetStatusInput is everything the verb needs apart from flag parsing.
type FleetNetStatusInput struct {
	Machines  string // the machines registry
	Tailscale string // the tailscale program; "" is `tailscale` on PATH
	Max       int    // at most this many lines of each kind; 0 is all
	Timeout   time.Duration
	Stdout    io.Writer
	Stderr    io.Writer
}

// FleetNetStatus is the tailnet view CROSSED with the registry. It prints one line per
// machine and one per finding, and its verdict counts both:
//
//	NET MACHINE hulk provider=tailnet online=yes addr=100.1.2.3 last-seen=-
//	NET FINDING machine=ada reason=no-tailnet-node (...)
//	NET STATUS OK machines=5 online=5 findings=0
//
// Exit 0 when the two agree, 1 when they do not (the tool saying NO: a finding is a state
// somebody must act on, not a failure to run), 2 when it could not run at all.
func FleetNetStatus(in FleetNetStatusInput) int {
	if strings.TrimSpace(in.Machines) == "" {
		return netRefuse(in.Stderr, "status", NetReasonNoMachines,
			"missing --machines; refusing to guess",
			"run: nova-pulse fleet net status --machines queue/control/machines.tsv")
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return netRefuse(in.Stderr, "status", NetReasonUnreadable, err.Error(),
			"fix the line the refusal names; the registry is read whole or not at all")
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	nodes, err := NewTailnet(in.Tailscale).Status(ctx)
	if err != nil {
		return netRefuse(in.Stderr, "status", NetReasonTailnet, err.Error(),
			"the tailnet is read through the tailscale CLI on this machine; check `tailscale status` by hand")
	}

	byNode := map[string]TailnetNode{}
	for _, n := range nodes {
		byNode[n.Name] = n
	}

	machines := bounded.Capped(in.Stdout, in.Max, NetToken, "machine",
		"run: nova-pulse fleet net status --machines "+in.Machines+" --max 0")
	findings := bounded.Capped(in.Stderr, in.Max, NetToken, "finding",
		"run: nova-pulse fleet net status --machines "+in.Machines+" --max 0")

	seen := map[string]bool{}
	online, found := 0, 0
	for _, m := range reg.Machines() {
		node, onTailnet := byNode[m.Name]
		seen[m.Name] = true
		machines.Line(netMachineLine(m, node, onTailnet))
		if node.Online {
			online++
		}
		// A machine reached over the LAN is not expected on the tailnet at all, and a
		// shared machine belongs to the node that shared it: neither is a finding when the
		// tailnet has never heard of it.
		if !onTailnet && m.Provider == fleet.ProviderTailnet {
			found++
			findings.Line(fmt.Sprintf("%s FINDING machine=%s reason=%s (%s)",
				NetToken, oneline.Field(m.Name), NetFindingNoNode,
				"the registry says it is on this tailnet and the tailnet has no such node: join it (nova-pulse fleet net join), or write its real provider in the eighth column"))
		}
	}
	names := make([]string, 0, len(byNode))
	for name := range byNode {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if seen[name] {
			continue
		}
		found++
		findings.Line(fmt.Sprintf("%s FINDING node=%s reason=%s (%s)",
			NetToken, oneline.Field(name), NetFindingNotInRegistry,
			"a node of this tailnet that the registry does not carry: add it to "+in.Machines+", or remove it from the tailnet"))
	}
	machines.More()
	findings.More()

	if found > 0 {
		fmt.Fprintf(in.Stderr, "%s STATUS FINDINGS machines=%d online=%d findings=%d\n",
			NetToken, len(reg.Machines()), online, found)
		return 1
	}
	fmt.Fprintf(in.Stdout, "%s STATUS OK machines=%d online=%d findings=%d\n",
		NetToken, len(reg.Machines()), online, found)
	return 0
}

// netMachineLine is one machine, crossed with what the tailnet says about it. Every column
// stands whether the tailnet knows the machine or not, so the line is the same width to
// read down: a dash is an answer.
func netMachineLine(m fleet.Machine, node TailnetNode, onTailnet bool) string {
	addr, lastSeen, onlineWord := "-", "-", "-"
	if onTailnet {
		onlineWord = "no"
		if node.Online {
			onlineWord = "yes"
		}
		if node.Addr != "" {
			addr = node.Addr
		}
		if !node.LastSeen.IsZero() {
			lastSeen = node.LastSeen.UTC().Format(time.RFC3339)
		}
	}
	return fmt.Sprintf("%s MACHINE %s provider=%s roles=%s online=%s addr=%s last-seen=%s",
		NetToken, oneline.Field(m.Name), oneline.Field(m.Provider), oneline.Field(m.RoleList()),
		onlineWord, oneline.Field(addr), oneline.Field(lastSeen))
}

// ---------------------------------------------------------------------------
// The refusal the unwritten verbs print
// ---------------------------------------------------------------------------

// netRefuse is the one refusal shape: the verb, the reason as a token, what happened, and
// one remedy in parentheses. Exit 2, always -- it could not run.
func netRefuse(w io.Writer, verb, reason, what, remedy string) int {
	fmt.Fprintf(w, "%s REFUSED verb=%s reason=%s: %s (%s)\n",
		NetToken, oneline.Field(verb), oneline.Field(reason), oneline.Escape(what), oneline.Escape(remedy))
	return 2
}

// netSpecRules names the rule of docs/SPEC-FLEET-NET.md each unwritten verb is specified
// by, so the refusal sends a reader to the paragraph that says what the verb will do rather
// than to an empty function.
var netSpecRules = map[string]string{
	"acl":    "R4",
	"ssh":    "R5",
	"join":   "R6",
	"expiry": "R7",
	"names":  "R8",
	"share":  "R9",
	"serve":  "R10",
}

// FleetNetNotImplemented is what a specified-but-unwritten verb prints. It names the rule,
// so the refusal is a pointer into the spec and not an apology.
func FleetNetNotImplemented(verb string, stderr io.Writer) int {
	rule, ok := netSpecRules[verb]
	if !ok {
		rule = "the spec"
	}
	return netRefuse(stderr, verb, NetReasonNotImplemented,
		fmt.Sprintf("fleet net %s is specified and not yet written", verb),
		fmt.Sprintf("the rule is %s in docs/SPEC-FLEET-NET.md; until it is written, do it with the tailscale CLI and the policy file at fleet/tailnet-policy.hujson", rule))
}

// NetVerbs is every sub-verb of `fleet net`, in the order the help prints them.
var NetVerbs = []string{"status", "init", "acl", "ssh", "join", "expiry", "names", "share", "serve"}

// ---------------------------------------------------------------------------
// net init
// ---------------------------------------------------------------------------

// FleetNetInitInput is everything `net init` needs. It is all data: no network, no machine
// touched, no model call -- three files out of one registry.
type FleetNetInitInput struct {
	Machines  string // the machines registry
	Node      string // the node's name: ours is `rowan`, yours is yours
	Tailnet   string // the tailnet name as Tailscale spells it (example.com, org.github)
	Owner     string // the login that owns the node's devices; the one src that reaches coordination
	Out       string // the directory the three files are written under
	ActionSHA string // the gitops action's 40-hex commit sha; "" is the pinned default
	ActionVer string // the human tag beside that sha, for the comment
	Force     bool   // overwrite files that are already there
	Stdout    io.Writer
	Stderr    io.Writer
}

// The three files init writes, relative to --out. They are written where a repository keeps
// them, so `--out .` in a node's own repository is the whole job.
const (
	NetPolicyPath   = "fleet/tailnet-policy.hujson"
	NetWorkflowPath = ".github/workflows/tailnet-acl.yml"
	NetDocPath      = "docs/TAILNET.md"
)

// NetDefaultActionSHA is tailscale/gitops-acl-action pinned by commit, which is the only
// pin a workflow may carry (internal/ci's TestEveryActionIsPinnedBySHA is the rule; a tag
// can move under you and an action that moves is an action somebody else controls).
const (
	NetDefaultActionSHA = "5a4a17f5708e9bf96f4ee915a95e9f83c2eebe1a"
	NetDefaultActionVer = "v1.5.2"
	NetCheckoutSHA      = "11d5960a326750d5838078e36cf38b85af677262"
	NetCheckoutVer      = "v4.4.0"
)

// NetAPIKeySecret is the one secret this design puts in the forge, named here so the doc,
// the workflow and the spec cannot drift about it.
const NetAPIKeySecret = "TAILSCALE_API_KEY"

// The ports the services role answers on. They are DATA so a node with a different stack
// changes one table rather than the generator.
var netServicePorts = []struct {
	Port int
	What string
}{
	{3100, "Loki"},
	{3000, "Grafana"},
	{6379, "Redis"},
}

// FleetNetInit writes a node's tailnet policy, its GitOps workflow and its TAILNET.md from
// the registry, and prints one line per file and a verdict:
//
//	NET WROTE fleet/tailnet-policy.hujson rules=6 tests=4
//	NET INIT OK node=rowan files=3 machines=8 tags=5
//
// Exit 0 when the three files were written, 2 when it could not run -- a registry it cannot
// read, a node with no name, a file already there and no --force.
func FleetNetInit(in FleetNetInitInput) int {
	node := strings.TrimSpace(in.Node)
	switch {
	case strings.TrimSpace(in.Machines) == "":
		return netRefuse(in.Stderr, "init", NetReasonNoMachines, "missing --machines; refusing to guess",
			"run: nova-pulse fleet net init --machines queue/control/machines.tsv --node <name> --tailnet <name> --owner <login> --out .")
	case node == "":
		return netRefuse(in.Stderr, "init", NetReasonUnknownNode, "missing --node; a node's policy is written for a named node",
			"name the node the way the bus names it: --node rowan")
	case strings.TrimSpace(in.Tailnet) == "":
		return netRefuse(in.Stderr, "init", NetReasonUnknownNode, "missing --tailnet; the GitOps workflow needs the tailnet as Tailscale spells it",
			"the name at the top of the Tailscale admin console: --tailnet example.com")
	case strings.TrimSpace(in.Owner) == "":
		return netRefuse(in.Stderr, "init", NetReasonUnknownNode, "missing --owner; a policy with no owner reaches the coordination machine from nowhere",
			"the login that owns the node's devices: --owner you@example.com")
	case strings.TrimSpace(in.Out) == "":
		return netRefuse(in.Stderr, "init", NetReasonUnreadable, "missing --out; every path comes from a flag and none has a default",
			"run it in the node's own repository: --out .")
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return netRefuse(in.Stderr, "init", NetReasonUnreadable, err.Error(),
			"fix the line the refusal names; the registry is read whole or not at all")
	}
	if in.ActionSHA == "" {
		in.ActionSHA, in.ActionVer = NetDefaultActionSHA, NetDefaultActionVer
	}

	policy, rules, tests := netPolicy(reg, node, in.Owner)
	files := []struct {
		path, body, note string
	}{
		{NetPolicyPath, policy, fmt.Sprintf("rules=%d tests=%d", rules, tests)},
		{NetWorkflowPath, netWorkflow(in), "action=tailscale/gitops-acl-action@" + in.ActionSHA},
		{NetDocPath, netDoc(reg, in), fmt.Sprintf("node=%s", node)},
	}
	// Nothing is written until every destination is free, so a refusal on the third file
	// does not leave the first two behind.
	if !in.Force {
		for _, f := range files {
			full := filepath.Join(in.Out, filepath.FromSlash(f.path))
			if _, err := os.Stat(full); err == nil {
				return netRefuse(in.Stderr, "init", NetReasonExists,
					fmt.Sprintf("%s is already there and init overwrites nothing", full),
					"read the diff first, then run it again with --force")
			}
		}
	}
	for _, f := range files {
		full := filepath.Join(in.Out, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return netRefuse(in.Stderr, "init", NetReasonUnreadable, err.Error(), "the directory could not be made; check --out")
		}
		if err := os.WriteFile(full, []byte(f.body), 0o644); err != nil {
			return netRefuse(in.Stderr, "init", NetReasonUnreadable, err.Error(), "the file could not be written; check --out")
		}
		fmt.Fprintf(in.Stdout, "%s WROTE %s %s\n", NetToken, oneline.Field(f.path), f.note)
	}
	fmt.Fprintf(in.Stdout, "%s INIT OK node=%s files=%d machines=%d rules=%d tests=%d\n",
		NetToken, oneline.Field(node), len(files), len(reg.Machines()), rules, tests)
	return 0
}

// netTags is the tags a policy needs: one per role any machine in the registry carries, in
// a fixed order so the generated file is byte-identical from one run to the next.
func netTags(reg *fleet.Registry) []string {
	seen := map[string]bool{}
	var out []string
	for _, role := range fleet.RoleNames() {
		for _, m := range reg.WithRole(role) {
			for _, tag := range NetMachineTags(m) {
				if !seen[tag] {
					seen[tag] = true
					out = append(out, tag)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// NetMachineTags is the set of tags ONE machine advertises, and it is the answer to
// Johnny's security read of 2026-09-18:
//
//	"A Tailscale ACL grants when any rule accepts, so tag:bench+tag:runner is reachable as a
//	bench and R3 is false for that host. The dated allow-shared= note is a hole with a
//	calendar, not a shape. One reach-role per machine: a host that runs CI on a bench is
//	tag:bench only. CI is a process, not a tag."
//
// So `tag:runner` is advertised ONLY by a machine whose roles are runner and nothing else.
// A shared bench-and-runner host is `tag:bench`, full stop -- which makes R3 TRUE rather
// than true-with-a-note: every machine carrying tag:runner is a machine nothing tagged
// reaches, and there is no overlapping accept to defeat the denial.
//
// Every other role is a genuine reach-role and a machine carries all of the ones it has: a
// bench that also hosts the stack is reachable as a bench by the owners and on the stack's
// named ports by other benches, and both of those are meant.
func NetMachineTags(m fleet.Machine) []string {
	var out []string
	for _, role := range fleet.RoleNames() {
		if !m.HasRole(role) {
			continue
		}
		if role == fleet.RoleRunner && len(m.Roles) > 1 {
			// CI is a process on this machine, not a way of reaching it.
			continue
		}
		out = append(out, "tag:"+role)
	}
	return out
}

// netPolicy writes Tailscale's OWN policy file -- HuJSON, with its `acls`, `ssh` and
// `tests` sections -- from the registry's roles. We do not invent an ACL language: the
// invariants are written in Tailscale's, so `tailscale acl test` is the thing that checks
// them and the GitOps action is the thing that applies them.
//
// It returns the body, the number of acl rules and the number of tests, so the verdict line
// can carry both without parsing what it just wrote.
func netPolicy(reg *fleet.Registry, node, owner string) (body string, rules, tests int) {
	has := func(role string) bool { return len(reg.WithRole(role)) > 0 }
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("// fleet/tailnet-policy.hujson -- the tailnet policy of the node %q, as code.", node)
	p("//")
	p("// GENERATED by `nova-pulse fleet net init`; edit it here, never in the admin console.")
	p("// The console is not the source of truth: this file is, and .github/workflows/tailnet-acl.yml")
	p("// runs `tailscale acl test` on every pull request and applies it on the default branch.")
	p("//")
	p("// One tailnet per NODE (Glenn, 2026-09-18). Cross-node reach is a Tailscale node share,")
	p("// one machine offered into another node's tailnet and written in the machines registry as")
	p("// `shared-from:<node>`. There is no merged tailnet, and there is no rule here that reaches")
	p("// another node: the bus (git) is the federation.")
	p("//")
	p("// Everything below comes from the machines registry's ROLES. A machine's role decides what")
	p("// it may reach and what may reach it; nothing is written per machine, so a new bench needs")
	p("// no edit here at all -- it needs its tag.")
	p("{")
	p("  // The groups. `group:owners` is the node's own people: the one source that reaches the")
	p("  // coordination machine, which is where a friend's window and its whole context live.")
	p("  \"groups\": {")
	p("    \"group:owners\": [%q],", owner)
	p("  },")
	p("")
	p("  // Who may apply a tag. A machine is tagged when it joins (`tailscale up --advertise-tags`),")
	p("  // and a tagged machine's key never expires, which is why R7 can turn expiry off by tag")
	p("  // rather than per machine in the console.")
	p("  \"tagOwners\": {")
	for _, tag := range netTags(reg) {
		p("    %q: [\"group:owners\"],", tag)
	}
	p("  },")
	p("")
	p("  // The ACL. Tailscale denies by default: a destination no rule names is reachable by")
	p("  // nothing, and that default is doing real work below -- it is the whole of R3.")
	p("  \"acls\": [")

	rule := func(comment string, src []string, dst []string) {
		p("    // %s", comment)
		p("    {\"action\": \"accept\", \"src\": [%s], \"dst\": [%s]},", netList(src), netList(dst))
		rules++
	}
	if has(fleet.RoleBud) && has(fleet.RoleBench) {
		rule("R5: a bud -- a person's own laptop -- reaches the benches over ssh and NOTHING else. It is not a bench, it runs no card, and it has no reason to reach the coordination machine, a runner host or the stack.",
			[]string{"tag:bud"}, []string{"tag:bench:22"})
	}
	if has(fleet.RoleBench) {
		rule("Benches reach each other over ssh: that is the swarm, the mirror fetch and the work steal.",
			[]string{"tag:bench"}, []string{"tag:bench:22"})
	}
	if has(fleet.RoleBench) && has(fleet.RoleServices) {
		var dst []string
		var what []string
		for _, s := range netServicePorts {
			dst = append(dst, fmt.Sprintf("tag:services:%d", s.Port))
			what = append(what, fmt.Sprintf("%d %s", s.Port, s.What))
		}
		rule("R11: the stack is reachable FROM THE BENCHES ON NAMED PORTS ONLY ("+strings.Join(what, ", ")+"). Not on ssh: a bench pushes logs and metrics, it does not administer the services host.",
			[]string{"tag:bench"}, dst)
	}
	if has(fleet.RoleCoordination) {
		rule("R12: the coordination machine accepts the node's OWN people and nobody else. No bench, no bud, no runner host, and no shared machine from another node: a friend's window is the one place that holds its whole context.",
			[]string{"group:owners"}, []string{"tag:coordination:*"})
	}
	rule("R16: the node's own people reach EVERY machine they own, a runner host included. Glenn, 2026-09-18, travelling: `I want to work with all friends, including keeper you and all fleet machines from the air with no restrictions.` R3 is about what the tailnet's TAGGED things may reach -- seats, buds and benches -- never about the people whose tailnet it is. NOTE that this rule reaches an owner's device only while it is UNTAGGED: a laptop that joined with --advertise-tags=tag:bud is a bud and gets the bud's one destination.",
		[]string{"group:owners"}, netAllTagDst(reg))
	// R3 is the ABSENCE of a rule, and an absence has to be said out loud or the next
	// person adds the rule that removes it.
	p("    // R3: no rule above lets anything TAGGED -- a bud, a bench, a seat's machine -- reach")
	p("    // tag:runner. A runner host talks OUT to the forge and that is the whole of its network")
	p("    // life; the lock of 2026-09-18 says runner hosts are CI-only, and this is that lock")
	p("    // written where the packets are. The one rule that does reach it is the owners' rule")
	p("    // just above, which is a person and not a workload. A rule added here would undo the")
	p("    // lock silently, which is why the tests below assert the denials rather than trusting")
	p("    // an absence.")
	p("  ],")
	p("")
	p("  // R6: Tailscale SSH. The tailnet identity IS the authorization, so no authorized_keys is")
	p("  // distributed to any machine and no key has to be rotated when a person leaves: the")
	p("  // policy is edited here and the next connection is refused.")
	p("  \"ssh\": [")
	p("    // R16: the node's own people, to EVERY machine they own, as any user, with no check.")
	p("    // Glenn travelling is the case this is for: an owner at an airport gate must be able to")
	p("    // reach every friend and every bench, and a re-authentication prompt on a bad connection")
	p("    // is the thing that stops the work.")
	p("    {\"action\": \"accept\", \"src\": [\"group:owners\"], \"dst\": [\"autogroup:self\"], \"users\": [\"autogroup:nonroot\", \"root\"]},")
	for _, tag := range netTags(reg) {
		p("    {\"action\": \"accept\", \"src\": [\"group:owners\"], \"dst\": [%q], \"users\": [\"autogroup:nonroot\", \"root\"]},", tag)
	}
	if has(fleet.RoleBud) && has(fleet.RoleBench) {
		p("    // A bud gets a CHECK: a browser re-authentication every twelve hours. A laptop leaves")
		p("    // the house, and the machine it can reach from a cafe is the one that runs the work.")
		p("    {\"action\": \"check\", \"src\": [\"tag:bud\"], \"dst\": [\"tag:bench\"], \"users\": [\"autogroup:nonroot\"], \"checkPeriod\": \"12h\"},")
	}
	p("  ],")
	p("")
	p("  // Tailscale's OWN tests. `tailscale acl test` runs these, in CI, before a change to this")
	p("  // file can be applied -- so the invariants are checked by the thing that enforces them")
	p("  // and not by a description of it. Every one of them is a rule of docs/SPEC-FLEET-NET.md.")
	p("  \"tests\": [")

	test := func(comment, src string, accept, deny []string) {
		p("    // %s", comment)
		p("    {\"src\": %q,", src)
		if len(accept) > 0 && len(deny) > 0 {
			p("     \"accept\": [%s],", netList(accept))
			p("     \"deny\": [%s]},", netList(deny))
		} else if len(accept) > 0 {
			p("     \"accept\": [%s]},", netList(accept))
		} else {
			p("     \"deny\": [%s]},", netList(deny))
		}
		tests++
	}
	if has(fleet.RoleBud) && has(fleet.RoleBench) {
		test("R5: a bud reaches benches and nothing else.", "tag:bud",
			[]string{"tag:bench:22"}, netDeny(reg, "tag:bench"))
	}
	if has(fleet.RoleBench) {
		var accept []string
		if has(fleet.RoleServices) {
			accept = append(accept, fmt.Sprintf("tag:services:%d", netServicePorts[0].Port))
		}
		accept = append(accept, "tag:bench:22")
		test("R11 and R3: a bench reaches the stack on its named ports and another bench over ssh -- never the services host's ssh, never the coordination machine, never a runner.",
			"tag:bench", accept, netBenchDeny(reg))
	}
	{
		// R16: the owners reach everything. This test must PASS for a runner host, which is
		// the half of R3 that is easy to break by tightening the rule above.
		var accept []string
		for _, tag := range netTags(reg) {
			accept = append(accept, tag+":22")
		}
		test("R16: the node's own people reach EVERY machine they own on ssh, a runner host and the coordination machine included -- Glenn travelling: `all fleet machines from the air with no restrictions`.",
			"group:owners", accept, nil)
	}
	if has(fleet.RoleRunner) {
		test("R3: a runner host is reachable from nothing TAGGED -- no bench, no bud, no seat's machine. It is the lock of 2026-09-18, asserted rather than assumed, and since Johnny's read of the same day it is true without a note: a shared bench-and-runner host advertises tag:bench only, so there is no overlapping accept to defeat this denial.",
			"tag:bench", nil, []string{"tag:runner:22", "tag:runner:80", "tag:runner:443"})
	}
	p("  ],")
	p("}")
	return b.String(), rules, tests
}

// netAllTagDst is every tag as an all-ports destination: what the node's own people reach.
// Every one, runner hosts included -- a person travelling must be able to work with every
// machine they own, and the lock of 2026-09-18 is a rule about tagged things, not about
// the people whose tailnet it is.
func netAllTagDst(reg *fleet.Registry) []string {
	var out []string
	for _, tag := range netTags(reg) {
		// A runner host is the one destination the owners get on SSH ALONE rather than on
		// every port. Glenn travelling needs to reach every machine he owns; he does not
		// need a runner host's arbitrary ports, and Johnny's read of 2026-09-18 asked for
		// exactly this one accept: `src: group:owners, dst: tag:runner:22`. The two
		// rulings meet here, and SPEC-FLEET-NET.md Q1 records the one-line difference.
		if tag == "tag:"+fleet.RoleRunner {
			out = append(out, tag+":22")
			continue
		}
		out = append(out, tag+":*")
	}
	return out
}

// netDeny is every tag EXCEPT the one named, as an ssh destination: what a source must not
// reach. Written from the registry rather than by hand, so a node with a role we do not
// have gets its denial for free.
func netDeny(reg *fleet.Registry, except string) []string {
	var out []string
	for _, tag := range netTags(reg) {
		if tag == except {
			continue
		}
		out = append(out, tag+":22")
	}
	return out
}

// netBenchDeny is what a bench must not reach: every other tag's ssh, and the services
// host's ssh in particular -- a bench pushes to the stack, it does not administer it.
func netBenchDeny(reg *fleet.Registry) []string {
	return netDeny(reg, "tag:bench")
}

// netList renders a JSON string array's contents.
func netList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, fmt.Sprintf("%q", s))
	}
	return strings.Join(quoted, ", ")
}

// netWorkflow is the GitOps workflow: Tailscale's own action, pinned by commit sha, testing
// on every pull request and applying on the default branch. We wrote no applier and no etag
// dance -- the action has both, and the rule is that we use what is there.
func netWorkflow(in FleetNetInitInput) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	p("# tailnet-acl.yml: the tailnet policy of this node, applied by Tailscale's own GitOps action.")
	p("#")
	p("# GENERATED by `nova-pulse fleet net init`. The policy is %s, and the", NetPolicyPath)
	p("# admin console is not the source of truth: a change made there is overwritten by the next")
	p("# apply, on purpose.")
	p("#")
	p("# On a pull request the action runs `tailscale acl test`, which runs the policy's own")
	p("# `tests` section -- the invariants -- against the proposed policy. On a push to the")
	p("# default branch it applies. The action does the etag check itself: an apply over a")
	p("# policy that was edited outside this repository fails rather than clobbering it.")
	p("#")
	p("# The one secret: %s, a Tailscale API key for THIS node's tailnet, in the", NetAPIKeySecret)
	p("# repository's secrets. Per-node tailnets mean per-node keys; no other node's key is here,")
	p("# and no key is ever in a flag, a file in the tree, or a line this workflow prints.")
	p("#")
	p("# Actions are pinned by sha (internal/ci's TestEveryActionIsPinnedBySHA is the rule).")
	p("")
	p("name: tailnet-acl")
	p("")
	p("on:")
	p("  push:")
	p("    branches: [%s]", in.branchOrDev())
	p("    paths:")
	p("      - '%s'", NetPolicyPath)
	p("      - '%s'", NetWorkflowPath)
	p("  pull_request:")
	p("    paths:")
	p("      - '%s'", NetPolicyPath)
	p("      - '%s'", NetWorkflowPath)
	p("")
	p("permissions:")
	p("  contents: read")
	p("")
	p("concurrency:")
	p("  group: tailnet-acl-${{ github.ref }}")
	p("  cancel-in-progress: false")
	p("")
	p("jobs:")
	for _, job := range []struct{ name, event, action, done, why string }{
		{"acl-test", "pull_request", "test", "tested",
			"The invariants, on every pull request that touches the policy. This is the gate: a policy whose tests fail never reaches the tailnet."},
		{"acl-apply", "push", "apply", "applied",
			"The apply, on the default branch only. Nothing applies from a pull request: a fork's pull request must never reach a tailnet, and a policy lands the way every other change lands -- reviewed, merged, then applied."},
	} {
		p("  %s:", job.name)
		for _, line := range netWrap(job.why, 84) {
			p("    # %s", line)
		}
		p("    if: github.event_name == '%s'", job.event)
		p("    runs-on: ubuntu-latest")
		p("    timeout-minutes: 5")
		p("    env:")
		p("      # The secret reaches the job as an environment value so the armed check below can")
		p("      # SEE whether it is there. The `secrets` context is not readable in a job-level")
		p("      # `if:`, and a workflow that fails red because a node has not armed it yet would")
		p("      # stop that node's queue for a reason that has nothing to do with its code.")
		p("      TAILNET_API_KEY: ${{ secrets.%s }}", NetAPIKeySecret)
		p("    steps:")
		p("      - uses: actions/checkout@%s # %s", NetCheckoutSHA, NetCheckoutVer)
		p("      - name: is this node armed")
		p("        id: armed")
		p("        shell: bash")
		p("        run: |")
		p("          if [ -n \"$TAILNET_API_KEY\" ]; then")
		p("            echo 'armed=yes' >> \"$GITHUB_OUTPUT\"")
		p("          else")
		p("            echo 'armed=no' >> \"$GITHUB_OUTPUT\"")
		p("            echo \"::notice title=tailnet-acl::%s is not set, so the policy is not %s. Add the secret to arm this workflow (docs/TAILNET.md).\"", NetAPIKeySecret, job.done)
		p("          fi")
		p("      - name: tailscale acl %s", job.action)
		p("        if: steps.armed.outputs.armed == 'yes'")
		p("        uses: tailscale/gitops-acl-action@%s # %s", in.ActionSHA, in.ActionVer)
		p("        with:")
		p("          tailnet: %s", in.Tailnet)
		p("          api-key: ${{ secrets.%s }}", NetAPIKeySecret)
		p("          policy-file: %s", NetPolicyPath)
		p("          action: %s", job.action)
		p("")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// netWrap breaks a sentence into lines of at most n characters on word boundaries, so a
// generated comment reads as a paragraph and not as one very long line.
func netWrap(s string, n int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= n:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// branchOrDev is the branch an apply runs on. `dev` is where every merge lands here
// (Glenn, 2026-09-16: main is promoted, never pushed to), and a node whose default branch
// is `main` changes this one line.
func (in FleetNetInitInput) branchOrDev() string { return "dev" }

// netDoc is TAILNET.md for this node: the design in words, with the node's own names as the
// example. It is written for ANY node running nova-tools -- ours is the first, not the only
// one -- so the prose is about roles and the names are filled in from the registry.
func netDoc(reg *fleet.Registry, in FleetNetInitInput) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	roleLine := func(role string) string {
		var names []string
		for _, m := range reg.WithRole(role) {
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			return "(none on this node)"
		}
		return strings.Join(names, ", ")
	}

	p("# The tailnet of the node `%s`", in.Node)
	p("")
	p("GENERATED by `nova-pulse fleet net init`. Re-run it when the machines registry changes;")
	p("edit the registry, not this file.")
	p("")
	p("## One tailnet per node")
	p("")
	p("Glenn, 2026-09-18: *every NODE -- a team of humans and AI seats -- has its OWN Tailscale")
	p("network.* A node is not a subnet of somebody else's tailnet and never joins one. Cross-node")
	p("reach is **node sharing**: one machine is offered into another node's tailnet, accepted")
	p("there, and written down in that node's machines registry as `provider=shared-from:<node>`.")
	p("There is no merged tailnet, no shared admin console and no shared API key.")
	p("")
	p("The reason is that **the bus is the federation**. Two nodes work together through git --")
	p("the bus, the queues, the pull requests -- and git needs no tailnet at all. A node with no")
	p("tailnet is still a node: it writes `provider=lan` in its registry and everything else in")
	p("nova-tools works unchanged. Nothing in the tools may hardcode a tailnet address, which is")
	p("why the registry carries a provider column and the verbs ask it.")
	p("")
	p("## This node")
	p("")
	p("| role | what it is | machines here |")
	p("| --- | --- | --- |")
	p("| `bench` | cards, probes and load run here | %s |", roleLine(fleet.RoleBench))
	p("| `runner` | it serves the forge's CI shards | %s |", roleLine(fleet.RoleRunner))
	p("| `coordination` | a friend's own window lives here | %s |", roleLine(fleet.RoleCoordination))
	p("| `services` | the stack: Loki, Grafana, Redis | %s |", roleLine(fleet.RoleServices))
	p("| `bud` | a person's own laptop | %s |", roleLine(fleet.RoleBud))
	p("")
	p("Tailnet: `%s`. Policy: [`%s`](../%s). Workflow: [`%s`](../%s).",
		in.Tailnet, NetPolicyPath, NetPolicyPath, NetWorkflowPath, NetWorkflowPath)
	p("")
	p("A machine advertises ONE TAG PER ROLE when it joins, and the policy is written about the")
	p("tags rather than about the names -- so a new bench needs a tag and no edit to the policy:")
	p("")
	p("| machine | provider | `tailscale up --advertise-tags` |")
	p("| --- | --- | --- |")
	for _, m := range reg.Machines() {
		p("| `%s` | `%s` | `%s` |", m.Name, m.Provider, strings.Join(NetMachineTags(m), ","))
	}
	p("")
	p("**An owner's own laptop joins UNTAGGED.** This is the one trap in the table above: a tag")
	p("replaces a device's user identity, so a laptop that joined with `--advertise-tags=tag:bud`")
	p("is a bud to the ACL and reaches benches and nothing else, whoever is typing on it. An")
	p("owner's own machine runs plain `tailscale up`, is covered by `group:owners`, and reaches")
	p("everything. Tag a bud only when it is somebody's machine and not one of the node's own")
	p("people's -- a seat's bench-runner, a contractor's laptop.")
	p("")
	p("**`tag:runner` is advertised only by a machine that is a runner and nothing else.** A host")
	p("that runs CI shards beside its cards is `tag:bench`, full stop: CI is a process on that")
	p("machine, not a way of reaching it. Johnny's security read of 2026-09-18 is the reason:")
	p("")
	p("> A Tailscale ACL grants when any rule accepts, so `tag:bench`+`tag:runner` is reachable")
	p("> as a bench and R3 is false for that host. The dated `allow-shared=` note is a hole with")
	p("> a calendar, not a shape. One reach-role per machine.")
	p("")
	p("So the denial is TRUE rather than true-with-a-note: every machine carrying `tag:runner` is")
	p("one nothing tagged reaches, and there is no overlapping accept to defeat it. The registry's")
	p("`allow-shared=` note keeps its own job -- it is what stops a CARD being placed on a runner")
	p("host -- and the two rules no longer have to agree for either to hold.")
	p("")
	p("## The ACL is code, and Tailscale applies it")
	p("")
	p("The policy file is Tailscale's own HuJSON, in this repository, with its own `tests` section.")
	p("`.github/workflows/tailnet-acl.yml` runs Tailscale's official GitOps action: `tailscale acl")
	p("test` on every pull request that touches the policy, `apply` on a push to the default")
	p("branch. We wrote no ACL language, no applier and no etag check -- the action has all three,")
	p("and an apply over a policy somebody edited in the console fails instead of clobbering it.")
	p("")
	p("The invariants live in the `tests` section, so the thing that enforces the policy is the")
	p("thing that checks it:")
	p("")
	p("* a **bud** may ssh to `bench` machines and to nothing else;")
	p("* nothing reaches the **coordination** machine but the node's own people;")
	p("* a **runner** host accepts nothing TAGGED -- no bud, no bench, no seat's machine. It talks")
	p("  out to the forge and that is its whole network life (the lock of 2026-09-18: runner hosts")
	p("  are CI-only). The node's own people are not a tagged thing and do reach it;")
	p("* the node's own **people** reach every machine they own, on every port, over ssh, with no")
	p("  check. Glenn, 2026-09-18, travelling: *\"I want to work with all friends, including keeper")
	p("  you and all fleet machines from the air with no restrictions.\"*")
	p("* **services** are reachable from benches on the named ports only, never on ssh.")
	p("")
	p("## One secret, in the forge")
	p("")
	p("`%s` in this repository's secrets: a Tailscale API key for this node's", NetAPIKeySecret)
	p("tailnet and no other. Per-node tailnets mean per-node keys -- a key that could reach two")
	p("nodes would be the merged tailnet by another road. The key reaches nothing outside the")
	p("workflow: not a flag, not a file in the tree, not a line any verb prints. On a machine, an")
	p("auth key reaches `tailscale up` through `nova-secrets exec` and the remote shell's stdin,")
	p("never an argv.")
	p("")
	p("## Tailscale SSH, Serve, MagicDNS")
	p("")
	p("**SSH**: the tailnet identity is the authorization. The `ssh` section of the policy decides")
	p("who may connect as whom; no `authorized_keys` is distributed to any machine, and a person")
	p("who leaves is removed from the policy rather than from every host's key file.")
	p("")
	p("**Serve**: something that has to be reachable in a browser is `tailscale serve`, which is")
	p("private to the tailnet and gets its certificate from Tailscale. **Funnel** -- the same thing")
	p("on the public internet -- is never on by default, and is a decision somebody makes out loud.")
	p("")
	p("**MagicDNS**: on. Every machine is reachable by its name, so there is no `/etc/hosts` block")
	p("to write on any machine and no name list to keep in step with the registry.")
	p("")
	p("## What certify probes")
	p("")
	p("`tailscale status --json`, parsed, crossed with the machines registry: a machine the")
	p("registry calls `tailnet` with no node is a finding, and a node the registry does not carry")
	p("is a finding. That is `nova-pulse fleet net status`, which runs read-only and opens no")
	p("socket in any test. The ssh workload proves a connection through the tailnet identity with")
	p("no `authorized_keys` on the far side.")
	p("")
	p("## A new node")
	p("")
	p("```")
	p("nova-pulse fleet net init --machines <registry> --node <name> \\")
	p("    --tailnet <tailnet> --owner <login> --out .")
	p("```")
	p("")
	p("Three files, from the registry the node already keeps: the policy, the workflow and this")
	p("page. Then add `%s` to the repository's secrets and merge.", NetAPIKeySecret)
	return b.String()
}
