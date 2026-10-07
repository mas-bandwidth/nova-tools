package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// releaseTargets is the file release-targets: the one list of what a release
// ships. Embedded, so the tool answers the same from any directory.
//
//go:embed release-targets
var releaseTargets string

// target is one shipped platform.
type target struct{ goos, goarch string }

func (t target) String() string { return t.goos + " " + t.goarch }

// ext is the suffix a binary of the platform carries.
func (t target) ext() string {
	if t.goos == "windows" {
		return ".exe"
	}
	return ""
}

// parseTargets reads the list: one "<goos> <goarch>" per line, # comments and
// blank lines skipped. A line of any other shape is an error, not a line to
// skip.
func parseTargets(text string) ([]target, error) {
	var out []target
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || line != f[0]+" "+f[1] {
			return nil, fmt.Errorf("release-targets line %d: %q is not \"<goos> <goarch>\"", n+1, line)
		}
		out = append(out, target{f[0], f[1]})
	}
	return out, nil
}

func (e env) shipped() ([]target, error) {
	if e.targets != nil {
		return e.targets, nil
	}
	return parseTargets(releaseTargets)
}

// toolNames lists the tools a release ships: every directory of <root>/<cmdDir>
// whose name does not begin with a dot, in byte order. The set is walked, never
// listed, so a tool added to the repository ships without anyone editing a list.
func toolNames(root, cmdDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, cmdDir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, cmdDir, ent.Name()))
		if err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, ent.Name())
	}
	sort.Strings(out)
	return out, nil
}

// artifactName is the only name a release uses for a binary:
// <tool>_<stamp>_<goos>_<goarch>[.exe].
func artifactName(tool, stamp string, t target) string {
	return tool + "_" + stamp + "_" + t.goos + "_" + t.goarch + t.ext()
}
