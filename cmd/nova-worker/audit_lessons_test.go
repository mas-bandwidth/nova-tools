package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE NEW-USER AUDIT (2026-09-11). Each test below is one footgun or one stumble a person
// meeting this tool for the first time actually hit, written as the assertion that would
// have stopped it.

// S3: the harness contract was undocumented -- cwd, argv, NOVA_SWARM_JOB, RESULT.md -- and
// the audit learned it by dumping the fake harness's own environment. The command reference
// says it, and names the fake harness that already demonstrates it. That reference is
// docs/CLI.md since the README became an adoption guide.
func TestTheCommandReferenceCarriesTheHarnessContract(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	require.NoError(t, err)
	body := string(raw)
	for _, want := range []string{"### The harness contract", "NOVA_SWARM_JOB", "RESULT.md",
		"cmd/nova-worker/testdata/fakeharness", "harness_args"} {
		assert.Contains(t, body, want, "docs/CLI.md's harness contract wants %q", want)
	}
}
