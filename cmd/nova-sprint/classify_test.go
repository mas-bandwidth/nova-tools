package main

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/redis/go-redis/v9"
)

func classifyRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 200})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c
}

func classifyEnd(t *testing.T, c *redis.Client, sprint, label, outcome, reason, depends string, extra ...string) {
	t.Helper()
	ctx := context.Background()
	key := "s:" + sprint + ":card:" + label
	fields := []any{"label", label, "state", "ended", "attempt", "1", "bench", "X", "pin", "",
		"outcome", outcome, "reason", reason, "kind", "model", "base_sha", "01234567", "base", "dev",
		"repo", "mas-bandwidth/nova-tools", "paths", "internal/x", "depends_on", depends, "priority", "1"}
	for _, v := range extra {
		fields = append(fields, v)
	}
	if err := c.HSet(ctx, key, fields...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SAdd(ctx, "s:"+sprint+":idx:card:ended", label).Err(); err != nil {
		t.Fatal(err)
	}
	id, err := c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: map[string]any{
		"kind": "card", "id": label, "to": "ended", "attempt": "1"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, key, "end_receipt", id).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyVerbOnce(t *testing.T) {
	t.Parallel()

	addr, c := classifyRedis(t)
	const sprint = "control-verb"
	if err := c.HSet(context.Background(), "s:"+sprint, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	classifyEnd(t, c, sprint, "blocked", "BLOCKED", "spec", "")
	var out, errOut bytes.Buffer
	code := runClassify(context.Background(), []string{"--redis", addr, "--sprint", sprint, "--consumer", "test", "--once"}, &out, &errOut)
	if code != 0 || errOut.Len() != 0 || out.String() != "blocked BLOCKED/spec -> UNRESOLVED\n" {
		t.Fatalf("first classify code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	code = runClassify(context.Background(), []string{"--redis", addr, "--sprint", sprint, "--consumer", "test", "--once"}, &out, &errOut)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("second classify code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestClassifyMakesNoForgeCalls(t *testing.T) {
	addr, c := classifyRedis(t)
	const sprint = "control-no-forge"
	ctx := context.Background()
	if err := c.HSet(ctx, "s:"+sprint, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SAdd(ctx, "benches", "X", "Y").Err(); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"X", "Y"} {
		if err := c.HSet(ctx, "bench:"+b+":desired", "slots", "1", "legs", "").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.HSet(ctx, "s:"+sprint+":card:live", "state", "queued").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "s:"+sprint+":pr:mas-bandwidth/nova-tools:41", "state", "closed").Err(); err != nil {
		t.Fatal(err)
	}
	classifyEnd(t, c, sprint, "wait-card", "BLOCKED", "deps", "live")
	classifyEnd(t, c, sprint, "closed-pr", "BLOCKED", "deps", "mas-bandwidth/nova-tools#41")
	classifyEnd(t, c, sprint, "mixed", "BLOCKED", "deps", "live, missing")
	classifyEnd(t, c, sprint, "unknown-pr", "BLOCKED", "deps", "mas-bandwidth/nova-tools#60")

	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "gh.calls")
	script := filepath.Join(bin, "gh")
	if err := testbin.WriteExecutable(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$GH_CALLS\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_CALLS", calls)
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	var out, errOut bytes.Buffer
	code := runClassify(ctx, []string{"--redis", addr, "--sprint", sprint, "--consumer", "test", "--once"}, &out, &errOut)
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("classify code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	for _, want := range []string{
		"wait-card BLOCKED/deps -> WAIT",
		"closed-pr BLOCKED/deps -> UNRESOLVED",
		"mixed BLOCKED/deps -> UNRESOLVED",
		"unknown-pr BLOCKED/deps -> UNRESOLVED",
	} {
		if !strings.Contains(out.String(), want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("fake gh was invoked: %v", err)
	}

	for _, path := range []string{"classify.go", filepath.Join("..", "..", "internal", "nsprint", "consume", "classify.go")} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"net/http"` || imp.Path.Value == `"os/exec"` {
				t.Errorf("%s imports forbidden forge-capable package %s", path, imp.Path.Value)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "deal" {
					switch x.Sel.Name {
					case "GH", "PRs", "Ready", "RedisSource":
						t.Errorf("%s references deal.%s", path, x.Sel.Name)
					}
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && strings.Trim(x.Value, "`\"") == "gh" {
					t.Errorf("%s contains gh command literal", path)
				}
			}
			return true
		})
	}
}

func Example() {
	fmt.Println("card BLOCKED/spec -> UNRESOLVED")
	// Output: card BLOCKED/spec -> UNRESOLVED
}
