//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// TestFnVerbsOnARedisServer runs fn check and fn load through run() and the
// production dial against a throwaway redis-server that asks for a password:
// check says MISSING, load LOADED, check OK, load UNCHANGED; other code put
// under the name makes check STALE and load REPLACED; and every function
// the library's files register answers on the store after the load.
func TestFnVerbsOnARedisServer(t *testing.T) {
	t.Parallel()
	const pw = "pw-from-nova-secrets"
	addr := testredis.Start(t, "--requirepass", pw)
	d := realDeps()
	d.getenv = func(k string) string {
		if k == PasswordEnv {
			return pw
		}
		return ""
	}
	sha := want(t)
	fnRun := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(args, &out, &errb, d)
		if errb.Len() != 0 {
			t.Errorf("%q: stderr %q", args, errb.String())
		}
		return code, out.String()
	}
	expect := func(verb string, code int, prefix string) {
		t.Helper()
		got, out := fnRun("fn", verb, "--addr", addr)
		if got != code || !strings.HasPrefix(out, prefix) || !strings.Contains(out, " store="+addr) {
			t.Fatalf("fn %s: exit %d %q; want exit %d and a line starting %q naming the store", verb, got, out, code, prefix)
		}
	}

	expect("check", 1, "MISSING nova_sprint loaded=none want="+sha+" ")
	expect("load", 0, "LOADED nova_sprint sha="+sha+" ")
	expect("check", 0, "OK nova_sprint loaded="+sha+" want="+sha+" ")
	expect("load", 0, "UNCHANGED nova_sprint sha="+sha+" ")

	c := redis.NewClient(&redis.Options{Addr: addr, Password: pw})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	names, err := library().Functions()
	if err != nil {
		t.Fatal(err)
	}
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "nova_sprint"}).Result()
	if err != nil || len(libs) != 1 {
		t.Fatalf("FUNCTION LIST: %v %v", libs, err)
	}
	onStore := map[string]bool{}
	for _, f := range libs[0].Functions {
		onStore[f.Name] = true
	}
	for _, n := range names {
		if !onStore[n] {
			t.Errorf("function %s is registered by the library's files and not on the store after fn load", n)
		}
	}

	other := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	if err := c.FunctionLoadReplace(ctx, other).Err(); err != nil {
		t.Fatal(err)
	}
	expect("check", 1, "STALE nova_sprint loaded=")
	expect("load", 0, "REPLACED nova_sprint sha="+sha+" was=")
	expect("check", 0, "OK nova_sprint loaded="+sha+" ")
}
