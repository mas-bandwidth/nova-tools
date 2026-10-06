package secrets

import (
	"errors"
	"fmt"
	"strings"
)

// Login is where a tool's store login reads its password in its own process: one
// name in one seat of a secrets store, opened with the seat's key and sops, and
// the list of other secret names that binary may read the same way (Names). It
// holds no secret and cannot: a tool may record a Login in its config file and
// print every field of it (nova-sprint seat login, docs/SPEC-SECRETS.md, "A
// tool's store login").
type Login struct {
	Store string   // the store's working copy (--store)
	As    string   // the seat (--as)
	Key   string   // the seat's age key file (--key)
	Sops  string   // the sops binary (--sops)
	Name  string   // the secret's name in the seat's file (--secret), the store password
	Names []string // secret names the binary may read besides Name: a unit's keys, never values
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

// String is the login on one line, every field a fact and none of them a secret.
func (l Login) String() string {
	s := fmt.Sprintf("store=%s as=%s key=%s sops=%s secret=%s", l.Store, l.As, l.Key, l.Sops, l.Name)
	if len(l.Names) > 0 {
		s += " keys=" + strings.Join(l.Names, ",")
	}
	return s
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
