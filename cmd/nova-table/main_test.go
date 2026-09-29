package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// runTable invokes the binary's own entry point.
func runTable(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// at appends --redis <addr> to a command line.
func at(addr string, args ...string) []string { return append(args, "--redis", addr) }

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runTable()
	if code != 2 || stdout != "" || stderr != "nova-table: no verb; available: help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help\n" {
		t.Fatalf("bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = runTable("bogus")
	if code != 2 || !strings.HasPrefix(stderr, "nova-table: unknown verb bogus;") || !strings.HasSuffix(stderr, "; run: nova-table help\n") {
		t.Fatalf("unknown verb: exit %d stderr %q", code, stderr)
	}
	code, stdout, _ = runTable("help")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table: ") || !strings.Contains(stdout, "\nexample:\n") {
		t.Fatalf("help: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = runTable("version")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table ") {
		t.Fatalf("version: exit %d %q", code, stdout)
	}
}

// TestLoginIsTheStoresLogin: with no seat, nova-table dials as nova-sprint's
// store did: no user is the default user with no password; a user with no
// password variable named reads NOVA_REDIS_BENCH_PASSWORD, named here since
// redisconn reads no default; a seat logs in as its user with the password
// its file holds, answered from memory under the seat's key.
func TestLoginIsTheStoresLogin(t *testing.T) {
	t.Parallel()
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	o, _, err := login("127.0.0.1:1", &seatcred.Selection{}, getenv)
	if err != nil || o.User != "" || o.PasswordEnv != "" || o.Env.User != "NOVA_SPRINT_REDIS_USER" || o.Env.PasswordEnv != "" {
		t.Fatalf("no user: %v %v", o, err)
	}
	// No user is no password, whatever the password's variable holds.
	env["NOVA_SPRINT_REDIS_PASSWORD_ENV"] = "OTHER_PASSWORD"
	if r, err := redisconn.Resolve(o, getenv); err != nil || r.PasswordEnv != "" {
		t.Fatalf("no user, a password variable named: %v %v", r, err)
	}
	delete(env, "NOVA_SPRINT_REDIS_PASSWORD_ENV")
	env["NOVA_SPRINT_REDIS_USER"] = "bench"
	if o, _, err = login("127.0.0.1:1", &seatcred.Selection{}, getenv); err != nil || o.PasswordEnv != "NOVA_REDIS_BENCH_PASSWORD" || o.Env.PasswordEnv != "NOVA_SPRINT_REDIS_PASSWORD_ENV" {
		t.Fatalf("default password variable: %v %v", o, err)
	}
	env["NOVA_SPRINT_REDIS_PASSWORD_ENV"] = "OTHER_PASSWORD"
	if o, _, err = login("127.0.0.1:1", &seatcred.Selection{}, getenv); err != nil || o.PasswordEnv != "" {
		t.Fatalf("named password variable: %v %v", o, err)
	}
	if r, err := redisconn.Resolve(o, getenv); err == nil || !strings.Contains(err.Error(), "OTHER_PASSWORD is empty") {
		t.Fatalf("named and empty: %v %v", r, err)
	}

	var sel seatcred.Selection
	sel.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: "synthetic", User: "coordinator", Key: "NOVA_REDIS_COORDINATOR_PASSWORD", Password: secrets.NewSecret("synthetic-seat-pw")}, nil
	})
	o, seatenv, err := login("127.0.0.1:1", &sel, getenv)
	if err != nil || o.User != "coordinator" || o.PasswordEnv != "NOVA_REDIS_COORDINATOR_PASSWORD" || seatenv("NOVA_REDIS_COORDINATOR_PASSWORD") != "synthetic-seat-pw" || seatenv("NOVA_SPRINT_REDIS_USER") != "bench" {
		t.Fatalf("seat: %v %v", o, err)
	}
	if strings.Contains(o.String(), "synthetic-seat-pw") {
		t.Fatalf("seat options show the password: %s", o)
	}

	var broken seatcred.Selection
	broken.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) { return seatcred.Cred{}, errors.New("seat synthetic: no file") })
	if _, _, err := login("127.0.0.1:1", &broken, getenv); err == nil || err.Error() != "seat synthetic: no file" {
		t.Fatalf("seat refusal: %v", err)
	}
}

// TestSeatWordsNameTheSeat: a seat's password is named as the seat's file's
// key, never as a variable of the environment; without a seat the words are
// redisconn's.
func TestSeatWordsNameTheSeat(t *testing.T) {
	t.Parallel()
	line := "redis at h:1 as user coordinator (password from NOVA_REDIS_COORDINATOR_PASSWORD): login refused: WRONGPASS; next: check that NOVA_REDIS_COORDINATOR_PASSWORD holds the password of coordinator and that the store has that user switched on"
	if got := seatWords(&seatcred.Selection{}, line); got != line {
		t.Fatalf("no seat: %q", got)
	}
	var sel seatcred.Selection
	sel.SelectWith("synthetic", "", func(string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: "synthetic", User: "coordinator", Key: "NOVA_REDIS_COORDINATOR_PASSWORD", Password: secrets.NewSecret("synthetic-seat-pw")}, nil
	})
	want := "redis at h:1 as user coordinator (password from seat synthetic, key NOVA_REDIS_COORDINATOR_PASSWORD of its file): login refused: WRONGPASS; next: check that seat synthetic's file holds under NOVA_REDIS_COORDINATOR_PASSWORD the password of coordinator and that the store has that user switched on"
	if got := seatWords(&sel, line); got != want {
		t.Fatalf("seat:\n got %q\nwant %q", got, want)
	}
	err := &reworded{want, errors.New("cause")}
	if !errors.Is(err, err.err) || err.Error() != want {
		t.Fatalf("reworded: %v", err)
	}
}
