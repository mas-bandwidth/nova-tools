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

// UnitKeyLogin returns a UnitKeyLogin for the seat named by l, naming names.
func (l Login) UnitKeyLogin(names ...string) UnitKeyLogin {
	return UnitKeyLogin{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Names: names}
}

// DecisionKey is the secret the sprint server's decision loop reads. It is the
// same name decide.JevSecret carries; secrets does not import that package.
const DecisionKey = "JEV_API_KEY"

// UnitKeyLogin is a seat login that names every secret a sprint unit reads in
// its own process, not only the store password. Names is the setting: the
// decision key, each provider key. It holds no secret, so a tool may record it
// and print every field.
type UnitKeyLogin struct {
	Store string
	As    string
	Key   string
	Sops  string
	Names []string
}

// Missing names every field left empty, as the flags that set them. "" when
// none is. A name list left empty is --keys.
func (u UnitKeyLogin) Missing() string {
	var m []string
	for _, f := range []struct{ v, flag string }{{u.Store, "--store <dir>"}, {u.As, "--as <seat>"}, {u.Key, "--key <file>"}, {u.Sops, "--sops <path>"}} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.flag)
		}
	}
	if len(u.names()) == 0 {
		m = append(m, "--keys <NAME,...>")
	}
	return strings.Join(m, ", ")
}

// String is the login on one line. Every field is a fact; none is a secret.
func (u UnitKeyLogin) String() string {
	return fmt.Sprintf("store=%s as=%s key=%s sops=%s keys=%s", u.Store, u.As, u.Key, u.Sops, strings.Join(u.names(), ","))
}

// names is Names with blanks dropped and duplicates kept once, in order.
func (u UnitKeyLogin) names() []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range u.Names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ReadUnitKeys reads each named secret in this process through OpenSeatFile,
// once. The values come back as Secrets and go into no environment. A field
// left empty, a name that is not a secret name, a seat that does not open, and
// a name the seat does not hold or holds empty are each a refusal naming that
// name and the next command; none of them is ever an empty key.
func ReadUnitKeys(u UnitKeyLogin) (map[string]Secret, error) {
	return readUnitKeys(u, OpenSeatFile)
}

// readUnitKeys is ReadUnitKeys over open: a test hands a fake store.
func readUnitKeys(u UnitKeyLogin, open func(storeDir, asName, keyPath, sopsPath string) (SeatFile, error)) (map[string]Secret, error) {
	if m := u.Missing(); m != "" {
		return nil, errors.New("the login names no " + m)
	}
	for _, n := range u.names() {
		if !IsValidEnvVar(n) {
			return nil, fmt.Errorf("the login names %s, which is not a secret name (capital letters, digits, _); run: nova-sprint run --keys <NAME,...>", n)
		}
	}
	sf, err := open(u.Store, u.As, u.Key, u.Sops)
	if err != nil {
		return nil, fmt.Errorf("seat %s of store %s did not open: %w", u.As, u.Store, err)
	}
	out := make(map[string]Secret, len(u.names()))
	for _, n := range u.names() {
		s, ok := sf.Secrets[n]
		if !ok {
			return nil, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", u.As, u.Store, n, u.Store, u.As)
		}
		if !s.Loaded() || s.Empty() {
			return nil, fmt.Errorf("seat %s of store %s holds %s empty, and an empty value is no key; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s", u.As, u.Store, n, u.Store, u.As, u.Key, u.Sops, n)
		}
		out[n] = s
	}
	return out, nil
}

// RouteKey is the one name of names a route needs. The provider of model
// (provider/model, the slash optional) wants <PROVIDER>_API_KEY when that name
// is listed. Otherwise the only listed name is the route's key. The decision
// key is never a route key. "" when none of that is so: more than one name and
// none is the route's, which is not the whole set.
func RouteKey(model string, names []string) string {
	provider, _, _ := strings.Cut(strings.TrimSpace(model), "/")
	want := strings.ToUpper(provider) + "_API_KEY"
	var rest []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || n == DecisionKey || seen[n] {
			continue
		}
		seen[n] = true
		if n == want {
			return n
		}
		rest = append(rest, n)
	}
	if len(rest) == 1 {
		return rest[0]
	}
	return ""
}
