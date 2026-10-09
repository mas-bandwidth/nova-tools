//go:build linux

package friend

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTraceFS records public durable receipt transitions, not native output.
type setupTraceFS struct {
	OSFS
	t *testing.T
}

func (s setupTraceFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if err := s.OSFS.WriteFile(path, data, perm); err != nil {
		return err
	}
	if filepath.Base(path) == "RUN" {
		var receipt setupReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return err
		}
		event, err := json.Marshal(struct {
			Event   string       `json:"event"`
			Receipt setupReceipt `json:"receipt"`
		}{"run_write", receipt})
		if err != nil {
			return err
		}
		s.t.Log(string(event))
	}
	return nil
}

func TestNativeSetupReceiptTraceAndAllowedExitKeepStreamsSeparate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	config := t.TempDir()
	require.NoError(t, os.Chmod(home, 0700))
	require.NoError(t, os.Chmod(config, 0700))
	root := filepath.Join(config, ".nova-friend-setup")
	runner := NativeSetupRunner()
	runner.FS = setupTraceFS{t: t}
	exec := runner.Exec
	runner.Exec = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		out, code, err := exec(ctx, dir, name, args, stdin)
		raw, readErr := runner.FS.ReadFile(filepath.Join(root, "RUN"))
		require.NoError(t, readErr)
		var receipt setupReceipt
		require.NoError(t, json.Unmarshal(raw, &receipt))
		require.Positive(t, receipt.PID)
		assert.NotEmpty(t, receipt.Identity)
		assert.False(t, runner.GroupRunnable(receipt.PID))
		event, marshalErr := json.Marshal(struct {
			Event    string `json:"event"`
			RunID    string `json:"run_id"`
			Runnable bool   `json:"group_runnable"`
		}{"exec_return", receipt.RunID, runner.GroupRunnable(receipt.PID)})
		require.NoError(t, marshalErr)
		t.Log(string(event))
		return out, code, err
	}
	validated := 0
	command := func(program string, args ...string) *SetupCommand {
		return &SetupCommand{Program: program, Args: args, Dir: home, Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Timeout: 5 * time.Second}
	}
	signedOut := command("/bin/sh", "-c", `printf '{"loggedIn":false,"authMethod":"none"}'; printf 'untrusted stderr' >&2; exit 1`)
	signedOut.AllowedExits = []int{0, 1}
	nativePlan := SetupPlan{Harness: "test", Steps: []SetupStep{
		{Name: "environment", Command: command("/usr/bin/env"), Validate: func(out SetupOutput) ([]SetupFact, error) {
			validated++
			assert.Equal(t, 0, out.Exit)
			lines := strings.Fields(out.Stdout)
			values := map[string]string{}
			for _, line := range lines {
				name, value, _ := strings.Cut(line, "=")
				values[name] = value
				assert.Contains(t, []string{"HOME", "PATH", "PWD"}, name, "only the native exec gate may add its public PWD")
				if name == "PWD" {
					assert.Equal(t, home, value)
				}
			}
			// Assert only selected public values; a defective environment
			// inheritance must never print all native environment contents.
			assert.Equal(t, home, values["HOME"])
			assert.Equal(t, "/usr/bin:/bin", values["PATH"])
			t.Log(`{"event":"validated","step":"environment"}`)
			return nil, nil
		}},
		{Name: "signed_out_fixture", Command: signedOut, Validate: func(out SetupOutput) ([]SetupFact, error) {
			validated++
			assert.Equal(t, 1, out.Exit)
			assert.JSONEq(t, `{"loggedIn":false,"authMethod":"none"}`, out.Stdout)
			assert.NotContains(t, out.Stdout, "untrusted stderr")
			t.Log(`{"event":"validated","step":"signed_out_fixture"}`)
			return nil, nil
		}},
	}}
	opts := SetupOptions{Harness: "test", Friend: "fixture", Dir: home + "-work", Home: home, ConfigDir: config, Profile: "isolated", Program: "/usr/bin/env", ProfileOnly: true, DenySelf: []string{home + "-coordinator"}}
	plan, err := PlanSetup(opts, runner.FS, func(SetupOptions, SettingsFS) (SetupPlan, error) { return nativePlan, nil })
	require.NoError(t, err)
	_, err = runner.Apply(context.Background(), plan, root)
	require.NoError(t, err)
	assert.Equal(t, 2, validated)
	profile, err := ReadSetupProfile(runner.FS, SetupProfilePath(config))
	require.NoError(t, err)
	assert.Equal(t, opts, profile.Selection)
	t.Log(`{"event":"profile_published"}`)
}

func TestNativeSetupOutputLimitStopsBeforeValidationAndNextStep(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.Chmod(home, 0700))
	runner := NativeSetupRunner()
	advanced := 0
	plan := SetupPlan{Steps: []SetupStep{{Name: "oversize", Command: &SetupCommand{Program: "/usr/bin/head", Args: []string{"-c", "4194305", "/dev/zero"}, Dir: home, Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Timeout: 5 * time.Second}, Validate: func(SetupOutput) ([]SetupFact, error) { advanced++; return nil, nil }}, {Apply: func() ([]SetupFact, error) { advanced++; return nil, nil }}}}
	_, err := runner.Apply(context.Background(), plan, filepath.Join(t.TempDir(), "setup"))
	require.Error(t, err)
	assert.Zero(t, advanced)
}
