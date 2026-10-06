package main

// The decision loop's API key is read in this process from the seat login, by
// the name JEV_API_KEY. The process environment is not the source when a login
// is recorded, and the value is written to no environment. A unit therefore
// carries no key, and no nova-secrets exec wrapper is required.

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// decisionKey is the decision loop's API key. A recorded seat login reads
// JEV_API_KEY from that seat in this process; a name the seat does not hold
// refuses, naming it and the remedy, and the environment is not consulted.
// No recorded login is the environment's key, which a unit does not have:
// "" grades nothing, the lane's old line.
func (a *app) decisionKey() (string, error) {
	l, ok, err := a.recordedLogin()
	if err != nil {
		return "", err
	}
	if !ok {
		return a.getenv(decide.JevSecret), nil
	}
	login := l.secrets()
	login.Names = []string{decide.JevSecret}
	var s secrets.Secret
	if a.loginSecret != nil {
		// a test's reader stands in for the seat; production reads the list
		login.Name = decide.JevSecret
		s, err = a.loginSecret(login)
	} else {
		var got map[string]secrets.Secret
		got, err = secrets.ReadUnitKeys(login)
		if err == nil {
			s = got[decide.JevSecret]
		}
	}
	if err != nil {
		return "", fmt.Errorf("the decision loop reads %s in process from seat %s of store %s, and it does not resolve: %s; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s",
			decide.JevSecret, l.As, l.Store, err.Error(), l.Store, l.As, l.Key, l.Sops, decide.JevSecret)
	}
	v := ""
	if err := s.Use(func(p string) error { v = p; return nil }); err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("the decision loop reads %s in process from seat %s of store %s, and it is empty; run: nova-secrets seal --store %s --as %s --key %s --sops %s --name %s",
			decide.JevSecret, l.As, l.Store, l.Store, l.As, l.Key, l.Sops, decide.JevSecret)
	}
	return v, nil
}
