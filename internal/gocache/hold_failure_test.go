package gocache

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHoldReportsMeasurementFailures pins the no-silent-failure contract for Hold
// (docs/STANDARD.md, the silent rule; SPEC-CI.md, `silent`): a shard ReadDir error found in
// Hold's first, measurement Round must not be swallowed by the second, trim Round, whose
// `t.failed[sub]` guard suppresses re-reporting the same failure. Hold must carry the failure
// count and the first diagnostic across both phases, while leaving Dry and trim behavior of a
// clean cache unchanged.
func TestHoldReportsMeasurementFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	base := Bounds{Limit: 10 << 10, Slack: 2 << 10, Remove: 1000}

	cases := []struct {
		name       string
		dry        bool
		malformed  int
		clean      bool
		wantFailed int
		wantSize   int64
	}{
		{name: "one malformed shard dry", dry: true, malformed: 1, wantFailed: 1},
		{name: "one malformed shard", dry: false, malformed: 1, wantFailed: 1},
		{name: "two malformed shards dry", dry: true, malformed: 2, wantFailed: 2},
		{name: "two malformed shards", dry: false, malformed: 2, wantFailed: 2},
		{name: "clean cache dry", dry: true, clean: true, wantFailed: 0, wantSize: 5 << 10},
		{name: "clean cache", dry: false, clean: true, wantFailed: 0, wantSize: 5 << 10},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "go-build")
			require.NoError(t, os.MkdirAll(dir, 0o755))

			var malformed []string
			for i := 0; i < tc.malformed; i++ {
				shard := filepath.Join(dir, fmt.Sprintf("%02x", i))
				require.NoError(t, os.WriteFile(shard, []byte("not a shard\n"), 0o644))
				malformed = append(malformed, shard)
			}
			if tc.clean {
				for i := range 5 {
					cacheEntry(t, dir, "c"+strconv.Itoa(i), 1024, now.Add(-1000*time.Hour))
				}
			}

			b := base
			b.Dry = tc.dry
			c := Hold(dir, now, b)

			assert.Equal(t, tc.wantFailed, c.Failed, "failed count")
			if tc.malformed > 0 {
				first := filepath.Join(dir, "00")
				assert.True(t, strings.HasPrefix(c.Why, first+": "),
					"why names the first malformed shard; got %q want prefix %q", c.Why, first+": ")
			} else {
				assert.Empty(t, c.Why, "clean cache has no diagnostic")
			}
			assert.Zero(t, c.Removed, "no successful removal claimed for failed measurements")
			assert.Zero(t, c.Freed, "no successful freeing claimed for failed measurements")
			if tc.wantSize != 0 {
				assert.Equal(t, tc.wantSize, c.Size, "final measured size")
			}

			for _, shard := range malformed {
				data, err := os.ReadFile(shard)
				require.NoError(t, err)
				assert.Equal(t, "not a shard\n", string(data), "malformed shard file survives untouched")
			}
		})
	}
}
