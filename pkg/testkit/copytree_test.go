package testkit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyTreeCopiesNestedFilesAndExecuteBits(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	testkit.WriteFile(t, filepath.Join(src, "a.md"), "a\n")
	testkit.WriteFile(t, filepath.Join(src, "sub", "b.sh"), "#!/bin/sh\n")
	require.NoError(t, os.Chmod(filepath.Join(src, "sub", "b.sh"), 0o755))

	dst := filepath.Join(t.TempDir(), "out")
	testkit.CopyTree(t, src, dst)

	assert.Equal(t, "a\n", testkit.ReadFile(t, filepath.Join(dst, "a.md")))
	assert.Equal(t, "#!/bin/sh\n", testkit.ReadFile(t, filepath.Join(dst, "sub", "b.sh")))
	info, err := os.Stat(filepath.Join(dst, "sub", "b.sh"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the execute bit did not survive: %v", info.Mode())
}
