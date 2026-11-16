//go:build functional

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunctionalReserveHelpAndValidation(t *testing.T) {
	t.Parallel()
	bin := buildFleet(t)

	// Help output
	code, out, _ := runFleet(t, bin, "reserve", "-h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-fleet reserve")

	code, out, _ = runFleet(t, bin, "resources", "-h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-fleet resources")

	// Missing --needs
	code, _, errs := runFleet(t, bin, "reserve")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-fleet reserve: --needs wants a resource spec")

	// Invalid --needs
	code, _, errs = runFleet(t, bin, "reserve", "--needs", "cpu 0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "invalid --needs spec")

	// Unknown flag
	code, _, errs = runFleet(t, bin, "resources", "--bogus")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "unknown flag --bogus")
}

func TestFunctionalReserveAndResourcesWithFakeSeams(t *testing.T) {
	t.Parallel()
	bin := buildFleet(t)

	binDir := t.TempDir()

	// Fake nova-config
	fakeConfig := filepath.Join(binDir, "nova-config")
	configScript := `#!/bin/sh
if [ "$1" = "machine" ] && [ "$2" = "list" ]; then
	cat <<'EOF'
{"items":[{"kind":"machine","fields":{"name":"bench-1","address":"100.64.0.1","user":"nova","roles":"bench","os":"linux","arch":"amd64","cpu_units":16,"memory_gb":64,"tags":"metal,gpu"}}]}
EOF
	exit 0
fi
exit 1
`
	require.NoError(t, os.WriteFile(fakeConfig, []byte(configScript), 0o755))

	// Fake ssh
	fakeSSH := filepath.Join(binDir, "ssh")
	sshScript := `#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--" ]; then
		shift
		while [ "$1" != "--" ] && [ $# -gt 0 ]; do shift; done
		[ "$1" = "--" ] && shift
		exec "$@"
	fi
done
case "$*" in
	*status\ --json*)
		echo '{"tags":["linux","amd64","metal","gpu"],"resources":{"cpu":16,"memory":64},"held_by_label":{"work":{"cpu":4,"memory":8}}}'
		exit 0
		;;
	*hold*)
		echo "HELD cpu=4 cores=4 slot0"
		# stay alive until stdin EOF
		cat >/dev/null
		exit 0
		;;
	*)
		echo "unexpected ssh command: $*" >&2
		exit 1
		;;
esac
`
	require.NoError(t, os.WriteFile(fakeSSH, []byte(sshScript), 0o755))

	origPath := os.Getenv("PATH")
	envPath := binDir + string(os.PathListSeparator) + origPath

	runWithEnv := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "PATH="+envPath, "TMPDIR="+t.TempDir())
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, o.String(), e.String()
	}

	// 1. Reserve execution with command
	code, out, errs := runWithEnv("reserve", "--needs", "cpu 4, memory 8, gpu", "--", "echo", "HELLO_FROM_RESERVE")
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, "RESERVED host=bench-1 address=100.64.0.1 user=nova cores=4")
	assert.Contains(t, out, "HELLO_FROM_RESERVE")

	// 2. Reserve JSON output
	code, out, errs = runWithEnv("reserve", "--needs", "cpu 4, memory 8", "--json", "--", "true")
	assert.Equal(t, 0, code, errs)
	var resObj map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &resObj))
	assert.Equal(t, "bench-1", resObj["host"])
	assert.Equal(t, "100.64.0.1", resObj["address"])
	assert.Equal(t, "nova", resObj["user"])
	assert.Equal(t, float64(4), resObj["cores"])

	// 3. Resources query
	code, out, errs = runWithEnv("resources")
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, "RESOURCE bench-1 status=ok cpu=4/16 memory=8/64 tags=linux,amd64,metal,gpu")

	// 4. Resources JSON query
	code, out, errs = runWithEnv("resources", "--json")
	assert.Equal(t, 0, code, errs)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "bench-1", rows[0]["machine"])
	assert.Equal(t, "ok", rows[0]["status"])
}
