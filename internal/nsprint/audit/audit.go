// Package audit provides the key-family registry, orphan key detection,
// and family key purging for the Redis sprint/table/swarm store (#4334).
package audit

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Family is one registered key family in the sprint/table/swarm universe.
type Family struct {
	Name        string // short identifier, e.g. "ws", "task", "card"
	Pattern     string // glob pattern, e.g. "ws:*", "sprints"
	OwnerVerb   string // the command or verb owning this family
	ClearedBy   string // what cleans or expires keys in this family
	Description string // what keys in this family hold
}

// DefaultFamilies lists every key family recognized in the Redis store.
var DefaultFamilies = []Family{
	{
		Name:        "ws",
		Pattern:     "ws:*",
		OwnerVerb:   "nova-sprint ws",
		ClearedBy:   "sprint clear",
		Description: "work set streams, states, order, paths, and logs",
	},
	{
		Name:        "s",
		Pattern:     "s:*",
		OwnerVerb:   "nova-sprint sprint",
		ClearedBy:   "sprint clear",
		Description: "sprint metadata, card pools, waits, and indexes",
	},
	{
		Name:        "sprint",
		Pattern:     "sprint:*",
		OwnerVerb:   "nova-sprint sprint",
		ClearedBy:   "sprint clear",
		Description: "sprint orders, epochs, notes, and tasks",
	},
	{
		Name:        "sprints",
		Pattern:     "sprints",
		OwnerVerb:   "nova-sprint sprint",
		ClearedBy:   "sprint clear",
		Description: "set of all active and historical sprints",
	},
	{
		Name:        "task",
		Pattern:     "task:*",
		OwnerVerb:   "nova-sprint task",
		ClearedBy:   "sprint clear",
		Description: "task records, state hashes, shapes, and dependencies",
	},
	{
		Name:        "card",
		Pattern:     "card:*",
		OwnerVerb:   "nova-sprint card",
		ClearedBy:   "sprint clear",
		Description: "card records, results, and desired states",
	},
	{
		Name:        "bench",
		Pattern:     "bench:*",
		OwnerVerb:   "nova-sprint bench",
		ClearedBy:   "sprint audit --purge bench",
		Description: "bench host states, queues, leases, and cards",
	},
	{
		Name:        "benches",
		Pattern:     "benches",
		OwnerVerb:   "nova-sprint bench",
		ClearedBy:   "sprint audit --purge bench",
		Description: "set of registered benchmark worker hosts",
	},
	{
		Name:        "friend",
		Pattern:     "friend:*",
		OwnerVerb:   "nova-sprint friend",
		ClearedBy:   "sprint audit --purge friend",
		Description: "friend presence, heartbeats, queues, and slots",
	},
	{
		Name:        "friends",
		Pattern:     "friends*",
		OwnerVerb:   "nova-sprint friend",
		ClearedBy:   "sprint audit --purge friend",
		Description: "registered friends set and login credentials",
	},
	{
		Name:        "q",
		Pattern:     "q:*",
		OwnerVerb:   "nova-sprint q",
		ClearedBy:   "sprint audit --purge q",
		Description: "queues for friends and task states",
	},
	{
		Name:        "table",
		Pattern:     "table:*",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "table layouts, cells, and member snapshots",
	},
	{
		Name:        "tables",
		Pattern:     "tables",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "table definitions set",
	},
	{
		Name:        "consumers",
		Pattern:     "consumers",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "set of active consumer names",
	},
	{
		Name:        "readers",
		Pattern:     "readers",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "set of registered table readers",
	},
	{
		Name:        "views",
		Pattern:     "views",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "set of configured table views",
	},
	{
		Name:        "view",
		Pattern:     "view:*",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "table view definitions",
	},
	{
		Name:        "cfg",
		Pattern:     "cfg:*",
		OwnerVerb:   "nova-sprint cfg",
		ClearedBy:   "none",
		Description: "configuration parameters for CI, deal, fleet, and landing",
	},
	{
		Name:        "ev",
		Pattern:     "ev:*",
		OwnerVerb:   "nova-sprint event",
		ClearedBy:   "reconciler",
		Description: "event streams and hooks",
	},
	{
		Name:        "proc",
		Pattern:     "proc:*",
		OwnerVerb:   "nova-sprint proc",
		ClearedBy:   "reconciler",
		Description: "process leases, progress, and locks",
	},
	{
		Name:        "land",
		Pattern:     "land:*",
		OwnerVerb:   "nova-sprint land",
		ClearedBy:   "reconciler",
		Description: "landing chains, batches, briefs, and gates",
	},
	{
		Name:        "landed",
		Pattern:     "landed:*",
		OwnerVerb:   "nova-sprint land",
		ClearedBy:   "reconciler",
		Description: "landed commit receipts and units",
	},
	{
		Name:        "lease",
		Pattern:     "lease:*",
		OwnerVerb:   "nova-sprint lease",
		ClearedBy:   "reconciler",
		Description: "reconciler, router, and landing leases",
	},
	{
		Name:        "machine",
		Pattern:     "machine:*",
		OwnerVerb:   "nova-sprint machine",
		ClearedBy:   "sprint audit --purge machine",
		Description: "machine debits and ceilings",
	},
	{
		Name:        "debit",
		Pattern:     "debit:*",
		OwnerVerb:   "nova-sprint machine",
		ClearedBy:   "sprint audit --purge machine",
		Description: "machine consumer debits",
	},
	{
		Name:        "aside",
		Pattern:     "aside:*",
		OwnerVerb:   "nova-sprint card",
		ClearedBy:   "sprint clear",
		Description: "parked cards kept aside across epochs",
	},
	{
		Name:        "control",
		Pattern:     "control:*",
		OwnerVerb:   "nova-sprint control",
		ClearedBy:   "sprint clear",
		Description: "control sprint tasks and teardown records",
	},
	{
		Name:        "body",
		Pattern:     "body:*",
		OwnerVerb:   "nova-sprint card",
		ClearedBy:   "sprint clear",
		Description: "card body payload sha256 content hashes",
	},
	{
		Name:        "cap",
		Pattern:     "cap:*",
		OwnerVerb:   "nova-sprint cap",
		ClearedBy:   "TTL",
		Description: "capacity events and log streams",
	},
	{
		Name:        "col",
		Pattern:     "col:*",
		OwnerVerb:   "nova-sprint table",
		ClearedBy:   "sprint clear",
		Description: "table column states and widths",
	},
	{
		Name:        "flaky",
		Pattern:     "flaky:*",
		OwnerVerb:   "nova-sprint flaky",
		ClearedBy:   "reconciler",
		Description: "flaky test observations, indexes, and locks",
	},
	{
		Name:        "fleet",
		Pattern:     "fleet:*",
		OwnerVerb:   "nova-sprint fleet",
		ClearedBy:   "none",
		Description: "fleet deployment metadata and release status",
	},
	{
		Name:        "route",
		Pattern:     "route:*",
		OwnerVerb:   "nova-sprint route",
		ClearedBy:   "reconciler",
		Description: "routing passes and locks",
	},
	{
		Name:        "routes",
		Pattern:     "routes:*",
		OwnerVerb:   "nova-sprint route",
		ClearedBy:   "reconciler",
		Description: "route cost ceilings and limits",
	},
	{
		Name:        "hold-route",
		Pattern:     "hold-route:*",
		OwnerVerb:   "nova-sprint route",
		ClearedBy:   "reconciler",
		Description: "held route locks",
	},
	{
		Name:        "runner",
		Pattern:     "runner:*",
		OwnerVerb:   "nova-sprint runner",
		ClearedBy:   "reconciler",
		Description: "runner assignments and statuses",
	},
	{
		Name:        "stream",
		Pattern:     "stream:*",
		OwnerVerb:   "nova-sprint ws",
		ClearedBy:   "sprint clear",
		Description: "stream metadata and configurations",
	},
	{
		Name:        "spec",
		Pattern:     "spec:*",
		OwnerVerb:   "nova-sprint spec",
		ClearedBy:   "reconciler",
		Description: "specification states and scores",
	},
	{
		Name:        "specs",
		Pattern:     "specs:*",
		OwnerVerb:   "nova-sprint spec",
		ClearedBy:   "reconciler",
		Description: "specification streams and working/done sets",
	},
	{
		Name:        "rec",
		Pattern:     "rec:*",
		OwnerVerb:   "nova-sprint rec",
		ClearedBy:   "reconciler",
		Description: "record sequence counters",
	},
	{
		Name:        "pr",
		Pattern:     "pr:*",
		OwnerVerb:   "nova-sprint pr",
		ClearedBy:   "reconciler",
		Description: "pull request tracking, references, and reap gates",
	},
	{
		Name:        "pr-to-read",
		Pattern:     "pr-to-read:*",
		OwnerVerb:   "nova-sprint pr",
		ClearedBy:   "reconciler",
		Description: "pull requests queued for adoption and reading",
	},
	{
		Name:        "pr-ambiguous",
		Pattern:     "pr-ambiguous:*",
		OwnerVerb:   "nova-sprint pr",
		ClearedBy:   "reconciler",
		Description: "pull requests with ambiguous head/state",
	},
	{
		Name:        "harvest",
		Pattern:     "harvest:*",
		OwnerVerb:   "nova-sprint harvest",
		ClearedBy:   "reconciler",
		Description: "harvested PR and branch assignments",
	},
	{
		Name:        "hold",
		Pattern:     "hold:*",
		OwnerVerb:   "nova-sprint hold",
		ClearedBy:   "reconciler",
		Description: "held tasks and PR locks",
	},
	{
		Name:        "adopt",
		Pattern:     "adopt:*",
		OwnerVerb:   "nova-sprint adopt",
		ClearedBy:   "reconciler",
		Description: "task and stream adoption records",
	},
	{
		Name:        "authz",
		Pattern:     "authz:*",
		OwnerVerb:   "nova-sprint authz",
		ClearedBy:   "none",
		Description: "authorization tokens and plan permissions",
	},
	{
		Name:        "batch",
		Pattern:     "batch:*",
		OwnerVerb:   "nova-sprint batch",
		ClearedBy:   "reconciler",
		Description: "gate and landing batch records",
	},
	{
		Name:        "beat",
		Pattern:     "beat:*",
		OwnerVerb:   "nova-sprint beat",
		ClearedBy:   "TTL",
		Description: "heartbeats and liveness records",
	},
	{
		Name:        "line",
		Pattern:     "line:*",
		OwnerVerb:   "nova-sprint line",
		ClearedBy:   "reconciler",
		Description: "timeline posts and commentary",
	},
	{
		Name:        "unit",
		Pattern:     "unit:*",
		OwnerVerb:   "nova-sprint unit",
		ClearedBy:   "sprint clear",
		Description: "work unit definitions and boundaries",
	},
	{
		Name:        "wf",
		Pattern:     "wf:*",
		OwnerVerb:   "nova-sprint wf",
		ClearedBy:   "reconciler",
		Description: "workflow execution and steps",
	},
	{
		Name:        "worker",
		Pattern:     "worker:*",
		OwnerVerb:   "nova-sprint worker",
		ClearedBy:   "reconciler",
		Description: "worker slot and process allocations",
	},
	{
		Name:        "width",
		Pattern:     "width:*",
		OwnerVerb:   "nova-sprint width",
		ClearedBy:   "TTL",
		Description: "width logs and slot capacity records",
	},
	{
		Name:        "verbs",
		Pattern:     "verbs:*",
		OwnerVerb:   "nova-sprint verbs",
		ClearedBy:   "TTL",
		Description: "unused verb telemetry logs",
	},
	{
		Name:        "ci",
		Pattern:     "ci:*",
		OwnerVerb:   "nova-sprint ci",
		ClearedBy:   "reconciler",
		Description: "continuous integration runs, pools, and status",
	},
	{
		Name:        "ci-dispose",
		Pattern:     "ci-dispose:*",
		OwnerVerb:   "nova-sprint ci",
		ClearedBy:   "reconciler",
		Description: "disposed CI resources",
	},
	{
		Name:        "ci-end",
		Pattern:     "ci-end:*",
		OwnerVerb:   "nova-sprint ci",
		ClearedBy:   "reconciler",
		Description: "completed CI runs",
	},
	{
		Name:        "ci-fail",
		Pattern:     "ci-fail:*",
		OwnerVerb:   "nova-sprint ci",
		ClearedBy:   "reconciler",
		Description: "failed CI runs",
	},
	{
		Name:        "ci-ok",
		Pattern:     "ci-ok:*",
		OwnerVerb:   "nova-sprint ci",
		ClearedBy:   "reconciler",
		Description: "passed CI run receipts",
	},
	{
		Name:        "idx",
		Pattern:     "idx:*",
		OwnerVerb:   "nova-sprint idx",
		ClearedBy:   "sprint clear",
		Description: "secondary indexes for cards and tasks",
	},
	{
		Name:        "probe",
		Pattern:     "probe*",
		OwnerVerb:   "nova-sprint probe",
		ClearedBy:   "TTL",
		Description: "health probe status and timestamps",
	},
	{
		Name:        "ref",
		Pattern:     "ref:*",
		OwnerVerb:   "nova-sprint card",
		ClearedBy:   "sprint clear",
		Description: "card reference mappings",
	},
}

// Registry manages key families and matching against Redis keys.
type Registry struct {
	families []Family
	matchers []*regexp.Regexp
}

// NewRegistry constructs a Registry from the given family list.
func NewRegistry(families ...Family) *Registry {
	r := &Registry{
		families: make([]Family, len(families)),
		matchers: make([]*regexp.Regexp, len(families)),
	}
	copy(r.families, families)
	for i, f := range families {
		r.matchers[i] = globToRegex(f.Pattern)
	}
	return r
}

// DefaultRegistry returns a Registry populated with DefaultFamilies.
func DefaultRegistry() *Registry {
	return NewRegistry(DefaultFamilies...)
}

// Families returns a copy of the registered families.
func (r *Registry) Families() []Family {
	out := make([]Family, len(r.families))
	copy(out, r.families)
	return out
}

// Match reports whether a Redis key matches any registered family.
func (r *Registry) Match(key string) (*Family, bool) {
	for i, m := range r.matchers {
		if m.MatchString(key) {
			return &r.families[i], true
		}
	}
	return nil, false
}

// FindFamily looks up a registered family by name or exact pattern.
func (r *Registry) FindFamily(nameOrPattern string) (*Family, bool) {
	for i := range r.families {
		if r.families[i].Name == nameOrPattern || r.families[i].Pattern == nameOrPattern {
			return &r.families[i], true
		}
	}
	return nil, false
}

// AuditReport holds the aggregate results of a Redis keyspace scan.
type AuditReport struct {
	TotalKeys    int
	FamilyCounts map[string]int // Family.Name -> count
	OrphanKeys   []string
}

// Audit scans all keys in Redis, tallies counts per registered family,
// and flags any key matching no family as an orphan.
func (r *Registry) Audit(ctx context.Context, client redis.UniversalClient) (*AuditReport, error) {
	report := &AuditReport{
		FamilyCounts: make(map[string]int),
	}
	var cursor uint64
	for {
		keys, nextCursor, err := client.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			report.TotalKeys++
			fam, ok := r.Match(key)
			if ok {
				report.FamilyCounts[fam.Name]++
			} else {
				report.OrphanKeys = append(report.OrphanKeys, key)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	sort.Strings(report.OrphanKeys)
	return report, nil
}

// Purge deletes all keys belonging to familyNameOrPattern. If familyNameOrPattern
// is "orphans", all keys not matching any registered family are deleted.
// Returns the number of keys deleted.
func (r *Registry) Purge(ctx context.Context, client redis.UniversalClient, familyNameOrPattern string) (int64, error) {
	if familyNameOrPattern == "orphans" {
		rep, err := r.Audit(ctx, client)
		if err != nil {
			return 0, err
		}
		if len(rep.OrphanKeys) == 0 {
			return 0, nil
		}
		return deleteInBatches(ctx, client, rep.OrphanKeys)
	}

	fam, ok := r.FindFamily(familyNameOrPattern)
	if !ok {
		return 0, fmt.Errorf("unknown family %q", familyNameOrPattern)
	}
	matcher := globToRegex(fam.Pattern)
	var cursor uint64
	var deleted int64
	for {
		keys, nextCursor, err := client.Scan(ctx, cursor, fam.Pattern, 1000).Result()
		if err != nil {
			return deleted, err
		}
		var toDel []string
		for _, k := range keys {
			if matcher.MatchString(k) {
				toDel = append(toDel, k)
			}
		}
		if len(toDel) > 0 {
			n, err := deleteInBatches(ctx, client, toDel)
			if err != nil {
				return deleted, err
			}
			deleted += n
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return deleted, nil
}

func deleteInBatches(ctx context.Context, client redis.UniversalClient, keys []string) (int64, error) {
	var total int64
	const batchSize = 500
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		n, err := client.Del(ctx, keys[i:end]...).Result()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// globToRegex converts a Redis glob pattern (supporting *, ?, and character sets) into a regexp.
func globToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteString("\\")
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
