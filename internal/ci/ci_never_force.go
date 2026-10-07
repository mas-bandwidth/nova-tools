package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// ci_never_force.go is the machine behind the `never-force` class test in
// docs/SPEC-CI.md. It reads every Go source, shell script, Makefile, workflow
// and card template under the repository root, and refuses patterns that
// force-push or hard-reset shared refs. The rule prevents accidental
// overwrites of shared branches like dev or sprint/* that other workers rely
// on. The allowed shape is a normal push or reset to a private branch.

const NeverForceVerbLine = "never-force   read every Go source, Makefile, workflow and card template; refuse force-push or hard-reset of shared refs"

const (
	NeverForceRemedyPush = "use a normal push; never force a shared ref"
	NeverForceRemedyReset = "use a normal reset; never hard-reset a shared ref"
	NeverForceRemedyAllow = "remove the stale allowlist entry; the file only shrinks"
)

type ForceFinding struct {
	File   string
	Line   int
	Kind   string
	Remedy string
}

func (f ForceFinding) Render() string {
	return fmt.Sprintf("CI-NEVER-FORCE file=%s line=%d kind=%s remedy=%q", f.File, f.Line, f.Kind, f.Remedy)
}

type ForceResult struct {
	Files       int
	Allowlisted int
	Findings    []ForceFinding
	Stale       []ForceFinding
	Measured    map[string]bool
}

func (r ForceResult) Refused() int { return len(r.Findings) + len(r.Stale) }

func (r ForceResult) OKLine() string {
	return fmt.Sprintf("CI-NEVER-FORCE OK files=%d allowlisted=%d refused=0", r.Files, r.Allowlisted)
}

func (r ForceResult) FailLine() string {
	return fmt.Sprintf("CI-NEVER-FORCE FAIL files=%d allowlisted=%d refused=%d", r.Files, r.Allowlisted, r.Refused())
}

func (r ForceResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// Patterns to refuse: push --force, push -f, --force-with-lease, push origin +, reset --hard origin/
var forcePatterns = []*regexp.Regexp{
	regexp.MustCompile(`push\s+--force`),
	regexp.MustCompile(`push\s+-f\s`),
	regexp.MustCompile(`push\s+-f$`),
	regexp.MustCompile(`--force-with-lease`),
	regexp.MustCompile(`push\s+origin\s+\+`),
	regexp.MustCompile(`reset\s+--hard\s+origin/`),
}

// forceAllow is one allowlist row.
type forceAllow struct {
	file string
	line int
	key  string
}

var ForceFileLineListOptions = allowlist.Options{Key: allowlist.Fields(2), Ceiling: true, MissingIsEmpty: true}

// CheckNeverForce walks the tree at root and returns findings.
func CheckNeverForce(root, allowlistPath string) (ForceResult, error) {
	return checkNeverForceWith(root, allowlistPath, defaultSourceSeams())
}

func checkNeverForceWith(root string, allowlistPath string, seams SourceSeams) (ForceResult, error) {
	var res ForceResult

	entries, err := readForceAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	// Walk Go sources, Makefiles, workflows, card templates
	dirs := []string{"internal", "cmd", "fleet", "scripts", "infra", "tools"}
	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, statErr := os.Stat(base); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return res, statErr
		}
		err := seams.walk(base, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", ".git", "vendor":
					return filepath.SkipDir
				}
				return nil
			}

			name := d.Name()
			// Check if it's a file we care about
			isTarget := strings.HasSuffix(name, ".go") ||
				strings.HasSuffix(name, ".sh") ||
				strings.HasSuffix(name, "akefile") ||
				strings.HasSuffix(name, ".yml") ||
				strings.HasSuffix(name, ".yaml")

			if !isTarget {
				return nil
			}

			raw, readErr := seams.readFile(path)
			if readErr != nil {
				return readErr
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}

			rel = filepath.ToSlash(rel)
			return scanFileForForce(rel, raw, &res)
		})
		if err != nil {
			return res, err
		}
	}

	// Match findings against allowlist
	var remaining []ForceFinding
	for _, f := range res.Findings {
		if i := matchForceAllow(entries, matched, f); i >= 0 {
			matched[i] = true
			res.Allowlisted++
			continue
		}
		remaining = append(remaining, f)
	}
	res.Findings = remaining

	// Find stale allowlist entries
	for i, e := range entries {
		if matched[i] {
			continue
		}
		res.Stale = append(res.Stale, ForceFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: NeverForceRemedyAllow})
	}

	// Build measured set
	res.Measured = map[string]bool{}
	for i, e := range entries {
		if matched[i] {
			res.Measured[e.key] = true
		}
	}
	for _, f := range res.Findings {
		res.Measured[FileLineKey(f.File, f.Line, f.Kind)] = true
	}

	return res, nil
}

func readForceAllowlist(path string) ([]forceAllow, error) {
	if path == "" {
		return nil, nil
	}
	list, err := allowlist.Load(path, ForceFileLineListOptions)
	if err != nil {
		return nil, err
	}
	var out []forceAllow
	for _, row := range list.Rows() {
		fields := strings.Fields(row.Text)
		if len(fields) < 2 {
			continue
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			continue
		}
		n, convErr := strconv.Atoi(fields[0][colon+1:])
		if convErr != nil {
			continue
		}
		out = append(out, forceAllow{file: fields[0][:colon], line: n, key: row.Key})
	}
	return out, nil
}

func matchForceAllow(entries []forceAllow, used []bool, f ForceFinding) int {
	for i, e := range entries {
		if used[i] || e.file != f.File {
			continue
		}
		if e.line == f.Line {
			return i
		}
	}
	return -1
}

func scanFileForForce(rel string, raw []byte, res *ForceResult) error {
	src := string(raw)
	lines := strings.Split(src, "\n")

	for i, line := range lines {
		for _, pattern := range forcePatterns {
			if pattern.MatchString(line) {
				res.Files++
				res.Findings = append(res.Findings, ForceFinding{
					File:   rel,
					Line:   i + 1,
					Kind:   "force",
					Remedy: NeverForceRemedyPush,
				})
				break
			}
		}
	}

	if res.Files == 0 {
		res.Files = 1
	}
	return nil
}
