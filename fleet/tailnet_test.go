package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The default policy of a new tailnet: allow all, an ssh check rule for members, no tags.
const allowAll = `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],
	"ssh":[{"action":"check","src":["autogroup:member"],"dst":["autogroup:self"],"users":["autogroup:nonroot","root"]}],
	"nodeAttrs":[{"target":["autogroup:member"],"attr":["funnel"]}]}`

func TestFleetPolicyIsTheEmbeddedFile(t *testing.T) {
	want := fleetPolicy()
	owners := want["tagOwners"].(map[string]any)
	if canon(owners["tag:nova"]) != `["autogroup:admin"]` || canon(owners["tag:control"]) != `["autogroup:admin"]` ||
		len(want["grants"].([]any)) != 2 || len(want["ssh"].([]any)) != 1 {
		t.Errorf("fleetPolicy() = %s", canon(want))
	}
}

func TestMergePolicy(t *testing.T) {
	want := fleetPolicy()
	// empty: everything the fleet needs is added
	got, changed := mergePolicy(map[string]any{}, want)
	if !changed || canon(got) != canon(want) {
		t.Errorf("empty: changed %v\n got %s\nwant %s", changed, canon(got), canon(want))
	}
	// the default allow-all: its acls, ssh rule and the rest are kept as they were, the fleet's appended
	have := decode(t, allowAll)
	got, changed = mergePolicy(have, want)
	if !changed || canon(got["acls"]) != canon(have["acls"]) || canon(got["nodeAttrs"]) != canon(have["nodeAttrs"]) {
		t.Errorf("allow-all: acls or nodeAttrs changed: %s", canon(got))
	}
	if ssh := got["ssh"].([]any); len(ssh) != 2 || canon(ssh[0]) != canon(have["ssh"].([]any)[0]) || canon(ssh[1]) != canon(want["ssh"].([]any)[0]) {
		t.Errorf("allow-all: ssh = %s", canon(ssh))
	}
	if canon(have) != canon(decode(t, allowAll)) {
		t.Error("the policy given was changed in place")
	}
	// idempotent: the merged policy merges to itself
	if again, changed := mergePolicy(got, want); changed || canon(again) != canon(got) {
		t.Errorf("a second merge changed %v: %s", changed, canon(again))
	}
	// existing tags and an equal rule (keys in another order): owners added, nothing duplicated or removed
	have = decode(t, `{"tagOwners":{"tag:nova":["alice@example.com"],"tag:other":["bob@example.com"]},
		"grants":[{"ip":["tcp:22","tcp:6379","tcp:5432"],"dst":["tag:nova","tag:runner"],"src":["tag:nova","tag:runner"]},{"src":["*"],"dst":["*"],"ip":["*"]}]}`)
	got, changed = mergePolicy(have, want)
	owners := got["tagOwners"].(map[string]any)
	if !changed || canon(owners["tag:nova"]) != `["alice@example.com","autogroup:admin"]` || canon(owners["tag:other"]) != `["bob@example.com"]` ||
		canon(owners["tag:control"]) != `["autogroup:admin"]` {
		t.Errorf("tagOwners = %s", canon(owners))
	}
	if g := got["grants"].([]any); len(g) != 3 || canon(g[1]) != `{"dst":["*"],"ip":["*"],"src":["*"]}` || canon(g[2]) != canon(want["grants"].([]any)[1]) {
		t.Errorf("grants = %s", canon(g))
	}
	if _, changed := mergePolicy(got, want); changed {
		t.Error("a second merge over existing tags changed something")
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
	if _, changed := mergePolicy(decode(t, a.policy), fleetPolicy()); changed {
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
	if strings.Count(all, "TAILSCALE_AUTHKEY=") != 2 || !strings.Contains(all, "tailscale.yml -e nova_tailscale_record_address=false -e nova_firewall=false [env TAILSCALE_AUTHKEY=tskey-auth-SECRET1]") ||
		!strings.Contains(all, "nova_join_host=third") || !strings.Contains(all, "[env TAILSCALE_AUTHKEY=tskey-auth-SECRET2]") {
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
		!strings.Contains(all, `sh -c printf %s "$TAILSCALE_AUTHKEY" | sudo "$0" up --reset --auth-key=file:/dev/stdin --advertise-tags=tag:control /bin/tailscale [env TAILSCALE_AUTHKEY=tskey-auth-SECRET1]`) ||
		!strings.Contains(strings.Join(f.api.calls, "\n"), `"tags":["tag:control"]`) {
		t.Errorf("offnet: code %d\n%s\n%s\n%s", code, out, errs, all)
	}
	// a value that is not one line tskey-api-...: refused, the value never printed
	f = &upFake{}
	f.install(t)
	old := outCmd
	outCmd = func(bin string, args, env []string) (string, string, int) {
		if strings.HasSuffix(strings.Join(args, " "), `printf %s "$TAILSCALE_API_KEY"`) {
			return "tskey-auth-SECRET\n", "", 0
		}
		return old(bin, args, env)
	}
	code, out, errs = runArgs("up", "--tools", "v2.0.0", "first=1.1.1.1")
	if code != 1 || !strings.Contains(errs, "TAILSCALE_API_KEY cannot be read as one line tskey-api-...") || !strings.Contains(errs, "Generate API access token") || !strings.Contains(errs, "UP tailnet FAILED") ||
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
