package main

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// The shared model store (docs/SPEC-LOCAL.md, rule 15 and "Fleet"): every machine keeps
// its weights under the shared directory of its AI root, <ai-root>/shared/models, one
// subdirectory per engine, never under any one account's working directory. The AI root
// is NOVA_AI_ROOT when set, else ai under the caller's home.

// AIRootEnv names the AI root on a machine whose root is not ~/ai.
const AIRootEnv = "NOVA_AI_ROOT"

// SharedRoot is the shared model directory of an AI root.
func SharedRoot(aiRoot string) string { return filepath.Join(aiRoot, "shared", "models") }

// AIRoot is the machine's AI root: NOVA_AI_ROOT, else <home>/ai; "" when neither is known.
func AIRoot(getenv func(string) string, home string) string {
	if r := getenv(AIRootEnv); r != "" {
		return r
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "ai")
}

// Resolve is p with every symlink resolved, component by component. A component that
// does not exist ends resolution: the longest existing prefix is resolved and the rest is
// appended as spelled. Any other failure is returned.
func Resolve(p string) (string, error) {
	cur, rest := filepath.Clean(p), ""
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Join(cur, rest), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// Shared is whether the resolved store lies under the resolved shared root, matched a
// whole component at a time (a models-scratch beside models is not under it): "yes",
// or "no" and why.
func Shared(store, root string) (word, why string) {
	rel, err := filepath.Rel(root, store)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "yes", ""
	}
	return "no", "the store is not under the shared model directory " + root
}
