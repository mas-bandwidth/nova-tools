// Package nogh installs a refusing gh command first on a child shell's PATH.
// The child returns requested forge actions to its coordinator and uses git
// for branch transport where permitted. This shim makes that routing visible
// when the child invokes gh by name.
//
// This is a command-routing aid, not a security boundary: an absolute path
// bypasses PATH lookup. Callers remain responsible for the child's permissions
// and credentials.
package nogh

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Name is the file the shim is written as.
const Name = "gh"

// Refusal is the one line the refusing gh prints on stderr. It carries no
// single quote, so Script can quote it for sh as written.
const Refusal = "gh: REFUSED GitHub CLI is unavailable in this child shell; " +
	"return the requested forge action in your report to the coordinator; use git for branch transport where permitted"

// Script is the refusing gh's whole text.
func Script() string {
	return "#!/bin/sh\n# the refusing gh (nova-tools #3600): GitHub is a git remote only (#3594)\necho '" + Refusal + "' >&2\nexit 2\n"
}

// Install puts the refusing gh at <dir>/gh (making dir) and returns its path.
// The file is written under a unique name and renamed into place, so two
// children starting at once never exec a half-written file. On windows no
// shim is written and the path is "".
func Install(dir string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("the gh shim directory %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".gh-*")
	if err != nil {
		return "", fmt.Errorf("the gh shim in %s: %w", dir, err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(Script())
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o755)
	}
	path := filepath.Join(dir, Name)
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		// ignored: a best-effort cleanup of the temp shim; the write or rename error is the one returned
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the gh shim %s: %w", path, werr)
	}
	return path, nil
}

// PathFirst is env with dir prepended to every PATH entry, so `gh` by name
// resolves to the shim before any real one; an env with no PATH gains
// PATH=<dir>. An empty dir returns env unchanged.
func PathFirst(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if name != "PATH" {
			out = append(out, kv)
			continue
		}
		found = true
		if val == "" {
			out = append(out, "PATH="+dir)
			continue
		}
		out = append(out, "PATH="+dir+string(os.PathListSeparator)+val)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
