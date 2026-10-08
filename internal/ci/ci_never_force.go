package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ci_never_force.go is the machine behind the `never-force` class test in
// docs/SPEC-CI.md. Nothing in nova-tools may rewrite a shared ref. The test
// reads the repository's Go sources, shell scripts, the Makefile, workflow YAML
// and card templates for the five force patterns of the card's PATTERNS TO
// REFUSE paragraph used against a shared ref: origin/<anything>, dev or main.
// The legitimate uses are rows of internal/ci/never_force_allowlist.txt, which
// only shrinks.

// neverForceAllowlistPath is the reasoned allowlist, one `path:line reason` per
// row, read relative to this package's directory.
const neverForceAllowlistPath = "never_force_allowlist.txt"

// neverForcePatterns are the shapes the rule refuses, in the order the card
// states them.
var neverForcePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bpush\s+--force\b`),
	regexp.MustCompile(`\bpush\s+-f\b`),
	regexp.MustCompile(`--force-with-lease`),
	regexp.MustCompile(`\bpush\s+origin\s+\+`),
	regexp.MustCompile(`\breset\s+--hard\s+origin/`),
}

// neverForceSharedRef matches a shared ref on the line: the remote origin, or
// the branch dev or main. A line with no shared ref states a pattern without
// acting on one, so it is not a finding.
var neverForceSharedRef = regexp.MustCompile(`(^|[^A-Za-z0-9_])origin([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])dev([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])main([^A-Za-z0-9_]|$)`)

// neverForceFinding is one refused pattern at one line of one file.
type neverForceFinding struct {
	Rel     string
	Line    int
	Pattern string
}

// key is the allowlist key of the finding: a repository-relative path and line.
func (f neverForceFinding) key() string {
	return f.Rel + ":" + strconv.Itoa(f.Line)
}

// neverForceScanned reports whether the rule reads a path by its kind: a Go
// source, a shell script, the Makefile, workflow YAML, or a card template.
func neverForceScanned(rel string) bool {
	if filepath.Base(rel) == "Makefile" {
		return true
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".sh", ".bash", ".zsh", ".card":
		return true
	}
	return strings.Contains(filepath.ToSlash(rel), ".github/workflows/") &&
		(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml"))
}

// neverForceSkip holds the rule's own files: the checker and its class test
// must name the patterns to refuse them, so reading them would refuse the rule.
func neverForceSkip(rel string) bool {
	return rel == "internal/ci/ci_never_force.go" || rel == "internal/ci/never_force_class_test.go"
}

// scanNeverForce reads one file's lines and names every force pattern used
// against a shared ref. A comment line is not read: it states the rule rather
// than running it.
func scanNeverForce(rel, body string) []neverForceFinding {
	var out []neverForceFinding
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !neverForceSharedRef.MatchString(line) {
			continue
		}
		for _, re := range neverForcePatterns {
			if !re.MatchString(line) {
				continue
			}
			out = append(out, neverForceFinding{Rel: rel, Line: i + 1, Pattern: re.String()})
			break
		}
	}
	return out
}

// neverForceFindings walks root and returns every force pattern used against a
// shared ref, with the repository-relative path and line of each. The rule's
// own files, testdata fixtures and the version-control store are not read.
func neverForceFindings(root string) ([]neverForceFinding, error) {
	var out []neverForceFinding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !neverForceScanned(rel) || neverForceSkip(rel) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, scanNeverForce(rel, string(raw))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
