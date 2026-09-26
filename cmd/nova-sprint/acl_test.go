package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aclRowsFixture = "../../internal/nsprint/acl/testdata/acl-rows.tsv"

// cannedACL swaps the store read for the redis-server 8.10.2 listing of the
// mirror rows, edited by edit; it records the login the verb dialled with.
func cannedACL(t *testing.T, edit func([]string) []string, err error) *[3]string {
	t.Helper()
	b, rerr := os.ReadFile("../../internal/nsprint/acl/testdata/acl-list-redis-8.10.2.txt")
	if rerr != nil {
		t.Fatal(rerr)
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if edit != nil {
		ls = edit(ls)
	}
	var got [3]string
	prev := aclList
	aclList = func(_ context.Context, addr, user, password string) ([]string, error) {
		got = [3]string{addr, user, password}
		return ls, err
	}
	t.Cleanup(func() { aclList = prev })
	return &got
}

// No drift: one OK line, exit 0, read as admin with the password from the env.
func TestACLCheckClean(t *testing.T) {
	t.Setenv("NS_ADMIN", "pw")
	got := cannedACL(t, nil, nil)
	code, stdout, stderr := runSprint("acl", "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture)
	if code != 0 || stderr != "" || stdout != "ACL CHECK OK store=127.0.0.1:9 users=12 rows="+aclRowsFixture+"\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if *got != [3]string{"127.0.0.1:9", "admin", "pw"} {
		t.Fatalf("dialled %v, want the admin user with NS_ADMIN", *got)
	}
}

// Drift: a line per drifted user, then the DRIFT line naming the play; exit 1.
func TestACLCheckDrift(t *testing.T) {
	t.Setenv("NS_ADMIN", "pw")
	cannedACL(t, func(ls []string) []string {
		for i, l := range ls {
			if strings.HasPrefix(l, "user bench ") {
				ls[i] = strings.Replace(l, " ~cfg:ci ", " ", 1) + " +keys"
			}
		}
		return ls
	}, nil)
	code, stdout, stderr := runSprint("acl", "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture)
	want := `ACL DRIFT user=bench missing="~cfg:ci" extra="+keys"` + "\n" +
		`ACL CHECK DRIFT store=127.0.0.1:9 drifted=1 users=12 rows=` + aclRowsFixture + ` converge="make -C rowan-tools/fleet store"` + "\n"
	if code != 1 || stderr != "" || stdout != want {
		t.Fatalf("exit %d stdout %q stderr %q; want\n%s", code, stdout, stderr, want)
	}
}

// Every refusal prints one line on stderr and exits 2; --fix and a missing
// password never reach the store.
func TestACLCheckRefusals(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "rows.tsv")
	if err := os.WriteFile(bad, []byte("bench\ton >pw ~*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := cannedACL(t, nil, errors.New("NOPERM User admin has no permissions to run the 'acl|list' command"))
	for _, c := range []struct {
		env  string
		args []string
		want string
	}{
		{"pw", []string{"acl", "check", "--fix"}, "REFUSED acl check --fix: the play is the only writer of the store's ACL, never a hand ACL SETUSER; run: make -C rowan-tools/fleet store\n"},
		{"pw", []string{"acl", "check", "--rows", filepath.Join(t.TempDir(), "none.tsv")}, "ACL CHECK REFUSED rows="},
		{"pw", []string{"acl", "check", "--rows", bad}, "reason=rows\\x20line\\x201\\x20user\\x20bench:\\x20a\\x20password\\x20token"},
		{"", []string{"acl", "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture}, "ACL CHECK REFUSED store=127.0.0.1:9 reason=no-admin-password env=NS_ADMIN remedy=export NS_ADMIN="},
		{"pw", []string{"acl", "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture}, "ACL CHECK REFUSED store=127.0.0.1:9 reason=NOPERM"},
		{"pw", []string{"acl"}, "nova-sprint acl: want check"},
		{"pw", []string{"acl", "fix"}, "nova-sprint acl: unknown subverb fix"},
		{"pw", []string{"acl", "check", "extra"}, "takes flags, not positional arguments"},
	} {
		t.Setenv("NS_ADMIN", c.env)
		*got = [3]string{}
		code, stdout, stderr := runSprint(c.args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, c.want) || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d stdout %q stderr %q; want one line holding %q, exit 2", c.args, code, stdout, stderr, c.want)
		}
		if strings.Contains(c.want, "REFUSED acl check --fix") || c.env == "" {
			if *got != ([3]string{}) {
				t.Errorf("%v dialled the store: %v", c.args, *got)
			}
		}
	}
}
