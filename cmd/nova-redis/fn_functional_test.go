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

	expect("check", 1, "MISSING nova_sprint sha="+sha+" loaded=none want="+sha+" ")
	expect("load", 0, "LOADED nova_sprint sha="+sha+" ")
	expect("check", 0, "OK nova_sprint sha="+sha+" loaded="+sha+" want="+sha+" ")
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
	expect("check", 1, "STALE nova_sprint sha="+sha+" loaded=")
	expect("load", 0, "REPLACED nova_sprint sha="+sha+" was=")
	expect("check", 0, "OK nova_sprint sha="+sha+" loaded="+sha+" ")
}

// TestFnVerbsLogInAsTheACLUser: on a store whose default user is off, fn load
// and fn check log in as the user --user (or NOVA_REDIS_USER) names, with
// NOVA_REDIS_PASSWORD. As a user with the coordinator seat's rules they load
// and check; as a user without FUNCTION, load is one FAILED line naming
// NOPERM and the remedy, exit 1, and the store holds no library.
func TestFnVerbsLogInAsTheACLUser(t *testing.T) {
	t.Parallel()
	const pw = "pw-from-nova-secrets"
	// The coordinator seat's rules (internal/nsprint/acl/testdata/acl-rows.tsv),
	// and the same with FUNCTION taken away.
	addr := testredis.Start(t,
		"--user", "default", "off",
		"--user", "fnuser", "on", ">"+pw, "~*", "&*", "+@all", "-@dangerous", "+info", "+config|get",
		"--user", "nofn", "on", ">"+pw, "~*", "&*", "+@all", "-@dangerous", "-function")
	env := map[string]string{PasswordEnv: pw}
	d := realDeps()
	d.getenv = func(k string) string { return env[k] }
	fnRun := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(args, &out, &errb, d)
		return code, out.String(), errb.String()
	}
	sha := want(t)

	code, out, errOut := fnRun("fn", "load", "--addr", addr, "--user", "nofn")
	if code != 1 || out != "" || strings.Count(errOut, "\n") != 1 ||
		!strings.HasPrefix(errOut, "FAILED nova_sprint sha="+sha+" store="+addr+" ") ||
		!strings.Contains(errOut, "NOPERM") || !strings.Contains(errOut, "remedy=") {
		t.Fatalf("fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	}

	env[UserEnv] = "fnuser" // the environment's user, no flag
	if code, out, errOut = fnRun("fn", "check", "--addr", addr); code != 1 || !strings.HasPrefix(out, "MISSING nova_sprint sha="+sha+" loaded=none ") || errOut != "" {
		t.Fatalf("fn check as NOVA_REDIS_USER=fnuser after the refused load: exit %d %q %q; want MISSING", code, out, errOut)
	}
	if code, out, errOut = fnRun("fn", "load", "--addr", addr); code != 0 || !strings.HasPrefix(out, "LOADED nova_sprint sha="+sha+" ") || errOut != "" {
		t.Fatalf("fn load as NOVA_REDIS_USER=fnuser: exit %d %q %q; want LOADED", code, out, errOut)
	}
	delete(env, UserEnv) // the flag's user
	if code, out, errOut = fnRun("fn", "check", "--addr", addr, "--user", "fnuser"); code != 0 || !strings.HasPrefix(out, "OK nova_sprint sha="+sha+" loaded="+sha+" want="+sha+" ") || errOut != "" {
		t.Fatalf("fn check --user fnuser: exit %d %q %q; want OK", code, out, errOut)
	}
	// With no user the default user is used, and it is off.
	if code, _, errOut = fnRun("fn", "check", "--addr", addr); code != 2 || !strings.Contains(errOut, "NOAUTH") && !strings.Contains(errOut, "WRONGPASS") {
		t.Fatalf("fn check with no user on a store whose default user is off: exit %d %q; want exit 2 naming the refused login", code, errOut)
	}
}
