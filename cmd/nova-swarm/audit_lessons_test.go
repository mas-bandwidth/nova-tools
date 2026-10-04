package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE NEW-USER AUDIT (2026-09-11). Each test below is one footgun or one stumble a person
// meeting this tool for the first time actually hit, written as the assertion that would
// have stopped it.

// #632: `template` must print the six typed card templates nova-pulse `cut` reads from a
// templates directory (read, fix, text, replay, drift, tone) plus models.tsv, so a templates
// dir can be built from the tool instead of copied out of cmd/nova-pulse/testdata.
func TestTemplatePrintsThePulseCardTemplates(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	for _, name := range []string{"read", "fix", "text", "replay", "drift", "tone", "models.tsv"} {
		exit, stdout, stderr := b.swarm("template", "--name", name)
		require.Equal(t, 0, exit, "`template --name %s` exited %d; nova-pulse cut needs the six typed templates and models.tsv:\n%s%s", name, exit, stdout, stderr)
		require.NotEqual(t, "", strings.TrimSpace(stdout), "`template --name %s` printed nothing", name)
	}
	// read is a text-only card and must carry the no-build line and the RESULT contract.
	exit, stdout, stderr := b.swarm("template", "--name", "read")
	require.Equal(t, 0, exit, "`template --name read` exited %d:\n%s%s", exit, stdout, stderr)
	mustContain(t, "the read card", stdout, "RESULT <label> sha=<sha12>")
	mustContain(t, "the read card", stdout, "Do not run go build, go test or any toolchain")
}

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
		"cmd/nova-swarm/testdata/fakeharness", "harness_args"} {
		assert.Contains(t, body, want, "docs/CLI.md's harness contract wants %q", want)
	}
}
