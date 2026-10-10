//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/stretchr/testify/require"
)

// quotedSocket is the proof's unix socket, named so the reply command must
// quote it. It stays under the test directory when that path fits a Unix
// socket, and otherwise in a short private directory, as twinSocket does when
// a long TMPDIR cannot hold one. The directory the fallback makes is the
// caller's to remove.
func quotedSocket(t *testing.T) string {
	t.Helper()
	sock, err := testredis.SocketPath(t.TempDir(), "emma's canary.sock")
	require.NoError(t, err)
	if strings.HasPrefix(sock, os.TempDir()) || strings.HasPrefix(sock, "/tmp") {
		dir := filepath.Dir(sock)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	return sock
}
