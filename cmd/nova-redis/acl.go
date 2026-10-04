package main

// The acl verbs put the fleet store's ACL users in the shape this build
// renders (internal/redisacl): one user per role, its key families, its
// command categories and FCALL of exactly the functions the library this
// binary embeds registers in the role's files.
//
//   - acl render opens no store: one ACL SETUSER line per user, then
//     ACL RENDER OK users=<n> functions=<n> library=<digest>.
//   - acl check reads the live ACL (ACL GETUSER per user, ACL CAT for what
//     the categories mean on that store) and writes nothing: ACL OK, ACL
//     DRIFT (what apply would add and remove) or ACL MISSING per user, then
//     ACL CHECK OK|DRIFT; exit 1 on any drift.
//   - acl apply is the same comparison, then ACL SETUSER for each user that
//     differs (ACL SET lines) and ACL SAVE when the store keeps an ACL file;
//     --dry-run prints ACL WOULD-SET and writes nothing. A password is never
//     set or read: a user keeps the one it has, and a new user has none
//     until its seat's password is set.
//
// check and apply log in as --user with the password in --password-env, the
// admin user that may run ACL, through connect as every nova-redis verb does.
//
// The acl verbs print their own lines (a multi-word head, ACL FAMILY, is a
// line the skeleton cannot render), so they are Prints verbs.

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisacl"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// aclServer is the store the acl verbs read and write; aclStore is the one
// over go-redis, and the unit tests hand in a fake.
type aclServer interface {
	// Read is every named user's live ACL and the default user's, the names
	// of every user the store has, and the store's catalog, in two round
	// trips.
	Read(ctx context.Context, users []string) (map[string]redisacl.Live, []string, redisacl.Catalog, error)
	SetUser(ctx context.Context, name string, rules []string) error
	// Save writes the ACL to the store's ACL file; saved is false, with no
	// error, when the store keeps none.
	Save(ctx context.Context) (saved bool, err error)
}

type aclStore struct{ c redis.UniversalClient }

func (s aclStore) Read(ctx context.Context, users []string) (map[string]redisacl.Live, []string, redisacl.Catalog, error) {
	users = append(append([]string{}, users...), "default")
	pipe := s.c.Pipeline()
	cats := pipe.Do(ctx, "ACL", "CAT")
	all := pipe.Do(ctx, "ACL", "USERS")
	got := make([]*redis.Cmd, len(users))
	for i, u := range users {
		got[i] = pipe.Do(ctx, "ACL", "GETUSER", u)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, nil, err
	}
	live := map[string]redisacl.Live{}
	for i, u := range users {
		live[u] = parseGetUser(got[i].Val())
	}
	everyone, err := all.StringSlice()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ACL USERS: %w", err)
	}
	names, err := cats.StringSlice()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ACL CAT: %w", err)
	}
	pipe = s.c.Pipeline()
	each := make([]*redis.Cmd, len(names))
	for i, n := range names {
		each[i] = pipe.Do(ctx, "ACL", "CAT", n)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, nil, err
	}
	cat := redisacl.Catalog{}
	for i, n := range names {
		cmds, err := each[i].StringSlice()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("ACL CAT %s: %w", n, err)
		}
		cat[strings.ToLower(n)] = cmds
	}
	return live, everyone, cat, nil
}

// parseGetUser reads an ACL GETUSER reply, a map (RESP3) or a flat list of
// pairs (RESP2), nil for a user the store does not have. The passwords
// field is never read.
func parseGetUser(reply any) redisacl.Live {
	fields := map[string]any{}
	switch r := reply.(type) {
	case map[any]any:
		for k, v := range r {
			fields[fmt.Sprint(k)] = v
		}
	case []any:
		for i := 0; i+1 < len(r); i += 2 {
			fields[fmt.Sprint(r[i])] = r[i+1]
		}
	default:
		return redisacl.Live{}
	}
	l := redisacl.Live{Exists: true}
	if flags, ok := fields["flags"].([]any); ok {
		for _, f := range flags {
			l.On = l.On || fmt.Sprint(f) == "on"
			l.NoPass = l.NoPass || fmt.Sprint(f) == "nopass"
		}
	}
	l.Keys, _ = fields["keys"].(string)
	l.Channels, _ = fields["channels"].(string)
	l.Commands, _ = fields["commands"].(string)
	if sel, ok := fields["selectors"].([]any); ok {
		l.Selectors = len(sel)
	}
	return l
}

func (s aclStore) SetUser(ctx context.Context, name string, rules []string) error {
	args := []any{"ACL", "SETUSER", name}
	for _, r := range rules {
		args = append(args, r)
	}
	return s.c.Do(ctx, args...).Err()
}

func (s aclStore) Save(ctx context.Context) (bool, error) {
	err := s.c.Do(ctx, "ACL", "SAVE").Err()
	if err != nil && strings.Contains(err.Error(), "ACL file") {
		return false, nil // the store keeps its ACL in its config, not a file: the users last until it restarts
	}
	return err == nil, err
}

// passwordSources is --password-env-for: user -> the variable holding the
// password apply gives a user it creates.
type passwordSources map[string]string

func (p passwordSources) String() string { return fmt.Sprint(map[string]string(p)) }

// Get is the flag.Getter half, so Call.Get reads the sources back as
// passwordSources (tool.Call).
func (p passwordSources) Get() any { return p }

var _ flag.Getter = passwordSources(nil)

func (p passwordSources) Set(v string) error {
	user, env, ok := strings.Cut(v, "=")
	if !ok || user == "" || !envName.MatchString(env) {
		return fmt.Errorf("--password-env-for wants <user>=<VARIABLE> (capital letters, digits and underscores), got %q", v)
	}
	p[user] = env
	return nil
}

// aclRenderVerb is acl render: prints this build's users, opens no store.
func aclRenderVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "acl render",
		Usage:   "acl render",
		Example: "",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.Prints()
		},
		Run: func(c *tool.Call) *tool.Out { return aclVerbRun(c, d, "render") },
	}
}

// aclCheckVerb is acl check: an inspection of the store's users.
func aclCheckVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "acl check",
		Usage:   "acl check --addr <host:port> [--user <name>] [--password-env <NAME>]",
		Example: "",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.Prints()
			loginFlags(f)
		},
		Run: func(c *tool.Call) *tool.Out { return aclVerbRun(c, d, "check") },
	}
}

// aclApplyVerb is acl apply: a store write of the users that differ.
func aclApplyVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "acl apply",
		Usage:   "acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]",
		Example: "",
		Effect:  tool.LocalWrite,
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.Prints()
			loginFlags(f)
			f.Var(passwordSources{}, "password-env-for", "<user>=<VARIABLE>, repeatable: the variable holding the password a user apply creates gets; a user the store lacks is created only with one")
		},
		Run: func(c *tool.Call) *tool.Out { return aclVerbRun(c, d, "apply") },
	}
}

// aclOpener opens the store for a login check accepted.
type aclOpener func(ctx context.Context, store login) (aclServer, func() error, error)

// aclVerbRun is acl render, check and apply over the store d.aclOpen (or connect)
// opens.
func aclVerbRun(c *tool.Call, d deps, sub string) *tool.Out {
	lib := library()
	digest, err := lib.Digest()
	if err != nil {
		return tool.Refuse(fmt.Sprintf("this binary's library does not build: %s; fix the Lua and rebuild", err))
	}
	users, err := redisacl.Render(lib)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("this build's roles do not render: %s; fix internal/redisacl and rebuild", err))
	}
	if sub == "render" {
		// A family is what an operator reads: its name and its key patterns.
		for _, f := range redisacl.Families {
			line(c.Stdout, "ACL FAMILY", "name", f.Name, "keys", free(strings.Join(f.Patterns, ",")))
		}
		fns := 0
		for _, u := range users {
			// The user's pasteable command (redisacl.User.Line): its rules follow its name.
			line(c.Stdout, "ACL SETUSER", "user", bare{u.Name, u.Name}, "rules", bare{strings.TrimPrefix(u.Line(), "ACL SETUSER "+u.Name+" "), u.Rules})
			fns = max(fns, u.Functions)
		}
		line(c.Stdout, "ACL RENDER OK", "users", len(users), "functions", fns, "library", digest)
		return tool.Exit(0)
	}
	store := loginFrom(c)
	if err := store.check(d); err != nil {
		return tool.Refuse(err.Error())
	}
	at := *store.addr
	failed := func(err error) *tool.Out {
		cause, _, _ := strings.Cut(oneline.Err(err), "; next: ")
		line(c.Stderr, "ACL "+strings.ToUpper(sub)+" FAILED", "store", at, "err", free(cause), "remedy",
			quoted("log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER: check --user ("+UserEnv+") and the password in "+*store.passwordEnv+", then nova-redis acl "+sub+" "+store.flags()))
		if answered(err) {
			return tool.Exit(1)
		}
		return tool.Exit(2)
	}
	ctx := context.Background()
	open := d.aclOpen
	if open == nil {
		open = func(ctx context.Context, store login) (aclServer, func() error, error) {
			conn, err := connect(ctx, store, d)
			if err != nil {
				return nil, nil, err
			}
			return aclStore{conn.Client()}, conn.Close, nil
		}
	}
	srv, closeStore, err := open(ctx, store)
	if err != nil {
		return failed(err)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = closeStore() }()
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Name
	}
	live, everyone, cat, err := srv.Read(ctx, names)
	if err != nil {
		return failed(err)
	}
	rendered := map[string]bool{"default": true}
	for _, n := range names {
		rendered[n] = true
	}
	var differ []redisacl.User
	for _, u := range users {
		dr := redisacl.Compare(u, live[u.Name], cat)
		switch {
		case dr.None():
			line(c.Stdout, "ACL OK", "user", u.Name, "role", u.Role)
		case dr.Missing:
			line(c.Stdout, "ACL MISSING", "user", u.Name, "role", u.Role)
			differ = append(differ, u)
		default:
			// Fields is the drift as the line spells it; --json carries every name.
			line(c.Stdout, "ACL DRIFT", "user", u.Name, "role", u.Role, "drift", bare{strings.TrimPrefix(dr.Fields(), " "), dr})
			differ = append(differ, u)
		}
	}
	// Users the rendering does not name are left as they are, and named.
	sort.Strings(everyone)
	for _, n := range everyone {
		if !rendered[n] {
			note(c.Stdout, fmt.Sprintf("ACL EXTRA user=%s: no role renders it; acl apply leaves it as it is", oneline.Field(n)))
		}
	}
	if def := live["default"]; def.Exists {
		note(c.Stdout, fmt.Sprintf("ACL DEFAULT on=%t nopass=%t", def.On, def.NoPass))
	}
	if sub == "check" {
		if len(differ) == 0 {
			line(c.Stdout, "ACL CHECK OK", "users", len(users), "library", digest, "store", at)
			return tool.Exit(0)
		}
		line(c.Stdout, "ACL CHECK DRIFT", "users", len(users), "differ", len(differ), "library", digest, "store", at,
			"remedy", quoted("nova-redis acl apply "+store.flags()+" sets the users that differ"))
		return tool.Exit(1)
	}
	// A user the store lacks is created only with a password from the
	// variable --password-env-for names: never on with none.
	sources := c.Get("password-env-for").(passwordSources)
	var unsourced []string
	for _, u := range differ {
		if live[u.Name].Exists {
			continue
		}
		if env := sources[u.Name]; env == "" || d.getenv(env) == "" {
			unsourced = append(unsourced, u.Name)
		}
	}
	if len(unsourced) > 0 {
		line(c.Stdout, "ACL APPLY REFUSED", "users", len(users), "missing", free(strings.Join(unsourced, ",")),
			"", why("a user the store lacks is created only with a password"),
			"", next("nova-redis acl apply "+store.flags()+" --password-env-for "+unsourced[0]+"=<VARIABLE> (the variable set, under nova-secrets exec --only <VARIABLE>)"))
		return tool.Exit(1)
	}
	if c.DryRun() {
		for _, u := range differ {
			line(c.Stdout, "ACL WOULD-SET", "user", u.Name, "role", u.Role)
		}
		line(c.Stdout, "ACL APPLY OK", "dry-run", true, "users", len(users), "set", 0, "would", len(differ), "library", digest, "store", at)
		return tool.Exit(0)
	}
	for i, u := range differ {
		rules := u.Rules
		if !live[u.Name].Exists {
			// The password goes to the store as a rule and nowhere else.
			rules = append([]string{">" + d.getenv(sources[u.Name])}, rules...)
		}
		if err := srv.SetUser(ctx, u.Name, rules); err != nil {
			line(c.Stdout, "ACL APPLY FAILED", "users", len(users), "set", i, "user", u.Name)
			return failed(err)
		}
		line(c.Stdout, "ACL SET", "user", u.Name, "role", u.Role)
	}
	saved := "none"
	if len(differ) > 0 {
		ok, err := srv.Save(ctx)
		if err != nil {
			return failed(fmt.Errorf("the users are set and ACL SAVE failed, so a restart loses them: %w", err))
		}
		saved = "no-acl-file"
		if ok {
			saved = "acl-file"
		}
	}
	line(c.Stdout, "ACL APPLY OK", "users", len(users), "set", len(differ), "saved", saved, "library", digest, "store", at)
	return tool.Exit(0)
}
