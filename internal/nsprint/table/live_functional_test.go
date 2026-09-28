//go:build functional

package table_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// case2674 is one keyspace of the #2674 control. Every golden is the bash of
// record's own output on that keyspace (live_bash_test.go runs it and, with
// -update-golden-2674, wrote the file); Go must print the same bytes.
type case2674 struct {
	name    string
	extra   [][]string // applied after Fixture2674()
	noRedis bool       // Redis does not answer at all
	golden  func() string
	file    string
	// diverge names a bench row the bash prints with its numbers and Go
	// prints "stale" (the bash's IFS-collapse defect; #3372); such a case has
	// no golden.
	diverge string
}

func cases2674() []case2674 {
	return []case2674{
		{name: "live", golden: table.Golden2674, file: "sprint-table-2674.golden"},
		{name: "degraded", extra: table.Fixture2674Degraded(), golden: table.Golden2674Degraded, file: "sprint-table-2674-degraded.golden"},
		{name: "noredis", noRedis: true, golden: table.Golden2674NoRedis, file: "sprint-table-2674-noredis.golden"},
		{name: "dead-bench-no-dealer-fields", diverge: "zz-dead", extra: [][]string{
			{"HSET", "bench:zz-dead", "host", "zz-dead", "queue", "2", "working", "1", "done", "5", "ok", "5", "fail", "0", "load1", "0.20", "at", table.Fixture2674Now().Add(-300 * time.Second).Format("2006-01-02T15:04:05Z")},
		}},
	}
}

// TestControl2674SprintLayout (DONE-WHEN of #2674): on the keyspace captured
// from the live fleet Redis, and on the degraded and no-Redis variants, Go's
// RenderLive prints the bytes the bash of record printed on the same keyspace,
// but for the progress line, the one count (#4411, table.MaskXY).
// The "bash" subtest re-runs the pinned bash itself against a real
// redis-server loaded with the same keys and requires bash == golden == Go.
func TestControl2674SprintLayout(t *testing.T) {
	t.Parallel()

	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	for _, c := range cases2674() {
		if c.golden == nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			snap := table.FailedLive(cfg, nil)
			if !c.noRedis {
				var err error
				client := liveStore(t, withCardViews(append(table.Fixture2674(), c.extra...), now))
				if snap, err = table.ReadLive(context.Background(), client, cfg); err != nil {
					t.Fatal(err)
				}
			}
			// every byte is the bash's but the progress line, the one count
			// (#4411): the fixture has no stream, so 0/0 with no eta, and
			// "?" when Redis never answered
			line := "0/0 done 0%, left 0, eta -"
			if c.noRedis {
				line = table.NeverCounted
			}
			got, want := snap.RenderLive(now), c.golden()
			if table.MaskXY(got) != table.MaskXY(want) || strings.Split(got, "\n")[2] != line {
				t.Fatalf("Go differs from the bash's %s (x/y masked; line 3 %q)\ngot:\n%s\nwant:\n%s", c.file, line, got, want)
			}
		})
	}
	t.Run("bash", bashParity2674)
}
