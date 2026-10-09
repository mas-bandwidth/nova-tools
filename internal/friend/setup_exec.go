package friend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/tty"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
)

type execEnvironmentKey struct{}
type execOutputLimitKey struct{}
type execSeparateOutputKey struct{}
type setupStderrOutputKey struct{}

const SetupOutputLimit = 4 << 20

// SetupRunner applies an inspected plan locally. Native receipts precede the
// launch gate; a restart holds an unverified group rather than launching twice.
// See SPEC-FRIEND.md, Harness setup and tla/FriendSetupLifecycle.tla.
type SetupRunner struct {
	FS            SettingsFS
	Exec          Exec
	Identity      func(int) string
	GroupRunnable func(int) bool
	Lock          func(string) (func() error, error)
	Nonce         func() string
}

func NativeSetupRunner() SetupRunner {
	return SetupRunner{FS: OSFS{}, Exec: RealExec, Identity: ProcessIdentity,
		GroupRunnable: procgroup.GroupRunnable, Nonce: setupNonce,
		Lock: func(path string) (func() error, error) {
			lock, err := filelock.TryLock(path, "harness setup")
			if err != nil {
				return nil, err
			}
			return lock.Unlock, nil
		},
	}
}

func setupNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

type setupReceipt struct {
	RunID    string `json:"run_id"`
	Command  string `json:"command_sha256"`
	PID      int    `json:"pid"`
	Identity string `json:"identity"`
	Ended    bool   `json:"ended"`
}

// SetupPath checks every parent, including missing paths, without following a
// symlink. Explicit apply may create a missing directory after this inspection.
func SetupPath(fs SettingsFS, path string, directory bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return errors.New("setup wants a clean absolute path below the filesystem root; name the owned real path")
	}
	for p := path; ; p = filepath.Dir(p) {
		e, err := fs.Lstat(p)
		if err != nil {
			return err
		}
		want := KindDir
		if p == path && !directory {
			want = KindFile
		}
		if e.Kind != KindMissing && e.Kind != want {
			return fmt.Errorf("setup path %s is %s; name a real %s without symlink parents", p, e.Kind, want)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// Apply serializes the profile root, retains raw native output only in memory,
// and advances only after proven group exit and successful native validation.
func (r SetupRunner) Apply(ctx context.Context, plan SetupPlan, root string) (out SetupPlan, err error) {
	out = plan
	if r.FS == nil || r.Exec == nil || r.Identity == nil || r.GroupRunnable == nil || r.Lock == nil || r.Nonce == nil {
		return out, errors.New("setup runner has incomplete native safety hooks; use the native runner or a complete test twin")
	}
	for _, step := range plan.Steps {
		if step.Command != nil {
			if step.Command.ConsentOnTerminal {
				terminal, _ := ctx.Value(codexConsentTerminalKey{}).(*os.File)
				if terminal == nil || !tty.IsTerminal(terminal) {
					return out, errors.New("native enrollment requires the person's terminal; run setup interactively")
				}
			}
			if err = validateSetupCommand(*step.Command); err != nil {
				return out, err
			}
		}
	}
	roots := append([]string{root}, plan.Roots...)
	slices.Sort(roots)
	roots = slices.Compact(roots)
	var unlocks []func() error
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			err = errors.Join(err, unlocks[i]())
		}
	}()
	for _, owned := range roots {
		if err = SetupPath(r.FS, owned, true); err != nil {
			return out, err
		}
		if err = r.FS.MkdirAll(owned, 0700); err != nil {
			return out, err
		}
		lockPath := filepath.Join(owned, "LOCK")
		if err = SetupPath(r.FS, lockPath, false); err != nil {
			return out, err
		}
		unlock, lockErr := r.Lock(lockPath)
		if lockErr != nil {
			return out, fmt.Errorf("setup profile is busy; wait for its current initializer: %w", lockErr)
		}
		unlocks = append(unlocks, unlock)
	}
	receiptPath := filepath.Join(root, "RUN")
	if err = SetupPath(r.FS, receiptPath, false); err != nil {
		return out, err
	}
	raw, readErr := r.FS.ReadFile(receiptPath)
	if readErr == nil {
		var old setupReceipt
		if json.Unmarshal(raw, &old) != nil || old.RunID == "" || old.Command == "" || old.PID < 0 || (old.PID > 0 && old.Identity == "") {
			return out, errors.New("setup receipt is malformed; inspect the owned RUN receipt before retrying")
		}
		if old.PID > 0 && r.GroupRunnable(old.PID) {
			return out, errors.New("setup native group remains live or unverified; wait for verified exit before retrying")
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return out, readErr
	}
	for _, step := range plan.Steps {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var facts []SetupFact
		if step.Command != nil {
			facts, err = r.native(ctx, step, receiptPath)
		} else if step.Apply != nil {
			facts, err = step.Apply()
		} else if step.Validate != nil {
			facts, err = step.Validate(SetupOutput{})
		}
		out.Facts = append(out.Facts, facts...)
		if err != nil {
			return out, fmt.Errorf("setup %s: %w", step.Name, err)
		}
	}
	return out, nil
}

// native implements Prepare, BindReceipt, Launch and Validate after proven group
// exit in tla/FriendSetupLifecycle.tla (docs/SPEC-FRIEND.md, Harness setup).
func (r SetupRunner) native(ctx context.Context, step SetupStep, path string) ([]SetupFact, error) {
	c := step.Command
	for _, item := range c.Env {
		name, value, _ := strings.Cut(item, "=")
		if name == "HOME" || name == "CLAUDE_CONFIG_DIR" || name == "CODEX_HOME" || name == "DSH_HOME" || name == "OPENCODE_CONFIG_DIR" {
			if err := setupPrivateRoot(r.FS, value); err != nil {
				return nil, err
			}
			e, err := r.FS.Lstat(value)
			if err != nil {
				return nil, err
			}
			if e.Kind != KindDir {
				return nil, errors.New("native setup private root is missing; apply the directory step before native initialization")
			}
		}
	}
	bytes, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(bytes)
	receipt := setupReceipt{RunID: r.Nonce(), Command: hex.EncodeToString(hash[:])}
	if receipt.RunID == "" {
		return nil, errors.New("setup run identity is unavailable; retry after native randomness is available")
	}
	save := func() error {
		bytes, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return r.FS.WriteFile(path, append(bytes, '\n'), 0600)
	}
	if err = save(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	ctx = context.WithValue(ctx, execEnvironmentKey{}, append([]string{}, c.Env...))
	ctx = context.WithValue(ctx, execOutputLimitKey{}, SetupOutputLimit)
	ctx = context.WithValue(ctx, execSeparateOutputKey{}, true)
	ctx = context.WithValue(ctx, setupStderrOutputKey{}, c.StatusOnStderr)
	if !c.ConsentOnTerminal {
		ctx = context.WithValue(ctx, codexConsentTerminalKey{}, (*os.File)(nil))
	}
	ctx = WithProcessGroupAnchor(ctx, path+"."+receipt.RunID+".anchor")
	ctx = WithProcessStarted(ctx, func(pid int) error {
		receipt.PID, receipt.Identity = pid, r.Identity(pid)
		if pid <= 0 || receipt.Identity == "" {
			return errors.New("setup native birth identity is unavailable")
		}
		return save()
	})
	stdout, exit, runErr := r.Exec(ctx, c.Dir, c.Program, c.Args, "")
	if receipt.PID > 0 && r.GroupRunnable(receipt.PID) {
		return nil, errors.New("native setup group exit is unverified; inspect RUN and wait before retrying")
	}
	if receipt.PID == 0 && runErr == nil {
		return nil, errors.New("native setup did not bind its process receipt; correct the native executor")
	}
	receipt.Ended = true
	if err = save(); err != nil {
		return nil, err
	}
	if len(stdout) > SetupOutputLimit {
		return nil, errors.New("native setup output exceeds four MiB; use a bounded native inspection")
	}
	if errors.Is(runErr, errNativeOutputLimit) {
		return nil, errNativeOutputLimit // fixed safe text; never include native output or credentials
	}
	allowed := exit == 0
	if len(c.AllowedExits) > 0 {
		allowed = slices.Contains(c.AllowedExits, exit)
	}
	if runErr != nil || !allowed || ctx.Err() != nil {
		return nil, fmt.Errorf("native setup exited %d or could not complete; inspect the native program without supplying credentials on the command line", exit)
	}
	if step.Validate != nil {
		return step.Validate(SetupOutput{Stdout: stdout, Exit: exit})
	}
	return []SetupFact{{Name: step.Name, State: "completed"}}, nil
}

func validateSetupCommand(c SetupCommand) error {
	limit := 2 * time.Minute
	if c.ConsentOnTerminal {
		limit = 15 * time.Minute
	}
	if !filepath.IsAbs(c.Program) || c.Timeout <= 0 || c.Timeout > limit || len(c.Env) == 0 || !filepath.IsAbs(c.Dir) {
		return errors.New("native setup wants absolute program/directory, complete environment and bounded timeout (two minutes for inspection, fifteen for consent); correct the plan")
	}
	seen := map[string]bool{}
	var codexHome string
	for _, item := range c.Env {
		name, _, found := strings.Cut(item, "=")
		allowed := name == "HOME" || name == "PATH" || name == "LANG" || name == "TMPDIR" || name == "XDG_CONFIG_HOME" || name == "XDG_CACHE_HOME" || name == "XDG_DATA_HOME" || name == "XDG_STATE_HOME" || name == "CODEX_HOME" || name == "DSH_HOME" || name == "CLAUDE_CONFIG_DIR" || name == "OPENCODE_CONFIG_DIR" || name == "OPENCODE_CONFIG"
		if !found || !allowed || seen[name] {
			return errors.New("native setup environment contains an inherited credential, unsupported name or duplicate; use only explicit home/config/runtime paths")
		}
		seen[name] = true
		if name == "CODEX_HOME" {
			_, codexHome, _ = strings.Cut(item, "=")
		}
	}
	if c.ConsentOnTerminal && (!slices.Equal(c.Args, []string{"-c", "log_dir=" + strconv.Quote(filepath.Join(codexHome, "log")), "login", "--device-auth"}) || !seen["CODEX_HOME"]) {
		return errors.New("terminal consent supports only isolated native Codex device login; correct the inspected plan")
	}
	if !seen["HOME"] || !seen["PATH"] {
		return errors.New("native setup environment wants explicit HOME and PATH; name the private native home and system executable path")
	}
	return nil
}
