//go:build functional

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestFunctionalFleetWithRealReserve(t *testing.T) {
	t.Parallel()
	fleetBin := buildFleet(t)

	realReserve := filepath.Join(t.TempDir(), "nova-reserve-real")
	buildOut, err := exec.Command("go", "build", "-o", realReserve, "../nova-reserve").CombinedOutput()
	require.NoError(t, err, "build nova-reserve: %s", string(buildOut))

	work := t.TempDir()
	binDir := filepath.Join(work, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	setupBench := func(addr string) string {
		home := filepath.Join(work, "home-"+addr)
		localBin := filepath.Join(home, ".local", "bin")
		resDir := filepath.Join(work, "res-"+addr)
		require.NoError(t, os.MkdirAll(localBin, 0o755))
		wrapper := fmt.Sprintf("#!/bin/sh\nv=$1; shift\nexec %q \"$v\" --dir %q \"$@\"\n", realReserve, resDir)
		require.NoError(t, os.WriteFile(filepath.Join(localBin, "nova-reserve"), []byte(wrapper), 0o755))
		out, err := exec.Command(realReserve, "set", "--dir", resDir,
			"--cpu", "8", "--memory", "16", "--cores", "4", "--tags", "gpu,metal", "--resources", "ps5=1").CombinedOutput()
		require.NoError(t, err, "nova-reserve set: %s", string(out))
		return resDir
	}
	resDir1 := setupBench("100.64.0.1")
	resDir2 := setupBench("100.64.0.2")

	configJSON := fmt.Sprintf(`{"items":[`+
		`{"kind":"machine","fields":{"name":"bench-1","address":"100.64.0.1","user":"nova","roles":"bench","os":%q,"arch":%q,"cores":4,"cpu_units":8,"memory_gb":16,"tags":"gpu,metal","resources":{"ps5":1}}},`+
		`{"kind":"machine","fields":{"name":"bench-2","address":"100.64.0.2","user":"nova","roles":"bench","os":%q,"arch":%q,"cores":4,"cpu_units":8,"memory_gb":16,"tags":"gpu,metal","resources":{"ps5":1}}}`+
		`]}`, runtime.GOOS, runtime.GOARCH, runtime.GOOS, runtime.GOARCH)
	fakeConfig := filepath.Join(binDir, "nova-config")
	configScript := "#!/bin/sh\nif [ \"$1\" = \"machine\" ] && [ \"$2\" = \"list\" ]; then\ncat <<'EOF'\n" + configJSON + "\nEOF\nexit 0\nfi\nexit 1\n"
	require.NoError(t, os.WriteFile(fakeConfig, []byte(configScript), 0o755))

	// Loopback ssh shim: resolves user@addr to its isolated HOME with real nova-reserve.
	fakeSSH := filepath.Join(binDir, "ssh")
	sshScript := fmt.Sprintf(`#!/bin/sh
while [ $# -gt 0 ]; do
	case "$1" in
		-o) shift 2 ;;
		-*) shift ;;
		*) target="$1"; shift; break ;;
	esac
done
addr="${target#*@}"
export HOME=%q"/home-$addr"
export PATH="$HOME/.local/bin:$PATH"
if [ "${1:-}" = "--" ]; then
	shift
	exec "$@"
fi
exec sh -c "$*"
`, work)
	require.NoError(t, os.WriteFile(fakeSSH, []byte(sshScript), 0o755))

	baseEnv := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "NOVA_RESERVE_DIR=") {
			baseEnv = append(baseEnv, kv)
		}
	}
	baseEnv = append(baseEnv, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+t.TempDir())

	runFleetReal := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(fleetBin, args...)
		cmd.Env = baseEnv
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			require.NoError(t, err)
		}
		return code, o.String(), e.String()
	}

	// 1. Atomic hold + remote command sees active real flock on the chosen bench, then auto-releases.
	code, out, errs := runFleetReal("reserve", "--needs", "cpu 4, memory 8, gpu, ps5",
		"--consumer", "friendA-1", "--label", "work", "--priority", "high",
		"--", "nova-reserve", "status", "--json")
	require.Equal(t, 0, code, errs)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2, out)
	assert.Contains(t, lines[0], "cores=2")
	var liveStatus struct {
		HeldOther   map[string]int            `json:"held_other"`
		HeldByLabel map[string]map[string]int `json:"held_by_label"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &liveStatus))
	assert.Equal(t, 4, liveStatus.HeldOther["cpu"])
	assert.Equal(t, 8, liveStatus.HeldOther["memory"])
	assert.Equal(t, 1, liveStatus.HeldOther["ps5"])
	assert.Equal(t, 4, liveStatus.HeldByLabel["work"]["cpu"])

	// 2. Hold ps5 on bench-1 directly; nova-fleet reserve fails over atomically to bench-2.
	hold1 := exec.Command(realReserve, "hold", "--dir", resDir1, "--needs", "ps5", "--holder", "friend")
	hold1In, err := hold1.StdinPipe()
	require.NoError(t, err)
	hold1Out, err := hold1.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, hold1.Start())
	defer func() {
		_ = hold1In.Close()
		_ = hold1.Wait()
	}()
	hLine1, err := bufio.NewReader(hold1Out).ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(hLine1, "HELD "))

	code, out, errs = runFleetReal("reserve", "--needs", "cpu 2, memory 4, ps5", "--", "true")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "RESERVED host=bench-2 address=100.64.0.2 user=nova cores=1")

	// 3. Hold ps5 on bench-2 as well; immediate reserve exits 1, and --wait acquires once bench-1 releases.
	hold2 := exec.Command(realReserve, "hold", "--dir", resDir2, "--needs", "ps5", "--holder", "friend")
	hold2In, err := hold2.StdinPipe()
	require.NoError(t, err)
	hold2Out, err := hold2.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, hold2.Start())
	defer func() {
		_ = hold2In.Close()
		_ = hold2.Wait()
	}()
	hLine2, err := bufio.NewReader(hold2Out).ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(hLine2, "HELD "))

	code, _, errs = runFleetReal("reserve", "--needs", "ps5", "--", "true")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no room on any eligible machine right now")

	// Query resources while both benches hold ps5.
	code, out, errs = runFleetReal("resources")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "RESOURCE bench-1 status=ok cpu=1/8 memory=1/16")
	assert.Contains(t, out, "RESOURCE bench-2 status=ok cpu=1/8 memory=1/16")

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = hold1In.Close()
	}()
	code, out, errs = runFleetReal("reserve", "--needs", "ps5", "--wait", "5s", "--", "true")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "RESERVED host=bench-1")
}
