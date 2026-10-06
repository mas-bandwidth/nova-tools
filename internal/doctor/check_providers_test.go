package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errNoModelsServer stands for a provider whose models call is refused or cannot be made.
var errNoModelsServer = errors.New("connection refused")

// providerRig is a fake Env for the providers check: a fake exec that answers the
// routes (a fake exec), the seat's secrets store under t.TempDir() (a fake filesystem),
// an injected clock, and a fake provider client (the no-cost models call and the funds
// read). Nothing real runs and no socket is opened.
type providerRig struct {
	fakeEnv
	ProviderProbe
}

// fakeProbe is a ProviderProbe answering from tables: a provider in modelsErr does not
// answer its models list, a provider in unknown reports no funds, and a provider in funds
// reports that many dollars.
type fakeProbe struct {
	modelsErr map[string]error
	funds     map[string]float64
	unknown   map[string]bool
}

func (p fakeProbe) Models(_ context.Context, provider, _ string) error { return p.modelsErr[provider] }

func (p fakeProbe) Funds(_ context.Context, provider, _ string) (float64, bool, error) {
	if p.unknown[provider] {
		return 0, false, nil
	}
	v, ok := p.funds[provider]
	return v, ok, nil
}

// routesJSON is `nova-config route list --json` as the tool prints it: one object whose
// items carry each route row's fields.
func routesJSON(rows ...map[string]any) string {
	type item struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	}
	items := make([]item, 0, len(rows))
	for _, r := range rows {
		items = append(items, item{Kind: "route", Fields: r})
	}
	b, err := json.Marshal(map[string]any{
		"result": map[string]any{"verb": "route list", "status": "ok", "exit": 0},
		"facts":  map[string]any{"kind": "route", "rows": len(rows)},
		"items":  items,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// routeRow is one route's fields as the store holds them.
func routeRow(name, provider, model, note string, enabled bool) map[string]any {
	return map[string]any{"name": name, "provider": provider, "model": model, "note": note, "enabled": enabled}
}

// The check names an enabled route whose provider's key is not in the seat's secrets
// store, or whose provider is out of funds, and passes a route that is right; a disabled
// route is listed with its note and never checked (docs/SPEC-DOCTOR.md, "providers").
func TestDoctorProvidersCheckNamesARouteWithNoKeyOrNoFunds(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes, secrets string, probe ProviderProbe) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte(secrets), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator", "OPENROUTER_API_KEY": "sk-live"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: probe,
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	const withKey = "OPENROUTER_API_KEY: ENC[AES256_GCM,data:x]\nDEEPSEEK_API_KEY: ENC[AES256_GCM,data:y]\nsops:\n    age:\n        - recipient: age1qqqq\n"
	const noKey = "SOME_OTHER_KEY: ENC[AES256_GCM,data:z]\nsops:\n    age:\n        - recipient: age1qqqq\n"

	t.Run("a missing key is a fail naming the route", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("flash-a", "openrouter", "x-ai/grok-4", "", true)),
			noKey, fakeProbe{funds: map[string]float64{"openrouter": 5}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "flash-a")
		assert.Contains(t, r.Evidence, "OPENROUTER_API_KEY")
		assert.Contains(t, r.Evidence, "is not in the seat's secrets store")
		assert.Contains(t, r.Fix, "nova-secrets seal")
	})
	t.Run("a route out of funds is a fail naming the route", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("pro-a", "openrouter", "x-ai/grok-4", "", true)),
			withKey, fakeProbe{funds: map[string]float64{"openrouter": 0}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "pro-a")
		assert.Contains(t, r.Evidence, "out of funds")
		assert.Contains(t, r.Fix, "nova-sprint funded")
	})
	t.Run("a provider that does not answer its models list is a fail naming the route", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("flash-a", "deepseek", "deepseek-chat", "", true)),
			withKey, fakeProbe{modelsErr: map[string]error{"deepseek": errNoModelsServer}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "flash-a")
		assert.Contains(t, r.Evidence, "models list")
		assert.NotEmpty(t, r.Fix)
	})
	t.Run("a key in the store, models answered and funds over zero is ok", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("flash-a", "openrouter", "x-ai/grok-4", "", true)),
			withKey, fakeProbe{funds: map[string]float64{"openrouter": 4.2}})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "1 enabled route")
	})
	t.Run("a provider with no reported funds is not failed for funds", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("flash-a", "deepseek", "deepseek-chat", "", true)),
			withKey, fakeProbe{unknown: map[string]bool{"deepseek": true}})
		assert.Equal(t, OK, r.Status, r)
	})
	t.Run("a disabled route is listed with its note and never checked", func(t *testing.T) {
		t.Parallel()
		r := run(t,
			routesJSON(routeRow("flash-b", "openrouter", "x-ai/grok-4", "2 ok of 12; not suited to flash", false)),
			noKey, fakeProbe{funds: map[string]float64{"openrouter": 0}})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "flash-b")
		assert.Contains(t, r.Evidence, "2 ok of 12; not suited to flash")
		assert.NotContains(t, r.Evidence, "out of funds")
	})
	t.Run("a store with no route names no provider and is ok", func(t *testing.T) {
		t.Parallel()
		r := run(t, routesJSON(), noKey, fakeProbe{})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "no route")
	})
}
