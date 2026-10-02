package main

import (
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The egress verbs, with the three things they reach the machine through REPLACED: the
// resolver (no packet), the privileged command (no nft, no sudo) and the platform. Nothing
// here resolves a name, opens a socket or runs a command; Johnny's page asks for exactly
// this shape: "unit test feeds a fake resolver + a fake connect".

// fakePriv is the privileged-command seam. It records what it was asked to run, in order,
// because the order is the contract: nothing is handed to nft before the plan has been
// audited.
type fakePriv struct {
	calls   []string
	missing bool
	runErr  error
	out     string
}

func (f *fakePriv) Look(name string) (string, error) {
	if f.missing {
		return "", errors.New("executable file not found in $PATH")
	}
	return "/usr/sbin/" + name, nil
}

func (f *fakePriv) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	return f.out, f.runErr
}

type fakeLookup struct{ table map[string][]netip.Addr }

func (f fakeLookup) LookupHost(name string) ([]netip.Addr, error) {
	got, ok := f.table[name]
	if !ok {
		return nil, errors.New("no such host")
	}
	return got, nil
}

// The addresses these tests pin are documentation addresses (TEST-NET-3 and
// 2001:db8::/32), and the model host comes out of the shipped allowlist rather than out of a
// literal here: a fixture never spells a host this suite could be pointed at, and the four
// names a card really reaches live in infra/image/egress.txt, which is data.
const (
	testModelAddr = "198.51.100.13"
	testBaseAddr  = "198.51.100.10"
	testBaseAddr6 = "2001:db8::11"
)

// shippedModelHost is the one model host of these runs, read from the file in git. A test
// that named it in a literal would be a second copy of the allowlist.
func shippedModelHost(t *testing.T) string {
	t.Helper()
	names, bad := sandbox.ParseEgressPolicy([]byte(testkit.ReadFile(t, filepath.Join("..", "..", "infra", "image", "egress.txt"))))
	require.Empty(t, bad, "infra/image/egress.txt does not parse")
	for _, n := range names {
		if !slices.Contains(sandbox.EgressBaseNames, n) {
			return n
		}
	}
	t.Fatal("infra/image/egress.txt carries no model host, so no run can name one")
	return ""
}

// egressBench puts the three seams in place for one test and puts the production bodies
// back afterwards, so a test that forgets cannot leave the next one talking to the machine.
// It returns the test's job and the path in it a plan is written to.
func egressBench(t *testing.T, goos string, priv *fakePriv) (job, string) {
	t.Helper()
	swap[privilegedCommand](t, &egressPriv, priv)
	swap(t, &egressGOOS, goos)
	table := map[string][]netip.Addr{
		sandbox.EgressBaseNames[0]: {netip.MustParseAddr(testBaseAddr)},
		sandbox.EgressBaseNames[1]: {netip.MustParseAddr("198.51.100.11"), netip.MustParseAddr(testBaseAddr6)},
		sandbox.EgressBaseNames[2]: {netip.MustParseAddr("198.51.100.12")},
		shippedModelHost(t):        {netip.MustParseAddr(testModelAddr)},
	}
	swap(t, &egressLookup, func(netip.Addr) sandboxResolver { return fakeLookup{table: table} })
	j := newJob(t)
	return j, filepath.Join(j.write, "plan.nft")
}

// planArgs is one good plan invocation against the SHIPPED policy file, which is the
// contract: a test that built its own allowlist would pass on the day the two disagreed.
func planArgs(t *testing.T, out string) []string {
	t.Helper()
	return []string{"egress", "plan", "--run", "j1", "--policy", filepath.Join("..", "..", "infra", "image", "egress.txt"),
		"--model-host", shippedModelHost(t), "--resolver", "10.9.0.53", "--bench-cidr", "10.1.0.0/24", "--uid", "10001", "--out", out}
}

// The egress verbs as a reviewer and a bench run them, one row a run on a bench of its own:
// what plan writes and refuses, and what apply, drop and check hand nft, read off the
// privileged-command seam in order, or refuse. OUT in a row is the plan's path; a row's
// plan, when it has one, is written by the good plan invocation and then broken by its edit.
func TestEgressApplyHandsTheAuditedPlanToNft(t *testing.T) {
	const nonZero = -1
	apply := []string{"egress", "apply", "--plan", "OUT", "--run", "j1"}
	check := []string{"egress", "check", "--plan", "OUT"}
	drop := func(run string) []string { return []string{"egress", "drop", "--run", run} }
	// A refused plan is exit 2 (the verb could not run) with its reason and the one remedy
	// line, and leaves no file behind for a later apply to pick up.
	refusedPlan := func(name, reason string, args []string) egressRow {
		return egressRow{name: name, args: args, code: 2, says: []string{"EGRESS REFUSED reason=" + reason, egressRemedy}, noPlan: true}
	}
	for _, c := range []egressRow{
		// The receipt is the line the spec publishes.
		{name: "plan writes a ruleset and prints its receipt", args: planArgs(t, "OUT"),
			says: []string{"EGRESS PLAN run=j1 allow=", "names=" + strings.Join(append(slices.Clone(sandbox.EgressBaseNames), shippedModelHost(t)), ",")},
			lines: []string{
				"table inet nova_egress_j1 {",
				"meta skuid 10001 ip daddr 169.254.169.254/32 drop",
				"meta skuid 10001 ip daddr 10.1.0.0/24 drop",
				"meta skuid 10001 ip daddr 10.9.0.53 udp dport 53 accept",
				"meta skuid 10001 ip daddr " + testModelAddr + " tcp dport 443 accept",
				"meta skuid 10001 drop",
			}},
		// The plan this tool writes passes this tool's own audit: the check verb reading the
		// file, which is what a reviewer runs.
		{name: "plan passes its own check", plan: true, args: check},
		refusedPlan("a model host outside the file", "bad_model_host", replaceFlag(planArgs(t, "OUT"), "--model-host", "other-model.example.test")),
		refusedPlan("a resolver inside a denied range", "bad_resolver", replaceFlag(planArgs(t, "OUT"), "--resolver", "127.0.0.53")),
		refusedPlan("a bench cidr that is not one", "bad_cidr", replaceFlag(planArgs(t, "OUT"), "--bench-cidr", "10.1.0.0")),
		refusedPlan("a run id that is not a table name", "no_name", replaceFlag(planArgs(t, "OUT"), "--run", "j 1")),
		refusedPlan("a policy file that is not there", "bad_policy", replaceFlag(planArgs(t, "OUT"), "--policy", filepath.Join(t.TempDir(), "nope.txt"))),
		refusedPlan("a flag of another verb", "no_command", append(planArgs(t, "OUT"), "--net-deny")),
		// An unscoped default deny would firewall the bench itself.
		refusedPlan("neither --uid nor --veth", "no_selector", replaceFlag(planArgs(t, "OUT"), "--uid", "")),
		{name: "apply hands the audited plan to nft", plan: true, args: apply,
			says: []string{"EGRESS OK verb=apply run=j1 table=nova_egress_j1"}, calls: "nft -f OUT"},
		// The default deny, removed: a plan that lost it allows everything the allows did not
		// name. A plan is audited BEFORE nft sees it, or the audit is a comment.
		{name: "apply refuses a plan it cannot audit", plan: true, edit: [2]string{"\t\tmeta skuid 10001 drop\n", ""},
			args: apply, code: nonZero, says: []string{"reason=no_default_deny"}},
		// A run applies its OWN plan: the drop that follows names one table and would leave
		// the other standing.
		{name: "apply refuses a plan from another run", plan: true, args: []string{"egress", "apply", "--plan", "OUT", "--run", "j2"},
			code: nonZero, says: []string{"reason=plan_mismatch"}},
		{name: "refuses when nft is not on the bench", missing: true, plan: true, args: apply, code: 2, says: []string{"reason=no_nft", nftRemedy}},
		{name: "drop deletes exactly the run's table", args: drop("j1"),
			says: []string{"EGRESS OK verb=drop run=j1 table=nova_egress_j1"}, calls: "nft delete table inet nova_egress_j1"},
		{name: "drop refuses an empty run id", args: drop(""), code: 2, says: []string{"reason=no_name"}},
		{name: "drop refuses a run id with a command in it", args: drop("j1; flush ruleset"), code: 2, says: []string{"reason=no_name"}},
		{name: "drop refuses a run id that is a path", args: drop("../j1"), code: 2, says: []string{"reason=no_name"}},
		// apply and drop are LINUX's: nftables is the bench's wall. On darwin the wall is the
		// seatbelt profile this binary already applies, and the refusal says so rather than
		// pretending a plan was enforced.
		{name: "apply refuses off linux", darwin: true, args: apply, code: 2, says: []string{"reason=not_linux", "seatbelt"}},
		{name: "drop refuses off linux", darwin: true, args: drop("j1"), code: 2, says: []string{"reason=not_linux", "seatbelt"}},
		// plan and check run ANYWHERE: a plan is text and an audit is a read, and a reviewer on
		// a Mac has to be able to build and check the ruleset a bench will apply.
		{name: "plan and check run off linux", darwin: true, plan: true, args: check},
		// A plan that allows the whole internet: exit 1, the verb ran and said NO.
		{name: "check goes red on a broken plan", plan: true,
			edit: [2]string{"ip daddr " + testModelAddr + " tcp dport 443 accept", "ip daddr 0.0.0.0/0 tcp dport 443 accept"},
			args: check, code: 1, says: []string{"reason=allow_any"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			priv := &fakePriv{missing: c.missing}
			goos := "linux"
			if c.darwin {
				goos = "darwin"
			}
			j, out := egressBench(t, goos, priv)
			if c.plan {
				j.run(t, planArgs(t, out)...).Exit(0)
			}
			if c.edit[0] != "" {
				raw := testkit.ReadFile(t, out)
				broken := strings.Replace(raw, c.edit[0], c.edit[1], 1)
				require.NotEqual(t, raw, broken, "the test did not break the plan")
				testkit.WriteFile(t, out, broken, 0o600)
			}
			args := slices.Clone(c.args)
			for i := range args {
				args[i] = strings.ReplaceAll(args[i], "OUT", out)
			}
			r := j.run(t, args...)
			if c.code == nonZero {
				assert.NotEqual(t, 0, r.Code, r)
			} else {
				assert.Equal(t, c.code, r.Code, r)
			}
			for _, s := range c.says {
				assert.Contains(t, r.Stderr, s, r)
			}
			assert.Equal(t, strings.ReplaceAll(c.calls, "OUT", out), strings.Join(priv.calls, "|"), "what nft was handed")
			if c.noPlan {
				assert.Error(t, statErr(out), "a refused plan wrote a plan file")
			}
			for _, line := range c.lines {
				assert.Contains(t, testkit.ReadFile(t, out), line, "the written plan has no such line")
			}
		})
	}
}

// egressRow is one run of TestEgressApplyHandsTheAuditedPlanToNft.
type egressRow struct {
	name          string
	darwin        bool      // egressGOOS is darwin, not linux
	missing, plan bool      // nft is not on the bench; the plan is written first
	edit          [2]string // the plan's text, old for new, that breaks it
	args          []string
	code          int
	says          []string
	calls         string   // every command nft was handed, "|"-joined; none for a refusal
	noPlan        bool     // no plan file is left at OUT
	lines         []string // lines the plan file at OUT holds
}

func TestEgressVerbRefusesAnUnknownSubVerb(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	for _, args := range [][]string{{"egress"}, {"egress", "flush"}} {
		r := j.run(t, args...)
		assert.Equal(t, 2, r.Code, r)
		assert.Contains(t, r.Stderr, "EGRESS REFUSED reason=no_command", r)
	}
}

// The wall's contract where its readers find it. The line a blocked destination costs is
// Johnny's, word for word, published in the spec's grammar block, so this reads the spec
// rather than a copy of it, like grammar_test.go does for the probe's reasons. The image's
// README carries the run contract: the worker calls plan → apply → run → drop, and a
// reader of the image learns that there.
func TestTheBlockedDestinationLineIsTheSpecsOwn(t *testing.T) {
	t.Parallel()
	t.Run("the blocked destination line is the spec's own", func(t *testing.T) {
		assert.NotEmpty(t, specFenced(t, egressDeniedLine), "docs/SPEC-SANDBOX.md publishes no fenced line beginning %q; the card's one line for a blocked destination is what a caller's parser stands on", egressDeniedLine)
	})
	t.Run("the image readme carries the egress contract", func(t *testing.T) {
		text := testkit.ReadFile(t, filepath.Join("..", "..", "infra", "image", "README.md"))
		for _, want := range []string{"egress.txt", "nova-sandbox egress plan", "egress drop", "EGRESS DENIED host=", "seatbelt"} {
			assert.Contains(t, text, want, "the image's run contract does not mention it")
		}
	})
}

// replaceFlag swaps one flag's value in an argv, or removes the flag when the value is
// empty. It is the tests' own small helper and never the tool's.
func replaceFlag(args []string, flag, value string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++
			if value != "" {
				out = append(out, flag, value)
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}
