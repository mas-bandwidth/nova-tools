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
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	var out, errb bytes.Buffer
	a := newApp(func(k string) string { return env[k] })
	a.newPath = false // the present path's library check, behind the switch
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
	b.newPath = false
	defer b.close()
	if code := b.run([]string{"init"}, &out, &errb); code != 0 {
		t.Fatalf("init with the library loaded: %s", errb.String())
	}
}

// TestNewPathRefusesAStoreWithoutThisBuildsLibrary: the new path's library
// check (the grammar decisions, 30): one FUNCTION LIST before the client's
// first call refuses a store whose nova_sprint library is not this build's,
// exit 2, naming the library, with nothing written; the legacy library is not
// this build's (the sprint profile, which does not assemble before G0, so no
// store matches it yet).
func TestNewPathRefusesAStoreWithoutThisBuildsLibrary(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	for i, load := range []bool{false, true} {
		if load {
			if err := fn.Load(context.Background(), c); err != nil {
				t.Fatal(err)
			}
		}
		var out, errb bytes.Buffer
		a := newApp(func(k string) string { return env[k] })
		code := a.run([]string{"init"}, &out, &errb)
		a.close()
		if code != exitRefused || !strings.Contains(errb.String()+out.String(), "nova_sprint") {
			t.Fatalf("case %d: init on the new path: exit %d\n%s%s", i, code, out.String(), errb.String())
		}
		if n, err := c.DBSize(context.Background()).Result(); err != nil || n != 0 {
			t.Fatalf("case %d: the store holds %d keys (%v) after a refused init", i, n, err)
		}
	}
}

