package redisconn

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolvePrecedence is every way a field can be resolved: what was
// given, else the variable the caller's Env names for it, else nothing, each
// field on its own.
func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	const (
		given = "given.test:1"
		named = "named.test:2"
	)
	// A tool's own names, unlike the general ones.
	tool := Env{Addr: "TOOL_REDIS", User: "TOOL_REDIS_USER", PasswordEnv: "TOOL_REDIS_PASSWORD_ENV"}
	for _, c := range []struct {
		name     string
		explicit Options
		env      map[string]string
		want     Options
	}{
		{"nothing but an address given: the default user, no password",
			Options{Addr: given}, nil,
			Options{Addr: given}},

		{"address: given wins over the variable",
			Options{Addr: given, Env: GeneralEnv}, map[string]string{GeneralEnv.Addr: named},
			Options{Addr: given, Env: GeneralEnv}},
		{"address: the variable when nothing is given",
			Options{Env: GeneralEnv}, map[string]string{GeneralEnv.Addr: named},
			Options{Addr: named, Env: GeneralEnv}},
		{"address: the tool's own variable",
			Options{Env: tool}, map[string]string{tool.Addr: named, GeneralEnv.Addr: "not-this.test:9"},
			Options{Addr: named, Env: tool}},
		{"address: a Unix socket by its absolute path",
			Options{Env: GeneralEnv}, map[string]string{GeneralEnv.Addr: "/var/run/store.sock"},
			Options{Addr: "/var/run/store.sock", Env: GeneralEnv}},

		{"user: given wins over the variable",
			Options{Addr: given, User: "given-user", PasswordEnv: "PW", Env: GeneralEnv},
			map[string]string{GeneralEnv.User: "named-user", "PW": "x"},
			Options{Addr: given, User: "given-user", PasswordEnv: "PW", Env: GeneralEnv}},
		{"user: the variable when nothing is given",
			Options{Addr: given, PasswordEnv: "PW", Env: GeneralEnv},
			map[string]string{GeneralEnv.User: "named-user", "PW": "x"},
			Options{Addr: given, User: "named-user", PasswordEnv: "PW", Env: GeneralEnv}},
		{"user: the tool's own variable",
			Options{Addr: given, PasswordEnv: "PW", Env: tool},
			map[string]string{tool.User: "named-user", GeneralEnv.User: "not-this-user", "PW": "x"},
			Options{Addr: given, User: "named-user", PasswordEnv: "PW", Env: tool}},

		{"password variable: given wins over the variable",
			Options{Addr: given, User: "u", PasswordEnv: "GIVEN_PW", Env: GeneralEnv},
			map[string]string{GeneralEnv.PasswordEnv: "NAMED_PW", "GIVEN_PW": "x"},
			Options{Addr: given, User: "u", PasswordEnv: "GIVEN_PW", Env: GeneralEnv}},
		{"password variable: the variable when nothing is given",
			Options{Addr: given, User: "u", Env: GeneralEnv},
			map[string]string{GeneralEnv.PasswordEnv: "NAMED_PW", "NAMED_PW": "x"},
			Options{Addr: given, User: "u", PasswordEnv: "NAMED_PW", Env: GeneralEnv}},
		{"password variable: the tool's own variable",
			Options{Addr: given, User: "u", Env: tool},
			map[string]string{tool.PasswordEnv: "NAMED_PW", GeneralEnv.PasswordEnv: "NOT_THIS_PW", "NAMED_PW": "x"},
			Options{Addr: given, User: "u", PasswordEnv: "NAMED_PW", Env: tool}},

		{"each field on its own: address from the variable, user given, password variable from the variable",
			Options{User: "given-user", Env: GeneralEnv},
			map[string]string{GeneralEnv.Addr: named, GeneralEnv.User: "named-user", GeneralEnv.PasswordEnv: "_PW2", "_PW2": "x"},
			Options{Addr: named, User: "given-user", PasswordEnv: "_PW2", Env: GeneralEnv}},
		{"an Env that names only the address: the other two are read from nowhere",
			Options{Env: Env{Addr: "TOOL_REDIS"}},
			map[string]string{"TOOL_REDIS": named, GeneralEnv.User: "not-this-user", GeneralEnv.PasswordEnv: "NOT_THIS_PW", "NOT_THIS_PW": "x"},
			Options{Addr: named, Env: Env{Addr: "TOOL_REDIS"}}},
		{"the default user with a password: no user, a password variable that holds one",
			Options{Addr: given, PasswordEnv: "PW"}, map[string]string{"PW": "x"},
			Options{Addr: given, PasswordEnv: "PW"}},
		{"all three from the tool's own names",
			Options{Env: tool},
			map[string]string{tool.Addr: named, tool.User: "named-user", tool.PasswordEnv: "NAMED_PW", "NAMED_PW": "x"},
			Options{Addr: named, User: "named-user", PasswordEnv: "NAMED_PW", Env: tool}},
	} {
		got, err := Resolve(c.explicit, environment(c.env))
		if err != nil || got != c.want {
			assert.Failf(t, "", "%s: Resolve(%+v, %v) = %+v, %v; want %+v", c.name, c.explicit, c.env, got, err, c.want)
			continue
		}
		// What was resolved is ready: resolving it again, with no environment
		// at all, changes nothing.
		env := map[string]string{c.want.PasswordEnv: c.env[c.want.PasswordEnv]}
		if again, err := Resolve(got, environment(env)); err != nil || again != got {
			assert.Failf(t, "", "%s: resolved again = %+v, %v; want it unchanged", c.name, again, err)
		}
	}
}

// TestTheZeroEnvReadsNothing: with no names, the environment is not
// consulted, whatever it holds.
func TestTheZeroEnvReadsNothing(t *testing.T) {
	t.Parallel()
	var asked []string
	full := func(name string) string {
		asked = append(asked, name)
		return "store.test:6379"
	}
	if _, err := Resolve(Options{}, full); err == nil || !strings.Contains(err.Error(), "no address: none was given;") {
		assert.Failf(t, "", "Resolve with nothing given and no names = %v; want the refusal for no address, naming no variable", err)
	}
	if got, err := Resolve(Options{Addr: "given.test:1"}, full); err != nil || got != (Options{Addr: "given.test:1"}) {
		assert.Failf(t, "", "Resolve with an address and no names = %+v, %v", got, err)
	}
	if len(asked) != 0 {
		assert.Len(t, asked, 0, "the environment was asked for %q; want nothing", asked)
	}
}

// TestResolveRefusals is every refusal: its class, the words that name what
// was tried and the next thing to do, and that nothing resolved comes back
// with it.
func TestResolveRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		explicit Options
		env      map[string]string
		class    Class
		want     string
	}{
		{"no address anywhere", Options{User: "u", Env: GeneralEnv}, map[string]string{GeneralEnv.User: "x"}, Unreachable,
			"redis: unreachable: no address: none was given, and NOVA_REDIS_ADDR is empty; next: pass the address or set NOVA_REDIS_ADDR: host:port (a port from 1 to 65535) or the absolute path of a Unix socket"},
		{"no address and no variable named for one", Options{User: "u"}, nil, Unreachable,
			"redis: unreachable: no address: none was given; next: pass the address: host:port (a port from 1 to 65535) or the absolute path of a Unix socket"},
		{"no port", Options{Addr: "store.test"}, nil, Unreachable,
			`redis at "store.test" given to this tool: unreachable: not an address: missing port in address; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket`},
		{"no host", Options{Env: GeneralEnv}, map[string]string{GeneralEnv.Addr: ":6379"}, Unreachable,
			`redis at ":6379" from NOVA_REDIS_ADDR: unreachable: not an address: it names no host; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket`},
		{"port zero", Options{Env: Env{Addr: "TOOL_REDIS"}}, map[string]string{"TOOL_REDIS": "store.test:0"}, Unreachable,
			`redis at "store.test:0" from TOOL_REDIS: unreachable: not an address: its port is not a number from 1 to 65535; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket`},
		{"port too large", Options{Addr: "store.test:65536"}, nil, Unreachable, "its port is not a number from 1 to 65535"},
		{"port with a sign", Options{Addr: "store.test:+6379"}, nil, Unreachable, "its port is not a number from 1 to 65535"},
		{"port by name", Options{Addr: "store.test:redis"}, nil, Unreachable, "its port is not a number from 1 to 65535"},
		{"port missing after the colon", Options{Addr: "store.test:"}, nil, Unreachable, "its port is not a number from 1 to 65535"},
		{"too many colons", Options{Addr: "a:b:6379"}, nil, Unreachable, "too many colons in address"},
		{"a relative path", Options{Addr: "run/store.sock"}, nil, Unreachable, "missing port in address"},
		{"a space in host:port", Options{Addr: "store.test :6379"}, nil, Unreachable,
			`redis at "store.test :6379" given to this tool: unreachable: not an address: it holds a space or a control character`},
		{"a line break in host:port", Options{Addr: "store.test:6379\nNOAUTH"}, nil, Unreachable,
			`redis at "store.test:6379\nNOAUTH" given to this tool: unreachable: not an address: it holds a space or a control character`},
		{"a control character in a path", Options{Addr: "/var/run/store\x1b.sock"}, nil, Unreachable,
			`redis at "/var/run/store\x1b.sock" given to this tool: unreachable: not an address: it holds a space or a control character`},
		{"a URL, which is not shown", Options{Env: GeneralEnv}, map[string]string{GeneralEnv.Addr: "redis://bench:hunter2@store.test:6379/0"}, Unreachable,
			"redis at the address from NOVA_REDIS_ADDR (not shown): unreachable: not an address: it is a URL or carries a login; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket, the user (NOVA_REDIS_USER) and the name of the password's variable (NOVA_REDIS_PASSWORD_ENV) on their own"},
		{"a login before the host, which is not shown", Options{Addr: "bench:hunter2@store.test:6379"}, nil, Unreachable,
			"redis at the address given to this tool (not shown): unreachable: not an address: it is a URL or carries a login"},

		{"a user and no password variable", Options{Addr: "store.test:6379", User: "bench", Env: GeneralEnv}, nil, AuthRefused,
			"redis at store.test:6379 as user bench, no password: login refused: no password variable is named: none was given, and NOVA_REDIS_PASSWORD_ENV is empty; next: set NOVA_REDIS_PASSWORD_ENV to the NAME of the variable that holds the password of bench, never to the password"},
		{"a user and no password variable, with no variable named for one", Options{Addr: "store.test:6379", User: "bench"}, nil, AuthRefused,
			"redis at store.test:6379 as user bench, no password: login refused: no password variable is named: none was given; next: give the NAME of the variable that holds the password of bench, never the password"},
		{"a user from the environment and no password variable", Options{Addr: "store.test:6379", Env: Env{User: "TOOL_REDIS_USER"}}, map[string]string{"TOOL_REDIS_USER": "bench"}, AuthRefused,
			"redis at store.test:6379 as user bench, no password: login refused: no password variable is named"},
		{"a user whose password variable is empty", Options{Addr: "store.test:6379", User: "bench", PasswordEnv: "PW"}, map[string]string{"PW": ""}, AuthRefused,
			"redis at store.test:6379 as user bench (password from PW): login refused: PW is empty; next: export PW, holding the password, in the environment of this process"},
		{"a user whose password variable is unset", Options{Addr: "store.test:6379", Env: GeneralEnv}, map[string]string{GeneralEnv.User: "bench", GeneralEnv.PasswordEnv: "NOVA_TEST_PW"}, AuthRefused,
			"redis at store.test:6379 as user bench (password from NOVA_TEST_PW): login refused: NOVA_TEST_PW is empty"},
		{"the default user whose password variable is empty", Options{Addr: "store.test:6379", PasswordEnv: "PW"}, nil, AuthRefused,
			"redis at store.test:6379 as the default user (password from PW): login refused: PW is empty"},
		{"the password where its variable's name belongs, which is not shown", Options{Addr: "store.test:6379", User: "bench", Env: GeneralEnv},
			map[string]string{GeneralEnv.PasswordEnv: "hunter2"}, AuthRefused,
			"redis at store.test:6379 as user bench (the password's variable is not shown): login refused: the name of the password's variable, from NOVA_REDIS_PASSWORD_ENV, is not a name, and is not shown in case it is the password itself; next: give the NAME of the variable that holds the password (capital letters, digits and underscores), never the password"},
		{"a given password variable that is not a name, which is not shown", Options{Addr: "store.test:6379", PasswordEnv: "Tr0ub4dor&3"}, nil, AuthRefused,
			"redis at store.test:6379 as the default user (the password's variable is not shown): login refused: the name of the password's variable, given to this tool, is not a name"},
	} {
		got, err := Resolve(c.explicit, environment(c.env))
		if err == nil {
			assert.Error(t, err, "%s: Resolve = %+v, nil; want a refusal", c.name, got)
			continue
		}
		if got != (Options{}) {
			assert.EqualValues(t, (Options{}), got, "%s: the refusal came with options %+v; want none", c.name, got)
		}
		if class := Classify(err); class != c.class {
			assert.EqualValues(t, c.class, class, "%s: class %v; want %v (%v)", c.name, class, c.class, err)
		}
		text := err.Error()
		if !strings.Contains(text, c.want) {
			assert.Contains(t, text, c.want, "%s:\n got %s\nwant %s", c.name, text, c.want)
		}
		if !strings.Contains(text, "; next: ") || strings.ContainsAny(text, "\n\r") {
			assert.Failf(t, "", "%s: %q is not one line that ends in the next thing to do", c.name, text)
		}
		for _, secret := range []string{"hunter2", "Tr0ub4dor&3"} {
			for _, shown := range errorsText(err) {
				if strings.Contains(shown, secret) {
					assert.NotContains(t, shown, secret, "%s: %q shows %q", c.name, shown, secret)
				}
			}
		}
	}
}

// TestResolveReadsOnlyTheEnvironmentItIsHanded: nil is the empty
// environment, and the variables asked for are the ones the Env names and
// the one the password's variable names, nothing else.
func TestResolveReadsOnlyTheEnvironmentItIsHanded(t *testing.T) {
	t.Parallel()
	if _, err := Resolve(Options{Env: GeneralEnv}, nil); err == nil || !strings.Contains(err.Error(), "no address") {
		assert.Failf(t, "", "Resolve with nothing given and a nil environment = %v; want the refusal for no address", err)
	}
	if got, err := Resolve(Options{Addr: "store.test:6379", Env: GeneralEnv}, nil); err != nil || got != (Options{Addr: "store.test:6379", Env: GeneralEnv}) {
		assert.Failf(t, "", "Resolve with an address and a nil environment = %+v, %v", got, err)
	}
	var asked []string
	env := map[string]string{GeneralEnv.PasswordEnv: "NOVA_TEST_PW", "NOVA_TEST_PW": "x", GeneralEnv.User: "bench", GeneralEnv.Addr: "store.test:6379"}
	if _, err := Resolve(Options{Env: GeneralEnv}, func(name string) string {
		asked = append(asked, name)
		return env[name]
	}); err != nil {
		require.NoError(t, err, err)
	}
	if got, want := fmt.Sprint(asked), fmt.Sprint([]string{GeneralEnv.Addr, GeneralEnv.User, GeneralEnv.PasswordEnv, "NOVA_TEST_PW"}); got != want {
		assert.EqualValues(t, want, got, "variables read: %s; want %s", got, want)
	}
	asked = nil
	if _, err := Resolve(Options{Addr: "store.test:6379", Env: GeneralEnv}, func(name string) string {
		asked = append(asked, name)
		return ""
	}); err != nil {
		require.NoError(t, err, err)
	}
	if got, want := fmt.Sprint(asked), fmt.Sprint([]string{GeneralEnv.User, GeneralEnv.PasswordEnv}); got != want {
		assert.EqualValues(t, want, got, "variables read with only an address given: %s; want %s", got, want)
	}
}

// TestAddressShapes: what is an address and what is not.
func TestAddressShapes(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]string{
		"127.0.0.1:6379":       "tcp",
		"[::1]:6379":           "tcp",
		"localhost:1":          "tcp",
		"store.test:65535":     "tcp",
		"store.test:06379":     "tcp",
		"/tmp/store.sock":      "unix",
		"/Users/a b/store.soc": "unix",
		"/":                    "unix",
	} {
		if fault := addrFault(addr); fault != "" {
			assert.Empty(t, fault, "addrFault(%q) = %q; want an address", addr, fault)
		}
		if got := network(addr); got != want {
			assert.EqualValues(t, want, got, "network(%q) = %q; want %q", addr, got, want)
		}
	}
	for _, addr := range []string{"", "store.test", "::1:6379", "[::1]", "store.test:-1", "store.test:6379 ", " store.test:6379", "store.test:63\t79", "tmp/store.sock", "./store.sock", "~/store.sock", "/tmp/store\n.sock"} {
		if fault := addrFault(addr); fault == "" {
			assert.NotEmpty(t, fault, "addrFault(%q) = \"\"; want a fault", addr)
		}
	}
}

// TestEnvNameShape: the name of a variable is capital letters, digits and
// underscores, not starting with a digit; anything else may be a password.
func TestEnvNameShape(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"A", "_", "PW", "NOVA_REDIS_BENCH_PASSWORD", "_X9", "A1_"} {
		if !envName(name) {
			assert.True(t, envName(name), "envName(%q) = false; want a name", name)
		}
	}
	for _, name := range []string{"", "9A", "pw", "Pw", "NOVA-REDIS", "NOVA REDIS", "PW=", "PW\n", "ÅNGSTRÖM", "hunter2", "$PW", "PW;"} {
		if envName(name) {
			assert.False(t, envName(name), "envName(%q) = true; want it refused", name)
		}
	}
}

// TestOptionsString: three fields on one line, each value one token.
func TestOptionsString(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		o    Options
		want string
	}{
		{Options{}, "addr= user= password-env="},
		{Options{Addr: "store.test:6379", User: "bench", PasswordEnv: "NOVA_TEST_PW"}, "addr=store.test:6379 user=bench password-env=NOVA_TEST_PW"},
		{Options{Addr: "/a b/s.sock", User: "u=1\nuser=root", PasswordEnv: "P W"}, `addr=/a\x20b/s.sock user=u\x3d1\x0auser\x3droot password-env=P\x20W`},
	} {
		for _, got := range []string{c.o.String(), fmt.Sprint(c.o), fmt.Sprintf("%v", c.o), fmt.Sprintf("%+v", c.o), fmt.Sprintf("%s", c.o)} {
			if got != c.want {
				assert.EqualValues(t, c.want, got, "%#v renders %q; want %q", c.o, got, c.want)
			}
		}
		if n := len(strings.Fields(c.o.String())); n != 3 {
			assert.EqualValues(t, 3, n, "%q is %d fields; want 3", c.o.String(), n)
		}
	}
}

// TestRefusalsAreThisPackagesErrors: a refusal unwraps to its cause and is
// found by errors.As under a wrap.
func TestRefusalsAreThisPackagesErrors(t *testing.T) {
	t.Parallel()
	_, err := Resolve(Options{Addr: "store.test:6379", User: "bench", PasswordEnv: "PW"}, nothing)
	if !isFailure(err) || !isFailure(fmt.Errorf("verb: %w", err)) {
		require.FailNowf(t, "", "%v is not this package's error", err)
	}
	if cause := errors.Unwrap(err); cause == nil || cause.Error() != "PW is empty" {
		assert.Failf(t, "", "the refusal unwraps to %v; want its cause", cause)
	}
	if got := Classify(fmt.Errorf("verb: %w", err)); got != AuthRefused {
		assert.EqualValues(t, AuthRefused, got, "wrapped, its class is %v; want %v", got, AuthRefused)
	}
}
