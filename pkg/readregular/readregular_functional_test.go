//go:build functional

package readregular

import (
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadNonRegularFileSocket(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("unix domain sockets on windows not guaranteed")
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	defer func() { _ = l.Close() }() // ignored: the test listener is done with

	_, err = Read(sockPath, DefaultMax)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not a regular file"), "got error %v, want 'not a regular file'", err)
}
