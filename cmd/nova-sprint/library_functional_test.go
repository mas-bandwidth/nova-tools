//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// A store without this build's table functions is refused before any verb
// runs, with the command that loads them; with them loaded, the verb runs.
func TestAStoreWithoutThisBuildsLibraryIsRefused(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_PREFIX": "f-", "NOVA_SPRINT_ACTOR": "coordinator"}
	var out, errb bytes.Buffer
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	if code := a.run([]string{"init"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "nova-redis fn load --addr "+addr) {
		t.Fatalf("init on a store with no library: %d %s", code, errb.String())
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	b := newApp(func(k string) string { return env[k] })
	defer b.close()
	if code := b.run([]string{"init"}, &out, &errb); code != 0 {
		t.Fatalf("init with the library loaded: %s", errb.String())
	}
}
