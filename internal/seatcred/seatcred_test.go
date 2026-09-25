package seatcred_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

func TestFromArgsTakesTheSeatFlagOrTheEnvironment(t *testing.T) {
	t.Parallel()

	t.Cleanup(func() { seatcred.Select("") })
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == seatcred.SeatEnv {
				return v
			}
			return ""
		}
	}
	for _, c := range []struct {
		args     []string
		env      string
		seat     string
		rest     []string
		refusing bool
	}{
		{args: []string{"table", "--seat", "studio", "--redis", "h:1"}, seat: "studio", rest: []string{"table", "--redis", "h:1"}},
		{args: []string{"table", "--seat=studio"}, env: "other", seat: "studio", rest: []string{"table"}},
		{args: []string{"table", "-seat", "air"}, seat: "air", rest: []string{"table"}},
		{args: []string{"table"}, env: "studio", seat: "studio", rest: []string{"table"}},
		{args: []string{"table"}, seat: "", rest: []string{"table"}},
		{args: []string{"refresh", "--", "x", "--seat", "s"}, seat: "", rest: []string{"refresh", "--", "x", "--seat", "s"}},
		{args: []string{"table", "--seat"}, refusing: true},
		{args: []string{"table", "--seat", "--redis", "h:1"}, refusing: true},
		{args: []string{"table", "--seat="}, refusing: true},
	} {
		rest, err := seatcred.FromArgs(c.args, env(c.env))
		if c.refusing {
			if err == nil || !strings.Contains(err.Error(), "--seat wants a seat name") {
				t.Fatalf("%v: err %v, want a refusal naming --seat", c.args, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(rest, c.rest) || seatcred.Selected() != c.seat {
			t.Fatalf("%v env=%q: rest %v seat %q err %v; want %v %q", c.args, c.env, rest, seatcred.Selected(), err, c.rest, c.seat)
		}
	}
}

func TestPasswordKeyNamesTheUsersVariable(t *testing.T) {
	t.Parallel()

	if got := seatcred.PasswordKey("coordinator"); got != "NOVA_REDIS_COORDINATOR_PASSWORD" {
		t.Fatalf("PasswordKey = %s", got)
	}
}

// TestResolveReadsTheSeatThroughTheSecretsLibrary is the resolution half of
// #4052: a real store, sealed by sops to a fresh age key, read from the
// default layout under HOME with nothing in the environment but HOME and PATH.
func TestResolveReadsTheSeatThroughTheSecretsLibrary(t *testing.T) {
	const coord, bench = "coord-test-pw-0f3a9c", "bench-test-pw-7d21e4"
	home := seattest.Home(t, "studio", map[string]string{
		"NOVA_REDIS_COORDINATOR_PASSWORD": coord,
		"NOVA_REDIS_BENCH_PASSWORD":       bench,
		"GH_TOKEN":                        "gh-test-value-not-redis",
	})
	seattest.Env(t, home)

	c, err := seatcred.Resolve("studio", os.Getenv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Seat != "studio" || c.User != "coordinator" || c.Key != "NOVA_REDIS_COORDINATOR_PASSWORD" || !same(c, coord) {
		t.Fatalf("Resolve = %v; want studio as coordinator with the coordinator password", c)
	}
	for _, s := range []string{c.String(), fmt.Sprintf("%v %+v %#v %s %q %x", c, c, c, c, c, c)} {
		if strings.Contains(s, coord) || strings.Contains(s, coord[:8]) {
			t.Fatalf("a formatted Cred carries the password: %s", s)
		}
	}

	t.Setenv(seatcred.UserEnv, "bench")
	if c, err = seatcred.Resolve("studio", os.Getenv); err != nil || c.User != "bench" || !same(c, bench) {
		t.Fatalf("Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	}
	t.Setenv(seatcred.UserEnv, "ghost")
	_, err = seatcred.Resolve("studio", os.Getenv)
	if err == nil || !strings.Contains(err.Error(), "NOVA_REDIS_GHOST_PASSWORD") || !strings.Contains(err.Error(), "nova-secrets seal") {
		t.Fatalf("Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	}
	t.Setenv(seatcred.UserEnv, "")

	if _, err := seatcred.Resolve("nobody", os.Getenv); err == nil || !strings.Contains(err.Error(), "seat nobody") {
		t.Fatalf("Resolve of an absent seat = %v; want a refusal naming it", err)
	}
	if _, err := seatcred.Resolve("../x", os.Getenv); err == nil {
		t.Fatal("Resolve accepted a seat name with a path in it")
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, coord) || strings.Contains(kv, bench) {
			t.Fatalf("a password entered this process's environment: %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
}

func TestActiveResolvesOnceAndOnlyWhenSelected(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "air", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": "air-bench-test-pw-11"})
	seattest.Env(t, home)
	if _, ok, _ := seatcred.Active(); ok {
		t.Fatal("Active with no seat selected reported one")
	}
	seatcred.Select("air")
	c, ok, err := seatcred.Active()
	if !ok || err != nil || c.User != "bench" || !same(c, "air-bench-test-pw-11") {
		t.Fatalf("Active = %v %v %v; want air as bench", c, ok, err)
	}
}

func TestChildEnvHandsThePasswordToTheChildOnly(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": "child-test-pw-5e"})
	seattest.Env(t, home)
	c, err := seatcred.Resolve("studio", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	env := seatcred.ChildEnv([]string{"PATH=/bin", "REDISCLI_AUTH=stale", "NOVA_REDIS_BENCH_PASSWORD=other", "HOME=/h"}, c)
	want := []string{"PATH=/bin", "HOME=/h", "REDISCLI_AUTH=child-test-pw-5e"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ChildEnv = %d entries; want PATH, HOME and one REDISCLI_AUTH", len(env))
	}
}

func same(c seatcred.Cred, want string) bool {
	eq := false
	_ = c.Password.Use(func(v string) error { eq = v == want; return nil })
	return eq
}
