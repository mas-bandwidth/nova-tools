package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// cardpath_forge.go is THE BOUNDARY (nova-tools#3967; Glenn
// 2026-09-25 3:35 PM ET: "Nothing inside the sprint table should need to
// refer to github, except at the point of import to waiting per work stream,
// and closing out of PRs and issues at landed. Everything else is redis
// native. Top to bottom."). A card touches GitHub at exactly two points:
//
//	import  task push --issue <owner/repo#n> copies the whole issue onto the
//	        record as the card enters ws:<stream>:waiting
//	landed  the lander's merge closes the member PR and the origin issue
//
// plus harvest's PR open, an outward WRITE of our record (never a read).
// Between them every verb on the card path (deal, work, render, read brief,
// end, harvest, land's member selection, table, fsck, resolve, route,
// consume, the reconciler) reads the record, the bench mirror, ci:* (our own
// runners) and ev:github (the webhook), never the forge.
//
// The rule reads the tree (TestNoGitHubReadsOnTheCardPath runs it). A function on the card path makes a FORGE CALL
// when its body
//
//	(a) names the gh program (a string literal "gh"), the REST host (a
//	    literal containing api.github.com) or a GitHub git remote (a literal
//	    git@github.com:..., or https://github.com/ with a ".git" literal in
//	    the same function; a web link to a PR is text, not a call);
//	(b) constructs a forge client (a composite literal of one of forgeTypes);
//	(c) calls a method through a forge seam: a receiver that is a field or a
//	    variable whose name or declared type ends in Forge.
//
// Package-level var initializers are read as functions named "var <name>"
// (a seam a test swaps). Every forge call outside forgeAllow is a violation
// named by file and function; an allow row that no longer matches a forge
// call is red too, so the list only shrinks.

// cardPathDirs are the card path's packages (every .go file under each,
// tests excepted).
var cardPathDirs = []string{
	"internal/nsprint/taskcard", "internal/nsprint/card", "internal/nsprint/deal",
	"internal/nsprint/harvest", "internal/nsprint/land", "internal/nsprint/table",
	"internal/nsprint/read", "internal/nsprint/reconcile", "internal/nsprint/consume",
	"internal/nsprint/route", "internal/nsprint/ready", "internal/nsprint/life",
}

// cardPathVerbs are the nova-sprint verb files on the card path, by prefix.
var cardPathVerbs = []string{
	"card", "consume", "deal", "devred", "friend_serve", "harvest", "idem", "land", "lander",
	"read", "ready", "reconcile", "route", "table", "task_card", "ws",
}

// forgeTypes are the forge clients: constructing one is a forge call.
// deal.GH (the dealer's DEPENDS-ON read) and read.Poster (read post's
// comment mirror) were removed by #3967; bringing either back is red.
var forgeTypes = map[string]bool{
	"harvest.GitHub": true, "stream.GitHub": true, "deal.GH": true, "read.Poster": true,
	"land.RESTFiler": true, "reconcile.RESTPRHost": true, "taskcard.GitHubIssues": true,
}

// forgeAllowRow is one allowed forge call: a function at one of the two
// points (or harvest's PR open), and the forge calls it may make ("*" any).
type forgeAllowRow struct {
	file, fn, calls, point string
}

// forgeAllow is the whole allowlist: the two points (import, landed), the
// close-out of a superseded card's PR beside the landed close, harvest's PR
// open (a write), and filing new work as an issue (it enters the table only
// by import).
var forgeAllow = []forgeAllowRow{
	// import: the whole issue onto the record at push (entering waiting)
	{"internal/nsprint/taskcard/import.go", "GitHubIssues.get", "*", "import"},
	{"cmd/nova-sprint/task_card.go", "var taskIssueSource", "*", "import"},
	// landed: the stream lander's clone and push, its PR, the merge, and the
	// CLOSE of every member PR and origin issue (#3793)
	{"internal/nsprint/land/stream/github.go", "GitHub.do", "*", "landed"},
	{"internal/nsprint/land/stream/land.go", "LandStream", "*", "landed"},
	{"cmd/nova-sprint/land_stream.go", "landGitHub", "*", "landed"},
	{"cmd/nova-sprint/land_stream.go", "runLandStream", "*", "landed"},
	{"cmd/nova-sprint/land_stream.go", "runLandMerge", "*", "landed"},
	// close: a card superseded by a later attempt closes its PR and parks
	// its branch, the landed close's twin for a card that will never land
	{"internal/nsprint/harvest/orphans.go", "supersede", "ClosePR", "close"},
	{"internal/nsprint/harvest/orphans.go", "supersede", "RenameBranch", "close"},
	// harvest's PR open: the branch push and the create, an outward write
	// of our record, never a read
	{"internal/nsprint/harvest/remote.go", "GitHub.api", "*", "harvest open (write)"},
	{"internal/nsprint/harvest/remote.go", "RemoteURL", "*", "harvest open (write)"},
	{"internal/nsprint/harvest/harvest.go", "harvestCard", "OpenPR", "harvest open (write)"},
	{"internal/nsprint/reconcile/idem.go", "RESTPRHost.Open", "*", "harvest open (write)"},
	{"cmd/nova-sprint/consume.go", "var consumeHarvestSeams", "*", "harvest open (write)"},
	{"cmd/nova-sprint/harvest.go", "cardHarvestPass", "*", "harvest open (write)"},
	// new work filed as an issue (land flaky observe): a write; the work
	// enters the table only by import
	{"internal/nsprint/land/rest_filer.go", "RESTFiler.endpoint", "*", "file new work (write)"},
	{"cmd/nova-sprint/land_flaky.go", "runLandFlakyObserve", "*", "file new work (write)"},
}

// forgeCall is one forge call found: where, and what (the marker).
type forgeCall struct{ file, fn, what string }

func (c forgeCall) String() string { return c.file + ":" + c.fn + " " + c.what }

// cardPathForgeCalls reads every card-path file and returns its forge calls.
func cardPathForgeCalls(root string) ([]forgeCall, error) {
	var files []string
	for _, d := range cardPathDirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	verbs, err := filepath.Glob(filepath.Join(root, "cmd", "nova-sprint", "*.go"))
	if err != nil {
		return nil, err
	}
	for _, p := range verbs {
		base := filepath.Base(p)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		for _, pre := range cardPathVerbs {
			if strings.HasPrefix(base, pre) {
				files = append(files, p)
				break
			}
		}
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	var out []forgeCall
	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil, err
		}
		calls, err := forgeCallsIn(fset, p, filepath.ToSlash(rel))
		if err != nil {
			return nil, err
		}
		out = append(out, calls...)
	}
	return out, nil
}

// forgeCallsIn is the rule over one file: every function's forge calls, one
// per function and marker, in source order.
func forgeCallsIn(fset *token.FileSet, path, rel string) ([]forgeCall, error) {
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []forgeCall
	for _, decl := range f.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, v := range vs.Values {
					if i < len(vs.Names) {
						out = append(out, forgeCallsInNode(rel, "var "+vs.Names[i].Name, v, map[string]bool{})...)
					}
				}
			}
			continue
		}
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		name := fd.Name.Name
		seams := map[string]bool{}
		addFields := func(fl *ast.FieldList) {
			if fl == nil {
				return
			}
			for _, fld := range fl.List {
				if isForgeTypeExpr(fld.Type) {
					for _, n := range fld.Names {
						seams[n.Name] = true
					}
				}
			}
		}
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			name = recvTypeName(fd.Recv.List[0].Type) + "." + name
			addFields(fd.Recv)
		}
		addFields(fd.Type.Params)
		out = append(out, forgeCallsInNode(rel, name, fd.Body, seams)...)
	}
	return out, nil
}

// forgeCallsInNode is the rule over one function body (or var initializer):
// its forge calls, one per marker, in source order. seams are the names
// declared with a forge type (receiver, parameters; locals are added).
func forgeCallsInNode(rel, name string, body ast.Node, seams map[string]bool) []forgeCall {
	var out []forgeCall
	seen := map[string]bool{}
	add := func(what string) {
		if !seen[what] {
			seen[what] = true
			out = append(out, forgeCall{file: rel, fn: name, what: what})
		}
	}
	webRoot, gitSuffix := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			if x.Type != nil && isForgeTypeExpr(x.Type) {
				for _, id := range x.Names {
					seams[id.Name] = true
				}
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				return true
			}
			switch {
			case s == "gh":
				add(`"gh"`)
			case strings.Contains(s, "api.github.com"):
				add("api.github.com")
			case strings.HasPrefix(s, "git@github.com:"):
				add("github.com remote")
			case strings.HasPrefix(s, "https://github.com/"):
				webRoot = true
			case s == ".git":
				gitSuffix = true
			}
		case *ast.CompositeLit:
			if tn := typeExprName(x.Type); forgeTypes[tn] {
				add(tn + "{}")
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch r := sel.X.(type) {
			case *ast.SelectorExpr:
				if strings.HasSuffix(r.Sel.Name, "Forge") || strings.HasSuffix(r.Sel.Name, "forge") {
					add(sel.Sel.Name)
				}
			case *ast.Ident:
				if seams[r.Name] || strings.HasSuffix(r.Name, "Forge") || r.Name == "forge" {
					add(sel.Sel.Name)
				}
			}
		}
		return true
	})
	if webRoot && gitSuffix {
		add("github.com remote")
	}
	return out
}

// isForgeTypeExpr is a declared type naming a forge seam: an interface or
// client whose name ends in Forge.
func isForgeTypeExpr(e ast.Expr) bool {
	n := typeExprName(e)
	return strings.HasSuffix(n, "Forge") || forgeTypes[n]
}

func typeExprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return typeExprName(x.X)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name + "." + x.Sel.Name
		}
	}
	return ""
}

func recvTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeName(x.X)
	}
	return "?"
}
