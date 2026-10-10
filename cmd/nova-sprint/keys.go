package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// unitKeysFile is the list of secret names run reads in process, beside the seat login.
const unitKeysFile = "keys.json"

// unitKeySetting is that list. It holds names and no value, so it may be recorded and printed.
type unitKeySetting struct {
	Names []string `json:"names"`
}

// holdNamedUnitKeys reads each named secret in this process from the seat login and
// answers later getenv calls for those names from the values read. The process
// environment is not changed. An empty setting leaves getenv as it was. A name that
// cannot be read is a refusal naming it and the remedy, never an empty key.
func (a *app) holdNamedUnitKeys(flagNames string) error {
	return a.holdNamedUnitKeysFrom(flagNames, secrets.ReadUnitKeys)
}

// holdNamedUnitKeysFrom is holdNamedUnitKeys over read: a test hands a fake store.
// The reader is the call's, so two tests do not share one.
func (a *app) holdNamedUnitKeysFrom(flagNames string, read func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error)) error {
	names, fromFlag, err := a.unitKeyNames(flagNames)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	l, ok, err := a.recordedLogin()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("run names %s and this nova-sprint has no seat login to read them from; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --sops <path> --secret <NAME> --user <user> --redis <addr>, then nova-sprint run --keys %s",
			strings.Join(names, ","), strings.Join(names, ","))
	}
	held, err := read(secrets.UnitKeyLogin{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Names: names})
	if err != nil {
		return withRun(err, "nova-sprint seat login --check, then nova-sprint run --keys "+strings.Join(names, ","))
	}
	a.holdUnitKeys(held)
	if fromFlag {
		if err := a.recordUnitKeyNames(names); err != nil {
			return err
		}
	}
	return nil
}

// unitKeyNames is the flag's list when the flag is set, else the names recorded beside
// the seat login. An app with the login off and no flag has none: a test records nothing.
// A missing keys file is none, not an error.
func (a *app) unitKeyNames(flagNames string) (names []string, fromFlag bool, err error) {
	if strings.TrimSpace(flagNames) != "" {
		return splitCSV(flagNames), true, nil
	}
	if a.loginFile == nil {
		return nil, false, nil
	}
	path, err := a.loginFile()
	if err != nil {
		return nil, false, fmt.Errorf("the seat login's keys have no place: %w; run: nova-sprint run --keys <NAME,...>", err)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), unitKeysFile))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("the seat login's keys file is unreadable: %v; run: nova-sprint run --keys <NAME,...>", err)
	}
	names, err = decodeKeyNames(b)
	if err != nil {
		return nil, false, err
	}
	return names, false, nil
}

// decodeKeyNames is a keys file's names. Any other field is a refusal naming the field,
// never its contents: a keys file holds names only.
func decodeKeyNames(b []byte) ([]string, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("the seat login's keys file is not a list of names; run: nova-sprint run --keys <NAME,...>")
	}
	for k := range probe {
		if k != "names" {
			return nil, fmt.Errorf("the seat login's keys file holds %q, and a keys file holds names only; run: nova-sprint run --keys <NAME,...>", k)
		}
	}
	var s unitKeySetting
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("the seat login's keys file is not a list of names; run: nova-sprint run --keys <NAME,...>")
	}
	return splitCSV(strings.Join(s.Names, ",")), nil
}

// holdUnitKeys answers getenv for each held name from the secret read in process.
// A held name wins over a value already in the environment. Nothing is set in the
// process environment, so no child inherits the set.
func (a *app) holdUnitKeys(held map[string]secrets.Secret) {
	prev := a.getenv
	a.getenv = func(k string) string {
		if s, ok := held[k]; ok && s.Loaded() && !s.Empty() {
			v := ""
			_ = s.Use(func(p string) error { v = p; return nil }) // ignored: Use's function here never fails
			if v != "" {
				return v
			}
		}
		if prev == nil {
			return ""
		}
		return prev(k)
	}
}

// recordUnitKeyNames writes the flag's names beside the seat login, mode 0600.
// An app with the login off records nothing.
func (a *app) recordUnitKeyNames(names []string) error {
	if a.loginFile == nil {
		return nil
	}
	path, err := a.loginFile()
	if err != nil {
		return fmt.Errorf("the seat login's keys have no place: %w; run: nova-sprint run --keys <NAME,...>", err)
	}
	b, err := json.Marshal(unitKeySetting{Names: names})
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(filepath.Dir(path), unitKeysFile), append(b, '\n')); err != nil {
		return fmt.Errorf("the seat login's keys were not recorded: %w; run: nova-sprint run --keys <NAME,...>", err)
	}
	return nil
}

// splitCSV is a comma list of names, blanks dropped and duplicates kept once, in order.
func splitCSV(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// withRun appends a remedy when err does not already name one.
func withRun(err error, remedy string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "; run: ") {
		return err
	}
	return fmt.Errorf("%s; run: %s", err.Error(), remedy)
}
