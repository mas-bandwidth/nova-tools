package friend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

type nativeControlBinding struct {
	ID nativeWorkID
	Namespace string
	Paths []string
}
type nativeControlIndex struct {
	Version int
	Bindings []nativeControlBinding
}
var nativeControlNamespace = regexp.MustCompile(`^[a-f0-9]{32}$`)

// NativeOwnerControlDir establishes the production account's namespace. The
// durable index, not HOME or a new device number after remount, chooses it.
func NativeOwnerControlDir(work string) (string, error) {
	home, err := nativeAccountHome()
	if err != nil { return "", err }
	return NativeControlDir(home, work)
}

// NativeControlDir binds canonical/physical work aliases to a stable namespace
// before native launch (SPEC-FRIEND, Native control roots). An identity change
// or missing/corrupt known index holds ownership rather than minting new debt.
func NativeControlDir(home, work string) (string, error) {
	canonical, err := filepath.EvalSymlinks(work)
	if err != nil || !filepath.IsAbs(canonical) || !filepath.IsAbs(home) { return "", errors.New("native control needs absolute real work and account home") }
	info, err := os.Stat(canonical)
	if err != nil { return "", err }
	id, err := nativeWorkIdentity(info)
	if err != nil { return "", err }
	base, fresh, err := nativeControlIndexBase(home)
	if err != nil { return "", err }
	path := filepath.Join(base, "INDEX.json")
	if err := nativeControlNoSymlinkFile(path+".lock"); err != nil { return "", err }
	return withLaneLock(path, func() (string, error) {
		if err := nativeControlOwnedPath(base, true); err != nil { return "", err }
		if err := nativeControlNoSymlinkFile(path); err != nil { return "", err }
		index := nativeControlIndex{Version: 1}
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) && fresh {
			if err := writeNativeControlIndex(path, &index); err != nil { return "", err }
		} else if err != nil || json.Unmarshal(raw, &index) != nil || index.Version != 1 {
			return "", errors.New("native control durable index is missing or corrupt; preserve and reconcile ownership before launch")
		}
		return bindNativeWork(base, path, &index, canonical, info, id)
	})
}

// The owned account home and each private index parent are checked without
// following symlinks; a preexisting foreign/writable directory is never adopted.
func nativeControlIndexBase(home string) (string, bool, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home { return "", false, errors.New("native control account home is not canonical") }
	base := filepath.Join(home, ".nova-friend", "native-control")
	if err := nativeBrokerOutsideRoots(base, (&sandbox.Policy{}).VisibleRoots()); err != nil { return "", false, err }
	if real, err := filepath.EvalSymlinks(home); err != nil || real != home { return "", false, errors.New("native control account home must be real without symlink aliases") }
	if err := nativeControlOwnedPath(home, false); err != nil { return "", false, err }
	for _, path := range []string{filepath.Join(home, ".nova-friend"), filepath.Join(home, ".nova-friend", "native-control")} {
		err := os.Mkdir(path, 0o700)
		fresh := err == nil
		if err != nil && !errors.Is(err, os.ErrExist) { return "", false, err }
		if err := nativeControlOwnedPath(path, path != filepath.Join(home, ".nova-friend")); err != nil { return "", false, err }
		if path == filepath.Join(home, ".nova-friend", "native-control") { return path, fresh, nil }
	}
	return "", false, errors.New("native control index base is unavailable")
}

func bindNativeWork(base, indexPath string, index *nativeControlIndex, canonical string, info os.FileInfo, id nativeWorkID) (string, error) {
	match := -1
	seen := map[string]bool{}
	for n, binding := range index.Bindings {
		if !nativeControlNamespace.MatchString(binding.Namespace) || len(binding.Paths) == 0 || binding.ID.UID != id.UID || binding.ID.Inode == 0 || binding.ID.Device == 0 { return "", errors.New("native control index binding is malformed") }
		if seen[binding.Namespace] { return "", errors.New("native control index repeats an authority namespace") }
		seen[binding.Namespace] = true
		if err := nativeControlOwnedPath(filepath.Join(base, binding.Namespace), true); err != nil { return "", errors.New("known native control namespace is missing or unsafe; ownership held") }
		alias, samePath := false, false
		for _, old := range binding.Paths {
			if !filepath.IsAbs(old) || filepath.Clean(old) != old { return "", errors.New("native control index work path is malformed") }
			samePath = samePath || old == canonical
			if prior, err := os.Stat(old); err == nil && os.SameFile(prior, info) { alias = true }
		}
		if samePath || alias || binding.ID == id {
			if binding.ID != id || !alias { return "", errors.New("native work identity changed or cannot be verified; reconcile existing ownership before launch") }
			if match != -1 { return "", errors.New("native work has conflicting durable authority bindings") }
			match = n
		}
	}
	if match >= 0 {
		binding := &index.Bindings[match]
		for _, old := range binding.Paths { if old == canonical { return filepath.Join(base, binding.Namespace), nil } }
		binding.Paths = append(binding.Paths, canonical)
	} else {
		var key [16]byte
		if _, err := rand.Read(key[:]); err != nil { return "", err }
		name := hex.EncodeToString(key[:])
		if err := os.Mkdir(filepath.Join(base, name), 0o700); err != nil { return "", err }
		index.Bindings = append(index.Bindings, nativeControlBinding{ID: id, Namespace: name, Paths: []string{canonical}})
		match = len(index.Bindings)-1
	}
	if err := writeNativeControlIndex(indexPath, index); err != nil { return "", err }
	return filepath.Join(base, index.Bindings[match].Namespace), nil
}

// A friend-specific state directory remembers its control namespace too. Losing
// the shared index must not let existing Started/read/settlement debt use a new
// namespace. Missing proof in a nonempty state directory fails closed.
func bindNativeControlState(state, root string) error {
	path := filepath.Join(state, "CONTROL-BINDING")
	if err := nativeControlNoSymlinkFile(path+".lock"); err != nil { return err }
	_, err := withLaneLock(path, func() (string, error) {
		if err := nativeControlNoSymlinkFile(path); err != nil { return "", err }
		if raw, err := os.ReadFile(path); err == nil {
			if string(raw) != root+"\n" { return "", errors.New("daemon control binding changed; preserve and reconcile its existing state") }
			return root, nil
		} else if !errors.Is(err, os.ErrNotExist) { return "", err }
		entries, err := os.ReadDir(state)
		if err != nil { return "", err }
		for _, entry := range entries { if entry.Name() != "CONTROL-BINDING.lock" { return "", errors.New("daemon state lacks its known control binding; ownership held for reconciliation") } }
		if err := atomicfile.WriteFile(path, []byte(root+"\n"), 0o600); err != nil { return "", fmt.Errorf("native control state binding: %w", err) }
		return root, nil
	})
	return err
}

func writeNativeControlIndex(path string, index *nativeControlIndex) error {
	raw, err := json.Marshal(index)
	if err != nil { return err }
	return atomicfile.WriteFile(path, append(raw, '\n'), 0o600)
}
