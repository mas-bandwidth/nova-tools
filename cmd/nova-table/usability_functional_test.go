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
	if out := success("set", "work", "--hide", "note"); !strings.Contains(out, `hide="note" trips=1`) {
		t.Errorf("hide confirmation: %s", out)
	}
	if out := success("set", "work", "--show", "note"); !strings.Contains(out, `show="note" trips=1`) {
		t.Errorf("show confirmation: %s", out)
	}

	out := success("show", "work")
	for _, part := range []string{`row="stream: build" todo=1 done=1 note="review passed" progress=50.0%`, `row=empty todo=0 done=0 note="" progress=0.0%`, `trips=1 epoch=0 revision=7`} {
		if !strings.Contains(out, part) {
			t.Errorf("show missing %q: %s", part, out)
		}
	}
	out = success("member", "find", "work", "check-a")
	if !strings.Contains(out, `state=placed row="stream: build" col=done epoch=0 table_revision=7 trips=1`) {
		t.Errorf("find: %s", out)
	}
	success("member", "create", "work", "new")
	if out = success("member", "find", "work", "new"); !strings.Contains(out, "state=unplaced epoch=0 table_revision=8 trips=1") {
		t.Errorf("unplaced: %s", out)
	}
	if out = success("member", "find", "work", "unknown"); !strings.Contains(out, "state=missing epoch=0 table_revision=8 trips=1") {
		t.Errorf("missing: %s", out)
	}
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	if out = success("view", "show", "today"); !strings.Contains(out, `title="My work" summary=done state="" trips=1`) {
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
	if out = success("check", "work"); !strings.Contains(out, "revision=8 members=2") {
		t.Errorf("view operations changed table: %s", out)
	}
}

// TestViewStateVerb: view state sets the text the summary line shows alone,
// view show prints it, view set keeps it, --clear shows the counts again, and a
// missing view or a bad state is refused.
func TestViewStateVerb(t *testing.T) {
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
	summary := func() string {
		t.Helper()
		out := success("render", "--view", "today")
		parts := strings.SplitN(out, "\n\n", 4) // clock, title, summary line, tables
		if len(parts) < 3 {
			t.Fatalf("frame: %q", out)
		}
		return parts[2]
	}
	success("create", "work", "--columns", "todo,done")
	success("row", "add", "work", "build")
	success("cell", "add", "work", "build", "todo", "a", "b")
	success("cell", "move", "work", "build", "todo", "done", "a")
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	if got := summary(); got != "1/2 50.0% -> ETA" {
		t.Fatalf("no state: %q", got)
	}
	if out := success("view", "state", "today", "STOPPED"); out != "VIEW STATE view=today state=\"STOPPED\" trips=1\n" {
		t.Fatalf("view state: %q", out)
	}
	if got := summary(); got != "STOPPED" {
		t.Fatalf("with a state: %q, want STOPPED alone", got)
	}
	if out := success("view", "show", "today"); !strings.Contains(out, `summary=done state="STOPPED" trips=1`) {
		t.Fatalf("view show: %q", out)
	}
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	if got := summary(); got != "STOPPED" {
		t.Fatalf("view set dropped the state: %q", got)
	}
	success("view", "state", "today", "--clear")
	if got := summary(); got != "1/2 50.0% -> ETA" {
		t.Fatalf("cleared: %q", got)
	}
	for _, bad := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"view", "state", "missing", "STOPPED"}, 1, `view "missing": no such view`},
		{[]string{"view", "state", "today"}, 2, "wants a view name and its state text"},
		{[]string{"view", "state", "today", "a\nb"}, 2, "a state is one line"},
	} {
		code, out, errout := runTable(at(addr, bad.args...)...)
		if code != bad.code || out != "" || !strings.Contains(errout, bad.want) {
			t.Errorf("%v: %d %q %q", bad.args, code, out, errout)
		}
	}
}
