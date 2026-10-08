package config

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopRunReadsTheRowsCommandAndCountsItsStarts(t *testing.T) {
	t.Parallel()
	argv, err := LoopRunArgv(Row{Name: "r", Fields: map[string]string{"argv": `["/bin/r","--once"]`, "enabled": "true"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/r", "--once"}, argv)
	for _, r := range []Row{
		{Name: "off", Fields: map[string]string{"argv": `["/bin/r"]`, "enabled": "false"}},
		{Name: "keyed", Fields: map[string]string{"argv": `["/bin/r"]`, "keys": "A_KEY", "seat": "s"}},
		{Name: "empty", Fields: map[string]string{}},
	} {
		_, err := LoopRunArgv(r)
		assert.True(t, errors.Is(err, ErrLoopNotRunnable), "%s: %v", r.Name, err)
	}
	assert.Equal(t, 1, NextLoopStarts(nil))
	assert.Equal(t, 1, NextLoopStarts([]byte("junk")))
	assert.Equal(t, 5, NextLoopStarts([]byte("4\n")))
	assert.Equal(t, "/run/a-b.lock", LoopLockPath("/run", "a-b"))
	assert.Equal(t, "/m/nova_loop_a_b.prom", LoopMetricsPath("/m", "a-b"))
	m := LoopMetrics("a-b", 3, time.Unix(42, 0))
	assert.Contains(t, m, "# TYPE nova_loop_starts_total counter\nnova_loop_starts_total{loop=\"a-b\"} 3\n")
	assert.Contains(t, m, "nova_loop_last_start_seconds{loop=\"a-b\"} 42\n")
}
