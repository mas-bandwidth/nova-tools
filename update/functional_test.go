//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportAgainstTheRealLatestRelease reads the published latest release over the
// network into an empty bin directory: every released tool is missing, none is stale.
func TestReportAgainstTheRealLatestRelease(t *testing.T) {
	var out, errs bytes.Buffer
	e := env{stdout: &out, stderr: &errs, forge: githubForge(os.Getenv), goos: "linux", goarch: "amd64", exe: os.Executable}
	code := run(context.Background(), []string{"report", "--bin", t.TempDir()}, e)
	require.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
	assert.Contains(t, out.String(), "TOOL name=nova-update state=missing")
	assert.Regexp(t, `REPORT version=v\S+ platform=linux-amd64 .* current=0 stale=0 missing=[1-9]`, out.String())
}
