//go:build functional

package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
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
		if !strings.Contains(out+errOut, id) {
			t.Fatal("refusal omitted affected copy")
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

func TestLegacyTaskBeatExplicitlyRefusesCopyWithLegacyFields(t *testing.T) {
	t.Parallel()
	_, c := loadedStore(t)
	ctx := context.Background()
	id := "legacy-fields~1"
	for _, state := range []string{"claimed", "working"} {
		if err := c.HSet(ctx, taskcard.Key(id), "consumer", "friend:fixture", "where", "working", "state", state, "owner", "fixture", "friend", "fixture", "token", "current", "lease_until", "123").Err(); err != nil {
			t.Fatal(err)
		}
		before := c.HGetAll(ctx, taskcard.Key(id)).Val()
		_, err := c.FCall(ctx, "ns_task_beat", nil, "fixture", id, "current", "fixture", "").Result()
		if err == nil || !strings.Contains(err.Error(), "OWNER id="+id) || !strings.Contains(err.Error(), "nova-sprint friend beat --as friend:fixture --once") {
			t.Fatalf("copy accepted through legacy state=%s: %v", state, err)
		}
		older, err := c.FCall(ctx, "ns_tcard_beat", nil, id, "fixture").Text()
		if err != nil || !strings.HasPrefix(older, "REFUSED OWNER id="+id) {
			t.Fatalf("older task-card beat accepted a copy with legacy fields: %s %v", older, err)
		}
		after := c.HGetAll(ctx, taskcard.Key(id)).Val()
		if len(after) != len(before) {
			t.Fatal("refused legacy beat wrote fields")
		}
		for key, value := range before {
			if after[key] != value {
				t.Fatalf("refused legacy beat wrote %s", key)
			}
		}
	}
}

func TestOwnerStateRemediesRunForDeadUnknownAndRefused(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	me := beatSeat()
	as := taskcard.Consumer{Kind: "friend", Name: me}
	id := beatStore(t, c, me, 1, 1)[0]
	for _, state := range []string{"dead", "unknown", "refused"} {
		var out bytes.Buffer
		if state == "refused" {
			printOwnerRefusals(&out, as, addr, []life.OwnerRefusal{{ID: id, Why: "FENCED token changed"}}, nil)
		} else {
			res := life.FriendBeatResult{}
			if state == "dead" {
				res.Dead = []string{id}
			} else {
				res.Unknown = []string{id}
			}
			printOwnerStates(&out, as, addr, res, nil)
		}
		m := regexp.MustCompile(`next="([^"]+)"`).FindStringSubmatch(out.String())
		if len(m) != 2 {
			t.Fatalf("no command in %s", out.String())
		}
		argv := strings.Fields(m[1])
		code, got, errOut := runSprint(argv[1:]...)
		if code != 0 || got == "" {
			t.Fatalf("%s remedy %q failed: %d %s%s", state, m[1], code, got, errOut)
		}
	}
}

func TestOwnerRequiredCarriesTheIssuedToken(t *testing.T) {
	t.Parallel()
	for _, door := range []string{"work", "pull"} {
		t.Run(door, func(t *testing.T) {
			t.Parallel()
			addr, c := loadedStore(t)
			me := beatSeat()
			as := taskcard.Consumer{Kind: "friend", Name: me}
			id := beatStore(t, c, me, 1, 1)[0]
			args := []string{"card", "work", "--as", as.String(), "--ids", id, "--redis", addr}
			if door == "pull" {
				args = []string{"friend", "pull", "--as", as.String(), "--dir", t.TempDir(), "--redis", addr}
			}
			code, out, errOut := runSprint(args...)
			if code != 0 {
				t.Fatalf("%s failed: %d %s%s", door, code, out, errOut)
			}
			token := c.HGet(context.Background(), taskcard.Key(id), "token").Val()
			if token == "" || !strings.Contains(out, "--token "+token+" --pid <harness-pid>") || strings.Contains(out, "<claim-token>") {
				t.Fatalf("%s did not use its issued token in the binding command: %s", door, out)
			}
		})
	}
}
