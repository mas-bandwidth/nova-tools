package swarm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// JobRepo is the clone under a job directory: <job>/repo, the same name gather
// and harvest already look for.
const JobRepo = "repo"

// StageJobTree clones source into dest as the job clone. When the card declares
// PATHS:, the working tree is a sparse checkout of those packages and their
// in-module dependencies only — an unrelated package is not materialized, and
// the named package's tests still run (#2498 S10). PATHS: none, or no PATHS:
// line, is a full checkout so a card that declared no bound keeps the whole
// tree. dest is typically filepath.Join(jobDir, JobRepo).
func StageJobTree(source, dest string, card []byte) error {
	source = strings.TrimSpace(source)
	dest = strings.TrimSpace(dest)
	if source == "" {
		return fmt.Errorf("source is required; refusing to guess a checkout")
	}
	if dest == "" {
		return fmt.Errorf("dest is required; refusing to guess a job clone")
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("source %s is not a checkout: %w", source, err)
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("dest %s already exists", dest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("dest %s: %w", dest, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	globs, declared := cardPATHS(string(card))
	if declared && len(globs) > 0 {
		if err := hygiene.ValidatePaths(globs); err != nil {
			return err
		}
	}

	// --shared borrows the reference checkout's objects (SPEC-SANDBOX tree: yes);
	// --no-checkout leaves the working tree empty so sparse-checkout can fill it.
	if _, err := sparseGit("", "clone", "--quiet", "--shared", "--no-checkout", source, dest); err != nil {
		return err
	}

	if !declared || len(globs) == 0 {
		_, err := sparseGit(dest, "checkout", "--quiet")
		return err
	}

	cones, err := sparseCones(source, globs, cardTestPackage(string(card)))
	if err != nil {
		return err
	}
	if len(cones) == 0 {
		_, err := sparseGit(dest, "checkout", "--quiet")
		return err
	}
	if _, err := sparseGit(dest, "sparse-checkout", "init", "--cone"); err != nil {
		return err
	}
	args := append([]string{"sparse-checkout", "set", "--cone"}, cones...)
	if _, err := sparseGit(dest, args...); err != nil {
		return err
	}
	_, err = sparseGit(dest, "checkout", "--quiet")
	return err
}

// cardPATHS reads the card's PATHS: line. declared is true when the line is
// present; globs is empty for `PATHS: none` or an empty line.
func cardPATHS(text string) (globs []string, declared bool) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "PATHS:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "PATHS:"))
		if rest == "" || rest == "none" {
			return nil, true
		}
		for _, g := range strings.Split(rest, ",") {
			g = strings.TrimSpace(g)
			if g != "" && g != "none" {
				globs = append(globs, g)
			}
		}
		return globs, true
	}
	return nil, false
}

// cardTestPackage reads the TEST: package, so a card whose tests live beside
// (or in) the named package still has that tree to run.
func cardTestPackage(text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "TEST:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "TEST:"))
		if rest == "" || rest == "none" {
			return ""
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return ""
		}
		return strings.Trim(fields[0], "/")
	}
	return ""
}

// sparseCones is the cone list git sparse-checkout set receives: PATHS
// directories, the TEST: package, and every in-module dependency `go list`
// names from the full source tree.
func sparseCones(src string, globs []string, testPkg string) ([]string, error) {
	var patterns []string
	fallback := map[string]bool{}
	addDir := func(dir string) {
		dir = strings.Trim(filepath.ToSlash(dir), "/")
		if dir == "" || dir == "." {
			return
		}
		fallback[dir] = true
	}
	for _, g := range globs {
		pat, dir := globToListPattern(g)
		if pat != "" {
			patterns = append(patterns, pat)
		}
		addDir(dir)
	}
	if testPkg != "" {
		p := strings.Trim(strings.TrimPrefix(testPkg, "./"), "/")
		if p != "" && p != "." {
			patterns = append(patterns, "./"+p)
			addDir(p)
		}
	}
	dirs, err := listDepDirs(src, patterns)
	if err != nil {
		dirs = nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		dir = strings.Trim(filepath.ToSlash(dir), "/")
		if dir == "" || dir == "." || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, d := range dirs {
		add(d)
	}
	for d := range fallback {
		add(d)
	}
	sort.Strings(out)
	return out, nil
}

// globToListPattern turns one PATHS glob into a `go list` pattern and the
// repo-relative directory that glob names.
func globToListPattern(glob string) (pattern, dir string) {
	g := filepath.ToSlash(glob)
	prefix := literalPrefix(g)
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "./...", "."
	}
	dir = prefix
	base := path.Base(dir)
	if strings.Contains(base, ".") && !strings.ContainsAny(base, "*?[") {
		dir = path.Dir(dir)
	}
	if dir == "." || dir == "" {
		return "./...", "."
	}
	if strings.Contains(g, "**") {
		return "./" + dir + "/...", dir
	}
	return "./" + dir, dir
}

func literalPrefix(g string) string {
	var segs []string
	for _, s := range strings.Split(g, "/") {
		if strings.ContainsAny(s, "*?[") {
			break
		}
		segs = append(segs, s)
	}
	return strings.Join(segs, "/")
}

func listDepDirs(src string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return nil, err
	}
	args := []string{"list", "-deps", "-test", "-f", "{{if not .Standard}}{{.Dir}}{{end}}"}
	args = append(args, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = src
	cmd.Env = append(goenv.Clean(os.Environ()), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("go list: %s", msg)
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(srcAbs); err == nil {
		srcAbs = real
	}
	var dirs []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		abs, err := filepath.Abs(line)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		rel, err := filepath.Rel(srcAbs, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	return dirs, nil
}

func sparseGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}
