package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
)

const lsRepo = "mas-bandwidth/nova-tools"

func TestLandVerbsRefuseUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"land", "stream"},
		{"land", "stream", "--repo", "nova-tools", "--stream", "s", "--redis", "127.0.0.1:1"},
		{"land", "merge", "--repo", lsRepo},
		{"land", "status", "--repo", "x"},
		{"pr"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--ci", "blue", "--redis", "127.0.0.1:1"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--head", "xyz", "--redis", "127.0.0.1:1"},
		{"pr", "lines", "--repo", lsRepo, "--n", "1", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	// A new record without head/base/stream is refused by the script.
	mr := miniredis.RunT(t)
	if code, _, errOut := runSprint("pr", "record", "--redis", mr.Addr(), "--repo", lsRepo, "--n", "5", "--ci", "green"); code != 2 || !strings.Contains(errOut, "REFUSED no record pr:nova-tools:5") {
		t.Fatalf("new record without head: %d %s", code, errOut)
	}
}

// TestLandStreamCardMustBeTheStreamsOwn: --card takes only the stream's
// merge card (land:merge:<stream> task) or its escalation (nova-tools
// #4324); an unrelated open task is refused before any claim, so it can
// never hold the stream until it closes.
func TestLandStreamCardMustBeTheStreamsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	const s = "land duty"
	for _, id := range []string{"merge-ld-1", "cross-ld-1", "t9"} {
		mr.HSet("task:"+id, "state", "open")
	}
	mr.HSet("land:merge:"+s, "task", "merge-ld-1", "escalation", "cross-ld-1")
	mr.HSet("land:merge:other", "task", "merge-ot-1")

	for _, tc := range []struct {
		card    string
		streams []string
		why     string
	}{
		{"t9", []string{s}, "--card t9 is not stream land\\x20duty's merge or escalation card (task=merge-ld-1 escalation=cross-ld-1)"},
		{"merge-ld-1", []string{s, "other"}, "--card merge-ld-1 is not stream other's merge or escalation card (task=merge-ot-1 escalation=-)"},
		{"t9", []string{"none"}, "--card t9 is not stream none's merge or escalation card (task=- escalation=-)"},
	} {
		_, err := landHold(ctx, c, tc.streams, tc.card, "rowan")
		var ref *stream.Refusal
		if !errors.As(err, &ref) || ref.Why != tc.why {
			t.Fatalf("--card %s on %v: %v, want REFUSED %s", tc.card, tc.streams, err, tc.why)
		}
		if o := mr.HGet("land:merge:"+s, "owner"); o != "" {
			t.Fatalf("a refused --card %s left a claim: %q", tc.card, o)
		}
	}
	// The stream's own cards pass the check to the claim itself.
	for _, id := range []string{"merge-ld-1", "cross-ld-1"} {
		if err := landCardOf(ctx, c, []string{s}, id); err != nil {
			t.Fatalf("--card %s: %v", id, err)
		}
	}
}
