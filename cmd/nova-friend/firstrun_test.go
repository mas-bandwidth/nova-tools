package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBannerExamplesRun pins the actual onboarding commands to store-free plans.
func TestBannerExamplesRun(t *testing.T) {
	t.Parallel()
	w := world{ctx: context.Background(), run: func(context.Context, string, request) *tool.Out { t.Fatal("example reached runtime"); return nil }}
	cli := friendTool(w)
	examples, err := onboarding.ExampleLines(cli.Banner(), "nova-friend")
	require.NoError(t, err)
	require.Len(t, examples, 3)
	for _, line := range examples {
		args, err := onboarding.SplitShell(line)
		require.NoError(t, err)
		var out, stderr bytes.Buffer
		assert.Equal(t, 0, cli.Run(args[1:], nil, &out, &stderr), stderr.String())
		assert.Contains(t, out.String(), "dry_run=true")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-friend")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-friend", lines)
	require.NoError(t, err)
	var commands []string
	var got []onboarding.Result
	for _, step := range steps {
		commands = append(commands, strings.TrimPrefix(step.Line, "$ "))
		var out, stderr bytes.Buffer
		code := cli.Run(step.Args, nil, &out, &stderr)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: stderr.String()})
	}
	require.Equal(t, examples, commands)
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}
