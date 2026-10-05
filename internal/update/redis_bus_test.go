package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportSendUsesTheRedisBus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := filepath.Join(dir, "versions.tsv")
	require.NoError(t, os.WriteFile(manifest, []byte(Header+"\nx\ttool\t1.2.3\tnpm:unused\tnone\towner\n"), 0o600))
	checks := filepath.Join(dir, "checks.tsv")
	require.NoError(t, os.WriteFile(checks, []byte(AdoptHeader+"\nversions\t/nonexistent/check\towner\n"), 0o600))

	var mu sync.Mutex
	var argv [][]string
	env := Environment{
		Now: func() time.Time { return time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC) },
		Process: func(_ context.Context, args []string, _ io.Reader, _ int) ProcessResult {
			mu.Lock()
			argv = append(argv, append([]string{}, args...))
			mu.Unlock()
			return ProcessResult{Stdout: "SEND OK id=01ABC to=x cc=- at=2026-10-04T17:00:00Z\n"}
		},
	}
	assertRedisSend := func(t *testing.T, out, errs string) {
		t.Helper()
		mu.Lock()
		got := append([][]string{}, argv...)
		mu.Unlock()
		require.Len(t, got, 1, "child argv: %v", got)
		line := strings.Join(got[0], " ")
		require.True(t, strings.HasPrefix(line, "nova-bus send --as "), "first argv is %q", line)
		assert.Contains(t, line, " --to ")
		assert.Contains(t, line, " --subject ")
		assert.Contains(t, line, " --stdin")
		assert.NotContains(t, line, "prepare")
		assert.Contains(t, out+errs, "01ABC")
	}

	code, out, errs := run(t, env, "report", "--file", manifest, "--send", "--as", "me", "--to", "x")
	require.Equal(t, 0, code, "%s%s", out, errs)
	assertRedisSend(t, out, errs)

	mu.Lock()
	argv = nil
	mu.Unlock()
	code, out, errs = run(t, env, "watch", "--adopt", checks, "--as", "me", "--to", "x")
	require.NotEqual(t, 2, code, "%s%s", out, errs)
	assertRedisSend(t, out, errs)
}
