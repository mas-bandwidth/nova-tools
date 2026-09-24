package wake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WakeBrokenNoteNotFound is the prefix used when a note file cannot be found or validated.
// Under Johnny's boundary: "A missing file is WAKE BROKEN, not a blind inbox.
// The packet names the note, not a command."
const WakeBrokenNoteNotFound = "WAKE BROKEN reason=note-file-not-found"

// Forbidden shell executables. A wake daemon must never execute a shell command.
var forbiddenShells = map[string]bool{
	"sh":   true,
	"bash": true,
	"zsh":  true,
	"csh":  true,
	"tcsh": true,
	"ksh":  true,
	"fish": true,
	"dash": true,
}

// Forbidden swarm verbs. A wake daemon must never execute swarm pipeline verbs.
var forbiddenSwarmVerbs = map[string]bool{
	"harvest": true,
	"fill":    true,
	"native":  true,
	"merge":   true,
}

// IsForbiddenShell reports whether a binary name represents a shell.
func IsForbiddenShell(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	base = strings.TrimSuffix(base, ".exe")
	return forbiddenShells[base]
}

// IsForbiddenSwarmVerb reports whether an argument represents a forbidden swarm verb.
func IsForbiddenSwarmVerb(arg string) bool {
	return forbiddenSwarmVerbs[strings.ToLower(arg)]
}

// ValidateDaemonArgv enforces Johnny's argv-only path boundary:
// - Direct argv execution only (no shells allowed as binary or arguments)
// - Reject -c flag
// - Reject --bodies (must not dump bodies)
// - Reject --allow-private (must not bypass private bus boundaries)
// - Reject --decide (decider invocation is forbidden)
// - Reject secondary --as (must not start a second --as or switch identity)
// - Reject swarm verbs (harvest, fill, native, merge)
func ValidateDaemonArgv(argv []string, targetAs string) error {
	if len(argv) == 0 {
		return errors.New("wake daemon boundary: empty argv")
	}

	bin := filepath.Base(argv[0])
	binLower := strings.ToLower(strings.TrimSuffix(bin, ".exe"))

	// 1. Reject shells as binary
	if IsForbiddenShell(binLower) {
		return fmt.Errorf("wake daemon boundary: shell execution is forbidden (%s)", argv[0])
	}

	// 2. Reject swarm binaries
	if binLower == "nova-swarm" || binLower == "nova-merge" {
		return fmt.Errorf("wake daemon boundary: swarm binary %s is forbidden", bin)
	}

	// 3. Reject nova-decide as binary
	if binLower == "nova-decide" {
		return errors.New("wake daemon boundary: nova-decide binary is forbidden")
	}

	var asCount int

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		argLower := strings.ToLower(arg)

		// Check if any argument is a shell executable
		if i > 0 && IsForbiddenShell(argLower) {
			return fmt.Errorf("wake daemon boundary: shell reference %q is forbidden in argv", arg)
		}

		// Reject -c
		if arg == "-c" || strings.HasPrefix(arg, "-c=") {
			return errors.New("wake daemon boundary: -c flag is forbidden")
		}

		// Reject --bodies
		if arg == "--bodies" || strings.HasPrefix(arg, "--bodies=") {
			return errors.New("wake daemon boundary: --bodies flag is forbidden (must not dump bodies)")
		}

		// Reject --allow-private
		if arg == "--allow-private" || strings.HasPrefix(arg, "--allow-private=") {
			return errors.New("wake daemon boundary: --allow-private flag is forbidden")
		}

		// Reject --decide
		if arg == "--decide" || strings.HasPrefix(arg, "--decide=") {
			return errors.New("wake daemon boundary: --decide is forbidden")
		}

		// Reject swarm verbs
		if IsForbiddenSwarmVerb(argLower) {
			return fmt.Errorf("wake daemon boundary: swarm verb %q is forbidden", arg)
		}

		// Track and reject secondary --as
		if arg == "--as" {
			asCount++
			if asCount > 1 {
				return errors.New("wake daemon boundary: secondary --as is forbidden")
			}
			if i+1 < len(argv) {
				asVal := argv[i+1]
				if targetAs != "" && !strings.EqualFold(asVal, targetAs) {
					return fmt.Errorf("wake daemon boundary: secondary --as %q does not match identity %q", asVal, targetAs)
				}
			}
		} else if strings.HasPrefix(arg, "--as=") {
			asCount++
			if asCount > 1 {
				return errors.New("wake daemon boundary: secondary --as is forbidden")
			}
			asVal := strings.TrimPrefix(arg, "--as=")
			if targetAs != "" && !strings.EqualFold(asVal, targetAs) {
				return fmt.Errorf("wake daemon boundary: secondary --as %q does not match identity %q", asVal, targetAs)
			}
		}
	}

	return nil
}

// ValidateNotePath checks that the note path exists and is a regular file on the filesystem.
// If the file is missing or invalid, it returns an error prefixed with
// "WAKE BROKEN reason=note-file-not-found".
func ValidateNotePath(notePath string) error {
	trimmed := strings.TrimSpace(notePath)
	if trimmed == "" {
		return fmt.Errorf("%s: note path is empty", WakeBrokenNoteNotFound)
	}

	st, err := os.Stat(trimmed)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: file %q does not exist", WakeBrokenNoteNotFound, trimmed)
		}
		return fmt.Errorf("%s: failed to stat %q: %w", WakeBrokenNoteNotFound, trimmed, err)
	}

	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %q is not a regular file (mode %s)", WakeBrokenNoteNotFound, trimmed, st.Mode())
	}

	return nil
}

// ValidateNotePaths validates a slice of note paths.
func ValidateNotePaths(notePaths []string) error {
	if len(notePaths) == 0 {
		return fmt.Errorf("%s: no note paths provided", WakeBrokenNoteNotFound)
	}
	for _, p := range notePaths {
		if err := ValidateNotePath(p); err != nil {
			return err
		}
	}
	return nil
}

// DaemonConfig holds settings for a wake daemon instance.
type DaemonConfig struct {
	As          string
	Command     []string
	SlotManager *SlotLeaseManager
	Stdout      io.Writer
	Stderr      io.Writer
}

// Daemon enforces Johnny's argv-only path boundary when executing wake commands.
type Daemon struct {
	as          string
	command     []string
	slotManager *SlotLeaseManager
	stdout      io.Writer
	stderr      io.Writer
}

// NewDaemon creates and validates a new Daemon instance.
func NewDaemon(cfg DaemonConfig) (*Daemon, error) {
	if err := ValidateDaemonArgv(cfg.Command, cfg.As); err != nil {
		return nil, err
	}
	return &Daemon{
		as:          cfg.As,
		command:     append([]string{}, cfg.Command...),
		slotManager: cfg.SlotManager,
		stdout:      cfg.Stdout,
		stderr:      cfg.Stderr,
	}, nil
}

// BuildCommand constructs an exec.Cmd for the daemon using direct argv execution.
// It verifies the argv boundary and validates that all notePaths exist as regular files.
func (d *Daemon) BuildCommand(ctx context.Context, notePaths ...string) (*exec.Cmd, error) {
	if err := ValidateDaemonArgv(d.command, d.as); err != nil {
		return nil, err
	}
	if err := ValidateNotePaths(notePaths); err != nil {
		return nil, err
	}

	// Direct argv execution: binary is d.command[0], args are d.command[1:] + notePaths.
	// No shell wrapping, no -c string interpolation.
	args := append(append([]string{}, d.command[1:]...), notePaths...)
	cmd := exec.CommandContext(ctx, d.command[0], args...)
	cmd.Stdout = d.stdout
	cmd.Stderr = d.stderr
	return cmd, nil
}

// Dispatch executes the daemon's command with the validated notePaths.
func (d *Daemon) Dispatch(ctx context.Context, notePaths ...string) error {
	cmd, err := d.BuildCommand(ctx, notePaths...)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// Close releases any held slot lease.
func (d *Daemon) Close() error {
	if d.slotManager != nil {
		return d.slotManager.Release()
	}
	return nil
}
