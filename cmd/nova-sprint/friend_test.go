package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// TestFriendVerbs drives #3101's verbs through the CLI against a throwaway
// Redis: the declared wake path, a keeper's out-of-credits report, friend
// show and one sweep, plus the refusals.
func TestFriendVerbs(t *testing.T) {
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"emma", "johnny"} {
		if err := client.SAdd(ctx, "friends", name).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.HSet(ctx, "friend:"+name+":desired", "slots", "8", "machine", "studio").Err(); err != nil {
			t.Fatal(err)
		}
	}
	runOK := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
		return out.String()
	}
	refused := func(args ...string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (%s)", args, code, out.String())
		}
	}

	if got := runOK("capacity", "friend", "--redis", addr, "--wake", "human", "--notify", "bus:To:Glenn", "--as", "config", "emma"); got != "SET friend emma wake=human:bus:To:Glenn\n" {
		t.Fatalf("wake human: %q", got)
	}
	if got := runOK("capacity", "friend", "--redis", addr, "--wake", "unit:com.nova.loop.wake-serve-johnny@studio", "--as", "config", "johnny"); !strings.Contains(got, "wake=unit:com.nova.loop.wake-serve-johnny@studio") {
		t.Fatalf("wake unit: %q", got)
	}
	refused("capacity", "friend", "--redis", addr, "--wake", "human", "--as", "config", "emma")
	refused("capacity", "friend", "--redis", addr, "--wake", "unit:no-host", "--as", "config", "emma")

	if got := runOK("friend", "report", "--redis", addr, "--as", "emma", "--out-of-credits", "--until", "3h"); !strings.HasPrefix(got, "REPORT friend=emma state=out-of-credits until=") {
		t.Fatalf("report: %q", got)
	}
	refused("friend", "report", "--redis", addr, "--as", "emma", "--away")
	refused("friend", "report", "--redis", addr, "--as", "emma", "--away", "--clear", "--until", "1h")
	refused("friend", "report", "--redis", addr, "--as", "nobody", "--out-of-credits")

	show := runOK("friend", "show", "--redis", addr, "emma")
	for _, want := range []string{"friend=emma", "state=out-of-credits", "wake=human:bus:To:Glenn"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show %q lacks %q", show, want)
		}
	}
	if got := runOK("friend", "sweep", "--redis", addr, "--as", "reconciler", "--builders", "johnny", "--coordinator", "rowan"); !strings.Contains(got, "SWEEP idem=sweep-") {
		t.Fatalf("sweep: %q", got)
	}
	if got := runOK("friend", "report", "--redis", addr, "--as", "emma", "--clear"); got != "REPORT friend=emma state=up\n" {
		t.Fatalf("clear: %q", got)
	}
	if n := client.Exists(ctx, "friend:emma:state").Val(); n != 0 {
		t.Fatal("clear left friend:emma:state")
	}
}
