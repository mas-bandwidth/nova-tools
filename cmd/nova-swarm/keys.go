package main

// The keys a member reads in process (seat-secrets-for-every-key): member --keys names the
// secrets the member reads from its seat's store (--seat or NOVA_SEAT, in nova-secrets'
// fleet layout, as the seat's Redis login is read: internal/seatcred), each through
// internal/secrets in this process, so its unit carries no key in its environment and no
// nova-secrets exec wrapper starts it. A launch is handed its route's one provider key,
// never the set (launchKeys); the member's own attempt decision reads the decide key
// through the keys' getenv (docs/SPEC-SECRETS.md, "A tool's store login").

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// memberKeys reads every key list names from seat's file in the store getenv's layout
// names (seatcred.PathsFor), through read (secrets.ReadKeys; a test's fake store). An
// empty list reads nothing. A list with no seat, or a key the seat cannot give, is an
// error naming it and the remedy: the member refuses at its start rather than start
// children that fail at their provider.
func memberKeys(list, seat string, getenv func(string) string, read func(secrets.Login, []string) (secrets.Keys, error)) (secrets.Keys, error) {
	names, err := secrets.KeyNames(list)
	if err != nil {
		return secrets.Keys{}, fmt.Errorf("--keys: %v", err)
	}
	if len(names) == 0 {
		return secrets.Keys{}, nil
	}
	if strings.TrimSpace(seat) == "" {
		return secrets.Keys{}, fmt.Errorf("--keys %s are read from the seat's store, and no seat is named; run: nova-swarm --seat <name> member ... (or set %s)", strings.Join(names, ","), seatcred.SeatEnv)
	}
	p, err := seatcred.PathsFor(seat, getenv)
	if err != nil {
		return secrets.Keys{}, fmt.Errorf("--keys %s: %v", strings.Join(names, ","), err)
	}
	keys, err := read(secrets.Login{Store: p.Store, As: seat, Key: p.Key, Sops: p.Sops}, names)
	if err != nil {
		return secrets.Keys{}, fmt.Errorf("--keys: %v; run: nova-secrets names --store %s --as %s, then nova-secrets seal the key the seat lacks, or leave it out of --keys", err, p.Store, seat)
	}
	return keys, nil
}

// launchKeys is what one launch's native is handed of the member's keys: the provider
// key of the route it runs (model, provider/model), and the decide key when the member
// holds it, which native keeps for its own decide read and gate decision and never hands
// the harness (nativeChildEnv removes it). Never another provider's key, so the harness
// starts with its route's one key.
func launchKeys(keys secrets.Keys, model string) []string {
	var out []string
	if provider, ok := providerOf(model); ok {
		out = append(out, keys.ChildEnv(provider)...)
	}
	if keys.Has(decide.JevSecret) {
		out = append(out, decide.JevSecret+"="+keys.Getenv(func(string) string { return "" })(decide.JevSecret))
	}
	return out
}
