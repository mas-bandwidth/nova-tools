//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

func TestCardRunCLI_Functional(t *testing.T) {
	t.Parallel()

	addr, c := wstest.Start(t)
	ctx := context.Background()

	friendName := "worker-run-cli"
	c.SAdd(ctx, "friends", friendName)

	as, err := taskcard.ParseConsumer("friend:" + friendName)
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	epoch, err := ws.Epoch(ctx, c)
	if err != nil {
		t.Fatalf("read epoch: %v", err)
	}

	copyID := "run-cli-task-1~1"
	primID := "run-cli-task-1"
	wtDir := filepath.Join(t.TempDir(), "wt")
	scriptDir := t.TempDir()
	harnessPath := filepath.Join(scriptDir, "fake-harness.sh")
	if err := os.WriteFile(harnessPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake harness: %v", err)
	}

	// Setup primary and copy records
	c.HSet(ctx, taskcard.Key(primID),
		"title", "CLI functional primary task",
		"where", "working",
	)
	c.HSet(ctx, taskcard.Key(copyID),
		"title", "CLI functional copy task",
		"where", "ready",
		"consumer", as.String(),
		"primary", primID,
	)

	readyKey := ws.ConsumerKeyAt(epoch, as.String(), "ready")
	c.ZAdd(ctx, readyKey, redis.Z{Score: 1000, Member: copyID})
	c.HSet(ctx, as.DesiredKey(), "slots", "4")

	code, out, errOut := runCLI(
		"card", "run",
		"--redis", addr,
		"--as", as.String(),
		"--id", copyID,
		"--model", "m1",
		"--harness", harnessPath,
		"--worktree", wtDir,
	)
	if code != 0 {
		t.Fatalf("card run = %d, want 0; stdout=%q stderr=%q", code, out, errOut)
	}

	match, _ := regexp.MatchString(`^CARD RUN as=`+regexp.QuoteMeta(as.String())+` id=`+regexp.QuoteMeta(copyID)+` model=m1 harness=.* pid=\d+ worktree=.* ms=\d+\n$`, out)
	if !match {
		t.Fatalf("stdout = %q, does not match expected CARD RUN pattern", out)
	}

	meta, err := c.HGetAll(ctx, "copy:"+copyID).Result()
	if err != nil {
		t.Fatalf("read copy metadata: %v", err)
	}
	if meta["model"] != "m1" {
		t.Errorf("meta[model] = %q, want m1", meta["model"])
	}
	if meta["harness"] != harnessPath {
		t.Errorf("meta[harness] = %q, want %q", meta["harness"], harnessPath)
	}
	if meta["pid"] == "" {
		t.Errorf("meta[pid] is empty")
	}
}

func TestCardWaitCLI_Functional(t *testing.T) {
	t.Parallel()

	addr, c := wstest.Start(t)
	ctx := context.Background()

	friendName := "worker-wait-cli"
	c.SAdd(ctx, "friends", friendName)

	as, err := taskcard.ParseConsumer("friend:" + friendName)
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	copyID := "wait-cli-task-1~1"

	// 1. Timeout when no copy ended
	code, out, errOut := runCLI(
		"card", "wait",
		"--redis", addr,
		"--as", as.String(),
		"--timeout", "100ms",
	)
	if code != 1 {
		t.Fatalf("card wait timeout code = %d, want 1; stdout=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "CARD WAIT REFUSED") || !strings.Contains(out, "why=timeout") {
		t.Fatalf("stdout = %q, want CARD WAIT REFUSED with why=timeout", out)
	}

	// 2. Simulate copy ending
	epoch, err := ws.Epoch(ctx, c)
	if err != nil {
		t.Fatalf("read epoch: %v", err)
	}
	okKey := ws.ConsumerKeyAt(epoch, as.String(), "ok")
	c.ZAdd(ctx, okKey, redis.Z{Score: float64(time.Now().UnixMilli()), Member: copyID})

	c.HSet(ctx, "copy:"+copyID,
		"outcome", "ok",
		"pr", "acme/repo#42",
		"result", "all functional tests pass",
	)

	_, err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: "ws:log",
		Values: map[string]interface{}{
			"id":       copyID,
			"to":       as.String() + ":ok",
			"consumer": as.String(),
			"at":       time.Now().UnixMilli(),
		},
	}).Result()
	if err != nil {
		t.Fatalf("write ws:log: %v", err)
	}

	code, out, errOut = runCLI(
		"card", "wait",
		"--redis", addr,
		"--as", as.String(),
		"--timeout", "2s",
	)
	if code != 0 {
		t.Fatalf("card wait = %d, want 0; stdout=%q stderr=%q", code, out, errOut)
	}

	wantLine := fmt.Sprintf("%s outcome=ok pr=acme/repo#42 result=all functional tests pass\n", copyID)
	if out != wantLine {
		t.Fatalf("stdout = %q, want %q", out, wantLine)
	}

	meta, err := c.HGetAll(ctx, "copy:"+copyID).Result()
	if err != nil {
		t.Fatalf("read copy metadata: %v", err)
	}
	if meta["notified"] != "1" {
		t.Errorf("meta[notified] = %q, want 1", meta["notified"])
	}
}
