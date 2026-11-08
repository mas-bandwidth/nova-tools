package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

// tsAPI is the Tailscale API v2 (tailnet "-", the API access token's own): one request in a content type
// (application/json, or application/hujson for the policy), its status, its ETag and its body. Unit tests
// fake it; tsHTTP is the real one.
type tsAPI interface {
	call(method, path, ctype, ifMatch string, body []byte) (int, string, []byte, error)
}

// tsBase is where the API is; the functional tier points it at a local stand-in.
var tsBase = "https://api.tailscale.com"

// newTailnet is the API seam: unit tests replace it.
var newTailnet = func(key string) tsAPI { return tsHTTP(key) }

// tsHTTP sends each request with the API access token as Bearer auth.
type tsHTTP string

// newKey is how to replace a token that expired or was revoked.
const newKey = "make a new one in the Tailscale admin console > Settings > Keys > Generate API access token, then: nova-secrets set TAILSCALE_API_KEY --as <seat> (the token on stdin)"

func (key tsHTTP) call(method, path, ctype, ifMatch string, body []byte) (int, string, []byte, error) {
	req, err := http.NewRequest(method, tsBase+"/api/v2"+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("Accept", ctype)
	req.Header.Set("Content-Type", ctype)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, resp.Header.Get("ETag"), b, err
}

// apiDo sends one request and wants 200; anything else is an error naming the request and the API's answer.
func apiDo(api tsAPI, method, path, ctype, ifMatch string, body []byte) (int, string, []byte, error) {
	code, etag, b, err := api.call(method, path, ctype, ifMatch, body)
	switch {
	case err == nil && code == 401:
		err = fmt.Errorf("%s %s: HTTP 401: TAILSCALE_API_KEY was refused; an API access token expires (90 days by default): %s", method, path, newKey)
	case err == nil && code != 200:
		err = fmt.Errorf("%s %s: HTTP %d %s", method, path, code, oneLine(string(b)))
	}
	return code, etag, b, err
}

// fleetPolicy is what the fleet needs of the tailnet policy: the embedded tailscale-acl.json (cmd/nova-fleet/README.md, "tailscale").
func fleetPolicy() (m map[string]any) {
	b, _ := fs.ReadFile(tree, "tailscale-acl.json") // ignored: embedded; a bad file panics below
	if err := json.Unmarshal(b, &m); err != nil {
		panic("tailscale-acl.json: " + err.Error())
	}
	return m
}

func canon(v any) string { b, _ := json.Marshal(v); return string(b) } // ignored: decoded JSON always marshals

// policyPatch is the JSON Patch (RFC 6902) that adds the fleet's needs (want) to the policy have: each owner of
// want's tagOwners a tag lacks, and each grant and ssh rule of want with no equal rule in have, appended. It
// removes and replaces nothing, so a rule the policy had (an "acls" accept * to *:*, say) still holds.
func policyPatch(have, want map[string]any) (ops []map[string]any) {
	add := func(path string, v any) { ops = append(ops, map[string]any{"op": "add", "path": path, "value": v}) }
	owners, ok := have["tagOwners"].(map[string]any)
	if !ok {
		add("/tagOwners", map[string]any{})
	}
	wantOwners := want["tagOwners"].(map[string]any)
	for _, tag := range slices.Sorted(maps.Keys(wantOwners)) {
		list, ok := owners[tag].([]any)
		if !ok {
			add("/tagOwners/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(tag), wantOwners[tag])
			continue
		}
		for _, w := range wantOwners[tag].([]any) {
			if !slices.Contains(list, w) {
				add("/tagOwners/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(tag)+"/-", w)
			}
		}
	}
	for _, k := range []string{"grants", "ssh"} {
		rules, ok := have[k].([]any)
		for _, r := range want[k].([]any) {
			if !slices.ContainsFunc(rules, func(x any) bool { return canon(x) == canon(r) }) {
				if !ok {
					add("/"+k, []any{})
					ok = true
				}
				add("/"+k+"/-", r)
			}
		}
	}
	return ops
}

// mergePolicy is the HuJSON policy src with policyPatch applied by hujson, so its comments and formatting
// survive; it reports whether anything was added.
func mergePolicy(src []byte, want map[string]any) ([]byte, bool, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		src = []byte("{}")
	}
	v, err := hujson.Parse(src)
	if err != nil {
		return nil, false, fmt.Errorf("the tailnet policy is not HuJSON: %v", err)
	}
	std, _ := hujson.Standardize(bytes.Clone(src)) // ignored: it parsed above; a copy, as Standardize writes over its input
	have := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.UseNumber()
	if err := dec.Decode(&have); err != nil {
		return nil, false, fmt.Errorf("the tailnet policy is not an object: %v", err)
	}
	ops := policyPatch(have, want)
	if len(ops) == 0 {
		return src, false, nil
	}
	if err := v.Patch([]byte(canon(ops))); err != nil {
		return nil, false, fmt.Errorf("the merge into the tailnet policy: %v", err)
	}
	return v.Pack(), true, nil
}

// policy merges the fleet's needs into the tailnet policy: read as JSON with its ETag, written back with
// If-Match only when the merge added something; a 412 (changed in between) reads it again, once.
func policy(api tsAPI, check bool) (string, error) {
	for try := 0; ; try++ {
		_, etag, b, err := apiDo(api, "GET", "/tailnet/-/acl", "application/hujson", "", nil)
		if err != nil {
			return "", err
		}
		merged, changed, err := mergePolicy(b, fleetPolicy())
		if err != nil {
			return "", err
		}
		switch {
		case !changed:
			return "unchanged", nil
		case check:
			return "would-merge", nil
		}
		code, _, _, err := apiDo(api, "POST", "/tailnet/-/acl", "application/hujson", etag, merged)
		if code == 412 && try == 0 {
			continue
		}
		return "merged", err
	}
}

// retag sets a device's tags to the ones it has and add; it reports whether that changed them.
func retag(api tsAPI, id string, have, add []string, check bool) (bool, error) {
	tags := slices.Clone(have)
	for _, t := range add {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	if len(tags) == len(have) || check {
		return len(tags) != len(have), nil
	}
	_, _, _, err := apiDo(api, "POST", "/device/"+id+"/tags", "application/json", "", []byte(canon(map[string][]string{"tags": tags})))
	return true, err
}

// mintKey is a single-use, preauthorized auth key with the tags, good for an hour; it is held in memory only.
func mintKey(api tsAPI, tags []string) (string, error) {
	create := map[string]any{"reusable": false, "ephemeral": false, "preauthorized": true, "tags": tags}
	_, _, b, err := apiDo(api, "POST", "/tailnet/-/keys", "application/json", "", []byte(canon(map[string]any{"capabilities": map[string]any{"devices": map[string]any{"create": create}},
		"expirySeconds": 3600, "description": "nova-fleet up"})))
	var k struct{ Key string }
	if err == nil && (json.Unmarshal(b, &k) != nil || k.Key == "") {
		err = fmt.Errorf("POST /tailnet/-/keys: no key in the answer")
	}
	return k.Key, err
}

type tsNode struct {
	ID, HostName, DNSName            string
	Tags, TailscaleIPs, SSH_HostKeys []string
	TxBytes, RxBytes                 int64 // this node's bytes to and from the peer since tailscaled started
}

type tsStatus struct {
	BackendState string
	Self         tsNode
	Peer         map[string]tsNode
}

// peer is the one peer whose MagicDNS label (the DNSName before its first dot) is name, with an address;
// more than one is refused with a reason, so a wrong device is never taken for the machine.
func (st tsStatus) peer(name string) (tsNode, bool, error) {
	var found []tsNode
	for _, p := range st.Peer {
		if label, _, _ := strings.Cut(p.DNSName, "."); label == name && len(p.TailscaleIPs) > 0 {
			found = append(found, p)
		}
	}
	if len(found) > 1 {
		var names []string
		for _, p := range found {
			names = append(names, p.ID+"="+strings.TrimSuffix(p.DNSName, "."))
		}
		slices.Sort(names)
		return tsNode{}, false, fmt.Errorf("%d peers have the MagicDNS label %s (%s); remove or rename all but the machine's own in the admin console", len(found), name, strings.Join(names, ", "))
	}
	if len(found) == 0 {
		return tsNode{}, false, nil
	}
	return found[0], true, nil
}

// node is peer, except that this node itself (Self, by the same label) is taken first and reported as self.
func (st tsStatus) node(name string) (tsNode, bool, bool, error) {
	if label, _, _ := strings.Cut(st.Self.DNSName, "."); label == name && len(st.Self.TailscaleIPs) > 0 {
		return st.Self, true, true, nil
	}
	p, ok, err := st.peer(name)
	return p, false, ok, err
}

// status is this node's tailscale status, and whether it is on the tailnet.
func (u *upRun) status() (st tsStatus, on bool) {
	out, _, c := outCmd(u.bin["tailscale"], []string{"status", "--json"}, os.Environ())
	return st, c == 0 && json.Unmarshal([]byte(out), &st) == nil && st.BackendState == "Running"
}

// tailnet is up's step before reach, through the API with the one API access token: the policy holds what the
// fleet needs; this node is on the tailnet, tagged tag:control; each machine named that is on the tailnet
// carries its tags; and each that is not gets a key for its join, held in u.keys and never stored.
func (u *upRun) tailnet() error {
	out, why, c := outCmd(u.bin["nova-secrets"], u.secrets([]string{"TAILSCALE_API_KEY"}, "sh", "-c", `printf %s "$TAILSCALE_API_KEY"`)[1:], os.Environ())
	key := strings.TrimSpace(out)
	if c != 0 || !strings.HasPrefix(key, "tskey-api-") || strings.ContainsAny(key, " \t\n") {
		return fmt.Errorf("TAILSCALE_API_KEY cannot be read as one line tskey-api-... (exit %d) %s; %s", c, oneLine(why), newKey)
	}
	api := newTailnet(key)
	pol, err := policy(api, u.check)
	if err != nil {
		return err
	}
	st, on := u.status()
	control := "already"
	switch {
	case !on && u.check:
		control = "would-join"
	case !on:
		key, err := mintKey(api, []string{"tag:control"})
		if err != nil {
			return err
		}
		sudo := map[bool]string{true: "sudo "}[goos != "darwin"]
		if c, err := runCmd("sh", []string{"-c", `printf %s "$TAILSCALE_AUTHKEY" | ` + sudo + `"$0" up --reset --auth-key=file:/dev/stdin --advertise-tags=tag:control`, u.bin["tailscale"]},
			append(os.Environ(), "TAILSCALE_AUTHKEY="+key)); err != nil || c != 0 {
			return fmt.Errorf("tailscale up on this node with a tag:control key: exit %d %v", c, err)
		}
		if st, on = u.status(); !on {
			return fmt.Errorf("this node is not Running on the tailnet after tailscale up")
		}
		control = "joined"
	default:
		if changed, err := retag(api, st.Self.ID, st.Self.Tags, []string{"tag:control"}, u.check); err != nil {
			return err
		} else if changed {
			control = map[bool]string{true: "would-tag", false: "tagged"}[u.check]
		}
	}
	tags := []string{"tag:nova"}
	if u.runners != "" {
		tags = append(tags, "tag:runner")
	}
	u.keys = map[string]string{}
	keys, retagged := 0, 0
	for _, m := range u.machines {
		p, self, ok, err := st.node(m.name)
		switch {
		case err != nil:
			return err
		case self: // this control node, in the inventory: already on the tailnet, tagged above
			continue
		case ok:
			changed, err := retag(api, p.ID, p.Tags, tags, u.check)
			if err != nil {
				return err
			}
			retagged += map[bool]int{true: 1}[changed]
			continue
		}
		if !u.check {
			if u.keys[m.name], err = mintKey(api, tags); err != nil {
				return err
			}
		}
		keys++
	}
	if u.check {
		u.say("UP TAILNET WOULD policy=%s control=%s keys=%d retagged=%d (check: GETs only, nothing changed)", pol, control, keys, retagged)
	} else {
		u.say("UP TAILNET OK policy=%s control=%s keys=%d retagged=%d", pol, control, keys, retagged)
	}
	return nil
}
