package bench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTransport struct {
	lines   []string
	shellFn func(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error)
	copyFn  func(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error
}

func (m *mockTransport) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	m.lines = append(m.lines, line)
	if m.shellFn != nil {
		return m.shellFn(ctx, host, line, stdout, stderr)
	}
	return 0, nil
}

func (m *mockTransport) Copy(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error {
	m.lines = append(m.lines, fmt.Sprintf("COPY %s -> %s", src, dst))
	if m.copyFn != nil {
		return m.copyFn(ctx, host, src, dst, withGit, stderr)
	}
	return nil
}

func TestCommandCarriesThreeEnvPathsInsideRunDirectory(t *testing.T) {
	t.Parallel()

	runDir := "/home/ubuntu/nova-bench/runs/gate-test123"
	line := ExecLine(runDir, "", []string{"go", "test", "-v", "./..."})

	assert.Contains(t, line, "cd '"+runDir+"/tree'")
	assert.Contains(t, line, "TMPDIR='"+runDir+"/tmp'")
	assert.Contains(t, line, "GOTMPDIR='"+runDir+"/tmp'")
	assert.Contains(t, line, "GOCACHE='"+runDir+"/gocache'")
	assert.Contains(t, line, "GOFLAGS=-mod=readonly")
	assert.Contains(t, line, "NOVA_TEST_NO_HOST=1")
	assert.Contains(t, line, "nice -n 19 'go' 'test' '-v' './...'")

	// Relative directory prefixes paths with "$HOME"/
	relDir := "nova-bench/runs/read-card456"
	relLine := ExecLine(relDir, "", []string{"go", "build"})
	assert.Contains(t, relLine, `TMPDIR="$HOME"/'`+relDir+`/tmp'`)
	assert.Contains(t, relLine, `GOTMPDIR="$HOME"/'`+relDir+`/tmp'`)
	assert.Contains(t, relLine, `GOCACHE="$HOME"/'`+relDir+`/gocache'`)
	assert.Contains(t, relLine, `cd "$HOME"/'`+relDir+`/tree'`)
}

func TestRunDirectoryRemovedOnOkFailingTestTimeoutAndCancel(t *testing.T) {
	t.Parallel()

	root := "/home/ubuntu/nova-bench"
	kind := "gate"
	id := "testrun"
	expectedDir := RunPath(root, kind, id)

	baseOpts := func() Options {
		return Options{
			Hosts: []string{"bench1"},
			Root:  root,
			Kind:  kind,
			ID:    id,
			Dir:   "/land/repo",
			Argv:  []string{"go", "test"},
		}
	}

	t.Run("on ok", func(t *testing.T) {
		t.Parallel()
		tr := &mockTransport{
			shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
				if strings.HasPrefix(line, "mkdir -p ") {
					_, _ = io.WriteString(stdout, expectedDir+"\n")
					return 0, nil
				}
				return 0, nil
			},
		}

		res, err := Run(context.Background(), tr, baseOpts())
		require.NoError(t, err)
		assert.Equal(t, 0, res.Code)
		assert.True(t, res.Removed)
		assert.Contains(t, tr.lines, RemoveLine(expectedDir))
	})

	t.Run("on a failing test", func(t *testing.T) {
		t.Parallel()
		tr := &mockTransport{
			shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
				if strings.HasPrefix(line, "mkdir -p ") {
					_, _ = io.WriteString(stdout, expectedDir+"\n")
					return 0, nil
				}
				if strings.HasPrefix(line, "cd ") {
					return 1, nil // test failed with exit code 1
				}
				return 0, nil
			},
		}

		res, err := Run(context.Background(), tr, baseOpts())
		require.NoError(t, err)
		assert.Equal(t, 1, res.Code)
		assert.True(t, res.Removed)
		assert.Contains(t, tr.lines, RemoveLine(expectedDir))
	})

	t.Run("on a timeout", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		tr := &mockTransport{
			shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
				if strings.HasPrefix(line, "mkdir -p ") {
					_, _ = io.WriteString(stdout, expectedDir+"\n")
					return 0, nil
				}
				if strings.HasPrefix(line, "cd ") {
					cancel()
					return -1, context.DeadlineExceeded
				}
				return 0, nil
			},
		}

		res, err := Run(ctx, tr, baseOpts())
		assert.Error(t, err)
		assert.True(t, res.Removed)
		assert.Contains(t, tr.lines, RemoveLine(expectedDir))
	})

	t.Run("on cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())

		tr := &mockTransport{
			shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
				if strings.HasPrefix(line, "mkdir -p ") {
					_, _ = io.WriteString(stdout, expectedDir+"\n")
					return 0, nil
				}
				if strings.HasPrefix(line, "cd ") {
					cancel() // cancel parent context during execution
					return -1, context.Canceled
				}
				return 0, nil
			},
		}

		res, err := Run(ctx, tr, baseOpts())
		assert.Error(t, err)
		assert.True(t, res.Removed)
		assert.Contains(t, tr.lines, RemoveLine(expectedDir))
	})
}

func TestRunRejectsPathSeparatorsInKindAndID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		kind, id string
	}{
		{name: "kind", kind: "gate/../escape", id: "safe"},
		{name: "id", kind: "gate", id: "../escape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := Options{Hosts: []string{"bench1"}, Root: "/bench", Kind: tc.kind, ID: tc.id, Dir: "/repo", Argv: []string{"go", "test"}}
			err := o.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "run "+tc.name)
		})
	}
}

func TestSweepRemovesOnlyRunsOlderThanBoundWithNoLiveProcess(t *testing.T) {
	t.Parallel()

	root := "/home/ubuntu/nova-bench"
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	bound := 2 * time.Hour

	dirOldDead := root + "/runs/gate-old-dead"
	dirOldLive := root + "/runs/gate-old-live"
	dirYoung := root + "/runs/gate-young"

	// Old dead: 3 hours old
	mtimeOldDead := now.Add(-3 * time.Hour).Unix()
	// Old live: 3 hours old
	mtimeOldLive := now.Add(-3 * time.Hour).Unix()
	// Young: 30 minutes old
	mtimeYoung := now.Add(-30 * time.Minute).Unix()

	statOutput := fmt.Sprintf("%d %s\n%d %s\n%d %s\n",
		mtimeOldDead, dirOldDead,
		mtimeOldLive, dirOldLive,
		mtimeYoung, dirYoung,
	)

	tr := &mockTransport{
		shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
			if strings.HasPrefix(line, "stat -c ") {
				_, _ = io.WriteString(stdout, statOutput)
				return 0, nil
			}
			if strings.HasPrefix(line, "fuser ") {
				if strings.Contains(line, dirOldLive) {
					_, _ = io.WriteString(stdout, "1234\n") // live process PID
					return 0, nil
				}
				// No process for old dead
				return 1, nil
			}
			if strings.HasPrefix(line, "rm -rf ") {
				return 0, nil
			}
			return 0, nil
		},
	}

	var buf bytes.Buffer
	removed, err := SweepRuns(context.Background(), tr, "bench1", root, bound, now, &buf)
	require.NoError(t, err)

	assert.Equal(t, []string{dirOldDead}, removed)
	assert.Equal(t, "REMOVED "+dirOldDead+"\n", buf.String())

	assert.Contains(t, tr.lines, RemoveLine(dirOldDead))
	assert.NotContains(t, tr.lines, RemoveLine(dirOldLive))
	assert.NotContains(t, tr.lines, RemoveLine(dirYoung))
}

func TestBenchUnderTheFloorTakesNoRunAndSaysSo(t *testing.T) {
	t.Parallel()

	root := "/home/ubuntu/nova-bench"
	// df output showing ~4.76 GiB available (5,000,000 1K-blocks)
	dfUnderFloor := "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100000000 95000000 5000000 95% /\n"

	tr := &mockTransport{
		shellFn: func(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
			if strings.HasPrefix(line, "df -Pk ") {
				_, _ = io.WriteString(stdout, dfUnderFloor)
				return 0, nil
			}
			return 0, nil
		},
	}

	opts := Options{
		Hosts:   []string{"bench1"},
		Root:    root,
		Kind:    "gate",
		ID:      "test123",
		FloorGB: 10,
		Dir:     "/land/repo",
		Argv:    []string{"go", "test"},
	}

	res, err := Run(context.Background(), tr, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "under the floor of 10 GiB")
	assert.Contains(t, err.Error(), "disk 4.8 GiB")
	assert.Contains(t, err.Error(), "red under the floor")

	// Takes no run: make line was never run, command was never run
	for _, l := range tr.lines {
		assert.False(t, strings.HasPrefix(l, "mkdir -p "), "MakeLine should not run when under floor: %s", l)
		assert.False(t, strings.HasPrefix(l, "cd "), "ExecLine should not run when under floor: %s", l)
		assert.False(t, strings.HasPrefix(l, "COPY "), "Copy should not run when under floor: %s", l)
	}
	assert.Empty(t, res.RunDir)
}
