package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TestSprintFoldVerb is `nova-sprint sprint fold --sprint <S> --redis <addr>`
// (#2618) end to end on miniredis: exit 2 with no sprint named, exit 1 with
// the remedy while the sprint is open, and once closed exit 0 with the report
// lines and the receipt last, the receipt also on s:<S>:fold:sum.
func TestSprintFoldVerb(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv(store.UserEnv, "")
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const s = "fold-verb"
	id := "s:" + s + ":card:one"
	c.HSet(ctx, id, "where", "done", "state", "landed", "kind", "code", "repo", "nova-tools", "pr", "7", "head", "abc1234def")
	c.ZAdd(ctx, "sprint:"+s+":cards", redis.Z{Score: 1, Member: id})
	c.HSet(ctx, "s:"+s+":disp:nova-tools:7", "stella@abc1234def", "APPROVE 9 u 1", "jev@abc1234", "APPROVE 8 u 2")

	if code, _, errOut := runSprint("sprint", "fold", "--redis", mr.Addr()); code != 2 || !strings.Contains(errOut, "needs --sprint") {
		t.Fatalf("no --sprint: code=%d stderr=%q; want 2 naming --sprint", code, errOut)
	}
	c.HSet(ctx, "s:"+s, "status", "open")
	if code, out, _ := runSprint("sprint", "fold", "--redis", mr.Addr(), "--sprint", s); code != 1 || !strings.Contains(out, "remedy: nova-sprint sprint close --sprint "+s) {
		t.Fatalf("open sprint: code=%d out=%q; want 1 with the close remedy", code, out)
	}
	c.HSet(ctx, "s:"+s, "status", "closed")
	code, out, errOut := runSprint("sprint", "fold", "--redis", mr.Addr(), "--sprint", s)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) < 2 {
		t.Fatalf("closed sprint: code=%d out=%q stderr=%q", code, out, errOut)
	}
	for _, want := range []string{
		"FOLD CALIB card=one type=code score=9 who=stella jev=8 head=abc1234def",
		"FOLD SCORE type=code type_src=kind cards=1 closed=1 landed=1 useful=1/1 labeled=1 mean=9.00 range=9-9 useful_min=8",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("no line %q in\n%s", want, out)
		}
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "FOLD sprint="+s+" cards=1 closed=1 types=1 proposals=0 calib=1 ") || c.HGet(ctx, "s:"+s+":fold:sum", "receipt").Val() != last {
		t.Fatalf("receipt %q; want it last and on s:%s:fold:sum", last, s)
	}
}
