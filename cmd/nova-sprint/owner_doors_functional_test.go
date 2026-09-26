//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func TestFriendCopyBeatDoorsNameObservedOwnerCommand(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	me := beatSeat()
	as := taskcard.Consumer{Kind: "friend", Name: me}
	id := beatStore(t, c, me, 1, 1)[0]
	if _, err := taskcard.Work(ctx, c, as, me, 1, false, id); err != nil {
		t.Fatal(err)
	}
	bindTestOwner(t, c, as, id)
	before := c.HGet(ctx, taskcard.Key(id), "lease_until").Val()
	for _, args := range [][]string{
		{"card", "beat", "--as", as.String(), "--id", id, "--redis", addr},
		{"task", "beat", "--actor", me, "--id", id, "--redis", addr},
	} {
		code, out, errOut := runSprint(args...)
		if code == 0 || !strings.Contains(out+errOut, "nova-sprint friend beat --as "+as.String()+" --once") {
			t.Fatalf("plain beat should refuse with valid next command: %v: exit=%d %s%s", args[:2], code, out, errOut)
		}
		if got := c.HGet(ctx, taskcard.Key(id), "lease_until").Val(); got != before {
			t.Fatal("refused beat changed lease")
		}
	}
	code, out, errOut := runSprint("friend", "beat", "--as", as.String(), "--once", "--redis", addr)
	if code != 0 || !strings.Contains(out, "working=1") {
		t.Fatalf("suggested command failed: %d %s%s", code, out, errOut)
	}
}
