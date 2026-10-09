package friend

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Names reads directory entry names only, never account or credential contents.
func (OSFS) Names(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func (m *MemFS) Names(path string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodes[path].kind != KindDir {
		return nil, errors.New("directory inspection wants a real directory")
	}
	var names []string
	for p := range m.nodes {
		if p != path && filepath.Dir(p) == path {
			names = append(names, filepath.Base(p))
		}
	}
	slices.Sort(names)
	return names, nil
}

func setupPrivateRoot(fs SettingsFS, path string) error {
	if err := SetupPath(fs, path, true); err != nil {
		return err
	}
	e, err := fs.Lstat(path)
	if err != nil {
		return err
	}
	if e.Kind == KindDir && (e.Mode.Perm()&0077 != 0 || e.Mode.Perm()&0700 != 0700) {
		return errors.New("setup private root is not owner-only; preserve it and name a fresh private root with mode 0700")
	}
	return nil
}

func setupReservedEnv(name string) bool {
	return name == "HOME" || name == "PATH" || name == "LANG" || name == "TMPDIR" || strings.HasPrefix(name, "XDG_") || name == "DSH_HOME" || name == "CLAUDE_CONFIG_DIR" || name == "OPENCODE_CONFIG_DIR" || name == "OPENCODE_CONFIG" || strings.HasPrefix(name, "NOVA_FRIEND_") || strings.HasPrefix(name, "NOVA_SPRINT_") || strings.HasPrefix(name, "NOVA_BUS_")
}

func setupUnownedRoot(fs SettingsFS, path string) error {
	e, err := fs.Lstat(path)
	if err != nil || e.Kind == KindMissing {
		return err
	}
	lister, ok := fs.(interface {
		Names(string) ([]string, error)
	})
	if !ok {
		return errors.New("setup cannot inspect unowned profile names; use a filesystem with names-only directory inspection")
	}
	names, err := lister.Names(path)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != ".nova-friend-setup" {
			return errors.New("setup config root is nonempty and unowned; preserve existing account state and name a fresh private root")
		}
	}
	// A failed prelaunch OWNER write may leave only the lock directory. A
	// public binding or native receipt without OWNER is not adopted on retry.
	state := filepath.Join(path, ".nova-friend-setup")
	e, err = fs.Lstat(state)
	if err != nil || e.Kind == KindMissing {
		return err
	}
	if err = SetupPath(fs, state, true); err != nil {
		return err
	}
	names, err = lister.Names(state)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != "LOCK" {
			return errors.New("setup state is unowned; preserve it and name a fresh private root")
		}
	}
	return nil
}
