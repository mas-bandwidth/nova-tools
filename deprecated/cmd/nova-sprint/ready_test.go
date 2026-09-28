package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

// mapForge is the forge seam as a map (CI-NET: no host in a test).
type mapForge map[string]deal.Ref

func (m mapForge) Ref(_ context.Context, repo string, n int) (deal.Ref, error) {
	r, ok := m[fmt.Sprintf("%s#%d", repo, n)]
	if !ok {
		return deal.Ref{}, fmt.Errorf("fake forge: %s#%d not in fixture", repo, n)
	}
	return r, nil
}

// TestReadyWhy is #3109's DONE-WHEN: an open dependency PR prints WAIT, an
// overlap prints WAIT PATHS, a REST base lookup returning unknown prints
// UNKNOWN and the task is not ready; a PR closed without merge prints DEAD;
// the ready list is exactly the antichain.
func TestReadyWhy(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const S, repo = "ctl-ready", "mas-bandwidth/nova-tools"
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	c.SAdd(ctx, "sprints", S)
	c.HSet(ctx, "s:"+S, "status", "open")
	card := func(label, state string, score float64, kv ...any) {
		c.HSet(ctx, "s:"+S+":card:"+label, append([]any{"state", state, "repo", repo, "base", "dev"}, kv...)...)
		c.SAdd(ctx, "s:"+S+":idx:card:"+state, label)
		if state == "queued" {
			c.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: score, Member: label})
		}
	}
	// Dependencies: A's PR is open, D's PR merged into a base REST did not
	// name, I's PR closed without merge.
	card("task-a", "ended", 0, "outcome", "DONE", "pr", "42")
	card("task-d", "ended", 0, "outcome", "DONE", "pr", "43")
	card("task-i", "ended", 0, "outcome", "DONE", "pr", "45")
	// In flight: F holds internal/x/y.
	card("task-f", "running", 0, "paths", "internal/x/y")
	// Candidates.
	card("task-b", "queued", 1, "depends_on", "task-a", "paths", "cmd/b")
	card("task-c", "queued", 2, "depends_on", "task-d", "paths", "cmd/c")
	card("task-e", "queued", 3, "paths", "internal/x/")
	card("task-g", "queued", 4, "paths", "docs/g.md")
	card("task-h", "queued", 5, "depends_on", "task-i", "paths", "cmd/h")
	c.HSet(ctx, "task:t-1", "state", "open", "priority", "6", "repo", repo, "base", "dev",
		"depends_on", repo+"#44", "paths", "cmd/t1")
	c.SAdd(ctx, "s:"+S+":idx:task:open", "t-1")

	old := readyForge
	t.Cleanup(func() { readyForge = old })
	readyForge = func(*store.Store) deal.PRs {
		return mapForge{
			repo + "#42": {IsPR: true, State: "open", Base: "dev"},
			repo + "#43": {IsPR: true, Merged: true, State: "closed", Base: ""},
			repo + "#44": {IsPR: true, Merged: true, State: "closed", Base: "dev"},
			repo + "#45": {IsPR: true, State: "closed", Base: "dev"},
		}
	}

	call := func(args ...string) (string, int) {
		var out, errOut bytes.Buffer
		code := run(append([]string{"ready", "--redis", mr.Addr()}, args...), &out, &errOut)
		if code == 2 {
			t.Fatalf("ready %v could not run: %s", args, errOut.String())
		}
		return out.String(), code
	}

	for _, tc := range []struct {
		id, want string
		code     int
	}{
		{"task-b", "WAIT task-a pr#42 open", 1},
		{"task-e", "WAIT PATHS task-f", 1},
		{"task-c", "UNKNOWN task-d pr#43 base-unresolved", 1},
		{"task-h", "DEAD task-i pr#45 closed-unmerged", 1},
		{S + "/task-g", "READY " + S + "/task-g", 0},
		{"t-1", "READY " + S + "/t-1", 0},
	} {
		got, code := call("--why", tc.id)
		if strings.TrimSpace(got) != tc.want || code != tc.code {
			t.Errorf("ready --why %s = %q exit %d, want %q exit %d", tc.id, strings.TrimSpace(got), code, tc.want, tc.code)
		}
	}

	got, code := call()
	want := "READY " + S + "/task-g card\nREADY " + S + "/t-1 task\nready 2/6\n"
	if got != want || code != 0 {
		t.Errorf("ready = %q exit %d, want %q exit 0: task-c's dependency merged into an unknown base must not be ready", got, code, want)
	}

	var errOut bytes.Buffer
	if code := run([]string{"ready", "--redis", mr.Addr(), "--why", "no-such"}, &bytes.Buffer{}, &errOut); code != 2 {
		t.Errorf("ready --why no-such exit %d, want 2 (%s)", code, errOut.String())
	}
}
