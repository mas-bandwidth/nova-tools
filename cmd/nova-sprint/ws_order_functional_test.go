//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// TestWSOrderVerbs is card land-order-1's functional DONE-WHEN (#4322,
// #4324) on a throwaway store: five `task push`es onto a stream each write
// the order (order=<n> order_rt=3 in the receipt), `ws show --order` prints
// the computed order with one reason per edge, `ws check` finds no drift; a
// hand ZADD is ORDER DRIFT with both sequences and exit 1, and `ws reorder`
// writes the order back.
func TestWSOrderVerbs(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	const stream = "land: order"
	for _, p := range []struct{ id, ref, paths, on string }{
		{"a", "#50", "internal/a", ""},
		{"b", "#20", "internal/b", "a"},
		{"c", "#30", "internal/c", "a"},
		{"d", "#40", "internal/x", "b,c"},
		{"e", "#45", "internal/x/e.go", ""},
	} {
		args := []string{"task", "push", "--redis", addr, "--actor", "test", "--id", p.id, "--stream", stream,
			"--kind", "build", "--title", p.id, "--ref", "mas-bandwidth/nova-tools" + p.ref, "--paths", p.paths}
		if p.on != "" {
			args = append(args, "--on", p.on)
		}
		code, stdout, stderr := runSprint(args...)
		if code != 0 || !strings.HasPrefix(stdout, "TASK push id="+p.id+" ") || !strings.Contains(stdout, " order_rt=3 ") {
			t.Fatalf("push %s: exit %d stdout %q stderr %q", p.id, code, stdout, stderr)
		}
		t.Logf("push %s: %s", p.id, strings.TrimSpace(stdout))
	}

	code, stdout, stderr := runSprint("ws", "show", "--redis", addr, "--order", "--stream", stream)
	want := strings.Join([]string{
		`STREAM 1 "land: order" cards=6 live=5 landed=0 sentinel=waiting`,
		`  1 ready   a`,
		`  2 waiting b <- a(ready) (reason: depends-on)`,
		`  3 waiting c <- a(ready) (reason: depends-on), b (reason: issue)`,
		`  4 waiting d <- b(waiting) (reason: depends-on), c(waiting) (reason: depends-on)`,
		`  5 ready   e <- d (reason: paths internal/x)`,
		`  6 waiting land-order:sentinel <- every other card of the stream (reason: sentinel; live 5)`,
		`SHOW streams=1 cards=6 edges=11 `,
	}, "\n")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, want) {
		t.Fatalf("ws show: exit %d stderr %q\n%s\nwant\n%s", code, stderr, stdout, want)
	}

	code, stdout, _ = runSprint("ws", "check", "--redis", addr)
	if code != 0 || !strings.HasPrefix(stdout, "CHECK streams=1 drift=0 cycles=0 invariants=ok ") {
		t.Fatalf("ws check clean: exit %d %q", code, stdout)
	}
	t.Logf("ws check: %s", strings.TrimSpace(stdout))

	if err := c.ZAdd(ctx, ws.Key(stream, "ready"), redis.Z{Score: 1, Member: "e"}).Err(); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runSprint("ws", "check", "--redis", addr, "--stream", stream)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	wantDrift := `ORDER DRIFT stream="land: order" stored=e,a,b,c,d,land-order:sentinel computed=a,b,c,d,e,land-order:sentinel`
	if code != 1 || len(lines) != 3 || !strings.HasPrefix(lines[0], "INVARIANTS ") || lines[1] != wantDrift ||
		!strings.HasPrefix(lines[2], "CHECK streams=1 drift=1 cycles=0 invariants=bad ") {
		t.Fatalf("ws check after a hand zadd: exit %d\n%s", code, stdout)
	}

	code, stdout, _ = runSprint("ws", "reorder", "--redis", addr, "--stream", stream)
	if code != 0 || !strings.HasPrefix(stdout, `REORDERED stream="land: order" cards=6 rescored=1 skipped=0 rt=3 `) {
		t.Fatalf("ws reorder: exit %d %q", code, stdout)
	}
	t.Logf("ws reorder: %s", strings.TrimSpace(stdout))
	if code, stdout, _ = runSprint("ws", "check", "--redis", addr); code != 0 {
		t.Fatalf("ws check after reorder: exit %d %q", code, stdout)
	}

	// card push: two cards with STREAM: write that stream's order, one
	// ORDER line: two (#7) before one (#8), the sentinel last.
	dir := t.TempDir()
	card := func(name, origin, depends string) string {
		path := filepath.Join(dir, name+".md")
		body := "LABEL: " + name + "\nREPO: mas-bandwidth/nova-tools\nBASE: dev\nbase-sha: " + strings.Repeat("ab", 20) + "\nPATHS: internal/y\nDEPENDS-ON: " + depends +
			"\nDONE-WHEN: go test passes\nSTREAM: cards: order\nORIGIN: mas-bandwidth/nova-tools#" + origin + "\n\nwhat and why\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	code, stdout, stderr = runSprint("card", "push", "--sprint", "sp1", "--redis", addr, card("one", "8", "none"), card("two", "7", "none"))
	if code != 0 || !regexp.MustCompile(`(?m)^ORDER stream="cards: order" order=3 order_rt=3 order_ms=[0-9.]+$`).MatchString(stdout) {
		t.Fatalf("card push: exit %d\n%s%s", code, stdout, stderr)
	}
	t.Logf("card push: %s", strings.TrimSpace(stdout))
	so, err := ws.ReadOrder(ctx, c, "cards: order")
	if err != nil || so.Drift() || len(so.Computed) != 3 || !strings.HasSuffix(so.Computed[0], ":two") {
		t.Fatalf("card stream order: %+v %v", so, err)
	}
	if code, stdout, _ = runSprint("ws", "check", "--redis", addr); code != 0 {
		t.Fatalf("ws check after card push: exit %d %q", code, stdout)
	}
}
