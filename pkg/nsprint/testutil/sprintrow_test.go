package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
)

// The sprint row Start applies is pkg/config's git_identity at its default: testutil
// spells the two strings itself (importing pkg/config would be a cycle through
// pkg/config's functional tests), so this pins them to config's.
func TestSprintRowIsConfigsGitIdentity(t *testing.T) {
	t.Parallel()
	assert.Equal(t, config.SprintKey(config.FieldGitIdentity), testutil.SprintRowKey)
	assert.Equal(t, config.DefaultGitIdentity, testutil.SprintRowValue)
}
