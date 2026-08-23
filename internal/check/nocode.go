package check

import (
	"bufio"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// codeExtsData is the floor deny-list, kept as data a reader can open and diff
// rather than as string literals inside a walk. See codeexts.txt for why it is
// embedded rather than demanded from the caller.
//
//go:embed codeexts.txt
var codeExtsData string

// Deny-list provenance, reported with every finding so that neither a red nor
// a green hides the basis it was reached on.
const (
	DenyFloor    = "floor list"
	DenyReplaced = "--deny-ext"
	DenyExtended = "floor list + --deny-ext-add"
)

// FloorDenyExts returns the embedded floor deny-list, sorted.
//
// It returns an error rather than an empty list if the data is unreadable or
// empty: a guard that cannot determine what it forbids must refuse, not pass.
func FloorDenyExts() ([]string, error) {
	exts := parseExtLines(codeExtsData)
	if len(exts) == 0 {
		return nil, fmt.Errorf("floor deny-list is empty: the embedded list did not parse")
	}
	return exts, nil
}

// parseExtLines reads one extension per line, ignoring blanks and # comments.
func parseExtLines(s string) []string {
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		line = strings.ToLower(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, ".") {
			line = "." + line
		}
		seen[line] = true
	}
	return sortedKeys(seen)
}

// ParseDenyList reads a deny-list specification: either a comma-separated list
// of extensions, or "@path" naming a file with one extension per line.
func ParseDenyList(spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty deny-list specification")
	}
	if strings.HasPrefix(spec, "@") {
		path := strings.TrimPrefix(spec, "@")
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("deny-list file: %w", err)
		}
		exts := parseExtLines(string(b))
		if len(exts) == 0 {
			return nil, fmt.Errorf("deny-list file %q contains no extensions", path)
		}
		return exts, nil
	}
	exts := parseExtLines(strings.ReplaceAll(spec, ",", "\n"))
	if len(exts) == 0 {
		return nil, fmt.Errorf("deny-list %q contains no extensions", spec)
	}
	return exts, nil
}

// NoCodeOptions configures the self/machinery separation check.
//
// Allow starts EMPTY and stays empty unless the caller narrows scope. Every
// scope narrowing is the caller's, stated per run — the same law the skip and
// exempt lists elsewhere in this repo obey. The deny-list runs the other way:
// it is a floor that ships with the tool, because a narrowing that goes
// missing fails open and a floor that ships cannot.
type NoCodeOptions struct {
	Dir        string   // root of the tree being guarded (required)
	Allow      []string // path prefixes where machinery may live; empty by default
	DenyExt    []string // effective deny-list; empty means use the floor list
	DenySource string   // provenance, reported with each finding
	Staged     []string // if non-nil, classify exactly these repo-relative paths
	StagedSet  bool     // distinguishes "no staged paths given" from "staged, none"
}

// NoCode reports every file that is machinery living inside a prose-only tree.
//
// A file is flagged when its extension is on the effective deny-list, when it
// carries an executable bit, or when it begins with a shebang. The three catch
// different things: the extension is the auditable common case, the mode bit
// catches a chmod +x on anything at all, and the shebang is the tell that
// survives renaming — a script with no extension is still a script.
//
// Symlinks are not followed and not inspected: a gate that can be walked out
// of the tree it guards is not a gate.
func NoCode(opts NoCodeOptions) (scanned int, findings []Failure, err error) {
	deny := opts.DenyExt
	source := opts.DenySource
	if len(deny) == 0 {
		deny, err = FloorDenyExts()
		if err != nil {
			return 0, nil, err
		}
		if source == "" {
			source = DenyFloor
		}
	}
	denySet := make(map[string]bool, len(deny))
	for _, e := range deny {
		denySet[strings.ToLower(e)] = true
	}
	if len(denySet) == 0 {
		return 0, nil, fmt.Errorf("effective deny-list is empty: refusing rather than passing everything")
	}
	if source == "" {
		source = DenyFloor
	}

	allowed := make(map[string]bool, len(opts.Allow))
	for _, a := range opts.Allow {
		a = filepath.ToSlash(strings.Trim(strings.TrimSpace(a), "/"))
		if a != "" {
			allowed[a] = true
		}
	}

	info, err := os.Stat(opts.Dir)
	if err != nil {
		return 0, nil, fmt.Errorf("dir: %w", err)
	}
	if !info.IsDir() {
		return 0, nil, fmt.Errorf("dir %q is not a directory", opts.Dir)
	}

	if opts.StagedSet {
		return noCodeStaged(opts, allowed, denySet, source)
	}

	err = filepath.WalkDir(opts.Dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(opts.Dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			// .git is machinery by construction and is not repo content.
			if d.Name() == ".git" || allowed[rel] || allowed[topSegment(rel)] {
				return filepath.SkipDir
			}
			return nil
		}
		if allowed[rel] || allowed[topSegment(rel)] {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !fi.Mode().IsRegular() {
			return nil // symlinks and specials: not followed, not flagged
		}
		scanned++
		if reasons := classify(path, rel, fi, denySet, source); len(reasons) > 0 {
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return scanned, findings, nil
}

// noCodeStaged classifies an explicit path list instead of walking the tree.
//
// The distinction is deliberate: the gate refuses what is being ADDED, not
// what already exists. Gating the whole tree at commit time would make the
// next commit hostage to deleting every finished investigation in the repo,
// which is how a reasonable rule becomes a rule everybody disables.
//
// A path that no longer exists on disk — a staged deletion — is skipped
// rather than reported: taking machinery back out of the self is the
// direction this gate wants.
func noCodeStaged(opts NoCodeOptions, allowed, denySet map[string]bool, source string) (int, []Failure, error) {
	var findings []Failure
	scanned := 0
	for _, raw := range opts.Staged {
		rel := filepath.ToSlash(strings.TrimSpace(raw))
		if rel == "" || rel == "." {
			continue
		}
		if allowed[rel] || allowed[topSegment(rel)] {
			continue
		}
		full := filepath.Join(opts.Dir, filepath.FromSlash(rel))
		fi, err := os.Lstat(full)
		if err != nil {
			continue // staged deletion, or gone: not an accumulation
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		scanned++
		if reasons := classify(full, rel, fi, denySet, source); len(reasons) > 0 {
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
	}
	return scanned, findings, nil
}

// classify returns every reason the file is machinery, or nil if it is prose.
// All reasons are reported when more than one holds: a gate that says only
// "no" teaches nothing, and each reason is separately actionable.
func classify(fullPath, rel string, fi os.FileInfo, denySet map[string]bool, source string) []string {
	var reasons []string
	if ext := strings.ToLower(filepath.Ext(rel)); denySet[ext] {
		reasons = append(reasons, fmt.Sprintf("code extension %s (%s)", ext, source))
	}
	if fi.Mode().Perm()&0o111 != 0 {
		reasons = append(reasons, fmt.Sprintf("executable (mode %04o)", fi.Mode().Perm()))
	}
	if hasShebang(fullPath) {
		reasons = append(reasons, "executable script (shebang)")
	}
	return reasons
}

// topSegment returns the first path element, so an allow entry of "history"
// covers everything beneath it without the caller enumerating subdirectories.
func topSegment(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// hasShebang reports whether a file begins with "#!".
func hasShebang(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := bufio.NewReader(f).Peek(2)
	return err == nil && string(b) == "#!"
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
