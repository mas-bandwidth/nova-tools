package audit

import (
	"context"
	"sort"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRegistryMatching(t *testing.T) {
	t.Parallel()

	reg := DefaultRegistry()

	tests := []struct {
		key        string
		wantFamily string
		wantMatch  bool
	}{
		{"ws:order", "ws", true},
		{"ws:names", "ws", true},
		{"ws:1:stream/fleet:ready", "ws", true},
		{"ws:slug:alpha", "ws", true},
		{"s:sprint-1", "s", true},
		{"s:sprint-1:pool", "s", true},
		{"s:sprint-1:card:123", "s", true},
		{"sprint:order", "sprint", true},
		{"sprint:epoch", "sprint", true},
		{"sprint:s1:cards", "sprint", true},
		{"sprints", "sprints", true},
		{"task:123", "task", true},
		{"task:closed", "task", true},
		{"card:123", "card", true},
		{"bench:space", "bench", true},
		{"bench:space:cards:ready", "bench", true},
		{"benches", "benches", true},
		{"friend:rowan", "friend", true},
		{"friend:rowan:queue", "friend", true},
		{"friends", "friends", true},
		{"friends:login", "friends", true},
		{"q:blocked", "q", true},
		{"q:rowan", "q", true},
		{"table:layout", "table", true},
		{"tables", "tables", true},
		{"consumers", "consumers", true},
		{"readers", "readers", true},
		{"views", "views", true},
		{"view:live", "view", true},
		{"cfg:ci", "cfg", true},
		{"ev:github:consumer:land", "ev", true},
		{"proc:reconciler", "proc", true},
		{"proc:land:nova-tools", "proc", true},
		{"land:gen", "land", true},
		{"land:repo:streams", "land", true},
		{"landed:repo:unit:head", "landed", true},
		{"lease:reconciler", "lease", true},
		{"machine:control-1:ceiling", "machine", true},
		{"debit:machine:coord", "debit", true},
		{"aside:123", "aside", true},
		{"control:s1", "control", true},
		{"body:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "body", true},
		{"cap:log", "cap", true},
		{"col:working", "col", true},
		{"flaky:idx", "flaky", true},
		{"fleet:release", "fleet", true},
		{"route:s1", "route", true},
		{"hold-route:s1", "hold-route", true},
		{"runner:r1", "runner", true},
		{"stream:swarm", "stream", true},
		{"spec:s1", "spec", true},
		{"specs:stream:working", "specs", true},
		{"rec:seq", "rec", true},
		{"pr:repo:123", "pr", true},
		{"pr-to-read:123", "pr-to-read", true},
		{"pr-ambiguous:123", "pr-ambiguous", true},
		{"harvest:b1", "harvest", true},
		{"hold:task:1", "hold", true},
		{"adopt:verbs", "adopt", true},
		{"authz:sprint-plan", "authz", true},
		{"batch:123", "batch", true},
		{"beat:friend", "beat", true},
		{"line:post", "line", true},
		{"unit:u1", "unit", true},
		{"wf:ci", "wf", true},
		{"worker:bench:slot", "worker", true},
		{"width:log", "width", true},
		{"verbs:unused:log", "verbs", true},
		{"ci:pool", "ci", true},
		{"ci-ok:repo:head", "ci-ok", true},
		{"idx:task:closed", "idx", true},
		{"probe", "probe", true},
		{"probe:http", "probe", true},
		{"ref:head:tasks", "ref", true},

		// Unknown / orphan keys
		{"random_orphan_key", "", false},
		{"orphan:redis:foo", "", false},
		{"test:something", "", false},
		{"myprefix:key1", "", false},
	}

	for _, tt := range tests {
		fam, ok := reg.Match(tt.key)
		if ok != tt.wantMatch {
			t.Errorf("Match(%q) = %v; wantMatch %v", tt.key, ok, tt.wantMatch)
			continue
		}
		if tt.wantMatch && fam.Name != tt.wantFamily {
			t.Errorf("Match(%q) family = %q; want %q", tt.key, fam.Name, tt.wantFamily)
		}
	}
}

func TestRegistryFindFamily(t *testing.T) {
	t.Parallel()

	reg := DefaultRegistry()

	fam, ok := reg.FindFamily("ws")
	if !ok || fam.Name != "ws" || fam.Pattern != "ws:*" {
		t.Fatalf("FindFamily(ws) = %+v, %v; want ws family", fam, ok)
	}

	famByPattern, ok := reg.FindFamily("ws:*")
	if !ok || famByPattern.Name != "ws" {
		t.Fatalf("FindFamily(ws:*) = %+v, %v; want ws family", famByPattern, ok)
	}

	_, ok = reg.FindFamily("nonexistent")
	if ok {
		t.Fatalf("FindFamily(nonexistent) returned true")
	}
}

func TestAuditAndPurgeWithMiniredis(t *testing.T) {
	t.Parallel()

	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	// Seed keys
	s.Set("ws:order", "1")
	s.Set("ws:names", "2")
	s.Set("task:t1", "3")
	s.Set("task:t2", "4")
	s.Set("s:s1:pool", "5")
	s.Set("orphan:key1", "6")
	s.Set("orphan:key2", "7")

	reg := DefaultRegistry()
	report, err := reg.Audit(ctx, client)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	if report.TotalKeys != 7 {
		t.Fatalf("report.TotalKeys = %d; want 7", report.TotalKeys)
	}
	if report.FamilyCounts["ws"] != 2 {
		t.Errorf("ws count = %d; want 2", report.FamilyCounts["ws"])
	}
	if report.FamilyCounts["task"] != 2 {
		t.Errorf("task count = %d; want 2", report.FamilyCounts["task"])
	}
	if report.FamilyCounts["s"] != 1 {
		t.Errorf("s count = %d; want 1", report.FamilyCounts["s"])
	}
	if len(report.OrphanKeys) != 2 {
		t.Fatalf("orphan count = %d; want 2", len(report.OrphanKeys))
	}
	sort.Strings(report.OrphanKeys)
	if report.OrphanKeys[0] != "orphan:key1" || report.OrphanKeys[1] != "orphan:key2" {
		t.Errorf("orphan keys = %v; want [orphan:key1, orphan:key2]", report.OrphanKeys)
	}

	// Purge family "task"
	deleted, err := reg.Purge(ctx, client, "task")
	if err != nil {
		t.Fatalf("Purge(task): %v", err)
	}
	if deleted != 2 {
		t.Errorf("Purge(task) deleted = %d; want 2", deleted)
	}

	// Re-audit
	report2, err := reg.Audit(ctx, client)
	if err != nil {
		t.Fatalf("Audit2: %v", err)
	}
	if report2.TotalKeys != 5 {
		t.Errorf("report2.TotalKeys = %d; want 5", report2.TotalKeys)
	}
	if report2.FamilyCounts["task"] != 0 {
		t.Errorf("task count after purge = %d; want 0", report2.FamilyCounts["task"])
	}

	// Purge orphans
	deletedOrphans, err := reg.Purge(ctx, client, "orphans")
	if err != nil {
		t.Fatalf("Purge(orphans): %v", err)
	}
	if deletedOrphans != 2 {
		t.Errorf("Purge(orphans) deleted = %d; want 2", deletedOrphans)
	}

	// Re-audit
	report3, err := reg.Audit(ctx, client)
	if err != nil {
		t.Fatalf("Audit3: %v", err)
	}
	if report3.TotalKeys != 3 {
		t.Errorf("report3.TotalKeys = %d; want 3", report3.TotalKeys)
	}
	if len(report3.OrphanKeys) != 0 {
		t.Errorf("orphan keys after purge = %v; want []", report3.OrphanKeys)
	}
}
