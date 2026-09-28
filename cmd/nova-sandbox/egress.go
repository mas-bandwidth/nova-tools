// The egress verbs are the card's OUTBOUND wall: `plan` renders one run's nftables ruleset
// from the reviewed allowlist in git, `apply` hands it to nft, `check` reads a plan back and
// asserts its invariants, and `drop` takes the wall away when the run is over. The rules
// themselves live in internal/sandbox (egress.go); this file is the verb, the flags and the
// three seams that touch the machine.
//
// The wall is on the BENCH, not in the card: Johnny, 2026-09-18, "Not an env list the worker
// applies (the worker is the adversary)". A card cannot see this ruleset, cannot name a host
// for it and cannot take it down; it can only find out that a destination is denied.
//
// apply and drop are LINUX's, because nftables is. On darwin the outbound wall is the
// seatbelt profile this binary already generates, and both verbs refuse there rather than
// pretending a plan was enforced.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// egressRemedy is the one remedy line every refusal of these verbs carries.
const egressRemedy = "run: nova-sandbox egress plan --run <id> --policy infra/image/egress.txt --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... --uid <n> --out <file>"

// nftRemedy is the one line a bench without nftables gets. It is a line to run, not an
// investigation.
const nftRemedy = "install it and let this user run it without a password: sudo apt-get install -y nftables, then one sudoers line: nova ALL=(root) NOPASSWD: /usr/sbin/nft"

// egressDeniedLine is the whole of what a card is told when it reaches for a destination the
// wall denies, and it is Johnny's line word for word: `EGRESS DENIED host=<name>` on the
// card's stdout, and the run exits non-zero. Fail closed, no retry to a different host.
const egressDeniedLine = "EGRESS DENIED host="

// sandboxResolver is the resolver seam, named here because the tests replace it.
type sandboxResolver = sandbox.EgressResolver

// privilegedCommand is the whole of these verbs' contact with the machine: one lookup that
// says whether the binary is there, and one run that executes it with privilege. The
// production body is `sudo -n nft ...`; the tests put a recorder here, so the contract —
// nothing is handed to nft before the plan is audited — is proved without a ruleset.
type privilegedCommand interface {
	Look(name string) (string, error)
	Run(name string, args ...string) (string, error)
}

// The three seams. Each is a package-level var, and none of them is reachable from caller
// input.
var (
	egressPriv   privilegedCommand                   = sudoCommand{}
	egressGOOS                                       = runtime.GOOS
	egressLookup func(at netip.Addr) sandboxResolver = benchResolver
)

// sudoCommand is the production privileged body. `sudo -n`: never a password prompt in a
// card runner's non-interactive shell — a bench that is not set up says so immediately
// instead of hanging on a tty nobody is watching.
type sudoCommand struct{}

func (sudoCommand) Look(name string) (string, error) { return exec.LookPath(name) }

func (sudoCommand) Run(name string, args ...string) (string, error) {
	out, err := exec.Command("sudo", append([]string{"-n", name}, args...)...).CombinedOutput()
	return string(out), err
}

// benchResolver asks the SAME resolver the card is then allowed to reach, so the addresses
// pinned in the plan are the addresses the card's own lookups will return.
func benchResolver(at netip.Addr) sandboxResolver { return dnsResolver{at: at} }

type dnsResolver struct{ at netip.Addr }

func (d dnsResolver) LookupHost(name string) ([]netip.Addr, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, net.JoinHostPort(d.at.String(), "53"))
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.LookupNetIP(ctx, "ip", name)
}

// egressFlags is the egress verbs' own argv, parsed by hand like every other verb's.
type egressFlags struct {
	run, policy, modelHost, resolver, out, plan, uid, veth string
	benchCIDRs                                             []string
	bad                                                    []sandbox.Refusal
}

func parseEgress(args []string) egressFlags {
	var f egressFlags
	add := func(reason, text string) {
		f.bad = append(f.bad, sandbox.Refusal{Reason: reason, Text: text})
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		want := func(flag string) string {
			if i+1 >= len(args) {
				add("no_command", flag+" wants a value: "+flag+" <value>")
				return ""
			}
			i++
			return args[i]
		}
		switch a {
		case "--run":
			f.run = want("--run")
		case "--policy":
			f.policy = want("--policy")
		case "--model-host":
			f.modelHost = want("--model-host")
		case "--resolver":
			f.resolver = want("--resolver")
		case "--bench-cidr":
			if v := want("--bench-cidr"); v != "" {
				f.benchCIDRs = append(f.benchCIDRs, v)
			}
		case "--uid":
			f.uid = want("--uid")
		case "--veth":
			f.veth = want("--veth")
		case "--out":
			f.out = want("--out")
		case "--plan":
			f.plan = want("--plan")
		default:
			add("no_command", oneline.Escape(a)+" is not a flag of the egress verbs; run: nova-sandbox help")
		}
	}
	return f
}

// egressVerb dispatches the four sub-verbs. They are not wrappers, so they use SPEC.md's
// grammar unchanged: 0 the verb ran and passed, 1 the verb ran and said NO, 2 it could not
// run.
func egressVerb(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		return egressRefuse(stderr, []sandbox.Refusal{{Reason: "no_command",
			Text: "egress wants one of plan, apply, check or drop"}})
	}
	f := parseEgress(args[1:])
	switch args[0] {
	case "plan":
		return egressPlanVerb(f, stderr)
	case "apply":
		return egressApplyVerb(f, stderr)
	case "check":
		return egressCheckVerb(f, stderr)
	case "drop":
		return egressDropVerb(f, stderr)
	}
	return egressRefuse(stderr, []sandbox.Refusal{{Reason: "no_command",
		Text: oneline.Escape(args[0]) + " is not an egress verb; it is one of plan, apply, check or drop"}})
}

// egressRefuse prints one line per independent problem (rule 16) and then the one remedy
// line, and costs 2: the verb could not run.
func egressRefuse(stderr io.Writer, bad []sandbox.Refusal) int {
	for _, r := range bad {
		fmt.Fprintf(stderr, "EGRESS REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(r.Text))
	}
	fmt.Fprintln(stderr, egressRemedy)
	return sandbox.ExitCannotRun
}

// egressSaidNo is the other half of the grammar: the verb ran, looked, and the answer is NO.
// It costs 1, and a caller tells the two apart by the number as well as by the line.
func egressSaidNo(stderr io.Writer, bad []sandbox.Refusal) int {
	for _, r := range bad {
		fmt.Fprintf(stderr, "EGRESS REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(r.Text))
	}
	return sandbox.ExitProbeFailed
}

// egressPlanVerb resolves the allowed names ONCE, pins what they answered, renders the
// ruleset and writes it to --out. It runs on every platform: a plan is text, and a reviewer
// on a Mac has to be able to build and read the ruleset a bench will apply.
func egressPlanVerb(f egressFlags, stderr io.Writer) int {
	bad := f.bad
	if f.out == "" {
		bad = append(bad, sandbox.Refusal{Reason: "no_command", Text: "--out wants the file the ruleset is written to: --out /run/nova/egress-<run>.nft"})
	}
	if f.plan != "" {
		bad = append(bad, sandbox.Refusal{Reason: "no_command", Text: "--plan belongs to the apply and check verbs; plan WRITES a ruleset and names it with --out"})
	}
	var names []string
	if f.policy == "" {
		bad = append(bad, sandbox.Refusal{Reason: "bad_policy", Text: "--policy wants the reviewed allowlist in git: --policy infra/image/egress.txt"})
	} else {
		raw, err := os.ReadFile(f.policy)
		if err != nil {
			bad = append(bad, sandbox.Refusal{Reason: "bad_policy",
				Text: fmt.Sprintf("the policy file could not be read: %s; it is a file in git and this verb never invents one", oneline.Err(err))})
		} else {
			got, parseBad := sandbox.ParseEgressPolicy(raw)
			bad, names = append(bad, parseBad...), got
		}
	}
	var resolver netip.Addr
	if f.resolver == "" {
		bad = append(bad, sandbox.Refusal{Reason: "bad_resolver", Text: "--resolver wants the bench resolver's address, the one destination UDP 53 is allowed to: --resolver 10.9.0.53"})
	} else if a, err := netip.ParseAddr(f.resolver); err != nil {
		bad = append(bad, sandbox.Refusal{Reason: "bad_resolver", Text: oneline.Escape(f.resolver) + " is not an IP address: --resolver 10.9.0.53"})
	} else {
		resolver = a
	}
	var cidrs []netip.Prefix
	for _, c := range f.benchCIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			bad = append(bad, sandbox.Refusal{Reason: "bad_cidr", Text: oneline.Escape(c) + " is not a CIDR: --bench-cidr 10.1.0.0/24"})
			continue
		}
		cidrs = append(cidrs, p.Masked())
	}
	if len(bad) > 0 {
		return egressRefuse(stderr, bad)
	}

	in := sandbox.EgressInput{
		Run: f.run, PolicyPath: f.policy, Names: names, ModelHost: f.modelHost,
		Resolver: resolver, BenchCIDRs: cidrs, UID: f.uid, Veth: f.veth,
		Lookup: egressLookup(resolver),
	}
	// The resolution is the one step of this verb that takes real time, so it says so:
	// a reader staring at a silent terminal cannot tell a slow resolver from a hung one.
	fmt.Fprintf(stderr, "EGRESS STEP name=resolve state=start\n")
	at := time.Now()
	plan, planBad := sandbox.BuildEgress(in)
	fmt.Fprintf(stderr, "EGRESS STEP name=resolve state=done ms=%d\n", time.Since(at).Milliseconds())
	if len(planBad) > 0 {
		return egressRefuse(stderr, planBad)
	}
	// The plan this tool writes passes this tool's audit, always: a renderer and its checker
	// that disagree would be two contracts, and the one a bench applies would be the
	// unchecked one.
	if _, auditBad := sandbox.CheckEgressPlan(plan.Text); len(auditBad) > 0 {
		return egressSaidNo(stderr, auditBad)
	}
	if err := os.WriteFile(f.out, []byte(plan.Text), 0o600); err != nil {
		return egressRefuse(stderr, []sandbox.Refusal{{Reason: "bad_out",
			Text: fmt.Sprintf("the ruleset could not be written: %s; every path is yours and none is guessed, so the directory is not created", oneline.Err(err))}})
	}
	fmt.Fprintf(stderr, "EGRESS PLAN run=%s allow=%d deny=%d names=%s\n",
		oneline.Field(plan.Run), plan.Allow, plan.Deny, oneline.Field(strings.Join(plan.Names, ",")))
	return 0
}

// egressCheckVerb reads a plan back and asserts the invariants. It is the verb a reviewer
// runs on a file, and it is what apply runs before nft ever sees one.
func egressCheckVerb(f egressFlags, stderr io.Writer) int {
	bad := f.bad
	if f.plan == "" {
		bad = append(bad, sandbox.Refusal{Reason: "no_command", Text: "--plan wants the ruleset to read back: --plan /run/nova/egress-<run>.nft"})
	}
	if len(bad) > 0 {
		return egressRefuse(stderr, bad)
	}
	audit, readBad, auditBad := egressReadPlan(f.plan)
	if len(readBad) > 0 {
		return egressRefuse(stderr, readBad)
	}
	if len(auditBad) > 0 {
		return egressSaidNo(stderr, auditBad)
	}
	fmt.Fprintf(stderr, "EGRESS CHECK table=%s chains=%d rules=%d allow=%d deny=%d\n",
		oneline.Field(audit.Table), len(audit.Chains), audit.Rules, audit.Allow, audit.Deny)
	return 0
}

// egressReadPlan reads a plan file and audits it. Both kinds of problem come back
// separately, so that a caller can tell "there is no such file" (could not run) from "the
// file is not a wall" (the verb ran and said NO).
func egressReadPlan(path string) (sandbox.EgressAudit, []sandbox.Refusal, []sandbox.Refusal) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sandbox.EgressAudit{}, []sandbox.Refusal{{Reason: "bad_plan",
			Text: fmt.Sprintf("the plan could not be read: %s", oneline.Err(err))}}, nil
	}
	audit, bad := sandbox.CheckEgressPlan(string(raw))
	return audit, nil, bad
}

// egressApplyVerb hands ONE audited plan to nft. The order is the contract: read, audit,
// check the table is this run's, look for nft, and only then run it.
func egressApplyVerb(f egressFlags, stderr io.Writer) int {
	bad := f.bad
	if f.plan == "" {
		bad = append(bad, sandbox.Refusal{Reason: "no_command", Text: "--plan wants the ruleset to apply: --plan /run/nova/egress-<run>.nft"})
	}
	if !okEgressRun(f.run) {
		bad = append(bad, sandbox.Refusal{Reason: "no_name", Text: "--run wants the run id the plan was built for: --run j1"})
	}
	if len(bad) > 0 {
		return egressRefuse(stderr, bad)
	}
	if line, remedy, refused := noNftBody(egressGOOS); refused {
		fmt.Fprintln(stderr, line)
		fmt.Fprintln(stderr, remedy)
		return sandbox.ExitCannotRun
	}
	audit, readBad, auditBad := egressReadPlan(f.plan)
	if len(readBad) > 0 {
		return egressRefuse(stderr, readBad)
	}
	if len(auditBad) > 0 {
		// Fail closed: a plan that cannot pass its own audit is never applied, whoever wrote
		// it. The invariants are the whole promise, and a ruleset that lost one is a wall
		// with a hole in the shape of the line that went missing.
		return egressSaidNo(stderr, auditBad)
	}
	table := sandbox.EgressTableName(f.run)
	if audit.Table != table {
		return egressRefuse(stderr, []sandbox.Refusal{{Reason: "plan_mismatch",
			Text: fmt.Sprintf("the plan holds the table %s and --run %s names %s; a run applies its OWN plan, because the drop that follows deletes one table by name and would leave the other standing", oneline.Escape(audit.Table), oneline.Escape(f.run), oneline.Escape(table))}})
	}
	if code := needNft(stderr); code != 0 {
		return code
	}
	return egressRun(stderr, "apply", f.run, table, "-f", f.plan)
}

// egressDropVerb takes the run's wall away. It names ONE table, the one this tool made.
func egressDropVerb(f egressFlags, stderr io.Writer) int {
	bad := f.bad
	if !okEgressRun(f.run) {
		bad = append(bad, sandbox.Refusal{Reason: "no_name",
			Text: "--run wants the run id whose wall goes away, letters, digits and _ : --run j1"})
	}
	if f.plan != "" {
		bad = append(bad, sandbox.Refusal{Reason: "no_command", Text: "--plan belongs to the apply and check verbs; drop deletes the table --run names"})
	}
	if len(bad) > 0 {
		return egressRefuse(stderr, bad)
	}
	if line, remedy, refused := noNftBody(egressGOOS); refused {
		fmt.Fprintln(stderr, line)
		fmt.Fprintln(stderr, remedy)
		return sandbox.ExitCannotRun
	}
	if code := needNft(stderr); code != 0 {
		return code
	}
	table := sandbox.EgressTableName(f.run)
	return egressRun(stderr, "drop", f.run, table, "delete", "table", "inet", table)
}

// egressRun is the one place nft is executed, and the one place these verbs print a receipt.
func egressRun(stderr io.Writer, verb, run, table string, args ...string) int {
	fmt.Fprintf(stderr, "EGRESS STEP name=%s state=start\n", oneline.Field(verb))
	at := time.Now()
	out, err := egressPriv.Run("nft", args...)
	fmt.Fprintf(stderr, "EGRESS STEP name=%s state=done ms=%d\n", oneline.Field(verb), time.Since(at).Milliseconds())
	if err != nil {
		return egressSaidNo(stderr, []sandbox.Refusal{{Reason: "nft_failed",
			Text: fmt.Sprintf("nft %s: %s: %s", strings.Join(args, " "), oneline.Err(err), oneline.Cap(strings.TrimSpace(out), 400))}})
	}
	fmt.Fprintf(stderr, "EGRESS OK verb=%s run=%s table=%s\n", oneline.Field(verb), oneline.Field(run), oneline.Field(table))
	return 0
}

// needNft is the refusal a bench without nftables gets, with the one line that fixes it. A
// tool that ran `sudo nft` and let the shell's error stand would leave the operator reading
// a message from a program they did not call.
func needNft(stderr io.Writer) int {
	if _, err := egressPriv.Look("nft"); err != nil {
		fmt.Fprintf(stderr, "EGRESS REFUSED reason=no_nft: nft is not on this bench (%s), and this wall is nftables; nothing was applied and nothing was dropped\n", oneline.Err(err))
		fmt.Fprintln(stderr, nftRemedy)
		return sandbox.ExitCannotRun
	}
	return 0
}

// noNftBody is rule 1's shape for these two verbs, with the platform NAMED so a test on a
// Mac can ask what the tool says on linux. apply and drop are nftables', and nftables is
// linux's; on darwin the card's outbound wall is the seatbelt profile this binary already
// generates, and saying so is better than a wall nobody applied.
func noNftBody(goos string) (line, remedy string, refused bool) {
	if goos == "linux" {
		return "", "", false
	}
	return fmt.Sprintf("EGRESS REFUSED reason=not_linux: the egress wall is nftables on the bench and nftables is linux's; %s has no body here and this tool does not pretend a ruleset was applied",
			oneline.Field(goos)),
		"on darwin the card's outbound wall is the seatbelt profile this binary already generates — run the card under `nova-sandbox run` (or the bare form) and use --net-deny when it needs no network at all; `egress plan` and `egress check` still run here, so a plan can be built and read on a Mac and applied on a bench",
		true
}

// okEgressRun is the shape a --run may take, and the check is internal/sandbox's own: the
// value becomes an nft table name, which is an identifier and not a string, and one tool
// does not carry two opinions about what a run id looks like.
func okEgressRun(s string) bool { return sandbox.OKEgressRun(s) }
