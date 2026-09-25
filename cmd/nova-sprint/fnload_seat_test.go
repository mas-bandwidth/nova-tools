package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestFnCheckAsBenchNamesTheSeat is #3562's DONE-WHEN for fn check: the
// bench seat is refused FUNCTION LIST by design (#3320), so fn check and fn
// load run as the bench seat end in a refusal that says which seat they need
// and how to run as it, never a bare NOPERM. The seat's rules are the fleet
// bench user's command grants (rowan-tools fleet/redis.yml) over every key.
func TestFnCheckAsBenchNamesTheSeat(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	seatAs(t, c, "bench", "~*", "&*", "+@read", "+@write", "+@stream", "+@hash", "+@string", "+multi", "+exec",
		"+@pubsub", "+@connection", "+fcall", "+fcall_ro", "+time", "-@dangerous")
	for _, sub := range []string{"check", "load"} {
		code, out, errOut := runSprint("fn", sub, "--redis", addr)
		if code != 2 || out != "" {
			t.Fatalf("fn %s as bench: code=%d out=%q err=%q, want 2 and a refusal", sub, code, out, errOut)
		}
		for _, want := range []string{"NOPERM", "fn " + sub + " reads FUNCTION LIST", "seat bench", "the coordinator seat",
			"NOVA_SPRINT_REDIS_USER=coordinator", "nova-secrets exec --only"} {
			if !strings.Contains(errOut, want) {
				t.Fatalf("fn %s as bench: refusal %q does not say %q", sub, errOut, want)
			}
		}
	}
	// The coordinator's grant (+@all -@dangerous) holds FUNCTION LIST: fn check answers.
	if err := c.Do(context.Background(), "ACL", "SETUSER", "coordinator", "reset", "on", ">seat-pw", "~*", "&*", "+@all", "-@dangerous").Err(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_SPRINT_REDIS_USER", "coordinator")
	if code, out, errOut := runSprint("fn", "check", "--redis", addr); code != 1 || !strings.HasPrefix(out, "MISSING nova_sprint ") {
		t.Fatalf("fn check as coordinator: code=%d out=%q err=%q, want 1 MISSING", code, out, errOut)
	}
}
