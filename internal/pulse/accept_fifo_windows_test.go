//go:build windows

package pulse

import "testing"

func mkfifo(t *testing.T, _ string) {
	t.Helper()
	t.Skip("windows has no fifo the gate could hang on; the untracked-file test covers the worker's copy there")
}
