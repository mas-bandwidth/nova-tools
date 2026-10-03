package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fnStore is a store that keeps the function libraries it holds, by name,
// loads as Redis does, and records the commands it was sent (without the
// code). A method the fn verbs are not meant to call panics on the nil
// embedded client. The functional test holds the same verbs against a
// redis-server.
type fnStore struct {
	redis.UniversalClient

	mu      sync.Mutex
	held    map[string]string // library name -> code
	others  []redis.Library   // the libraries of other names an unfiltered FUNCTION LIST answers
	listErr error             // FUNCTION LIST fails with this when set
	loadErr error             // FUNCTION LOAD [REPLACE] fails with this when set
	sent    []string
	closed  int
}

func newFnStore() *fnStore { return &fnStore{held: map[string]string{}} }

func (s *fnStore) note(command string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, command)
}

// take returns the commands sent since the last take.
func (s *fnStore) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sent := s.sent
	s.sent = nil
	return sent
}

func (s *fnStore) FunctionList(ctx context.Context, q redis.FunctionListQuery) *redis.FunctionListCmd {
	command := "FUNCTION LIST"
	if q.LibraryNamePattern != "" {
		command += " LIBRARYNAME " + q.LibraryNamePattern
	}
	if q.WithCode {
		command += " WITHCODE"
	}
	s.note(command)
	cmd := redis.NewFunctionListCmd(ctx, "function", "list")
	if s.listErr != nil {
		cmd.SetErr(s.listErr)
		return cmd
	}
	var libs []redis.Library
	if q.LibraryNamePattern == "" {
		libs = append(libs, s.others...)
	}
	for name, code := range s.held {
		if strings.EqualFold(name, q.LibraryNamePattern) {
			lib := redis.Library{Name: name}
			if q.WithCode {
				lib.Code = code
			}
			libs = append(libs, lib)
		}
	}
	cmd.SetVal(libs)
	return cmd
}

func (s *fnStore) FunctionLoadReplace(ctx context.Context, code string) *redis.StringCmd {
	s.note("FUNCTION LOAD REPLACE")
	cmd := redis.NewStringCmd(ctx, "function", "load", "replace", code)
	if s.loadErr != nil {
		cmd.SetErr(s.loadErr)
		return cmd
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(code, "#!lua name="), "\n")
	s.held[name] = code
	cmd.SetVal(name)
	return cmd
}

func (s *fnStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

// storeReply is an error the store itself replied with.
type storeReply string

func (e storeReply) Error() string { return string(e) }

func (storeReply) RedisError() {}

// fnHarness runs the fn verbs over one fnStore and counts the opens. The
// store it hands fnVerb stands where connect's redisconn connection stands;
// the login it is asked to open with is checked against the one the case
// wants, in redisconn's terms.
type fnHarness struct {
	t           *testing.T
	store       *fnStore
	dials       int
	user        string // the user the login must name
	passwordEnv string // the variable the password is read from; "" is PasswordEnv
	d           deps
}

// open is fnVerb's opener over the harness's store.
func (h *fnHarness) open(_ context.Context, store login) (redis.UniversalClient, func() error, error) {
	h.dials++
	o := store.options(h.d)
	env := h.passwordEnv
	if env == "" {
		env = PasswordEnv
	}
	if assert.Equal(h.t, *store.addr, o.Addr, "opened with %s; want user %q and the password in %s, and no variable of redisconn's own", o, h.user, env) {
		if assert.Equal(h.t, h.user, o.User, "opened with %s; want user %q and the password in %s, and no variable of redisconn's own", o, h.user, env) {
			if assert.Equal(h.t, env, o.PasswordEnv, "opened with %s; want user %q and the password in %s, and no variable of redisconn's own", o, h.user, env) {
				if assert.Equal(h.t, "pw", h.d.getenv(o.PasswordEnv), "opened with %s; want user %q and the password in %s, and no variable of redisconn's own", o, h.user, env) {
					assert.Equal(h.t, (redisconn.Env{}), o.Env, "opened with %s; want user %q and the password in %s, and no variable of redisconn's own", o, h.user, env)
				}
			}
		}
	}
	return h.store, h.store.Close, nil
}

func newFnHarness(t *testing.T) *fnHarness {
	t.Helper()
	h := &fnHarness{t: t, store: newFnStore()}
	h.d = deps{
		getenv: func(k string) string {
			env := h.passwordEnv
			if env == "" {
				env = PasswordEnv
			}
			if k == env {
				return "pw"
			}
			return ""
		},
	}
	h.d.fnOpen = h.open
	return h
}

// run is nova-redis over the harness: fn through the harness's store, anything
// else through run().
func (h *fnHarness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, h.d)
	return code, out.String(), errb.String()
}

// want is the digest of the library this binary embeds.
func want(t *testing.T) string {
	t.Helper()
	d, err := library().Digest()
	require.NoError(t, err, err)
	return d
}

// TestFnLibraryIsTheLoadersBytes: the library the fn verbs load is, byte for
// byte, the source internal/nsprint/fn builds and every earlier loader put on
// the store, so its digest is the one a deployed store holds.
func TestFnLibraryIsTheLoadersBytes(t *testing.T) {
	t.Parallel()
	source, err := fn.Source()
	require.NoError(t, err, err)
	mine, err := library().Source()
	require.NoError(t, err, err)
	require.Equal(t, source, mine, "library().Source() is %d bytes and fn.Source() %d; they must be the same bytes", len(mine), len(source))
	{
		got := want(t)
		expected := fn.Sum(source)
		if got != expected {
			require.Equal(t, expected, got, "digest %s, fn.Sum %s", got, fn.Sum(source))
		}
	}
}

// TestFnLoadAndCheckOnAStore walks one store through every state: absent,
// loaded, loaded again, other code under the name, replaced. Each step's
// line, exit status and commands sent are what the verb promises.
func TestFnLoadAndCheckOnAStore(t *testing.T) {
	t.Parallel()
	h := newFnHarness(t)
	sha := want(t)
	const addr = "127.0.0.1:6399"
	const list = "FUNCTION LIST LIBRARYNAME nova_sprint WITHCODE"
	other := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	remedy := ` remedy="nova-redis fn load --addr 127.0.0.1:6399 puts this binary's library on the store"`

	steps := []struct {
		name   string
		before func()
		args   []string
		code   int
		out    string
		sent   []string
	}{
		{"check an empty store", nil, []string{"fn", "check", "--addr", addr}, 1,
			"MISSING nova_sprint sha=" + sha + " loaded=none want=" + sha + " store=" + addr + remedy + "\n", []string{list}},
		{"load onto it", nil, []string{"fn", "load", "--addr", addr}, 0,
			"LOADED nova_sprint sha=" + sha + " store=" + addr + "\n", []string{list, "FUNCTION LOAD REPLACE"}},
		{"check it", nil, []string{"fn", "check", "--addr", addr}, 0,
			"OK nova_sprint sha=" + sha + " loaded=" + sha + " want=" + sha + " store=" + addr + "\n", []string{list}},
		{"load it again", nil, []string{"fn", "load", "--addr", addr}, 0,
			"UNCHANGED nova_sprint sha=" + sha + " store=" + addr + "\n", []string{list}},
		{"check other code", func() { h.store.held["nova_sprint"] = other }, []string{"fn", "check", "--addr", addr}, 1,
			"STALE nova_sprint sha=" + sha + " loaded=" + redisfn.DigestOf(other) + " want=" + sha + " store=" + addr + remedy + "\n", []string{list}},
		{"replace it", nil, []string{"fn", "load", "--addr", addr}, 0,
			"REPLACED nova_sprint sha=" + sha + " was=" + redisfn.DigestOf(other) + " store=" + addr + "\n", []string{list, "FUNCTION LOAD REPLACE"}},
		{"check the replacement", nil, []string{"fn", "check", "--addr", addr}, 0,
			"OK nova_sprint sha=" + sha + " loaded=" + sha + " want=" + sha + " store=" + addr + "\n", []string{list}},
	}
	for i, s := range steps {
		if s.before != nil {
			s.before()
		}
		code, out, errOut := h.run(s.args...)
		if assert.Equal(t, s.code, code, "%s: exit %d stdout %q stderr %q; want exit %d stdout %q and no stderr", s.name, code, out, errOut, s.code, s.out) {
			if assert.Equal(t, s.out, out, "%s: exit %d stdout %q stderr %q; want exit %d stdout %q and no stderr", s.name, code, out, errOut, s.code, s.out) {
				assert.Empty(t, errOut, "%s: exit %d stdout %q stderr %q; want exit %d stdout %q and no stderr", s.name, code, out, errOut, s.code, s.out)
			}
		}
		{
			sent := h.store.take()
			assert.True(t, slices.Equal(sent, s.sent), "%s: sent %q, want %q", s.name, sent, s.sent)
		}
		assert.Equal(t, i+1, h.store.closed, "%s: the client was closed %d times after %d runs", s.name, h.store.closed, i+1)
	}
}

// TestFnFailuresNameTheStateAndTheRemedy: every failure is one FAILED line on
// stderr that names the operation, the cause and what the store holds, with
// the one remedy for that cause, and nothing on stdout. A refusal the store
// answered exits 1; no answer exits 2. The remedy's command logs in as the
// verb did.
func TestFnFailuresNameTheStateAndTheRemedy(t *testing.T) {
	t.Parallel()
	const addr = "127.0.0.1:6399"
	unreachable := func(s *fnStore) {
		s.listErr = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	}
	noperm := func(s *fnStore) {
		s.listErr = storeReply("NOPERM User nofn has no permissions to run the 'function|list' command")
	}
	collision := func(s *fnStore) {
		s.loadErr = storeReply("ERR Function ns_ping already exists")
		s.others = []redis.Library{{Name: "other_lib", Functions: []redis.Function{{Name: "ns_ping"}}}}
	}
	credentials := "log in as a user"
	cases := []struct {
		name    string
		setup   func(*fnStore)
		args    []string // the subverb and any flags after --addr
		code    int
		inLine  []string
		notIn   []string
		notSent string
	}{
		{"check, store unreachable", unreachable, []string{"check"}, 2,
			[]string{"FAILED nova_sprint sha=", "store=127.0.0.1:6399", "redisfn: check nova_sprint: the store did not answer", "nothing was changed",
				`remedy="no answer: check that the store at 127.0.0.1:6399 is up and --addr is right, then nova-redis fn check --addr 127.0.0.1:6399"`}, []string{credentials}, ""},
		{"load, store unreachable", unreachable, []string{"load"}, 2,
			[]string{"FAILED nova_sprint sha=", "redisfn: check nova_sprint: the store did not answer", "nothing was changed",
				`remedy="no answer, so the store may hold either library: check that the store at 127.0.0.1:6399 is up and --addr is right, then nova-redis fn check --addr 127.0.0.1:6399"`}, []string{credentials}, "FUNCTION LOAD REPLACE"},
		{"load, NOPERM, as a named user", noperm, []string{"load", "--user", "nofn"}, 1,
			[]string{"the store refused: NOPERM User nofn", "nothing was changed",
				`remedy="log in as a user that may run FUNCTION LIST and FUNCTION LOAD: check --user (NOVA_REDIS_USER) and the password in NOVA_REDIS_PASSWORD, then nova-redis fn load --addr 127.0.0.1:6399 --user nofn"`}, nil, "FUNCTION LOAD REPLACE"},
		{"check, NOPERM", noperm, []string{"check"}, 1,
			[]string{`remedy="log in as a user that may run FUNCTION LIST: check --user (NOVA_REDIS_USER) and the password in NOVA_REDIS_PASSWORD, then nova-redis fn check --addr 127.0.0.1:6399"`}, nil, ""},
		{"load, store refuses the library", func(s *fnStore) {
			s.loadErr = storeReply("ERR Error compiling function: user_function:3: '=' expected")
		}, []string{"load"}, 1,
			[]string{"FAILED nova_sprint sha=", "redisfn: load nova_sprint: the store refused", "the store holds what it held before", "user_function:3 = ",
				`remedy="the store would not take this binary's library; err names the file and line: fix the Lua and rebuild, then nova-redis fn load --addr 127.0.0.1:6399"`}, []string{credentials}, ""},
		{"load, a function name another library holds", collision, []string{"load", "--user", "coordinator"}, 1,
			[]string{"FAILED nova_sprint sha=", "function ns_ping is registered by library other_lib",
				`remedy="a function name belongs to one library and library other_lib registers ns_ping: load the version of that library that no longer registers it, or delete it, then nova-redis fn load --addr 127.0.0.1:6399 --user coordinator"`},
			[]string{credentials, "NOVA_REDIS_PASSWORD", "FUNCTION LIST and"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newFnHarness(t)
			if len(c.args) > 2 {
				h.user = c.args[2]
			}
			c.setup(h.store)
			code, out, errOut := h.run(append([]string{"fn", c.args[0], "--addr", addr}, c.args[1:]...)...)
			if assert.Equal(t, c.code, code, "exit %d stdout %q; want exit %d and no stdout", code, out, c.code) {
				assert.Empty(t, out, "exit %d stdout %q; want exit %d and no stdout", code, out, c.code)
			}
			if assert.Equal(t, 1, strings.Count(errOut, "\n"), "stderr is %d lines with %d remedies, want one line with one: %q", strings.Count(errOut, "\n"), strings.Count(errOut, "remedy"), errOut) {
				assert.Equal(t, 1, strings.Count(errOut, "remedy"), "stderr is %d lines with %d remedies, want one line with one: %q", strings.Count(errOut, "\n"), strings.Count(errOut, "remedy"), errOut)
			}
			for _, w := range c.inLine {
				assert.Contains(t, errOut, w, "stderr %q does not hold %q", errOut, w)
			}
			for _, w := range c.notIn {
				assert.NotContains(t, errOut, w, "stderr %q holds %q, which is not this cause's remedy", errOut, w)
			}
			if c.notSent != "" {
				assert.False(t, slices.Contains(h.store.take(), c.notSent), "%s was sent after a failed read", c.notSent)
			}
		})
	}
}

// TestFnRemedyLogsInAsTheVerbDid: STALE's and MISSING's remedy command
// carries the --user and --password-env the check was run with.
func TestFnRemedyLogsInAsTheVerbDid(t *testing.T) {
	t.Parallel()
	h := newFnHarness(t)
	h.user, h.passwordEnv = "coordinator", "SEAT_PW"
	code, out, _ := h.run("fn", "check", "--addr", "127.0.0.1:6399", "--user", "coordinator", "--password-env", "SEAT_PW")
	{
		want := `remedy="nova-redis fn load --addr 127.0.0.1:6399 --user coordinator --password-env SEAT_PW puts this binary's library on the store"`
		if assert.Equal(t, 1, code, "exit %d %q; want MISSING ending in %s", code, out, want) {
			assert.True(t, strings.HasSuffix(out, want+"\n"), "exit %d %q; want MISSING ending in %s", code, out, want)
		}
	}
}

// TestFnRefusesBeforeTheDial: a missing subverb, an unknown one, a missing or
// malformed --addr and a stray argument are refused (exit 2) and the store is
// never dialled.
func TestFnRefusesBeforeTheDial(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		line string
	}{
		{[]string{"fn"}, "nova-redis fn REFUSED: no subverb given; load puts this binary's function library on the store, check compares the store's with it; run: nova-redis help fn\n"},
		{[]string{"fn", "deploy", "--addr", "127.0.0.1:6379"}, "nova-redis fn REFUSED: unknown subverb \"deploy\"; want load or check; run: nova-redis help fn\n"},
		{[]string{"fn", "load"}, "FN-LOAD REFUSED: --addr is required: the store's address as <host:port>, such as 127.0.0.1:6379 (no default); refusing to guess; run: nova-redis help\n"},
		{[]string{"fn", "check", "--addr", "127.0.0.1"}, "FN-CHECK REFUSED: --addr \"127.0.0.1\" is not <host:port>; refusing to guess; run: nova-redis help\n"},
		{[]string{"fn", "check", "--addr", ":6379"}, "FN-CHECK REFUSED: --addr \":6379\" names no host; refusing to guess localhost; run: nova-redis help\n"},
		{[]string{"fn", "load", "--addr", "127.0.0.1:6379", "extra"}, "FN-LOAD REFUSED: unexpected argument \"extra\"; every input is a flag; run: nova-redis help\n"},
	}
	for _, c := range cases {
		h := newFnHarness(t)
		code, out, errOut := h.run(c.args...)
		if assert.Equal(t, 2, code, "%q: exit %d stdout %q stderr %q; want exit 2 and %q", c.args, code, out, errOut, c.line) {
			if assert.Empty(t, out, "%q: exit %d stdout %q stderr %q; want exit 2 and %q", c.args, code, out, errOut, c.line) {
				assert.Equal(t, c.line, errOut, "%q: exit %d stdout %q stderr %q; want exit 2 and %q", c.args, code, out, errOut, c.line)
			}
		}
		assert.Zero(t, h.dials, "%q: dialled %d times; a refusal comes before the dial", c.args, h.dials)
	}
}

// loginCase is one login a verb is given: the flags after --addr, the
// environment, and either the user/password the login is made with or the
// refusal's reason (what stands between "nova-redis <verb> REFUSED: " and
// "; run: nova-redis help <verb>") made before any dial.
type loginCase struct {
	name    string
	flag    []string
	env     map[string]string
	want    string // user/password the login is made with
	refusal string // "" when the verb opens the store
}

// loginVerbs are the four verbs that open a store, each with --addr at
// index 2.
var loginVerbs = [][]string{
	{"spill", "--addr", "127.0.0.1:1", "--owner", "o", "--name", "n", "--ttl", "1m", "--value", "v"},
	{"recall", "--addr", "127.0.0.1:1", "--owner", "o", "--name", "n"},
	{"fn", "load", "--addr", "127.0.0.1:1"},
	{"fn", "check", "--addr", "127.0.0.1:1"},
}

// loginCases are the logins every verb that opens a store is held to: the
// user --user names, else NOVA_REDIS_USER, else none (the default user), with
// the password in the variable --password-env names, else
// NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD; and the refusals,
// each naming where the bad value came from.
func loginCases() []loginCase {
	return []loginCase{
		{"no user", nil, map[string]string{PasswordEnv: "pw"}, "/pw", ""},
		{"the environment's user", nil, map[string]string{PasswordEnv: "pw", UserEnv: "coordinator"}, "coordinator/pw", ""},
		{"the flag's user", []string{"--user", "fnuser"}, map[string]string{PasswordEnv: "pw"}, "fnuser/pw", ""},
		{"the flag over the environment", []string{"--user", "fnuser"}, map[string]string{PasswordEnv: "pw", UserEnv: "coordinator"}, "fnuser/pw", ""},
		{"the flag's password variable", []string{"--user", "coordinator", "--password-env", "SEAT_PW"}, map[string]string{PasswordEnv: "pw", "SEAT_PW": "seat"}, "coordinator/seat", ""},
		{"the environment's password variable", nil, map[string]string{UserEnv: "coordinator", PasswordEnvEnv: "SEAT_PW", "SEAT_PW": "seat"}, "coordinator/seat", ""},
		{"a user without a password", []string{"--user", "fnuser"}, map[string]string{}, "",
			"user fnuser (from --user) but NOVA_REDIS_PASSWORD is empty; run under nova-secrets exec --only NOVA_REDIS_PASSWORD, refusing to log in without a password"},
		{"a user whose named password variable is empty", nil, map[string]string{UserEnv: "coordinator", PasswordEnvEnv: "SEAT_PW", PasswordEnv: "pw"}, "",
			"user coordinator (from NOVA_REDIS_USER) but SEAT_PW is empty; run under nova-secrets exec --only SEAT_PW, refusing to log in without a password"},
		{"a user name with a space, from the environment", nil, map[string]string{PasswordEnv: "pw", UserEnv: "fn user"}, "",
			"NOVA_REDIS_USER \"fn user\" holds whitespace; give the ACL user's name"},
		{"a user name with a space, from the flag", []string{"--user", "fn user"}, map[string]string{PasswordEnv: "pw"}, "",
			"--user \"fn user\" holds whitespace; give the ACL user's name"},
		{"a password variable that is not a name", []string{"--password-env", "1-bad"}, map[string]string{PasswordEnv: "pw"}, "",
			"--password-env \"1-bad\" is not a variable name; name the variable that holds the password (default NOVA_REDIS_PASSWORD)"},
		{"a password variable from the environment that is not a name", nil, map[string]string{PasswordEnvEnv: "SEAT PW"}, "",
			"NOVA_REDIS_PASSWORD_ENV \"SEAT PW\" is not a variable name; name the variable that holds the password (default NOVA_REDIS_PASSWORD)"},
	}
}

// TestEveryVerbLogsInAsTheUserItIsGiven: spill, recall, fn load and fn check
// share one login (loginFlags, check, options), which every case resolves here
// to the redisconn options the verb opens the store with, without a dial: the
// user, the variable the password is read from and what it holds, and no
// variable of redisconn's own. Each verb then runs whole: fn over an opener
// that records the options it is handed; every refusal (exit 2, one line)
// through run(), refused before the store is opened. Nothing listens at the
// verbs' address, 127.0.0.1:1, and no store is started: this file owns no
// socket. The login made on a real store, for spill and recall, is the
// functional TestSpillAndRecallLogInAsTheUserItIsGiven.
func TestEveryVerbLogsInAsTheUserItIsGiven(t *testing.T) {
	t.Parallel()
	for _, c := range loginCases() {
		d := deps{
			now:    func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
			getenv: func(k string) string { return c.env[k] },
		}
		user, password, _ := strings.Cut(c.want, "/")

		fs := flag.NewFlagSet("login", flag.ContinueOnError)
		l := loginFromFlags(fs)
		{
			err := fs.Parse(append([]string{"--addr", "127.0.0.1:1"}, c.flag...))
			require.NoError(t, err, err)
		}
		err := l.check(d)
		if c.refusal != "" {
			if assert.Error(t, err, "%s: check %v; want the refusal %q", c.name, err, c.refusal) {
				assert.Equal(t, c.refusal, err.Error(), "%s: check %v; want the refusal %q", c.name, err, c.refusal)
			}
		} else {
			o := l.options(d)
			if assert.NoError(t, err, "%s: check %v, options %s (the variable holds %q); want user %q, password %q and no variable of redisconn's own", c.name, err, o, d.getenv(o.PasswordEnv), user, password) {
				if assert.Equal(t, "127.0.0.1:1", o.Addr, "%s: check %v, options %s (the variable holds %q); want user %q, password %q and no variable of redisconn's own", c.name, err, o, d.getenv(o.PasswordEnv), user, password) {
					if assert.Equal(t, user, o.User, "%s: check %v, options %s (the variable holds %q); want user %q, password %q and no variable of redisconn's own", c.name, err, o, d.getenv(o.PasswordEnv), user, password) {
						if assert.Equal(t, password, d.getenv(o.PasswordEnv), "%s: check %v, options %s (the variable holds %q); want user %q, password %q and no variable of redisconn's own", c.name, err, o, d.getenv(o.PasswordEnv), user, password) {
							assert.Equal(t, (redisconn.Env{}), o.Env, "%s: check %v, options %s (the variable holds %q); want user %q, password %q and no variable of redisconn's own", c.name, err, o, d.getenv(o.PasswordEnv), user, password)
						}
					}
				}
			}
		}

		for _, verb := range loginVerbs {
			args := append(slices.Clone(verb), c.flag...)
			if c.refusal == "" && verb[0] != "fn" {
				continue // opening the store is the functional test's
			}
			var got []string
			var out, errb bytes.Buffer
			d.fnOpen = func(_ context.Context, store login) (redis.UniversalClient, func() error, error) {
				o := store.options(d)
				got = append(got, o.User+"/"+d.getenv(o.PasswordEnv))
				s := newFnStore()
				return s, s.Close, nil
			}
			code := run(args, &out, &errb, d)
			if c.refusal != "" {
				verbName := verb[0]
				if verb[0] == "fn" {
					verbName = "fn " + verb[1]
				}
				{
					want := "nova-redis " + verbName + " REFUSED: " + c.refusal + "; run: nova-redis help " + verbName + "\n"
					if assert.Equal(t, 2, code, "%s, %q: exit %d stderr %q opens %q; want exit 2, %q and no open", c.name, args, code, errb.String(), got, want) {
						if assert.Equal(t, want, errb.String(), "%s, %q: exit %d stderr %q opens %q; want exit 2, %q and no open", c.name, args, code, errb.String(), got, want) {
							assert.Len(t, got, 0, "%s, %q: exit %d stderr %q opens %q; want exit 2, %q and no open", c.name, args, code, errb.String(), got, want)
						}
					}
				}
				continue
			}
			{
				want := []string{c.want}
				assert.True(t, slices.Equal(got, want), "%s, %q: opened as %q, want %q (stderr %q)", c.name, args, got, want, errb.String())
			}
		}
	}
}

// TestLoginFlagsEchoWhatWasGiven: the login a remedy prints keeps every login
// flag given on the line, even empty or equal to the default, adds what the
// environment set to other than the default, and quotes each value as one
// POSIX shell word (the functional TestFnRemedyRoundTripsTheLogin runs the
// printed command through /bin/sh and a store).
func TestLoginFlagsEchoWhatWasGiven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		env  map[string]string
		want string
	}{
		{[]string{"--addr", "127.0.0.1:6379"}, nil, "--addr 127.0.0.1:6379"},
		{[]string{"--addr", "127.0.0.1:6379", "--user", ""}, map[string]string{UserEnv: "wronguser"}, "--addr 127.0.0.1:6379 --user ''"},
		{[]string{"--addr", "127.0.0.1:6379", "--password-env", PasswordEnv}, map[string]string{PasswordEnvEnv: "OTHER_PW"}, "--addr 127.0.0.1:6379 --password-env NOVA_REDIS_PASSWORD"},
		{[]string{"--addr", "127.0.0.1:6379", "--user", "fn'user"}, nil, `--addr 127.0.0.1:6379 --user 'fn'\''user'`},
		{[]string{"--addr", "127.0.0.1:6379"}, map[string]string{UserEnv: "coordinator", PasswordEnvEnv: "SEAT_PW"}, "--addr 127.0.0.1:6379 --user coordinator --password-env SEAT_PW"},
		{[]string{"--addr", "[::1]:6379"}, nil, "--addr '[::1]:6379'"},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("fn check", flag.ContinueOnError)
		l := loginFromFlags(fs)
		{
			err := fs.Parse(c.args)
			require.NoError(t, err, err)
		}
		if err := l.check(deps{getenv: func(k string) string {
			if k == "OTHER_PW" || k == "SEAT_PW" || k == PasswordEnv {
				return "pw"
			}
			return c.env[k]
		}}); err != nil {
			require.NoError(t, err, err)
		}
		{
			got := l.flags()
			assert.Equal(t, c.want, got, "%q with %v: flags() = %q, want %q", c.args, c.env, got, c.want)
		}
	}
}
