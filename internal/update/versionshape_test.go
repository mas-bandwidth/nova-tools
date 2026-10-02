//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two version-line shapes a reader meets in a real bin directory. Every binary
// here is a shell stub, so this file is unix-only and touches no network.

// TestSnapshotAcceptsAToolThatSaysOneMoreTrueThing: a tool that prints a fifth
// `build=<hex>` token carries extra metadata and is not a broken binary. One
// tool adding a field must never refuse the whole bin.
func TestSnapshotAcceptsAToolThatSaysOneMoreTrueThing(t *testing.T) {
	t.Parallel()

	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	bin := t.TempDir()
	specStub(t, bin, "nova-bus", "nova-bus "+stamp+" darwin/arm64 go1.27.1")
	specStub(t, bin, "nova-merge", "nova-merge "+stamp+" darwin/arm64 go1.27.1 build=9c1885748f57")
	specStub(t, bin, "nova-sandbox", "nova-sandbox "+stamp+" darwin/arm64 go1.27.1 backend=sandbox-exec platform=darwin")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, stdout, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	require.EqualValuesf(t, 0, code, "snapshot refused a bin holding a tool with extras: exit %d\n%s", code, stderr)
	need(t, stdout, "SNAPSHOT OK", "tools=3", "stamp="+field(stamp))
	body := string(readFileOrFail(t, out))
	for _, want := range []string{
		"nova-bus\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
		"nova-merge\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
		"nova-sandbox\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
	} {
		assert.Containsf(t, body, want, "snapshot did not record %q:\n%s", want, body)
	}
	// The extras are metadata about one tool, not a column of the set: the
	// stamp every row carries is the same, so the mixed-set gate still reads
	// field two and nothing else.
	if strings.Contains(body, "build=") || strings.Contains(body, "backend=") {
		assert.Failf(t, "", "an extra leaked into the snapshot's own columns:\n%s", body)
	}
}

// TestSnapshotStillRefusesALineThatIsNotAVersionLine keeps the repair from
// being "accept anything": a binary that prints a usage refusal is still a
// binary this verb cannot read, and saying so is the whole job.
func TestSnapshotStillRefusesALineThatIsNotAVersionLine(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	specStub(t, bin, "nova-bus", "nova-bus v1 darwin/arm64 go1.27.1")
	specStub(t, bin, "nova-broken", "nova-broken: no verb given; run: nova-broken help")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	require.EqualValuesf(t, 2, code, "exit %d, want 2; stderr=%s", code, stderr)
	need(t, stderr, "SNAPSHOT REFUSED", "nova-broken", "go build ./cmd/nova-broken")
	if _, err := os.Stat(out); err == nil {
		assert.Error(t, err, "a refused snapshot wrote its --out")
	}
}

// A manifest row naming an executable alone uses the version-probe ladder:
// `version`, then `--version`, then bare. A nova tool answers the first probe;
// its bare usage refusal must not turn that valid reading into UNKNOWN.
func TestReportAsksOurOwnToolsTheVerbTheyAnswer(t *testing.T) {
	t.Parallel()

	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	bin := t.TempDir()
	// Answers `version` and refuses anything else, exactly like a nova tool.
	novaish := specScript(t, bin, "nova-swarm", `case "$1" in
version) printf '%s\n' 'nova-swarm `+stamp+` darwin/arm64 go1.27.1' ;;
*) printf '%s\n' 'nova-swarm: no verb given; run: nova-swarm help' >&2; exit 2 ;;
esac`)
	// Answers only `--version`, the other spelling a friend's tool may hold.
	dashed := specScript(t, bin, "nova-secrets", `case "$1" in
--version) printf '%s\n' 'nova-secrets `+stamp+` darwin/arm64 go1.27.1 build=9c1885748f57' ;;
*) printf '%s\n' 'nova-secrets: no verb given' >&2; exit 2 ;;
esac`)
	// Answers bare, the way a foreign tool does: the ladder must not break it.
	bare := specStub(t, bin, "nova-foreign", "nova-foreign 1.2.3 darwin/arm64 go1.27.1")

	file := manifest(t,
		row("nova-swarm", "tool", novaish, "local:"+novaish, "none"),
		row("nova-secrets", "tool", dashed, "local:"+dashed, "none"),
		row("nova-foreign", "tool", bare, "local:"+bare, "none"),
	)
	// The OS's first-exec toll is paid here and not out of the report's --timeout: see
	// seen(). nova-secrets is the tool this matters most for -- it is the only one asked
	// twice, so it is the one whose ladder the toll would eat.
	seen(t, novaish, dashed, bare)

	code, stdout, stderr := specRun(t, Environment{}, "report", "--file", file)
	assert.NotContainsf(t, stdout, "REPORT UNKNOWN", "report ran our own tools bare and could not read them:\n%s", stdout)
	for _, want := range []string{
		"REPORT TOOL name=nova-swarm",
		"REPORT TOOL name=nova-secrets",
		"REPORT TOOL name=nova-foreign",
		"known=3",
		"unknown=0",
	} {
		assert.Containsf(t, stdout, want, "missing %q in:\n%s\n%s", want, stdout, stderr)
	}
	assert.EqualValuesf(t, 0, code, "exit %d, want 0; stderr=%s", code, stderr)
}

// TestReportStillRefusesAToolThatAnswersNothing: the ladder tries three
// invocations, and a tool that answers none of them is still UNKNOWN with the
// remedy naming what to do -- a ladder that invents a reading is worse than the
// bare run it replaced.
func TestReportStillRefusesAToolThatAnswersNothing(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	mute := specScript(t, bin, "nova-mute", `printf '%s\n' 'nova-mute: no verb given' >&2; exit 2`)
	file := manifest(t, row("nova-mute", "tool", mute, "local:"+mute, "none"))
	code, _, stderr := specRun(t, Environment{}, "report", "--file", file)
	// A report that ran and failed prints on stderr, where a FAIL belongs.
	need(t, stderr, "REPORT UNKNOWN name=nova-mute", "unknown=1")
	assert.EqualValuesf(t, 1, code, "exit %d, want 1 (the check ran and failed)", code)
}

// TestReportRunsAnExplicitArgvExactlyAsWritten: a manifest that names its own
// argv -- `go version`, `sops --version` -- is the caller's sentence and the
// ladder never appends to it.
func TestReportRunsAnExplicitArgvExactlyAsWritten(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	// Refuses any argument at all: proof that nothing was appended.
	strict := specScript(t, bin, "strict", `if [ $# -ne 1 ] || [ "$1" != "report" ]; then printf 'argv was %s\n' "$*" >&2; exit 3; fi
printf '%s\n' 'strict 4.5.6'`)
	file := manifest(t, row("strict", "tool", strict+" report", "local:"+strict, "none"))
	// Warm the fixture before the report's default timeout so platform first-exec
	// work cannot masquerade as an explicit-argv parsing failure.
	seen(t, strict)
	code, stdout, stderr := specRun(t, Environment{}, "report", "--file", file)
	if code != 0 || strings.Contains(stdout, "UNKNOWN") {
		require.Failf(t, "", "the ladder rewrote an explicit argv: exit %d\n%s\n%s", code, stdout, stderr)
	}
	need(t, stdout, "version=4.5.6")
}

func readFileOrFail(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}
