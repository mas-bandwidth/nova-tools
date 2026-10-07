package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first run the help prints for a reader with no store runs as printed in
// an empty directory: --example writes the fixture the next command reads, so
// a binary-only user never reaches into the source tree. The commands are run
// exactly as the help prints them, through a named nova-config, as a stranger
// would paste them.
func TestInventoryFirstRunPrintsAndReadsItsOwnFixture(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, help, errs := h.run(t, "inventory", "-h")
	require.Zero(t, code, errs)
	const heading = "first run, with no store: "
	command, found := "", false
	for _, line := range strings.Split(help, "\n") {
		if rest, ok := strings.CutPrefix(line, heading); ok {
			command, found = rest, true
			break
		}
	}
	require.True(t, found, "the help has no first-run line: %s", help)
	require.Contains(t, command, "nova-config inventory --example", command)

	dir := t.TempDir()
	self, err := os.Executable()
	require.NoError(t, err)
	shim := "#!/bin/sh\nNOVA_CONFIG_TEST_HELPER=1 exec " + strconv.Quote(self) + " -test.run='^TestInventoryHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-config"), []byte(shim), 0o755))
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the first run the help prints did not run in an empty directory:\n%s", out)
	assert.Contains(t, string(out), `"ansible_host"`, "the printed first run prints an inventory: %s", out)
}
