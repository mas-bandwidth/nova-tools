package main

// The one network this tool reaches besides the ledger store: the Redis bus whose log
// `--bus` reads (SPEC-TOKENS rule 6; docs/SPEC-BUS.md). It is read only: XRANGE of
// bus2:log and the roster, through internal/bus.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
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

type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

var silenceOnce sync.Once

// openRedisBus dials the bus store at addr with the fleet seat (redisauth.Auth: the
// ACL user from NOVA_SPRINT_REDIS_USER, its password from the variable that names it),
// after bus.CheckAddr: a store is reached over loopback or the tailnet only.
func openRedisBus(ctx context.Context, addr string) (bus.Store, func(), error) {
	if why := bus.CheckAddr(ctx, addr, func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}); why != "" {
		return nil, nil, errors.New(why)
	}
	user, password, err := redisauth.Auth("", "")
	if err != nil {
		return nil, nil, err
	}
	silenceOnce.Do(func() { redis.SetLogger(quietRedis{}) })
	c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password, DisableIdentity: true})
	return bus.Redis{C: c}, func() { _ = c.Close() }, nil // ignored: the read is done before the close, which has nothing to add
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
