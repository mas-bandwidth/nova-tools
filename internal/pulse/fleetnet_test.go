package pulse

// The red tests docs/SPEC-FLEET-NET.md names, one per rule it can have one for. Every one
// of them drives the FAKE tailnet: no test here opens a socket, runs tailscale, or reaches a
// machine (the hard rule of 2026-09-17 -- unit tests never touch the network).

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"strings"
	"testing"
	"time"
)

// fakeTailnet is the seam's fake: a list of nodes and an error, and nothing else. It is as
// strict as the real thing in the one way that matters -- it answers only what a real
// `tailscale status --json` could answer.
type fakeTailnet struct {
	nodes []TailnetNode
	err   error
}

func (f fakeTailnet) Status(context.Context) ([]TailnetNode, error) { return f.nodes, f.err }

// withTailnet installs the fake for one test and puts the real one back after it.
func withTailnet(t *testing.T, tn Tailnet) {
	t.Helper()
	was := NewTailnet
	NewTailnet = func(string) Tailnet { return tn }
	t.Cleanup(func() { NewTailnet = was })
}

// netRegistry writes a machines file for one test. Eight columns, because the provider is
// the whole point of these verbs.
func netRegistry(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	netRowHulk   = "hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\ttailnet\t-"
	netRowStudio = "studio\tstudio\tdarwin/arm64\tcoordination\tstudio\t32\ttailnet\t-"
	netRowAir    = "air\tair\tdarwin/arm64\tbud\t-\t8\ttailnet\tthe M2 Air"
	netRowMini   = "mini\tmini\tlinux/x64\trunner\t-\t4\ttailnet\tone CI runner"
	netRowSpace  = "space\tspace\tlinux/x64\tbench,services\tswarm-space\t32\ttailnet\tthe stack"
	netRowNAS    = "nas\tnas\tlinux/x64\tservices\t-\t4\tlan\ta node with no tailnet is still a node"
	netRowAda    = "ada\tada\tlinux/x64\tbench\t-\t16\tshared-from:stella\tanother node's machine"
	netRowOld    = "vision\tvision\tlinux/x64\tbench\tswarm-vision\t64\tthe seven-column form" // no provider
)

// TestNetStatusCrossesTheTailnetWithTheRegistry is R2's red test: the verb's whole job is
// the CROSS, and the two directions of it are the two findings.
func TestNetStatusCrossesTheTailnetWithTheRegistry(t *testing.T) {
	withTailnet(t, fakeTailnet{nodes: []TailnetNode{
		{Name: "hulk", Addr: "100.1.2.3", Online: true},
		{Name: "studio", Addr: "100.1.2.4", Online: false, LastSeen: time.Date(2026, 9, 18, 3, 4, 5, 0, time.UTC)},
	}})
	var out, errb bytes.Buffer
	code := FleetNetStatus(FleetNetStatusInput{
		Machines: netRegistry(t, netRowHulk, netRowStudio),
		Stdout:   &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%q", code, errb.String())
	}
	for _, want := range []string{
		"NET MACHINE hulk provider=tailnet roles=bench online=yes addr=100.1.2.3 last-seen=-",
		"NET MACHINE studio provider=tailnet roles=coordination online=no addr=100.1.2.4 last-seen=2026-09-18T03:04:05Z",
		"NET STATUS OK machines=2 online=1 findings=0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not carry %q; got:\n%s", want, out.String())
		}
	}
}

// TestNetStatusFindsAMachineWithNoNodeAndANodeWithNoMachine is the same rule's other half,
// and the exit code that goes with it: a finding is the tool saying NO (exit 1), not a
// failure to run (exit 2).
func TestNetStatusFindsAMachineWithNoNodeAndANodeWithNoMachine(t *testing.T) {
	withTailnet(t, fakeTailnet{nodes: []TailnetNode{
		{Name: "hulk", Addr: "100.1.2.3", Online: true},
		{Name: "stranger", Addr: "100.9.9.9", Online: true},
	}})
	var out, errb bytes.Buffer
	code := FleetNetStatus(FleetNetStatusInput{
		Machines: netRegistry(t, netRowHulk, netRowStudio),
		Stdout:   &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("exit %d, want 1 (findings are the tool saying NO); stderr=%q", code, errb.String())
	}
	for _, want := range []string{
		"NET FINDING machine=studio reason=no-tailnet-node",
		"NET FINDING node=stranger reason=not-in-registry",
		"NET STATUS FINDINGS machines=2 online=1 findings=2",
	} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr does not carry %q; got:\n%s", want, errb.String())
		}
	}
}

// TestNetStatusIsSilentAboutAMachineItWasNeverToldIsOnTheTailnet is R1 reaching R2: a `lan`
// machine and a `shared-from:` machine are not expected on this tailnet, so their absence is
// not a finding. This is what keeps the verb honest for a node that has no tailnet at all.
func TestNetStatusIsSilentAboutAMachineItWasNeverToldIsOnTheTailnet(t *testing.T) {
	withTailnet(t, fakeTailnet{nodes: []TailnetNode{{Name: "hulk", Online: true}}})
	var out, errb bytes.Buffer
	code := FleetNetStatus(FleetNetStatusInput{
		Machines: netRegistry(t, netRowHulk, netRowNAS, netRowAda),
		Stdout:   &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d, want 0; a lan machine and a shared machine are not findings. stderr=%q", code, errb.String())
	}
	if strings.Contains(errb.String(), "FINDING") {
		t.Errorf("stderr carries a finding for a machine that was never said to be on this tailnet:\n%s", errb.String())
	}
	for _, want := range []string{"provider=lan", "provider=shared-from:stella"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not carry %q; got:\n%s", want, out.String())
		}
	}
}

// TestNetStatusRefusesRatherThanGuess: no registry, an unreadable one, and a tailnet that
// would not answer are each exit 2 with a reason token and a remedy.
func TestNetStatusRefusesRatherThanGuess(t *testing.T) {
	withTailnet(t, fakeTailnet{nodes: []TailnetNode{{Name: "hulk"}}})
	var out, errb bytes.Buffer
	if code := FleetNetStatus(FleetNetStatusInput{Stdout: &out, Stderr: &errb}); code != 2 {
		t.Errorf("exit %d for a missing --machines, want 2", code)
	}
	if !strings.Contains(errb.String(), "reason="+NetReasonNoMachines) {
		t.Errorf("the refusal carries no reason token: %q", errb.String())
	}

	errb.Reset()
	if code := FleetNetStatus(FleetNetStatusInput{Machines: filepath.Join(t.TempDir(), "nope.tsv"), Stdout: &out, Stderr: &errb}); code != 2 {
		t.Errorf("exit %d for an unreadable registry, want 2", code)
	}
	if !strings.Contains(errb.String(), "reason="+NetReasonUnreadable) || !strings.Contains(errb.String(), "nope.tsv") {
		t.Errorf("the refusal does not name the file and the reason: %q", errb.String())
	}

	withTailnet(t, fakeTailnet{err: errors.New("tailscaled is not running")})
	errb.Reset()
	code := FleetNetStatus(FleetNetStatusInput{Machines: netRegistry(t, netRowHulk), Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Errorf("exit %d for a tailnet that would not answer, want 2", code)
	}
	if !strings.Contains(errb.String(), "reason="+NetReasonTailnet) || !strings.Contains(errb.String(), "tailscaled is not running") {
		t.Errorf("the refusal does not carry the tailnet's own words: %q", errb.String())
	}
}

// TestNetStatusParsesTailscalesOwnStatusDocument holds the parse against a recorded
// `tailscale status --json`, so the seam's default is read off Tailscale's document rather
// than off our hope about it. The document is a fixture; nothing runs.
func TestNetStatusParsesTailscalesOwnStatusDocument(t *testing.T) {
	const doc = `{
	  "Self":  {"HostName":"studio","DNSName":"studio.tail1234.ts.net.","TailscaleIPs":["100.1.2.4"],"Online":true},
	  "Peer": {
	    "nodekey:aaa": {"HostName":"hulk","DNSName":"hulk.tail1234.ts.net.","TailscaleIPs":["100.1.2.3","fd7a::1"],"Online":true},
	    "nodekey:bbb": {"HostName":"air","DNSName":"","TailscaleIPs":["100.1.2.5"],"Online":false,"LastSeen":"2026-09-17T22:10:00Z"}
	  }
	}`
	nodes, err := parseTailscaleStatus([]byte(doc))
	if err != nil {
		t.Fatalf("parseTailscaleStatus: %v", err)
	}
	got := map[string]TailnetNode{}
	for _, n := range nodes {
		got[n.Name] = n
	}
	if len(got) != 3 {
		t.Fatalf("parsed %d nodes, want 3 (Self and two peers): %v", len(got), got)
	}
	if got["hulk"].Addr != "100.1.2.3" {
		t.Errorf("hulk's address is %q, want the first TailscaleIP", got["hulk"].Addr)
	}
	if !got["studio"].Online {
		t.Error("Self is not read as online")
	}
	if got["air"].LastSeen.Format(time.RFC3339) != "2026-09-17T22:10:00Z" {
		t.Errorf("air's last-seen is %v; a peer with no DNS name still reads by hostname", got["air"].LastSeen)
	}
}

// ---------------------------------------------------------------------------
// net init
// ---------------------------------------------------------------------------

// initNode runs `net init` into a temp directory and returns the three files' bodies.
func initNode(t *testing.T, in FleetNetInitInput) (dir string, files map[string]string, out, errb string) {
	t.Helper()
	dir = t.TempDir()
	var o, e bytes.Buffer
	in.Out, in.Stdout, in.Stderr = dir, &o, &e
	if code := FleetNetInit(in); code != 0 {
		t.Fatalf("FleetNetInit exit %d; stderr=%q", code, e.String())
	}
	files = map[string]string{}
	for _, p := range []string{NetPolicyPath, NetWorkflowPath, NetDocPath} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		files[p] = string(raw)
	}
	return dir, files, o.String(), e.String()
}

// exampleInit is the node every init test generates, roles and all.
func exampleInit(t *testing.T) FleetNetInitInput {
	return FleetNetInitInput{
		Machines: netRegistry(t, netRowHulk, netRowSpace, netRowStudio, netRowMini, netRowAir),
		Node:     "rowan", Tailnet: "example.com", Owner: "glenn@example.com",
	}
}

// TestNetInitWritesTheThreeFilesFromTheRegistry is R13's red test: one command, three files,
// generated from the registry a node already keeps. No network, no machine, no model.
func TestNetInitWritesTheThreeFilesFromTheRegistry(t *testing.T) {
	_, files, out, _ := initNode(t, exampleInit(t))
	for _, p := range []string{NetPolicyPath, NetWorkflowPath, NetDocPath} {
		if strings.TrimSpace(files[p]) == "" {
			t.Errorf("%s was written empty", p)
		}
		if !strings.Contains(out, "NET WROTE "+p) {
			t.Errorf("stdout does not name %s; got:\n%s", p, out)
		}
	}
	if !strings.Contains(out, "NET INIT OK node=rowan files=3 machines=5") {
		t.Errorf("the verdict line is missing or wrong:\n%s", out)
	}
}

// TestNetInitPolicyCarriesTheInvariantsAsTailscalesOwnTests is the security rule, and the
// one Johnny reads: the four invariants are in the policy's `tests` section, in Tailscale's
// language, so `tailscale acl test` is what checks them.
func TestNetInitPolicyCarriesTheInvariantsAsTailscalesOwnTests(t *testing.T) {
	_, files, _, _ := initNode(t, exampleInit(t))
	policy := files[NetPolicyPath]

	tests, ok := netSection(policy, "\"tests\": [")
	if !ok {
		t.Fatal("the policy has no tests section; the invariants would be a description of a rule and not the rule")
	}
	for _, want := range []struct{ what, line string }{
		// R5: a bud reaches benches and nothing else.
		{"a bud accepts ssh to a bench", `"src": "tag:bud"`},
		{"a bud is denied the coordination machine", `"tag:coordination:22"`},
		{"a bud is denied a runner host", `"tag:runner:22"`},
		// R11: services from benches on named ports only.
		{"a bench accepts the stack's named port", `"tag:services:3100"`},
		// R12: the coordination machine, from the owners only.
		{"the owners reach the coordination machine", `"src": "group:owners"`},
	} {
		if !strings.Contains(tests, want.line) {
			t.Errorf("the tests section does not assert that %s (%s):\n%s", want.what, want.line, tests)
		}
	}

	// R3: nothing reaches a runner host, and the way that is written is the ABSENCE of a
	// rule whose dst is tag:runner. An absence is only a rule if it is checked.
	acls, ok := netSection(policy, "\"acls\": [")
	if !ok {
		t.Fatal("the policy has no acls section")
	}
	for _, forbidden := range []string{`"tag:runner:*"`, `"tag:runner:22"`} {
		for _, line := range strings.Split(acls, "\n") {
			if strings.Contains(line, "\"action\": \"accept\"") && strings.Contains(line, forbidden) && !strings.Contains(line, "group:owners") {
				t.Errorf("an acl rule accepts traffic to a runner host: %q. Runner hosts are CI-only (the lock of 2026-09-18)", strings.TrimSpace(line))
			}
		}
	}

	// R12 the other way: the only accept whose dst is the coordination machine comes from
	// the node's own people.
	for _, line := range strings.Split(acls, "\n") {
		if !strings.Contains(line, "tag:coordination") || !strings.Contains(line, "accept") {
			continue
		}
		if !strings.Contains(line, "group:owners") {
			t.Errorf("something other than the node's own people reaches the coordination machine: %q", strings.TrimSpace(line))
		}
	}
}

// TestNetInitPolicyIsWrittenFromRolesAndNotFromNames: a node with different machines gets a
// policy for ITS roles, and a role nobody carries produces no tag and no rule. This is what
// makes the verb generic -- our fleet is the first node, not the only one.
func TestNetInitPolicyIsWrittenFromRolesAndNotFromNames(t *testing.T) {
	in := exampleInit(t)
	in.Machines = netRegistry(t, netRowHulk, netRowStudio) // a node with no bud, no runner, no services
	in.Node, in.Owner = "stella", "stella@example.com"
	_, files, _, _ := initNode(t, in)
	policy := files[NetPolicyPath]

	for _, absent := range []string{`"tag:bud": [`, `"tag:runner": [`, `"tag:services": [`} {
		if strings.Contains(policy, absent) {
			t.Errorf("the policy tags %q on a node that carries no such machine", absent)
		}
	}
	if !strings.Contains(policy, `"tag:bench": ["group:owners"]`) {
		t.Error("the policy does not tag the role this node does carry")
	}
	if !strings.Contains(policy, `"group:owners": ["stella@example.com"]`) {
		t.Error("the policy does not carry this node's own owner")
	}
	// And no machine NAME is a rule: a new bench needs a tag, never an edit here.
	acls, _ := netSection(policy, "\"acls\": [")
	for _, name := range []string{"hulk", "studio"} {
		for _, line := range strings.Split(acls, "\n") {
			if strings.Contains(line, "accept") && strings.Contains(line, `"`+name+`"`) {
				t.Errorf("an acl rule names the machine %s; rules are written from roles: %q", name, strings.TrimSpace(line))
			}
		}
	}
}

// TestNetInitWorkflowPinsTailscalesActionBySHAAndAppliesOnTheDefaultBranchOnly is the CI
// rule: the action is Tailscale's own, pinned by commit; a pull request TESTS and only a
// push to the default branch APPLIES; and the API key is a forge secret and nothing else.
func TestNetInitWorkflowPinsTailscalesActionBySHAAndAppliesOnTheDefaultBranchOnly(t *testing.T) {
	_, files, _, _ := initNode(t, exampleInit(t))
	wf := files[NetWorkflowPath]

	if !strings.Contains(wf, "uses: tailscale/gitops-acl-action@"+NetDefaultActionSHA) {
		t.Errorf("the workflow does not use Tailscale's own GitOps action pinned by sha:\n%s", wf)
	}
	for _, line := range strings.Split(wf, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		ref, _, _ := strings.Cut(strings.TrimSpace(strings.SplitN(line, "@", 2)[1]), " ")
		if len(ref) != 40 {
			t.Errorf("a uses: is not pinned by a 40-hex sha: %q", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(wf, "action: test") || !strings.Contains(wf, "action: apply") {
		t.Error("the workflow does not both test and apply")
	}
	testJob := netJob(wf, "acl-test:")
	applyJob := netJob(wf, "acl-apply:")
	if !strings.Contains(testJob, "github.event_name == 'pull_request'") || !strings.Contains(testJob, "action: test") {
		t.Errorf("the test job is not guarded to pull requests:\n%s", testJob)
	}
	if !strings.Contains(applyJob, "github.event_name == 'push'") || !strings.Contains(applyJob, "action: apply") {
		t.Errorf("the apply job is not guarded to a push:\n%s", applyJob)
	}
	if strings.Contains(applyJob, "pull_request") {
		t.Error("the apply job can run from a pull request; a fork's pull request must never reach a tailnet")
	}
	if !strings.Contains(wf, "${{ secrets."+NetAPIKeySecret+" }}") {
		t.Errorf("the workflow does not take the API key from the forge secret %s", NetAPIKeySecret)
	}
	if strings.Contains(wf, "tskey-") {
		t.Error("the workflow carries something shaped like a Tailscale key; the key lives in the forge and nowhere else")
	}
}

// TestNetInitIsDeterministic: the same registry generates the same bytes, so a re-run is an
// empty diff and a real change is the only thing that shows in one.
func TestNetInitIsDeterministic(t *testing.T) {
	in := exampleInit(t)
	_, first, _, _ := initNode(t, in)
	_, second, _, _ := initNode(t, in)
	for path, body := range first {
		if second[path] != body {
			t.Errorf("%s differs between two runs over the same registry; the generator is not deterministic", path)
		}
	}
}

// TestNetInitOverwritesNothingWithoutForce, and writes nothing at all when it is going to
// refuse: a refusal on the third file must not leave the first two behind.
func TestNetInitOverwritesNothingWithoutForce(t *testing.T) {
	in := exampleInit(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(NetDocPath)), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	in.Out, in.Stdout, in.Stderr = dir, &out, &errb
	if code := FleetNetInit(in); code != 2 {
		t.Fatalf("exit %d over a file that is already there, want 2", code)
	}
	if !strings.Contains(errb.String(), "reason="+NetReasonExists) || !strings.Contains(errb.String(), "--force") {
		t.Errorf("the refusal carries no reason token or no remedy: %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(NetPolicyPath))); err == nil {
		t.Error("the policy was written although the run refused; nothing is written until every destination is free")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(NetDocPath)))
	if string(raw) != "mine\n" {
		t.Error("the file that was already there was overwritten")
	}

	out.Reset()
	errb.Reset()
	in.Force = true
	if code := FleetNetInit(in); code != 0 {
		t.Fatalf("exit %d with --force, want 0; stderr=%q", code, errb.String())
	}
}

// TestNetInitRefusesEveryMissingFlagByName: every path and every name comes from a flag and
// none has a default, which is the fleet rule since the four bench scripts became verbs.
func TestNetInitRefusesEveryMissingFlagByName(t *testing.T) {
	full := exampleInit(t)
	for _, c := range []struct {
		name string
		edit func(*FleetNetInitInput)
		want string
	}{
		{"machines", func(in *FleetNetInitInput) { in.Machines = "" }, NetReasonNoMachines},
		{"node", func(in *FleetNetInitInput) { in.Node = "" }, NetReasonUnknownNode},
		{"tailnet", func(in *FleetNetInitInput) { in.Tailnet = "" }, NetReasonUnknownNode},
		{"owner", func(in *FleetNetInitInput) { in.Owner = "" }, NetReasonUnknownNode},
		{"out", func(in *FleetNetInitInput) { in.Out = "" }, NetReasonUnreadable},
	} {
		var out, errb bytes.Buffer
		in := full
		in.Out, in.Stdout, in.Stderr = t.TempDir(), &out, &errb
		c.edit(&in)
		if code := FleetNetInit(in); code != 2 {
			t.Errorf("exit %d with no --%s, want 2", code, c.name)
		}
		if !strings.Contains(errb.String(), "--"+c.name) || !strings.Contains(errb.String(), "reason="+c.want) {
			t.Errorf("the refusal for a missing --%s is %q; it must name the flag and the reason", c.name, errb.String())
		}
	}
}

// TestNetInitDocIsWrittenForAnyNode: the page explains the design in terms of ROLES, with
// this node's names as the example, so a person on another node can read it and act.
func TestNetInitDocIsWrittenForAnyNode(t *testing.T) {
	_, files, _, _ := initNode(t, exampleInit(t))
	doc := files[NetDocPath]
	for _, want := range []string{
		"One tailnet per node",
		"shared-from:",
		"provider=lan",
		"the bus is the federation",
		"MagicDNS",
		"Funnel",
		NetAPIKeySecret,
		"nova-pulse fleet net init",
		"hulk, space", // the node's own benches, as the example
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("TAILNET.md does not carry %q", want)
		}
	}
}

// TestNetNotImplementedNamesItsSpecRule: a stub that says `not-implemented` and points at
// the rule is a pointer into the spec, not an apology. Every specified-but-unwritten verb
// has one, and every one names a rule.
func TestNetNotImplementedNamesItsSpecRule(t *testing.T) {
	for _, verb := range []string{"acl", "ssh", "join", "expiry", "names", "share", "serve"} {
		var errb bytes.Buffer
		if code := FleetNetNotImplemented(verb, &errb); code != 2 {
			t.Errorf("%s exits %d, want 2", verb, code)
		}
		line := errb.String()
		if !strings.Contains(line, "NET REFUSED verb="+verb) || !strings.Contains(line, "reason="+NetReasonNotImplemented) {
			t.Errorf("%s refuses as %q", verb, line)
		}
		if !strings.Contains(line, "docs/SPEC-FLEET-NET.md") || !strings.Contains(line, netSpecRules[verb]) {
			t.Errorf("%s does not name its spec rule: %q", verb, line)
		}
		if strings.Count(strings.TrimRight(line, "\n"), "\n") != 0 {
			t.Errorf("%s printed more than one line: %q", verb, line)
		}
	}
}

// netSection returns the text from a section's opening line to the line that closes it at
// the same indentation. It is a shape read over the generated file, like the CI class tests:
// this repository carries no HuJSON parser and does not need one to assert its own output.
func netSection(src, open string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, open) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " "))
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " ")
		if strings.HasPrefix(trimmed, "]") && len(lines[i])-len(trimmed) == indent {
			return strings.Join(lines[start:i+1], "\n"), true
		}
	}
	return strings.Join(lines[start:], "\n"), true
}

// netJob returns one workflow job's text, from its key to the next key at the same depth.
func netJob(src, key string) string {
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == strings.TrimSpace(key) {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " "))
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " ")
		if trimmed == "" {
			continue
		}
		if len(lines[i])-len(trimmed) <= indent && strings.HasSuffix(trimmed, ":") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// TestNetInitLetsNothingTAGGEDReachARunnerHostAndLetsTheOwnersIn is R3 after two rulings of
// 2026-09-18, and it is the rule that was nearly written wrong twice.
//
// Glenn, travelling: "I want to work with all friends, including keeper you and all fleet
// machines from the air with no restrictions." Johnny, on the security read: "'Nothing but
// the forge' is no PEER from buds or benches. group:owners is not the swarm." So R3 is a
// rule about what the tailnet's TAGGED things may reach, and the owners reach a runner host
// on ssh -- Johnny's one accept, `src: group:owners, dst: tag:runner:22`.
func TestNetInitLetsNothingTAGGEDReachARunnerHostAndLetsTheOwnersIn(t *testing.T) {
	_, files, _, _ := initNode(t, exampleInit(t))
	acls, ok := netSection(files[NetPolicyPath], "\"acls\": [")
	if !ok {
		t.Fatal("the policy has no acls section")
	}
	owners := 0
	for _, line := range strings.Split(acls, "\n") {
		if !strings.Contains(line, "\"action\": \"accept\"") || !strings.Contains(line, "tag:runner") {
			continue
		}
		if !strings.Contains(line, "\"group:owners\"") {
			t.Errorf("something TAGGED reaches a runner host: %q. The lock of 2026-09-18 is that runner hosts are CI-only", strings.TrimSpace(line))
			continue
		}
		owners++
		// Johnny scoped it to ssh: an owner needs to administer the machine, not to reach
		// an arbitrary port on it.
		if !strings.Contains(line, "tag:runner:22") {
			t.Errorf("the owners' rule reaches a runner host on more than ssh: %q", strings.TrimSpace(line))
		}
	}
	if owners != 1 {
		t.Errorf("%d rules let the node's own people reach a runner host, want exactly 1", owners)
	}

	tests, _ := netSection(files[NetPolicyPath], "\"tests\": [")
	// The owner test must PASS for a runner host...
	if !strings.Contains(tests, `"src": "group:owners"`) || !strings.Contains(tests, `"tag:runner:22", "tag:services:22"`) {
		t.Errorf("no test asserts that the node's own people reach a runner host on ssh:\n%s", tests)
	}
	// ...and the tagged ones must FAIL for it, and for the coordination machine.
	for _, want := range []string{
		`"src": "tag:bud"`,
		`"src": "tag:bench"`,
	} {
		if !strings.Contains(tests, want) {
			t.Errorf("the tests section has no entry for %s", want)
		}
	}
	for _, entry := range netTestEntries(tests) {
		if !strings.Contains(entry, `"src": "tag:bud"`) && !strings.Contains(entry, `"src": "tag:bench"`) {
			continue
		}
		if !strings.Contains(entry, "tag:runner:22") || !strings.Contains(entry, `"deny"`) {
			t.Errorf("a tagged source is not denied a runner host:\n%s", entry)
		}
		if strings.Contains(entry, `"src": "tag:bench"`) && strings.Contains(entry, `"accept"`) &&
			!strings.Contains(entry, "tag:coordination:22") {
			t.Errorf("a bench is not denied the coordination machine:\n%s", entry)
		}
	}
}

// TestNetInitAdvertisesTagRunnerOnlyForARunnerOnlyMachine is Johnny's shape, 2026-09-18:
// "dual-tag is a hole, not a note ... one reach-role per machine. A host that runs CI on a
// bench is tag:bench only. CI is a process, not a tag." A machine that advertised both tags
// would be reachable as a bench, which makes the R3 denial false for that host however
// firmly the policy states it.
func TestNetInitAdvertisesTagRunnerOnlyForARunnerOnlyMachine(t *testing.T) {
	reg, err := fleet.ReadRegistry("../fleet/testdata/machines.tsv")
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	for _, m := range reg.Machines() {
		tags := NetMachineTags(m)
		hasRunnerTag := false
		for _, tag := range tags {
			if tag == "tag:"+fleet.RoleRunner {
				hasRunnerTag = true
			}
		}
		if hasRunnerTag && len(tags) != 1 {
			t.Errorf("%s advertises %v: a machine carrying tag:runner beside another tag is reachable through the other tag, and R3 is false for it", m.Name, tags)
		}
		if m.HasRole(fleet.RoleRunner) && m.HasRole(fleet.RoleBench) && hasRunnerTag {
			t.Errorf("%s is a shared bench-and-runner host and still advertises tag:runner; CI is a process on it, not a way of reaching it", m.Name)
		}
		if len(tags) == 0 {
			t.Errorf("%s advertises no tag at all", m.Name)
		}
	}
}

// netTestEntries splits the tests section into one string per `{...}` entry, so an assertion
// can be made about ONE entry rather than about the whole section -- which is what let an
// earlier version of this test pass on a deny that belonged to a different source.
func netTestEntries(section string) []string {
	var out []string
	var cur []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			cur = []string{line}
			continue
		}
		if cur != nil {
			cur = append(cur, line)
			if strings.HasSuffix(trimmed, "},") {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
		}
	}
	return out
}

// TestNetInitDocNamesTheTagEveryMachineAdvertises: the policy is written about tags, so the
// page has to say which tags each machine carries -- otherwise a correct policy and a
// wrongly-joined machine look the same from here.
func TestNetInitDocNamesTheTagEveryMachineAdvertises(t *testing.T) {
	_, files, _, _ := initNode(t, exampleInit(t))
	doc := files[NetDocPath]
	for _, want := range []string{
		"| `hulk` | `tailnet` | `tag:bench` |",
		"| `space` | `tailnet` | `tag:bench,tag:services` |",
		"| `air` | `tailnet` | `tag:bud` |",
		"| `mini` | `tailnet` | `tag:runner` |",
		"advertise-tags",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("TAILNET.md does not carry %q", want)
		}
	}
}
