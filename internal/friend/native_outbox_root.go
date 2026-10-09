package friend

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ensureNativeOutbox prepares the single handoff directory shared by local
// reply and pong. The native verbs still require this real directory and never
// create it themselves; setup and daemon startup own its lifecycle.
func ensureNativeOutbox(fsys SettingsFS, dir string) error {
	if fsys == nil {
		return errors.New("native outbox needs a filesystem")
	}
	if err := SetupPath(fsys, dir, true); err != nil {
		return fmt.Errorf("native outbox work root: %w", err)
	}
	work, err := fsys.Lstat(dir)
	if err != nil {
		return err
	}
	if work.Kind != KindDir {
		return errors.New("native outbox needs a real friend working directory")
	}
	outbox := filepath.Join(dir, "outbox")
	if err := SetupPath(fsys, outbox, true); err != nil {
		return fmt.Errorf("native outbox path: %w", err)
	}
	if err := fsys.MkdirAll(outbox, 0o700); err != nil {
		return fmt.Errorf("native outbox cannot be created: %w", err)
	}
	if err := SetupPath(fsys, outbox, true); err != nil {
		return fmt.Errorf("native outbox path after creation: %w", err)
	}
	return nil
}
