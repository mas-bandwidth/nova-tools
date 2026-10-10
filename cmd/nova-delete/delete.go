package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// allowedRoots returns the list of allowed root directories.
// These are: system temp directory and NOVA_DELETE_ROOTS (colon-separated).
func allowedRoots() ([]string, error) {
	roots := []string{os.TempDir()}

	if s := os.Getenv("NOVA_DELETE_ROOTS"); s != "" {
		for _, r := range strings.Split(s, ":") {
			if r != "" {
				roots = append(roots, r)
			}
		}
	}

	return roots, nil
}

// validatePath checks if the path is a valid literal absolute path.
// It returns an error if the path is invalid or should be refused.
func validatePath(path string, roots []string) error {
	// Check for empty path
	if path == "" {
		return errors.New("empty argument")
	}

	// Check for glob characters or special characters (unexpanded variable or glob)
	if matched, _ := regexp.MatchString("[*?\\[\\]`\\n]", path); matched {
		return errors.New("path contains glob or special characters")
	}

	// Check for relative path
	if !filepath.IsAbs(path) {
		return errors.New("relative path")
	}

	// Check for top-level roots (temp, home, allowed roots themselves)
	cleanPath := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		// Only reject if path is the root itself, not paths inside allowed roots
		if cleanPath == cleanRoot {
			return errors.New("path is an allowed root itself")
		}
	}

	// Check if path is / (root filesystem)
	if cleanPath == "/" {
		return errors.New("path is /")
	}

	// Check for symlinks anywhere on the path
	current := "/"
	components := strings.Split(cleanPath, string(filepath.Separator))
	for i, comp := range components {
		if comp == "" {
			continue
		}
		if i == 0 {
			current = "/"
		} else {
			current = filepath.Join(current, comp)
		}
		info, err := os.Lstat(current)
		if err != nil {
			// Path doesn't exist yet - check remaining components
			break
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in path")
		}
	}

	// Check if path is outside all allowed roots
	insideRoot := false
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) || cleanPath == cleanRoot {
			insideRoot = true
			break
		}
	}
	if !insideRoot {
		return errors.New("path outside allowed roots")
	}

	return nil
}

// quarantinePath returns the target quarantine path for a given file.
func quarantinePath(path string) (string, error) {
	base := filepath.Base(path)
	now := time.Now()
	dir := fmt.Sprintf(".quarantine-%s", now.Format("20060102"))
	root := os.TempDir()

	// Find the allowed root that contains this path
	roots, err := allowedRoots()
	if err != nil {
		return "", err
	}

	cleanPath := filepath.Clean(path)
	for _, r := range roots {
		cleanRoot := filepath.Clean(r)
		if strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) || cleanPath == cleanRoot {
			root = cleanRoot
			break
		}
	}

	quarantineDir := filepath.Join(root, dir)
	if err := os.MkdirAll(quarantineDir, 0755); err != nil {
		return "", err
	}

	name := fmt.Sprintf("%s.%s.%d", base, now.Format("150405"), os.Getpid())
	return filepath.Join(quarantineDir, name), nil
}

// Delete moves a path to quarantine. It returns a message and exit code.
func Delete(path string) (string, int, error) {
	roots, err := allowedRoots()
	if err != nil {
		return "", 2, err
	}

	// Validate the path
	if err := validatePath(path, roots); err != nil {
		return "", 2, err
	}

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Sprintf("SKIP %s: not there", path), 0, nil
	}
	if err != nil {
		return "", 2, err
	}

	// Get quarantine destination
	quarantinePath, err := quarantinePath(path)
	if err != nil {
		return "", 2, err
	}

	// Move the file/directory
	if err := os.Rename(path, quarantinePath); err != nil {
		return "", 2, err
	}

	return fmt.Sprintf("MOVED %s -> %s", path, quarantinePath), 0, nil
}

// Sweep removes quarantine entries older than the given duration.
func Sweep(olderThan time.Duration) ([]string, error) {
	roots, err := allowedRoots()
	if err != nil {
		return nil, err
	}
	return SweepWithRoots(olderThan, roots)
}

// SweepWithRoots is the implementation that takes roots directly.
func SweepWithRoots(olderThan time.Duration, roots []string) ([]string, error) {

	var swept []string
	now := time.Now()

	for _, root := range roots {
		quarantineDir := filepath.Join(root, ".quarantine-*")
		entries, err := filepath.Glob(quarantineDir)
		if err != nil {
			continue
		}

		for _, dir := range entries {
			info, err := os.Stat(dir)
			if err != nil {
				continue
			}

			if now.Sub(info.ModTime()) > olderThan {
				if err := safepath.RemoveUnder(root, dir); err != nil {
					continue
				}
				swept = append(swept, dir)
			}
		}
	}

	return swept, nil
}
