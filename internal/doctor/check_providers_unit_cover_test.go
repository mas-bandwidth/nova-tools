package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorProbe is a ProviderProbe that returns errors from Models and Funds for testing.
type errorProbe struct {
	modelsErr error
	fundsErr  error
}

func (p errorProbe) Models(_ context.Context, provider, key string) error {
	if p.modelsErr != nil {
		return p.modelsErr
	}
	return nil
}

func (p errorProbe) Funds(_ context.Context, provider, key string) (float64, bool, error) {
	if p.fundsErr != nil {
		return 0, false, p.fundsErr
	}
	return 0, false, nil
}

func TestDoctorProvidersCoverCheckProvidersFailSecretsEnv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := fakeEnv{
		env:  map[string]string{},
		root: root,
		exec: func(string, ...string) (string, error) { return routesJSON(routeRow("r", "openrouter", "m", "", true)), nil },
	}
	r := NewRegistry()
	r.Register(Default.checks["providers"])
	res, _, err := r.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "could not be read")
	assert.Contains(t, res[0].Fix, "nova-secrets names")
}

func TestDoctorProvidersCoverCheckProvidersFailMissingYaml(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	env := fakeEnv{
		env: map[string]string{
			"NOVA_SECRETS_STORE":    "secrets",
			"NOVA_SECRETS_SEAT": "coordinator",
		},
		root: root,
		exec: func(string, ...string) (string, error) { return routesJSON(routeRow("r", "openrouter", "m", "", true)), nil },
	}
	r := NewRegistry()
	r.Register(Default.checks["providers"])
	res, _, err := r.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "could not be read")
	assert.Contains(t, res[0].Fix, "nova-secrets names")
}

func TestDoctorProvidersCoverCheckProvidersFailRoutesExecErr(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
	env := fakeEnv{
		env: map[string]string{
			"NOVA_SECRETS_STORE":    "secrets",
			"NOVA_SECRETS_SEAT": "coordinator",
			"OPENROUTER_API_KEY": "sk-live",
		},
		root: root,
		exec: func(string, ...string) (string, error) { return "", errors.New("exec error") },
	}
	r := NewRegistry()
	r.Register(Default.checks["providers"])
	res, _, err := r.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "could not be read")
	assert.Contains(t, res[0].Fix, "nova-config route list --json")
}

func TestDoctorProvidersCoverCheckProvidersFailRoutesNotJson(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
	env := fakeEnv{
		env: map[string]string{
			"NOVA_SECRETS_STORE":    "secrets",
			"NOVA_SECRETS_SEAT": "coordinator",
			"OPENROUTER_API_KEY": "sk-live",
		},
		root: root,
		exec: func(string, ...string) (string, error) { return "not json", nil },
	}
	r := NewRegistry()
	r.Register(Default.checks["providers"])
	res, _, err := r.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "could not be read")
	assert.Contains(t, res[0].Fix, "nova-config route list --json")
}

func TestDoctorProvidersCoverCheckProvidersModelsNoModels(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes string) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator", "OPENROUTER_API_KEY": "sk-live"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: errorProbe{modelsErr: errNoModels},
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	r := run(t, routesJSON(routeRow("r", "custom", "m", "", true)))
	assert.Equal(t, OK, r.Status)
	assert.Contains(t, r.Evidence, "not checked")
}

func TestDoctorProvidersCoverCheckProvidersFundsError(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes string) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator", "OPENROUTER_API_KEY": "sk-live"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: errorProbe{fundsErr: errors.New("funds error")},
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	r := run(t, routesJSON(routeRow("r", "openrouter", "m", "", true)))
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "funds could not be read")
	assert.Contains(t, r.Fix, "nova-sprint funded")
}

func TestDoctorProvidersCoverCheckProvidersOrFix(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes string) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("SOME_OTHER_KEY: ENC[x]\nsops:\n"), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: errorProbe{},
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	r := run(t, routesJSON(
		routeRow("r1", "openrouter", "m", "", true),
		routeRow("r2", "deepseek", "m", "", true),
	))
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "r1")
	assert.Contains(t, r.Evidence, "r2")
	assert.Contains(t, r.Fix, "nova-secrets seal")
}

func TestDoctorProvidersCoverCheckProvidersDisabledNoNote(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes string) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator", "OPENROUTER_API_KEY": "sk-live"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: errorProbe{},
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	r := run(t, routesJSON(routeRow("r", "openrouter", "m", "", false)))
	assert.Equal(t, OK, r.Status)
	assert.Contains(t, r.Evidence, "disabled")
	assert.Contains(t, r.Evidence, "no note")
}

func TestDoctorProvidersCoverCheckProvidersSkipNonRouteKind(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, routes string) Result {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("OPENROUTER_API_KEY: ENC[x]\nsops:\n"), 0o644))
		env := providerRig{
			fakeEnv: fakeEnv{
				env:   map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator", "OPENROUTER_API_KEY": "sk-live"},
				root:  root,
				exec:  func(string, ...string) (string, error) { return routes, nil },
				clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
			},
			ProviderProbe: errorProbe{},
		}
		r := NewRegistry()
		r.Register(Default.checks["providers"])
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	routesOther := `{"result":{"verb":"route list","status":"ok","exit":0},"facts":{"kind":"route","rows":2},"items":[{"kind":"other","fields":{"name":"x","provider":"y","model":"z","enabled":"true"}},{"kind":"route","fields":{"name":"r","provider":"openrouter","model":"m","enabled":"true"}}]}`
	r := run(t, routesOther)
	assert.Equal(t, OK, r.Status)
	assert.Contains(t, r.Evidence, "1 enabled route")
}

func TestDoctorProvidersCoverSecretNamesIndented(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := fakeEnv{env: map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator"}, root: root}
	names, err := secretNames(env)
	require.Error(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("  indented: value\nkey: value\n"), 0o644))
	names, err = secretNames(env)
	require.NoError(t, err)
	assert.True(t, names["key"])
	assert.False(t, names["  indented"])
}

func TestDoctorProvidersCoverSecretNamesComments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("# comment\nkey: value\n"), 0o644))
	env := fakeEnv{env: map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator"}, root: root}
	names, err := secretNames(env)
	require.NoError(t, err)
	assert.True(t, names["key"])
}

func TestDoctorProvidersCoverSecretNamesNoColon(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("nokeyoneline: value\n"), 0o644))
	env := fakeEnv{env: map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator"}, root: root}
	names, err := secretNames(env)
	require.NoError(t, err)
	assert.True(t, names["nokeyoneline"])
}

func TestDoctorProvidersCoverSecretNamesSopsBreak(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "coordinator.yaml"), []byte("key: value\nsops:\n    age:\n"), 0o644))
	env := fakeEnv{env: map[string]string{"NOVA_SECRETS_STORE": "secrets", "NOVA_SECRETS_SEAT": "coordinator"}, root: root}
	names, err := secretNames(env)
	require.NoError(t, err)
	assert.True(t, names["key"])
	assert.False(t, names["age"])
}

func TestDoctorProvidersCoverProviderProbeForFakeEnv(t *testing.T) {
	t.Parallel()
	env := fakeEnv{}
	probe := providerProbeFor(env)
	assert.NotNil(t, probe)
}

func TestDoctorProvidersCoverHTTPProbeModelsNoModels(t *testing.T) {
	t.Parallel()
	probe := httpProbe{}
	err := probe.Models(context.Background(), "unknown", "key")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, errNoModels))
}

func TestDoctorProvidersCoverHTTPProbeFundsOpenrouterEmptyKey(t *testing.T) {
	t.Parallel()
	probe := httpProbe{}
	v, known, err := probe.Funds(context.Background(), "openrouter", "")
	assert.NoError(t, err)
	assert.False(t, known)
	assert.Zero(t, v)
}

func TestDoctorProvidersCoverHTTPProbeFundsOtherProvider(t *testing.T) {
	t.Parallel()
	probe := httpProbe{}
	v, known, err := probe.Funds(context.Background(), "other", "key")
	assert.NoError(t, err)
	assert.False(t, known)
	assert.Zero(t, v)
}
