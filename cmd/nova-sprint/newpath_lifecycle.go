package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Layer 1's lifecycle on the new path (the L1 contract amendment, lifecycle,
// 2026-09-30): the one route to the store beside sprintfn.Client, and only
// here. init defines the namespace through it before its clock step, and
// teardown deletes it; each is one of Layer 1's library functions,
// ns_tset_define and ns_tset_teardown.

// lifecycleHandle is Layer 1's lifecycle as the new path holds it.
type lifecycleHandle = tset.Lifecycle

// sprintBuild is this build's sprint library, which define names so that a
// store holding another build's library refuses BUILD.
func sprintBuild() (string, error) { return fn.TSetBuild(fn.TSetSprint) }

// newPathLifecycle is the production lifecycle of the new path: Layer 1's
// Redis store at the address, with the login newPathClient dials with.
func (a *app) newPathLifecycle(_ context.Context, addr string) (tset.Lifecycle, func() error, error) {
	user, passwordEnv := a.getenv(redisauth.UserEnv), ""
	if user != "" {
		passwordEnv = a.getenv(redisauth.PasswordEnvEnv)
		if passwordEnv == "" {
			passwordEnv = redisauth.DefaultPasswordEnv
		}
	}
	r, err := tset.NewRedis(addr, user, passwordEnv)
	if err != nil {
		return nil, nil, err
	}
	return r, r.Close, nil
}

// lifecycleAt is the lifecycle at an address, opened once per process as
// the clients are.
func (a *app) lifecycleAt(ctx context.Context, addr string) (lifecycleHandle, error) {
	ps := a.pathState()
	if lc, ok := ps.lifecycles[addr]; ok {
		return lc, nil
	}
	lc, closer, err := a.lifecycle(ctx, addr)
	if err != nil {
		return nil, err
	}
	ps.lifecycles[addr] = lc
	if closer != nil {
		ps.closers = append(ps.closers, closer)
	}
	return lc, nil
}
