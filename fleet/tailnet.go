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
)

// tsAPI is the Tailscale API v2 (tailnet "-", the API access token's own): one request, its status, its ETag
// and its body. Unit tests fake it; tsHTTP is the real one.
type tsAPI interface {
	call(method, path, ifMatch string, body []byte) (int, string, []byte, error)
}

// tsBase is where the API is; the functional tier points it at a local stand-in.
var tsBase = "https://api.tailscale.com"

// newTailnet is the API seam: unit tests replace it.
var newTailnet = func(key string) tsAPI { return tsHTTP(key) }

// tsHTTP sends each request with the API access token as Bearer auth.
type tsHTTP string

// newKey is how to replace a token that expired or was revoked.
const newKey = "make a new one in the Tailscale admin console > Settings > Keys > Generate API access token, then: nova-secrets set TAILSCALE_API_KEY --as <seat> (the token on stdin)"

func (key tsHTTP) call(method, path, ifMatch string, body []byte) (int, string, []byte, error) {
	req, err := http.NewRequest(method, tsBase+"/api/v2"+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
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
func apiDo(api tsAPI, method, path, ifMatch string, v any) (int, string, []byte, error) {
	var body []byte
	if v != nil {
		body, _ = json.Marshal(v) // ignored: maps of strings and lists always marshal
	}
	code, etag, b, err := api.call(method, path, ifMatch, body)
	switch {
	case err == nil && code == 401:
		err = fmt.Errorf("%s %s: HTTP 401: TAILSCALE_API_KEY was refused; an API access token expires (90 days by default): %s", method, path, newKey)
	case err == nil && code != 200:
		err = fmt.Errorf("%s %s: HTTP %d %s", method, path, code, oneLine(string(b)))
	}
	return code, etag, b, err
}

// fleetPolicy is what the fleet needs of the tailnet policy: the embedded tailscale-acl.json (fleet/README.md, "tailscale").
func fleetPolicy() (m map[string]any) {
	b, _ := fs.ReadFile(tree, "tailscale-acl.json") // ignored: embedded; a bad file panics below
	if err := json.Unmarshal(b, &m); err != nil {
		panic("tailscale-acl.json: " + err.Error())
	}
	return m
}

func canon(v any) string { b, _ := json.Marshal(v); return string(b) }

// mergePolicy is have with the fleet's needs added (the tag owners first in effect: up writes the policy
// before it tags a device or mints a key): each owner of want's tagOwners a tag lacks, and each
// grant and ssh rule of want with no equal rule in have, appended. Nothing of have is removed or replaced,
// so a policy that allowed all ("acls" accept * to *:*) still does. It reports whether anything was added.
func mergePolicy(have, want map[string]any) (map[string]any, bool) {
	out, changed := maps.Clone(have), false
	if out == nil {
		out = map[string]any{}
	}
	owners, _ := have["tagOwners"].(map[string]any) // ignored: a type assertion, none is nil
	owners = maps.Clone(owners)
	if owners == nil {
		owners = map[string]any{}
	}
	for tag, ws := range want["tagOwners"].(map[string]any) {
		list, _ := owners[tag].([]any) // ignored: a type assertion, none is nil
		for _, w := range ws.([]any) {
			if !slices.Contains(list, w) {
				list, changed = append(slices.Clip(list), w), true
			}
		}
		owners[tag] = list
	}
	out["tagOwners"] = owners
	for _, k := range []string{"grants", "ssh"} {
		rules, _ := have[k].([]any) // ignored: a type assertion, none is nil
		for _, r := range want[k].([]any) {
			if !slices.ContainsFunc(rules, func(x any) bool { return canon(x) == canon(r) }) {
				rules, changed = append(slices.Clip(rules), r), true
			}
		}
		out[k] = rules
	}
	return out, changed
}

// policy merges the fleet's needs into the tailnet policy: read as JSON with its ETag, written back with
// If-Match only when the merge added something; a 412 (changed in between) reads it again, once.
func policy(api tsAPI, check bool) (string, error) {
	for try := 0; ; try++ {
		_, etag, b, err := apiDo(api, "GET", "/tailnet/-/acl", "", nil)
		if err != nil {
			return "", err
		}
		have := map[string]any{}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&have); err != nil && err != io.EOF {
			return "", fmt.Errorf("GET /tailnet/-/acl: not a JSON policy: %v", err)
		}
		merged, changed := mergePolicy(have, fleetPolicy())
		switch {
		case !changed:
			return "unchanged", nil
		case check:
			return "would-merge", nil
		}
		code, _, _, err := apiDo(api, "POST", "/tailnet/-/acl", etag, merged)
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
	_, _, _, err := apiDo(api, "POST", "/device/"+id+"/tags", "", map[string][]string{"tags": tags})
	return true, err
}

// mintKey is a single-use, preauthorized auth key with the tags, good for an hour; it is held in memory only.
func mintKey(api tsAPI, tags []string) (string, error) {
	create := map[string]any{"reusable": false, "ephemeral": false, "preauthorized": true, "tags": tags}
	_, _, b, err := apiDo(api, "POST", "/tailnet/-/keys", "", map[string]any{"capabilities": map[string]any{"devices": map[string]any{"create": create}},
		"expirySeconds": 3600, "description": "nova-fleet up"})
	var k struct{ Key string }
	if err == nil && (json.Unmarshal(b, &k) != nil || k.Key == "") {
		err = fmt.Errorf("POST /tailnet/-/keys: no key in the answer")
	}
	return k.Key, err
}

type tsNode struct {
	ID, HostName, DNSName            string
	Tags, TailscaleIPs, SSH_HostKeys []string
}

type tsStatus struct {
	BackendState string
	Self         tsNode
	Peer         map[string]tsNode
}

// peer is the peer with the hostname and an address.
func (st tsStatus) peer(name string) (tsNode, bool) {
	for _, p := range st.Peer {
		if p.HostName == name && len(p.TailscaleIPs) > 0 {
			return p, true
		}
	}
	return tsNode{}, false
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
		if p, ok := st.peer(m.name); ok {
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
