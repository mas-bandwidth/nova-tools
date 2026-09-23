package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// TestPushRefusesOverlappingBuildPaths is the nova-tools #3067 control: a build
// task whose PATHS intersect a live task's PATHS outside a DEPENDS-ON chain is
// refused with both ids; the fixture push with the chain declared succeeds.
func TestPushRefusesOverlappingBuildPaths(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "paths-3067"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-a", 4)

	push := func(id, title string) task.PushResult {
		t.Helper()
		got, err := task.PushChecked(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: title,
			Repo: "nova-tools", To: "ctl-a",
		})
		if err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
		return got
	}

	if got := push("build-3011-card-verb", "card verb | DONE-WHEN: x | PATHS: cmd/nova-sprint/card.go, internal/nsprint/card | DEPENDS-ON: -"); got.Status != task.PushCreated {
		t.Fatalf("first build push = %s; want CREATED", got.Status)
	}

	// The #3011/#3056 shape: a second build task registers the same verb file.
	got := push("build-3056-card-cut", "card cut | PATHS: cmd/nova-sprint/card.go | DEPENDS-ON: #2929 (landed)")
	if got.Status != task.PushOverlap {
		t.Fatalf("overlapping push = %s; want OVERLAP", got.Status)
	}
	if got.Overlap == nil || got.Overlap.With != "build-3011-card-verb" ||
		got.Overlap.Path != "cmd/nova-sprint/card.go" || got.Overlap.OtherPath != "cmd/nova-sprint/card.go" {
		t.Fatalf("overlap = %+v; want build-3011-card-verb on cmd/nova-sprint/card.go", got.Overlap)
	}
	line := got.Overlap.String()
	if !strings.Contains(line, "build-3056-card-cut") || !strings.Contains(line, "build-3011-card-verb") {
		t.Fatalf("overlap line %q must print both ids", line)
	}
	if got.Status.ExitCode() == 0 {
		t.Fatal("an overlap must exit non-zero")
	}
	if n, _ := client.Exists(ctx, "s:"+sprint+":task:build-3056-card-cut").Result(); n != 0 {
		t.Fatal("a refused push must write no task hash")
	}
	if n, _ := receipts(ctx, client, "s:"+sprint+":log", "task push", "build-3056-card-cut"); n != 0 {
		t.Fatalf("a refused push wrote %d receipts; want none", n)
	}

	// A directory PATH without a trailing slash covers the files beneath it.
	if got := push("build-dir", "dir | PATHS: internal/nsprint/card/cut.go"); got.Status != task.PushOverlap ||
		got.Overlap.With != "build-3011-card-verb" {
		t.Fatalf("file under a directory PATH = %+v; want OVERLAP with build-3011-card-verb", got)
	}

	// The fixture with the chain declared succeeds, and the chain is transitive.
	if got := push("build-3056-card-cut-chained", "card cut | PATHS: cmd/nova-sprint/card.go | DEPENDS-ON: build-3011-card-verb"); got.Status != task.PushCreated {
		t.Fatalf("chained push = %s (%v); want CREATED", got.Status, got.Overlap)
	}
	if got := push("build-3057-after", "after | PATHS: cmd/nova-sprint/card.go | DEPENDS-ON: build-3056-card-cut-chained"); got.Status != task.PushCreated {
		t.Fatalf("transitively chained push = %s (%v); want CREATED", got.Status, got.Overlap)
	}

	// A re-push of an existing id is answered by the create-only push, never
	// the lint: the identical payload is EXISTS even though its PATHS overlap.
	if got := push("build-3011-card-verb", "card verb | DONE-WHEN: x | PATHS: cmd/nova-sprint/card.go, internal/nsprint/card | DEPENDS-ON: -"); got.Status != task.PushExists {
		t.Fatalf("identical re-push = %s (%v); want EXISTS", got.Status, got.Overlap)
	}

	// Disjoint PATHS, another repo's PATHS and a read task never overlap.
	if got := push("build-disjoint", "other | PATHS: cmd/nova-sprint/table.go"); got.Status != task.PushCreated {
		t.Fatalf("disjoint push = %s (%v); want CREATED", got.Status, got.Overlap)
	}
	if got := push("build-other-repo", "other repo | PATHS: rowan-tools: cmd/nova-sprint/card.go"); got.Status != task.PushCreated {
		t.Fatalf("other-repo push = %s (%v); want CREATED", got.Status, got.Overlap)
	}
	read, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "read-3011", Kind: task.KindRead, To: "ctl-a",
		Title: "read nova-tools#3011 card verb | PATHS: cmd/nova-sprint/card.go",
		Repo:  "nova-tools", PR: 3011, Head: "abc",
	})
	if err != nil || read.Status == task.PushOverlap {
		t.Fatalf("read push = %+v, %v; a read task is never path-linted", read, err)
	}

	// A closed task no longer holds its PATHS.
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "build-disjoint", As: "ctl-a"})
	if err != nil || !ok {
		t.Fatalf("take build-disjoint: %v %v", ok, err)
	}
	if _, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "build-disjoint", Token: claim.Token, Evidence: "PR"}); err != nil {
		t.Fatal(err)
	}
	if got := push("build-after-close", "after close | PATHS: cmd/nova-sprint/table.go"); got.Status != task.PushCreated {
		t.Fatalf("push over a closed task = %s (%v); want CREATED", got.Status, got.Overlap)
	}

	// A live task in another sprint still holds its PATHS.
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	other := "paths-3067-b"
	client.HSet(ctx, "s:"+other, "status", "open")
	cross, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: other, ID: "build-cross", Kind: task.KindFix, To: "ctl-a",
		Title: "cross | PATHS: cmd/nova-sprint/table.go", Repo: "nova-tools",
	})
	if err != nil || cross.Status != task.PushOverlap || cross.Overlap.With != sprint+"/build-after-close" {
		t.Fatalf("cross-sprint push = %+v, %v; want OVERLAP with %s/build-after-close", cross, err, sprint)
	}
}

// TestPathsParseAndIntersect pins the title grammar and the check-cut.py match
// rule the push lint shares.
func TestPathsParseAndIntersect(t *testing.T) {
	paths, deps := task.ParseTitle("t | DONE-WHEN: a | PATHS: `cmd/a.go`, internal/b; rowan-tools: bin/c | DEPENDS-ON: #2929 (landed), build-x", "nova-tools")
	want := []string{"nova-tools:cmd/a.go", "nova-tools:internal/b", "rowan-tools:bin/c"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("paths = %q; want %q", paths, want)
	}
	if strings.Join(deps, " ") != "#2929 build-x" {
		t.Fatalf("deps = %q; want #2929 build-x", deps)
	}
	for _, c := range []struct {
		a, b string
		hit  bool
	}{
		{"r:cmd/a.go", "r:cmd/a.go", true},
		{"r:internal/b", "r:internal/b/c.go", true},
		{"r:internal/b/", "r:internal/b/c.go", true},
		{"r:internal/b/**", "r:internal/b/c/d.go", true},
		{"r:internal/b/*", "r:internal/b/c.go", true},
		{"r:internal/b/*", "r:internal/b/c/d.go", false},
		{"r:internal/*.go", "r:internal/x.go", true},
		{"r:internal/bc", "r:internal/b", false},
		{"r:cmd/a.go", "s:cmd/a.go", false},
		{":cmd/a.go", "s:cmd/a.go", true},
	} {
		if got := task.PathsIntersect(c.a, c.b); got != c.hit {
			t.Errorf("PathsIntersect(%q, %q) = %v; want %v", c.a, c.b, got, c.hit)
		}
		if got := task.PathsIntersect(c.b, c.a); got != c.hit {
			t.Errorf("PathsIntersect(%q, %q) = %v; want %v", c.b, c.a, got, c.hit)
		}
	}
}
