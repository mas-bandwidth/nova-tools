package friend

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupNativeReceiptPrecedesLaunchAndHoldsLiveRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		live      bool
		saveFails bool
	}{{"complete", false, false}, {"live", true, false}, {"missing-birth", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := NewMemFS()
			require.NoError(t, fs.MkdirAll("/private", 0700))
			root := filepath.Join(t.TempDir(), "state")
			runner := SetupRunner{FS: fs, Identity: func(int) string {
				if tc.saveFails {
					return ""
				}
				return "birth"
			}, GroupRunnable: func(int) bool { return tc.live }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "run" }}
			calls, checked := 0, 0
			runner.Exec = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				calls++
				assert.Empty(t, stdin)
				assert.Equal(t, []string{"HOME=/private", "PATH=/usr/bin:/bin"}, ctx.Value(execEnvironmentKey{}))
				err := processStarted(ctx)(42)
				if err != nil {
					return "", 0, err
				}
				raw, err := fs.ReadFile(filepath.Join(root, "RUN"))
				require.NoError(t, err)
				var receipt setupReceipt
				require.NoError(t, json.Unmarshal(raw, &receipt))
				assert.Equal(t, 42, receipt.PID)
				assert.Equal(t, "birth", receipt.Identity)
				assert.NotEmpty(t, receipt.Command)
				assert.False(t, receipt.Ended)
				assert.Contains(t, processGroupAnchor(ctx), "run.anchor")
				return `{"schema":true}`, 0, nil
			}
			plan := SetupPlan{Harness: "test", Steps: []SetupStep{{Name: "schema", Command: &SetupCommand{Program: "/native", Args: []string{"schema"}, Dir: "/private", Env: []string{"HOME=/private", "PATH=/usr/bin:/bin"}, Timeout: time.Minute}, Validate: func(out SetupOutput) ([]SetupFact, error) {
				checked++
				assert.JSONEq(t, `{"schema":true}`, out.Stdout)
				return []SetupFact{{Name: "schema", State: "checked"}}, nil
			}}}}
			_, err := runner.Apply(context.Background(), plan, root)
			if tc.live || tc.saveFails {
				require.Error(t, err)
				assert.Zero(t, checked)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, checked)
			}
			assert.Equal(t, 1, calls)
			if tc.live {
				_, err = runner.Apply(context.Background(), plan, root)
				require.ErrorContains(t, err, "remains live")
				assert.Equal(t, 1, calls, "restart cannot duplicate a live native initializer")
			}
		})
	}
}

func TestSetupRefusesUnsafePathsAndCredentialEnvironmentBeforeWrites(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	fs.Symlink("/elsewhere", "/owned")
	assert.ErrorContains(t, SetupPath(fs, "/owned/config", true), "symlink")
	calls := 0
	r := SetupRunner{FS: fs, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
		calls++
		return "", 0, nil
	}, Identity: func(int) string { return "x" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "x" }}
	plan := SetupPlan{Steps: []SetupStep{{Apply: func() ([]SetupFact, error) { calls++; return nil, nil }}, {Command: &SetupCommand{Program: "/native", Dir: "/private", Timeout: time.Minute, Env: []string{"HOME=/private", "PATH=/bin", "ANTHROPIC_API_KEY=value"}}}}}
	_, err := r.Apply(context.Background(), plan, "/state")
	require.ErrorContains(t, err, "credential")
	assert.Zero(t, calls)
	entry, err := fs.Lstat("/state")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
}

func TestSetupPlanIsReadOnlyAndPreservesOtherOwner(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	opts := SetupOptions{Harness: "test", Friend: "worker", Dir: "/work", ConfigDir: "/config", Home: "/private", Program: "/native", Profile: "isolated", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
	writes := 0
	planner := func(SetupOptions, SettingsFS) (SetupPlan, error) {
		return SetupPlan{Harness: "test", Steps: []SetupStep{{Name: "write", Apply: func() ([]SetupFact, error) { writes++; return nil, nil }}}}, nil
	}
	plan, err := PlanSetup(opts, fs, planner)
	require.NoError(t, err)
	assert.Zero(t, writes)
	entry, err := fs.Lstat("/config")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
	_, err = plan.Steps[0].Apply()
	require.NoError(t, err)
	opts.Friend = "other"
	_, err = PlanSetup(opts, fs, planner)
	require.ErrorContains(t, err, "another selection")
	assert.Zero(t, writes)
	opts.Friend = "worker"
	opts.KeyEnv = "API_KEY=value"
	_, err = PlanSetup(opts, fs, planner)
	assert.Error(t, err)
}

func TestSetupDoesNotAdvanceOnNativeFailureOrUnverifiedReceipt(t *testing.T) {
	t.Parallel()
	for _, fail := range []string{"exit", "error", "missing-pid", "malformed-recovery"} {
		t.Run(fail, func(t *testing.T) {
			t.Parallel()
			fs := NewMemFS()
			require.NoError(t, fs.MkdirAll("/private", 0700))
			root := filepath.Join(t.TempDir(), "state")
			if fail == "malformed-recovery" {
				require.NoError(t, fs.MkdirAll(root, 0700))
				require.NoError(t, fs.WriteFile(filepath.Join(root, "RUN"), []byte(`{bad`), 0600))
			}
			later := 0
			r := SetupRunner{FS: fs, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "x" }}
			r.Exec = func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
				if fail != "missing-pid" {
					require.NoError(t, processStarted(ctx)(42))
				}
				if fail == "error" {
					return "sensitive output", 0, errors.New("credential value")
				}
				if fail == "exit" {
					return "sensitive output", 1, nil
				}
				return "", 0, nil
			}
			plan := SetupPlan{Steps: []SetupStep{{Name: "native", Command: &SetupCommand{Program: "/native", Dir: "/private", Env: []string{"HOME=/private", "PATH=/bin"}, Timeout: time.Minute}}, {Apply: func() ([]SetupFact, error) { later++; return nil, nil }}}}
			_, err := r.Apply(context.Background(), plan, root)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "sensitive output")
			assert.NotContains(t, err.Error(), "credential value")
			assert.Zero(t, later)
		})
	}
}
