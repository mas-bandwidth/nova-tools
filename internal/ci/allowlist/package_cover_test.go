// Unit coverage for the package-ledger entry points the per-function table
// showed at zero: CheckPackagesCounted, and the silentPackageReporter it
// substitutes under UPDATE (Helper, Errorf). Everything runs in process over
// plain files in t.TempDir(): no sleeps, no real time, no network, no
// subprocess, no Redis or Postgres.
package allowlist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPackageCoverCheckPackagesCounted drives CheckPackagesCounted over the
// package's own recorder and shard fixture. The rows pin the main path (a
// matching ledger reports nothing), a reported path (an unlisted measured key
// comes back for the caller), and a refusal (a negative measured count).
func TestPackageCoverCheckPackagesCounted(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name         string
		measured     map[string]int
		wantUnlisted []string
		wantRefusal  string
	}{
		{
			name:     "the main path accepts a matching ledger and reports nothing",
			measured: map[string]int{"a/a.go:f:blank": 2},
		},
		{
			name:         "an unlisted measured key comes back for the caller",
			measured:     map[string]int{"a/a.go:f:blank": 2, "a/new.go:g:blank": 1},
			wantUnlisted: []string{"a/new.go:g:blank"},
		},
		{
			name:        "a negative measured count is refused",
			measured:    map[string]int{"a/a.go:f:blank": -1},
			wantRefusal: "negative site count",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 2 known\n")
			var r recorder
			res := CheckPackagesCounted(&r, loadPackageFixture(t, dir), row.measured)

			assert.Equal(t, row.wantUnlisted, res.Unlisted, "unlisted keys")
			assert.False(t, res.Updated, "CheckPackagesCounted without UPDATE must not write")
			if row.wantRefusal == "" {
				assert.Empty(t, r.lines, "the main path prints nothing")
			} else {
				assert.Equal(t, 1, r.count(row.wantRefusal), "refusal lines: %q", r.lines)
			}
		})
	}
}

// TestPackageCoverCheckPackagesCountedNilLedger pins the entry point's first
// refusal: a nil *Packages is named before anything else is read.
func TestPackageCoverCheckPackagesCountedNilLedger(t *testing.T) {
	t.Parallel()

	var r recorder
	res := CheckPackagesCounted(&r, nil, map[string]int{"a/a.go:f:blank": 1})

	assert.Zero(t, res)
	assert.Equal(t, 1, r.count("package ledger is nil"))
}

// TestPackageCoverCheckPackagesCountedModeUsesSilentReporter drives the update
// path that replaces the caller's Reporter with silentPackageReporter: a
// lowered count is written, the caller sees only the one UpdatedRerun line,
// and the silent reporter's Helper is exercised through CheckCountedMode.
func TestPackageCoverCheckPackagesCountedModeUsesSilentReporter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 3 known\n")
	var r recorder
	res := CheckPackagesCountedMode(&r, loadPackageFixture(t, dir), map[string]int{"a/a.go:f:blank": 2}, true)

	require.True(t, res.Updated)
	assert.Equal(t, 1, r.count(UpdatedRerun))
	assert.Equal(t, "# ceiling: 1\na/a.go:f:blank 2 known\n", readBack(t, file))
}

// TestPackageCoverSilentReporterIsSilent exercises both methods of
// silentPackageReporter, the Reporter CheckPackagesCountedMode substitutes
// under UPDATE so a per-shard diagnostic does not surface before the
// whole-ledger preflight finishes.
func TestPackageCoverSilentReporterIsSilent(t *testing.T) {
	t.Parallel()

	var r Reporter = silentPackageReporter{}
	r.Helper()
	r.Errorf("%s", "swallowed")
}
