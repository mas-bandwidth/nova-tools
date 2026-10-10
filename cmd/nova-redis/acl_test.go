package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
)

// fakeACL is a store's ACL: users by name as GETUSER prints them, the
// catalog, what was set and saved; it refuses like the real one when told.
type fakeACL struct {
	live     map[string]redisacl.Live
	cat      redisacl.Catalog
	set      []string
	saves    int
	noFile   bool
	readErr  error
	setErr   error
	opened   int
	setRules map[string][]string
}

func (f *fakeACL) Read(_ context.Context, users []string) (map[string]redisacl.Live, []string, redisacl.Catalog, error) {
	if f.readErr != nil {
		return nil, nil, nil, f.readErr
	}
	out := map[string]redisacl.Live{}
	for _, u := range append(users, "default") {
		out[u] = f.live[u]
	}
	var all []string
	for u := range f.live {
		all = append(all, u)
	}
	return out, all, f.cat, nil
}

func (f *fakeACL) SetUser(_ context.Context, name string, rules []string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.set = append(f.set, name)
	if f.setRules == nil {
		f.setRules = map[string][]string{}
	}
	f.setRules[name] = rules
	// The store now prints the user as set.
	var keys, cmds []string
	for _, r := range rules {
		switch {
		case strings.HasPrefix(r, "~") || strings.HasPrefix(r, "%"):
			keys = append(keys, r)
		case strings.HasPrefix(r, "+") || strings.HasPrefix(r, "-"):
			cmds = append(cmds, r)
		}
	}
	f.live[name] = redisacl.Live{Exists: true, On: true, Keys: strings.Join(keys, " "), Commands: strings.Join(cmds, " ")}
	return nil
}

func (f *fakeACL) Save(context.Context) (bool, error) {
	f.saves++
	return !f.noFile, nil
}

func aclRun(t *testing.T, f *fakeACL, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	d := deps{getenv: func(k string) string {
		switch k {
		case "ADMIN_PW":
			return "secret"
		case "NEW_PW":
			return "new-secret"
		}
		return ""
	}}
	d.aclOpen = func(context.Context, login) (aclServer, func() error, error) {
		f.opened++
		return f, func() error { return nil }, nil
	}
	code := run(append([]string{"acl"}, args...), &out, &errb, d)
	return code, out.String(), errb.String()
}

var login4 = []string{"--redis", "127.0.0.1:6379", "--user", "admin", "--password-env", "ADMIN_PW"}

// sourced is every rendered user's password source, for an apply that
// creates them.
var sourced = []string{"--password-env-for", "coordinator=NEW_PW", "--password-env-for", "bench=NEW_PW", "--password-env-for", "ns-table=NEW_PW", "--password-env-for", "ns-friend=NEW_PW"}

// render opens no store and prints one pasteable line per role, then the
// summary with this binary's library digest.
func TestACLRenderOpensNoStore(t *testing.T) {
	t.Parallel()
	f := &fakeACL{}
	code, out, errs := aclRun(t, f, "render")
	require.Equal(t, 0, code, errs)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	n := len(redisacl.Families)
	require.Len(t, lines, n+5)
	for i, f := range redisacl.Families {
		assert.Equal(t, "ACL FAMILY name="+f.Name+" keys="+strings.Join(f.Patterns, ","), lines[i])
	}
	for i, user := range []string{"coordinator", "bench", "ns-table", "ns-friend"} {
		assert.True(t, strings.HasPrefix(lines[n+i], "ACL SETUSER "+user+" on "), lines[n+i])
	}
	assert.Regexp(t, `^ACL RENDER OK users=4 functions=[1-9][0-9]* library=[0-9a-f]{16}$`, lines[n+4])
	assert.Equal(t, 0, f.opened)
}

// check on an empty store says MISSING per user and exits 1; apply sets each
// and saves; check after it says OK and exits 0; a second apply sets none.
func TestACLCheckApplyConverge(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, out, _ := aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.Equal(t, 1, code)
	assert.Equal(t, 4, strings.Count(out, "ACL MISSING user="))
	assert.Contains(t, out, "ACL CHECK DRIFT users=4 differ=4 ")
	assert.Contains(t, out, `remedy="nova-redis acl apply --redis 127.0.0.1:6379 --user admin --password-env ADMIN_PW sets the users that differ"`)
	assert.Empty(t, f.set, "check wrote")

	code, out, _ = aclRun(t, f, append(append([]string{"apply", "--dry-run"}, login4...), sourced...)...)
	assert.Equal(t, 0, code)
	assert.Equal(t, 4, strings.Count(out, "ACL WOULD-SET "))
	assert.Empty(t, f.set, "--dry-run wrote")

	code, out, _ = aclRun(t, f, append([]string{"apply"}, login4...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend: a user the store lacks is created only with a password")
	assert.Empty(t, f.set, "a refused apply wrote")

	code, out, errs := aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)
	assert.Equal(t, []string{"coordinator", "bench", "ns-table", "ns-friend"}, f.set)
	assert.Equal(t, ">new-secret", f.setRules["bench"][0], "a created user gets its password from the named variable")
	assert.NotContains(t, out, "new-secret")
	assert.Contains(t, out, "ACL APPLY OK users=4 set=4 saved=acl-file ")
	assert.Equal(t, 1, f.saves)

	code, out, _ = aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.Equal(t, 0, code, out)
	assert.Equal(t, 4, strings.Count(out, "ACL OK user="))
	code, out, _ = aclRun(t, f, append([]string{"apply"}, login4...)...)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "ACL APPLY OK users=4 set=0 saved=none ")
	assert.Equal(t, 1, f.saves, "nothing set, nothing saved")
}

// TestACLApplyDryRunNeverDials: acl apply --dry-run prints the plan this
// build renders and dials nothing. The plan comes from the rendering alone,
// so a store that is not there cannot make the dry run fail, and the skeleton
// never reports a --dry-run it was not read (STANDARD "a verb that writes has
// a dry run"; docs/CLI.md's acl apply bullet; SPEC-REDIS rule 5 keeps this
// unit test off the network).
func TestACLApplyDryRunNeverDials(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, out, errs := aclRun(t, f, append(append([]string{"apply", "--dry-run"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)
	assert.Zero(t, f.opened, "acl apply --dry-run dialled the store")
	assert.Zero(t, f.saves, "acl apply --dry-run saved the ACL")
	assert.Empty(t, f.set, "acl apply --dry-run wrote")
	assert.Equal(t, 4, strings.Count(out, "ACL WOULD-SET "), out)
	assert.Contains(t, out, "ACL APPLY OK dry-run=true users=4 set=0 would=4 ")
}

// A drifted user is named with what apply would change, and only it is set;
// a store with no ACL file says so.
func TestACLApplySetsOnlyTheUsersThatDiffer(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{"legacy": {Exists: true, On: true}, "default": {Exists: true, On: false}}, cat: redisacl.Catalog{}, noFile: true}
	_, _, _ = aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	f.set = nil
	l := f.live["bench"]
	l.Keys += " ~legacy:*"
	f.live["bench"] = l
	code, out, _ := aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "ACL DRIFT user=bench role=member keys-=~legacy:*")
	assert.Contains(t, out, "NOTE ACL EXTRA user=legacy: no role renders it")
	assert.Contains(t, out, "NOTE ACL DEFAULT on=false nopass=false")
	code, out, _ = aclRun(t, f, append([]string{"apply"}, login4...)...)
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"bench"}, f.set)
	assert.Contains(t, out, "saved=no-acl-file")
}

// A live user that matches its rendering in every other way but carries the
// nopass flag is drift: check names it and exits 1; apply refuses to mend it
// unless a --password-env-for source names the user, and mends it with
// resetpass and that password when one does.
func TestANopassUserIsDrift(t *testing.T) {
	t.Parallel()
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	code, _, errs := aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)

	l := f.live["bench"]
	l.NoPass = true
	f.live["bench"] = l

	code, out, _ := aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL DRIFT user=bench role=member nopass=true")
	assert.Contains(t, out, "ACL CHECK DRIFT users=4 differ=1")
	assert.Equal(t, 3, strings.Count(out, "ACL OK user="), out)

	f.set = nil
	code, out, _ = aclRun(t, f, append([]string{"apply"}, login4...)...)
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "ACL APPLY REFUSED users=4 nopass=bench")
	assert.Empty(t, f.set, "a refused apply wrote")

	code, out, errs = aclRun(t, f, append(append([]string{"apply"}, login4...), sourced...)...)
	require.Equal(t, 0, code, errs)
	assert.Equal(t, []string{"bench"}, f.set)
	assert.Equal(t, "resetpass", f.setRules["bench"][0], "the nopass flag goes before the new password")
	assert.Equal(t, ">new-secret", f.setRules["bench"][1], "the user gets its password from the named variable")
	assert.NotContains(t, out, "new-secret")

	code, out, _ = aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "ACL OK user=bench")
}

// Refusals before the store: a missing --addr, an unknown subverb, a login
// whose password variable is empty; a store failure is one FAILED line.
func TestACLRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no subverb", []string{}, 2, "acl wants one of its verbs"},
		{"unknown", []string{"drop"}, 2, `unknown verb "acl drop" in acl`},
		{"no addr", []string{"check"}, 2, "--addr is required"},
		{"empty password", []string{"check", "--addr", "127.0.0.1:6379", "--user", "admin", "--password-env", "NOT_SET"}, 2, "NOT_SET is empty"},
		{"render takes no addr", []string{"render", "--addr", "x:1"}, 2, "unknown flag --addr; acl render takes no flags"},
		{"bad password source", []string{"apply", "--addr", "127.0.0.1:6379", "--password-env-for", "bench"}, 2, "--password-env-for wants <user>=<VARIABLE>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeACL{live: map[string]redisacl.Live{}}
			code, out, errs := aclRun(t, f, append([]string{}, tc.args...)...)
			assert.Equal(t, tc.code, code)
			assert.Empty(t, out)
			assert.Contains(t, errs, tc.want)
			assert.Equal(t, 0, f.opened)
		})
	}
	f := &fakeACL{readErr: errors.New("NOPERM this user has no permissions to run the 'acl|getuser' command")}
	code, _, errs := aclRun(t, f, append([]string{"check"}, login4...)...)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "ACL CHECK FAILED store=127.0.0.1:6379")
	assert.Contains(t, errs, "remedy=")
}

// parseGetUser reads both reply shapes and never the passwords.
func TestParseGetUser(t *testing.T) {
	t.Parallel()
	resp3 := map[any]any{"flags": []any{"on"}, "passwords": []any{"hash"}, "commands": "-@all +get", "keys": "~a:*", "channels": "", "selectors": []any{}}
	resp2 := []any{"flags", []any{"off"}, "passwords", []any{}, "commands", "-@all", "keys", "", "channels", "&*", "selectors", []any{[]any{"x"}}}
	assert.Equal(t, redisacl.Live{Exists: true, On: true, Commands: "-@all +get", Keys: "~a:*"}, parseGetUser(resp3))
	assert.True(t, parseGetUser(map[any]any{"flags": []any{"on", "nopass"}}).NoPass)
	assert.Equal(t, redisacl.Live{Exists: true, Commands: "-@all", Channels: "&*", Selectors: 1}, parseGetUser(resp2))
	assert.Equal(t, redisacl.Live{}, parseGetUser(nil))
}
