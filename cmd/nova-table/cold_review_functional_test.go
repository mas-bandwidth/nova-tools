//go:build functional

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/redis/go-redis/v9"
)

func TestColumnRemovalNamesAllBlockersAndRunnableBatches(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	for _, args := range [][]string{{"create", "t", "--columns", "active,done"}, {"row", "add", "t", "first row", "second"}, {"cell", "add", "t", "first row", "active", "m1", "m2"}} {
		if code, out, errout := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, errout)
		}
	}
	if code, out, errout := runTable("cell", "add", "t", "second", "active", "--redis", addr, "--", "--third"); code != 0 {
		t.Fatalf("flag-like member: %d %s %s", code, out, errout)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	before, err := ntable.Read(ctx, c, "t")
	if err != nil {
		t.Fatal(err)
	}
	code, out, errout := runTable(at(addr, "col", "del", "t", "active")...)
	if code != 1 || out != "" {
		t.Fatalf("occupied delete: %d %s %s", code, out, errout)
	}
	for _, want := range []string{`row "first row"`, `row "second"`, `"m1"`, `"m2"`, `"--third"`, `cell remove 't' 'first row' 'active' 'm1' 'm2'`, `cell remove -- 't' 'second' 'active' '--third'`} {
		if !strings.Contains(errout, want) {
			t.Errorf("missing blocker/remedy %q in %s", want, errout)
		}
	}
	after, err := ntable.Read(ctx, c, "t")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused deletion changed snapshot: %v", err)
	}
	_, remedies, ok := strings.Cut(strings.TrimSpace(errout), "; run: ")
	if !ok {
		t.Fatal(errout)
	}
	for _, line := range strings.Split(remedies, "; ") {
		words, err := onboarding.SplitShell(line)
		if err != nil {
			t.Fatal(err)
		}
		// The advertised command, with this fixture's address supplied before
		// any -- separator. Both remedies remove a whole cell's blockers.
		args := append([]string{"cell", "remove", "--redis", addr}, words[3:]...)
		if code, out, errout := runTable(args...); code != 0 {
			t.Fatalf("remedy %q: %d %s %s", line, code, out, errout)
		}
	}
	if code, out, errout := runTable(at(addr, "col", "del", "t", "active")...); code != 0 {
		t.Fatalf("cleared column: %d %s %s", code, out, errout)
	}
}

func TestRenderStoredViewIsDiscoverableOneFrame(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	for _, args := range [][]string{{"create", "t", "--columns", "ready,done"}, {"row", "add", "t", "r"}, {"cell", "add", "t", "r", "done", "m"}, {"view", "set", "v", "--tables", "t", "--title", "Working view", "--summary", "done"}} {
		if code, out, errout := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, errout)
		}
	}
	code, render, errout := runTable(at(addr, "render", "--view", "v", "--label-width", "10")...)
	if code != 0 || errout != "" || !strings.Contains(render, "Working view\n\n1/1 100.0% -> ETA") || strings.Contains(render, clearScreen) {
		t.Fatalf("render: %d %s %s", code, render, errout)
	}
	code, watch, errout := runTable(at(addr, "watch", "--view", "v", "--once", "--label-width", "10")...)
	_, renderBody, _ := strings.Cut(render, "\n")
	_, watchBody, _ := strings.Cut(watch, "\n")
	if code != 0 || errout != "" || renderBody != watchBody {
		t.Fatalf("same view frame:\n%s\n%s\n%s", render, watch, errout)
	}
	for _, args := range [][]string{{"render", "t", "--view", "v"}, {"render", "--view", "v", "--at-epoch", "0"}} {
		if code, out, _ := runTable(at(addr, args...)...); code != 2 || out != "" {
			t.Fatalf("invalid target combination: %v %d %s", args, code, out)
		}
	}
	if code, out, errout := runTable(at(addr, "render", "--view", "missing")...); code != 1 || out != "" || !strings.Contains(errout, "view set") {
		t.Fatalf("missing view: %d %s %s", code, out, errout)
	}
}
