//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunctionalReservePlayAndResourcesQuery(t *testing.T) {
	t.Parallel()
	bin := buildFleet(t)

	// reserve is now the arbitrator provisioning play.
	code, out, _ := runFleet(t, bin, "show", "reserve")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "the fleet's resource arbitrator")

	code, out, _ = runFleet(t, bin, "resources", "-h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-fleet resources")

	code, _, errs := runFleet(t, bin, "resources", "--bogus")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "unknown flag")

	// Fake nova-reserve binary returning a v3 snapshot frame.
	binDir := t.TempDir()
	fakeReserve := filepath.Join(binDir, "nova-reserve")
	script := `#!/bin/sh
if [ "$1" = "status" ] && [ "$2" = "--json" ]; then
	echo '{"op":"snapshot","reconnect_until":"","machines":[{"host":"bench-1","online":true,"tags":["amd64","gpu","linux"],"usable_cores":16,"resources":{"cpu":16,"memory":64},"free":{"cpu":12,"memory":56},"leases":[{"paused":false,"sublease":false,"needs":{"cpu":4,"memory":8}}],"claims":[],"queue":[]}]}'
	exit 0
fi
exit 2
`
	require.NoError(t, os.WriteFile(fakeReserve, []byte(script), 0o755))

	cmd := exec.Command(bin, "resources")
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	require.NoError(t, cmd.Run(), se.String())
	assert.Equal(t, "RESOURCE bench-1 status=ok cpu=4/16 memory=8/64 free_cpu=12 free_memory=56 leases=1 claims=0 queued=0 tags=amd64,gpu,linux\n", so.String())
}
