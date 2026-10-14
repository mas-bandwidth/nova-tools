//go:build functional

package dshdesktop

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Native receiver tests execute the shipped module with a contract-shaped fake
// live agent. No harness app, Redis, socket or model is involved.
func TestNativeReceiverExecutor(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	require.NoError(t, err, "native receiver executor wants Node.js")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "--test", "testdata/receiver.test.mjs").CombinedOutput()
	require.NoError(t, err, string(output))
}
