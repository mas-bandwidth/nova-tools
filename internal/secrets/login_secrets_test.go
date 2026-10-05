package secrets

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A fake secrets store: the server's decision key and a member's route key
// are read in process; no environment variable carries either; a missing one
// refuses naming it; no test sleeps on the wall clock and no secret value
// appears in any output, file or child environment.
func TestEveryUnitKeyIsReadInProcessNeverFromTheEnvironment(t *testing.T) {
	t.Parallel()
	const (
		decisionKey = "JEV_API_KEY"
		decisionVal = "jev-proc-key-77"
		routeKey    = "DEEPSEEK_API_KEY"
		routeVal    = "deepseek-proc-key-88"
	)

	// No environment variable carries either key.
	assert.Empty(t, os.Getenv(decisionKey), "environment must not carry decision key")
	assert.Empty(t, os.Getenv(routeKey), "environment must not carry route key")

	l := Login{
		Store:   "/secrets",
		As:      "bench-a",
		Key:     "/k/bench-a.key",
		Sops:    "/bin/sops",
		Name:    "NOVA_REDIS_BENCH_PASSWORD",
		Secrets: []string{decisionKey, routeKey},
	}

	open := func(store, as, key, sops string) (SeatFile, error) {
		assert.Equal(t, []string{"/secrets", "bench-a", "/k/bench-a.key", "/bin/sops"}, []string{store, as, key, sops})
		return SeatFile{
			Path: "/secrets/bench-a.yaml",
			Secrets: map[string]Secret{
				"NOVA_REDIS_BENCH_PASSWORD": NewSecret("redis-pw"),
				decisionKey:                 NewSecret(decisionVal),
				routeKey:                    NewSecret(routeVal),
				"EMPTY":                     NewSecret(""),
			},
		}, nil
	}

	// Read in process.
	secs, err := readLoginSecrets(l, open)
	require.NoError(t, err)
	require.Contains(t, secs, decisionKey)
	require.Contains(t, secs, routeKey)

	var gotDecision, gotRoute string
	require.NoError(t, secs[decisionKey].Use(func(v string) error { gotDecision = v; return nil }))
	require.NoError(t, secs[routeKey].Use(func(v string) error { gotRoute = v; return nil }))
	assert.Equal(t, decisionVal, gotDecision)
	assert.Equal(t, routeVal, gotRoute)

	// No secret value appears in any output, file or child environment.
	for _, sec := range []Secret{secs[decisionKey], secs[routeKey]} {
		assert.False(t, Leaks(fmt.Sprintf("%v %s %+v %#v", sec, sec, l, l), sec), "secret value must not leak into representations")
	}

	// Missing secret refuses naming it and the remedy.
	missingL := l
	missingL.Secrets = []string{decisionKey, "MISSING_ROUTE_KEY"}
	_, err = readLoginSecrets(missingL, open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat bench-a of store /secrets holds no MISSING_ROUTE_KEY")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /secrets --as bench-a")

	// Empty secret refuses naming it.
	emptyL := l
	emptyL.Secrets = []string{"EMPTY"}
	_, err = readLoginSecrets(emptyL, open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat bench-a of store /secrets holds EMPTY empty, and an empty value is no secret")
	assert.Contains(t, err.Error(), "run: nova-secrets seal --store /secrets --as bench-a --key /k/bench-a.key --sops /bin/sops --name EMPTY")
}
