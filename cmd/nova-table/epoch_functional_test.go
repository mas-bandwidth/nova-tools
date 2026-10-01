//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestCLIObservedEpochHistoryAndReceipt(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		if code != 0 || errout != "" {
			t.Fatalf("%v: exit=%d out=%q err=%q", args, code, out, errout)
		}
		return out
	}
	success("create", "epoch-cli", "--columns", "row:text:none,ready", "--epoch-key", "domain:epoch")
	success("row", "add", "epoch-cli", "build")
	success("cell", "add", "epoch-cli", "build", "ready", "old", "--score", "7")
	if err := c.HSet(ctx, "domain:epoch", "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	events := c.XLen(ctx, "table:epoch-cli:changes").Val()
	code, _, errout := runTable(at(addr, "clear", "epoch-cli")...)
	if code != 1 || !strings.Contains(errout, "requested epoch is stale") {
		t.Fatalf("stale CLI = %d %s", code, errout)
	}
	if got := c.XLen(ctx, "table:epoch-cli:changes").Val(); got != events {
		t.Fatal("stale CLI emitted a receipt")
	}
	out := success("row", "add", "epoch-cli", "build", "--epoch", "1", "--actor", "Stella", "--receipt")
	if !strings.Contains(out, "TABLE RECEIPT event=") || !strings.Contains(out, "epoch=1") {
		t.Fatalf("missing committed receipt: %s", out)
	}
	success("member", "create", "epoch-cli", "new", "--epoch", "1")
	success("cell", "add", "epoch-cli", "build", "ready", "new", "--score", "9", "--epoch", "1")
	history := success("show", "epoch-cli", "--at-epoch", "0")
	if !strings.Contains(history, "epoch=0 revision=3") || !strings.Contains(history, "ready=1") {
		t.Fatalf("historical snapshot: %s", history)
	}
	if out := success("check", "epoch-cli"); !strings.Contains(out, "epoch=1") || !strings.Contains(out, "members=1") {
		t.Fatalf("current check: %s", out)
	}
	success("drop", "epoch-cli", "--epoch", "1", "--definition")
	// the epoch's definition stays readable; its rows go with the table
	if out := success("render", "epoch-cli", "--at-epoch", "0"); !strings.Contains(out, "ready") || strings.Contains(out, "build") {
		t.Fatalf("template deletion: the definition of epoch 0 or its rows: %s", out)
	}
}
