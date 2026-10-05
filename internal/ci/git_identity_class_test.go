package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitIdentityAllowlistPath is the shrink-only list of scratch-repository commits
// in a _test.go that still do not name an author and a committer on the command,
// one file:function a row.
const gitIdentityAllowlistPath = "testdata/git-identity-allowlist.txt"

// gitIdentityRemedy is the line docs/SPEC-CI.md carries for this rule.
const gitIdentityRemedy = "set GIT_AUTHOR_NAME, GIT_AUTHOR_EMAIL, GIT_COMMITTER_NAME and GIT_COMMITTER_EMAIL on the command through internal/testgit Apply (Test User, test@example.com), or pass -c user.name and -c user.email; a hosted runner has no global git identity"

// gitCommitScript matches a git commit a shell runs. commit-tree and the
// error text "git commit:" do not match: the word commit has to end there.
var gitCommitScript = regexp.MustCompile(`git(?:\s+\S+){0,6}\s+commit(?:\s|$)`)

var gitCloneScript = regexp.MustCompile(`git(?:\s+\S+){0,6}\s+clone(?:\s|$)`)

// gitIDValueOpt is an option that takes the next argv word, so that word is
// not the subcommand.
func gitIDValueOpt(s string) bool {
	switch s {
	case "-C", "-c", "-m", "-b", "-B", "--author", "--date", "-F", "--file", "--message", "--path", "-o", "--output", "--format", "--template", "--cleanup":
		return true
	default:
		return false
	}
}

// TestNoBareGitCommitInATest is the class rule: a test that runs git commit
// in a scratch repository names the author and the committer on that command,
// or in the same function's repository config when that function does not clone.
// A hosted runner has no global git identity.
func TestNoBareGitCommitInATest(t *testing.T) {
	t.Parallel()

	spec := readGitIdentitySpec(t)
	for _, name := range []string{"TestNoBareGitCommitInATest", "TestGitIdentityRuleCatchesABareCommit"} {
		assert.Contains(t, spec, "`"+name+"`", "docs/SPEC-CI.md does not name %s", name)
	}
	assert.Contains(t, spec, gitIdentityRemedy, "docs/SPEC-CI.md does not carry the remedy line")

	tree := repoTree(t)
	var srcs []gitIDSrc
	for _, f := range tree.Files {
		if !f.Go || !f.Test || f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr, "%s: %v", f.Rel, f.ParseErr)
		srcs = append(srcs, gitIDSrc{rel: f.Rel, file: f.AST, fset: tree.FSet})
	}
	require.NotEmpty(t, srcs, "the walk saw no _test.go files")

	allow := loadGitIdentityAllowlist(t)
	seen := map[string]bool{}
	var violations []string
	for _, h := range scanGitIdentity(srcs) {
		seen[h.key] = true
		if allow.Has(h.key) {
			continue
		}
		violations = append(violations, h.where)
	}
	for _, row := range allowlist.Check(t, allow, seen).Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no bare git commit is there any more; delete the stale entry (the list only shrinks)",
			gitIdentityAllowlistPath, row.Key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		assert.Fail(t, v)
	}
}

func readGitIdentitySpec(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	require.NoError(t, err)
	return string(raw)
}

func loadGitIdentityAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	allow := loadAllowlist(t, gitIdentityAllowlistPath, shrinkOnly)
	for _, row := range allow.Rows() {
		_, reason, _ := strings.Cut(row.Text, " ")
		assert.NotEmpty(t, strings.TrimSpace(reason), "%s: %q carries no reason", gitIdentityAllowlistPath, row.Text)
	}
	return allow
}

// TestGitIdentityRuleCatchesABareCommit feeds the scanner planted files: a bare
// commit, a bare helper, a clone after config, a shell commit and a composite
// argv are red; the helper, the four variables, a -c pair, same-function config,
// commit-tree and an error string are green.
func TestGitIdentityRuleCatchesABareCommit(t *testing.T) {
	t.Parallel()

	src := parseGitID(t, "fixture/id_test.go", gitIdentityPlant)
	var got []string
	seen := map[string]bool{}
	for _, h := range scanGitIdentity([]gitIDSrc{src}) {
		if seen[h.key] {
			continue
		}
		seen[h.key] = true
		got = append(got, h.key)
	}
	sort.Strings(got)
	want := []string{
		"fixture/id_test.go:TestBare",
		"fixture/id_test.go:TestCloneThenCommit",
		"fixture/id_test.go:TestCompositeBare",
		"fixture/id_test.go:TestHelperBare",
		"fixture/id_test.go:TestShell",
	}
	require.Equal(t, want, got)
}

func parseGitID(t *testing.T, rel, src string) gitIDSrc {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	require.NoError(t, err)
	return gitIDSrc{rel: rel, file: f, fset: fset}
}

const gitIdentityPlant = `package p

import (
	"os"
	"os/exec"
	"internal/testgit"
)

func TestBare(t *testing.T) {
	cmd := exec.Command("git", "commit", "-m", "x")
	cmd.Run()
}

func TestHelperBare(t *testing.T) {
	bareGit("commit", "-m", "x")
}

func bareGit(args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Run()
}

func TestConfigured(t *testing.T) {
	cmd := exec.Command("git", "config", "user.name", "Test User")
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "x")
	cmd.Run()
}

func TestCloneThenCommit(t *testing.T) {
	cmd := exec.Command("git", "config", "user.name", "Test User")
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Run()
	cmd = exec.Command("git", "clone", "origin", "repo")
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "x")
	cmd.Run()
}

func TestCommitThenClone(t *testing.T) {
	cmd := exec.Command("git", "config", "user.name", "Test User")
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "x")
	cmd.Run()
	cmd = exec.Command("git", "clone", "origin", "repo")
	cmd.Run()
}

func TestApplied(t *testing.T) {
	cmd := exec.Command("git", "commit", "-m", "x")
	testgit.Apply(cmd)
	cmd.Run()
}

func coverGit(args ...string) {
	cmd := exec.Command("git", args...)
	testgit.Apply(cmd)
	cmd.Run()
}

func TestThroughHelper(t *testing.T) {
	coverGit("commit", "-m", "x")
}

func TestDashC(t *testing.T) {
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "x")
	cmd.Run()
}

func TestFour(t *testing.T) {
	cmd := exec.Command("git", "commit", "-m", "x")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com")
	cmd.Run()
}

func TestMessage(t *testing.T) {
	_ = "git commit: author unknown"
}

func TestTree(t *testing.T) {
	cmd := exec.Command("git", "commit-tree", "HEAD")
	cmd.Run()
}

func TestShell(t *testing.T) {
	cmd := exec.Command("sh", "-c", "git commit -m x")
	cmd.Run()
}

func TestShellOK(t *testing.T) {
	cmd := exec.Command("sh", "-c", "git commit -m x")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com")
	cmd.Run()
}

func TestCompositeBare(t *testing.T) {
	args := []string{"commit", "-m", "x"}
	cmd := exec.Command("git", args...)
	cmd.Run()
}

func TestCompositeDash(t *testing.T) {
	args := []string{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "x"}
	cmd := exec.Command("git", args...)
	cmd.Run()
}
`

type gitIDSrc struct {
	rel  string
	file *ast.File
	fset *token.FileSet
}

type gitIDHit struct {
	key   string
	where string
}

type gitIDFile struct {
	rel, pkg              string
	fset                  *token.FileSet
	execName, testgitName string
	gitrunName            string
	imports               map[string]bool
	fileHasFour           bool
	funcs                 []*idFunc
	idx                   *gitIDIndex
}

type gitIDIndex struct {
	fn     map[string]map[string][]*idFunc
	method map[string]map[string][]*idFunc
}

type idFunc struct {
	file                     *gitIDFile
	name, recv, declKey, key string
	parent                   *idFunc
	locals                   map[string]*idFunc
	body                     *ast.BlockStmt
	commandID, repoID        bool
	clonePos                 []token.Pos
	sawTestgit, bundledEnv   bool
	directGit, directShell   bool
	directProgram            bool
	calls                    []idCall
	directSites              []siteDraft
	composites               []siteDraft
	runnerState, safeState   int
}

type idCall struct {
	sel  bool
	name string
	args []ast.Expr
	pos  token.Pos
}

type siteDraft struct {
	pos   token.Pos
	args  []ast.Expr
	shell string
}

func scanGitIdentity(srcs []gitIDSrc) []gitIDHit {
	files := make([]*gitIDFile, 0, len(srcs))
	for _, s := range srcs {
		if s.file == nil {
			continue
		}
		files = append(files, buildGitIDFile(s))
	}
	idx := &gitIDIndex{
		fn:     map[string]map[string][]*idFunc{},
		method: map[string]map[string][]*idFunc{},
	}
	var all []*idFunc
	for _, f := range files {
		f.idx = idx
		if idx.fn[f.pkg] == nil {
			idx.fn[f.pkg] = map[string][]*idFunc{}
			idx.method[f.pkg] = map[string][]*idFunc{}
		}
		for _, fn := range f.funcs {
			all = append(all, fn)
			if fn.parent != nil {
				continue
			}
			if fn.recv == "" {
				idx.fn[f.pkg][fn.name] = append(idx.fn[f.pkg][fn.name], fn)
			} else {
				idx.method[f.pkg][fn.name] = append(idx.method[f.pkg][fn.name], fn)
			}
		}
	}
	for _, fn := range all {
		fn.analyze()
	}
	var hits []gitIDHit
	for _, fn := range all {
		fn.collect(&hits)
	}
	return hits
}

func buildGitIDFile(s gitIDSrc) *gitIDFile {
	f := &gitIDFile{rel: s.rel, fset: s.fset, pkg: path.Dir(s.rel) + "\x00" + s.file.Name.Name}
	f.execName, f.testgitName, f.gitrunName, f.imports = gitIDImports(s.file)
	f.fileHasFour = gitIDFileFour(s.file)
	for _, decl := range s.file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			addIDFunc(f, nil, fn.Name.Name, gitIDRecv(fn), fn.Body)
		}
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, val := range vs.Values {
				lit, ok := val.(*ast.FuncLit)
				if !ok || lit.Body == nil || i >= len(vs.Names) {
					continue
				}
				addIDFunc(f, nil, vs.Names[i].Name, "", lit.Body)
			}
		}
	}
	return f
}

func addIDFunc(file *gitIDFile, parent *idFunc, name, recv string, body *ast.BlockStmt) *idFunc {
	declKey := name
	if recv != "" {
		declKey = recv + "." + name
	}
	if parent != nil {
		declKey = parent.declKey
	}
	fn := &idFunc{
		file:    file,
		name:    name,
		recv:    recv,
		declKey: declKey,
		key:     file.rel + ":" + declKey,
		parent:  parent,
		locals:  map[string]*idFunc{},
		body:    body,
	}
	file.funcs = append(file.funcs, fn)
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt:
			for i, rhs := range x.Rhs {
				lit, ok := rhs.(*ast.FuncLit)
				if !ok || lit.Body == nil || i >= len(x.Lhs) {
					continue
				}
				litName := "lit"
				if id, ok := x.Lhs[i].(*ast.Ident); ok {
					litName = id.Name
					fn.locals[id.Name] = addIDFunc(file, fn, litName, "", lit.Body)
				} else {
					addIDFunc(file, fn, litName, "", lit.Body)
				}
			}
		case *ast.CallExpr:
			for _, a := range x.Args {
				lit, ok := a.(*ast.FuncLit)
				if !ok || lit.Body == nil {
					continue
				}
				addIDFunc(file, fn, "lit", "", lit.Body)
			}
		}
		return true
	})
	return fn
}

func (fn *idFunc) analyze() {
	if fn.body == nil {
		return
	}
	var lits []string
	ast.Inspect(fn.body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		switch x := n.(type) {
		case *ast.BasicLit:
			if s, ok := unquoteLit(x); ok {
				lits = append(lits, s)
			}
		case *ast.AssignStmt:
			fn.noteEnvAssign(x)
		case *ast.CompositeLit:
			fn.noteComposite(x)
		case *ast.CallExpr:
			fn.noteCall(x)
		}
		return true
	})
	fn.commandID = fn.sawTestgit || gitIDFour(lits) || gitIDDashPair(lits) || (fn.bundledEnv && fn.file.fileHasFour)
	fn.repoID = gitIDConfig(lits)
}

func (fn *idFunc) noteEnvAssign(as *ast.AssignStmt) {
	if len(as.Lhs) != len(as.Rhs) {
		return
	}
	for i, lhs := range as.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Env" {
			continue
		}
		if gitIDBundled(as.Rhs[i]) {
			fn.bundledEnv = true
		}
	}
}

func (fn *idFunc) noteComposite(cl *ast.CompositeLit) {
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if ok && id.Name == "Env" && gitIDBundled(kv.Value) {
			fn.bundledEnv = true
		}
	}
	args := compositeArgv(cl)
	if args == nil {
		return
	}
	sub := gitIDSub(args)
	if sub == "clone" {
		fn.clonePos = append(fn.clonePos, cl.Pos())
	}
	if sub == "commit" {
		fn.composites = append(fn.composites, siteDraft{pos: cl.Pos(), args: args})
	}
}

func (fn *idFunc) noteCall(c *ast.CallExpr) {
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		switch {
		case fn.file.testgitName != "" && id.Name == fn.file.testgitName && gitIDTestgitSel(sel.Sel.Name):
			fn.sawTestgit = true
			return
		case fn.file.execName != "" && id.Name == fn.file.execName && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"):
			fn.noteExec(c, sel.Sel.Name == "CommandContext")
			return
		case fn.file.gitrunName != "" && id.Name == fn.file.gitrunName && gitIDGitrunSel(sel.Sel.Name):
			fn.directGit = true
			fn.noteGitArgs(c.Args, c.Pos())
			return
		case fn.file.imports[id.Name]:
			return
		}
		fn.calls = append(fn.calls, idCall{sel: true, name: sel.Sel.Name, args: c.Args, pos: c.Pos()})
		fn.noteArgSub(c.Args, c.Pos())
		return
	}
	id, ok := c.Fun.(*ast.Ident)
	if !ok {
		return
	}
	// The helper's own tests call Apply and Env by name. Anywhere else those
	// names are ordinary functions and do not count as the fixture.
	if (id.Name == "Apply" || id.Name == "Env") && strings.HasPrefix(fn.file.rel, "internal/testgit/") {
		fn.sawTestgit = true
	}
	fn.calls = append(fn.calls, idCall{name: id.Name, args: c.Args, pos: c.Pos()})
	fn.noteArgSub(c.Args, c.Pos())
}

func (fn *idFunc) noteExec(c *ast.CallExpr, hasCtx bool) {
	args := c.Args
	if hasCtx {
		if len(args) == 0 {
			return
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	prog, ok := unquoteExpr(args[0])
	rest := args[1:]
	if !ok {
		fn.directProgram = true
		return
	}
	switch prog {
	case "git":
		fn.directGit = true
		fn.noteGitArgs(rest, c.Pos())
	case "sh", "bash", "/bin/sh", "/bin/bash":
		fn.directShell = true
		script := scriptArg(rest)
		if gitCommitScript.MatchString(script) {
			fn.directSites = append(fn.directSites, siteDraft{pos: c.Pos(), args: rest, shell: script})
		}
		if gitCloneScript.MatchString(script) {
			fn.clonePos = append(fn.clonePos, c.Pos())
		}
	}
}

func (fn *idFunc) noteGitArgs(args []ast.Expr, pos token.Pos) {
	sub := gitIDSub(args)
	if sub == "clone" {
		fn.clonePos = append(fn.clonePos, pos)
	}
	if sub == "commit" {
		fn.directSites = append(fn.directSites, siteDraft{pos: pos, args: args})
	}
}

func (fn *idFunc) noteArgSub(args []ast.Expr, pos token.Pos) {
	sub, explicit := argvSub(args)
	if sub == "clone" || (explicit && sub == "clone") {
		fn.clonePos = append(fn.clonePos, pos)
	}
	for _, a := range args {
		s, ok := unquoteExpr(a)
		if ok && gitCloneScript.MatchString(s) {
			fn.clonePos = append(fn.clonePos, pos)
		}
	}
}

func (fn *idFunc) collect(hits *[]gitIDHit) {
	for _, s := range fn.directSites {
		if fn.covers(s.pos, s.args, s.shell, nil) {
			continue
		}
		fn.addHit(hits, s.pos)
	}
	if fn.directGit || fn.directProgram {
		for _, s := range fn.composites {
			if fn.covers(s.pos, s.args, "", nil) {
				continue
			}
			fn.addHit(hits, s.pos)
		}
	}
	for _, c := range fn.calls {
		cals := fn.resolve(c)
		var runners []*idFunc
		for _, cal := range cals {
			if cal.isRunner() {
				runners = append(runners, cal)
			}
		}
		if len(runners) == 0 {
			continue
		}
		sub, explicit := argvSub(c.args)
		hit := false
		shell := ""
		for _, cal := range runners {
			if cal.directGit && sub == "commit" {
				hit = true
			}
			if cal.directProgram && explicit && sub == "commit" {
				hit = true
			}
			if !cal.directGit && !cal.directProgram && !cal.directShell && sub == "commit" {
				hit = true
			}
			if cal.directShell && scriptHits(c.args) {
				hit = true
				shell = joinedScripts(c.args)
			}
		}
		if !hit || fn.covers(c.pos, c.args, shell, runners) {
			continue
		}
		fn.addHit(hits, c.pos)
	}
}

func (fn *idFunc) addHit(hits *[]gitIDHit, pos token.Pos) {
	line := fn.file.fset.Position(pos).Line
	*hits = append(*hits, gitIDHit{
		key:   fn.key,
		where: fmt.Sprintf("%s:%d: %s: %s", fn.file.rel, line, fn.key, gitIdentityRemedy),
	})
}

func (fn *idFunc) covers(pos token.Pos, args []ast.Expr, extra string, callees []*idFunc) bool {
	lits := exprLits(args)
	if extra != "" {
		lits = append(lits, extra)
	}
	if gitIDDashPair(lits) || gitIDFour(lits) {
		return true
	}
	if fn.commandID || (fn.repoID && !fn.clonedBefore(pos)) {
		return true
	}
	if len(callees) == 0 {
		return false
	}
	for _, c := range callees {
		if !c.safe() {
			return false
		}
	}
	return true
}

// clonedBefore reports whether a clone runs earlier in this function than pos.
// A commit before that clone still sees the local config this function set.
func (fn *idFunc) clonedBefore(pos token.Pos) bool {
	for _, p := range fn.clonePos {
		if p < pos {
			return true
		}
	}
	return false
}

func (fn *idFunc) resolve(c idCall) []*idFunc {
	if !c.sel {
		for p := fn; p != nil; p = p.parent {
			if lit, ok := p.locals[c.name]; ok {
				return []*idFunc{lit}
			}
		}
		return fn.file.idx.fn[fn.file.pkg][c.name]
	}
	return fn.file.idx.method[fn.file.pkg][c.name]
}

func (fn *idFunc) isRunner() bool {
	switch fn.runnerState {
	case 1:
		return true
	case 2, 3:
		return false
	}
	if fn.directGit || fn.directShell || fn.directProgram {
		fn.runnerState = 1
		return true
	}
	fn.runnerState = 3
	for _, c := range fn.calls {
		for _, cal := range fn.resolve(c) {
			if cal.isRunner() {
				fn.runnerState = 1
				return true
			}
		}
	}
	fn.runnerState = 2
	return false
}

func (fn *idFunc) safe() bool {
	switch fn.safeState {
	case 1:
		return true
	case 2, 3:
		return false
	}
	if fn.commandID {
		fn.safeState = 1
		return true
	}
	if fn.directGit || fn.directShell || fn.directProgram {
		fn.safeState = 2
		return false
	}
	fn.safeState = 3
	saw := false
	for _, c := range fn.calls {
		for _, cal := range fn.resolve(c) {
			if !cal.isRunner() {
				continue
			}
			saw = true
			if !cal.safe() {
				fn.safeState = 2
				return false
			}
		}
	}
	if !saw {
		fn.safeState = 2
		return false
	}
	fn.safeState = 1
	return true
}

func gitIDImports(f *ast.File) (execName, testgitName, gitrunName string, all map[string]bool) {
	all = map[string]bool{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(p)
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			local = imp.Name.Name
		}
		if local == "_" || local == "." {
			continue
		}
		all[local] = true
		switch {
		case p == "os/exec":
			execName = local
		case p == "internal/testgit" || strings.HasSuffix(p, "/internal/testgit"):
			testgitName = local
		case strings.HasSuffix(p, "/internal/gitrun"):
			gitrunName = local
		}
	}
	return execName, testgitName, gitrunName, all
}

func gitIDFileFour(f *ast.File) bool {
	var lits []string
	ast.Inspect(f, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		if s, ok := unquoteLit(bl); ok {
			lits = append(lits, s)
		}
		return true
	})
	return gitIDFour(lits)
}

func gitIDRecv(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return gitIDTypeName(fn.Recv.List[0].Type)
}

func gitIDTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return gitIDTypeName(t.X)
	case *ast.IndexExpr:
		return gitIDTypeName(t.X)
	default:
		return ""
	}
}

func unquoteLit(bl *ast.BasicLit) (string, bool) {
	if bl == nil || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

func unquoteExpr(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	return unquoteLit(bl)
}

func gitIDSub(args []ast.Expr) string {
	valueNext := false
	for _, a := range args {
		s, ok := unquoteExpr(a)
		if !ok {
			valueNext = false
			continue
		}
		switch {
		case valueNext:
			valueNext = false
		case gitIDValueOpt(s):
			valueNext = true
		case !strings.HasPrefix(s, "-"):
			return s
		}
	}
	return ""
}

func argvSub(args []ast.Expr) (sub string, explicit bool) {
	for i, a := range args {
		s, ok := unquoteExpr(a)
		if !ok || s != "git" {
			continue
		}
		return gitIDSub(args[i+1:]), true
	}
	return gitIDSub(args), false
}

func gitIDFour(lits []string) bool {
	var a, b, c, d bool
	for _, s := range lits {
		if strings.Contains(s, "GIT_AUTHOR_NAME") {
			a = true
		}
		if strings.Contains(s, "GIT_AUTHOR_EMAIL") {
			b = true
		}
		if strings.Contains(s, "GIT_COMMITTER_NAME") {
			c = true
		}
		if strings.Contains(s, "GIT_COMMITTER_EMAIL") {
			d = true
		}
	}
	return a && b && c && d
}

func gitIDDashPair(lits []string) bool {
	var name, email bool
	for _, s := range lits {
		if strings.Contains(s, "user.name=") {
			name = true
		}
		if strings.Contains(s, "user.email=") {
			email = true
		}
	}
	return name && email
}

func gitIDConfig(lits []string) bool {
	var name, email, config bool
	for _, s := range lits {
		switch s {
		case "user.name":
			name = true
		case "user.email":
			email = true
		case "config":
			config = true
		}
	}
	return name && email && config
}

func gitIDBundled(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return true
	default:
		return false
	}
}

func gitIDTestgitSel(name string) bool {
	switch name {
	case "Apply", "Env":
		return true
	default:
		return false
	}
}

func gitIDGitrunSel(name string) bool {
	switch name {
	case "Run", "Output", "Combined":
		return true
	default:
		return false
	}
}

func compositeArgv(cl *ast.CompositeLit) []ast.Expr {
	if cl.Type != nil && !stringSliceType(cl.Type) {
		return nil
	}
	if len(cl.Elts) == 0 {
		return nil
	}
	any := false
	for _, e := range cl.Elts {
		switch e.(type) {
		case *ast.BasicLit, *ast.Ident:
		default:
			return nil
		}
		if _, ok := unquoteExpr(e); ok {
			any = true
		}
	}
	if !any {
		return nil
	}
	return cl.Elts
}

func stringSliceType(e ast.Expr) bool {
	at, ok := e.(*ast.ArrayType)
	if !ok {
		return false
	}
	id, ok := at.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

func scriptArg(args []ast.Expr) string {
	for i, a := range args {
		s, ok := unquoteExpr(a)
		if !ok || s != "-c" || i+1 >= len(args) {
			continue
		}
		if t, ok := unquoteExpr(args[i+1]); ok {
			return t
		}
	}
	return ""
}

func scriptHits(args []ast.Expr) bool {
	for _, a := range args {
		s, ok := unquoteExpr(a)
		if ok && gitCommitScript.MatchString(s) {
			return true
		}
	}
	return false
}

func joinedScripts(args []ast.Expr) string {
	var b strings.Builder
	for _, a := range args {
		s, ok := unquoteExpr(a)
		if ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func exprLits(args []ast.Expr) []string {
	var out []string
	for _, a := range args {
		if s, ok := unquoteExpr(a); ok {
			out = append(out, s)
		}
	}
	return out
}
