// The acl verb (nova-tools #4333): `nova-sprint acl check` reads the store's
// ACL LIST as the admin user and diffs it against the declared rows
// (internal/nsprint/acl), one ACL DRIFT line per drifted user. It never
// writes the ACL: --fix is refused with the play command, the play being the
// only writer. Exit 0 no drift, 1 drift, 2 could not check (usage, rows,
// password, store) or --fix.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/acl"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "acl",
		Summary: "check [--redis <addr>] [--rows <file>] [--admin-password-env NAME]: the store's live ACL LIST against the declared rows, one ACL DRIFT line per drifted user; --fix is refused (the play is the only writer)",
		Run:     runACL,
	})
}

// aclLister is the one read of the store: ACL LIST as the given user.
type aclLister func(ctx context.Context, addr, user, password string) ([]string, error)

// redisACLList is the real read. A test hands aclCheck a canned listing
// instead; the functional test runs this one.
func redisACLList(ctx context.Context, addr, user, password string) ([]string, error) {
	c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password, MaxRetries: -1})
	defer c.Close()
	return c.Do(ctx, "ACL", "LIST").StringSlice()
}

func runACL(ctx context.Context, args []string, out, errOut io.Writer) int {
	return aclCheck(ctx, args, out, errOut, os.Getenv, redisACLList)
}

// aclCheck is the verb with its two seams, the environment and the store
// read, passed in so a test sets neither process state nor a package var.
func aclCheck(ctx context.Context, args []string, out, errOut io.Writer, getenv func(string) string, list aclLister) int {
	if len(args) == 0 {
		return refuse(errOut, "acl", "want check")
	}
	if args[0] != "check" {
		return refuse(errOut, "acl", "unknown subverb "+args[0]+"; want check")
	}
	const name = "acl check"
	fs, addr := lifeFlags(name)
	rows := fs.String("rows", acl.DefaultRowsPath, "the declared rows, <user> TAB <rules> per line (the play installs them beside the server)")
	adminEnv := fs.String("admin-password-env", fleetbuild.DefaultAdminEnv, "the variable holding the store's admin password")
	fix := fs.Bool("fix", false, "refused: the play is the only writer of the store's ACL; the verb prints its command")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, name, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, name, "takes flags, not positional arguments")
	}
	if *fix {
		fmt.Fprintf(errOut, "REFUSED acl check --fix: the play is the only writer of the store's ACL, never a hand ACL SETUSER; run: %s\n", acl.PlayCommand)
		return 2
	}
	f, err := os.Open(*rows)
	if err != nil {
		fmt.Fprintf(errOut, "ACL CHECK REFUSED rows=%s reason=%s remedy=pass --rows <file>, or run the play (%s) that installs %s\n",
			oneline.Field(*rows), oneline.Field(err.Error()), acl.PlayCommand, acl.DefaultRowsPath)
		return 2
	}
	declared, err := acl.ParseRows(f)
	f.Close()
	if err != nil {
		fmt.Fprintf(errOut, "ACL CHECK REFUSED rows=%s reason=%s remedy=fix the rows file (<user> TAB <rules>, no password)\n",
			oneline.Field(*rows), oneline.Field(err.Error()))
		return 2
	}
	store := lifeAddr(*addr)
	password := getenv(*adminEnv)
	if password == "" {
		fmt.Fprintf(errOut, "ACL CHECK REFUSED store=%s reason=no-admin-password env=%s remedy=export %s=$(ssh <store host> 'sudo cat /var/lib/nova-redis/admin.pass'), then rerun\n",
			oneline.Field(store), oneline.Field(*adminEnv), oneline.Field(*adminEnv))
		return 2
	}
	lines, err := list(ctx, store, "admin", password)
	if err != nil {
		fmt.Fprintf(errOut, "ACL CHECK REFUSED store=%s reason=%s remedy=the admin user reads ACL LIST; check the address and %s\n",
			oneline.Field(store), oneline.Field(strings.TrimSpace(err.Error())), oneline.Field(*adminEnv))
		return 2
	}
	live, err := acl.ParseList(lines)
	if err != nil {
		fmt.Fprintf(errOut, "ACL CHECK REFUSED store=%s reason=%s\n", oneline.Field(store), oneline.Field(err.Error()))
		return 2
	}
	drift := acl.Diff(declared, live)
	for _, d := range drift {
		fmt.Fprintln(out, d.Line())
	}
	if len(drift) == 0 {
		fmt.Fprintf(out, "ACL CHECK OK store=%s users=%d rows=%s\n", oneline.Field(store), len(declared), oneline.Field(*rows))
		return 0
	}
	fmt.Fprintf(out, "ACL CHECK DRIFT store=%s drifted=%d users=%d rows=%s converge=%s\n",
		oneline.Field(store), len(drift), len(declared), oneline.Field(*rows), oneline.Quote(acl.PlayCommand))
	return 1
}
