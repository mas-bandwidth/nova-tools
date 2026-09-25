package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

func rolesFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := startThrowawayRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(context.Background(), "friends", "a", "b", "c").Err(); err != nil {
		t.Fatal(err)
	}
	return addr, client
}

func runRoles(t *testing.T, seat, addr string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("NOVA_FRIEND", seat)
	var out, errOut bytes.Buffer
	base := []string{"friend", "roles", "--redis", addr}
	code := run(append(base, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func bootstrapCoordinator(t *testing.T, addr string) {
	t.Helper()
	if code, _, errOut := runRoles(t, "a", addr, "--set", "a", "--roles", "coordinator"); code != 0 {
		t.Fatalf("bootstrap: exit %d %s", code, errOut)
	}
}

func TestRolesNonCoordinatorRefused(t *testing.T) {
	addr, client := rolesFixture(t)
	bootstrapCoordinator(t, addr)
	before := client.XLen(context.Background(), "cap:log").Val()
	code, _, errOut := runRoles(t, "b", addr, "--set", "b", "--roles", "coordinator")
	if code != 1 || !strings.Contains(errOut, "REFUSED roles") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if client.Exists(context.Background(), "friend:b:roles").Val() != 0 || client.XLen(context.Background(), "cap:log").Val() != before {
		t.Fatal("refused role write changed Redis")
	}
}

func TestRolesBootstrapOnlyWhenNoCoordinator(t *testing.T) {
	addr, _ := rolesFixture(t)
	code, out, _ := runRoles(t, "a", addr, "--set", "a", "--roles", "coordinator")
	if code != 0 || !strings.Contains(out, "bootstrap=1") {
		t.Fatalf("bootstrap exit %d out %q", code, out)
	}
	code, _, errOut := runRoles(t, "b", addr, "--set", "b", "--roles", "may-hold")
	if code != 1 || !strings.Contains(errOut, "REFUSED roles") {
		t.Fatalf("second exit %d stderr %q", code, errOut)
	}
}

func TestRolesLastCoordinatorKept(t *testing.T) {
	addr, client := rolesFixture(t)
	bootstrapCoordinator(t, addr)
	code, _, errOut := runRoles(t, "a", addr, "--set", "a", "--roles", "")
	if code != 1 || !strings.Contains(errOut, "LASTCOORD") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if got := client.HGet(context.Background(), "friend:a:roles", "roles").Val(); got != "coordinator" {
		t.Fatalf("roles %q", got)
	}
}

func TestRolesRequiresExplicitRolesFlag(t *testing.T) {
	addr, client := rolesFixture(t)
	bootstrapCoordinator(t, addr)
	before := client.Dump(context.Background(), "friend:a:roles").Val()
	code, _, errOut := runRoles(t, "a", addr, "--set", "a")
	if code != 2 || !strings.Contains(errOut, "needs --set <friend> --roles <csv>") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if got := client.Dump(context.Background(), "friend:a:roles").Val(); got != before {
		t.Fatal("omitted --roles changed the coordinator roles")
	}
}

func TestRolesRefusalNamesBadValue(t *testing.T) {
	addr, _ := rolesFixture(t)
	bootstrapCoordinator(t, addr)
	code, _, errOut := runRoles(t, "a", addr, "--set", "missing", "--roles", "may-hold")
	if code != 1 || !strings.Contains(errOut, "UNKNOWN missing") {
		t.Fatalf("unknown exit %d stderr %q", code, errOut)
	}
	code, _, errOut = runRoles(t, "a", addr, "--set", "b", "--roles", "reader")
	if code != 1 || !strings.Contains(errOut, "BADROLE reader") {
		t.Fatalf("bad role exit %d stderr %q", code, errOut)
	}
}

func TestRolesSetByCoordinatorRoutesNextCall(t *testing.T) {
	addr, client := rolesFixture(t)
	bootstrapCoordinator(t, addr)
	if code, _, errOut := runRoles(t, "a", addr, "--set", "b", "--roles", "may-hold"); code != 0 {
		t.Fatal(errOut)
	}
	ctx := context.Background()
	for _, f := range []string{"b", "c"} {
		if err := client.HSet(ctx, "friend:"+f+":desired", "slots", "4", "paused", "0").Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.HSet(ctx, "friend:"+f+":beat", "at", "1").Err(); err != nil {
			t.Fatal(err)
		}
	}
	seed(t, addr, [][]string{
		{"SADD", "sprints", "s"}, {"HSET", "s:s", "status", "open"},
		{"HSET", "task:r", "state", "open", "kind", "read", "priority", "1", "repo", "r", "pr", "1", "head", strings.Repeat("1", 40), "author", "a"},
		{"ZADD", "s:s:open:c", "1", "r"}, {"SADD", "s:s:idx:task:open", "r"},
	})
	t.Setenv("NOVA_FRIEND", "a")
	var out, errOut bytes.Buffer
	code := run([]string{"redistribute", "--redis", addr, "--from", "c", "--reason", "test"}, &out, &errOut)
	if code != 0 || client.ZScore(ctx, "s:s:open:b", "r").Err() != nil {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errOut.String())
	}
}

func TestHelloDoesNotWriteFriendRoles(t *testing.T) {
	addr, client := rolesFixture(t)
	ctx := context.Background()
	seed(t, addr, [][]string{
		{"HSET", "friend:b:roles", "roles", "may-hold", "at", "1", "by", "a"},
		{"HSET", "friend:b:desired", "slots", "1", "machine", "m", "paused", "0"},
		{"HSET", "friend:b:beat", "session", "s", "host", "h", "at", "1"},
		{"HSET", "machine:m:ceiling", "slots", "4"},
	})
	before := client.Dump(ctx, "friend:b:roles").Val()
	t.Setenv("NOVA_FRIEND", "b")
	var out, errOut bytes.Buffer
	code := run([]string{"friend", "hello", "--redis", addr, "--as", "b", "--host", "h", "--machine", "m", "--session", "s", "--once"}, &out, &errOut)
	if code != 0 || client.Dump(ctx, "friend:b:roles").Val() != before {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errOut.String())
	}
	code = run([]string{"friend", "hello", "--redis", addr, "--as", "b", "--roles", "coordinator", "--once"}, &out, &errOut)
	if code != 2 || client.Dump(ctx, "friend:b:roles").Val() != before {
		t.Fatalf("roles flag exit %d", code)
	}
}
