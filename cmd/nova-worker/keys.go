package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// swarmKeysEnv is a path to a JSON seat login of names (store, as, key, sops, names).
// It holds no secret value. Unset, the member uses NOVA_SEAT.
const swarmKeysEnv = "NOVA_SWARM_KEYS"

// swarmKeyFile is the seat a member reads named secrets from. A value field is refused.
type swarmKeyFile struct {
	Store string   `json:"store"`
	As    string   `json:"as"`
	Key   string   `json:"key"`
	Sops  string   `json:"sops"`
	Names []string `json:"names"`
}

// prepareMemberKeys reads, in this process, each name of pass (and of the
// NOVA_SWARM_KEYS file, when set) whose value getenv does not hold. Names
// already in the environment are left there and the seat is not opened. extra
// is the file's names, to hand on with pass. held is nil when nothing was read.
func prepareMemberKeys(pass []string, getenv func(string) string) (held map[string]secrets.Secret, extra []string, err error) {
	return prepareMemberKeysFrom(pass, getenv, secrets.ReadUnitKeys)
}

// prepareMemberKeysFrom is prepareMemberKeys over read: a test hands a fake store.
// The reader is the call's, so two tests do not share one.
func prepareMemberKeysFrom(pass []string, getenv func(string) string, read func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error)) (held map[string]secrets.Secret, extra []string, err error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var file swarmKeyFile
	haveFile := false
	if p := strings.TrimSpace(getenv(swarmKeysEnv)); p != "" {
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil, nil, fmt.Errorf("NOVA_SWARM_KEYS %s is unreadable: %v; run: nova-worker member --pass <NAME,...> with NOVA_SEAT set, or NOVA_SWARM_KEYS=<file> naming the seat (store, as, key, sops, names) and never a value", p, rerr)
		}
		file, rerr = decodeSwarmKeys(b)
		if rerr != nil {
			return nil, nil, rerr
		}
		haveFile = true
		extra = file.Names
		pass = append(append([]string{}, pass...), file.Names...)
	}
	var missing []string
	seen := map[string]bool{}
	for _, n := range pass {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if getenv(n) == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil, extra, nil
	}
	u := secrets.UnitKeyLogin{Names: missing}
	if haveFile {
		u.Store, u.As, u.Key, u.Sops = file.Store, file.As, file.Key, file.Sops
	} else {
		seat := strings.TrimSpace(getenv(seatcred.SeatEnv))
		if seat == "" {
			return nil, nil, fmt.Errorf("%s is absent from this environment and no seat is named to read it in process; run: NOVA_SEAT=<seat> nova-worker member --pass %s (the value is read in process from nova-secrets, and the unit's environment carries the name only)",
				strings.Join(missing, ","), strings.Join(missing, ","))
		}
		paths, perr := seatcred.PathsFor(seat, getenv)
		if perr != nil {
			return nil, nil, withMemberRun(perr, missing)
		}
		u.Store, u.As, u.Key, u.Sops = paths.Store, seat, paths.Key, paths.Sops
	}
	held, err = read(u)
	if err != nil {
		return nil, nil, withMemberRun(err, missing)
	}
	return held, extra, nil
}

// decodeSwarmKeys is a keys file's seat and names. A field other than store, as,
// key, sops and names is a refusal naming the field, never its contents.
func decodeSwarmKeys(b []byte) (swarmKeyFile, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return swarmKeyFile{}, fmt.Errorf("NOVA_SWARM_KEYS is not a seat login of names; run: nova-worker member --pass <NAME,...> with NOVA_SEAT set, or NOVA_SWARM_KEYS=<file> naming the seat (store, as, key, sops, names) and never a value")
	}
	allowed := map[string]bool{"store": true, "as": true, "key": true, "sops": true, "names": true}
	for k := range probe {
		if !allowed[k] {
			return swarmKeyFile{}, fmt.Errorf("NOVA_SWARM_KEYS holds %q, and a keys file holds store, as, key, sops and names only; run: nova-worker member --pass <NAME,...> with NOVA_SEAT set, or NOVA_SWARM_KEYS=<file> naming the seat and never a value", k)
		}
	}
	var file swarmKeyFile
	if err := json.Unmarshal(b, &file); err != nil {
		return swarmKeyFile{}, fmt.Errorf("NOVA_SWARM_KEYS is not a seat login of names; run: nova-worker member --pass <NAME,...> with NOVA_SEAT set, or NOVA_SWARM_KEYS=<file> naming the seat (store, as, key, sops, names) and never a value")
	}
	return file, nil
}

func withMemberRun(err error, missing []string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "; run: ") {
		return err
	}
	return fmt.Errorf("%s; run: nova-worker member --pass %s with NOVA_SEAT set, or NOVA_SWARM_KEYS=<file> naming the seat (store, as, key, sops, names) and never a value", err.Error(), strings.Join(missing, ","))
}

// oneRoutePass is the names a child is handed from pass: the decision key when
// pass names it, and the one provider key the route needs. More than one provider
// key and none the route's is none of them, never the whole set.
func oneRoutePass(pass []string, model string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range pass {
		n = strings.TrimSpace(n)
		if n == decide.JevSecret && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if k := secrets.RouteKey(model, pass); k != "" && !seen[k] {
		out = append(out, k)
	}
	return out
}

// childEnv is the environment one native child starts with. Held secrets are
// dropped from the inherited environment and appended only when the route's
// pass names them, so the child receives the decision key and the one route
// key, never the whole set. The value is not printed.
func (r *nativeRunner) childEnv(model string) []string {
	base := append(os.Environ(), r.env...)
	pass := oneRoutePass(r.pass, model)
	if len(r.held) == 0 {
		return childEnviron(base, pass)
	}
	filtered := make([]string, 0, len(base))
	for _, kv := range base {
		n, _, _ := strings.Cut(kv, "=")
		if _, ok := r.held[n]; ok {
			continue
		}
		filtered = append(filtered, kv)
	}
	out := childEnviron(filtered, pass)
	have := map[string]bool{}
	for _, kv := range out {
		n, _, _ := strings.Cut(kv, "=")
		have[n] = true
	}
	for _, n := range pass {
		if have[n] {
			continue
		}
		s, ok := r.held[n]
		if !ok || !s.Loaded() || s.Empty() {
			continue
		}
		var v string
		if err := s.Use(func(p string) error { v = p; return nil }); err != nil {
			continue
		}
		out = append(out, n+"="+v)
	}
	return out
}

// getenv answers a held name from the secret read in this process, else the
// process environment. The member's own attempt decision reads the decision
// key through it, so that key need not be in the unit's environment.
func (r *nativeRunner) getenv(k string) string {
	if s, ok := r.held[k]; ok && s.Loaded() && !s.Empty() {
		v := ""
		if err := s.Use(func(p string) error { v = p; return nil }); err == nil && v != "" {
			return v
		}
	}
	return os.Getenv(k)
}
