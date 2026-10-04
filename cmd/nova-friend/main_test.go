package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIRefusesBeforeRuntime pins the skeleton's combined-input boundary.
func TestCLIRefusesBeforeRuntime(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		args []string
		code int
		want []string
	}{
		{"bare", nil, 2, []string{"run: nova-friend help"}},
		{"missing", []string{"listen"}, 2, []string{"--friend", "--prefix", "--adapter", "--redis", "--consumer"}},
		{"bad durations", []string{"listen", "--friend", "reader", "--prefix", "example", "--adapter", "adapter.json", "--redis", "unused:1", "--consumer", "worker", "--timeout", "0s", "--lease", "-1s"}, 2, []string{"--timeout", "--lease"}},
		{"help", []string{"listen", "-h"}, 0, []string{"effect:", "exit codes:"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			w := world{ctx: context.Background(), run: func(context.Context, string, request) *tool.Out { t.Fatal("runtime reached"); return nil }}
			var out, err bytes.Buffer
			code := friendTool(w).Run(row.args, nil, &out, &err)
			assert.Equal(t, row.code, code)
			for _, want := range row.want {
				assert.Contains(t, out.String()+err.String(), want)
			}
		})
	}
}

// TestPlansNeverReachHarness pins all write aliases to the dry-run boundary.
func TestPlansNeverReachHarness(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"register", "startup", "listen", "serve"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := world{ctx: context.Background(), run: func(context.Context, string, request) *tool.Out { t.Fatal("dry run reached runtime"); return nil }}
			var out, err bytes.Buffer
			code := friendTool(w).Run([]string{name, "--friend", "reader", "--prefix", "example", "--redis", "unused:1", "--adapter", "missing.json", "--consumer", "worker", "--dry-run", "--json"}, nil, &out, &err)
			require.Equal(t, 0, code, err.String())
			assert.Contains(t, out.String(), `"dry_run":true`)
			assert.Contains(t, out.String(), "no store read")
		})
	}
}

// TestStatusPreservesUnknownLiveness pins inspection dispatch and identity.
func TestStatusPreservesUnknownLiveness(t *testing.T) {
	t.Parallel()
	var got request
	w := world{ctx: context.Background(), run: func(_ context.Context, name string, r request) *tool.Out {
		assert.Equal(t, "status", name)
		got = r
		return tool.Done().Fact("awake", "unknown")
	}}
	var out, err bytes.Buffer
	require.Equal(t, 0, friendTool(w).Run([]string{"status", "--friend", "reader", "--prefix", "example", "--redis", "unused:1"}, nil, &out, &err), err.String())
	assert.Equal(t, "reader", got.Friend)
	assert.Equal(t, "example", got.Prefix)
	assert.Contains(t, out.String(), "awake=unknown")
}

// TestToolDefinitionMeetsStandard holds the skeleton definition itself.
func TestToolDefinitionMeetsStandard(t *testing.T) {
	t.Parallel()
	cli := friendTool(world{})
	assert.Empty(t, cli.Problems())
	var out, stderr bytes.Buffer
	require.Equal(t, 0, cli.Run([]string{"help"}, nil, &out, &stderr))
	assert.Contains(t, out.String(), "nova-friend is pre-alpha: not ready for production use.")
}
