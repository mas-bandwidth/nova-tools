package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// timelineFile is a job's hand-written timeline: the columns the native run writes, with
// known phase durations so the profile line's arithmetic is exact.
func timelineFile(t *testing.T, dir string, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, swarm.TimelineFileName), []byte(body), 0o644))
}

// TestProfilePrintsPhases: `nova-worker profile --jobs <glob>` prints one PROFILE line per
// job and one summary line, with every phase inferred from the tool call's command and its
// seconds, including a test run after a failing test run counted as retry.
func TestProfilePrintsPhases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// job-a: clone, read, edit, a failing test, a passing test (retry), result and deps.
	timelineFile(t, filepath.Join(root, "job-a"),
		"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
			"2026-09-17T00:00:00Z\t2026-09-17T00:00:10Z\tmodel\t10000\t100\t20\n"+
			"2026-09-17T00:00:10Z\t2026-09-17T00:00:35Z\tbash git clone https://github.com/x/y\t25000\t\t\n"+
			"2026-09-17T00:00:35Z\t2026-09-17T00:00:40Z\tRead internal/swarm/usage.go\t5000\t\t\n"+
			"2026-09-17T00:00:40Z\t2026-09-17T00:00:45Z\tEdit internal/swarm/usage.go\t5000\t\t\n"+
			"2026-09-17T00:00:45Z\t2026-09-17T00:00:55Z\tbash go test ./... rc=1\t10000\t\t\n"+
			"2026-09-17T00:00:55Z\t2026-09-17T00:01:02Z\tbash go test ./... rc=0\t7000\t\t\n"+
			"2026-09-17T00:01:02Z\t2026-09-17T00:01:05Z\tedit Write RESULT.md\t3000\t\t\n"+
			"2026-09-17T00:01:05Z\t2026-09-17T00:01:09Z\tbash go mod tidy\t4000\t\t\n")
	// job-b: one clone only.
	timelineFile(t, filepath.Join(root, "job-b"),
		"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
			"2026-09-17T01:00:00Z\t2026-09-17T01:00:20Z\tbash git fetch origin\t20000\t\t\n")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"profile", "--jobs", filepath.Join(root, "job-*")},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "profile exits 0, got %d:\n%s", rc, stderr.String())
	out := stdout.String()
	for _, want := range []string{
		"PROFILE job=job-a", "turns=1", "tools=7",
		"clone=25.0", "deps=4.0", "read=5.0", "edit=5.0", "test=10.0", "retry=7.0", "result=3.0",
		"PROFILE job=job-b",
		"PROFILE SUMMARY jobs=2",
	} {
		assert.Contains(t, out, want, "profile output does not name %q:\n%s", want, out)
	}
}
