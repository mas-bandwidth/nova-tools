// ci_never_force.go implements the never-force class test.
// It enforces the rule that nothing in nova-tools may rewrite a shared ref.
package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	forcePushPatterns = []*regexp.Regexp{
		regexp.MustCompile(`push\s+--force`),
		regexp.MustCompile(`push\s+-f\b`),
		regexp.MustCompile(`--force-with-lease`),
		regexp.MustCompile(`push\s+origin\s+\+`),
		regexp.MustCompile(`reset\s+--hard\s+origin/`),
	}
	sharedRefPattern = regexp.MustCompile(`(origin/\w+|dev|main)\b`)
)

// ScanForForcePatterns walks the tree and finds force push patterns
// Returns findings as map[path][]line
func ScanForForcePatterns(root string) (map[string][]string, error) {
	findings := make(map[string][]string)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip testdata and vendor
		if strings.Contains(path, "/testdata/") || strings.Contains(path, "/vendor/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check file type
		ext := filepath.Ext(path)
		base := filepath.Base(path)

		// Scan Go files, shell scripts, Makefiles, workflows
		if !strings.HasSuffix(ext, ".go") && ext != ".sh" && base != "Makefile" &&
			!strings.HasSuffix(base, ".yml") && !strings.HasSuffix(base, ".yaml") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		text := string(content)
		lines := strings.Split(text, "\n")

		for i, line := range lines {
			// Skip comments
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}

			// Check for force patterns
			for _, re := range forcePushPatterns {
				if re.MatchString(line) {
					// Check if it targets a shared ref
					if sharedRefPattern.MatchString(line) {
						findings[path] = append(findings[path],
							fmt.Sprintf("line %d: %s", i+1, re.String()))
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return findings, nil
}
