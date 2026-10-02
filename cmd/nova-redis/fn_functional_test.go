//go:build functional

package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Zero(t, errb.Len(), "%q: stderr %q", args, errb.String())
		return code, out.String()
	}
	expect := func(verb string, code int, prefix string) {
		t.Helper()
		got, out := fnRun("fn", verb, "--addr", addr)
		require.Equal(t, code, got, "fn %s: exit %d %q; want exit %d and a line starting %q naming the store", verb, got, out, code, prefix)
		require.True(t, strings.HasPrefix(out, prefix), "fn %s: exit %d %q; want exit %d and a line starting %q naming the store", verb, got, out, code, prefix)
		require.Contains(t, out, " store="+addr, "fn %s: exit %d %q; want exit %d and a line starting %q naming the store", verb, got, out, code, prefix)
	}

	expect("check", 1, "MISSING nova_sprint sha="+sha+" loaded=none want="+sha+" ")
	expect("load", 0, "LOADED nova_sprint sha="+sha+" ")
	expect("check", 0, "OK nova_sprint sha="+sha+" loaded="+sha+" want="+sha+" ")
	expect("load", 0, "UNCHANGED nova_sprint sha="+sha+" ")

	c := redis.NewClient(&redis.Options{Addr: addr, Password: pw})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	names, err := library().Functions()
	require.NoError(t, err, err)
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "nova_sprint"}).Result()
	require.NoError(t, err, "FUNCTION LIST: %v %v", libs, err)
	require.Len(t, libs, 1, "FUNCTION LIST: %v %v", libs, err)
	onStore := map[string]bool{}
	for _, f := range libs[0].Functions {
		onStore[f.Name] = true
	}
	for _, n := range names {
		assert.True(t, onStore[n], "function %s is registered by the library's files and not on the store after fn load", n)
	}

	other := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	{
		err := c.FunctionLoadReplace(ctx, other).Err()
		require.NoError(t, err, err)
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
	// The coordinator seat's rules (internal/nsprint/store/acl.go),
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
	require.Equal(t, 1, code, "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	require.Empty(t, out, "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	require.Equal(t, 1, strings.Count(errOut, "\n"), "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	require.True(t, strings.HasPrefix(errOut, "FAILED nova_sprint sha="+sha+" store="+addr+" "), "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	require.Contains(t, errOut, "NOPERM", "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)
	require.Contains(t, errOut, "remedy=", "fn load as nofn: exit %d stdout %q stderr %q; want exit 1 and one FAILED line naming NOPERM and the remedy", code, out, errOut)

	env[UserEnv] = "fnuser" // the environment's user, no flag
	{
		code, out, errOut = fnRun("fn", "check", "--addr", addr)
		require.Equal(t, 1, code, "fn check as NOVA_REDIS_USER=fnuser after the refused load: exit %d %q %q; want MISSING", code, out, errOut)
		require.True(t, strings.HasPrefix(out, "MISSING nova_sprint sha="+sha+" loaded=none "), "fn check as NOVA_REDIS_USER=fnuser after the refused load: exit %d %q %q; want MISSING", code, out, errOut)
		require.Empty(t, errOut, "fn check as NOVA_REDIS_USER=fnuser after the refused load: exit %d %q %q; want MISSING", code, out, errOut)
	}
	{
		code, out, errOut = fnRun("fn", "load", "--addr", addr)
		require.Zero(t, code, "fn load as NOVA_REDIS_USER=fnuser: exit %d %q %q; want LOADED", code, out, errOut)
		require.True(t, strings.HasPrefix(out, "LOADED nova_sprint sha="+sha+" "), "fn load as NOVA_REDIS_USER=fnuser: exit %d %q %q; want LOADED", code, out, errOut)
		require.Empty(t, errOut, "fn load as NOVA_REDIS_USER=fnuser: exit %d %q %q; want LOADED", code, out, errOut)
	}
	delete(env, UserEnv) // the flag's user
	{
		code, out, errOut = fnRun("fn", "check", "--addr", addr, "--user", "fnuser")
		require.Zero(t, code, "fn check --user fnuser: exit %d %q %q; want OK", code, out, errOut)
		require.True(t, strings.HasPrefix(out, "OK nova_sprint sha="+sha+" loaded="+sha+" want="+sha+" "), "fn check --user fnuser: exit %d %q %q; want OK", code, out, errOut)
		require.Empty(t, errOut, "fn check --user fnuser: exit %d %q %q; want OK", code, out, errOut)
	}
	// With no user the default user is used, and it is off: the store
	// refuses the login, which exits 2 in every nova-redis verb, with the
	// login remedy.
	{
		code, _, errOut = fnRun("fn", "check", "--addr", addr)
		require.Equal(t, 2, code, "fn check with no user on a store whose default user is off: exit %d %q; want exit 2 naming the refused login and one remedy", code, errOut)
		require.False(t, !strings.Contains(errOut, "NOAUTH") && !strings.Contains(errOut, "WRONGPASS"), "fn check with no user on a store whose default user is off: exit %d %q; want exit 2 naming the refused login and one remedy", code, errOut)
		require.Contains(t, errOut, `remedy="log in as a user that may run FUNCTION LIST`, "fn check with no user on a store whose default user is off: exit %d %q; want exit 2 naming the refused login and one remedy", code, errOut)
		require.NotContains(t, errOut, "; next:", "fn check with no user on a store whose default user is off: exit %d %q; want exit 2 naming the refused login and one remedy", code, errOut)
	}
}

// TestFnLoadNamesTheLibraryThatHoldsAFunction: another library on the store
// registers ns_ping, so the store refuses nova_sprint. fn load is one FAILED
// line, exit 1, with one remedy, which names that library and the function
// and says nothing of credentials; the store holds what it held.
func TestFnLoadNamesTheLibraryThatHoldsAFunction(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	{
		err := c.FunctionLoad(ctx, "#!lua name=other_lib\nredis.register_function('ns_ping', function() return 1 end)\n").Err()
		require.NoError(t, err, err)
	}
	d := realDeps()
	d.getenv = func(string) string { return "" }
	var out, errb bytes.Buffer
	code := run([]string{"fn", "load", "--addr", addr}, &out, &errb, d)
	line := errb.String()
	require.Equal(t, 1, code, "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.Zero(t, out.Len(), "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.Equal(t, 1, strings.Count(line, "\n"), "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.Equal(t, 1, strings.Count(line, "remedy"), "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.Contains(t, line, "function ns_ping is registered by library other_lib", "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.Contains(t, line, `remedy="a function name belongs to one library and library other_lib registers ns_ping: `, "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	require.NotContains(t, line, "password", "fn load over other_lib's ns_ping: exit %d stdout %q stderr %q; want exit 1 and one FAILED line with one remedy naming other_lib", code, out.String(), line)
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	require.NoError(t, err, "the store after the refused load holds %v (%v); want other_lib alone", libs, err)
	require.Len(t, libs, 1, "the store after the refused load holds %v (%v); want other_lib alone", libs, err)
	require.Equal(t, "other_lib", libs[0].Name, "the store after the refused load holds %v (%v); want other_lib alone", libs, err)
}

// TestFnRemedyRoundTripsTheLogin is Stella's probe of 47e862ac8, promoted to
// the regression: fn check on an empty store prints MISSING with a remedy
// command; that command, split into argv by /bin/sh exactly as a person
// pasting it would, runs through run() and the production dial with the
// same environment and loads the library. Each case is a login the remedy
// once lost: an explicit --password-env equal to the default beside
// NOVA_REDIS_PASSWORD_ENV naming a wrong one, an explicit empty --user beside
// NOVA_REDIS_USER naming a wrong user, and a user name holding a quote. All
// credentials are synthetic.
func TestFnRemedyRoundTripsTheLogin(t *testing.T) {
	t.Parallel()
	const pw = "synthetic-password"
	cases := []struct {
		name  string
		user  string // the ACL user the store has; "" is the default user, on
		env   map[string]string
		flags []string
	}{
		{"a named user", "fnuser", map[string]string{PasswordEnv: pw}, []string{"--user", "fnuser"}},
		{"an explicit --password-env equal to the default, beside NOVA_REDIS_PASSWORD_ENV", "fnuser",
			map[string]string{PasswordEnv: pw, PasswordEnvEnv: "OTHER_PW", "OTHER_PW": "wrong"}, []string{"--user", "fnuser", "--password-env", PasswordEnv}},
		{"an explicit empty --user, beside NOVA_REDIS_USER", "",
			map[string]string{PasswordEnv: pw, UserEnv: "wronguser"}, []string{"--user", ""}},
		{"a user name holding a quote", "fn'user", map[string]string{PasswordEnv: pw}, []string{"--user", "fn'user"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			users := []string{"--user", "default", "on", ">" + pw, "~*", "&*", "+@all"}
			if c.user != "" {
				users = []string{"--user", "default", "off", "--user", c.user, "on", ">" + pw, "~*", "&*", "+@all"}
			}
			addr := testredis.Start(t, users...)
			d := realDeps()
			d.getenv = func(k string) string { return c.env[k] }
			fnRun := func(args []string) (int, string, string) {
				var out, errb bytes.Buffer
				code := run(args, &out, &errb, d)
				return code, out.String(), errb.String()
			}
			check := append([]string{"fn", "check", "--addr", addr}, c.flags...)
			code, out, errOut := fnRun(check)
			require.Equal(t, 1, code, "fn check %q: exit %d %q %q; want MISSING", check, code, out, errOut)
			require.True(t, strings.HasPrefix(out, "MISSING "), "fn check %q: exit %d %q %q; want MISSING", check, code, out, errOut)
			require.Empty(t, errOut, "fn check %q: exit %d %q %q; want MISSING", check, code, out, errOut)
			_, quoted, ok := strings.Cut(strings.TrimSpace(out), " remedy=")
			require.True(t, ok, "MISSING line has no remedy: %q", out)
			remedy, err := strconv.Unquote(quoted)
			require.NoError(t, err, "remedy %s: %v", quoted, err)
			command, ok := strings.CutSuffix(remedy, " puts this binary's library on the store")
			require.True(t, ok, "remedy %q is not a nova-redis fn load command", remedy)
			require.True(t, strings.HasPrefix(command, "nova-redis fn load "), "remedy %q is not a nova-redis fn load command", remedy)
			// The shell splits the printed words; the names in it are this
			// test's own synthetic ones.
			words, err := exec.Command("/bin/sh", "-c", `printf '%s\000' `+strings.TrimPrefix(command, "nova-redis ")).Output()
			require.NoError(t, err, "the shell cannot read the remedy %q: %v", command, err)
			argv := strings.Split(strings.TrimSuffix(string(words), "\x00"), "\x00")
			{
				code, out, errOut = fnRun(argv)
				require.Zero(t, code, "the remedy %q as argv %q: exit %d %q %q; want LOADED, logged in as the check was", command, argv, code, out, errOut)
				require.True(t, strings.HasPrefix(out, "LOADED nova_sprint "), "the remedy %q as argv %q: exit %d %q %q; want LOADED, logged in as the check was", command, argv, code, out, errOut)
				require.Empty(t, errOut, "the remedy %q as argv %q: exit %d %q %q; want LOADED, logged in as the check was", command, argv, code, out, errOut)
			}
			{
				code, out, _ = fnRun(check)
				require.Zero(t, code, "fn check after the remedy: exit %d %q; want OK", code, out)
				require.True(t, strings.HasPrefix(out, "OK nova_sprint "), "fn check after the remedy: exit %d %q; want OK", code, out)
			}
			t.Logf("remedy %s -> LOADED", command)
		})
	}
}

// TestFnFailureIsOneLineFromTheBinary runs nova-redis's main() in a child
// process (this test binary, re-entered through TestNovaRedisMain), with an
// environment scrubbed to mainEnv alone so no login from the parent reaches
// it, and asserts what the process writes to its own stderr: one FAILED line,
// exit 2, nothing on stdout. go-redis's pool log goes to the process's
// stderr and not to run()'s writer; the production dial silences it. Two
// stores that never answer, neither a port another process could take:
//
//   - an endpoint the test owns for its whole life, a listener that accepts
//     every connection and closes it at once;
//   - a host name with an empty label, which Go's resolver refuses without
//     asking anyone, so the dial itself fails. That is the path go-redis
//     writes its pool log on (a dial that succeeds and then reads nothing,
//     as above, writes none), so this case is the one that turns red when
//     the silencer goes.
func TestFnFailureIsOneLineFromTheBinary(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, err)
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			c, err := ln.Accept()
			if err != nil {
				return // the listener is closed at the test's end
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-accepted })

	for _, c := range []struct{ name, addr, cause string }{
		{"an owned endpoint that closes every connection", ln.Addr().String(), "unreachable: EOF"},
		{"a host no resolver is asked about", "no..such:6379", "no such host"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestNovaRedisMain$", "--", "fn", "check", "--addr", c.addr)
		cmd.Env = []string{mainEnv + "=1"}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		var exit *exec.ExitError
		if !assert.ErrorAs(t, err, &exit, "%s: the child ended with %v; want exit 2 (stderr %q)", c.name, err, errb.String()) {
			continue
		}
		if !assert.Equal(t, 2, exit.ExitCode(), "%s: the child ended with %v; want exit 2 (stderr %q)", c.name, err, errb.String()) {
			continue
		}
		if assert.Zero(t, out.Len(), "%s: stdout %q stderr %q; want no stdout and exactly one FAILED line on stderr naming %q", c.name, out.String(), errb.String(), c.cause) {
			if assert.Equal(t, 1, strings.Count(errb.String(), "\n"), "%s: stdout %q stderr %q; want no stdout and exactly one FAILED line on stderr naming %q", c.name, out.String(), errb.String(), c.cause) {
				if assert.True(t, strings.HasPrefix(errb.String(), "FAILED nova_sprint sha="), "%s: stdout %q stderr %q; want no stdout and exactly one FAILED line on stderr naming %q", c.name, out.String(), errb.String(), c.cause) {
					assert.Contains(t, errb.String(), c.cause, "%s: stdout %q stderr %q; want no stdout and exactly one FAILED line on stderr naming %q", c.name, out.String(), errb.String(), c.cause)
				}
			}
		}
	}
}

// mainEnv, set to 1, makes TestNovaRedisMain be nova-redis's main().
const mainEnv = "NOVA_REDIS_TEST_MAIN"

// TestNovaRedisMain is nova-redis's main() with the arguments after "--",
// when mainEnv is 1; otherwise it returns at once. Only
// TestFnFailureIsOneLineFromTheBinary runs it that way.
func TestNovaRedisMain(t *testing.T) {
	t.Parallel()
	if os.Getenv(mainEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(run(args, os.Stdout, os.Stderr, realDeps()))
}
