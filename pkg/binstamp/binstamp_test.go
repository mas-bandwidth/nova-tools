package binstamp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheStampChangesWhenTheFileIsReplacedAndIsEmptyWhenItIsGone(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "bin")
	assert.Equal(t, "", Of(p), "a file that is not there has no stamp")
	require.NoError(t, os.WriteFile(p, []byte("one"), 0o755))
	first := Of(p)
	assert.Contains(t, first, p+" 3 ")
	assert.Equal(t, first, Of(p), "an untouched file keeps its stamp")
	require.NoError(t, os.WriteFile(p, []byte("one, longer"), 0o755))
	assert.NotEqual(t, first, Of(p))
}
