package docs

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxRootBytes is the ceiling on the root AGENTS.md. A harness reads that
// page at the start of every session, so a page that grows without a cap stops
// being read. The page carries the whole of docs/STANDARD.md (StandardDoc)
// beside the directory map, because everything someone needs to know while
// building or working on a tool is meant to be in the one file a harness
// loads; the ceiling is sized for the standard plus the map with headroom for
// catalog rows, and a growth past it is shortened at the standard's words or
// the catalog rows, never by raising the number unread. It is 26 KiB because
// the standard carries the whole of what a builder meets: the ten rules never
// to break, the five onboarding points and every class rule by name, folded in
// from CONTRIBUTING.md and ONBOARDING.md so there is one standard, not three.
// It rose from 24 KiB by two KiB for two rules the standard states as a goal
// and its checks: every Go test uses testify (section 8), and the standard
// library and the adopted modules are searched before a helper is written
// (section 7); the page measures 25.3 KiB with both.
const MaxRootBytes = 26 * 1024

// StandardDoc is the one source of the standard every tool is built to. The
// root page embeds it whole, headings one level down.
const StandardDoc = "docs/STANDARD.md"

const RootAgents = "AGENTS.md"

// skipNames are directories the map does not catalog: VCS, build output, and
// local toolchain / scratch unpacks.
var skipNames = map[string]bool{
	".git":         true,
	"bin":          true,
	"build":        true,
	"dist":         true,
	"node_modules": true,
	"obj":          true,
	"runtimes":     true,
	"scratch":      true,
	"target":       true,
	"tending":      true,
}

// deprecatedDir is the one top-level directory no walk of this repository
// descends into: the tools kept there are reference only, a module of their own
// that nothing builds, tests or ships (deprecated/README.md). The map still
// lists the directory itself, from its catalog row.
const deprecatedDir = "deprecated"

// Entry is one mapped directory.
type Entry struct {
	Path    string // slash-separated, no trailing slash
	Purpose string
	Guard   string
	Command string
	Page    bool // write Path/AGENTS.md listing this directory's children
}

func E(path, purpose, guard, command string) Entry {
	return Entry{Path: path, Purpose: purpose, Guard: guard, Command: command}
}

func Page(path, purpose, guard, command string) Entry {
	n := E(path, purpose, guard, command)
	n.Page = true
	return n
}

type CatalogIndex map[string]Entry

func IndexCatalog(cat []Entry) (CatalogIndex, []string) {
	idx := make(CatalogIndex, len(cat))
	var issues []string
	for _, e := range cat {
		if e.Path == "" {
			issues = append(issues, "catalog row has an empty path")
			continue
		}
		if strings.Contains(e.Path, "\\") || strings.HasPrefix(e.Path, "/") || strings.HasSuffix(e.Path, "/") {
			issues = append(issues, fmt.Sprintf("catalog path %q must be slash-separated with no leading or trailing slash", e.Path))
		}
		if _, dup := idx[e.Path]; dup {
			issues = append(issues, fmt.Sprintf("catalog names %s twice", e.Path))
		}
		if strings.TrimSpace(e.Purpose) == "" || strings.TrimSpace(e.Guard) == "" || strings.TrimSpace(e.Command) == "" {
			issues = append(issues, fmt.Sprintf("catalog row %s is missing purpose, guard, or command", e.Path))
		}
		idx[e.Path] = e
	}
	return idx, issues
}

// Render builds every AGENTS.md page from the live tree and the catalog.
// Uncatalogued children still appear (as "-" rows) so a committed page goes
// stale the moment a mapped directory grows a child. Completeness issues are
// returned beside the pages; make map refuses to write when any are present.
func Render(root string, cat []Entry) (map[string]string, []string) {
	idx, issues := IndexCatalog(cat)
	issues = append(issues, treeIssues(root, cat, idx)...)

	pages := make(map[string]string)
	pages[RootAgents] = renderPage(root, "", idx)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(StandardDoc))); err != nil {
		issues = append(issues, fmt.Sprintf("%s is missing; the root %s embeds it", StandardDoc, RootAgents))
	}
	for _, e := range cat {
		if !e.Page {
			continue
		}
		pages[e.Path+"/"+RootAgents] = renderPage(root, e.Path, idx)
	}
	if n := len(pages[RootAgents]); n >= MaxRootBytes {
		issues = append(issues, fmt.Sprintf("%s is %d bytes, over the %d-byte cap; shorten the catalog rows or the standard", RootAgents, n, MaxRootBytes))
	}
	return pages, issues
}

func treeIssues(root string, cat []Entry, idx CatalogIndex) []string {
	var issues []string
	top, err := childDirs(root, "")
	if err != nil {
		return []string{err.Error()}
	}
	for _, name := range top {
		if _, ok := idx[name]; !ok {
			issues = append(issues, fmt.Sprintf("uncatalogued directory %s; add a row to internal/docs/catalog.go and run: make map", name))
		}
	}
	for _, e := range cat {
		abs := filepath.Join(root, filepath.FromSlash(e.Path))
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			issues = append(issues, fmt.Sprintf("catalog names %s which is not a directory", e.Path))
			continue
		}
		parent := parentPath(e.Path)
		if parent == "" {
			continue
		}
		p, ok := idx[parent]
		if !ok || !p.Page {
			issues = append(issues, fmt.Sprintf("%s is catalogued but %s is not a page", e.Path, parent))
		}
	}
	for _, e := range cat {
		if !e.Page {
			continue
		}
		kids, err := childDirs(root, e.Path)
		if err != nil {
			issues = append(issues, err.Error())
			continue
		}
		for _, name := range kids {
			path := e.Path + "/" + name
			if _, ok := idx[path]; !ok {
				issues = append(issues, fmt.Sprintf("uncatalogued directory %s; add a row to internal/docs/catalog.go and run: make map", path))
			}
		}
	}
	return issues
}

func renderPage(root, dir string, idx CatalogIndex) string {
	var b strings.Builder
	if dir == "" {
		b.WriteString("# AGENTS.md — generated map\n\n")
		b.WriteString("Do not edit. `make map` regenerates this file. AGENTS.md alone: no `CLAUDE.md`, no pointer, no symlink.\n\n")
		b.WriteString("Nova Tools is machinery: command-line tools that AI friends and people run against their own records, on their own machines, with their own identities. Adoption is a choice — one tool is a fine number. The standard is below; how review goes: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).\n\n")
		b.WriteString("```\nmake build          # go build ./...\nmake test           # the fast tier, plus the per-package time budget\nmake map            # regenerate AGENTS.md and per-directory maps\n```\n\n")
		b.WriteString(embedStandard(root))
	} else {
		depth := strings.Count(dir, "/") + 1
		up := strings.Repeat("../", depth)
		rulesRel := up + StandardDoc
		if dir == "docs" {
			rulesRel = "STANDARD.md"
		}
		b.WriteString("# AGENTS.md — generated map of ")
		b.WriteString(dir)
		b.WriteString("/\n\n")
		b.WriteString("Do not edit. `make map` regenerates this file. Root: [AGENTS.md](")
		b.WriteString(up)
		b.WriteString("AGENTS.md). Rules: [STANDARD.md](")
		b.WriteString(rulesRel)
		b.WriteString(").\n\n")
	}
	b.WriteString("| dir | purpose | guard | command |\n| --- | --- | --- | --- |\n")

	kids, err := childDirs(root, dir)
	if err != nil {
		b.WriteString("| - | ")
		b.WriteString(err.Error())
		b.WriteString(" | - | - |\n")
		return b.String()
	}
	for _, name := range kids {
		path := name
		if dir != "" {
			path = dir + "/" + name
		}
		e, ok := idx[path]
		if !ok {
			e = Entry{Path: path, Purpose: "-", Guard: "-", Command: "-"}
		}
		b.WriteString(renderRow(e))
	}
	return b.String()
}

// embedStandard returns docs/STANDARD.md whole, each heading one level down so
// the page keeps its one title. A missing file yields a pointer line; Render
// reports the absence as an issue.
func embedStandard(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StandardDoc)))
	if err != nil {
		return "The standard: [" + StandardDoc + "](" + StandardDoc + ").\n\n"
	}
	var b strings.Builder
	b.WriteString("The standard below is [" + StandardDoc + "](" + StandardDoc + "), embedded whole; every PR meets it.\n\n")
	inFence := false
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "#") {
			line = "#" + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func renderRow(e Entry) string {
	label := "`" + filepath.Base(e.Path) + "/`"
	if e.Page {
		rel := filepath.Base(e.Path) + "/AGENTS.md"
		label = "[" + filepath.Base(e.Path) + "/](" + rel + ")"
	}
	guard := e.Guard
	if guard != "-" && guard != "none" {
		guard = "`" + guard + "`"
	}
	cmd := e.Command
	if cmd != "-" && cmd != "none" {
		cmd = "`" + cmd + "`"
	}
	return fmt.Sprintf("| %s | %s | %s | %s |\n", label, e.Purpose, guard, cmd)
}

func childDirs(root, rel string) ([]string, error) {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, filepath.FromSlash(rel))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if skipDir(name) {
			continue
		}
		if !e.IsDir() {
			if e.Type()&fs.ModeSymlink != 0 {
				st, err := os.Stat(filepath.Join(dir, name))
				if err != nil || !st.IsDir() {
					continue
				}
			} else {
				continue
			}
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func skipDir(name string) bool {
	if skipNames[name] {
		return true
	}
	return strings.HasPrefix(name, ".") && name != ".github"
}

func parentPath(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

// Check holds the committed pages against a fresh Render. A mapped directory
// that grew a child, a catalog row that was edited, or a hand-edit of a page
// all fail with "stale; run: make map".
func Check(root string, cat []Entry) []string {
	pages, issues := Render(root, cat)
	for _, path := range slices.Sorted(maps.Keys(pages)) {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s is missing; run: make map", path))
			continue
		}
		if string(got) != pages[path] {
			issues = append(issues, fmt.Sprintf("%s is stale; run: make map", path))
		}
	}
	issues = append(issues, extraAgentsPages(root, pages)...)
	return issues
}

func extraAgentsPages(root string, pages map[string]string) []string {
	var extra []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipNames[d.Name()] {
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") && d.Name() != ".github" {
				return filepath.SkipDir
			}
			// deprecated/ is out of scope of the testing drive (Glenn 2026-09-27); see deprecated/README.md
			if path == filepath.Join(root, deprecatedDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != RootAgents {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := pages[rel]; !ok {
			extra = append(extra, fmt.Sprintf("%s is not generated by internal/docs; delete it or add a Page row and run: make map", rel))
		}
		return nil
	})
	if err != nil {
		return []string{err.Error()}
	}
	slices.Sort(extra)
	return extra
}

func Write(root string, pages map[string]string) error {
	for _, path := range slices.Sorted(maps.Keys(pages)) {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(pages[path]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RepoRoot walks up from the current working directory to find Makefile.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no Makefile in any parent of working directory")
		}
		dir = parent
	}
}

func RunAgentsMap(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: make map (or go run ./tools/agentsmap)")
	}
	root, err := RepoRoot()
	if err != nil {
		return err
	}
	pages, issues := Render(root, DefaultCatalog)
	if len(issues) > 0 {
		for _, s := range issues {
			fmt.Fprintf(os.Stderr, "agentsmap: %s\n", s)
		}
		return fmt.Errorf("%d issue(s) found", len(issues))
	}
	if err := Write(root, pages); err != nil {
		return err
	}
	fmt.Printf("wrote %d AGENTS.md pages\n", len(pages))
	return nil
}
