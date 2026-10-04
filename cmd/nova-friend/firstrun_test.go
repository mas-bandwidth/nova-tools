package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/require"
)

func TestBannerPlansNeverStartChildren(t *testing.T) {
	t.Parallel()
	cli := friendTool(context.Background())
	examples, err := onboarding.ExampleLines(cli.Banner(), "nova-friend")
	require.NoError(t, err)
	require.Len(t, examples, 3)
	for _, line := range examples {
		args, err := onboarding.SplitShell(line)
		require.NoError(t, err)
		var out, stderr bytes.Buffer
		require.Equal(t, 0, cli.Run(args[1:], nil, &out, &stderr), stderr.String())
	}
	var out, stderr bytes.Buffer
	require.Equal(t, 0, cli.Run([]string{"watch", "--server", "unused:1", "--friend", "reader", "--argv", `["/executable-does-not-exist"]`, "--dry-run"}, nil, &out, &stderr))
	require.Contains(t, out.String(), "dry_run=true")
}

func TestWatchReportsIndependentInputProblems(t *testing.T) {
	t.Parallel()
	var out, stderr bytes.Buffer
	code := friendTool(context.Background()).Run([]string{"watch", "--argv", "null", "--every", "0s"}, nil, &out, &stderr)
	require.NotZero(t, code)
	for _, wanted := range []string{"server", "friend", "--argv wants", "positive durations"} {
		require.Contains(t, stderr.String(), wanted)
	}
}

func TestToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	require.Empty(t, friendTool(context.Background()).Problems())
}

func TestFirstRunTranscript(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../../docs/TESTS.md")
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(body), "nova-friend")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-friend", lines)
	require.NoError(t, err)
	var got []onboarding.Result
	for _, step := range steps {
		var out, stderr bytes.Buffer
		code := friendTool(context.Background()).Run(step.Args, nil, &out, &stderr)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: stderr.String()})
	}
	require.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}

func TestWatchChildFailureAndHelpHaveExplicitExitCodes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		args []string
		exit int
	}{
		{"help", []string{"watch", "-h"}, 0},
		{"invalid", []string{"watch"}, 2},
		{"failed child", []string{"watch", "--server", "unused:1", "--friend", "reader", "--sprint", "/usr/bin/true", "--argv", `["/executable-does-not-exist"]`}, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var out, stderr bytes.Buffer
			require.Equal(t, row.exit, friendTool(context.Background()).Run(row.args, nil, &out, &stderr), stderr.String())
			if row.exit != 0 {
				require.NotContains(t, out.String(), "WATCH OK")
			}
		})
	}
}

func TestUnsafeTimingRefusedBeforeDryRun(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		every string
		exit  int
	}{{"4.5s", 0}, {"4.500000001s", 2}, {"15s", 2}} {
		t.Run(row.every, func(t *testing.T) {
			t.Parallel()
			var out, stderr bytes.Buffer
			require.Equal(t, row.exit, friendTool(context.Background()).Run([]string{"watch", "--server", "unused:1", "--friend", "reader", "--argv", `["/executable-does-not-exist"]`, "--every", row.every, "--timeout", "3s", "--dry-run"}, nil, &out, &stderr))
			if row.exit != 0 {
				require.Contains(t, stderr.String(), "at most 7.5s")
			}
		})
	}
}
