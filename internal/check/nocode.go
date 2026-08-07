package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// codeExts are the extensions that mark a file as code in a self repo.
// Extend the list rather than sniffing contents: a list is auditable.
var codeExts = map[string]bool{
	".go": true, ".py": true, ".sh": true, ".js": true, ".ts": true,
	".c": true, ".cc": true, ".cpp": true, ".rs": true, ".cs": true,
	".rb": true, ".pl": true, ".php": true, ".java": true, ".lua": true,
	".swift": true, ".kt": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".tsx": true, ".bash": true, ".ps1": true, ".bat": true, ".cmd": true,
	".exe": true,
}

// NoCode walks dir (skipping .git) and reports every regular file that is
// code by extension or has an executable bit set. A self repo holds prose;
// machinery lives elsewhere. scanned counts the regular files examined.
func NoCode(dir string) (scanned int, findings []Failure, err error) {
	info, err := os.Stat(dir)
	if err != nil {
		return 0, nil, fmt.Errorf("dir: %w", err)
	}
	if !info.IsDir() {
		return 0, nil, fmt.Errorf("dir %q is not a directory", dir)
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil // symlinks and specials: deliberately not checked, see SPEC.md
		}
		scanned++
		var reasons []string
		if ext := strings.ToLower(filepath.Ext(d.Name())); codeExts[ext] {
			reasons = append(reasons, "code extension "+ext)
		}
		if fi.Mode().Perm()&0o111 != 0 {
			reasons = append(reasons, fmt.Sprintf("executable (mode %04o)", fi.Mode().Perm()))
		}
		if len(reasons) > 0 {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				rel = path
			}
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return scanned, findings, nil
}
