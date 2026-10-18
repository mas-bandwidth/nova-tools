package workfile_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAWrongTreeVersionIsRefusedByItsVersion pins the reader's answer to a
// file of a version it does not read: the version is named, never one of
// another version's missing keys (SPEC-WORK-V1 section 1.2).
func TestAWrongTreeVersionIsRefusedByItsVersion(t *testing.T) {
	t.Parallel()
	const wrong = `(work-tree "v2")`
	_, err := workfile.Decode("t.lisp", []byte(wrong), workfile.Limits(len(wrong)))
	require.Error(t, err, "a v2 tree was read by a v1 reader")
	assert.Contains(t, err.Error(), "version v2 is not read; this build reads v1", "err=%v", err)
	assert.NotContains(t, err.Error(), "has no :source", "the wrong version is answered with a missing key: %v", err)
}

// TestScopeIsAnOptionalKeyUnderThePresentFormat pins the decision the :scope
// key needed (SPEC-WORK-V1 section 1.2): :scope is optional under v1, not a
// new format, so a v1 file with it reads and a v1 file without it is a
// whole-organization tree.
func TestScopeIsAnOptionalKeyUnderThePresentFormat(t *testing.T) {
	t.Parallel()
	const scoped = `(work-tree "v1" :source "github" :org "acme" :fetched "2026-10-02T12:00:00Z"
 :scope ("acme/widgets")
 :repos ((repo "acme/widgets" :url "https://github.com/acme/widgets" :archived false :issues ())))`
	tree, err := workfile.Decode("t.lisp", []byte(scoped), workfile.Limits(len(scoped)+1))
	require.NoError(t, err, "a v1 tree with :scope was refused")
	assert.Equal(t, []string{"acme/widgets"}, tree.Scope)
}
