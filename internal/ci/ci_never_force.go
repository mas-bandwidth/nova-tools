package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// ci_never_force.go is the machine behind the `never-force` class test in
// docs/SPEC-CI.md. It reads every .go file, shell script, Makefile, and
// .github/workflows/*.yml file in the repository and refuses patterns that
// rewrite shared refs: `push --force`, `push -f`, `--force-with-lease`,
// `push origin +`, and `reset --hard origin/`. The allowed refs are the
// caller's own job branch (origin/<anything>), dev, and main.

const NeverForceVerbLine = "never-force read every .go, shell script, Makefile and .github/workflows/*.yml; refuse patterns that rewrite shared refs without allowlist entry"

const (
	NeverForceRemedy      = "remove the force-push pattern; use --force-with-lease=refs/heads/<local-branch> only for local work, never shared refs; cite docs/SPEC-CI.md"
	NeverForceRemedyAllow = "delete the stale row; the allowlist only shrinks"
)

type NeverForceResult struct {
	Files       int
	Allowlisted int
	Findings    []NeverForceFinding
	Stale       []NeverForceFinding
	Measured    map[string]bool
}

type NeverForceFinding struct {
	File   string
	Line   int
	Kind   string
	Remedy string
}

func (f NeverForceFinding) Render() string {
	return fmt.Sprintf("CI-NEVER-FORCE file=%s line=%d kind=%s remedy=%q", f.File, f.Line, f.Kind, f.Remedy)
}

func (r NeverForceResult) Refused() int { return len(r.Findings) + len(r.Stale) }

func (r NeverForceResult) OKLine() string {
	return fmt.Sprintf("CI-NEVER-FORCE OK files=%d allowlisted=%d refused=0", r.Files, r.Allowlisted)
}

func (r NeverForceResult) FailLine() string {
	return fmt.Sprintf("CI-NEVER-FORCE FAIL files=%d allowlisted=%d refused=%d", r.Files, r.Allowlisted, r.Refused())
}

func (r NeverForceResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// patternRules define patterns to refuse and their matching rules
var patternRules = []struct {
	pattern *regexp.Regexp
	kind    string
}{
	{regexp.MustCompile(`\bpush\s+--force\b`), "push-force"},
	{regexp.MustCompile(`\bpush\s+-f\b`), "push-f"},
	{regexp.MustCompile(`--force-with-lease`), "force-with-lease"},
	{regexp.MustCompile(`push\s+origin\s+\+`), "push-origin-plus"},
	{regexp.MustCompile(`reset\s+--hard\s+origin/`), "reset-hard-origin"},
}

// CheckNeverForce reads the repository and returns force-push patterns found
func CheckNeverForce(root, allowlistPath string) (NeverForceResult, error) {
	var res NeverForceResult
	res.Measured = map[string]bool{}

	entries, err := readNeverForceAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	// Walk repository reading relevant files
	err = walkNeverForceFiles(root, func(rel string, raw []byte) error {
		res.Files++
		findings := scanNeverForceFile(rel, raw)
		if len(findings) > 0 {
			res.Findings = append(res.Findings, findings...)
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	// Match findings against allowlist
	var remaining []NeverForceFinding
	for _, f := range res.Findings {
		if i := matchNeverForceAllow(entries, matched, f); i >= 0 {
			matched[i] = true
			res.Allowlisted++
			continue
		}
		remaining = append(remaining, f)
	}
	res.Findings = remaining
	res.Measured = usedRowKeys(entries, matched)

	// Check for stale allowlist entries
	for i, e := range entries {
		if matched[i] {
			continue
		}
		res.Stale = append(res.Stale, NeverForceFinding{
			File:   e.file,
			Line:   e.line,
			Kind:   "allowlist",
			Remedy: NeverForceRemedyAllow,
		})
	}

	// Add remaining findings to Measured
	for _, f := range res.Findings {
		res.Measured[FileLineKey(f.File, f.Line, f.Kind)] = true
	}

	return res, nil
}

func walkNeverForceFiles(root string, fn func(rel string, src []byte) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			switch info.Name() {
			case ".git", "testdata", "vendor":
				return filepath.SkipDir
			case ".":
				return nil
			}
			return nil
		}

		// Determine if this is a file we should check
		name := info.Name()
		ext := filepath.Ext(name)
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		shouldCheck := false
		switch {
		case ext == ".go":
			shouldCheck = true
		case ext == ".sh":
			shouldCheck = true
		case name == "Makefile":
			shouldCheck = true
		case strings.HasSuffix(rel, ".yml") && strings.Contains(rel, ".github/workflows/"):
			shouldCheck = true
		}

		if !shouldCheck {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		return fn(filepath.ToSlash(rel), raw)
	})
}

func scanNeverForceFile(rel string, src []byte) []NeverForceFinding {
	var findings []NeverForceFinding
	lines := strings.Split(string(src), "\n")
	seen := map[string]bool{}

	for lineNum, line := range lines {
		lineNum++ // 1-indexed
		for _, rule := range patternRules {
			if !rule.pattern.MatchString(line) {
				continue
			}
			// Special handling for force-with-lease: only flag if not used safely
			if rule.kind == "force-with-lease" && isSafeForceWithLease(line) {
				continue
			}
			key := fmt.Sprintf("%d:%s", lineNum, rule.kind)
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, NeverForceFinding{
				File:   rel,
				Line:   lineNum,
				Kind:   rule.kind,
				Remedy: NeverForceRemedy,
			})
		}
	}

	return findings
}

// isSafeForceWithLease checks if a force-with-lease is used safely (with refs/heads/)
func isSafeForceWithLease(line string) bool {
	// Safe patterns:
	// --force-with-lease=refs/heads/...
	// --force-with-lease = refs/heads/...
	// Must have refs/heads/ immediately following (with optional spaces around =)
	return strings.Contains(line, "--force-with-lease=refs/heads/") ||
		regexp.MustCompile(`--force-with-lease\s*=\s*refs/heads/`).MatchString(line)
}

func matchNeverForceAllow(entries []waitAllow, used []bool, f NeverForceFinding) int {
	loose := -1
	for i, e := range entries {
		if used[i] || e.file != f.File || e.kind != f.Kind {
			continue
		}
		if e.line == f.Line {
			return i
		}
		if loose < 0 {
			loose = i
		}
	}
	return loose
}

func readNeverForceAllowlist(path string) ([]waitAllow, error) {
	if path == "" {
		return nil, nil
	}
	list, err := allowlist.Load(path, FileLineListOptions)
	if err != nil {
		return nil, err
	}
	var out []waitAllow
	for _, row := range list.Rows() {
		fields := strings.Fields(row.Text)
		if len(fields) < 2 {
			continue
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			continue
		}
		n := 0
		if _, convErr := fmt.Sscanf(fields[0][colon+1:], "%d", &n); convErr != nil {
			continue
		}
		out = append(out, waitAllow{file: fields[0][:colon], line: n, kind: fields[1], key: row.Key})
	}
	return out, nil
}
