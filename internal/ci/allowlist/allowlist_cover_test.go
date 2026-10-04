// Unit coverage for the two entry points the unit tier's per-function table
// showed at zero: Updating and Check. Everything runs in-process over plain
// files in t.TempDir() through the package's own recorder: no sleeps, no real
// time, no network, no subprocess, no Redis or Postgres.
package allowlist

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllowlistCoverUpdatingReadsTheEnvironment reaches Updating, the one
// reader of NOVA_CI_UPDATE. Updating has no per-test seam: it calls os.Getenv
// directly, and a parallel test may not change the process environment. This
// pins its main path -- the variable unset, no update -- while
// TestUpdateNeedsTheValueOne pins every value through updatingFrom, the seam
// the "1" branch belongs to.
func TestAllowlistCoverUpdatingReadsTheEnvironment(t *testing.T) {
	t.Parallel()

	assert.False(t, Updating(), "with %s unset, Updating must be false", UpdateEnv)
	assert.Equal(t, updatingFrom(os.Getenv), Updating(), "Updating must read the process environment through updatingFrom")
}

// TestAllowlistCoverCheckReportsAndRefuses reaches Check on its two paths: the
// main one, where the stale rows and unlisted keys come back and nothing is
// written, and the refusal, a list over its ceiling reported in the same call.
func TestAllowlistCoverCheckReportsAndRefuses(t *testing.T) {
	t.Parallel()

	const list = "# header\na.go:f  # reason a\nb.go:g  # reason b\n"

	rows := []struct {
		name         string
		text         string
		opt          Options
		measured     map[string]bool
		wantStale    []string
		wantUnlisted []string
		wantRefusal  string
	}{
		{
			name:         "the main path reports the stale row and the unlisted key and writes nothing",
			text:         list,
			opt:          Options{},
			measured:     set("a.go:f", "d.go:new"),
			wantStale:    []string{"b.go:g"},
			wantUnlisted: []string{"d.go:new"},
		},
		{
			name:        "a list over its ceiling is refused in the same call",
			text:        "# ceiling: 1\na\nb\n",
			opt:         Options{Ceiling: true},
			measured:    set("a", "b"),
			wantRefusal: "over its ceiling of 1",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			path := writeList(t, row.text)
			var r recorder
			res := Check(&r, load(t, path, row.opt), row.measured)

			var stale []string
			for _, s := range res.Stale {
				stale = append(stale, s.Key)
			}
			assert.Equal(t, row.wantStale, stale, "stale rows")
			assert.Equal(t, row.wantUnlisted, res.Unlisted, "unlisted keys")
			assert.False(t, res.Updated, "Check outside an update must not write")
			assert.Equal(t, row.text, readBack(t, path), "Check outside an update must not write")
			if row.wantRefusal == "" {
				assert.Empty(t, r.lines, "the main path prints nothing")
			} else {
				assert.Equal(t, 1, r.count(row.wantRefusal), "refusal lines: %q", r.lines)
			}
		})
	}
}
