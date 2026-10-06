//go:build slow || functional

package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// awaitLease answers the lease file's bytes as soon as there are any. The deadline only
// bounds a failure; nothing here waits on a chosen duration when the file is already there.
func awaitLease(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
		require.False(t, time.Now().After(deadline), "no lease appeared at %s: the first run never took the job directory", path)
	}
}
