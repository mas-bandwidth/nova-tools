package main

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The egress verbs, with the three things they reach the machine through REPLACED: the
// resolver (no packet), the privileged command (no nft, no sudo) and the platform. What is
// under test is the contract a bench stands on — a plan that pins what it resolved, an
// apply that refuses a plan it cannot audit or that belongs to another run, a drop that
// names one table, and a check that goes red on a broken plan.
//
// Nothing here resolves a name, opens a socket or runs a command. Johnny's page asks for
// exactly this shape: "unit test feeds a fake resolver + a fake connect".

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
	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "image", "egress.txt"))
	require.NoError(t, err, "infra/image/egress.txt is the allowlist and it has to be readable: %s", err)
	names, bad := sandbox.ParseEgressPolicy(raw)
	require.Empty(t, bad, "infra/image/egress.txt does not parse: %v", bad)
	base := map[string]bool{}
	for _, n := range sandbox.EgressBaseNames {
		base[n] = true
	}
	for _, n := range names {
		if !base[n] {
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

// planned writes the plan the apply and check tests start from, and requires it.
func planned(t *testing.T, j job, out string) {
	t.Helper()
	r := j.run(t, planArgs(t, out)...)
	require.Equal(t, 0, r.Code, "the plan could not be built: %s", r.Stderr)
}

// planArgs is one good plan invocation against the SHIPPED policy file, which is the
// contract: a test that built its own allowlist would pass on the day the two disagreed.
func planArgs(t *testing.T, out string, extra ...string) []string {
	t.Helper()
	return append([]string{"egress", "plan",
		"--run", "j1",
		"--policy", filepath.Join("..", "..", "infra", "image", "egress.txt"),
		"--model-host", shippedModelHost(t),
		"--resolver", "10.9.0.53",
		"--bench-cidr", "10.1.0.0/24",
		"--uid", "10001",
		"--out", out}, extra...)
}

func TestEgressPlanWritesARulesetAndPrintsItsReceipt(t *testing.T) {
	j, out := egressBench(t, "linux", &fakePriv{})
	r := j.run(t, planArgs(t, out)...)
	require.Equal(t, 0, r.Code, "a good plan exited %d: %s", r.Code, r.Stderr)
	wantNames := "names=" + strings.Join(append(append([]string{}, sandbox.EgressBaseNames...), shippedModelHost(t)), ",")
	assert.Contains(t, r.Stderr, "EGRESS PLAN run=j1 allow=", "the plan's receipt is not the line the spec publishes: %q", r.Stderr)
	assert.Contains(t, r.Stderr, wantNames, "the plan's receipt is not the line the spec publishes: %q", r.Stderr)
	raw, err := os.ReadFile(out)
	require.NoError(t, err, "the plan file was not written: %s", err)
	text := string(raw)
	for _, want := range []string{
		"table inet nova_egress_j1 {",
		"meta skuid 10001 ip daddr 169.254.169.254/32 drop",
		"meta skuid 10001 ip daddr 10.1.0.0/24 drop",
		"meta skuid 10001 ip daddr 10.9.0.53 udp dport 53 accept",
		"meta skuid 10001 ip daddr " + testModelAddr + " tcp dport 443 accept",
		"meta skuid 10001 drop",
	} {
		assert.Contains(t, text, want, "the written plan has no line %q:\n%s", want, text)
	}
	// And the plan this tool writes passes this tool's own audit — the check verb read
	// from the file, which is what a reviewer runs.
	r = j.run(t, "egress", "check", "--plan", out)
	assert.Equal(t, 0, r.Code, "the plan this tool wrote fails its own check verb: exit %d, %s", r.Code, r.Stderr)
}

func TestEgressPlanRefusesTheInputsThatWouldWidenTheWall(t *testing.T) {
	j, out := egressBench(t, "linux", &fakePriv{})
	cases := []struct {
		name, reason string
		args         []string
	}{
		{"a model host outside the file", "bad_model_host", replaceFlag(planArgs(t, out), "--model-host", "other-model.example.test")},
		{"a resolver inside a denied range", "bad_resolver", replaceFlag(planArgs(t, out), "--resolver", "127.0.0.53")},
		{"a bench cidr that is not one", "bad_cidr", replaceFlag(planArgs(t, out), "--bench-cidr", "10.1.0.0")},
		{"a run id that is not a table name", "no_name", replaceFlag(planArgs(t, out), "--run", "j 1")},
		{"a policy file that is not there", "bad_policy", replaceFlag(planArgs(t, out), "--policy", filepath.Join(j.write, "nope.txt"))},
		{"a flag of another verb", "no_command", append(planArgs(t, out), "--net-deny")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(out)
			r := j.run(t, c.args...)
			assert.Equal(t, 2, r.Code, "%s exited %d, want 2 (the verb could not run): %s", c.name, r.Code, r.Stderr)
			assert.Contains(t, r.Stderr, "EGRESS REFUSED reason="+c.reason, "%s did not refuse with reason=%s: %q", c.name, c.reason, r.Stderr)
			assert.Contains(t, r.Stderr, egressRemedy, "%s refused without the one remedy line: %q", c.name, r.Stderr)
			assert.Error(t, statErr(out), "%s was refused and a plan file was written anyway; a refused plan leaves nothing behind for a later apply to pick up", c.name)
		})
	}
}

func TestEgressPlanNeedsASelector(t *testing.T) {
	j, out := egressBench(t, "linux", &fakePriv{})
	args := planArgs(t, out)
	args = replaceFlag(args, "--uid", "")
	r := j.run(t, args...)
	r.ExitErr(2, "reason=no_selector", "a plan with neither --uid nor --veth exited %d: %q; an unscoped default deny would firewall the bench itself", r.Code, r.Stderr)
}

func TestEgressApplyHandsTheAuditedPlanToNft(t *testing.T) {
	priv := &fakePriv{}
	j, out := egressBench(t, "linux", priv)
	planned(t, j, out)
	r := j.run(t, "egress", "apply", "--plan", out, "--run", "j1")
	require.Equal(t, 0, r.Code, "apply exited %d: %s", r.Code, r.Stderr)
	got, want := strings.Join(priv.calls, "|"), "nft -f "+out
	assert.Equal(t, want, got, "apply ran %q, want %q", got, want)
	assert.Contains(t, r.Stderr, "EGRESS OK verb=apply run=j1 table=nova_egress_j1", "apply printed no receipt: %q", r.Stderr)
}

func TestEgressApplyRefusesAPlanItCannotAudit(t *testing.T) {
	priv := &fakePriv{}
	j, out := egressBench(t, "linux", priv)
	planned(t, j, out)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	// The default deny, removed. A plan that lost it is a plan that allows everything the
	// allows above did not name — the exact opposite of what it says on the tin.
	broken := strings.Replace(string(raw), "\t\tmeta skuid 10001 drop\n", "", 1)
	require.NotEqual(t, string(raw), broken, "the test did not break the plan")
	require.NoError(t, os.WriteFile(out, []byte(broken), 0o600))
	r := j.run(t, "egress", "apply", "--plan", out, "--run", "j1")
	assert.NotEqual(t, 0, r.Code, "apply took a plan that fails its own audit: %s", r.Stderr)
	assert.Contains(t, r.Stderr, "reason=no_default_deny", "apply did not name what was wrong with the plan: %q", r.Stderr)
	assert.Empty(t, priv.calls, "apply ran %v before the audit; a plan is audited BEFORE nft sees it, or the audit is a comment", priv.calls)
}

func TestEgressApplyRefusesAPlanFromAnotherRun(t *testing.T) {
	priv := &fakePriv{}
	j, out := egressBench(t, "linux", priv)
	planned(t, j, out)
	r := j.run(t, "egress", "apply", "--plan", out, "--run", "j2")
	require.NotEqual(t, 0, r.Code, "apply took run j1's plan for run j2: exit %d, %q; the drop that follows names one table and would leave the other standing", r.Code, r.Stderr)
	require.Contains(t, r.Stderr, "reason=plan_mismatch", "apply took run j1's plan for run j2: exit %d, %q; the drop that follows names one table and would leave the other standing", r.Code, r.Stderr)
	assert.Empty(t, priv.calls, "apply ran %v on a plan that belongs to another run", priv.calls)
}

func TestEgressRefusesWhenNftIsNotOnTheBench(t *testing.T) {
	priv := &fakePriv{missing: true}
	j, out := egressBench(t, "linux", priv)
	planned(t, j, out)
	r := j.run(t, "egress", "apply", "--plan", out, "--run", "j1")
	r.ExitErr(2, "reason=no_nft", "a bench without nft exited %d: %q", r.Code, r.Stderr)
	assert.Contains(t, r.Stderr, nftRemedy, "the refusal carries no remedy line: %q", r.Stderr)
	assert.Empty(t, priv.calls, "something was run on a bench with no nft: %v", priv.calls)
}

func TestEgressDropDeletesExactlyTheRunsTable(t *testing.T) {
	priv := &fakePriv{}
	j, _ := egressBench(t, "linux", priv)
	r := j.run(t, "egress", "drop", "--run", "j1")
	require.Equal(t, 0, r.Code, "drop exited %d: %s", r.Code, r.Stderr)
	got, want := strings.Join(priv.calls, "|"), "nft delete table inet nova_egress_j1"
	assert.Equal(t, want, got, "drop ran %q, want %q", got, want)
	assert.Contains(t, r.Stderr, "EGRESS OK verb=drop run=j1 table=nova_egress_j1", "drop printed no receipt: %q", r.Stderr)
}

func TestEgressDropRefusesARunIdThatIsNotATableName(t *testing.T) {
	priv := &fakePriv{}
	j, _ := egressBench(t, "linux", priv)
	for _, run := range []string{"", "j1; flush ruleset", "../j1"} {
		r := j.run(t, "egress", "drop", "--run", run)
		assert.Equal(t, 2, r.Code, "drop --run %q exited %d: %q", run, r.Code, r.Stderr)
		assert.Contains(t, r.Stderr, "reason=no_name", "drop --run %q exited %d: %q", run, r.Code, r.Stderr)
	}
	assert.Empty(t, priv.calls, "drop ran %v on a name it refused", priv.calls)
}

// apply and drop are LINUX's: nftables is the bench's wall. On darwin the wall is the
// seatbelt profile this binary already applies, and the refusal says so rather than
// pretending a plan was enforced.
func TestEgressApplyAndDropRefuseOffLinux(t *testing.T) {
	priv := &fakePriv{}
	j, out := egressBench(t, "darwin", priv)
	for _, args := range [][]string{
		{"egress", "apply", "--plan", out, "--run", "j1"},
		{"egress", "drop", "--run", "j1"},
	} {
		r := j.run(t, args...)
		assert.Equal(t, 2, r.Code, "%v on darwin exited %d: %q", args, r.Code, r.Stderr)
		assert.Contains(t, r.Stderr, "reason=not_linux", "%v on darwin exited %d: %q", args, r.Code, r.Stderr)
		assert.Contains(t, r.Stderr, "seatbelt", "%v does not say where the wall is on this platform: %q", args, r.Stderr)
	}
	assert.Empty(t, priv.calls, "something ran nft on darwin: %v", priv.calls)
}

// plan and check run ANYWHERE: a plan is text and an audit is a read, and a reviewer on a
// Mac has to be able to build and check the ruleset a bench will apply.
func TestEgressPlanAndCheckRunOffLinux(t *testing.T) {
	j, out := egressBench(t, "darwin", &fakePriv{})
	r := j.run(t, planArgs(t, out)...)
	require.Equal(t, 0, r.Code, "plan on darwin exited %d: %s", r.Code, r.Stderr)
	r = j.run(t, "egress", "check", "--plan", out)
	require.Equal(t, 0, r.Code, "check on darwin exited %d: %s", r.Code, r.Stderr)
}

func TestEgressCheckGoesRedOnABrokenPlan(t *testing.T) {
	j, out := egressBench(t, "linux", &fakePriv{})
	planned(t, j, out)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	broken := strings.Replace(string(raw), "ip daddr "+testModelAddr+" tcp dport 443 accept", "ip daddr 0.0.0.0/0 tcp dport 443 accept", 1)
	require.NoError(t, os.WriteFile(out, []byte(broken), 0o600))
	r := j.run(t, "egress", "check", "--plan", out)
	assert.Equal(t, 1, r.Code, "check exited %d on a plan that allows the whole internet, want 1 (the verb ran and said NO): %s", r.Code, r.Stderr)
	assert.Contains(t, r.Stderr, "reason=allow_any", "check did not name what it found: %q", r.Stderr)
}

func TestEgressVerbRefusesAnUnknownSubVerb(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	for _, args := range [][]string{{"egress"}, {"egress", "flush"}} {
		r := j.run(t, args...)
		assert.Equal(t, 2, r.Code, "%v exited %d: %q", args, r.Code, r.Stderr)
		assert.Contains(t, r.Stderr, "EGRESS REFUSED reason=no_command", "%v exited %d: %q", args, r.Code, r.Stderr)
	}
}

// The line a blocked destination costs is Johnny's, word for word, and it is published in
// the spec's grammar block — so this reads the spec rather than a copy of it, like
// grammar_test.go does for the probe's reasons.
func TestTheBlockedDestinationLineIsTheSpecsOwn(t *testing.T) {
	t.Parallel()

	spec := specSandbox(t)
	inFence, found := false, false
	for _, line := range strings.Split(spec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(line, egressDeniedLine) {
			found = true
		}
	}
	assert.True(t, found, "docs/SPEC-SANDBOX.md publishes no fenced line beginning %q; the card's one line for a blocked destination is what a caller's parser stands on", egressDeniedLine)
}

// The run contract in the image's README is the other half of this wall, and a reader of
// the image has to find the order the worker calls the verbs in.
func TestTheImageReadmeCarriesTheEgressContract(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "image", "README.md"))
	require.NoError(t, err, "infra/image/README.md is the run contract and it has to be readable: %s", err)
	text := string(raw)
	for _, want := range []string{"egress.txt", "nova-sandbox egress plan", "egress drop", "EGRESS DENIED host=", "seatbelt"} {
		assert.Contains(t, text, want, "the image's run contract does not mention %q; the worker calls plan → apply → run → drop and a reader of the image learns that here", want)
	}
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
