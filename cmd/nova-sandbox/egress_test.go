package main

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// egressBench puts the three seams in place for one test and puts the production bodies
// back afterwards, so a test that forgets cannot leave the next one talking to the machine.
func egressBench(t *testing.T, goos string, priv *fakePriv) {
	t.Helper()
	oldPriv, oldGOOS, oldLookup := egressPriv, egressGOOS, egressLookup
	t.Cleanup(func() { egressPriv, egressGOOS, egressLookup = oldPriv, oldGOOS, oldLookup })
	egressPriv, egressGOOS = priv, goos
	egressLookup = func(netip.Addr) sandboxResolver {
		return fakeLookup{table: map[string][]netip.Addr{
			"github.com":                    {netip.MustParseAddr("140.82.121.4")},
			"api.github.com":                {netip.MustParseAddr("140.82.121.6"), netip.MustParseAddr("2606:50c0:8000::153")},
			"objects.githubusercontent.com": {netip.MustParseAddr("185.199.108.133")},
			"api.deepseek.com":              {netip.MustParseAddr("104.18.26.90")},
		}}
	}
}

// planArgs is one good plan invocation against the SHIPPED policy file, which is the
// contract: a test that built its own allowlist would pass on the day the two disagreed.
func planArgs(out string, extra ...string) []string {
	return append([]string{"egress", "plan",
		"--run", "j1",
		"--policy", filepath.Join("..", "..", "infra", "image", "egress.txt"),
		"--model-host", "api.deepseek.com",
		"--resolver", "10.9.0.53",
		"--bench-cidr", "10.1.0.0/24",
		"--uid", "10001",
		"--out", out}, extra...)
}

func TestEgressPlanWritesARulesetAndPrintsItsReceipt(t *testing.T) {
	egressBench(t, "linux", &fakePriv{})
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	code, _, errOut := j.tool(t, j.env(), planArgs(out)...)
	if code != 0 {
		t.Fatalf("a good plan exited %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "EGRESS PLAN run=j1 allow=") || !strings.Contains(errOut, "names=github.com,api.github.com,objects.githubusercontent.com,api.deepseek.com") {
		t.Errorf("the plan's receipt is not the line the spec publishes: %q", errOut)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the plan file was not written: %s", err)
	}
	text := string(raw)
	for _, want := range []string{
		"table inet nova_egress_j1 {",
		"meta skuid 10001 ip daddr 169.254.169.254/32 drop",
		"meta skuid 10001 ip daddr 10.1.0.0/24 drop",
		"meta skuid 10001 ip daddr 10.9.0.53 udp dport 53 accept",
		"meta skuid 10001 ip daddr 104.18.26.90 tcp dport 443 accept",
		"meta skuid 10001 drop",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the written plan has no line %q:\n%s", want, text)
		}
	}
	// And the plan this tool writes passes this tool's own audit — the check verb read
	// from the file, which is what a reviewer runs.
	if code, _, errOut := j.tool(t, j.env(), "egress", "check", "--plan", out); code != 0 {
		t.Errorf("the plan this tool wrote fails its own check verb: exit %d, %s", code, errOut)
	}
}

func TestEgressPlanRefusesTheInputsThatWouldWidenTheWall(t *testing.T) {
	egressBench(t, "linux", &fakePriv{})
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	cases := []struct {
		name, reason string
		args         []string
	}{
		{"a model host outside the file", "bad_model_host", replaceFlag(planArgs(out), "--model-host", "api.example-model.com")},
		{"a resolver inside a denied range", "bad_resolver", replaceFlag(planArgs(out), "--resolver", "127.0.0.53")},
		{"a bench cidr that is not one", "bad_cidr", replaceFlag(planArgs(out), "--bench-cidr", "10.1.0.0")},
		{"a run id that is not a table name", "no_name", replaceFlag(planArgs(out), "--run", "j 1")},
		{"a policy file that is not there", "bad_policy", replaceFlag(planArgs(out), "--policy", filepath.Join(j.write, "nope.txt"))},
		{"a flag of another verb", "no_command", append(planArgs(out), "--net-deny")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(out)
			code, _, errOut := j.tool(t, j.env(), c.args...)
			if code != 2 {
				t.Errorf("%s exited %d, want 2 (the verb could not run): %s", c.name, code, errOut)
			}
			if !strings.Contains(errOut, "EGRESS REFUSED reason="+c.reason) {
				t.Errorf("%s did not refuse with reason=%s: %q", c.name, c.reason, errOut)
			}
			if !strings.Contains(errOut, egressRemedy) {
				t.Errorf("%s refused without the one remedy line: %q", c.name, errOut)
			}
			if _, err := os.Stat(out); err == nil {
				t.Errorf("%s was refused and a plan file was written anyway; a refused plan leaves nothing behind for a later apply to pick up", c.name)
			}
		})
	}
}

func TestEgressPlanNeedsASelector(t *testing.T) {
	egressBench(t, "linux", &fakePriv{})
	j := newJob(t)
	args := planArgs(filepath.Join(j.write, "plan.nft"))
	args = replaceFlag(args, "--uid", "")
	code, _, errOut := j.tool(t, j.env(), args...)
	if code != 2 || !strings.Contains(errOut, "reason=no_selector") {
		t.Fatalf("a plan with neither --uid nor --veth exited %d: %q; an unscoped default deny would firewall the bench itself", code, errOut)
	}
}

func TestEgressApplyHandsTheAuditedPlanToNft(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "linux", priv)
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("the plan could not be built: %s", errOut)
	}
	code, _, errOut := j.tool(t, j.env(), "egress", "apply", "--plan", out, "--run", "j1")
	if code != 0 {
		t.Fatalf("apply exited %d: %s", code, errOut)
	}
	if got, want := strings.Join(priv.calls, "|"), "nft -f "+out; got != want {
		t.Errorf("apply ran %q, want %q", got, want)
	}
	if !strings.Contains(errOut, "EGRESS OK verb=apply run=j1 table=nova_egress_j1") {
		t.Errorf("apply printed no receipt: %q", errOut)
	}
}

func TestEgressApplyRefusesAPlanItCannotAudit(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "linux", priv)
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("the plan could not be built: %s", errOut)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The default deny, removed. A plan that lost it is a plan that allows everything the
	// allows above did not name — the exact opposite of what it says on the tin.
	broken := strings.Replace(string(raw), "\t\tmeta skuid 10001 drop\n", "", 1)
	if broken == string(raw) {
		t.Fatal("the test did not break the plan")
	}
	if err := os.WriteFile(out, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := j.tool(t, j.env(), "egress", "apply", "--plan", out, "--run", "j1")
	if code == 0 {
		t.Errorf("apply took a plan that fails its own audit: %s", errOut)
	}
	if !strings.Contains(errOut, "reason=no_default_deny") {
		t.Errorf("apply did not name what was wrong with the plan: %q", errOut)
	}
	if len(priv.calls) != 0 {
		t.Errorf("apply ran %v before the audit; a plan is audited BEFORE nft sees it, or the audit is a comment", priv.calls)
	}
}

func TestEgressApplyRefusesAPlanFromAnotherRun(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "linux", priv)
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("the plan could not be built: %s", errOut)
	}
	code, _, errOut := j.tool(t, j.env(), "egress", "apply", "--plan", out, "--run", "j2")
	if code == 0 || !strings.Contains(errOut, "reason=plan_mismatch") {
		t.Fatalf("apply took run j1's plan for run j2: exit %d, %q; the drop that follows names one table and would leave the other standing", code, errOut)
	}
	if len(priv.calls) != 0 {
		t.Errorf("apply ran %v on a plan that belongs to another run", priv.calls)
	}
}

func TestEgressRefusesWhenNftIsNotOnTheBench(t *testing.T) {
	priv := &fakePriv{missing: true}
	egressBench(t, "linux", priv)
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("the plan could not be built: %s", errOut)
	}
	code, _, errOut := j.tool(t, j.env(), "egress", "apply", "--plan", out, "--run", "j1")
	if code != 2 || !strings.Contains(errOut, "reason=no_nft") {
		t.Fatalf("a bench without nft exited %d: %q", code, errOut)
	}
	if !strings.Contains(errOut, nftRemedy) {
		t.Errorf("the refusal carries no remedy line: %q", errOut)
	}
	if len(priv.calls) != 0 {
		t.Errorf("something was run on a bench with no nft: %v", priv.calls)
	}
}

func TestEgressDropDeletesExactlyTheRunsTable(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "linux", priv)
	j := newJob(t)
	code, _, errOut := j.tool(t, j.env(), "egress", "drop", "--run", "j1")
	if code != 0 {
		t.Fatalf("drop exited %d: %s", code, errOut)
	}
	if got, want := strings.Join(priv.calls, "|"), "nft delete table inet nova_egress_j1"; got != want {
		t.Errorf("drop ran %q, want %q", got, want)
	}
	if !strings.Contains(errOut, "EGRESS OK verb=drop run=j1 table=nova_egress_j1") {
		t.Errorf("drop printed no receipt: %q", errOut)
	}
}

func TestEgressDropRefusesARunIdThatIsNotATableName(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "linux", priv)
	j := newJob(t)
	for _, run := range []string{"", "j1; flush ruleset", "../j1"} {
		code, _, errOut := j.tool(t, j.env(), "egress", "drop", "--run", run)
		if code != 2 || !strings.Contains(errOut, "reason=no_name") {
			t.Errorf("drop --run %q exited %d: %q", run, code, errOut)
		}
	}
	if len(priv.calls) != 0 {
		t.Errorf("drop ran %v on a name it refused", priv.calls)
	}
}

// apply and drop are LINUX's: nftables is the bench's wall. On darwin the wall is the
// seatbelt profile this binary already applies, and the refusal says so rather than
// pretending a plan was enforced.
func TestEgressApplyAndDropRefuseOffLinux(t *testing.T) {
	priv := &fakePriv{}
	egressBench(t, "darwin", priv)
	j := newJob(t)
	for _, args := range [][]string{
		{"egress", "apply", "--plan", filepath.Join(j.write, "plan.nft"), "--run", "j1"},
		{"egress", "drop", "--run", "j1"},
	} {
		code, _, errOut := j.tool(t, j.env(), args...)
		if code != 2 || !strings.Contains(errOut, "reason=not_linux") {
			t.Errorf("%v on darwin exited %d: %q", args, code, errOut)
		}
		if !strings.Contains(errOut, "seatbelt") {
			t.Errorf("%v does not say where the wall is on this platform: %q", args, errOut)
		}
	}
	if len(priv.calls) != 0 {
		t.Errorf("something ran nft on darwin: %v", priv.calls)
	}
}

// plan and check run ANYWHERE: a plan is text and an audit is a read, and a reviewer on a
// Mac has to be able to build and check the ruleset a bench will apply.
func TestEgressPlanAndCheckRunOffLinux(t *testing.T) {
	egressBench(t, "darwin", &fakePriv{})
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("plan on darwin exited %d: %s", code, errOut)
	}
	if code, _, errOut := j.tool(t, j.env(), "egress", "check", "--plan", out); code != 0 {
		t.Fatalf("check on darwin exited %d: %s", code, errOut)
	}
}

func TestEgressCheckGoesRedOnABrokenPlan(t *testing.T) {
	egressBench(t, "linux", &fakePriv{})
	j := newJob(t)
	out := filepath.Join(j.write, "plan.nft")
	if code, _, errOut := j.tool(t, j.env(), planArgs(out)...); code != 0 {
		t.Fatalf("the plan could not be built: %s", errOut)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(raw), "ip daddr 104.18.26.90 tcp dport 443 accept", "ip daddr 0.0.0.0/0 tcp dport 443 accept", 1)
	if err := os.WriteFile(out, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := j.tool(t, j.env(), "egress", "check", "--plan", out)
	if code != 1 {
		t.Errorf("check exited %d on a plan that allows the whole internet, want 1 (the verb ran and said NO): %s", code, errOut)
	}
	if !strings.Contains(errOut, "reason=allow_any") {
		t.Errorf("check did not name what it found: %q", errOut)
	}
}

func TestEgressVerbRefusesAnUnknownSubVerb(t *testing.T) {
	j := newJob(t)
	for _, args := range [][]string{{"egress"}, {"egress", "flush"}} {
		code, _, errOut := j.tool(t, j.env(), args...)
		if code != 2 || !strings.Contains(errOut, "EGRESS REFUSED reason=no_command") {
			t.Errorf("%v exited %d: %q", args, code, errOut)
		}
	}
}

// The line a blocked destination costs is Johnny's, word for word, and it is published in
// the spec's grammar block — so this reads the spec rather than a copy of it, like
// grammar_test.go does for the probe's reasons.
func TestTheBlockedDestinationLineIsTheSpecsOwn(t *testing.T) {
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
	if !found {
		t.Errorf("docs/SPEC-SANDBOX.md publishes no fenced line beginning %q; the card's one line for a blocked destination is what a caller's parser stands on", egressDeniedLine)
	}
}

// The run contract in the image's README is the other half of this wall, and a reader of
// the image has to find the order the worker calls the verbs in.
func TestTheImageReadmeCarriesTheEgressContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "image", "README.md"))
	if err != nil {
		t.Fatalf("infra/image/README.md is the run contract and it has to be readable: %s", err)
	}
	text := string(raw)
	for _, want := range []string{"egress.txt", "nova-sandbox egress plan", "egress drop", "EGRESS DENIED host=", "seatbelt"} {
		if !strings.Contains(text, want) {
			t.Errorf("the image's run contract does not mention %q; the worker calls plan → apply → run → drop and a reader of the image learns that here", want)
		}
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
