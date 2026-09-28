package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// storeHint is the shape every refusal here keeps: what the input WANTS,
// not just what was wrong with it.
const storeHint = `--store <host:port> is the fleet Redis the friends' presence lives on, the same address the other verbs spell --redis; the password is never a flag -- it reaches this process through --seat (nova-secrets' library) or as NOVA_REDIS_BENCH_PASSWORD through nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`

// storeOpener is the seam the tests enter through: awake --store dials
// Redis, a test hands in a fake with a clock it moves. (The beat and
// presence verbs that shared it are gone: a friend's heartbeat is
// nova-friend's, #2610's key friend:<name> included.)
type storeOpener func(ctx context.Context, addr, user string) (presence.Store, func() error, error)

// dialStore is the live opener.
func dialStore(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
	r, err := presence.Open(ctx, addr, user)
	if err != nil {
		return nil, nil, err
	}
	return r, r.Close, nil
}
