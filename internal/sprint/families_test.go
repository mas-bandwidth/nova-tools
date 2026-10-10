package sprint_test

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
)

// The sprint's own keys are the redisacl family "sprint": the store's ACL roles reach
// them by that family's patterns (it was a row of pkg/redisacl's
// TestFamiliesAreTheOwnersKeys).
func TestTheSprintsKeysAreItsRedisACLFamily(t *testing.T) {
	t.Parallel()
	var patterns []string
	for _, f := range redisacl.Families {
		if f.Name == "sprint" {
			patterns = f.Patterns
		}
	}
	names := sprint.Names{}
	for _, k := range []string{names.EpochKey(), names.Key("beat:bench-a"), names.KeyAt("tick", 2)} {
		matched := false
		for _, p := range patterns {
			if ok, _ := path.Match(p, k); ok {
				matched = true
			}
		}
		assert.True(t, matched, "sprint: %s", k)
	}
}
