package swarm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
)

// JobRepo is the clone under a job directory: <job>/repo, the same name gather
// and harvest already look for.
const JobRepo = "repo"

// StageJobTree clones source into dest as the job clone. When the card declares
// PATHS:, the working tree is a sparse checkout of those packages and their
// in-module dependencies only — an unrelated package is not materialized, and
// the named package's tests still run (#2498 S10). An import that cannot be
// resolved is not an empty dependency set: staging refuses before the clone,
// so the worker is not handed a tree with that dependency omitted. A resolved
// set with no in-module directories is empty and still checks out the PATHS
// and TEST cones. PATHS: none, or no PATHS: line, is a full checkout so a card
// that declared no bound keeps the whole tree. dest is typically
// filepath.Join(jobDir, JobRepo).
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

	// Resolve the cone before cloning. A dependency-resolution error then
	// refuses the stage with no job tree created, instead of a sparse checkout
	// that quietly dropped the import.
	sparse := declared && len(globs) > 0
	var cones []string
	if sparse {
		var err error
		cones, err = sparseCones(source, globs, cardTestPackage(string(card)))
		if err != nil {
			return err
		}
	}

	// --shared borrows the reference checkout's objects (SPEC-SANDBOX tree: yes);
	// --no-checkout leaves the working tree empty so sparse-checkout can fill it.
	if _, err := sparseGit("", "clone", "--quiet", "--shared", "--no-checkout", source, dest); err != nil {
		return err
	}

	if !sparse || len(cones) == 0 {
		if _, err := sparseGit(dest, "checkout", "--quiet"); err != nil {
			return err
		}
		return normalizeTrackedTimes(dest)
	}
	if _, err := sparseGit(dest, "sparse-checkout", "init", "--cone"); err != nil {
		return err
	}
	args := append([]string{"sparse-checkout", "set", "--cone"}, cones...)
	if _, err := sparseGit(dest, args...); err != nil {
		return err
	}
	if _, err := sparseGit(dest, "checkout", "--quiet"); err != nil {
		return err
	}
	return normalizeTrackedTimes(dest)
}

// normalizeTrackedTimes gives every materialized regular source file the tip commit's
// timestamp. ASDF validates compiled output by source mtime and also keys its output by
// translated source path. A fresh clone otherwise makes every source newer than a FASL
// compiled moments earlier in the exact-tip reference checkout, defeating prewarm.
// Symlinks are skipped: Chtimes follows them and must never touch a target outside the tree.
func normalizeTrackedTimes(repo string) error {
	rawStamp, err := sparseGit(repo, "show", "-s", "--format=%ct", "HEAD")
	if err != nil {
		return err
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(rawStamp), 10, 64)
	if err != nil {
		return fmt.Errorf("git commit timestamp %q: %w", rawStamp, err)
	}
	cmd := exec.Command("git", "ls-files", "-z", "--")
	cmd.Dir = repo
	cmd.Env = append(goenv.WithoutSecrets(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	stamp := time.Unix(unix, 0)
	for _, name := range bytes.Split(raw, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(repo, filepath.FromSlash(string(name)))
		fi, err := os.Lstat(path)
		if os.IsNotExist(err) { // sparse checkout: tracked outside the cone
			continue
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			return err
		}
	}
	return nil
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

// referenceCheckout is the dispatcher's reference checkout for a PATHS card:
// <pool>/ref/<owner>/<name>@<rev> (SPEC-SANDBOX, one checkout per distinct ref).
// SOURCE: names the repo; @<rev> on that token names the rev. A card that names
// the repo and not the rev uses the one checkout present for that repo. None,
// or more than one, is not a checkout this call will invent: the card still
// clones itself. PATHS: none, or no PATHS: line, is not a sparse stage.
func referenceCheckout(poolDir, card string) string {
	poolDir = strings.TrimSpace(poolDir)
	if poolDir == "" {
		return ""
	}
	globs, declared := cardPATHS(card)
	if !declared || len(globs) == 0 {
		return ""
	}
	repo, rev := cardSourceRepo(card)
	if repo == "" {
		if repos := cardRepos(card); len(repos) == 1 {
			repo = repos[0]
		}
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	if rev != "" {
		p := filepath.Join(poolDir, "ref", owner, name+"@"+rev)
		if isGitCheckout(p) {
			return p
		}
		return ""
	}
	dir := filepath.Join(poolDir, "ref", owner)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := name + "@"
	var match string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || len(e.Name()) == len(prefix) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !isGitCheckout(p) {
			continue
		}
		if match != "" {
			return ""
		}
		match = p
	}
	return match
}

// cardSourceRepo reads SOURCE: as owner/name, with an optional @rev and an
// optional #n issue suffix. A token that is not owner/name names no repo.
func cardSourceRepo(text string) (repo, rev string) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "SOURCE:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(t, "SOURCE:")))
		if len(fields) == 0 {
			return "", ""
		}
		tok := fields[0]
		if i := strings.Index(tok, "#"); i >= 0 {
			tok = tok[:i]
		}
		owner, nameRev, ok := strings.Cut(tok, "/")
		if !ok || owner == "" || nameRev == "" {
			return "", ""
		}
		name, rev, _ := strings.Cut(nameRev, "@")
		if name == "" || strings.Contains(name, "/") {
			return "", ""
		}
		return owner + "/" + name, rev
	}
	return "", ""
}

func isGitCheckout(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	g, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (g.IsDir() || g.Mode().IsRegular())
}

// cardTestPackage reads the TEST: package through cardhdr.ParseTest, the one
// TEST grammar, so a card whose tests live beside (or in) the named package
// still has that tree to run. `none <why>`, a tagged line's -tags and a line
// ParseTest refuses are read right: no package, or the package after the tags.
func cardTestPackage(text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "TEST:") {
			continue
		}
		tl, why := cardhdr.ParseTest(strings.TrimPrefix(t, "TEST:"))
		if why != "" || tl.None {
			return ""
		}
		return strings.Trim(tl.Package, "/")
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
		// A failed lookup is not an empty dependency set. Falling through would
		// check out only the PATHS and TEST directories and omit the import.
		// (nil, nil) — no module, no Go packages, or no in-module directories —
		// is empty and still adds those cones below.
		return nil, err
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

// listDepFmt prints one JSON object per non-standard package or package error.
// -e keeps a pattern that is not a Go package from aborting the listing of the
// packages that are, so their directories are not dropped with the error.
const listDepFmt = `{{if .Error}}{"err":{{printf "%q" .Error.Err}},"import":{{printf "%q" .ImportPath}}}{{else if not .Standard}}{"dir":{{printf "%q" .Dir}}}{{end}}`

type listDepRec struct {
	Err    string `json:"err"`
	Import string `json:"import"`
	Dir    string `json:"dir"`
}

// emptyDepPattern is a go list result that names no dependency. The PATHS or
// TEST pattern is not a Go package; that is a valid empty set. An unresolved
// import does not match.
func emptyDepPattern(msg string) bool {
	msg = strings.TrimSpace(msg)
	switch {
	case strings.HasPrefix(msg, "no Go files in "):
		return true
	case strings.HasPrefix(msg, "build constraints exclude all Go files"):
		return true
	case strings.Contains(msg, "matched no packages"):
		return true
	case strings.HasSuffix(msg, "directory not found"):
		return true
	default:
		return false
	}
}

func listDepDirs(src string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		if os.IsNotExist(err) {
			// Not a module: there is no in-module dependency to resolve.
			return nil, nil
		}
		return nil, err
	}
	args := []string{"list", "-e", "-deps", "-test", "-f", listDepFmt}
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
		var rec listDepRec
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("go list: %w", err)
		}
		if rec.Err != "" {
			if emptyDepPattern(rec.Err) {
				continue
			}
			if rec.Import != "" {
				return nil, fmt.Errorf("go list: %s: %s", rec.Import, rec.Err)
			}
			return nil, fmt.Errorf("go list: %s", rec.Err)
		}
		if rec.Dir == "" {
			continue
		}
		abs, err := filepath.Abs(rec.Dir)
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
	cmd.Env = append(goenv.WithoutSecrets(os.Environ()),
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
