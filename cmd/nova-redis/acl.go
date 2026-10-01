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

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisacl"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
)

// aclServer is the store the acl verbs read and write; aclStore is the one
// over go-redis, and the unit tests hand in a fake.
type aclServer interface {
	// Read is every named user's live ACL and the store's catalog, in two
	// round trips.
	Read(ctx context.Context, users []string) (map[string]redisacl.Live, redisacl.Catalog, error)
	SetUser(ctx context.Context, name string, rules []string) error
	// Save writes the ACL to the store's ACL file; saved is false, with no
	// error, when the store keeps none.
	Save(ctx context.Context) (saved bool, err error)
}

type aclStore struct{ c redis.UniversalClient }

func (s aclStore) Read(ctx context.Context, users []string) (map[string]redisacl.Live, redisacl.Catalog, error) {
	pipe := s.c.Pipeline()
	cats := pipe.Do(ctx, "ACL", "CAT")
	got := make([]*redis.Cmd, len(users))
	for i, u := range users {
		got[i] = pipe.Do(ctx, "ACL", "GETUSER", u)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, err
	}
	live := map[string]redisacl.Live{}
	for i, u := range users {
		live[u] = parseGetUser(got[i].Val())
	}
	names, err := cats.StringSlice()
	if err != nil {
		return nil, nil, fmt.Errorf("ACL CAT: %w", err)
	}
	pipe = s.c.Pipeline()
	each := make([]*redis.Cmd, len(names))
	for i, n := range names {
		each[i] = pipe.Do(ctx, "ACL", "CAT", n)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, err
	}
	cat := redisacl.Catalog{}
	for i, n := range names {
		cmds, err := each[i].StringSlice()
		if err != nil {
			return nil, nil, fmt.Errorf("ACL CAT %s: %w", n, err)
		}
		cat[strings.ToLower(n)] = cmds
	}
	return live, cat, nil
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

func cmdACL(args []string, stdout, stderr io.Writer, d deps) int {
	return aclVerb(args, stdout, stderr, d, func(ctx context.Context, store login) (aclServer, func() error, error) {
		conn, err := connect(ctx, store, d)
		if err != nil {
			return nil, nil, err
		}
		return aclStore{conn.Client()}, conn.Close, nil
	})
}

// aclOpener opens the store for a login check accepted.
type aclOpener func(ctx context.Context, store login) (aclServer, func() error, error)

func aclVerb(args []string, stdout, stderr io.Writer, d deps, open aclOpener) int {
	if len(args) == 0 {
		return refuse(stderr, " acl", "no subverb given; render prints this build's users, check compares the store's with them, apply writes the ones that differ")
	}
	sub := args[0]
	if sub != "render" && sub != "check" && sub != "apply" {
		return refuse(stderr, " acl", fmt.Sprintf("unknown subverb %q; want render, check or apply", sub))
	}
	fs := flag.NewFlagSet("acl "+sub, flag.ContinueOnError)
	var store login
	var dryRun *bool
	required := []string{}
	if sub != "render" {
		store = loginFlags(fs)
		required = append(required, "addr")
	}
	if sub == "apply" {
		dryRun = fs.Bool("dry-run", false, "print the users apply would set (ACL WOULD-SET) and write nothing")
	}
	if !parse(fs, args[1:], stderr, required...) {
		return 2
	}
	lib := library()
	digest, err := lib.Digest()
	if err != nil {
		return refuse(stderr, " acl "+sub, fmt.Sprintf("this binary's library does not build: %s; fix the Lua and rebuild", err))
	}
	users, err := redisacl.Render(lib)
	if err != nil {
		return refuse(stderr, " acl "+sub, fmt.Sprintf("this build's roles do not render: %s; fix internal/redisacl and rebuild", err))
	}
	if sub == "render" {
		fns := 0
		for _, u := range users {
			fmt.Fprintln(stdout, u.Line())
			fns = max(fns, u.Functions)
		}
		fmt.Fprintf(stdout, "ACL RENDER OK users=%d functions=%d library=%s\n", len(users), fns, digest)
		return 0
	}
	if err := store.check(d); err != nil {
		return refuse(stderr, " acl "+sub, err.Error())
	}
	at := oneline.Field(*store.addr)
	failed := func(err error) int {
		cause, _, _ := strings.Cut(oneline.Err(err), "; next: ")
		fmt.Fprintf(stderr, "ACL %s FAILED store=%s err=%s remedy=%q\n", strings.ToUpper(sub), at, cause,
			"log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER: check --user ("+UserEnv+") and the password in "+*store.passwordEnv+", then nova-redis acl "+sub+" "+store.flags())
		if answered(err) {
			return 1
		}
		return 2
	}
	ctx := context.Background()
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
	live, cat, err := srv.Read(ctx, names)
	if err != nil {
		return failed(err)
	}
	var differ []redisacl.User
	for _, u := range users {
		dr := redisacl.Compare(u, live[u.Name], cat)
		switch {
		case dr.None():
			fmt.Fprintf(stdout, "ACL OK user=%s role=%s\n", oneline.Field(u.Name), u.Role)
		case dr.Missing:
			fmt.Fprintf(stdout, "ACL MISSING user=%s role=%s\n", oneline.Field(u.Name), u.Role)
			differ = append(differ, u)
		default:
			fmt.Fprintf(stdout, "ACL DRIFT user=%s role=%s%s\n", oneline.Field(u.Name), u.Role, dr.Fields())
			differ = append(differ, u)
		}
	}
	if sub == "check" {
		if len(differ) == 0 {
			fmt.Fprintf(stdout, "ACL CHECK OK users=%d library=%s store=%s\n", len(users), digest, at)
			return 0
		}
		fmt.Fprintf(stdout, "ACL CHECK DRIFT users=%d differ=%d library=%s store=%s remedy=%q\n", len(users), len(differ), digest, at,
			"nova-redis acl apply "+store.flags()+" sets the users that differ")
		return 1
	}
	if *dryRun {
		for _, u := range differ {
			fmt.Fprintf(stdout, "ACL WOULD-SET user=%s role=%s\n", oneline.Field(u.Name), u.Role)
		}
		fmt.Fprintf(stdout, "ACL APPLY OK dry-run=true users=%d set=0 would=%d library=%s store=%s\n", len(users), len(differ), digest, at)
		return 0
	}
	for i, u := range differ {
		if err := srv.SetUser(ctx, u.Name, u.Rules); err != nil {
			fmt.Fprintf(stdout, "ACL APPLY FAILED users=%d set=%d user=%s\n", len(users), i, oneline.Field(u.Name))
			return failed(err)
		}
		fmt.Fprintf(stdout, "ACL SET user=%s role=%s\n", oneline.Field(u.Name), u.Role)
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
	fmt.Fprintf(stdout, "ACL APPLY OK users=%d set=%d saved=%s library=%s store=%s\n", len(users), len(differ), saved, digest, at)
	return 0
}
