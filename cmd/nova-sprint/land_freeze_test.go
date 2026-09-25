package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// land freeze and land thaw (#3139 rev 7 §7.7, §8.4, B11): usage exits 2, a
// freeze writes land:<repo>:<base>:freeze, both are idempotent, a thaw removes it.
func TestLandFreezeThaw(t *testing.T) {
	if code, _, errOut := runSprint("land", "freeze", "nova-tools"); code != 2 || !strings.Contains(errOut, "usage: land freeze <repo> <base>") {
		t.Fatalf("bare freeze: %d %q", code, errOut)
	}
	if code, _, errOut := runSprint("land", "freeze", "nova-tools", "dev", "--redis", "127.0.0.1:1"); code != 2 || !strings.Contains(errOut, "needs --reason") {
		t.Fatalf("freeze without a reason: %d %q", code, errOut)
	}
	addr := startThrowawayRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	code, out, errOut := runSprint("land", "freeze", "nova-tools", "dev", "--reason", "release window", "--by", "glenn", "--redis", addr)
	if code != 0 || out != "FROZEN nova-tools dev reason=release\\x20window\n" {
		t.Fatalf("freeze: %d %q %q", code, out, errOut)
	}
	if src := client.HGet(ctx, "land:nova-tools:dev:freeze", "source").Val(); src != "hand" {
		t.Fatalf("freeze source %q", src)
	}
	if code, out, _ := runSprint("land", "freeze", "nova-tools", "dev", "--reason", "x", "--redis", addr); code != 0 || !strings.Contains(out, "already reason=release\\x20window") {
		t.Fatalf("second freeze: %d %q", code, out)
	}
	if code, out, _ := runSprint("land", "thaw", "nova-tools", "dev", "--redis", addr); code != 0 || out != "THAW nova-tools dev was=hand\n" {
		t.Fatalf("thaw: %d %q", code, out)
	}
	if n := client.Exists(ctx, "land:nova-tools:dev:freeze").Val(); n != 0 {
		t.Fatalf("freeze key left after thaw")
	}
	if code, out, _ := runSprint("land", "thaw", "nova-tools", "dev", "--redis", addr); code != 0 || out != "THAW nova-tools dev not frozen\n" {
		t.Fatalf("second thaw: %d %q", code, out)
	}
}
