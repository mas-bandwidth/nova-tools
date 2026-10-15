package config

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func migratedFileStore(t *testing.T) *FileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "try.json")
	st, err := OpenFile(path)
	require.NoError(t, err)
	_, _, _, err = st.Migrate(context.Background())
	require.NoError(t, err)
	return st
}

func addMachineRow(t *testing.T, st Store, name, roles string) {
	t.Helper()
	k, _ := Lookup(KindMachine)
	row, err := k.NewRow(name, map[string]string{"user": "u", "seat": "s", "slots": "1", "roles": roles})
	require.NoError(t, err)
	_, err = st.Insert(context.Background(), KindMachine, row, "operator")
	require.NoError(t, err)
}

func putProvider(t *testing.T, st Store, name, endpoint string) error {
	t.Helper()
	k, _ := Lookup(KindProvider)
	raw := map[string]string{"billing": "subscription", "funded": "true"}
	if endpoint != "" {
		raw["endpoint"] = endpoint
	}
	row, err := k.NewRow(name, raw)
	if err != nil {
		return err
	}
	_, err = st.Insert(context.Background(), KindProvider, row, "operator")
	return err
}

func engineBase(scheme, host, path string) string {
	return scheme + "://" + host + ":11434" + path
}

func TestALocalProviderNamesItsEndpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := migratedFileStore(t)
	addMachineRow(t, st, "g1", "bench")

	require.NoError(t, putProvider(t, st, "local-g1", engineBase("http", "g1", "/v1")))
	got, found, err := st.Get(ctx, KindProvider, "local-g1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, engineBase("http", "g1", "/v1"), got.Fields["endpoint"])

	for _, tc := range []struct {
		name, provider, endpoint, want string
	}{
		{"no endpoint", "local-bare", "", "endpoint"},
		{"other provider", "openrouter", engineBase("http", "g1", "/v1"), "endpoint"},
		{"public address", "local-public", engineBase("http", "8.8.8.8", "/v1"), "8.8.8.8"},
		{"not http", "local-https", engineBase("https", "g1", "/v1"), "http"},
		{"no v1 path", "local-path", engineBase("http", "g1", "/api"), "/v1"},
		{"no machine", "local-missing", engineBase("http", "missing", "/v1"), "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := putProvider(t, st, tc.provider, tc.endpoint)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestALocalEndpointOnTheCoordinatingMachineIsRefused(t *testing.T) {
	t.Parallel()
	st := migratedFileStore(t)
	addMachineRow(t, st, "hub", "bench,coordination")
	err := putProvider(t, st, "local-hub", engineBase("http", "hub", "/v1"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "hub")
	assert.ErrorContains(t, err, "coordination")
	assert.ErrorContains(t, err, "bench machine")
}
