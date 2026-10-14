package update

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionMovedAndSnapshotDeclareDryRun pins both halves of the dry-run
// contract for nova-version's two writing verbs. Each sets Verb.DryRun, so
// pkg/tool knows the verb takes --dry-run and adds the flag itself (STANDARD
// ONBOARDING point "a verb that writes has a dry run"); and each reads
// Call.DryRun at entry, so a refusal under --dry-run keeps its own reason and
// never carries the skeleton's "it may have written" diagnostic for a flag the
// verb ignored (pkg/tool call: c.dryRead).
func TestVersionMovedAndSnapshotDeclareDryRun(t *testing.T) {
	t.Parallel()
	defined := map[string]bool{}
	for _, v := range VersionTool("", Environment{}).Verbs {
		defined[v.Name] = v.DryRun
	}
	for _, name := range []string{"moved", "snapshot"} {
		t.Run(name+" declares DryRun", func(t *testing.T) {
			t.Parallel()
			dry, ok := defined[name]
			require.True(t, ok, "the %s verb is missing from the tool definition", name)
			assert.True(t, dry, "%s must set Verb.DryRun so pkg/tool adds --dry-run itself", name)
		})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"snapshot", []string{"snapshot", "--file", filepath.Join(t.TempDir(), "absent.tsv"), "--dry-run"}},
		{"moved", []string{"moved", "--from", "a", "--to", "b", "--repo", t.TempDir(), "--out", "note.txt", "--dry-run"}},
	} {
		t.Run(c.name+" refusal keeps its reason", func(t *testing.T) {
			t.Parallel()
			code, out, errs := runTool(t, "nova-version", c.args...)
			require.Equal(t, 2, code, out+errs)
			assert.NotContains(t, out+errs, "may have written")
			assert.NotContains(t, out+errs, "never read it")
		})
	}
}
