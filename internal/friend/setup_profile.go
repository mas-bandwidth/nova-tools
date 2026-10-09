package friend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// SetupProfile is a versioned public selection, never an account or presence
// proof. Only a completed local setup writes it; install/run read it again.
type SetupProfile struct {
	Version   int          `json:"version"`
	Selection SetupOptions `json:"selection"`
	RunID     string       `json:"run_id,omitempty"`
}

func SetupProfilePath(configDir string) string {
	return filepath.Join(configDir, ".nova-friend-setup", "PROFILE")
}

// writeSetupProfile is the final Publish action in tla/FriendSetupLifecycle.tla.
func writeSetupProfile(fs SettingsFS, opts SetupOptions) ([]SetupFact, error) {
	path := SetupProfilePath(opts.ConfigDir)
	if err := SetupPath(fs, path, false); err != nil {
		return nil, err
	}
	if ClaudeTokenSetupRoute(opts) {
		if err := claudeOwnedAdmission(opts, fs); err != nil {
			return nil, err
		}
	}
	runID, err := setupCompletedRun(fs, opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(SetupProfile{Version: 1, Selection: opts, RunID: runID})
	if err != nil {
		return nil, err
	}
	if err = fs.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		return nil, err
	}
	readback, err := fs.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(bytes.TrimSpace(readback), raw) {
		return nil, errors.New("setup selection readback differs; inspect the owned PROFILE before installing")
	}
	return []SetupFact{{Name: "setup_profile", State: "written", Detail: path}}, nil
}

// ReadSetupProfile validates the binding and every owned path at use time.
// It reads public setup metadata only, never native credentials.
func ReadSetupProfile(fs SettingsFS, path string) (SetupProfile, error) {
	var profile SetupProfile
	if err := SetupPath(fs, path, false); err != nil {
		return profile, err
	}
	raw, err := fs.ReadFile(path)
	if err != nil {
		return profile, err
	}
	if len(raw) > 16384 {
		return profile, errors.New("setup profile is oversized; inspect the bounded public selection")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&profile); err != nil {
		return profile, errors.New("setup profile is malformed; rerun explicit setup inspection")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || profile.Version != 1 || path != SetupProfilePath(profile.Selection.ConfigDir) {
		return profile, errors.New("setup profile has an unsupported version or location; use the completed setup's PROFILE path")
	}
	opts := profile.Selection
	runID, err := setupCompletedRun(fs, opts.ConfigDir)
	if err != nil {
		return profile, err
	}
	if runID != profile.RunID {
		return profile, errors.New("setup PROFILE does not certify the latest native attempt; finish explicit setup successfully before activation")
	}
	if _, err = PlanSetup(opts, fs, func(SetupOptions, SettingsFS) (SetupPlan, error) { return SetupPlan{Harness: opts.Harness}, nil }); err != nil {
		return profile, err
	}
	if ClaudeTokenSetupRoute(opts) {
		if err := claudeOwnedAdmission(opts, fs); err != nil {
			return profile, err
		}
	}
	ownerPath := filepath.Join(opts.ConfigDir, ".nova-friend-setup", "OWNER")
	owner, err := fs.Lstat(ownerPath)
	if err != nil {
		return profile, err
	}
	if owner.Kind != KindFile {
		return profile, errors.New("setup profile has no durable owner; rerun explicit setup apply")
	}
	entry, err := fs.Lstat(path)
	if err != nil {
		return profile, err
	}
	if entry.Mode.Perm()&0077 != 0 {
		return profile, errors.New("setup profile is not private; preserve it and rerun setup under owner-only roots")
	}
	return profile, nil
}

// setupCompletedRun binds Publish to the last native attempt. A previous
// PROFILE cannot certify a retry after prepare, execution or publication fails.
func setupCompletedRun(filesystem SettingsFS, configDir string) (string, error) {
	path := filepath.Join(configDir, ".nova-friend-setup", "RUN")
	if err := SetupPath(filesystem, path, false); err != nil {
		return "", err
	}
	raw, err := filesystem.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var receipt setupReceipt
	if len(raw) > 16384 || json.Unmarshal(raw, &receipt) != nil || receipt.RunID == "" || receipt.PID <= 0 || receipt.Identity == "" || !receipt.Ended {
		return "", errors.New("setup native attempt is incomplete or unverified; finish explicit setup before activation")
	}
	return receipt.RunID, nil
}

// ValidateUse refuses mismatched owners and incomplete local configuration.
// Profile-only initialization does not select a route and cannot activate work.
func (p SetupProfile) ValidateUse(friend, harness, dir string, fs SettingsFS, planner SetupPlanner) error {
	o := p.Selection
	if o.Friend != friend || o.Harness != harness || o.Dir != dir {
		return errors.New("setup profile owner/harness/working directory does not match this invocation; select the matching owned PROFILE")
	}
	if o.ProfileOnly {
		return errors.New("setup profile initializes local files/auth only and has no selected model route; configure an explicitly supported provider/model route before activation")
	}
	if ClaudeTokenSetupRoute(o) {
		if err := claudeOwnedAdmission(o, fs); err != nil {
			return err
		}
	}
	plan, err := planner(o, fs)
	if err != nil {
		return err
	}
	for _, step := range plan.Steps {
		if step.Apply != nil {
			return errors.New("setup profile configuration has drifted or is incomplete; rerun its explicit setup before activation")
		}
		if step.Command != nil && p.RunID == "" {
			return errors.New("setup profile lacks its completed native receipt; finish explicit setup before activation")
		}
	}
	return nil
}

// Environment is the selected public harness environment. Credential values
// are supplied separately by nova-secrets to explicitly named variables.
func (p SetupProfile) Environment(path string) []string {
	o := p.Selection
	env := []string{"HOME=" + o.Home, "PATH=" + path, "XDG_CONFIG_HOME=" + filepath.Join(o.Home, ".config"), "XDG_DATA_HOME=" + filepath.Join(o.Home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(o.Home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(o.Home, ".local/state")}
	switch o.Harness {
	case "opencode":
		env = append(env, "OPENCODE_CONFIG="+filepath.Join(o.Dir, OpenCodeConfig), "OPENCODE_CONFIG_DIR="+o.ConfigDir)
	case "codex":
		env = append(env, "CODEX_HOME="+o.ConfigDir)
	case "claude":
		env = append(env, "CLAUDE_CONFIG_DIR="+o.ConfigDir)
	case "dsh":
		env = append(env, "DSH_HOME="+o.ConfigDir)
	}
	return env
}

// Exec binds all native adapter commands, including non-lane session reads, to
// the selected environment. Only named secrets pass from the launcher's seat.
func (p SetupProfile) Exec(run Exec, getenv func(string) string) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if ClaudeTokenSetupRoute(p.Selection) {
			if err := claudeOwnedAdmission(p.Selection, OSFS{}); err != nil {
				return "", 125, err
			}
		}
		env := p.Environment(getenv("PATH"))
		for _, key := range p.Selection.SecretNames {
			env = append(env, key+"="+getenv(key))
		}
		if name == p.Selection.Harness || (p.Selection.Harness == "dsh" && name == DSHProgram) {
			name = p.Selection.Program
		}
		// The Claude adapter uses native env to name its isolated config root.
		if name == "env" {
			args = slices.Clone(args)
			for i, arg := range args {
				if arg == "claude" {
					args[i] = p.Selection.Program
					break
				}
			}
		}
		ctx = context.WithValue(ctx, execEnvironmentKey{}, env)
		return run(ctx, dir, name, args, stdin)
	}
}

func (p SetupProfile) Said() string {
	return fmt.Sprintf("owned %s/%s profile %s at %s", p.Selection.Friend, p.Selection.Harness, p.Selection.Profile, p.Selection.ConfigDir)
}

func setupEnvMap(env []string) map[string]string {
	out := map[string]string{}
	for _, item := range env {
		name, value, _ := strings.Cut(item, "=")
		out[name] = value
	}
	return out
}
