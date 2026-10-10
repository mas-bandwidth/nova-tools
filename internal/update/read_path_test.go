package update

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathRemedy(t *testing.T) {
	t.Parallel()
	// Test that for absolute paths in installed, the remedy does not include PATH
	// For relative paths (command names), it should include directory count
	e := Entry{Installed: []string{"/usr/local/bin/nova-update"}, Kind: "tool"}
	ctx := context.Background()
	r := installed(ctx, e, 0, false, func(ctx context.Context, args []string, _ io.Reader, _ int) ProcessResult {
		return ProcessResult{Reason: "not_found"}
	})
	assert.Contains(t, r.Remedy, "install /usr/local/bin/nova-update")
	assert.False(t, strings.Contains(r.Remedy, "searched="), "absolute path should not include PATH")

	// Test relative path
	e2 := Entry{Installed: []string{"nova-update"}, Kind: "tool"}
	r2 := installed(ctx, e2, 0, false, func(ctx context.Context, args []string, _ io.Reader, _ int) ProcessResult {
		return ProcessResult{Reason: "not_found"}
	})
	assert.Contains(t, r2.Remedy, "searched=PATH dirs=")
}
