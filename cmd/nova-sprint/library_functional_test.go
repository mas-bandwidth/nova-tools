//go:build functional

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// A store without this build's table functions is refused before any verb
// runs, with the command that loads them; with them loaded, the verb runs.
func TestAStoreWithoutThisBuildsLibraryIsRefused(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	var out, errb bytes.Buffer
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	code := a.run([]string{"init"}, &out, &errb)
	require.Equal(t, 2, code, "init on a store with no library: %d %s", code, errb.String())
	require.Contains(t, errb.String(), "nova-redis fn load --addr "+addr, "init on a store with no library: %d %s", code, errb.String())
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, fn.Load(context.Background(), c))
	b := newApp(func(k string) string { return env[k] })
	defer b.close()
	code = b.run([]string{"init"}, &out, &errb)
	require.Equal(t, 0, code, "init with the library loaded: %s", errb.String())
}
