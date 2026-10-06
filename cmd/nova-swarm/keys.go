package main

// A member's provider keys are read in this process from the seat login
// nova-sprint recorded, named by --pass. A child is handed only the one key
// its route needs. The process environment is not the source, so a unit
// carries no key and no nova-secrets exec wrapper is required.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// recordedSeat is the place nova-sprint seat login recorded. It holds no secret.
type recordedSeat struct {
	Store  string
	As     string
	Key    string
	Sops   string
	Secret string
}

// seatLoginPath is the per-user file nova-sprint seat login writes.
func seatLoginPath(getenv func(string) string, home func() (string, error)) (string, error) {
	if x := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "nova-sprint", "login.json"), nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "nova-sprint", "login.json"), nil
}

// readRecordedSeat reads the seat login at path. A missing file is os.ErrNotExist.
func readRecordedSeat(path string) (recordedSeat, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return recordedSeat{}, err
	}
	var raw struct {
		Store  string `json:"store"`
		As     string `json:"as"`
		Key    string `json:"key"`
		Sops   string `json:"sops"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return recordedSeat{}, fmt.Errorf("the seat login %s is not a login: %w", path, err)
	}
	s := recordedSeat{Store: raw.Store, As: raw.As, Key: raw.Key, Sops: raw.Sops, Secret: raw.Secret}
	if s.Store == "" || s.As == "" || s.Key == "" || s.Sops == "" {
		return recordedSeat{}, fmt.Errorf("the seat login %s names no store, seat, key or sops; run: nova-sprint seat logout, then nova-sprint seat login again", path)
	}
	return s, nil
}

// loadMemberKeys reads each name in pass from the seat login, in process.
// pass empty reads nothing: nil, nil, and the member keeps handing --pass from
// the environment. A missing login, a seat that does not open, or a name the
// seat does not hold refuses, naming the name and the remedy. When the pass
// names resolve, JEV_API_KEY is read too if the seat holds it, for native, and
// a seat that does not hold it is not a refusal.
func loadMemberKeys(pass []string, getenv func(string) string, home func() (string, error), read func(secrets.Login) (map[string]secrets.Secret, error)) (map[string]secrets.Secret, error) {
	if len(pass) == 0 {
		return nil, nil
	}
	path, err := seatLoginPath(getenv, home)
	if err != nil {
		return nil, fmt.Errorf("--pass names %s, and the seat login has no place: %s; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>", strings.Join(pass, ","), err.Error())
	}
	seat, err := readRecordedSeat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("--pass names %s, and no seat login is recorded at %s to read them in this process; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>", strings.Join(pass, ","), path)
	}
	if err != nil {
		return nil, fmt.Errorf("--pass names %s, and the seat login cannot be read: %s", strings.Join(pass, ","), err.Error())
	}
	login := secrets.Login{Store: seat.Store, As: seat.As, Key: seat.Key, Sops: seat.Sops, Name: seat.Secret, Names: append([]string{}, pass...)}
	keys, err := read(login)
	if err != nil {
		return nil, fmt.Errorf("the member reads its provider keys in process from seat %s of %s, and a named one does not resolve: %s", seat.As, seat.Store, err.Error())
	}
	jev := login
	jev.Names = []string{decide.JevSecret}
	if extra, err := read(jev); err == nil {
		if s, ok := extra[decide.JevSecret]; ok && s.Loaded() && !s.Empty() {
			keys[decide.JevSecret] = s
		}
	}
	return keys, nil
}

// childEnv is the environment one native child starts with. With no keys read
// in process it is the allowlist plus the names --pass lists, copied from the
// environment. With keys, the environment's copies of those names are dropped
// and the child is handed only the one provider key its route needs, the
// worker description's secret when it names a different one, and JEV_API_KEY
// when the seat held it: native asks the decide read and the gate with that
// key and does not hand it to the harness. No other name in the set is handed.
func (r *nativeRunner) childEnv(model string) ([]string, error) {
	if r.keys == nil {
		return childEnviron(append(os.Environ(), r.env...), r.pass), nil
	}
	base := childEnviron(append(os.Environ(), r.env...), nil)
	provider, ok := providerOf(model)
	if !ok {
		return nil, fmt.Errorf("model %q is not provider/model, so no one key can be chosen for the child", model)
	}
	name, err := secrets.RouteKeyName(provider, r.keyNames)
	if err != nil {
		return nil, err
	}
	env, err := secrets.OneRouteKey(base, r.keys, name)
	if err != nil {
		return nil, err
	}
	if r.workerSecret != "" && r.workerSecret != name {
		env, err = secrets.OneRouteKey(env, map[string]secrets.Secret{r.workerSecret: r.keys[r.workerSecret]}, r.workerSecret)
		if err != nil {
			return nil, err
		}
	}
	if s, ok := r.keys[decide.JevSecret]; ok {
		env, err = secrets.OneRouteKey(env, map[string]secrets.Secret{decide.JevSecret: s}, decide.JevSecret)
		if err != nil {
			return nil, err
		}
	}
	return env, nil
}
