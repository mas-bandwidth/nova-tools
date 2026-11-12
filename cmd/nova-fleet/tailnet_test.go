package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

// std is a HuJSON policy as decoded JSON.
func std(t *testing.T, b []byte) map[string]any {
	t.Helper()
	j, err := hujson.Standardize(slices.Clone(b))
	if err != nil {
		t.Fatalf("not HuJSON: %v\n%s", err, b)
	}
	m := map[string]any{}
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The default policy of a new tailnet, as the admin console holds it: comments, trailing commas, allow all.
const allowAll = `// Example/default ACLs for unrestricted connections.
{
	// Declare static groups of users. Use autogroup:admin for the admins.
	// "groups": {"group:example": ["alice@example.com"]},

	// Allow all connections.
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],

	// Test access rules every time they're saved.
	"ssh": [
		{
			"action": "check",
			"src":    ["autogroup:member"],
			"dst":    ["autogroup:self"],
			"users":  ["autogroup:nonroot", "root"],
		},
	],
	"nodeAttrs": [{"target": ["autogroup:member"], "attr": ["funnel"]}],
}
`

// A policy built on autogroup:self: each member reaches only their own devices, so a tagged device is
// reached by nobody unless the merge says so.
const selfOnly = `{
	"grants": [{"src": ["autogroup:member"], "dst": ["autogroup:self"], "ip": ["*"]}], // my own devices only
	"ssh": [{"action": "check", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["autogroup:nonroot", "root"]}],
}`

func TestFleetPolicyIsTheEmbeddedFile(t *testing.T) {
	want := fleetPolicy()
	owners := want["tagOwners"].(map[string]any)
	if canon(owners["tag:nova"]) != `["autogroup:admin"]` || canon(owners["tag:control"]) != `["autogroup:admin"]` ||
		len(want["grants"].([]any)) != 4 || len(want["ssh"].([]any)) != 3 {
		t.Errorf("fleetPolicy() = %s", canon(want))
	}
}

func merge(t *testing.T, src string) ([]byte, bool) {
	t.Helper()
	out, changed, err := mergePolicy([]byte(src), fleetPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return out, changed
}

func TestMergePolicy(t *testing.T) {
	want := fleetPolicy()
	// empty: everything the fleet needs is added
	for _, src := range []string{"", "{}", "// nothing yet\n{}\n"} {
		if got, changed := merge(t, src); !changed || canon(std(t, got)) != canon(want) {
			t.Errorf("%q: changed %v\n got %s\nwant %s", src, changed, got, canon(want))
		}
	}
	// the default allow-all: every comment and line of it survives, its acls, ssh rule and the rest are
	// as they were, the fleet's appended
	got, changed := merge(t, allowAll)
	have, m := std(t, []byte(allowAll)), std(t, got)
	if !changed || canon(m["acls"]) != canon(have["acls"]) || canon(m["nodeAttrs"]) != canon(have["nodeAttrs"]) {
		t.Errorf("allow-all: acls or nodeAttrs changed:\n%s", got)
	}
	for _, line := range strings.Split(allowAll, "\n")[:9] {
		if !strings.Contains(string(got), line) {
			t.Errorf("allow-all: the line %q is gone:\n%s", line, got)
		}
	}
	if ssh := m["ssh"].([]any); len(ssh) != 4 || canon(ssh[0]) != canon(have["ssh"].([]any)[0]) || canon(ssh[1:]) != canon(want["ssh"]) {
		t.Errorf("allow-all: ssh = %s", canon(ssh))
	}
	// idempotent: the merged policy merges to itself, byte for byte
	if again, changed := merge(t, string(got)); changed || string(again) != string(got) {
		t.Errorf("a second merge changed %v:\n%s", changed, again)
	}
	// existing tags and an equal rule (keys in another order): owners added, nothing duplicated or removed
	src := `{"tagOwners":{"tag:nova":["alice@example.com"],"tag:other":["bob@example.com"]},
		"grants":[{"ip":["tcp:22","tcp:6379","tcp:5432"],"dst":["tag:nova","tag:runner"],"src":["tag:nova","tag:runner"]},{"src":["*"],"dst":["*"],"ip":["*"]}]}`
	got, changed = merge(t, src)
	m = std(t, got)
	owners := m["tagOwners"].(map[string]any)
	if !changed || canon(owners["tag:nova"]) != `["alice@example.com","autogroup:admin"]` || canon(owners["tag:other"]) != `["bob@example.com"]` ||
		canon(owners["tag:control"]) != `["autogroup:admin"]` {
		t.Errorf("tagOwners = %s", canon(owners))
	}
	if g := m["grants"].([]any); len(g) != 5 || canon(g[1]) != `{"dst":["*"],"ip":["*"],"src":["*"]}` || canon(g[2:]) != canon(want["grants"].([]any)[1:]) {
		t.Errorf("grants = %s", canon(g))
	}
	if _, changed := merge(t, string(got)); changed {
		t.Error("a second merge over existing tags changed something")
	}
	if _, _, err := mergePolicy([]byte("{"), want); err == nil {
		t.Error("a policy that does not parse: want an error")
	}
}

// The fleet's own rules. Every source is a fleet tag or the tailnet's admins and every destination a fleet tag, so
// nothing is opened to an untagged or member device; no tagged source gets root; and nothing reaches the control node
// without a person: no grant opens its port 22 to a tag, and every ssh rule into it is a non-root check.
func TestFleetPolicyGuardsRootAndTheControlNode(t *testing.T) {
	has := func(list any, v string) bool { l, _ := list.([]any); return slices.Contains(l, any(v)) }
	isTag := func(v any) bool { s, _ := v.(string); return strings.HasPrefix(s, "tag:") }
	p := fleetPolicy()
	for _, kind := range []string{"grants", "ssh"} {
		for _, r := range p[kind].([]any) {
			g := r.(map[string]any)
			src, dst := g["src"].([]any), g["dst"].([]any)
			for _, s := range src {
				if !isTag(s) && s != "autogroup:admin" {
					t.Errorf("%s %s: source %v is neither a fleet tag nor the admins", kind, canon(g), s)
				}
			}
			for _, d := range dst {
				if !isTag(d) {
					t.Errorf("%s %s: destination %v is not a fleet tag", kind, canon(g), d)
				}
			}
			if kind == "grants" {
				if has(dst, "tag:control") && slices.ContainsFunc(src, isTag) && (has(g["ip"], "tcp:22") || has(g["ip"], "*")) {
					t.Errorf("grant %s opens the control node's port 22 to a tag", canon(g))
				}
				continue
			}
			if slices.ContainsFunc(src, isTag) && has(g["users"], "root") {
				t.Errorf("ssh %s: a tagged source gets root", canon(g))
			}
			if has(dst, "tag:control") && (g["action"] != "check" || has(g["users"], "root")) {
				t.Errorf("ssh %s: into the control node other than a non-root check", canon(g))
			}
		}
	}
}

// The merge over the policy main shipped (testdata, main at ee936b822), the policy a fleet tailnet holds today. The
// merge only adds rules, so a rule main had that this file changed would stay in force ahead of its replacement: the
// merged ssh rules and grants must be exactly the file's, in its order. Among them, no tag source reaches the control
// node or gets root, and the admins reach the control node only by a non-root check.
func TestMergeOverMainsPolicy(t *testing.T) {
	has := func(list any, v string) bool { l, _ := list.([]any); return slices.Contains(l, any(v)) }
	isTag := func(v any) bool { s, _ := v.(string); return strings.HasPrefix(s, "tag:") }
	src, err := os.ReadFile("testdata/tailscale-acl-main-ee936b822.json")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := merge(t, string(src))
	m, want := std(t, got), fleetPolicy()
	for _, kind := range []string{"ssh", "grants"} {
		if canon(m[kind]) != canon(want[kind]) {
			t.Errorf("merged over main, %s is not the file's:\n got %s\nwant %s", kind, canon(m[kind]), canon(want[kind]))
		}
	}
	adminIntoControl := 0
	for _, r := range m["ssh"].([]any) {
		g := r.(map[string]any)
		tagSrc := slices.ContainsFunc(g["src"].([]any), isTag)
		if tagSrc && has(g["dst"], "tag:control") {
			t.Errorf("merged over main: a tag source into tag:control: %s", canon(g))
		}
		if tagSrc && has(g["users"], "root") {
			t.Errorf("merged over main: a tag source gets root: %s", canon(g))
		}
		if has(g["src"], "autogroup:admin") && has(g["dst"], "tag:control") {
			adminIntoControl++
			if g["action"] != "check" || has(g["users"], "root") {
				t.Errorf("merged over main: the admins into tag:control other than a non-root check: %s", canon(g))
			}
		}
	}
	if adminIntoControl == 0 {
		t.Error("merged over main: no rule lets the admins into tag:control after a check")
	}
}

// After the merge the tailnet's admins still reach every tagged machine, on every port and over Tailscale SSH as
// root too, whether the policy allowed all or only each member's own devices.
func TestMergeKeepsAdminAccess(t *testing.T) {
	has := func(list any, v string) bool { l, _ := list.([]any); return slices.Contains(l, any(v)) }
	for name, src := range map[string]string{"allow-all": allowAll, "autogroup:self": selfOnly} {
		got, _ := merge(t, src)
		m := std(t, got)
		for _, tag := range []string{"tag:nova", "tag:runner", "tag:control"} {
			reach := slices.ContainsFunc(m["grants"].([]any), func(r any) bool {
				g := r.(map[string]any)
				return has(g["src"], "autogroup:admin") && has(g["dst"], tag) && has(g["ip"], "*")
			})
			if !reach {
				t.Errorf("%s: no grant lets autogroup:admin reach %s on every port:\n%s", name, tag, got)
			}
			if tag == "tag:control" {
				continue
			}
			ssh := slices.ContainsFunc(m["ssh"].([]any), func(r any) bool {
				g := r.(map[string]any)
				return g["action"] == "accept" && has(g["src"], "autogroup:admin") && has(g["dst"], tag) && has(g["users"], "root") && has(g["users"], "autogroup:nonroot")
			})
			if !ssh {
				t.Errorf("%s: no ssh rule lets autogroup:admin in to %s as root and nonroot:\n%s", name, tag, got)
			}
		}
	}
	// allow-all keeps its own rule too: everyone still reaches everything
	got, _ := merge(t, allowAll)
	if canon(std(t, got)["acls"]) != `[{"action":"accept","dst":["*:*"],"src":["*"]}]` {
		t.Errorf("allow-all rule changed:\n%s", got)
	}
}

// Every fleet machine reaches every machine but the control node over Tailscale SSH whatever the policy was built on:
// each pair has a grant on tcp:22 and an accepting ssh rule as a non-root login (nova-fleet ssh-names checks the
// pairs). Into the control node, no tag has port 22 or an ssh rule, and the tailnet's admins get it as a non-root
// login after a check, never as root.
func TestMergeLetsEveryFleetTagSSHToEveryOther(t *testing.T) {
	has := func(list any, v string) bool { l, _ := list.([]any); return slices.Contains(l, any(v)) }
	for name, src := range map[string]string{"allow-all": allowAll, "autogroup:self": selfOnly} {
		got, _ := merge(t, src)
		m := std(t, got)
		for _, from := range []string{"tag:nova", "tag:runner", "tag:control"} {
			for _, to := range []string{"tag:nova", "tag:runner"} {
				port := slices.ContainsFunc(m["grants"].([]any), func(r any) bool {
					g := r.(map[string]any)
					return has(g["src"], from) && has(g["dst"], to) && (has(g["ip"], "tcp:22") || has(g["ip"], "*"))
				})
				ssh := slices.ContainsFunc(m["ssh"].([]any), func(r any) bool {
					g := r.(map[string]any)
					return g["action"] == "accept" && has(g["src"], from) && has(g["dst"], to) && has(g["users"], "autogroup:nonroot")
				})
				if !port || !ssh {
					t.Errorf("%s: %s cannot ssh to %s (grant on 22: %v, ssh rule: %v):\n%s", name, from, to, port, ssh, got)
				}
			}
		}
		for _, from := range []string{"tag:nova", "tag:runner"} {
			port := slices.ContainsFunc(m["grants"].([]any), func(r any) bool {
				g := r.(map[string]any)
				return has(g["src"], from) && has(g["dst"], "tag:control") && (has(g["ip"], "tcp:22") || has(g["ip"], "*"))
			})
			ssh := slices.ContainsFunc(m["ssh"].([]any), func(r any) bool {
				g := r.(map[string]any)
				return has(g["src"], from) && has(g["dst"], "tag:control")
			})
			if port || ssh {
				t.Errorf("%s: %s reaches the control node without a person (grant on 22: %v, ssh rule: %v):\n%s", name, from, port, ssh, got)
			}
		}
		adminRootOnControl := slices.ContainsFunc(m["ssh"].([]any), func(r any) bool {
			g := r.(map[string]any)
			return has(g["src"], "autogroup:admin") && has(g["dst"], "tag:control") && has(g["users"], "root")
		})
		adminOnControl := slices.ContainsFunc(m["ssh"].([]any), func(r any) bool {
			g := r.(map[string]any)
			return g["action"] == "check" && has(g["src"], "autogroup:admin") && has(g["dst"], "tag:control") && has(g["users"], "autogroup:nonroot")
		})
		if adminRootOnControl || !adminOnControl {
			t.Errorf("%s: admins on the control node: as root %v (want false), as a non-root login after a check %v (want true)", name, adminRootOnControl, adminOnControl)
		}
	}
}

func TestPolicyRetriesOnce(t *testing.T) {
	a := &fakeAPI{policy: allowAll, conflict: 1}
	if got, err := policy(a, false); err != nil || got != "merged" {
		t.Fatalf("policy = %q %v", got, err)
	}
	var methods []string
	for _, c := range a.calls {
		w := strings.Fields(c)
		methods = append(methods, strings.Join(w[:min(3, len(w))], " "))
	}
	if !slices.Equal(methods, []string{`GET /tailnet/-/acl`, `POST /tailnet/-/acl "e0"`, `GET /tailnet/-/acl`, `POST /tailnet/-/acl "e1"`}) {
		t.Errorf("calls = %q", methods)
	}
	if !strings.Contains(a.policy, "// Allow all connections.") {
		t.Errorf("the policy written lost its comments:\n%s", a.policy)
	}
	if _, changed := merge(t, a.policy); changed {
		t.Error("the policy written is not the merge")
	}
	// a second run: one GET, nothing written
	a.calls = nil
	if got, err := policy(a, false); err != nil || got != "unchanged" || len(a.calls) != 1 {
		t.Errorf("second run: %q %v calls %q", got, err, a.calls)
	}
	// two 412s in a row: refused, after one retry
	a = &fakeAPI{policy: "{}", conflict: 2}
	if _, err := policy(a, false); err == nil || !strings.Contains(err.Error(), "HTTP 412") || len(a.calls) != 4 {
		t.Errorf("two conflicts: %v calls %q", err, a.calls)
	}
	// --check: GETs only
	a = &fakeAPI{policy: "{}"}
	if got, err := policy(a, true); err != nil || got != "would-merge" || len(a.calls) != 1 {
		t.Errorf("check: %q %v calls %q", got, err, a.calls)
	}
}

// A peer is the machine by its MagicDNS label, never by HostName; two with the label are refused.
func TestPeerByMagicDNSLabel(t *testing.T) {
	ip := []string{"100.64.0.1"}
	st := tsStatus{Peer: map[string]tsNode{
		"a": {ID: "n1", HostName: "web-1", DNSName: "laptop.tail.ts.net.", TailscaleIPs: ip},
		"b": {ID: "n2", HostName: "laptop", DNSName: "web-1.tail.ts.net.", TailscaleIPs: ip},
	}}
	if p, ok, err := st.peer("web-1"); err != nil || !ok || p.ID != "n2" {
		t.Errorf("web-1 = %v %v %v, want n2 (its DNS label), not the HostName match n1", p.ID, ok, err)
	}
	if _, ok, err := st.peer("web"); ok || err != nil {
		t.Errorf("a label prefix matched: %v %v", ok, err)
	}
	st.Peer["c"] = tsNode{ID: "n3", DNSName: "web-1.other.ts.net.", TailscaleIPs: ip}
	if _, ok, err := st.peer("web-1"); ok || err == nil || err.Error() != "2 peers have the MagicDNS label web-1 (n2=web-1.tail.ts.net, n3=web-1.other.ts.net); remove or rename all but the machine's own in the admin console" {
		t.Errorf("two peers: %v %v", ok, err)
	}
	// up refuses at the tailnet step, tagging nothing
	f := &upFake{peers: map[string]string{"first": "100.64.0.2"}}
	f.install(t)
	old := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if strings.Join(args, " ") == "status --json" {
			return `{"BackendState":"Running","Self":{"ID":"n-control","Tags":["tag:control"]},"Peer":{"a":{"ID":"n1","DNSName":"first.ts.example.","TailscaleIPs":["100.64.0.2"]},"b":{"ID":"n2","DNSName":"first.ts.other.","TailscaleIPs":["100.64.0.3"]}}}`, "", 0
		}
		return old(bin, args, env)
	}
	code, _, errs := runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 1 || !strings.Contains(errs, "2 peers have the MagicDNS label first (n1=first.ts.example, n2=first.ts.other)") || strings.Contains(strings.Join(f.api.calls, "\n"), "/device/") {
		t.Errorf("ambiguous peer: code %d %s\n%v", code, errs, f.api.calls)
	}
}

// up's tailnet step: the control node tagged, a machine on the tailnet retagged, a key minted for each
// machine that is not, each key reaching only its own play's environment; no secret on any printed line.
func TestUpTailnet(t *testing.T) {
	f := &upFake{untagged: true, peers: map[string]string{"second": "100.64.0.2"}, api: &fakeAPI{policy: allowAll}}
	f.install(t)
	code, out, errs := runArgs("up", "--tools", "v2.0.0", "--runners", "https://github.com/o/r", "first=203.0.113.7", "second=203.0.113.8", "third=203.0.113.9")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "UP TAILNET OK policy=merged control=tagged keys=2 retagged=1\n") || strings.Index(out, "UP TAILNET") > strings.Index(out, "UP REACH") {
		t.Errorf("stdout:\n%s", out)
	}
	api := strings.Join(f.api.calls, "\n")
	for _, w := range []string{`POST /device/n-control/tags  {"tags":["tag:control"]}`, `POST /device/n-second/tags  {"tags":["tag:nova","tag:runner"]}`,
		`POST /tailnet/-/keys  {"capabilities":{"devices":{"create":{"ephemeral":false,"preauthorized":true,"reusable":false,"tags":["tag:nova","tag:runner"]}}},"description":"nova-fleet up","expirySeconds":3600}`} {
		if !strings.Contains(api, w) {
			t.Errorf("no API call %q:\n%s", w, api)
		}
	}
	if f.api.keys != 2 {
		t.Errorf("keys minted = %d, want 2 (first, third)", f.api.keys)
	}
	all := strings.Join(f.calls, "\n")
	if strings.Count(all, "NOVA_FLEET_AUTHKEY=") != 2 || !strings.Contains(all, "tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false [env NOVA_FLEET_AUTHKEY="+fakeKey("auth", "SECRET1")+"]") ||
		!strings.Contains(all, "nova_join_host=third") || !strings.Contains(all, "[env NOVA_FLEET_AUTHKEY="+fakeKey("auth", "SECRET2")+"]") {
		t.Errorf("the keys' plays:\n%s", all)
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Split(c, " [env ")[0], "SECRET") {
			t.Errorf("a secret on a command line: %s", c)
		}
	}
	if strings.Contains(out+errs, "SECRET") || strings.Contains(out+errs, "tskey-") {
		t.Errorf("a secret printed:\n%s\n%s", out, errs)
	}
	// the same run again: the policy and the tags are as wanted, every machine is on the tailnet
	f.api.calls, f.untagged = nil, false
	for _, n := range []string{"first", "second", "third"} {
		f.peers[n] = "100.64.0.9"
	}
	if code, out, _ = runArgs("up", "--tools", "v2.0.0", "first=203.0.113.7"); code != 0 || !strings.Contains(out, "UP TAILNET OK policy=unchanged control=already keys=0 retagged=0\n") {
		t.Errorf("second run: code %d\n%s", code, out)
	}
	for _, c := range f.api.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("second run wrote: %s", c)
		}
	}
}

func TestUpTailnetCheckAndRefusals(t *testing.T) {
	f := &upFake{untagged: true, api: &fakeAPI{policy: allowAll}}
	f.install(t)
	code, out, errs := runArgs("up", "--check", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 0 || !strings.Contains(out, "UP TAILNET WOULD policy=would-merge control=would-tag keys=1 retagged=0 (check: GETs only, nothing changed)\n") {
		t.Errorf("check: code %d\n%s\n%s", code, out, errs)
	}
	for _, c := range f.api.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("--check called %s", c)
		}
	}
	// this node off the tailnet: a tag:control key, tailscale up with it on stdin from the environment (sudo on Linux)
	oldOS := goos
	t.Cleanup(func() { goos = oldOS })
	goos = "linux"
	f = &upFake{offnet: true}
	f.install(t)
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	all := strings.Join(f.calls, "\n")
	if code != 0 || !strings.Contains(out, "CHECK NOTE tailnet") || !strings.Contains(out, "UP TAILNET OK policy=merged control=joined keys=1") ||
		!strings.Contains(all, `sh -c printf %s "$TAILSCALE_AUTHKEY" | sudo "$0" up --reset --auth-key=file:/dev/stdin --advertise-tags=tag:control /bin/tailscale [env TAILSCALE_AUTHKEY=`+fakeKey("auth", "SECRET1")+`]`) ||
		!strings.Contains(strings.Join(f.api.calls, "\n"), `"tags":["tag:control"]`) {
		t.Errorf("offnet: code %d\n%s\n%s\n%s", code, out, errs, all)
	}
	// a value that is not one line, not an API key: refused, the value never printed
	f = &upFake{}
	f.install(t)
	old := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if strings.HasSuffix(strings.Join(args, " "), `printf %s "$TAILSCALE_API_KEY"`) {
			return fakeKey("auth", "SECRET") + "\n", "", 0
		}
		return old(bin, args, env)
	}
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 1 || !strings.Contains(errs, "TAILSCALE_API_KEY cannot be read as one line "+fakeKey("api", "")+"...") || !strings.Contains(errs, "Generate API access token") || !strings.Contains(errs, "UP tailnet FAILED") ||
		strings.Contains(out+errs, "SECRET") || len(f.plays()) != 0 {
		t.Errorf("bad value: code %d\n%s\n%s", code, out, errs)
	}
	// the API refuses: the run stops at the tailnet with its answer
	f = &upFake{api: &fakeAPI{policy: "{}", conflict: 9}}
	f.install(t)
	if code, _, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1"); code != 1 || !strings.Contains(errs, "POST /tailnet/-/acl: HTTP 412") || len(f.plays()) != 0 {
		t.Errorf("refused: code %d %s", code, errs)
	}
	// an expired token: the line says how to make a new one
	f = &upFake{api: &fakeAPI{policy: "{}", expired: true}}
	f.install(t)
	if code, _, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1"); code != 1 || !strings.Contains(errs, "GET /tailnet/-/acl: HTTP 401: TAILSCALE_API_KEY was refused; an API access token expires (90 days by default): make a new one in the Tailscale admin console > Settings > Keys > Generate API access token, then: nova-secrets set TAILSCALE_API_KEY --as <seat>") {
		t.Errorf("expired: code %d %s", code, errs)
	}
}
