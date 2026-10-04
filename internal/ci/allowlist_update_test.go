package ci

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shrinkOnly is the options of every list in internal/ci/testdata: keyed by the
// row's first field, and ceiling-only -- each list's header says it only shrinks,
// so NOVA_CI_UPDATE=1 drops stale rows and refuses to add one (nova-tools#4339).
var shrinkOnly = allowlist.Options{Ceiling: true}

// loadAllowlist reads a class test's list through the one helper; a list that
// cannot be read is a broken test, not an empty list.
func loadAllowlist(t *testing.T, path string, opt allowlist.Options) *allowlist.List {
	t.Helper()
	l, err := allowlist.Load(filepath.FromSlash(path), opt)
	require.NoError(t, err)
	return l
}

// listFilePatterns name the allowlists under internal/ci/testdata: the shapes
// every list file there is spelt in today.
var listFilePatterns = []string{"*allowlist*.txt", "*.allow", "*_examples.txt"}

// countedShardDirectories are the class ledgers whose rows are split by package.
// LoadPackages owns every .txt shard below one of these directories, including
// future package shards and the @root shard.
var countedShardDirectories = []string{
	"discarded", "scripthide", "okonfailure", "remedy", "generality", "generality-text", "testify",
}

// TestEveryAllowlistIsReadThroughTheOneHelper is the class test of #4339: every
// top-level list is loaded by loadAllowlist or allowlist.Load, every present
// package-ledger directory is consumed by allowlist.LoadPackages, and no Go file
// reads a list or shard directly. A list without the shared updater has no
// NOVA_CI_UPDATE=1 path, and a script would be back to editing it by hand; a
// second reader outside internal/ci is a second parser of the same format.
//
// The walk is syntactic and per package. A call's path argument is resolved
// through string literals (filepath.Join parts included), package constants, a
// local variable assigned in the same function, and one level of function
// parameter (every call site's argument); a path computed any other way is not
// seen. A package none of whose files spells a list file or counted-ledger
// directory is not parsed: the resolver could not reach a list from it.
func TestEveryAllowlistIsReadThroughTheOneHelper(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	lists := map[string]bool{}
	for _, pat := range listFilePatterns {
		matches, err := filepath.Glob(filepath.Join(root, "internal", "ci", "testdata", pat))
		require.NoError(t, err)
		for _, m := range matches {
			lists[filepath.Base(m)] = true
		}
	}
	loaded, raw := treeHelperReads(t, root, lists)
	shardDirs := existingShardDirectories(t, root)
	require.True(t, len(lists) > 0 || len(shardDirs) > 0, "no top-level list or counted ledger under internal/ci/testdata; the walk is looking in the wrong place")
	for _, name := range mapKeysSorted(lists) {
		assert.True(t, loaded[name], "internal/ci/testdata/%s is not read through allowlist.Load (loadAllowlist in a test); its class test has no %s=1 path, so a removal would edit it by hand",
			name, allowlist.UpdateEnv)
	}
	for _, dir := range unconsumedShardDirectories(shardDirs, loaded) {
		t.Errorf("internal/ci/testdata/%s has package shards but no allowlist.LoadPackages call consumes the directory", dir)
	}
	for _, r := range raw {
		t.Errorf("%s reads a list file or shard directory directly; use loadAllowlist, allowlist.Load, or allowlist.LoadPackages (nova-tools#4339)", r)
	}
}

// treeHelperReads walks every Go package under root (skipping .git, testdata and
// vendor) and returns the list files a helper call loads and every raw read of
// one, each read named by its path relative to root. Counted ledgers are matched
// by their directory and every recursive .txt shard belongs to that load.
func treeHelperReads(t *testing.T, root string, lists map[string]bool) (map[string]bool, []string) {
	t.Helper()
	var tree *repoTreeIndex
	if root == repoRoot(t) {
		tree = repoTree(t)
	} else {
		var err error
		tree, err = loadRepoTree(root)
		require.NoError(t, err)
	}
	pkgs := map[string][]*treeFile{}
	for _, f := range tree.Files {
		if !f.Go || f.AST == nil {
			continue
		}
		if f.HasDirNamed(".git") || f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("node_modules") {
			continue
		}
		dir := filepath.Dir(f.Rel)
		pkgs[dir] = append(pkgs[dir], f)
	}

	listBytes := make([][]byte, 0, len(lists))
	for name := range lists {
		listBytes = append(listBytes, []byte(name))
	}
	packageDirs, shardsByDir := packageShardInventory(t, root, lists)
	for dir := range packageDirs {
		listBytes = append(listBytes, []byte("testdata/"+dir), []byte(dir))
	}

	loaded := map[string]bool{}
	var raw []string
	for _, files := range pkgs {
		hasName := false
		for _, f := range files {
			for _, lb := range listBytes {
				if bytes.Contains(f.Src, lb) {
					hasName = true
					break
				}
			}
			if hasName {
				break
			}
		}
		if !hasName {
			continue
		}
		l, r := helperReadsTree(t, tree.Root, tree.FSet, files, lists, packageDirs, shardsByDir)
		for name := range l {
			loaded[name] = true
		}
		raw = append(raw, r...)
	}
	sort.Strings(raw)
	return loaded, raw
}

func helperReadsTree(t *testing.T, root string, fset *token.FileSet, files []*treeFile, lists map[string]bool, packageDirs map[string]bool, shardsByDir map[string][]string) (map[string]bool, []string) {
	t.Helper()
	consts := map[string]string{}
	funcs := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, decl := range f.AST.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil {
									consts[name.Name] = v
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Body != nil {
					funcs[d.Name.Name] = d
				}
			}
		}
	}

	r := listResolver{lists: lists, packageDirs: packageDirs, consts: consts, funcs: funcs}
	loaded := map[string]bool{}
	var raw []string
	for _, fn := range funcs {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call.Fun) {
			case "loadAllowlist":
				if len(call.Args) > 1 {
					for _, name := range r.resolve(fn, call.Args[1], 0) {
						loaded[name] = true
					}
				}
			case "allowlist.Load", "allowlist.Parse":
				if len(call.Args) > 0 {
					for _, name := range r.resolve(fn, call.Args[0], 0) {
						loaded[name] = true
					}
				}
			case "allowlist.LoadPackages":
				if len(call.Args) > 0 {
					for _, key := range r.resolve(fn, call.Args[0], 0) {
						if !strings.HasPrefix(key, "@dir:") {
							continue
						}
						dir := strings.TrimPrefix(key, "@dir:")
						loaded[key] = true
						for _, shard := range shardsByDir[dir] {
							loaded[shard] = true
						}
					}
				}
			case "os.ReadFile", "os.Open", "readFile":
				if len(call.Args) == 0 {
					return true
				}
				arg := call.Args[len(call.Args)-1]
				for _, name := range r.resolve(fn, arg, 0) {
					if strings.HasPrefix(name, "@dir:") {
						name = "shard directory " + strings.TrimPrefix(name, "@dir:")
					}
					pos := fset.Position(call.Pos())
					if rel, err := filepath.Rel(root, pos.Filename); err == nil {
						pos.Filename = filepath.ToSlash(rel)
					}
					raw = append(raw, fmt.Sprintf("%s: %s(%s)", pos, callName(call.Fun), name))
				}
			}
			return true
		})
	}
	sort.Strings(raw)
	return loaded, raw
}

// callName spells a call's function as `name` or `pkg.name`.
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// listResolver turns a path expression into the list files it names.
type listResolver struct {
	lists       map[string]bool
	packageDirs map[string]bool
	consts      map[string]string
	funcs       map[string]*ast.FuncDecl
	activeLocal map[resolverLocalKey]bool
}

func (r listResolver) resolve(fn *ast.FuncDecl, e ast.Expr, depth int) []string {
	if r.activeLocal == nil {
		r.activeLocal = make(map[resolverLocalKey]bool)
	}
	var out []string
	add := func(v string) {
		clean := ledgerPath(v)
		if r.lists[clean] {
			out = append(out, clean)
			return
		}
		if !strings.Contains(clean, "/") && r.lists[path.Base(clean)] {
			out = append(out, path.Base(clean))
			return
		}
		if r.packageDirs[clean] {
			out = append(out, packageLedgerKey(clean))
		}
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if v, err := strconv.Unquote(x.Value); err == nil {
					add(v)
				}
			}
		case *ast.Ident:
			if v, ok := r.consts[x.Name]; ok {
				add(v)
				return true
			}
			out = append(out, r.local(fn, x.Name, depth)...)
		}
		return true
	})
	return out
}

type resolverLocalKey struct {
	fn   *ast.FuncDecl
	name string
}

// ledgerPath removes the package working-directory prefix while preserving a
// nested shard path. The first path component under testdata identifies a ledger.
func ledgerPath(value string) string {
	clean := filepath.ToSlash(filepath.Clean(value))
	if at := strings.LastIndex(clean, "internal/ci/testdata/"); at >= 0 {
		return clean[at+len("internal/ci/testdata/"):]
	}
	if strings.HasPrefix(clean, "testdata/") {
		return strings.TrimPrefix(clean, "testdata/")
	}
	return strings.TrimPrefix(clean, "./")
}

func packageLedgerKey(dir string) string { return "@dir:" + dir }

// packageShardInventory returns the present counted ledger directories and their
// recursively discovered .txt shards. An absent directory is valid when its debt
// is zero; once a directory exists, the class test must load it as a package ledger.
func packageShardInventory(t *testing.T, root string, lists map[string]bool) (map[string]bool, map[string][]string) {
	t.Helper()
	packageDirs := map[string]bool{}
	shardsByDir := map[string][]string{}
	for _, name := range countedShardDirectories {
		base := filepath.Join(root, "internal", "ci", "testdata", name)
		info, err := os.Stat(base)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err, "stat package ledger %s", name)
		require.True(t, info.IsDir(), "package ledger path %s is not a directory", name)
		packageDirs[name] = true
		err = filepath.WalkDir(base, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".txt" {
				return nil
			}
			rel, err := filepath.Rel(base, file)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(filepath.Join(name, rel))
			lists[key] = true
			shardsByDir[name] = append(shardsByDir[name], key)
			return nil
		})
		require.NoError(t, err, "walk package ledger %s", name)
		sort.Strings(shardsByDir[name])
	}
	return packageDirs, shardsByDir
}

func existingShardDirectories(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	for _, name := range countedShardDirectories {
		base := filepath.Join(root, "internal", "ci", "testdata", name)
		info, err := os.Stat(base)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err, "stat package ledger %s", name)
		if info.IsDir() {
			dirs = append(dirs, name)
		}
	}
	return dirs
}

func unconsumedShardDirectories(dirs []string, loaded map[string]bool) []string {
	var unconsumed []string
	for _, dir := range dirs {
		if !loaded[packageLedgerKey(dir)] {
			unconsumed = append(unconsumed, dir)
		}
	}
	return unconsumed
}

// local resolves a name inside fn: a variable assigned there, or a parameter
// through every call site of fn (one level).
func (r listResolver) local(fn *ast.FuncDecl, name string, depth int) []string {
	if r.activeLocal == nil {
		r.activeLocal = make(map[resolverLocalKey]bool)
	}
	key := resolverLocalKey{fn: fn, name: name}
	if r.activeLocal[key] {
		return nil
	}
	r.activeLocal[key] = true
	defer delete(r.activeLocal, key)

	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				if rhs, ok := as.Rhs[i].(*ast.Ident); ok && rhs.Name == name {
					continue
				}
				out = append(out, r.resolve(fn, as.Rhs[i], depth)...)
			}
		}
		return true
	})
	if depth > 0 {
		return out
	}
	idx, i := -1, 0
	for _, field := range fn.Type.Params.List {
		for _, n := range field.Names {
			if n.Name == name {
				idx = i
			}
			i++
		}
	}
	if idx < 0 {
		return out
	}
	for _, caller := range r.funcs {
		ast.Inspect(caller.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok && callName(call.Fun) == fn.Name.Name && idx < len(call.Args) {
				out = append(out, r.resolve(caller, call.Args[idx], depth+1)...)
			}
			return true
		})
	}
	return out
}

// TestPackageShardGuardRecognizesConsumptionAndRawReads pins the syntactic
// boundary for counted ledgers: one LoadPackages call consumes all recursive
// shards, an unconsumed directory stays visible, and a direct shard read is raw.
func TestPackageShardGuardRecognizesConsumptionAndRawReads(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		ledger     string
		source     string
		wantLoaded bool
		wantRaw    bool
	}{
		{
			name:   "package loader consumes every shard",
			ledger: "discarded",
			source: `package ci
import "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
const ledgerPath = "testdata/discarded"
func readLedger() { _, _ = allowlist.LoadPackages(ledgerPath, allowlist.Options{}) }
`,
			wantLoaded: true,
		},
		{
			name:   "testify ledger is consumed through the same loader",
			ledger: "testify",
			source: `package ci
import "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
func readLedger() { _, _ = allowlist.LoadPackages("testdata/testify", allowlist.Options{PackageKeys: true}) }
`,
			wantLoaded: true,
		},
		{
			name:   "cyclic aliases stop while an independent valid alias resolves",
			ledger: "discarded",
			source: `package ci
import "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
func readLedger() {
	var first string
	var second string
	first = second
	second = first
	_, _ = allowlist.LoadPackages(first, allowlist.Options{})
	validPath := "testdata/discarded"
	_, _ = allowlist.LoadPackages(validPath, allowlist.Options{})
}
`,
			wantLoaded: true,
		},
		{
			name:   "unconsumed shard directory remains visible",
			ledger: "discarded",
			source: `package ci
const ledgerPath = "testdata/discarded"
func mentionLedger() { _ = ledgerPath }
`,
		},
		{
			name:   "raw shard read is reported",
			ledger: "discarded",
			source: `package ci
import "os"
func readShard() { _, _ = os.ReadFile("testdata/discarded/cmd/nova-sprint.txt") }
`,
			wantRaw: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLedgerGuardFixture(t, root, fmt.Sprintf("internal/ci/testdata/%s/cmd/nova-sprint.txt", tc.ledger), "# ceiling: 1\nexample 1 reason\n")
			writeLedgerGuardFixture(t, root, fmt.Sprintf("internal/ci/testdata/%s/@root.txt", tc.ledger), "# ceiling: 0\n")
			writeLedgerGuardFixture(t, root, "internal/ci/class_test.go", tc.source)

			loaded, raw := treeHelperReads(t, root, map[string]bool{})
			unconsumed := unconsumedShardDirectories(existingShardDirectories(t, root), loaded)
			dirLoaded := loaded[packageLedgerKey(tc.ledger)]
			packageShardLoaded := loaded[tc.ledger+"/cmd/nova-sprint.txt"]
			rootShardLoaded := loaded[tc.ledger+"/@root.txt"]
			if tc.wantLoaded {
				assert.True(t, dirLoaded && packageShardLoaded && rootShardLoaded, "LoadPackages should consume directory and package/@root shards: %v", loaded)
				assert.Empty(t, unconsumed, "consumed ledger should not be reported unconsumed")
			} else {
				assert.False(t, dirLoaded || packageShardLoaded || rootShardLoaded, "unconsumed ledger was reported loaded: %v", loaded)
				assert.Equal(t, []string{tc.ledger}, unconsumed, "unconsumed ledger should be reported by the main guard")
			}
			hasRawShard := false
			for _, finding := range raw {
				if strings.Contains(finding, tc.ledger+"/cmd/nova-sprint.txt") {
					hasRawShard = true
				}
			}
			assert.Equal(t, tc.wantRaw, hasRawShard, "raw shard findings: %v", raw)
		})
	}
}

func writeLedgerGuardFixture(t *testing.T, root, rel, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(contents), 0o644))
}
