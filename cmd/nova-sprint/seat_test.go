//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
	"github.com/redis/go-redis/v9"
)

// seatStore is the fleet shape (#4052): a throwaway Redis with the default
// user off and one ACL user, coordinator, whose password is sealed in the
// studio seat's file of a real nova-secrets store under a temporary HOME.
// Every Redis and secrets variable is cleared, so the only route to the
// password is --seat reading that file.
func seatStore(t *testing.T, pw string) string {
	t.Helper()
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw})
	addr := testutil.Start(t, "--user", "default", "off", "--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all")
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: pw})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	seattest.Env(t, home)
	for _, k := range []string{store.PasswordEnvEnv, store.DefaultPasswordEnv, "NOVA_REDIS_COORDINATOR_PASSWORD", "REDISCLI_AUTH", "NOVA_REDIS_ADDR"} {
		t.Setenv(k, "")
	}
	return addr
}

func assertNoPassword(t *testing.T, pw string, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if strings.Contains(s, pw) {
			t.Fatal("the password was printed")
		}
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, pw) {
			t.Fatalf("the password entered this process's environment as %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
}

// TestTableRendersUnderSeatWithNoWrapper is #4052's DONE-WHEN: `nova-sprint
// table --seat studio --redis <addr>` renders with no env and no wrapper. The
// same call without --seat is refused NOAUTH, so the seat is what logged in.
func TestTableRendersUnderSeatWithNoWrapper(t *testing.T) {
	const pw = "seat-table-test-pw-4052"
	addr := seatStore(t, pw)

	var out, errOut bytes.Buffer
	if code := run([]string{"table", "--redis", addr, "--once"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "NOAUTH") {
		t.Fatalf("table with no seat: exit %d stderr %q; want a NOAUTH refusal", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"table", "--seat", "studio", "--redis", addr, "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("table --seat studio: exit %d stderr %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("table --seat studio rendered nothing")
	}
	assertNoPassword(t, pw, out.String(), errOut.String())

	t.Setenv(seatcred.SeatEnv, "studio")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"table", "--redis", addr, "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("table with NOVA_SEAT=studio: exit %d stderr %s", code, errOut.String())
	}
	t.Setenv(seatcred.SeatEnv, "")
}

// TestRedisCLIRunsOneCommandUnderTheSeat is the hand read: one redis-cli
// command under the seat's login, the password in the child's environment
// only.
func TestRedisCLIRunsOneCommandUnderTheSeat(t *testing.T) {
	const pw = "seat-cli-test-pw-4052"
	addr := seatStore(t, pw)
	if _, err := exec.LookPath("redis-cli"); err != nil {
		t.Skip("redis-cli not on PATH")
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"redis-cli", "--seat", "studio", "--redis", addr, "--", "SET", "seat:probe", "1"}, &out, &errOut); code != 0 || strings.TrimSpace(out.String()) != "OK" {
		t.Fatalf("redis-cli SET: exit %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "REDIS-CLI seat=studio user=coordinator") || !strings.Contains(errOut.String(), "exit=0") {
		t.Fatalf("receipt missing: %q", errOut.String())
	}
	assertNoPassword(t, pw, out.String(), errOut.String())

	// A stand-in redis-cli records its argv and whether REDISCLI_AUTH is the
	// password: the password is in the child's environment and never an argument.
	dir := t.TempDir()
	argvFile, authFile := filepath.Join(dir, "argv"), filepath.Join(dir, "auth")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvFile + "'\n[ \"$REDISCLI_AUTH\" = '" + pw + "' ] && echo match > '" + authFile + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "redis-cli"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	errOut.Reset()
	if code := run([]string{"redis-cli", "--seat", "studio", "--redis", addr, "--", "GET", "seat:probe"}, &out, &errOut); code != 0 {
		t.Fatalf("stand-in redis-cli: exit %d stderr %q", code, errOut.String())
	}
	argv, _ := os.ReadFile(argvFile)
	auth, _ := os.ReadFile(authFile)
	if strings.Contains(string(argv), pw) || !strings.Contains(string(argv), "--user\ncoordinator\n") || strings.TrimSpace(string(auth)) != "match" {
		t.Fatalf("child argv %q auth %q; want --user coordinator on argv and the password only as REDISCLI_AUTH", argv, auth)
	}

	for _, c := range [][]string{
		{"redis-cli", "--redis", addr, "--", "PING"},
		{"redis-cli", "--seat", "studio", "--redis", addr},
		{"redis-cli", "--seat", "studio", "--", "PING"},
	} {
		errOut.Reset()
		if code := run(c, &out, &errOut); code != 2 {
			t.Fatalf("%v: exit %d, want 2; stderr %q", c, code, errOut.String())
		}
	}
}
