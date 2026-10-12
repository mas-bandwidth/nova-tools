//go:build functional && linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tlcReadHelperEnv makes the test binary the walled child: Run restricts the process that
// calls it and a Landlock domain cannot be lifted, so the wall is applied in a re-exec of
// this binary and never in the test process itself (the delete_outside_test.go pattern).
const tlcReadHelperEnv = "NOVA_TEST_TLC_READ"

// naturalsProbe is a TLC spec that EXTENDS Naturals, one of the ten standard modules the
// jar bundles and the smallest whose check forces TLC to unpack that module into
// java.io.tmpdir (tla/README.md, "The record machines"). Without the JAVA_TOOL_OPTIONS grant
// the JVM unpacks into /tmp, which the wall denies, and the run dies with
// FileNotFoundException /tmp/Naturals.tla.
const naturalsProbe = `---- MODULE NaturalsProbe ----
EXTENDS Naturals

VARIABLE x

Init == x = 0
Next == x < 3 /\ x' = x + 1

TypeOK == x \in Nat

Spec == Init /\ [][Next]_x
====
`

const naturalsProbeCfg = "SPECIFICATION Spec\nINVARIANT TypeOK\n"

// TestTheTLCRuleLetsACardRunTLCInsideTheWall is the TLA+ rule measured
// (docs/SPEC-SANDBOX.md, "the TLA+ rule"): inside the wall the TLC jar opens for read, a
// write to it and into its directory is refused, and a real TLC run of a spec that EXTENDS
// Naturals succeeds, its standard module unpacked into the wall's own temp directory rather
// than /tmp. A wall that hides the jar or the toolchain makes every TLA+ card fail by
// construction.
func TestTheTLCRuleLetsACardRunTLCInsideTheWall(t *testing.T) {
	t.Parallel()
	if os.Getenv(tlcReadHelperEnv) == "1" {
		os.Exit(runTLCReadHelper())
	}
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	jar := filepath.Join(tlaJarDir, "tla2tools.jar")
	if fi, err := os.Stat(jar); err != nil || fi.IsDir() {
		t.Skipf("no TLC jar at %s on this machine (tla/README.md, \"The record machines\")", jar)
	}
	java, toolchain := tlcToolchain()
	if java == "" {
		t.Skip("no java under the bench toolchain root on this machine (fleet/group_vars/all.yml, nova_tla_java_candidates)")
	}

	write := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(write, "home"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(write, "NaturalsProbe.tla"), []byte(naturalsProbe), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(write, "NaturalsProbe.cfg"), []byte(naturalsProbeCfg), 0o644))

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheTLCRuleLetsACardRunTLCInsideTheWall$", "-test.count=1")
	cmd.Env = append(os.Environ(), tlcReadHelperEnv+"=1", "TLC_WRITE="+write, "TLC_JAVA="+java, "TLC_JAR="+jar, "TLC_JARDIR="+tlaJarDir, "TLC_TOOLCHAIN="+toolchain)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	assert.Contains(t, got, "read=0", "open of the jar for read inside the wall was refused")
	assert.Contains(t, got, "write=1", "a write to the jar inside the wall was not refused")
	assert.Contains(t, got, "writedir=1", "a write into the jar's directory inside the wall was not refused: the grant is read-only")
	assert.Contains(t, got, "tlc=0", "a TLC run of a spec that EXTENDS Naturals failed inside the wall: its standard-module unpack was denied")
}

// runTLCReadHelper is the walled child: one shell reads the jar, tries a write to it and a
// write into its directory, and runs TLC on a spec that EXTENDS Naturals, printing each
// status. The wall is the card wall's shape: the bench toolchain root as a --read (the
// jar's directory is already read through the linux /opt root), the job directory the one
// --write, and ChildEnv the child's environment so JAVA_TOOL_OPTIONS points java.io.tmpdir
// at the wall's own temp directory.
func runTLCReadHelper() int {
	write, java, jar, jarDir, toolchain := os.Getenv("TLC_WRITE"), os.Getenv("TLC_JAVA"), os.Getenv("TLC_JAR"), os.Getenv("TLC_JARDIR"), os.Getenv("TLC_TOOLCHAIN")
	script := strings.Join([]string{
		`cat "$3" >/dev/null 2>/dev/null; echo read=$?`,
		`echo x >> "$3" 2>/dev/null; [ $? -eq 0 ] && echo write=0 || echo write=1`,
		`echo x > "$4/nova-tlc-write-probe" 2>/dev/null; [ $? -eq 0 ] && echo writedir=0 || echo writedir=1`,
		`cd "$1" && "$2" -cp "$3" tlc2.TLC -workers 2 -deadlock NaturalsProbe.tla >/dev/null 2>&1; echo tlc=$?`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{write},
		Reads:  []string{toolchain},
		Home:   filepath.Join(write, "home"),
		Argv:   []string{"sh", "-c", script, "sh", write, java, jar, jarDir, toolchain},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return 2
	}
	code, err := Run(p, ChildEnv(os.Environ(), p.Tmp), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return 2
	}
	return code
}

// tlcToolchain is the bench's java and the wall's read root for it, from the same source the
// card wall uses (swarm.ToolchainRoots over the bench user's password-database home, never
// $HOME: a card's child sets HOME to its data home and cmd/nova-swarm's benchPasswdHome reads
// the password entry). java is <home>/sdk/bin/java, the first candidate the tla play names
// (fleet/group_vars/all.yml, nova_tla_java_candidates); the read root is <home>/sdk. ("", "")
// when the bench has no sdk tree, and the test skips.
func tlcToolchain() (string, string) {
	u, err := user.Current()
	if err != nil {
		return "", ""
	}
	root := filepath.Join(u.HomeDir, "sdk")
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return "", ""
	}
	java := filepath.Join(root, "bin", "java")
	if fi, err := os.Stat(java); err != nil || fi.IsDir() {
		return "", ""
	}
	return java, root
}
