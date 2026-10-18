package cardcontract_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wantPlaybookLine = "Read nova-sprint help worker-playbook first: how to take, work, finish and report, and your harness's section."

// assertPlaybookFirst holds one JOB.md to R-WORKER-PLAYBOOK: line 1 is the title, the first
// line with text after it is the worker playbook line word for word, and it is written once.
func assertPlaybookFirst(t *testing.T, raw, title, what string) {
	t.Helper()
	lines := strings.Split(raw, "\n")
	assert.True(t, strings.HasPrefix(lines[0], title), "%s: line 1 is the title %q: %q", what, title, lines[0])
	first := ""
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			first = l
			break
		}
	}
	assert.Equal(t, wantPlaybookLine, first, "%s: the first instruction names the worker playbook", what)
	assert.Equal(t, 1, strings.Count(raw, wantPlaybookLine), "%s: the playbook line is written once", what)
}

// TestJobMDNamesTheWorkerPlaybookFirst pins R-WORKER-PLAYBOOK: in every profile, for a
// work card and a read, the JOB.md Install writes has the worker playbook line as its
// first instruction, the line right after the title, word for word the same, once.
func TestJobMDNamesTheWorkerPlaybookFirst(t *testing.T) {
	t.Parallel()
	require.Equal(t, wantPlaybookLine, cardcontract.WorkerPlaybookLine)
	for _, family := range cardcontract.Families {
		for _, kind := range []string{"work", "read"} {
			t.Run(family+"/"+kind, func(t *testing.T) {
				t.Parallel()
				job := t.TempDir()
				f := cardcontract.Frame{Kind: kind, Card: "c1", Attempt: 1, Model: family, Repo: "https://example.invalid/r.git", BaseRef: "main", ReviewBase: "main", Branch: "sprint/c1.g1.e1"}
				s := cardcontract.Staged{Job: job, Repo: filepath.Join(job, "repo"), Head: strings.Repeat("a", 40), Git: "/usr/bin/git"}
				require.NoError(t, cardcontract.Install(cardcontract.For(family), f, s, filepath.Join(job, ".shims")))
				raw, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
				require.NoError(t, err)
				title := "# JOB: "
				if kind == "read" {
					title = cardcontract.ReadTitle
				}
				assertPlaybookFirst(t, string(raw), title, family+" "+kind)
			})
		}
	}
}

// TestFriendStageJobMDNamesTheWorkerPlaybookFirst pins R-WORKER-PLAYBOOK on the friend
// stager's writer (pkg/friend writeJob): a job staged from a local git remote (no network)
// has the worker playbook line first, after the title and before the checkout.
func TestFriendStageJobMDNamesTheWorkerPlaybookFirst(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	g.Push(g.Clones[0])
	base := g.Head(g.Clones[0])
	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o644))
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+global, "GIT_TERMINAL_PROMPT=0")
	dir := t.TempDir()
	p := friend.Packet{Card: "a.w1", Job: "a.w1", Repo: "o/r", Base: base, Branch: "sprint/a.w1.g1.e1", Attempt: 1}
	sha, err := (&friend.Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}).Stage(context.Background(), p)
	require.NoError(t, err)
	require.Equal(t, base, sha)
	raw, err := os.ReadFile(filepath.Join(friend.JobDir(dir, p.Job), friend.JobFile))
	require.NoError(t, err)
	assertPlaybookFirst(t, string(raw), "# JOB: work a.w1, attempt 1", "friend stage")
	assert.Less(t, strings.Index(string(raw), wantPlaybookLine), strings.Index(string(raw), "The staged checkout: "), "the playbook line comes before the checkout")
}

// TestOnlyJobMDWritesJobMD is the lint behind R-WORKER-PLAYBOOK's one definition: every call in
// the module's production code that writes, creates, opens for writing or renames onto a path
// naming JOB.md (JobName, friend.JobFile, the literal, or a variable of the same file assigned
// from one) writes exactly []byte(JobMD(...)) of pkg/cardcontract, and the playbook sentence is
// spelled in one production file only. It reads source files; it runs nothing.
func TestOnlyJobMDWritesJobMD(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))
	writers := map[string]bool{"Write": true, "WriteFile": true, "Create": true, "CreateTemp": true, "OpenFile": true, "Rename": true}
	mentions := func(n ast.Node, names map[string]bool) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				found = found || x.Name == "JobName" || x.Name == "JobFile" || names[x.Name]
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					v, err := strconv.Unquote(x.Value)
					found = found || (err == nil && filepath.Base(v) == "JOB.md")
				}
			}
			return !found
		})
		return found
	}
	// isJobMD says e is []byte(JobMD(...)) or JobMD(...): the cardcontract one, named bare
	// inside pkg/cardcontract and as cardcontract.JobMD anywhere else
	isJobMD := func(e ast.Expr, inCardcontract bool) bool {
		c, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		if conv, ok := c.Fun.(*ast.ArrayType); ok && len(c.Args) == 1 {
			if el, ok := conv.Elt.(*ast.Ident); ok && el.Name == "byte" {
				if c, ok = c.Args[0].(*ast.CallExpr); !ok {
					return false
				}
			}
		}
		switch fn := c.Fun.(type) {
		case *ast.Ident:
			return inCardcontract && fn.Name == "JobMD"
		case *ast.SelectorExpr:
			x, ok := fn.X.(*ast.Ident)
			return ok && x.Name == "cardcontract" && fn.Sel.Name == "JobMD"
		}
		return false
	}
	// pathish says e is a path's own expression (a name, a literal, a join or a concatenation),
	// so a result assigned from a write (err := os.WriteFile(...JobName...)) is no JOB.md path
	pathish := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.Ident, *ast.BasicLit, *ast.BinaryExpr:
			return true
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			return ok && (sel.Sel.Name == "Join" || sel.Sel.Name == "Sprintf")
		}
		return false
	}
	var through, bypass, spelled []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.Contains(string(src), wantPlaybookLine) {
			spelled = append(spelled, rel)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		inCardcontract := file.Name.Name == "cardcontract"
		// the file's variables that hold a JOB.md path, to a fixed point: p := filepath.Join(dir, JobName); q := p
		names := map[string]bool{}
		for grew := true; grew; {
			grew = false
			ast.Inspect(file, func(n ast.Node) bool {
				a, ok := n.(*ast.AssignStmt)
				if !ok || len(a.Lhs) != len(a.Rhs) {
					return true
				}
				for i, l := range a.Lhs {
					if id, ok := l.(*ast.Ident); ok && !names[id.Name] && pathish(a.Rhs[i]) && mentions(a.Rhs[i], names) {
						names[id.Name], grew = true, true
					}
				}
				return true
			})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || len(c.Args) == 0 {
				return true
			}
			name := ""
			switch fn := c.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			target := c.Args[0]
			if name == "Rename" && len(c.Args) == 2 {
				target = c.Args[1]
			}
			if !writers[name] || !mentions(target, names) {
				return true
			}
			if name == "OpenFile" && !mentions(c, map[string]bool{"O_WRONLY": true, "O_RDWR": true, "O_CREATE": true, "O_APPEND": true, "O_TRUNC": true}) {
				return true // opened to read
			}
			where := rel + ":" + strconv.Itoa(fset.Position(c.Pos()).Line)
			if (name == "Write" || name == "WriteFile") && len(c.Args) >= 2 && isJobMD(c.Args[1], inCardcontract) {
				through = append(through, where)
			} else {
				bypass = append(bypass, where)
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, bypass, "these write a JOB.md with text other than []byte(cardcontract.JobMD(...))")
	joined := strings.Join(through, " ")
	assert.Contains(t, joined, "pkg/cardcontract/contract.go:", "the lint sees Install's write")
	assert.Contains(t, joined, "pkg/friend/stage.go:", "the lint sees the friend stager's write")
	assert.Equal(t, []string{"pkg/cardcontract/contract.go"}, spelled, "the playbook line is spelled once, in pkg/cardcontract")
}
