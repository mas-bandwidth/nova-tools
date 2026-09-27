//go:build functional

package main

import (
	"strings"
	"testing"
)

func TestWorkingStateAndViewLifecycle(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		if code != 0 || errout != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errout)
		}
		return out
	}
	success("create", "work", "--columns", "todo,done,note:text,progress:pct(done)", "--footer", "total")
	success("row", "add", "work", "stream: build", "empty")
	success("cell", "add", "work", "stream: build", "todo", "check-a", "check-b")
	success("cell", "move", "work", "stream: build", "todo", "done", "check-a")
	success("row", "set", "work", "stream: build", "note=review passed")
	out := success("show", "work")
	for _, part := range []string{`row="stream: build" todo=1 done=1 note="review passed" progress=50.0%`, `row=empty todo=0 done=0 note="" progress=0.0%`, `trips=1 epoch=0 revision=5`} {
		if !strings.Contains(out, part) {
			t.Errorf("show missing %q: %s", part, out)
		}
	}
	out = success("member", "find", "work", "check-a")
	if !strings.Contains(out, `state=placed row="stream: build" col=done epoch=0 revision=5 trips=1`) {
		t.Errorf("find: %s", out)
	}
	success("member", "create", "work", "new")
	if out = success("member", "find", "work", "new"); !strings.Contains(out, "state=unplaced epoch=0 revision=6 trips=1") {
		t.Errorf("unplaced: %s", out)
	}
	if out = success("member", "find", "work", "unknown"); !strings.Contains(out, "state=missing epoch=0 revision=6 trips=1") {
		t.Errorf("missing: %s", out)
	}
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	if out = success("view", "show", "today"); !strings.Contains(out, `title="My work" summary=done trips=1`) {
		t.Errorf("view show: %s", out)
	}
	if out = success("view", "list"); out != "VIEW LIST views=1 trips=1\nVIEW view=today\n" {
		t.Errorf("list: %s", out)
	}
	success("watch", "--view", "today", "--once")
	for _, bad := range []struct {
		args []string
		want string
	}{
		{[]string{"view", "set", "bad", "--tables", "work", "--summary", "progress"}, `view "bad": summary wants a count column in table "work"; "progress" is not one; run: nova-table show 'work'`},
		{[]string{"watch", "--view", "missing", "--once"}, `view "missing": no such view; run: nova-table view set 'missing' --tables <a,b,...>`},
		{[]string{"view", "set", "bad", "--tables", "gone"}, `view "bad": referenced table "gone" does not exist; run: nova-table create 'gone' --columns <columns>`},
	} {
		code, out, errout := runTable(at(addr, bad.args...)...)
		if code != 1 || out != "" || !strings.Contains(errout, bad.want) || strings.Count(errout, "; run:") != 1 {
			t.Errorf("%v: %d %q %q", bad.args, code, out, errout)
		}
	}
	if code, _, errout := runTable(at(addr, "watch", "work", "--view", "today", "--once")...); code != 2 || !strings.Contains(errout, "or --view") {
		t.Fatalf("mixed watch targets: %d %s", code, errout)
	}

	if out = success("view", "del", "today"); out != "VIEW DEL view=today existed=1 trips=1\n" {
		t.Errorf("del: %s", out)
	}
	if out = success("view", "del", "today"); out != "VIEW DEL view=today existed=0 trips=1\n" {
		t.Errorf("repeat del: %s", out)
	}
	if out = success("view", "list"); out != "VIEW LIST views=0 trips=1\n" {
		t.Errorf("empty list: %s", out)
	}
	if out = success("check", "work"); !strings.Contains(out, "revision=6 members=2") {
		t.Errorf("view operations changed table: %s", out)
	}
}
