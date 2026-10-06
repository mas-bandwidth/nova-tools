package main

// The keys a sprint unit reads in process (seat-secrets-for-every-key): run --keys names
// the secrets the run loop reads from the store the recorded seat login names (its store,
// seat, key and sops), each through internal/secrets in this process, so no unit carries
// a key in its environment and no nova-secrets exec wrapper starts the loop. Every verb
// of the loop that read a key from its environment (the decide lane's JEV_API_KEY, land's
// gate and score, the providers' balances) reads it through the app's getenv, which
// answers each key named from the value read; the process's environment never holds one,
// so no child of the loop inherits it (docs/SPEC-SECRETS.md, "A tool's store login").

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// keysOn reads every key list names from the recorded seat login's seat and turns them
// on for this app's getenv. A key that does not resolve, or a list with no seat login to
// read it through, is an error naming the key and the remedy: the loop refuses at its
// start rather than run without a key it was told to read. An empty list reads nothing.
func (a *app) keysOn(list string) error {
	names, err := secrets.KeyNames(list)
	if err != nil {
		return fmt.Errorf("--keys: %v", err)
	}
	if len(names) == 0 {
		return nil
	}
	l, ok, err := a.recordedLogin()
	if err != nil {
		return fmt.Errorf("--keys %s: %v", strings.Join(names, ","), err)
	}
	if !ok {
		return fmt.Errorf("--keys %s are read from the store the seat login names, and no seat login is recorded; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>", strings.Join(names, ","))
	}
	keys, err := secrets.ReadKeysWith(l.secrets(), names, a.secretReader())
	if err != nil {
		return fmt.Errorf("--keys: %v; run: nova-secrets names --store %s --as %s, then nova-secrets seal the key the seat lacks, or leave it out of --keys", err, l.Store, l.As)
	}
	a.getenv = keys.Getenv(a.getenv)
	return nil
}
