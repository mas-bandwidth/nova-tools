package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tlaJarDir is the TLC jar directory a record bench holds (fleet/group_vars/all.yml,
// nova_tla_dir; tla/README.md, "The record machines"). The card wall reads it and never
// writes it: a card's STEP runs `java -cp /opt/tla/tla2tools.jar tlc2.TLC`, so a wall that
// hides the jar makes every TLA+ card fail by construction.
const tlaJarDir = "/opt/tla"

// TestChildEnvSetsJavaTmpDirInsideTheWall is one half of the TLA+ rule, held in the child's
// environment (docs/SPEC-SANDBOX.md, "the TLA+ rule"): TLC unpacks its standard modules
// (Naturals, Integers, ...) into java.io.tmpdir, which the JVM defaults to /tmp — a path the
// wall denies — so a card's STEP running `java -cp /opt/tla/tla2tools.jar tlc2.TLC` against
// a spec that EXTENDS one of them dies with FileNotFoundException /tmp/Naturals.tla. ChildEnv
// points java.io.tmpdir at the wall's own temp directory, inside a --write, through
// JAVA_TOOL_OPTIONS, so every TLC call runs unchanged. Red on dev before the grant, green
// after.
func TestChildEnvSetsJavaTmpDirInsideTheWall(t *testing.T) {
	t.Parallel()

	got := javaToolOption(ChildEnv([]string{"HOME=/w/home", "PATH=/bin"}, "/w/.nova-sandbox-tmp"))
	assert.Equal(t, "-Djava.io.tmpdir=/w/.nova-sandbox-tmp", got,
		"JAVA_TOOL_OPTIONS does not point java.io.tmpdir at the wall's own temp directory")
}

// TestChildEnvAppendsJavaTmpDirToAnExistingToolOptions keeps the caller's own JVM options
// and puts the tool's tmpdir option LAST, so the wall's own directory wins the JVM's
// last-option-wins over a caller that already named a java.io.tmpdir of its own.
func TestChildEnvAppendsJavaTmpDirToAnExistingToolOptions(t *testing.T) {
	t.Parallel()

	got := javaToolOption(ChildEnv([]string{"JAVA_TOOL_OPTIONS=-Xmx1g -Djava.io.tmpdir=/outside"}, "/w/.nova-sandbox-tmp"))
	assert.Equal(t, "-Xmx1g -Djava.io.tmpdir=/outside -Djava.io.tmpdir=/w/.nova-sandbox-tmp", got,
		"the caller's options were not kept with the tool's tmpdir appended last")
}

// TestTheTLCRuleKeepsTheJarReadOnly pins the read-only half of the same rule: the wall
// writes only the caller's --write set, so the jar directory — which the card wall reads and
// never writes (the toolchain root likewise: pkg/swarm grants ~/sdk as --read, /opt a linux
// read root) — is never a write, while TLC's unpacked standard modules land in the wall's own
// temp directory, inside the write set, and not beside the jar.
func TestTheTLCRuleKeepsTheJarReadOnly(t *testing.T) {
	t.Parallel()

	write := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(write, "home"), 0o755))
	p, bad := Build(Input{Writes: []string{write}, Home: filepath.Join(write, "home"), Argv: []string{anExecutable(t)}})
	require.Empty(t, bad, "refused at build: %v", bad)
	assert.NotContains(t, p.Writes, tlaJarDir, "the jar directory is a write of the wall; it is read-only")
	assert.True(t, Inside(p.Tmp, write), "the wall's temp directory is outside the write set; TLC's unpacked standard modules would be denied")
}

// javaToolOption is the JAVA_TOOL_OPTIONS value of the child's environment, or "" when the
// variable is not there.
func javaToolOption(env []string) string {
	for _, kv := range env {
		if name, value, _ := strings.Cut(kv, "="); name == "JAVA_TOOL_OPTIONS" {
			return value
		}
	}
	return ""
}
