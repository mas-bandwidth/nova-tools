package sprint

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// GitRunner runs git in a clone and returns its output, trimmed. RunGit is the tree's; a
// test gives its own, or RunGit on a twin repository (the drift reader, ReadDrift, takes
// one; it is the test's until the binding reads the drift, export_test.go).
type GitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// RunGit is git through the tree's runner (pkg/subproc): bounded by git's budget, and
// its stderr in the error.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	var out, errs bytes.Buffer
	argv := append([]string{"-C", dir}, args...)
	cmd, cancel := subproc.CommandFor(ctx, subproc.GitBudgetFor(argv), "git", argv...)
	defer cancel()
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errs.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
