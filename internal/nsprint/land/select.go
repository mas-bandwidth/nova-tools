// Package land provides test selection over base and train dependency graph unions
// per GOOS and tag sets (nova-tools #3139 section 5.4, B4, controls L9/L9b/L9c/L9d/L9e).
package land

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// ClassGo is the class when only Go packages, their testdata or embeds changed.
	ClassGo = "go"
	// ClassVetWin is the class when a cmd/ file or a windows build constraint changed.
	ClassVetWin = "+vetwin"
	// ClassLisp is the class when lisp/, *.asd or docs/roadmaps/nova-work.sexp changed.
	ClassLisp = "lisp"
	// ClassFull is the class for go.mod, go.sum, .github/, Makefile, fleet/land/,
	// any unmapped path, CI singles, or tip gates.
	ClassFull = "full"

	// MaxCachedGraphs is the bound on cached selection graph keys per repo index.
	MaxCachedGraphs = 2000
)

// PackageInfo is the JSON structure emitted by go list -deps -test -json ./...
type PackageInfo struct {
	Dir             string   `json:"Dir"`
	ImportPath      string   `json:"ImportPath"`
	Name            string   `json:"Name"`
	GoFiles         []string `json:"GoFiles"`
	TestGoFiles     []string `json:"TestGoFiles"`
	XTestGoFiles    []string `json:"XTestGoFiles"`
	EmbedFiles      []string `json:"EmbedFiles"`
	TestEmbedFiles  []string `json:"TestEmbedFiles"`
	XTestEmbedFiles []string `json:"XTestEmbedFiles"`
	Imports         []string `json:"Imports"`
	TestImports     []string `json:"TestImports"`
	XTestImports    []string `json:"XTestImports"`
}

// Graph holds the reverse dependency graph and file-to-package mappings for a given
// tree, GOOS and build tag configuration.
type Graph struct {
	Tree        string
	GOOS        string
	Tags        []string
	Packages    map[string]PackageInfo
	ReverseDeps map[string][]string // dep import path -> importing package paths
	FileToPkg   map[string]string   // clean relative file path -> package import path
	DirToPkg    map[string]string   // clean relative dir -> package import path
}

// Step represents one execution step in a gate.
type Step struct {
	Name string   `json:"name"`
	Cmd  []string `json:"cmd"`
	Env  []string `json:"env,omitempty"`
}

// SelectionResult is the outcome of test selection over a batch of changed files.
type SelectionResult struct {
	Class    string   `json:"class"`
	Packages []string `json:"packages"`
	Checks   string   `json:"checks"`
	Steps    []Step   `json:"steps"`
}

// SelectionLine formats the gate log line: "checks=selected packages=<n>".
func SelectionLine(packages []string) string {
	return fmt.Sprintf("checks=selected packages=%d", len(packages))
}

// ConfigHash computes sha256(canonical tag set (sorted, comma-joined), go version,
// GOFLAGS, the sel_goos and tags section of fleet/land/<repo>.yml) as specified in 5.4.
func ConfigHash(tags []string, goVersion, goFlags, policySection string) string {
	sortedTags := make([]string, len(tags))
	copy(sortedTags, tags)
	sort.Strings(sortedTags)
	canonicalTags := strings.Join(sortedTags, ",")

	h := sha256.New()
	h.Write([]byte(canonicalTags))
	h.Write([]byte("\n"))
	h.Write([]byte(goVersion))
	h.Write([]byte("\n"))
	h.Write([]byte(goFlags))
	h.Write([]byte("\n"))
	h.Write([]byte(policySection))
	return hex.EncodeToString(h.Sum(nil))
}

// SelKey returns the Redis key for a cached selection graph: land:<repo>:sel:<tree>:<goos>:<cfg>.
func SelKey(repo, tree, goos, cfg string) string {
	return fmt.Sprintf("land:%s:sel:%s:%s:%s", repo, tree, goos, cfg)
}

// SelIndexKey returns the Redis sorted set key for the selection index: land:<repo>:sel:idx.
func SelIndexKey(repo string) string {
	return fmt.Sprintf("land:%s:sel:idx", repo)
}

// ParseGoListJSON decodes the stream of JSON objects from go list -deps -test -json ./...
func ParseGoListJSON(r io.Reader) ([]PackageInfo, error) {
	dec := json.NewDecoder(r)
	var pkgs []PackageInfo
	for dec.More() {
		var p PackageInfo
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// NewGraph constructs a Graph from package information, building reverse dependency edges
// and mapping package files, embeds, and testdata paths.
func NewGraph(tree, goos string, tags []string, pkgs []PackageInfo, repoRoot ...string) *Graph {
	var root string
	if len(repoRoot) > 0 {
		root = repoRoot[0]
	}

	g := &Graph{
		Tree:        tree,
		GOOS:        goos,
		Tags:        tags,
		Packages:    make(map[string]PackageInfo, len(pkgs)),
		ReverseDeps: make(map[string][]string),
		FileToPkg:   make(map[string]string),
		DirToPkg:    make(map[string]string),
	}

	for _, p := range pkgs {
		g.Packages[p.ImportPath] = p

		relDir := p.Dir
		if root != "" && filepath.IsAbs(p.Dir) {
			if rel, err := filepath.Rel(root, p.Dir); err == nil {
				relDir = rel
			}
		}
		relDir = filepath.Clean(filepath.ToSlash(relDir))
		if relDir != "." && relDir != "" {
			g.DirToPkg[relDir] = p.ImportPath
		}

		allFiles := append([]string{}, p.GoFiles...)
		allFiles = append(allFiles, p.TestGoFiles...)
		allFiles = append(allFiles, p.XTestGoFiles...)
		allFiles = append(allFiles, p.EmbedFiles...)
		allFiles = append(allFiles, p.TestEmbedFiles...)
		allFiles = append(allFiles, p.XTestEmbedFiles...)

		for _, f := range allFiles {
			var fullPath string
			if relDir != "." && relDir != "" {
				fullPath = relDir + "/" + f
			} else {
				fullPath = f
			}
			g.FileToPkg[filepath.Clean(filepath.ToSlash(fullPath))] = p.ImportPath
		}

		allDeps := make(map[string]bool)
		for _, imp := range p.Imports {
			allDeps[imp] = true
		}
		for _, imp := range p.TestImports {
			allDeps[imp] = true
		}
		for _, imp := range p.XTestImports {
			allDeps[imp] = true
		}

		for dep := range allDeps {
			g.ReverseDeps[dep] = append(g.ReverseDeps[dep], p.ImportPath)
		}
	}

	for dep, revs := range g.ReverseDeps {
		g.ReverseDeps[dep] = dedupeAndSort(revs)
	}

	return g
}

// MapsFile reports whether the graph recognizes the given file as belonging to a package,
// including Go source files, explicit embeds, or files inside a package's testdata/ directory.
func (g *Graph) MapsFile(path string) bool {
	p := filepath.Clean(filepath.ToSlash(path))
	if _, ok := g.FileToPkg[p]; ok {
		return true
	}
	for dir := range g.DirToPkg {
		prefix := dir + "/testdata/"
		if strings.HasPrefix(p, prefix) || p == dir+"/testdata" {
			return true
		}
	}
	return false
}

// FilePackage returns the owning package import path for a file path, if known.
func (g *Graph) FilePackage(path string) (string, bool) {
	p := filepath.Clean(filepath.ToSlash(path))
	if pkg, ok := g.FileToPkg[p]; ok {
		return pkg, true
	}
	for dir, pkg := range g.DirToPkg {
		prefix := dir + "/testdata/"
		if strings.HasPrefix(p, prefix) || p == dir+"/testdata" {
			return pkg, true
		}
		if strings.HasPrefix(p, dir+"/") && strings.HasSuffix(p, ".go") {
			return pkg, true
		}
	}
	return "", false
}

// UnionGraphs computes the union of multiple dependency graphs (base + train across GOOS
// and tag sets). Reverse dependency edges from any input graph are preserved in the union.
func UnionGraphs(graphs ...*Graph) *Graph {
	union := &Graph{
		Packages:    make(map[string]PackageInfo),
		ReverseDeps: make(map[string][]string),
		FileToPkg:   make(map[string]string),
		DirToPkg:    make(map[string]string),
	}

	revMap := make(map[string]map[string]bool)

	for _, g := range graphs {
		if g == nil {
			continue
		}
		for path, pkg := range g.Packages {
			if _, exists := union.Packages[path]; !exists {
				union.Packages[path] = pkg
			}
		}
		for f, pkg := range g.FileToPkg {
			union.FileToPkg[f] = pkg
		}
		for dir, pkg := range g.DirToPkg {
			union.DirToPkg[dir] = pkg
		}
		for dep, revs := range g.ReverseDeps {
			if revMap[dep] == nil {
				revMap[dep] = make(map[string]bool)
			}
			for _, r := range revs {
				revMap[dep][r] = true
			}
		}
	}

	for dep, revs := range revMap {
		list := make([]string, 0, len(revs))
		for r := range revs {
			list = append(list, r)
		}
		sort.Strings(list)
		union.ReverseDeps[dep] = list
	}

	return union
}

// isFullRulePath checks paths that mandate class full per 5.4.
func isFullRulePath(path string) bool {
	p := filepath.Clean(filepath.ToSlash(path))
	if p == "go.mod" || p == "go.sum" || p == "Makefile" {
		return true
	}
	if strings.HasPrefix(p, ".github/") || strings.HasPrefix(p, "fleet/land/") || strings.HasPrefix(p, "vendor/") {
		return true
	}
	return false
}

// isLispRulePath checks paths that mandate class lisp per 5.4.
func isLispRulePath(path string) bool {
	p := filepath.Clean(filepath.ToSlash(path))
	if strings.HasPrefix(p, "lisp/") || strings.HasSuffix(p, ".asd") || p == "docs/roadmaps/nova-work.sexp" {
		return true
	}
	return false
}

// isVetWinRulePath checks paths that mandate class +vetwin per 5.4.
func isVetWinRulePath(path string) bool {
	p := filepath.Clean(filepath.ToSlash(path))
	if strings.HasPrefix(p, "cmd/") || strings.HasSuffix(p, "_windows.go") {
		return true
	}
	return false
}

// Classify determines the gate class (go, +vetwin, lisp, full) from the list of changed files
// and the graph's file mappings (section 5.4).
func Classify(changedFiles []string, g *Graph) string {
	if len(changedFiles) == 0 {
		return ClassGo
	}
	hasVetWin := false
	hasLisp := false

	// 1. Explicit class full triggers
	for _, f := range changedFiles {
		if isFullRulePath(f) {
			return ClassFull
		}
	}

	// 2. Unmapped paths check
	for _, f := range changedFiles {
		if isLispRulePath(f) {
			hasLisp = true
			continue
		}
		if g != nil && g.MapsFile(f) {
			if isVetWinRulePath(f) {
				hasVetWin = true
			}
			continue
		}
		p := filepath.Clean(filepath.ToSlash(f))
		if strings.HasSuffix(p, ".go") || strings.Contains(p, "/testdata/") {
			if isVetWinRulePath(f) {
				hasVetWin = true
			}
			continue
		}
		// Non-Go file not mapped through EmbedFiles or testdata/ -> full
		return ClassFull
	}

	if hasLisp {
		return ClassLisp
	}
	if hasVetWin {
		return ClassVetWin
	}
	return ClassGo
}

// PlanSteps determines the gate steps for a given class and selected packages (section 5.4).
func PlanSteps(class string, selectedPkgs []string, requiredSet ...Step) []Step {
	switch class {
	case ClassGo:
		return []Step{
			{Name: "build", Cmd: []string{"go", "build", "./..."}},
			{Name: "vet", Cmd: append([]string{"go", "vet"}, selectedPkgs...)},
			{Name: "test", Cmd: append([]string{"go", "test", "-count=1", "-json"}, selectedPkgs...)},
		}
	case ClassVetWin:
		return []Step{
			{Name: "build", Cmd: []string{"go", "build", "./..."}},
			{Name: "vet", Cmd: append([]string{"go", "vet"}, selectedPkgs...)},
			{Name: "vetwin", Cmd: append([]string{"go", "vet"}, selectedPkgs...), Env: []string{"GOOS=windows"}},
			{Name: "test", Cmd: append([]string{"go", "test", "-count=1", "-json"}, selectedPkgs...)},
		}
	case ClassLisp:
		return []Step{
			{Name: "build", Cmd: []string{"go", "build", "./..."}},
			{Name: "vet", Cmd: append([]string{"go", "vet"}, selectedPkgs...)},
			{Name: "test", Cmd: append([]string{"go", "test", "-count=1", "-json"}, selectedPkgs...)},
			{Name: "lisp", Cmd: []string{"sbcl", "--non-interactive", "--eval", "(asdf:test-system :nova)"}},
		}
	case ClassFull:
		if len(requiredSet) > 0 {
			return requiredSet
		}
		return []Step{
			{Name: "build", Cmd: []string{"go", "build", "./..."}},
			{Name: "vet", Cmd: []string{"go", "vet", "./..."}},
			{Name: "vetwin", Cmd: []string{"go", "vet", "./..."}, Env: []string{"GOOS=windows"}},
			{Name: "test", Cmd: []string{"go", "test", "-count=1", "-json", "./..."}},
			{Name: "lisp", Cmd: []string{"sbcl", "--non-interactive", "--eval", "(asdf:test-system :nova)"}},
		}
	default:
		return []Step{
			{Name: "build", Cmd: []string{"go", "build", "./..."}},
			{Name: "vet", Cmd: append([]string{"go", "vet"}, selectedPkgs...)},
			{Name: "test", Cmd: append([]string{"go", "test", "-count=1", "-json"}, selectedPkgs...)},
		}
	}
}

// Select computes the selected packages and steps for a set of changed files on a dependency graph.
// The selection is every package owning a changed file plus every package that imports one in the graph, transitively.
func Select(changedFiles []string, g *Graph) SelectionResult {
	class := Classify(changedFiles, g)

	changedPkgs := make(map[string]bool)
	if g != nil {
		for _, f := range changedFiles {
			if pkg, ok := g.FilePackage(f); ok {
				changedPkgs[pkg] = true
			}
		}
	}

	selectedMap := make(map[string]bool)
	queue := make([]string, 0, len(changedPkgs))
	for pkg := range changedPkgs {
		selectedMap[pkg] = true
		queue = append(queue, pkg)
	}

	if g != nil {
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			for _, rev := range g.ReverseDeps[curr] {
				if !selectedMap[rev] {
					selectedMap[rev] = true
					queue = append(queue, rev)
				}
			}
		}
	}

	selectedList := make([]string, 0, len(selectedMap))
	for pkg := range selectedMap {
		selectedList = append(selectedList, pkg)
	}
	sort.Strings(selectedList)

	steps := PlanSteps(class, selectedList)

	return SelectionResult{
		Class:    class,
		Packages: selectedList,
		Checks:   SelectionLine(selectedList),
		Steps:    steps,
	}
}

// SelectUnion computes test selection over the union of base and train dependency graphs (Stella 5).
func SelectUnion(changedFiles []string, baseGraph, trainGraph *Graph) SelectionResult {
	union := UnionGraphs(baseGraph, trainGraph)
	return Select(changedFiles, union)
}

// SelectMulti computes test selection across multiple base and train graphs for all GOOS in sel_goos
// and configured tag sets.
func SelectMulti(changedFiles []string, baseGraphs, trainGraphs []*Graph) SelectionResult {
	all := append([]*Graph{}, baseGraphs...)
	all = append(all, trainGraphs...)
	union := UnionGraphs(all...)
	return Select(changedFiles, union)
}

// LoadLiveGraph runs go list -deps -test -json ./... in the specified directory with the given
// GOOS and build tags, constructing the live dependency graph.
func LoadLiveGraph(ctx context.Context, dir, goos string, tags []string) (*Graph, error) {
	args := []string{"list", "-deps", "-test", "-json"}
	if len(tags) > 0 {
		args = append(args, fmt.Sprintf("-tags=%s", strings.Join(tags, ",")))
	}
	args = append(args, "./...")

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	if goos != "" {
		cmd.Env = append(os.Environ(), "GOOS="+goos)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	pkgs, err := ParseGoListJSON(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("decode go list JSON: %w", err)
	}
	return NewGraph("", goos, tags, pkgs, dir), nil
}

// nsSelPutScript implements ns_sel_put: set-if-absent, ZADD to index with score 'at',
// and bound index to 2,000 keys by deleting oldest keys (section 2.2, L9e).
var nsSelPutScript = redis.NewScript(`
local key = KEYS[1]
local idx_key = KEYS[2]
local at = tonumber(ARGV[1])

local exists = redis.call('EXISTS', key)
if exists == 0 then
    if #ARGV > 1 then
        local hset_args = {key}
        for i = 2, #ARGV do
            table.insert(hset_args, ARGV[i])
        end
        redis.call('HSET', unpack(hset_args))
    else
        redis.call('HSET', key, '_init', '1')
    end
    redis.call('ZADD', idx_key, at, key)
    local count = redis.call('ZCARD', idx_key)
    if count > 2000 then
        local excess = count - 2000
        local to_remove = redis.call('ZRANGE', idx_key, 0, excess - 1)
        for _, rem_key in ipairs(to_remove) do
            redis.call('DEL', rem_key)
        end
        redis.call('ZREMRANGEBYRANK', idx_key, 0, excess - 1)
    end
    return 1
else
    return 0
end
`)

// SelPut stores the reverse dependencies graph in Redis using ns_sel_put.
// It sets the hash if absent, updates the index, and bounds the cache to 2,000 keys without TTL.
func SelPut(ctx context.Context, rdb redis.Cmdable, repo, tree, goos, cfg string, at int64, reverseDeps map[string][]string) (bool, error) {
	key := SelKey(repo, tree, goos, cfg)
	idxKey := SelIndexKey(repo)

	argv := []interface{}{at}
	for pkg, revs := range reverseDeps {
		b, err := json.Marshal(revs)
		if err != nil {
			return false, err
		}
		argv = append(argv, pkg, string(b))
	}

	res, err := nsSelPutScript.Run(ctx, rdb, []string{key, idxKey}, argv...).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// SelGet retrieves the cached reverse dependency graph from Redis.
func SelGet(ctx context.Context, rdb redis.Cmdable, repo, tree, goos, cfg string) (map[string][]string, bool, error) {
	key := SelKey(repo, tree, goos, cfg)
	exists, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, nil
	}
	vals, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, false, err
	}
	res := make(map[string][]string, len(vals))
	for pkg, raw := range vals {
		if pkg == "_init" {
			continue
		}
		var revs []string
		if err := json.Unmarshal([]byte(raw), &revs); err != nil {
			continue
		}
		res[pkg] = revs
	}
	return res, true, nil
}

func dedupeAndSort(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
