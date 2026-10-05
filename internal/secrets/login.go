package secrets

import (
	"errors"
	"fmt"
	"strings"
)

// Login is where a tool's store login reads its password in its own process: one
// name in one seat of a secrets store, opened with the seat's key and sops. It
// holds no secret and cannot: a tool may record a Login in its config file and
// print every field of it (nova-sprint seat login, docs/SPEC-SECRETS.md, "A
// tool's store login"). It may also name more than the store password: a list of
// secret names the binary may read in process.
type Login struct {
	Store   string   `json:"store,omitempty"`   // the store's working copy (--store)
	As      string   `json:"as,omitempty"`      // the seat (--as)
	Key     string   `json:"key,omitempty"`     // the seat's age key file (--key)
	Sops    string   `json:"sops,omitempty"`    // the sops binary (--sops)
	Name    string   `json:"secret,omitempty"`  // the secret's name in the seat's file (--secret)
	Secrets []string `json:"secrets,omitempty"` // secret names the binary may read
}

// Missing names every field of the login left empty, as the flags that set
// them, in one list: "" when none is.
func (l Login) Missing() string {
	var m []string
	for _, f := range []struct{ v, flag string }{{l.Store, "--store <dir>"}, {l.As, "--as <seat>"}, {l.Key, "--key <file>"}, {l.Sops, "--sops <path>"}, {l.Name, "--secret <NAME>"}} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.flag)
		}
	}
	return strings.Join(m, ", ")
}

// MissingForSecrets names every field needed to open the seat left empty:
// store, as, key, sops (not secret, because the secrets are named in Secrets or args).
func (l Login) MissingForSecrets() string {
	var m []string
	for _, f := range []struct{ v, flag string }{{l.Store, "--store <dir>"}, {l.As, "--as <seat>"}, {l.Key, "--key <file>"}, {l.Sops, "--sops <path>"}} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.flag)
		}
	}
	return strings.Join(m, ", ")
}

// String is the login on one line, every field a fact and none of them a secret.
func (l Login) String() string {
	base := fmt.Sprintf("store=%s as=%s key=%s sops=%s secret=%s", l.Store, l.As, l.Key, l.Sops, l.Name)
	if len(l.Secrets) > 0 {
		return base + fmt.Sprintf(" secrets=%s", strings.Join(l.Secrets, ","))
	}
	return base
}

// ReadLogin reads the login's secret in this process through OpenSeatFile, the
// one path exec takes, so it checks everything exec checks before it decrypts.
// The value comes back as a Secret, never a string, and goes into no
// environment. A login with a field missing, a seat that does not open, and a
// name the seat does not hold or holds empty are each a refusal naming the
// login and the next thing to run; none of them is ever an empty password.
func ReadLogin(l Login) (Secret, error) { return readLogin(l, OpenSeatFile) }

// readLogin is ReadLogin over open: a test hands a fake store.
func readLogin(l Login, open func(storeDir, asName, keyPath, sopsPath string) (SeatFile, error)) (Secret, error) {
	if m := l.Missing(); m != "" {
		return Secret{}, errors.New("the login names no " + m)
	}
	sf, err := open(l.Store, l.As, l.Key, l.Sops)
	if err != nil {
		return Secret{}, fmt.Errorf("seat %s of store %s did not open: %w", l.As, l.Store, err)
	}
	s, ok := sf.Secrets[l.Name]
	if !ok {
		return Secret{}, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, l.Name, l.Store, l.As)
	}
	if !s.Loaded() || s.Empty() {
		return Secret{}, fmt.Errorf("seat %s of store %s holds %s empty, and an empty value is no password; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s", l.As, l.Store, l.Name, l.Store, l.As, l.Key, l.Sops, l.Name)
	}
	return s, nil
}

// ReadLoginSecrets reads the login's named secrets in this process through OpenSeatFile,
// the one path exec takes, so it checks everything exec checks before it decrypts.
// If names are given, those names are read; otherwise, l.Secrets (and l.Name if set)
// are read. The values come back as a map[string]Secret, never strings, and go into no
// environment. A login with a field missing, a seat that does not open, and a
// name the seat does not hold or holds empty are each a refusal naming the
// secret and the next thing to run.
func ReadLoginSecrets(l Login, names ...string) (map[string]Secret, error) {
	return readLoginSecrets(l, OpenSeatFile, names...)
}

func readLoginSecrets(l Login, open func(storeDir, asName, keyPath, sopsPath string) (SeatFile, error), names ...string) (map[string]Secret, error) {
	if m := l.MissingForSecrets(); m != "" {
		return nil, errors.New("the login names no " + m)
	}
	wanted := names
	if len(wanted) == 0 {
		wanted = l.Secrets
	}
	if len(wanted) == 0 && l.Name != "" {
		wanted = []string{l.Name}
	}
	if len(wanted) == 0 {
		return nil, errors.New("the login names no secrets")
	}
	sf, err := open(l.Store, l.As, l.Key, l.Sops)
	if err != nil {
		return nil, fmt.Errorf("seat %s of store %s did not open: %w", l.As, l.Store, err)
	}
	out := make(map[string]Secret, len(wanted))
	for _, name := range wanted {
		s, ok := sf.Secrets[name]
		if !ok {
			return nil, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, name, l.Store, l.As)
		}
		if !s.Loaded() || s.Empty() {
			emptyDesc := "secret"
			if name == l.Name {
				emptyDesc = "password"
			}
			return nil, fmt.Errorf("seat %s of store %s holds %s empty, and an empty value is no %s; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s", l.As, l.Store, name, emptyDesc, l.Store, l.As, l.Key, l.Sops, name)
		}
		out[name] = s
	}
	return out, nil
}
