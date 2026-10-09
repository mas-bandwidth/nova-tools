package friend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
 "strings"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// nativeRunDir is the base of daemon-owned work/control receipts. Legacy
// injected daemons without a native control root retain their explicit layout;
// production binds and validates a private root before every walled launch.
func (d *Daemon) nativeRunDir() string {
	if d.NativeControlDir == "" {
		return d.Dir
	}
	return filepath.Join(d.NativeControlDir, "runs")
}

func (d *Daemon) nativeReadRoot() string {
	if d.NativeControlDir == "" {
		return filepath.Join(d.Dir, "reads")
	}
	return filepath.Join(d.NativeControlDir, "reads")
}

func (d *Daemon) nativeReadDir(dir string) string {
	if d.NativeControlDir == "" {
		return dir
	}
	return filepath.Join(d.nativeReadRoot(), filepath.Base(dir))
}

// ValidateNativeControlRoots uses the same owner-only, no-symlink and physical
// nonoverlap checks as the broker. Neither cleanup channels nor durable daemon
// state may be granted to the native child, even through a read-only root.
func (d *Daemon) ValidateNativeControlRoots(roots []string) error {
	if d.NativeControlDir == "" {
		return nativeControlHold{errors.New("native control directory is not bound")}
	}
	if d.NativeControlBinding != nil {
		bound, err := d.NativeControlBinding()
		if err != nil {
			return nativeControlHold{fmt.Errorf("native control binding cannot be verified: %w", err)}
		}
		if bound != d.NativeControlDir {
			return nativeControlHold{errors.New("native control binding changed or cannot be verified; reconcile its durable ownership before launch")}
		}
	}
	// The parent contains the shared durable index, so protect it as well as
	// this namespace's receipts. Backend fixed reads apply to every wall.
	roots = append(append([]string{}, roots...), (&sandbox.Policy{}).VisibleRoots()...)
	for _, root := range []string{filepath.Dir(d.NativeControlDir), d.NativeControlDir, d.StateDir} {
		if root == "" {
			continue
		}
		// Check reach before provisioning: an unsafe request cannot create
		// daemon-owned directories inside a native grant.
		if err := nativeControlOutsideRoots(root, roots); err != nil {
			return nativeControlHold{fmt.Errorf("daemon authority overlaps native wall roots: %w", err)}
		}
		authority := &Daemon{Dir: d.Dir, NativeBrokerDir: root,
			NativeBrokerRoots: func() []string { return roots }}
		if _, err := authority.nativeBrokerRoot(); err != nil {
			return nativeControlHold{fmt.Errorf("native control directory: %w", err)}
		}
		if err := nativeControlOwnedPath(root, true); err != nil {
			return nativeControlHold{err}
		}
	}
	if d.NativeControlBinding != nil && d.StateDir != "" {
		if err := bindNativeControlOwnerState(d.Friend, d.StateDir, d.NativeControlDir); err != nil {
			return nativeControlHold{err}
		}
		if err := bindNativeControlStateForFriend(d.StateDir, d.NativeControlDir, d.Friend); err != nil {
			return nativeControlHold{err}
		}
	}
	return nil
}

func (d *Daemon) validateNativeControl() error {
	if d.NativeControlDir == "" {
		return nil // an injected legacy daemon; production's wall refuses this
	}
	roots := []string{d.Dir}
	if d.NativeControlRoots != nil {
		roots = d.NativeControlRoots()
	}
	return d.ValidateNativeControlRoots(roots)
}

// ValidateLegacyNativeAuthority is a startup fence, not a migration. A sidecar reopens
// its original FIFO pathname, so moving a live channel would lose cancellation.
// Preserve its files and ownership until the old owner drains/reconciles them.
func (d *Daemon) ValidateLegacyNativeAuthority() error {
	if d.NativeControlDir == "" {
		return nil
	}
	legacyState := StateDirIn(d.Dir)
	if filepath.Clean(d.StateDir) != filepath.Clean(legacyState) {
		entries, err := os.ReadDir(legacyState)
		if err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
			return errors.New("legacy in-work daemon state remains; drain and reconcile it into private state before launching")
		}
	}
	for _, kind := range []string{"jobs", "reads"} {
		entries, err := os.ReadDir(filepath.Join(d.Dir, kind))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("legacy native authority cannot be inspected; preserve and reconcile it before launching")
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("legacy native ownership contains a symlink; preserve and reconcile it before launching")
			}
			if !entry.IsDir() {
				continue
			}
			if kind == "reads" {
				legacy := filepath.Join(d.Dir, kind, entry.Name())
				_, found, err := loadReadActive(legacy)
				_, settled, settleErr := loadReadSettlement(legacy)
				_, stopErr := os.Lstat(readStopNoLaunchPath(legacy))
				if found || err != nil || settled || settleErr != nil || !errors.Is(stopErr, os.ErrNotExist) {
					return errors.New("legacy native read ownership remains; drain and reconcile it before launching")
				}
				continue
			}
            raw,laneErr := nativeControlReadLegacyLane(laneMarkPath(d.Dir,entry.Name()))
            if laneErr != nil && !errors.Is(laneErr,os.ErrNotExist) { return errors.New("legacy native lane claim is unreadable or malformed; preserve and reconcile it before launching") }
            if laneErr == nil {
                who,ended := strings.CutPrefix(string(raw),laneEnded)
                if !ended { return errors.New("legacy native lane claim remains unknown; drain and reconcile it before launching") }
                who = strings.TrimSuffix(who,"\n")
                if who == "" || strings.ContainsAny(who,"\r\n") || string(raw) != LaneMarkEnded(who) { return errors.New("legacy native lane claim is unreadable or malformed; preserve and reconcile it before launching") }
            }
			if _, err := os.Lstat(runReceiptPath(d.Dir, entry.Name())); errors.Is(err, os.ErrNotExist) {
				if _, anchorErr := os.Lstat(runReceiptPath(d.Dir, entry.Name()) + ".anchor"); errors.Is(anchorErr, os.ErrNotExist) {
					continue
				}
				return errors.New("legacy native anchor lacks a receipt; preserve and reconcile it before launching")
			} else if err != nil {
				return errors.New("legacy native anchor cannot be inspected; preserve it before launching")
			}
			r, err := readRunReceipt(d.Dir, entry.Name())
			if err != nil || r.PID <= 0 || r.Identity == "" || procgroup.GroupRunnable(r.PID) {
				return errors.New("legacy native cleanup authority remains live or unknown; drain and reconcile it before launching")
			}
		}
	}
	return nil
}

// A refused authority root is an unlaunched reservation, not a provider failure.
// The existing held-work and deferred-message paths retry it without spending
// a work attempt or acknowledging unread requests.
type nativeControlHold struct{ cause error }

func (e nativeControlHold) Error() string        { return "native control isolation: " + e.cause.Error() }
func (e nativeControlHold) Is(target error) bool { return target == errMachineLaunchHeld }
func (e nativeControlHold) Unwrap() error        { return Deferred{Reason: e.Error()} }

// Inside asks the filesystem about physical ancestors, including casefold and
// bind aliases. Only absent backend roots (which the backend also skips) may be
// omitted; an unresolved caller grant cannot be treated as safely unrelated.
func nativeControlOutsideRoots(root string, roots []string) error {
	if err := nativeBrokerOutsideRoots(root, roots); err != nil {
		return err
	}
	for _, allowed := range roots {
		if allowed == "" {
			continue
		}
		if _, err := os.Stat(allowed); err != nil {
			optional := false
			for _, backend := range (&sandbox.Policy{}).VisibleRoots() {
				optional = optional || backend == allowed
			}
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("native control cannot resolve wall root %s: %w", allowed, err)
		}
		if sandbox.Inside(root, allowed) || sandbox.Inside(allowed, root) {
			return errors.New("native control physically overlaps a wall-visible root")
		}
	}
	return nil
}
