//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
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

// TestSeatRowDrivesVerbsWithNoWrapper is #4330's DONE-WHEN on a throwaway
// fleet-shaped Redis (default user off): `nova-sprint --seat coordinator
// table` and `nova-sprint --seat coordinator redis <cmd>` run with no user or
// password in the environment and no wrapper -- the row in seats.tsv names
// the login (and, for redis, the address), the seat's file the password. A
// row for a user the ACL restricts prints Redis's refusal.
func TestSeatRowDrivesVerbsWithNoWrapper(t *testing.T) {
	const pw, rpw = "row-coord-test-pw-4330", "row-reader-test-pw-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "NOVA_REDIS_READER_PASSWORD": rpw})
	addr := testutil.Start(t, "--user", "default", "off",
		"--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all",
		"--user", "reader", "on", ">"+rpw, "~*", "+get", "+ping")
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: pw})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	seattest.Env(t, home)
	for _, k := range append([]string{SprintSeatEnv, store.PasswordEnvEnv, store.DefaultPasswordEnv, "NOVA_REDIS_COORDINATOR_PASSWORD", "REDISCLI_AUTH"}, seatAddrEnvs...) {
		t.Setenv(k, "")
	}
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	row := func(name, user string) string {
		return name + "\t" + addr + "\t" + user + "\tNOVA_REDIS_" + strings.ToUpper(user) + "_PASSWORD\t~/nova-bench/secrets\t~/.config/nova-secrets/studio.key\n"
	}
	if err := os.MkdirAll(filepath.Join(xdg, "nova-sprint"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "nova-sprint", "seats.tsv"), []byte(row("coordinator", "coordinator")+row("reader", "reader")), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"--seat", "coordinator", "table", "--redis", addr, "--once"}, &out, &errOut); code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Fatalf("--seat coordinator table: exit %d stderr %s", code, errOut.String())
	}
	assertNoPassword(t, pw, out.String(), errOut.String())

	t.Setenv(SprintSeatEnv, "coordinator")
	for _, k := range seatAddrEnvs {
		os.Unsetenv(k)
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"redis", "SET", "seat:probe", "v1"}, &out, &errOut); code != 0 || out.String() != "OK\n" || !strings.Contains(errOut.String(), "REDIS seat=coordinator user=coordinator") {
		t.Fatalf("NOVA_SPRINT_SEAT redis SET: exit %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"redis", "--", "HMGET", "nokey", "a"}, &out, &errOut); code != 0 || out.String() != "\n" {
		t.Fatalf("redis HMGET: exit %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
	t.Setenv(SprintSeatEnv, "")

	out.Reset()
	errOut.Reset()
	if code := run([]string{"--seat", "reader", "redis", "GET", "seat:probe"}, &out, &errOut); code != 0 || out.String() != "v1\n" {
		t.Fatalf("reader GET: exit %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"--seat", "reader", "redis", "SET", "seat:probe", "v2"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "REDIS REFUSED seat=reader") || !strings.Contains(errOut.String(), "NOPERM") {
		t.Fatalf("reader SET: exit %d stderr %q; want 1 and the NOPERM refusal printed", code, errOut.String())
	}
	assertNoPassword(t, rpw, out.String(), errOut.String())

	out.Reset()
	errOut.Reset()
	if code := run([]string{"--seat", "ghost", "redis", "--redis", addr, "PING"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), filepath.Join(xdg, "nova-sprint", "seats.tsv")) {
		t.Fatalf("ghost seat: exit %d stderr %q; want 2 naming seats.tsv", code, errOut.String())
	}
	seatcred.Select("")
}
