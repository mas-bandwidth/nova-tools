package friend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

type nativeControlStateBinding struct {
	Friend string
	Path   string
	ID     nativeWorkID
}

func nativeControlStatePath(root, name string) string {
	key := sha256.Sum256([]byte(name))
	return filepath.Join(root, "STATE-"+hex.EncodeToString(key[:])+".json")
}

// Each friend's namespace binds one physical state before CONTROL-BINDING.
// INDEX.lock serializes all friends, and the index plus mirrored STATE record
// fences state moves and lost records (SPEC-FRIEND, Native control roots).
func bindNativeControlOwnerState(name, state, root string) error {
	if name == "" {
		return errors.New("native control state needs its friend identity")
	}
	info, err := os.Stat(state)
	if err != nil {
		return err
	}
	id, err := nativeWorkIdentity(info)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(state)
	if err != nil || !filepath.IsAbs(canonical) {
		return errors.New("native control state directory cannot be verified")
	}
	indexPath := filepath.Join(filepath.Dir(root), "INDEX.json")
 if err := nativeControlNoSymlinkFile(indexPath+".lock"); err != nil { return err }
	_, err = withLaneLock(indexPath, func() (string, error) {
		if err := nativeControlNoSymlinkFile(indexPath); err != nil {
			return "", err
		}
		raw, err := os.ReadFile(indexPath)
		var index nativeControlIndex
		if err != nil || readNativeControlIndex(raw, &index) != nil || index.Version != 1 {
			return "", errors.New("native control state ownership index cannot be verified")
		}
		if err := verifyNativeControlNamespaces(filepath.Dir(root), &index); err != nil {
			return "", err
		}
		selected := -1
		for n, binding := range index.Bindings {
			if filepath.Join(filepath.Dir(root), binding.Namespace) == root {
				selected = n
			}
			for _, prior := range binding.States {
				old, readErr := os.Stat(prior.Path)
				if binding.Namespace == filepath.Base(root) && prior.Friend == name {
					if readErr != nil || prior.ID != id || !os.SameFile(old, info) {
						return "", errors.New("native control state ownership changed or cannot be verified; preserve prior durable debt")
					}
					return canonical, nil
				}
				if readErr == nil && os.SameFile(old, info) {
					return "", errors.New("native control state already belongs to another friend or work namespace")
				}
			}
		}
		if selected < 0 {
			return "", errors.New("native control state namespace is not indexed")
		}
		entries, err := os.ReadDir(canonical)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if entry.Name() != "CONTROL-BINDING.lock" {
				return "", errors.New("native control state lacks its reverse ownership binding; preserve known debt")
			}
		}
		record := nativeControlStateBinding{Friend: name, Path: canonical, ID: id}
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		// A crash between these writes leaves an unindexed record and holds. No
		// launch occurs until both membership and the forward proof are durable.
		if err := atomicfile.WriteFile(nativeControlStatePath(root, name), append(encoded, '\n'), 0600); err != nil {
			return "", err
		}
		index.Bindings[selected].States = append(index.Bindings[selected].States, record)
		if err := writeNativeControlIndex(indexPath, &index); err != nil {
			return "", err
		}
		return canonical, nil
	})
	return err
}

func verifyNativeControlStates(root string, states []nativeControlStateBinding) error {
	expected := map[string]bool{}
	for _, state := range states {
		path := nativeControlStatePath(root, state.Friend)
		if state.Friend == "" || !filepath.IsAbs(state.Path) || filepath.Clean(state.Path) != state.Path || state.ID.Inode == 0 || state.ID.Device == 0 || state.ID.UID != uint32(os.Geteuid()) || expected[filepath.Base(path)] {
			return errors.New("native control state membership is malformed")
		}
		expected[filepath.Base(path)] = true
		if err := nativeControlNoSymlinkFile(path); err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		encoded, encodeErr := json.Marshal(state)
		if err != nil || encodeErr != nil || !bytes.Equal(raw, append(encoded, '\n')) {
			return errors.New("known native control state record is missing or corrupt; ownership held")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "STATE-") && !expected[entry.Name()] {
			return errors.New("native control contains unindexed state ownership; preserve and reconcile it")
		}
	}
	return nil
}
