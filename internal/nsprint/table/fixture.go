package table

// DefectFixture seeds the defects of 2026-09-22 (6.8) so that `table --check`
// renders them and asserts the exact output. Tests then add each forbidden
// sidecar key in turn to prove the two-writers refusal.
//
// The copy's lease_until is deliberately in the past so the stale count is
// deterministic; no value in the fixture is relative to now. The rows are
// the consumer sets (#3998): ready and working are <consumer>:cards:ready
// and :cards:working.
func DefectFixture() [][]string {
	return [][]string{
		{"SADD", "sprints", "s1", "old", "control-hidden"},
		{"HSET", "s:s1", "status", "open"},
		{"HSET", "s:old", "status", "closed"},
		{"HSET", "s:control-hidden", "status", "open"},
		{"SADD", "benches", "b1", "b2", "b3"},
		{"SADD", "friends", "fran", "eight", "sleeping"},
		{"HSET", "bench:b1:desired", "slots", "4"},
		{"HSET", "bench:b1:beat", "at", "1"},
		{"ZADD", "bench:b1:cards:ready", "0", "s:s1:card:r1"},
		{"ZADD", "bench:b1:cards:working", "1", "s:s1:card:l1", "2", "t1~1"},
		{"HSET", "task:t1~1", "lease_until", "1"},
		{"ZADD", "s:s1:bench:b1:queue", "0", "c1"},
		{"ZADD", "s:old:bench:b1:queue", "0", "old1", "0", "old2"},
		{"ZADD", "s:control-hidden:bench:b1:queue", "0", "ctl1", "0", "ctl2"},
		{"SADD", "s:s1:bench:b1:ended", "e1"},
		{"ZADD", "s:s1:pool", "0", "pool-card"},
		{"HSET", "bench:b2", "host", ""},
		{"HSET", "bench:b2:beat", "at", "1"},
		{"ZADD", "s:s1:bench:b2:queue", "0", "d1", "0", "d2"},
		{"HSET", "bench:b3:desired", "slots", "1"},
		{"HSET", "friend:fran:beat", "at", "1"},
		{"HSET", "friend:eight:desired", "slots", "8"},
		{"HSET", "friend:eight:beat", "at", "1"},
		{"ZADD", "friend:eight:cards:working", "1", "s1/a/1", "1", "s1/b/1", "1", "s1/c/1", "1", "s1/d/1", "1", "s1/e/1",
			"1", "s1/f/1", "1", "s1/g/1", "9999999999999", "t8"},
		{"HSET", "friend:sleeping:desired", "slots", "2"},
	}
}

// DefectGolden is the exact table DefectFixture must render.
func DefectGolden() string {
	body := "name | up | desired | ready | working | stale | leased | queue | done | why\n" +
		"bench:b1 | up | 4 | 1 | 1 | 1 | 2 | 1 | 1 | stale: lease lapsed\n" +
		"bench:b2 | up | missing | 0 | 0 | 0 | 0 | 2 | 0 | missing: desired\n" +
		"bench:b3 | down | 1 | 0 | 0 | 0 | 0 | 0 | 0 | down: beat\n" +
		"friend:eight | up | 8 | 0 | 8 | 0 | 8 | 0 | 0 | \n" +
		"friend:fran | up | missing | 0 | 0 | 0 | 0 | 0 | 0 | missing: desired\n" +
		"friend:sleeping | down | 2 | 0 | 0 | 0 | 0 | 0 | 0 | down: beat\n" +
		// s1's pool holds a card, and the row still refuses (#4411)
		"pipeline s1 " + RetiredPipeline + "\n"
	for _, name := range []string{"backpressure", "harvest:b1", "harvest:b2", "harvest:b3", "hold-to-fix", "ok-to-friend", "pr-to-read", "reconciler"} {
		body += "proc " + name + " down age=-1s why=missing: pass\n"
	}
	return body
}
