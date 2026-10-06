package secrets

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Login is where a tool's store login reads its password in its own process: one
// name in one seat of a secrets store, opened with the seat's key and sops. It
// holds no secret and cannot: a tool may record a Login in its config file and
// print every field of it (nova-sprint seat login, docs/SPEC-SECRETS.md, "A
// tool's store login").
type Login struct {
	Store string // the store's working copy (--store)
	As    string // the seat (--as)
	Key   string // the seat's age key file (--key)
	Sops  string // the sops binary (--sops)
	Name  string // the secret's name in the seat's file (--secret)
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
	return fmt.Sprintf("store=%s as=%s key=%s sops=%s secret=%s", l.Store, l.As, l.Key, l.Sops, l.Name)
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

// Keys are the secrets a binary reads through its seat's login besides the store's
// password, each by its name in the seat's file and in this process, where it is used
// (docs/SPEC-SECRETS.md, "A tool's store login", the keys): the server's decide key, the
// provider keys a member hands its children. No environment holds them: a binary
// answers a key's name through Getenv, and a child is handed its route's one key
// through ChildEnv, never the set. Printed, Keys show their names and no value.
type Keys struct {
	held map[string]Secret
}

// KeyNames is a comma list of key names, blanks dropped: each a secret's name, as
// nova-secrets seals one, and none named twice.
func KeyNames(list string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		switch {
		case n == "":
			continue
		case !IsValidEnvVar(n):
			return nil, fmt.Errorf("the key %q is not a secret's name (A-Z, 0-9, _, a letter first)", n)
		case seen[n]:
			return nil, fmt.Errorf("the key %s is named twice", n)
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, nil
}

// ReadKeys reads each of names from the login's seat in this process, through ReadLogin
// (the checks exec makes), the login's own Name aside. Any name the seat cannot give is
// a refusal naming it and the next thing to run, so a binary refuses at its start and
// never runs on without a key it was told to read. No names read nothing.
func ReadKeys(l Login, names []string) (Keys, error) { return ReadKeysWith(l, names, ReadLogin) }

// ReadKeysWith is ReadKeys over read, the reader of one name: a binary's own reader, or a
// test's fake store.
func ReadKeysWith(l Login, names []string, read func(Login) (Secret, error)) (Keys, error) {
	k := Keys{held: map[string]Secret{}}
	if len(names) == 0 {
		return k, nil
	}
	for _, n := range names {
		if !IsValidEnvVar(n) {
			return Keys{}, fmt.Errorf("the key %q is not a secret's name (A-Z, 0-9, _, a letter first)", n)
		}
	}
	where := l
	where.Name = names[0]
	if m := where.Missing(); m != "" {
		return Keys{}, fmt.Errorf("the keys %s are read through a login that names no %s", strings.Join(names, ","), m)
	}
	for _, n := range names {
		one := l
		one.Name = n
		s, err := read(one)
		if err == nil && (!s.Loaded() || s.Empty()) {
			err = fmt.Errorf("seat %s of store %s holds %s empty", l.As, l.Store, n)
		}
		if err != nil {
			return Keys{}, fmt.Errorf("the key %s does not resolve: %w", n, err)
		}
		k.held[n] = s
	}
	return k, nil
}

// Names are the keys held, sorted.
func (k Keys) Names() []string {
	names := make([]string, 0, len(k.held))
	for n := range k.held {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Has says the key is held.
func (k Keys) Has(name string) bool {
	_, ok := k.held[name]
	return ok
}

// Getenv is next with every key held answered from the value read in process: the
// getenv a binary reads its keys through, where it read them from its environment
// before. The process's environment never holds them, so no child inherits one.
func (k Keys) Getenv(next func(string) string) func(string) string {
	return func(name string) string {
		s, ok := k.held[name]
		if !ok {
			return next(name)
		}
		v := ""
		_ = s.Use(func(p string) error { v = p; return nil }) // ignored: the function never fails
		return v
	}
}

// ProviderKey is the name of a provider's key, the one a harness reads from its
// environment: <PROVIDER>_API_KEY, upper case, every character but a letter or a digit
// an underscore (openrouter: OPENROUTER_API_KEY).
func ProviderKey(provider string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(provider) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + "_API_KEY"
}

// ChildEnv is what a child on a route of provider is handed: its provider's key, one
// NAME=value entry, when it is held, and nothing else; none when it is not. This is the
// one place a key leaves its Secret for another process, and only into that child's
// environment.
func (k Keys) ChildEnv(provider string) []string {
	name := ProviderKey(provider)
	s, ok := k.held[name]
	if !ok {
		return nil
	}
	var out []string
	_ = s.Use(func(v string) error { out = []string{name + "=" + v}; return nil }) // ignored: the function never fails
	return out
}

// String is the keys' names, never a value.
func (k Keys) String() string { return "keys[" + strings.Join(k.Names(), ",") + "]" }

// GoString is String: %#v shows no value either.
func (k Keys) GoString() string { return k.String() }

// Format closes every fmt verb, as Secret's does: a struct's unexported map would
// otherwise be printed field by field.
// ignored: fmt.State's write has no caller to report to
func (k Keys) Format(f fmt.State, verb rune) { _, _ = io.WriteString(f, k.String()) }
