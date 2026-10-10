package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/bench"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBench is a Transport with no wire: each host answers or not, makes the
// run directory it is told to, and the command prints and exits as told. It
// records every call in order.
type fakeBench struct {
	down  map[string]bool // hosts whose ssh fails (exit 255)
	made  string          // what mktemp prints; default <root>/run.FAKE0001
	code  int             // the command's exit status
	calls []string
}

func (f *fakeBench) Shell(_ context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	f.calls = append(f.calls, "shell "+host+" "+line)
	if f.down[host] {
		fmt.Fprintln(stderr, "ssh: connect to host "+host+" port 22: Connection timed out")
		return bench.NoAnswer, nil
	}
	switch {
	case strings.HasPrefix(line, "mkdir -p "):
		made := f.made
		if made == "" {
			root, _, _ := strings.Cut(strings.TrimPrefix(line, "mkdir -p '"), "'")
			made = root + "/run.FAKE0001"
		}
		fmt.Fprintln(stdout, made)
		return 0, nil
	case strings.HasPrefix(line, "cd "):
		fmt.Fprintln(stdout, "ok  \tgithub.com/x/y\t0.1s")
		fmt.Fprintln(stderr, "a line on stderr")
		return f.code, nil
	}
	return 0, nil
}

func (f *fakeBench) Copy(_ context.Context, host, src, dst string, withGit bool, _ io.Writer) error {
	f.calls = append(f.calls, fmt.Sprintf("copy %s %s -> %s with-git=%v", host, src, dst, withGit))
	return nil
}

func benchRun(t *testing.T, f *fakeBench, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmdBench(context.Background(), append([]string{"run"}, args...), nil, &stdout, &stderr, f)
	return code, stdout.String(), stderr.String()
}

// The verb copies the tree, runs the command in the copy under nice with the
// bench's cache and the standard environment, streams its output, removes
// exactly the directory it made, and exits with the command's status; an
// unanswering host falls back.
func TestBenchRunCopiesRunsAndCleansUp(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()

	t.Run("copy, run, remove", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{}
		code, stdout, stderr := benchRun(t, f, "--host", "vision", "--dir", tree, "--", "go", "test", "-count=1", "./cmd/nova-ci/")
		require.Equal(t, 0, code, "stderr %s", stderr)
		assert.Equal(t, []string{
			"shell vision mkdir -p 'nova-bench/runs' && mktemp -d 'nova-bench/runs/run.XXXXXXXX'",
			"copy vision " + tree + " -> nova-bench/runs/run.FAKE0001/repo with-git=false",
			`shell vision cd 'nova-bench/runs/run.FAKE0001/repo' && GOCACHE="$HOME"/'nova-bench/cache/go-build' GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 nice -n 19 'go' 'test' '-count=1' './cmd/nova-ci/'`,
			"shell vision rm -rf -- 'nova-bench/runs/run.FAKE0001'",
		}, f.calls)
		assert.Equal(t, "ok  \tgithub.com/x/y\t0.1s\n", stdout, "stdout is exactly the command's")
		assert.Contains(t, stderr, "a line on stderr\n")
		assert.True(t, strings.HasSuffix(stderr, "CI BENCH host=vision run=nova-bench/runs/run.FAKE0001 exit=0 removed=yes\n"), stderr)
	})

	t.Run("the command's exit status is the verb's", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{code: 1}
		code, _, stderr := benchRun(t, f, "--host", "vision", "--dir", tree, "--", "go", "vet", "./...")
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "exit=1 removed=yes")
		assert.Equal(t, "shell vision rm -rf -- 'nova-bench/runs/run.FAKE0001'", f.calls[len(f.calls)-1], "a red command still cleans up")
	})

	t.Run("an unanswering host falls back", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{down: map[string]bool{"vision": true}}
		code, _, stderr := benchRun(t, f, "--host", "vision", "--fallback", "hetzner", "--dir", tree, "--with-git", "--root", "/srv/runs", "--cache", "/srv/cache", "--", "go", "version")
		require.Equal(t, 0, code, "stderr %s", stderr)
		require.Len(t, f.calls, 5)
		assert.Equal(t, "shell vision mkdir -p 'nova-bench/runs' && mktemp -d 'nova-bench/runs/run.XXXXXXXX'"[:len("shell vision mkdir")], f.calls[0][:len("shell vision mkdir")])
		assert.Equal(t, "shell hetzner mkdir -p '/srv/runs' && mktemp -d '/srv/runs/run.XXXXXXXX'", f.calls[1])
		assert.Contains(t, f.calls[2], "copy hetzner ")
		assert.Contains(t, f.calls[2], "with-git=true")
		assert.Contains(t, f.calls[3], "GOCACHE='/srv/cache' ")
		for _, c := range f.calls[1:] {
			assert.NotContains(t, c, "shell vision", "nothing more is asked of the host that did not answer")
		}
		assert.Contains(t, stderr, "CI BENCH PASSED host=vision ")
		assert.Contains(t, stderr, "next=hetzner")
		assert.Contains(t, stderr, "CI BENCH host=hetzner ")
	})

	t.Run("no host answering is a refusal and nothing is removed", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{down: map[string]bool{"vision": true, "hetzner": true}}
		code, stdout, stderr := benchRun(t, f, "--host", "vision", "--fallback", "hetzner", "--dir", tree, "--", "go", "version")
		assert.Equal(t, 2, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "BENCH-RUN REFUSED: no bench answered: vision (")
		for _, c := range f.calls {
			assert.NotContains(t, c, "rm -rf", "a run that made nothing removes nothing")
		}
	})

	t.Run("a mktemp answer that is not a run directory is never removed", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{made: "nova-bench"}
		code, _, stderr := benchRun(t, f, "--host", "vision", "--dir", tree, "--", "go", "version")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "not a run directory")
		require.Len(t, f.calls, 1)
	})
}

func TestBenchRunRefusesUsage(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--dir", tree, "--", "go", "version"}, "--host is required"},
		{[]string{"--host", "vision", "--", "go", "version"}, "--dir is required"},
		{[]string{"--host", "vision", "--dir", tree}, "no command after --"},
		{[]string{"--host", "-oProxyCommand=x", "--dir", tree, "--", "go", "version"}, "is not a host name"},
		{[]string{"--host", "vision", "--fallback", "vision", "--dir", tree, "--", "go"}, "same bench"},
		{[]string{"--host", "vision", "--dir", tree, "--root", "~/runs", "--", "go"}, "no ~"},
		{[]string{"--host", "vision", "--dir", tree, "--root", "runs/../..", "--", "go"}, "climbs"},
		{[]string{"--host", "vision", "--dir", tree, "--root", "/", "--", "go"}, "the home or the root itself"},
		{[]string{"--host", "vision", "--dir", tree + "/missing", "--", "go"}, "is not a directory here"},
	} {
		f := &fakeBench{}
		code, _, stderr := benchRun(t, f, tc.args...)
		assert.Equal(t, 2, code, "%v", tc.args)
		assert.Contains(t, stderr, "BENCH-RUN REFUSED: ")
		assert.Contains(t, stderr, tc.want, "%v", tc.args)
		assert.Empty(t, f.calls, "a refused call reaches no bench: %v", tc.args)
	}
	var stderr bytes.Buffer
	assert.Equal(t, 2, cmdBench(context.Background(), []string{"walk"}, nil, io.Discard, &stderr, &fakeBench{}))
	assert.Contains(t, stderr.String(), `unknown verb "bench walk"`)
	stderr.Reset()
	assert.Equal(t, 2, cmdBench(context.Background(), []string{"run", "--host", "vision", "--dir", tree, "go", "version"}, nil, io.Discard, &stderr, &fakeBench{}))
	assert.Contains(t, stderr.String(), "takes no positional arguments", "the command comes after --, never in among the flags")
}

func TestBenchRunAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, ciRun, []testverbhelp.Case{
		{Verb: "bench run", Flags: []string{"--host", "vision", "--dir", "{dir}"}},
	})
	code, stdout, _ := runCI(t, []string{"bench", "run", "-h"}, "")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "--fallback")
	assert.Contains(t, stdout, "--with-git")
	assert.Contains(t, stdout, "bench run: the command's own exit status")
	assert.Contains(t, stdout, "effect: delivery: ")
}

// The bench tool meets the standard its banner and help carry (tool.Problems).
func TestBenchToolHasNoProblems(t *testing.T) {
	t.Parallel()
	assert.Empty(t, benchTool(&fakeBench{}, nil).Problems())
}
