package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aclRowsFixture = "../../internal/nsprint/acl/testdata/acl-rows.tsv"

// aclEnv is a getenv holding only NS_ADMIN=password ("" is unset).
func aclEnv(password string) func(string) string {
	return func(k string) string {
		if k == "NS_ADMIN" {
			return password
		}
		return ""
	}
}

// cannedACL is a store read answering the redis-server 8.10.2 listing of the
// mirror rows, edited by edit; it records the login the verb dialled with.
func cannedACL(t *testing.T, edit func([]string) []string, err error) (aclLister, *[3]string) {
	t.Helper()
	b, rerr := os.ReadFile("../../internal/nsprint/acl/testdata/acl-list-redis-8.10.2.txt")
	if rerr != nil {
		t.Fatal(rerr)
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if edit != nil {
		ls = edit(ls)
	}
	got := new([3]string)
	return func(_ context.Context, addr, user, password string) ([]string, error) {
		*got = [3]string{addr, user, password}
		return ls, err
	}, got
}

func runACLCheck(getenv func(string) string, list aclLister, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := aclCheck(context.Background(), args, &stdout, &stderr, getenv, list)
	return code, stdout.String(), stderr.String()
}

// No drift: one OK line, exit 0, read as admin with the password from NS_ADMIN.
func TestACLCheckClean(t *testing.T) {
	t.Parallel()

	list, got := cannedACL(t, nil, nil)
	code, stdout, stderr := runACLCheck(aclEnv("pw"), list, "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture)
	if code != 0 || stderr != "" || stdout != "ACL CHECK OK store=127.0.0.1:9 users=12 rows="+aclRowsFixture+"\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if *got != [3]string{"127.0.0.1:9", "admin", "pw"} {
		t.Fatalf("dialled %v, want the admin user with NS_ADMIN", *got)
	}
}

// Drift: a line per drifted user, then the DRIFT line naming the play; exit 1.
func TestACLCheckDrift(t *testing.T) {
	t.Parallel()

	list, _ := cannedACL(t, func(ls []string) []string {
		for i, l := range ls {
			if strings.HasPrefix(l, "user bench ") {
				ls[i] = strings.Replace(l, " ~cfg:ci ", " ", 1) + " +keys"
			}
		}
		return ls
	}, nil)
	code, stdout, stderr := runACLCheck(aclEnv("pw"), list, "check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture)
	want := `ACL DRIFT user=bench missing="~cfg:ci" extra="+keys"` + "\n" +
		`ACL CHECK DRIFT store=127.0.0.1:9 drifted=1 users=12 rows=` + aclRowsFixture + ` converge="make -C rowan-tools/fleet store"` + "\n"
	if code != 1 || stderr != "" || stdout != want {
		t.Fatalf("exit %d stdout %q stderr %q; want\n%s", code, stdout, stderr, want)
	}
}

// --fix through the dispatcher: refused with the play command, exit 2.
func TestACLCheckFixRefused(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint("acl", "check", "--fix")
	want := "REFUSED acl check --fix: the play is the only writer of the store's ACL, never a hand ACL SETUSER; run: make -C rowan-tools/fleet store\n"
	if code != 2 || stdout != "" || stderr != want {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// Every refusal prints one line on stderr and exits 2; --fix and a missing
// password never reach the store.
func TestACLCheckRefusals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bad := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(bad, []byte("bench\ton >pw ~*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		env    string
		args   []string
		want   string
		dialed bool
	}{
		{"pw", []string{"check", "--fix"}, "REFUSED acl check --fix: the play is the only writer", false},
		{"pw", []string{"check", "--rows", filepath.Join(dir, "none.tsv")}, "ACL CHECK REFUSED rows=", false},
		{"pw", []string{"check", "--rows", bad}, "reason=rows\\x20line\\x201\\x20user\\x20bench:\\x20a\\x20password\\x20token", false},
		{"", []string{"check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture}, "ACL CHECK REFUSED store=127.0.0.1:9 reason=no-admin-password env=NS_ADMIN remedy=export NS_ADMIN=", false},
		{"pw", []string{"check", "--redis", "127.0.0.1:9", "--rows", aclRowsFixture}, "ACL CHECK REFUSED store=127.0.0.1:9 reason=NOPERM", true},
		{"pw", nil, "nova-sprint acl: want check", false},
		{"pw", []string{"fix"}, "nova-sprint acl: unknown subverb fix", false},
		{"pw", []string{"check", "extra"}, "takes flags, not positional arguments", false},
	} {
		list, got := cannedACL(t, nil, errors.New("NOPERM User admin has no permissions to run the 'acl|list' command"))
		code, stdout, stderr := runACLCheck(aclEnv(c.env), list, c.args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, c.want) || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d stdout %q stderr %q; want one line holding %q, exit 2", c.args, code, stdout, stderr, c.want)
		}
		if dialed := *got != ([3]string{}); dialed != c.dialed {
			t.Errorf("%v: dialled=%v, want %v", c.args, dialed, c.dialed)
		}
	}
}
