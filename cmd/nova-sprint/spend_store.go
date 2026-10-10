package main

// The release spend gate's store readout (internal/release, spendcheck.go): what the sprint's
// store recorded over a release's window. The store's tables are the sprint's, so the reader
// lives here and is handed to the release library, which reads no sprint store of its own.

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() { release.SpendStore = spendStore }

// snapshotSpend is release.RecordedSpend over one read of the store's tables: the work
// table's cost records and the routes (sprint.RecordedSpendIn, cost_spend.go).
type snapshotSpend struct{ s *sprint.Snapshot }

// Providers is the paid providers the snapshot knows of over the window.
func (s snapshotSpend) Providers(_ context.Context, w release.SpendWindow) ([]string, error) {
	return sprint.RecordedProvidersIn(s.s, w.From, w.To), nil
}

// Spend is the snapshot's dollars of the provider over the window.
func (s snapshotSpend) Spend(_ context.Context, provider string, w release.SpendWindow) (float64, error) {
	return sprint.RecordedSpendIn(s.s, provider, w.From, w.To), nil
}

// Tokens is the snapshot's tokens of each subscription friend over the window.
func (s snapshotSpend) Tokens(_ context.Context, w release.SpendWindow) (map[string]int64, error) {
	return sprint.RecordedTokensIn(s.s, w.From, w.To), nil
}

// spendStore reads the work and fleet tables and the routes of the store at addr, logged in
// as nova-sprint logs in from the environment (NOVA_SPRINT_REDIS_USER and the variable
// NOVA_SPRINT_REDIS_PASSWORD_ENV names).
func spendStore(ctx context.Context, addr string, getenv func(string) string) (release.RecordedSpend, error) {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := redisconn.Open(dctx, o, getenv)
	if err != nil {
		return nil, err
	}
	defer conn.Close() // ignored: a read-only connection; what was read is kept
	st := &store.Store{B: &store.Redis{C: conn.Client(), Names: sprint.Names{}, Now: time.Now}, Names: sprint.Names{}, Actor: "nova-update", Now: time.Now, NewID: store.NewID}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet}, nil)
	if err != nil {
		return nil, err
	}
	if s.Routes, _, err = st.Routes(ctx); err != nil {
		return nil, err
	}
	return snapshotSpend{s: s}, nil
}
