package table

// DefectFixture seeds the defects of 2026-09-22 (6.8) so that `table --check`
// renders them and asserts the exact output. Tests then add each forbidden
// sidecar key in turn to prove the two-writers refusal.
//
// The living scores are deliberately older than the 120 s freshness window so
// the stale count is deterministic; no score in the fixture is relative to now.
func DefectFixture() [][]string {
	return [][]string{
		{"SADD", "sprints", "s1"},
		{"HSET", "s:s1", "status", "open"},
		{"SADD", "benches", "b1", "b2", "b3"},
		{"SADD", "friends", "fran"},
		{"HSET", "bench:b1:desired", "slots", "4"},
		{"HSET", "bench:b1:beat", "at", "1"},
		{"ZADD", "bench:b1:starting", "0", "s1"},
		{"ZADD", "bench:b1:living", "1", "l1"},
		{"ZADD", "s:s1:bench:b1:queue", "0", "c1"},
		{"SADD", "s:s1:bench:b1:ended", "e1"},
		{"HSET", "bench:b2:beat", "at", "1"},
		{"ZADD", "s:s1:bench:b2:queue", "0", "d1", "0", "d2"},
		{"HSET", "bench:b3:desired", "slots", "1"},
		{"HSET", "friend:fran:beat", "at", "1"},
	}
}

// DefectGolden is the exact table DefectFixture must render.
func DefectGolden() string {
	return "name | up | desired | starting | living | stale | leased | queue | done | why\n" +
		"bench:b1 | up | 4 | 1 | 0 | 1 | 2 | 1 | 1 | stale: living >120s\n" +
		"bench:b2 | up | missing | 0 | 0 | 0 | 0 | 2 | 0 | missing: desired\n" +
		"bench:b3 | down | 1 | 0 | 0 | 0 | 0 | 0 | 0 | down: beat\n" +
		"friend:fran | up | missing | 0 | 0 | 0 | 0 | 0 | 0 | missing: desired\n" +
		"pipeline s1 queued=0 dealt=0 running=0 ended=0 harvested=0 review-ready=0 land-ready=0 landed=0 pool=0 waiting=0 backpressure=0 orphan-effect=0 reconcile-required=0\n"
}
