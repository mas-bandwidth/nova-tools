package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunctionalrunPodmanCoverFirstWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		n    int
		want string
	}{
		{"under", []string{"a", "b", "c"}, 1, "a"},
		{"equal", []string{"a", "b", "c"}, 3, "a b c"},
		{"over", []string{"a", "b", "c"}, 5, "a b c"},
		{"no args", nil, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, firstWords(tc.args, tc.n))
		})
	}
}

func TestFunctionalrunPodmanCoverNewPodman(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "podman")
	eng := newPodman(bin, &bytes.Buffer{})
	require.NotNil(t, eng)
	assert.Equal(t, bin, eng.bin)
	found := false
	for _, kv := range eng.env {
		if strings.HasPrefix(kv, "RUNNER_TRACKING_ID=") {
			found = true
			break
		}
	}
	assert.False(t, found, "env should not contain RUNNER_TRACKING_ID")
}

func TestFunctionalrunPodmanCoverOutput(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "no-such-podman")
	eng := newPodman(bin, &bytes.Buffer{})
	require.NotNil(t, eng)
	out, err := eng.Output(context.Background(), "ps", "-a", "--format", "{{.ID}}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), bin)
	assert.Contains(t, err.Error(), "ps -a")
	assert.Empty(t, out)
}

func TestFunctionalrunPodmanCoverStart(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "no-such-podman")
	eng := newPodman(bin, &bytes.Buffer{})
	require.NotNil(t, eng)
	var stderr bytes.Buffer
	proc, err := eng.Start([]string{"ps", "-a", "--format", "{{.ID}}"}, &stderr, &stderr)
	require.Error(t, err)
	require.Nil(t, proc)
	assert.Contains(t, err.Error(), bin)
	assert.Contains(t, err.Error(), "ps -a")
}

func TestFunctionalrunPodmanCoverUseRuntime(t *testing.T) {
	t.Parallel()
	lookPath := func(string) (string, error) {
		return "", os.ErrNotExist
	}
	var stderr bytes.Buffer
	bin, ok := useRuntime("", lookPath, &stderr)
	assert.Equal(t, "", bin)
	assert.False(t, ok)
	assert.Contains(t, stderr.String(), "no container runtime on PATH")
}
