//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/stretchr/testify/require"
)

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
