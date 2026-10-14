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
// being read. The page carries the directory map and a link to
// docs/STANDARD.md (StandardDoc), not the standard's body; the standard is one
// source a worker reads on demand (docs/STANDARD.md section 7: less code is
// the best code). The ceiling leaves headroom for catalog rows, and a growth
// past it is shortened at the catalog rows, never by raising the number
// unread.
const MaxRootBytes = 32 * 1024

// StandardDoc is the one source of the standard every tool is built to. The
// root page links it; a worker reads it on demand.
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

// Render builds STANDARD's class-rule list and every AGENTS.md page from the
// spec, live tree and catalog.
// Uncatalogued children still appear (as "-" rows) so a committed page goes
// stale the moment a mapped directory grows a child. Completeness issues are
// returned beside the pages; make map refuses to write when any are present.
func Render(root string, cat []Entry) (map[string]string, []string) {
	idx, issues := IndexCatalog(cat)
	issues = append(issues, treeIssues(root, cat, idx)...)

	pages := make(map[string]string)
	standard, err := readClassRuleStandard(root)
	if err != nil {
		issues = append(issues, err.Error())
	} else {
		pages[StandardDoc] = standard
	}
	pages[RootAgents] = renderPage(root, "", idx)
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
		b.WriteString("Nova Tools is machinery: command-line tools that AI friends and people run against their own records, on their own machines, with their own identities. Adoption is a choice — one tool is a fine number. The standard is linked; how review goes: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).\n\n")
		b.WriteString("```\nmake build          # go build ./...\nmake test           # the fast tier, plus the per-package time budget\nmake map            # regenerate AGENTS.md and per-directory maps\n```\n\n")
		b.WriteString(linkStandard())
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

// linkStandard returns the one-line link to the standard the root page carries
// instead of the standard's body. docs/STANDARD.md section 7 (less code is the
// best code): the standard is one source a worker reads on demand, not a
// second copy kept in the page.
func linkStandard() string {
	return "The standard is [" + StandardDoc + "](" + StandardDoc + "), linked not embedded; every PR meets it.\n\n"
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
	fmt.Printf("wrote %d AGENTS.md pages and %s\n", len(pages)-1, StandardDoc)
	return nil
}
