//go:build linux

package friend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeProcProbeRole = "NOVA_SYNTHETIC_PROC_PARENT"
const nativeProcProbeSentinel = "NOVA_SYNTHETIC_SENTINEL=fake-probe-only"

func init() {
	if os.Getenv(nativeProcProbeRole) == "1" {
		testbin.Enter("native-proc-probe", nil)
	}
}

// The Linux lane wall's /proc grant is tested only against this test's own
// synthetic parent and a fake sentinel. The child receives a clean environment;
// its read of the parent is the subject, while a broker-like file stays outside
// the wall's roots. No live daemon process or credential is inspected.
func TestNativeWallCannotReadSyntheticParentEnvironment(t *testing.T) {
	t.Parallel()
	if os.Getenv(nativeProcProbeRole) == "1" {
		work, config, read, denied, private := os.Getenv("NOVA_PROBE_WORK"), os.Getenv("NOVA_PROBE_CONFIG"),
			os.Getenv("NOVA_PROBE_READ"), os.Getenv("NOVA_PROBE_DENIED"), os.Getenv("NOVA_PROBE_PRIVATE")
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", os.Getpid()))
		require.NoError(t, err)
		require.True(t, bytes.Contains(raw, []byte(nativeProcProbeSentinel)), "the synthetic parent must really hold the fake sentinel")
		_, err = os.ReadFile(private)
		require.NoError(t, err, "the parent must be able to read its own fake private file")
		require.NoError(t, ProtectDaemonProcEnvironment())
		require.NotEmpty(t, ProcessIdentity(os.Getpid()), "nondumpable daemon must still read its own process identity")
		wall := Wall{Dir: work, ConfigDir: config, Reads: []string{read}, Deny: []string{denied}}
		args := wall.Args()[1:]
		script := `parent=$(sed -n 's/^PPid:[[:space:]]*//p' /proc/$$/status); ` +
			`if [ -z "${NOVA_SYNTHETIC_SENTINEL+x}" ]; then echo CHILD_ENV=absent; else echo CHILD_ENV=present; fi; ` +
			`if cat /proc/self/stat /proc/self/status >/dev/null 2>&1; then echo CHILD_PROC=readable; else echo CHILD_PROC=denied; fi; ` +
			`if grep -aFq 'NOVA_SYNTHETIC_SENTINEL=fake-probe-only' "/proc/$parent/environ" 2>/dev/null; then echo PARENT_ENV=readable; else echo PARENT_ENV=denied; fi; ` +
			`if cat "$NOVA_PROBE_PRIVATE" >/dev/null 2>&1; then echo PRIVATE_FILE=readable; else echo PRIVATE_FILE=denied; fi`
		args = append(args, "/bin/sh", "-c", script)
		childEnv := []string{"PATH=/usr/bin:/bin", "HOME=" + config, "NOVA_PROBE_PRIVATE=" + private}
		code := RunWall(args, childEnv, nil, os.Stdout, os.Stderr)
		require.Equal(t, 0, code, "the synthetic wall must launch and complete")
		job := "synthetic~1"
		require.NoError(t, os.MkdirAll(filepath.Join(work, "jobs", job), 0o700))
		pid := 0
		ctx := WithProcessStarted(context.Background(), func(started int) error {
			pid = started
			birth := ProcessIdentity(started)
			if birth == "" {
				return fmt.Errorf("nondumpable daemon cannot read native child birth")
			}
			return writeRunReceipt(work, job, runReceipt{RunID: "synthetic-run", PID: started, Identity: birth})
		})
		ctx = WithProcessGroupAnchor(ctx, runReceiptPath(work, job)+".anchor")
		_, exit, err := RealExec(ctx, work, "/bin/sh", []string{"-c", "exit 0"}, "")
		require.NoError(t, err)
		require.Zero(t, exit)
		require.Positive(t, pid)
		require.False(t, procgroup.GroupRunnable(pid), "native child group must be quiescent after a nondumpable parent")
		fmt.Println("GROUP_CLEAN=ok")
		return
	}
	if _, ok := sandbox.Available(); !ok {
		t.Skip("this Linux kernel has no enforceable Landlock wall")
	}
	root := t.TempDir()
	work, config, read, denied, private := filepath.Join(root, "work"), filepath.Join(root, "config"),
		filepath.Join(root, "read"), filepath.Join(root, "coordinator"), filepath.Join(root, "private", "grant")
	for _, dir := range []string{work, config, read, denied, filepath.Dir(private)} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	require.NoError(t, os.WriteFile(private, []byte("fake private grant"), 0o600))
	bin, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestNativeWallCannotReadSyntheticParentEnvironment$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + root,
		testbin.DepthEnv("native-proc-probe") + "=1", nativeProcProbeRole + "=1", nativeProcProbeSentinel,
		"NOVA_PROBE_WORK=" + work, "NOVA_PROBE_CONFIG=" + config, "NOVA_PROBE_READ=" + read,
		"NOVA_PROBE_DENIED=" + denied, "NOVA_PROBE_PRIVATE=" + private}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Run(), "owned synthetic parent did not complete: %s", output.String())
	got := output.String()
	assert.Contains(t, got, "CHILD_ENV=absent", "the child did not inherit the fake value")
	assert.Contains(t, got, "CHILD_PROC=readable", "the contained child still needs its own proc files")
	assert.Contains(t, got, "PRIVATE_FILE=denied", "the private broker-like file must remain unreadable")
	assert.Contains(t, got, "GROUP_CLEAN=ok", "native group birth and cleanup must still work")
	assert.Contains(t, got, "PARENT_ENV=denied", "a confined child must not recover the synthetic parent's environment through /proc")
	assert.NotContains(t, got, nativeProcProbeSentinel)
	assert.NotContains(t, strings.ToLower(got), "fake private grant")
}
