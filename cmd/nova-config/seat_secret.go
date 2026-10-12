package main

// The seat's password with no wrapper (coordinator-config-through-the-seat): a seat
// profile names the variable of its password, and a coordinator typing nova-config
// --seat <name> bare has no such variable, since nothing ran it under nova-secrets
// exec. Then the password is read in this process from the nova-secrets seat the
// machine's store login names (nova-sprint seat login, the file beside the seat
// profile nova-sprint seat install writes), under the profile's variable name as its
// key, and never put in an environment or printed.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// sprintLoginPath is the store login nova-sprint seat login records:
// $XDG_CONFIG_HOME/nova-sprint/login.json, else ~/.config/nova-sprint/login.json.
func sprintLoginPath(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nova-sprint", "login.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nova-sprint", "login.json"), nil
}

// sprintStoreLogin is the record nova-sprint seat login writes: the sprint
// store's address and ACL user, and where the user's password is in
// nova-secrets. nova-config reads it when no flag and no NOVA_SPRINT_REDIS*
// variable names the sprint store, so a bare fleet sync and friend sync run
// with no wrapper (docs/SPEC-CONFIG.md, "Connecting").
type sprintStoreLogin struct {
	Redis  string `json:"redis"`
	User   string `json:"user"`
	Store  string `json:"store"`
	As     string `json:"as"`
	Key    string `json:"key"`
	Sops   string `json:"sops"`
	Secret string `json:"secret"`
}

func (l sprintStoreLogin) secrets() secrets.Login {
	return secrets.Login{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Name: l.Secret}
}

// sprintLoginRecord is the recorded nova-sprint seat login. ok is false when
// none is recorded. A file that cannot be read or is not a whole login is an
// error naming it and the remedy, never a login with a field missing
// (docs/SPEC-CONFIG.md, "Connecting").
func sprintLoginRecord(getenv func(string) string) (l sprintStoreLogin, path string, ok bool, err error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	path, err = sprintLoginPath(getenv)
	if err != nil {
		return l, "", false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, path, false, nil
	}
	remedy := "; run: nova-sprint seat login --check, then nova-sprint seat login again"
	if err != nil {
		return l, path, false, fmt.Errorf("the seat login %s is unreadable: %v%s", path, err, remedy)
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, path, false, fmt.Errorf("the seat login %s is not a login: %v%s", path, err, remedy)
	}
	if l.Redis == "" || l.User == "" {
		return l, path, false, fmt.Errorf("the seat login %s names no redis or no user%s", path, remedy)
	}
	if m := l.secrets().Missing(); m != "" {
		return l, path, false, fmt.Errorf("the seat login %s names no %s%s", path, m, remedy)
	}
	return l, path, true, nil
}

// seatPassword is the seat profile's password when its variable is not in the
// environment: ok false when the variable is set (the environment's wins). A refusal
// names the seat, the file and the remedy, never a value.
func seatPassword(prof seatcred.ConfigProfile, getenv func(string) string) (pw string, ok bool, err error) {
	if prof.PasswordEnv == "" || getenv(prof.PasswordEnv) != "" {
		return "", false, nil
	}
	path, err := sprintLoginPath(getenv)
	if err != nil {
		return "", false, fmt.Errorf("seat %s: %s is not set and the store login's file has no place: %v", prof.Name, prof.PasswordEnv, err)
	}
	remedy := "; run: nova-sprint seat login, or run under nova-secrets exec --only " + prof.PasswordEnv
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("seat %s: %s is not set and no store login is recorded at %s to read it from%s", prof.Name, prof.PasswordEnv, path, remedy)
	}
	if err != nil {
		return "", false, fmt.Errorf("seat %s: %s is not set and the store login %s is unreadable: %v%s", prof.Name, prof.PasswordEnv, path, err, remedy)
	}
	var rec struct{ Store, As, Key, Sops string } // nova-sprint's storeLogin, its fields that say where the seat's file is
	if err := json.Unmarshal(b, &rec); err != nil {
		return "", false, fmt.Errorf("seat %s: %s is not set and %s is not a store login: %v%s", prof.Name, prof.PasswordEnv, path, err, remedy)
	}
	l := secrets.Login{Store: rec.Store, As: rec.As, Key: rec.Key, Sops: rec.Sops, Name: prof.PasswordEnv}
	if m := l.Missing(); m != "" {
		return "", false, fmt.Errorf("seat %s: %s is not set and the store login %s names no %s%s", prof.Name, prof.PasswordEnv, path, m, remedy)
	}
	s, err := loginSecret(path, l)
	if err != nil || !s.Loaded() || s.Empty() {
		why := "it is empty"
		if err != nil {
			why = err.Error()
		}
		return "", false, fmt.Errorf("seat %s: %s is not set, and seat %s of %s (the store login %s) does not give it: %s; seal it with nova-secrets seal --as %s --name %s", prof.Name, prof.PasswordEnv, l.As, l.Store, path, why, l.As, prof.PasswordEnv)
	}
	if err := s.Use(func(p string) error { pw = p; return nil }); err != nil {
		return "", false, err
	}
	return pw, true, nil
}
