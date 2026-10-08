package main

// The one network this tool reaches besides the ledger store: the Redis bus whose log
// `--bus` reads (SPEC-TOKENS rule 6; docs/SPEC-BUS.md). It is read only: XRANGE of
// bus2:log and the roster, through internal/bus.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// busTimeout bounds the whole read of the log.
const busTimeout = 60 * time.Second

// world is what the tool reaches outside itself that a test replaces: the Redis bus
// opened for an address. main passes the real one; a test passes a fake over
// internal/bus's Fake, so no test opens a socket.
type world struct {
	openBus func(ctx context.Context, addr string) (bus.Store, func(), error)
}

func realWorld() world { return world{openBus: openRedisBus} }

// openRedisBus dials the bus store at addr with the fleet seat (the ACL user from
// NOVA_SPRINT_REDIS_USER, its password from the variable that names it), after
// bus.CheckAddr: a store is reached over loopback or the tailnet only (SPEC-TOKENS rule 6).
func openRedisBus(ctx context.Context, addr string) (bus.Store, func(), error) {
	getenv := os.Getenv
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	resolved, err := redisconn.Resolve(o, getenv)
	if err != nil {
		return nil, nil, err
	}
	if why := bus.CheckAddr(ctx, resolved.Addr, func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}); why != "" {
		return nil, nil, errors.New(why)
	}
	conn, err := redisconn.Open(ctx, resolved, getenv)
	if err != nil {
		return nil, nil, err
	}
	return bus.Redis{C: conn.Client()}, func() { _ = conn.Close() }, nil // ignored: close releases the read-only Redis connection and cannot change the fold
}

// readBus is --bus: the fold over the bus log at addr, read through w. A bus that cannot
// be reached is one unreadable source, which makes the run say NO (SPEC-TOKENS rule 3).
func readBus(w world, addr string, rules *tokens.Rules, now time.Time) []*tokens.Source {
	ctx, cancel := context.WithTimeout(context.Background(), busTimeout)
	defer cancel()
	st, closeStore, err := w.openBus(ctx, addr)
	if err != nil {
		s := &tokens.Source{Label: tokens.KindBus, Kind: tokens.KindBus, Path: addr, Basis: tokens.UTC}
		s.Unreadables = append(s.Unreadables, tokens.Unreadable{Label: s.Label, Path: addr, Why: err.Error()})
		return []*tokens.Source{s}
	}
	defer closeStore()
	return tokens.ReadBus(ctx, &bus.Bus{Store: st}, addr, rules, now)
}
