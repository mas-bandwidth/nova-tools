package secrets

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// missingPlace is the login's store, seat, key and sops left empty. Name, the
// store password, is not one of them: a unit key is not the password.
func (l Login) missingPlace() string {
	var m []string
	for _, f := range []struct{ v, flag string }{{l.Store, "--store <dir>"}, {l.As, "--as <seat>"}, {l.Key, "--key <file>"}, {l.Sops, "--sops <path>"}} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.flag)
		}
	}
	return strings.Join(m, ", ")
}

// cleanNames is Names with blanks and duplicates dropped, in order. Empty is a
// refusal: a unit key is a name the login lists, not a guess.
func cleanNames(names []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("the login names no unit key (a list of secret names, such as JEV_API_KEY or the provider key a route needs)")
	}
	return out, nil
}

// ReadUnitKeys reads every name in l.Names from the seat in this process,
// through OpenSeatFile, the path exec takes. The values come back as Secrets
// and are written to no environment. A value the process environment holds
// under one of the names is not the secret returned, and it does not fill a
// name the seat does not hold. A field of the place left empty, a seat that
// does not open, a name the seat does not hold and a name it holds empty are
// each a refusal naming the name and the next command; none is ever an empty key.
func ReadUnitKeys(l Login) (map[string]Secret, error) {
	return readUnitKeys(l, OpenSeatFile, os.Getenv)
}

// readUnitKeys is ReadUnitKeys over open and lookup. lookup is the process
// environment. It is not a source: a value it returns is never the secret.
func readUnitKeys(l Login, open func(storeDir, asName, keyPath, sopsPath string) (SeatFile, error), lookup func(string) string) (map[string]Secret, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	if m := l.missingPlace(); m != "" {
		return nil, errors.New("the login names no " + m)
	}
	names, err := cleanNames(l.Names)
	if err != nil {
		return nil, err
	}
	sf, err := open(l.Store, l.As, l.Key, l.Sops)
	if err != nil {
		return nil, fmt.Errorf("seat %s of store %s did not open: %w", l.As, l.Store, err)
	}
	out := make(map[string]Secret, len(names))
	for _, name := range names {
		s, ok := sf.Secrets[name]
		envHolds := lookup(name) != ""
		if !ok {
			from := ""
			if envHolds {
				from = "; this process's environment is not where a unit reads it"
			}
			return nil, fmt.Errorf("seat %s of store %s holds no %s%s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, name, from, l.Store, l.As)
		}
		if !s.Loaded() || s.Empty() {
			from := ""
			if envHolds {
				from = "; this process's environment is not where a unit reads it"
			}
			return nil, fmt.Errorf("seat %s of store %s holds %s empty, and an empty value is no key%s; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s", l.As, l.Store, name, from, l.Store, l.As, l.Key, l.Sops, name)
		}
		out[name] = s
	}
	return out, nil
}

// RouteKeyName is the one name in names the route's provider needs. provider
// is the half of provider/model before the slash. The name <PROVIDER>_API_KEY
// wins when it is in the list. One name in the list is that name: a pool file
// holds one provider key. None or several, with no exact name, is a refusal
// naming the provider and the names.
func RouteKeyName(provider string, names []string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "", errors.New("the route names no provider, so no one key can be chosen")
	}
	if len(names) == 0 {
		return "", errors.New("the member names no provider key (--pass <NAME,...>)")
	}
	if len(names) == 1 {
		return names[0], nil
	}
	want := strings.ToUpper(provider) + "_API_KEY"
	var match []string
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return n, nil
		}
		if strings.Contains(strings.ToLower(n), provider) {
			match = append(match, n)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", fmt.Errorf("no key of --pass (%s) is the provider %s's; name it %s, or pass that one name", strings.Join(names, ","), provider, want)
	default:
		return "", fmt.Errorf("more than one key of --pass (%s) could be the provider %s's (%s); name the one %s", strings.Join(names, ","), provider, strings.Join(match, ","), want)
	}
}

// OneRouteKey is the environment a child is handed for one route: base, with
// every unit key removed, plus exactly one line, name=value, whose value is
// the seat's secret. No other name in keys is present, and the process
// environment is not read. A name keys does not hold is a refusal naming it.
func OneRouteKey(base []string, keys map[string]Secret, name string) ([]string, error) {
	s, ok := keys[name]
	if !ok || !s.Loaded() || s.Empty() {
		return nil, fmt.Errorf("the route needs %s and the keys read in process do not hold it; run: nova-secrets names", name)
	}
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		n, _, _ := strings.Cut(kv, "=")
		if _, drop := keys[n]; drop {
			continue
		}
		out = append(out, kv)
	}
	if err := s.Use(func(v string) error {
		out = append(out, name+"="+v)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
