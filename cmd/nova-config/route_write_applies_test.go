package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Route commands implement ConfigApply.tla NoSilentUnappliedWrite: the
// committed history survives a failed copy, whose command cannot say OK.
func TestRouteWriteApplies(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"add", "set", "remove"} {
		for _, failure := range []string{"", "unreachable", "refused", "missing fleet", "ahead"} {
			for _, asJSON := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", op, failure, asJSON), func(t *testing.T) {
					t.Parallel()
					h := loopHarness(t)
					ctx := context.Background()
					addProviders(t, h.store, "p")
					h.machine(t, "store-host", "2")
					_, _, err := h.store.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"store": "store-host", "redis_port": "6381"}, "a1")
					require.NoError(t, err)
					if op != "add" {
						k, _ := config.Lookup(config.KindRoute)
						row, err := k.NewRow("r", map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input": "1"})
						require.NoError(t, err)
						_, err = h.store.Insert(ctx, config.KindRoute, row, "a1")
						require.NoError(t, err)
						_, err = config.Apply(ctx, h.store, h.redis, config.KindRoute, "a1", false, func(config.Op) {})
						require.NoError(t, err)
					}
					before, err := h.store.Rev(ctx, config.KindRoute)
					require.NoError(t, err)
					if failure == "ahead" {
						h.redis.revs[config.KindRoute] = 999
					}
					applied := h.redis.revs[config.KindRoute]
					if failure == "missing fleet" {
						h.override = missingRouteFleet{h.store}
					}
					d := h.deps()
					d.openRedis = func(_ context.Context, addr string) (redisSide, error) {
						assert.Equal(t, "store-host:6381", addr, "the fleet endpoint overrides a stale environment endpoint")
						if failure == "unreachable" {
							return nil, errors.New("copy unreachable")
						}
						if failure == "refused" {
							return refusingRouteCopy{h.redis}, nil
						}
						return h.redis, nil
					}
					args := []string{"route", op, "r", "--actor", "a1", "--pg", dsn}
					switch op {
					case "add":
						args = append(args, "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60", "--price_input", "1")
					case "set":
						args = append(args, "--enabled", "false", "--note", "paused")
					}
					if asJSON {
						args = append(args, "--json")
					}
					var out, errs bytes.Buffer
					code := run(args, &out, &errs, d)
					rev, err := h.store.Rev(ctx, config.KindRoute)
					require.NoError(t, err)
					require.Greater(t, rev, before)
					history, err := h.store.History(ctx, config.KindRoute, "r")
					require.NoError(t, err)
					require.NotEmpty(t, history)
					assert.Equal(t, rev, history[len(history)-1].ID)
					if asJSON {
						var result struct {
							Result struct {
								Exit   int    `json:"exit"`
								Remedy string `json:"remedy"`
							} `json:"result"`
							Facts map[string]json.RawMessage `json:"facts"`
						}
						require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
						assert.Equal(t, code, result.Result.Exit)
						assert.Contains(t, result.Facts, "applied")
						if failure != "" {
							assert.Equal(t, 1, code)
							assert.Contains(t, result.Result.Remedy, "nova-config apply --kind route")
							assert.JSONEq(t, `"NOT-APPLIED"`, string(result.Facts["apply_status"]))
						} else {
							assert.Zero(t, code)
							assert.JSONEq(t, `"APPLY"`, string(result.Facts["apply_status"]))
						}
					} else if failure != "" {
						assert.Equal(t, 1, code, errs.String())
						assert.Contains(t, errs.String(), fmt.Sprintf("CONFIG NOT-APPLIED kind=route rev=%d applied=", rev))
						assert.Contains(t, errs.String(), "run: nova-config apply --kind route --actor a1 --pg "+dsn)
						if failure != "missing fleet" {
							assert.Contains(t, errs.String(), "--redis store-host:6381")
						}
						assert.Equal(t, applied, h.redis.revs[config.KindRoute])
						assert.NotContains(t, out.String(), " OK")
					} else {
						assert.Zero(t, code, errs.String())
						assert.Contains(t, out.String(), "CONFIG APPLY kind=route")
						assert.Contains(t, out.String(), fmt.Sprintf("rev=%d", rev))
						assert.GreaterOrEqual(t, h.redis.revs[config.KindRoute], rev)
						if op == "remove" {
							assert.NotContains(t, h.redis.views[config.KindRoute], "r")
						}
						if op == "set" {
							assert.Equal(t, "false", h.redis.views[config.KindRoute]["r"]["enabled"])
						}
					}
				})
			}
		}
	}
}

type refusingRouteCopy struct{ *fakeRedis }

func (r refusingRouteCopy) Prepare(context.Context) error {
	return &config.RefusedError{Err: config.ErrActor, Detail: "actor refused"}
}

func TestRouteWriteDryRunAppliesNothing(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"add", "set", "remove"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			h := loopHarness(t)
			addProviders(t, h.store, "p")
			args := []string{"route", op, "r", "--dry-run"}
			fields := map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60"}
			if op != "add" {
				k, _ := config.Lookup(config.KindRoute)
				row, err := k.NewRow("r", fields)
				require.NoError(t, err)
				_, err = h.store.Insert(context.Background(), config.KindRoute, row, "a1")
				require.NoError(t, err)
			}
			if op == "add" {
				args = append(args, "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60")
			}
			if op == "set" {
				args = append(args, "--tokens", "42")
			}
			before, err := h.store.Rev(context.Background(), config.KindRoute)
			require.NoError(t, err)
			code, out, errs := h.run(t, args...)
			assert.Zero(t, code, errs)
			assert.Contains(t, out, "CONFIG DRY-RUN")
			assert.Zero(t, h.redis.opens)
			after, err := h.store.Rev(context.Background(), config.KindRoute)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

// missingRouteFleet exercises a genuinely missing fleet row without changing
// the store's validation rules or bypassing the committed route write.
type missingRouteFleet struct{ pgStore }

func (m missingRouteFleet) Get(ctx context.Context, kind, name string) (config.Row, bool, error) {
	if kind == config.KindFleet {
		return config.Row{}, false, nil
	}
	return m.pgStore.Get(ctx, kind, name)
}

func TestRouteApplySelectsFleetUnlessExplicit(t *testing.T) {
	t.Parallel()
	for _, explicit := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/missing=%v", explicit, missing), func(t *testing.T) {
				t.Parallel()
				h := loopHarness(t)
				h.machine(t, "store-host", "2")
				_, _, err := h.store.Update(context.Background(), config.KindFleet, config.KindFleet, map[string]string{"store": "store-host", "redis_port": "6381"}, "a1")
				require.NoError(t, err)
				if missing {
					h.override = missingRouteFleet{h.store}
				}
				d := h.deps()
				opened := ""
				d.openRedis = func(_ context.Context, addr string) (redisSide, error) { opened = addr; return h.redis, nil }
				args := []string{"apply", "--kind", "route"}
				if explicit {
					args = append(args, "--redis", "override:6382")
				}
				var out, errs bytes.Buffer
				code := run(args, &out, &errs, d)
				switch {
				case explicit:
					assert.Zero(t, code, errs.String())
					assert.Equal(t, "override:6382", opened)
				case missing:
					assert.NotZero(t, code)
					assert.Empty(t, opened, "missing fleet must not fall back to the stale ambient Redis")
				default:
					assert.Zero(t, code, errs.String())
					assert.Equal(t, "store-host:6381", opened)
				}
			})
		}
	}
}

// A store write just after List must not be advertised by the copy's stamp.
// The route revision is a lower bound read before its rows, not after them.
func TestRouteApplyNeverStampsUnreadRevision(t *testing.T) {
	t.Parallel()
	h := loopHarness(t)
	addProviders(t, h.store, "p")
	k, _ := config.Lookup(config.KindRoute)
	row, err := k.NewRow("r", map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input": "1"})
	require.NoError(t, err)
	rev, err := h.store.Insert(context.Background(), config.KindRoute, row, "a1")
	require.NoError(t, err)
	st := routeWriteAfterSnapshot{h.store}
	res, err := config.Apply(context.Background(), st, h.redis, config.KindRoute, "a1", false, func(config.Op) {})
	require.NoError(t, err)
	assert.Equal(t, rev, res.Rev)
	assert.Equal(t, rev, h.redis.revs[config.KindRoute])
	assert.Equal(t, "true", h.redis.views[config.KindRoute]["r"]["enabled"])
	current, err := h.store.Rev(context.Background(), config.KindRoute)
	require.NoError(t, err)
	assert.Greater(t, current, rev, "the later committed write still needs its own apply")
}

type routeWriteAfterSnapshot struct{ pgStore }

func (s routeWriteAfterSnapshot) List(ctx context.Context, kind string) ([]config.Row, error) {
	rows, err := s.pgStore.List(ctx, kind)
	if err == nil && kind == config.KindRoute {
		_, _, err = s.pgStore.Update(ctx, kind, "r", map[string]string{"enabled": "false", "note": "later write"}, "a1")
	}
	return rows, err
}

func TestRouteWriteUnpricedCommitCanBeRepaired(t *testing.T) {
	t.Parallel()
	h := loopHarness(t)
	addProviders(t, h.store, "p")
	h.machine(t, "store-host", "2")
	_, _, err := h.store.Update(context.Background(), config.KindFleet, config.KindFleet, map[string]string{"store": "store-host", "redis_port": "6381"}, "a1")
	require.NoError(t, err)
	code, _, errs := h.run(t, "route", "add", "r", "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "CONFIG NOT-APPLIED kind=route")
	assert.Contains(t, errs, "is enabled with no price")
	assert.Contains(t, errs, "nova-config route set r --price_input")
	assert.Contains(t, errs, "run: nova-config apply --kind route --actor a1 --redis store-host:6381")
	assert.Zero(t, h.redis.revs[config.KindRoute])
	code, out, errs := h.run(t, "route", "set", "r", "--price_input", "1")
	assert.Zero(t, code, errs)
	assert.Contains(t, out, "CONFIG APPLY kind=route")
	history, err := h.store.History(context.Background(), config.KindRoute, "r")
	require.NoError(t, err)
	require.Len(t, history, 2, "repair preserves both committed writes")
	assert.Equal(t, history[1].ID, h.redis.revs[config.KindRoute])
	assert.Equal(t, "1", h.redis.views[config.KindRoute]["r"]["price_input"])
}
