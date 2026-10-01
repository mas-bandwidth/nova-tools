package allowlist

// Package shards implement the shrink-only counted ledgers in SPEC-CI. By
// default a shard belongs to a source file's parent directory; PackageKeys
// selects explicit package-qualified keys instead.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Packages is a counted ledger split by source directory or explicit package.
// Its rows retain their full keys and original reasons.
type Packages struct {
	Path   string
	opt    Options
	shards map[string]*List // repo-relative source directory -> list
}

// LoadPackages reads every shard below dir. A missing directory is an empty
// ledger; an offender in it is still unlisted and cannot be added by UPDATE.
// Shards use <dir>/<owner>.txt, where owner is a source directory by default
// or a package with Options.PackageKeys. @root.txt represents the repository
// root. A real path component beginning with @ gains one @, so the mapping is
// reversible even for a directory literally named @root.
func LoadPackages(dir string, opt Options) (*Packages, error) {
	if !opt.Ceiling || !opt.Counted {
		return nil, fmt.Errorf("%s: package ledgers need Ceiling and Counted options", dir)
	}
	p := &Packages{Path: dir, opt: opt, shards: make(map[string]*List)}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: package ledger is not a directory", dir)
	}
	err = filepath.WalkDir(dir, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if file == dir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: package ledger rejects symlinks", file)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s: package ledger needs regular files", file)
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		pkg, err := packageFromShard(filepath.ToSlash(rel))
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if _, exists := p.shards[pkg]; exists {
			return fmt.Errorf("%s: duplicate package shard %q", file, pkg)
		}
		list, err := Load(file, opt)
		if err != nil {
			return err
		}
		if _, ok := list.Ceiling(); !ok {
			return fmt.Errorf("%s: package shard needs a # ceiling: line", file)
		}
		seen := make(map[string]bool)
		previous := ""
		for _, row := range list.Rows() {
			owner, err := p.owner(row.Key)
			if err != nil {
				return fmt.Errorf("%s:%d: %w", file, row.Line, err)
			}
			if owner != pkg {
				return fmt.Errorf("%s:%d: %q belongs to package %q, not %q", file, row.Line, row.Key, owner, pkg)
			}
			if seen[row.Key] {
				return fmt.Errorf("%s:%d: duplicate key %q", file, row.Line, row.Key)
			}
			if previous != "" && row.Key < previous {
				return fmt.Errorf("%s:%d: package shard is not sorted: %q follows %q", file, row.Line, row.Key, previous)
			}
			seen[row.Key] = true
			previous = row.Key
		}
		p.shards[pkg] = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Has and Count read the merged ledger by its unchanged full row key.
func (p *Packages) Has(key string) bool { return p.Count(key) > 0 }

func (p *Packages) Count(key string) int {
	owner, err := p.owner(key)
	if err != nil {
		return 0
	}
	if list := p.shards[owner]; list != nil {
		return list.Count(key)
	}
	return 0
}

// Rows returns all shards' rows sorted by their full keys. Lists returns the
// underlying lists in package-name order when a caller needs shard provenance.
func (p *Packages) Rows() []Row {
	var rows []Row
	for _, list := range p.Lists() {
		rows = append(rows, list.Rows()...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

func (p *Packages) Lists() []*List {
	names := make([]string, 0, len(p.shards))
	for name := range p.shards {
		names = append(names, name)
	}
	sort.Strings(names)
	lists := make([]*List, 0, len(names))
	for _, name := range names {
		lists = append(lists, p.shards[name])
	}
	return lists
}

// Ceiling is the sum of the shards' row ceilings, including zero for an empty
// ledger. LoadPackages requires a ceiling in every existing shard.
func (p *Packages) Ceiling() (int, bool) {
	total := 0
	for _, list := range p.shards {
		n, _ := list.Ceiling()
		total += n
	}
	return total, true
}

// CheckPackagesCounted compares measured site counts and honors NOVA_CI_UPDATE.
func CheckPackagesCounted(r Reporter, p *Packages, measured map[string]int) Result {
	r.Helper()
	return CheckPackagesCountedMode(r, p, measured, Updating())
}

// CheckPackagesCountedMode performs a whole-ledger preflight before any update
// write. An unlisted key, raised count, invalid key, or raised ceiling in any
// shard refuses all writes; a permitted update replaces only changed shards.
// Each replacement is atomic through WriteAtomic. Multiple replacements are
// not a single crash-atomic transaction.
func CheckPackagesCountedMode(r Reporter, p *Packages, measured map[string]int, update bool) Result {
	r.Helper()
	var result Result
	if p == nil {
		r.Errorf("package ledger is nil")
		return result
	}
	byPackage := make(map[string]map[string]int)
	blocked := false
	for key, count := range measured {
		if count < 0 {
			r.Errorf("%s: measured %q at a negative site count %d", p.Path, key, count)
			blocked = true
			continue
		}
		if count == 0 {
			continue
		}
		owner, err := p.owner(key)
		if err != nil {
			r.Errorf("%s: %v", p.Path, err)
			blocked = true
			continue
		}
		if byPackage[owner] == nil {
			byPackage[owner] = make(map[string]int)
		}
		byPackage[owner][key] = count
	}
	for pkg := range p.shards {
		if byPackage[pkg] == nil {
			byPackage[pkg] = make(map[string]int)
		}
	}
	names := make([]string, 0, len(byPackage))
	for pkg := range byPackage {
		names = append(names, pkg)
	}
	sort.Strings(names)
	type plan struct {
		list *List
		text string
	}
	var plans []plan
	for _, pkg := range names {
		list := p.shards[pkg]
		if list == nil {
			file, err := packageShardPath(p.Path, pkg)
			if err != nil {
				r.Errorf("%s: %v", p.Path, err)
				blocked = true
				continue
			}
			list, err = Parse(file, "", p.opt)
			if err != nil {
				r.Errorf("%s: %v", file, err)
				blocked = true
				continue
			}
		}
		// An update may remove stale rows from an already overfull shard.
		// Suppress the current-size diagnostic until the planned kept size
		// is known; checks outside UPDATE still report current overage.
		checkReporter := r
		if update {
			checkReporter = silentPackageReporter{}
		}
		res := CheckCountedMode(checkReporter, list, byPackage[pkg], false)
		result.Stale = append(result.Stale, res.Stale...)
		result.Unlisted = append(result.Unlisted, res.Unlisted...)
		result.Over = append(result.Over, res.Over...)
		result.Lowered = append(result.Lowered, res.Lowered...)
		if len(res.Unlisted)+len(res.Over) > 0 {
			blocked = true
		}
		if n, ok := list.Ceiling(); ok {
			kept := list.Len()
			if update {
				kept -= len(res.Stale)
			}
			if kept > n {
				blocked = true
				if update {
					r.Errorf("%s would hold %d rows, over its ceiling of %d; an update never raises a ceiling", list.Path, kept, n)
				}
			}
		}
		if !update || p.shards[pkg] == nil {
			continue
		}
		drop := make(map[int]bool)
		for _, row := range res.Stale {
			drop[row.Line-1] = true
		}
		lower := make(map[int]int)
		for _, row := range list.Rows() {
			if n := byPackage[pkg][row.Key]; n > 0 && n < list.Count(row.Key) {
				lower[row.Line-1] = n
			}
		}
		text := list.render(drop, lower, nil, list.Len()-len(res.Stale))
		if text != list.Text() {
			plans = append(plans, plan{list: list, text: text})
		}
	}
	sort.Strings(result.Unlisted)
	sort.Slice(result.Over, func(i, j int) bool { return result.Over[i].Key < result.Over[j].Key })
	sort.Slice(result.Lowered, func(i, j int) bool { return result.Lowered[i].Key < result.Lowered[j].Key })
	sort.Slice(result.Stale, func(i, j int) bool { return result.Stale[i].Key < result.Stale[j].Key })
	if !update {
		return result
	}
	if blocked {
		for _, key := range result.Unlisted {
			r.Errorf("%s is ceiling-only and refuses to grow under %s=1: %s is not listed", p.Path, UpdateEnv, key)
		}
		for _, row := range result.Over {
			r.Errorf("%s refuses to raise a count under %s=1: %s is listed at %d sites and measured at %d", p.Path, UpdateEnv, row.Key, row.Listed, row.Measured)
		}
		return result
	}
	for _, change := range plans {
		if err := WriteAtomic(change.list.Path, change.text); err != nil {
			r.Errorf("%s: the update could not write the package shard: %v", change.list.Path, err)
			return result
		}
		result.Updated = true
	}
	if result.Updated {
		result.Stale, result.Lowered = nil, nil
		r.Errorf("%s: %s (%d package shards changed)", p.Path, UpdatedRerun, len(plans))
	}
	return result
}

type silentPackageReporter struct{}

func (silentPackageReporter) Helper()               {}
func (silentPackageReporter) Errorf(string, ...any) {}

func (p *Packages) owner(key string) (string, error) {
	if p.opt.PackageKeys {
		return packageFromPackageKey(key)
	}
	return packageFromKey(key)
}

// packageFromPackageKey keeps the package prefix as its shard identity. This
// is distinct from the default file-key mode, which takes path.Dir(file).
func packageFromPackageKey(key string) (string, error) {
	pkg, kind, ok := strings.Cut(key, ":")
	if !ok || kind == "" || strings.Contains(kind, ":") || strings.Contains(kind, "/") || strings.Contains(kind, "\\") {
		return "", fmt.Errorf("invalid package ledger key %q: expected package:kind", key)
	}
	if pkg == "." {
		return pkg, nil
	}
	if _, err := packageShardPath("", pkg); err != nil {
		return "", fmt.Errorf("invalid package ledger key %q: %w", key, err)
	}
	return pkg, nil
}

// packageFromKey reads the source filename before the first ':' in a counted
// row key. Every existing counted class uses a repo-relative file prefix.
func packageFromKey(key string) (string, error) {
	file, kind, ok := strings.Cut(key, ":")
	if !ok || file == "" || kind == "" || strings.Contains(file, "\\") || path.IsAbs(file) ||
		(len(file) == 1 && ((file[0] >= 'A' && file[0] <= 'Z') || (file[0] >= 'a' && file[0] <= 'z')) &&
			(strings.HasPrefix(kind, "/") || strings.HasPrefix(kind, "\\"))) {
		return "", fmt.Errorf("invalid package ledger key %q: expected repo-relative file:kind", key)
	}
	for _, segment := range strings.Split(file, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid package ledger key %q: unsafe source path", key)
		}
	}
	return path.Dir(file), nil
}

func packageShardPath(dir, pkg string) (string, error) {
	if pkg == "." {
		return filepath.Join(dir, "@root.txt"), nil
	}
	var segments []string
	for _, segment := range strings.Split(pkg, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, "\\") {
			return "", fmt.Errorf("invalid source package %q", pkg)
		}
		if strings.HasPrefix(segment, "@") {
			segment = "@" + segment
		}
		segments = append(segments, segment)
	}
	segments[len(segments)-1] += ".txt"
	return filepath.Join(append([]string{dir}, segments...)...), nil
}

func packageFromShard(rel string) (string, error) {
	if rel == "@root.txt" {
		return ".", nil
	}
	if strings.Contains(rel, "\\") || !strings.HasSuffix(rel, ".txt") {
		return "", fmt.Errorf("invalid package shard %q: expected .txt", rel)
	}
	segments := strings.Split(strings.TrimSuffix(rel, ".txt"), "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "@@") {
			segment = segment[1:]
		} else if strings.HasPrefix(segment, "@") {
			return "", fmt.Errorf("invalid package shard %q: noncanonical @ component", rel)
		}
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid package shard %q: unsafe component", rel)
		}
		segments[i] = segment
	}
	pkg := strings.Join(segments, "/")
	canonical, err := packageShardPath("", pkg)
	if err != nil || filepath.ToSlash(canonical) != rel {
		return "", fmt.Errorf("invalid package shard %q: noncanonical path", rel)
	}
	return pkg, nil
}
