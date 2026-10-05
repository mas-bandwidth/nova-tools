package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// serverSeatLogin loads the recorded seat login if one exists.
func (a *app) serverSeatLogin() (secrets.Login, bool, error) {
	if a.loginFile == nil {
		return secrets.Login{}, false, nil
	}
	path, err := a.loginFile()
	if err != nil {
		return secrets.Login{}, false, fmt.Errorf("the seat login's file has no place: %w", err)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return secrets.Login{}, false, nil
	}
	remedy := "; run: nova-sprint seat logout, then nova-sprint seat login again"
	if err != nil {
		return secrets.Login{}, false, fmt.Errorf("the seat login %s is unreadable: %v%s", path, err, remedy)
	}
	var raw struct {
		Store   string   `json:"store"`
		As      string   `json:"as"`
		Key     string   `json:"key"`
		Sops    string   `json:"sops"`
		Secret  string   `json:"secret"`
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return secrets.Login{}, false, fmt.Errorf("the seat login %s is not a login: %v%s", path, err, remedy)
	}
	l := secrets.Login{
		Store:   raw.Store,
		As:      raw.As,
		Key:     raw.Key,
		Sops:    raw.Sops,
		Name:    raw.Secret,
		Secrets: raw.Secrets,
	}
	if m := l.MissingForSecrets(); m != "" {
		return secrets.Login{}, false, fmt.Errorf("the seat login %s names no %s%s", path, m, remedy)
	}
	return l, true, nil
}

// readServerSecret reads a named secret in process using a.secretReader (or nova-secrets).
func (a *app) readServerSecret(l secrets.Login, name string) (secrets.Secret, error) {
	req := l
	req.Name = name
	return a.secretReader()(req)
}

// serverDecisionKey reads JEV_API_KEY in process for the server's decision loop.
// If a seat login is recorded, it reads JEV_API_KEY from that seat.
// If reading the key fails, it refuses with the remedy naming the key.
func (a *app) serverDecisionKey() (string, error) {
	login, ok, err := a.serverSeatLogin()
	if err != nil {
		return "", err
	}
	if ok {
		s, err := a.readServerSecret(login, decide.JevSecret)
		if err != nil {
			return "", err
		}
		var val string
		// ignored: the closure returns nil so Use never returns an error
		_ = s.Use(func(v string) error { val = v; return nil })
		return val, nil
	}
	if envKey := a.getenv(decide.JevSecret); envKey != "" {
		return envKey, nil
	}
	return "", fmt.Errorf("the seat login names no %s; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>", decide.JevSecret)
}

// checkServerLoginSecrets verifies that every secret named in the seat login's
// secrets list can be read in process, refusing at start with the remedy when any cannot.
func (a *app) checkServerLoginSecrets() error {
	login, ok, err := a.serverSeatLogin()
	if err != nil {
		return err
	}
	if !ok || len(login.Secrets) == 0 {
		return nil
	}
	for _, name := range login.Secrets {
		if _, err := a.readServerSecret(login, name); err != nil {
			return err
		}
	}
	return nil
}
