package release

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// edgesDoneContext is a context cancelled before the call. os/exec refuses an
// already-done context in Start, before os.StartProcess, so each edge function
// below reaches its production body and its refusal without a child process.
func edgesDoneContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// edgesFakeProgram writes an executable into t.TempDir() and returns its path.
// It is the value of the package's own SSH seam, ExecSSH.Path. testguard reads a
// program under a temp root as a test's fake rather than the fleet, so the host
// guard passes and the cancelled context above is what refuses the child.
func edgesFakeProgram(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ssh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

// TestEdgesCoverCommandsRefuseADoneContext pins the one exec site and its two
// policy wrappers: each returns the context's error, no output and no ceiling
// hit, so a caller can tell a refused child from an overflowing one.
func TestEdgesCoverCommandsRefuseADoneContext(t *testing.T) {
	t.Parallel()
	ctx := edgesDoneContext(t)
	t.Run("runCommand", func(t *testing.T) {
		t.Parallel()
		out, err := runCommand(ctx, "git", "status")
		require.Error(t, err)
		assert.Empty(t, out)
	})
	t.Run("runCommandInput", func(t *testing.T) {
		t.Parallel()
		out, err := runCommandInput(ctx, strings.NewReader("input"), "", "git", "status")
		require.Error(t, err)
		assert.Empty(t, out)
	})
	t.Run("runCommandCapped", func(t *testing.T) {
		t.Parallel()
		out, hit, err := runCommandCapped(ctx, childCap, nil, "", "git", "status")
		require.Error(t, err)
		assert.False(t, hit)
		assert.Empty(t, out)
	})
}

// TestEdgesCoverForgeRefusesADoneContext pins NewGH and every one of the seven
// forge questions: each reaches the one gh exec site, and the apiError refusal
// names the gh command rather than returning an empty answer.
func TestEdgesCoverForgeRefusesADoneContext(t *testing.T) {
	t.Parallel()
	ctx := edgesDoneContext(t)
	g := NewGH(time.Minute)
	require.NotNil(t, g)
	assert.Equal(t, time.Minute, g.Timeout)
	for _, tc := range []struct {
		name string
		call func() (string, error)
	}{
		{"api", func() (string, error) { return g.api(ctx, "api", "repos/o/n") }},
		{"HeadSHA", func() (string, error) { return g.HeadSHA(ctx, "o/n", "main") }},
		{"CheckRuns", func() (string, error) { _, err := g.CheckRuns(ctx, "o/n", "abc"); return "", err }},
		{"Tags", func() (string, error) { _, err := g.Tags(ctx, "o/n"); return "", err }},
		{"Compare", func() (string, error) { _, err := g.Compare(ctx, "o/n", "v0.1.0", "v0.2.0"); return "", err }},
		{"Files", func() (string, error) { _, err := g.Files(ctx, "o/n", "v0.1.0", "v0.2.0"); return "", err }},
		{"Tag", func() (string, error) { return "", g.Tag(ctx, "o/n", "v0.2.0", "abc", "msg") }},
		{"TagMessage", func() (string, error) { return g.TagMessage(ctx, "o/n", "v0.2.0") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := tc.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "gh")
			assert.Empty(t, out)
		})
	}
}

// TestEdgesCoverToolchainRefusesADoneContext pins the go build edge: Build
// returns the context's error with no captured output, and Platforms returns no
// platform list.
func TestEdgesCoverToolchainRefusesADoneContext(t *testing.T) {
	t.Parallel()
	ctx := edgesDoneContext(t)
	tb := GoBuild{}
	t.Run("Build", func(t *testing.T) {
		t.Parallel()
		out, err := tb.Build(ctx, t.TempDir(), "./cmd/nova-bus",
			filepath.Join(t.TempDir(), "nova-bus"), "linux", "amd64", []string{"-trimpath"})
		require.Error(t, err)
		assert.Empty(t, out)
	})
	t.Run("Platforms", func(t *testing.T) {
		t.Parallel()
		pairs, err := tb.Platforms(ctx)
		require.Error(t, err)
		assert.Nil(t, pairs)
	})
}

// TestEdgesCoverGitRefusesADoneContext pins the local checkout read: a refused
// git returns no path list, so nothing is ever classified from partial output.
func TestEdgesCoverGitRefusesADoneContext(t *testing.T) {
	t.Parallel()
	names, err := ExecGit{}.DiffNames(edgesDoneContext(t), t.TempDir(), "v0.1.0", "v0.2.0")
	require.Error(t, err)
	assert.Nil(t, names)
}

// TestEdgesCoverSSHSendRefusesWithoutASumsFile pins Send's first refusal: it
// copies only what the checksum file names, so a directory with no checksum file
// is refused before any child is even considered.
func TestEdgesCoverSSHSendRefusesWithoutASumsFile(t *testing.T) {
	t.Parallel()
	out, err := ExecSSH{Path: edgesFakeProgram(t)}.
		Send(edgesDoneContext(t), "bench.invalid", t.TempDir(), "/dest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), SumsFile)
	assert.Empty(t, out)
}

// TestEdgesCoverSSHRefusesADoneContext pins the three remote edges through the
// package's own seam: with the fake program named by ExecSSH.Path and a done
// context, Run, Send and Fetch all reach the child edge and return its error.
func TestEdgesCoverSSHRefusesADoneContext(t *testing.T) {
	t.Parallel()
	ctx := edgesDoneContext(t)
	ssh := ExecSSH{Path: edgesFakeProgram(t)}
	t.Run("Run", func(t *testing.T) {
		t.Parallel()
		out, err := ssh.Run(ctx, "bench.invalid", []string{"true"})
		require.Error(t, err)
		assert.Empty(t, out)
	})
	t.Run("Send", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, SumsFile), []byte("abc123  nova-bus\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-bus"), []byte("binary"), 0o755))
		out, err := ssh.Send(ctx, "bench.invalid", dir, "/dest")
		require.Error(t, err)
		assert.Empty(t, out)
	})
	t.Run("Fetch", func(t *testing.T) {
		t.Parallel()
		out, err := ssh.Fetch(ctx, "bench.invalid", "/remote", t.TempDir())
		require.Error(t, err)
		assert.Empty(t, out)
	})
}

// TestEdgesCoverAPIErrorReportsTheCeilingBeforeTheChildError pins the decision
// the forge ceiling exists for: a captured ceiling is reported by name even when
// the cancelled child also returned an error.
func TestEdgesCoverAPIErrorReportsTheCeilingBeforeTheChildError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		hit  bool
		err  error
		want string
	}{
		{"ceiling", true, context.Canceled, "raise forgeCap"},
		{"child", false, context.Canceled, "gh api repos/o/n"},
		{"clean", false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := apiError([]string{"api", "repos/o/n"}, tc.hit, tc.err)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestReadTarForcesMode0755WhateverTheHeaderSays pins security#72 finding 7:
// fetched artifacts must land with the installed mode 0755, never with modes
// chosen by the far side.
func TestReadTarForcesMode0755WhateverTheHeaderSays(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, mode := range []int64{0o600, 0o777} {
		name := fmt.Sprintf("f%o", mode)
		body := "binary"
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	dest := t.TempDir()
	require.NoError(t, readTar(bytes.NewReader(buf.Bytes()), dest))
	for _, mode := range []int64{0o600, 0o777} {
		name := fmt.Sprintf("f%o", mode)
		fi, err := os.Stat(filepath.Join(dest, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "%s: mode", name)
	}
}
