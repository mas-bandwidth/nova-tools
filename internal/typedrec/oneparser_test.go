// TestOneTypedParser is the one-typed-parser rule (#2506): internal/typedrec
// is the home of every typed line a nova tool reads, the RESULT v2 record
// and DISPOSITION line first, and the Lua replies (the PATHS gate's
// refusal) and any other typed line with them. Outside typedrec no function
// compares a line's token to a bare word, builds a table of the tokens, or
// cuts a token's prefix; the tree walk below finds those shapes and skips
// internal/typedrec because it is that home, not by accident. The allowlist
// names the records that are not RESULT lines and only shrinks; a drift
// entry names the commit that added it and why it stays.
package typedrec_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

var shapePkgs = map[string]bool{
	"strings":      true,
	"bytes":        true,
	"regexp":       true,
	"bufio":        true,
	"text/scanner": true,
}

type hit struct {
	file  string
	line  int
	fn    string
	form  string
	tok   string
	shape string
}

type allowlistEntry struct {
	file   string
	fn     string
	record string
	partB  bool
	// since and reason are set on drift entries only: the dev commit that
	// added the hit after the spec's measurement, and why it is not a RESULT
	// parser that part A moves into typedrec.
	since  string
	reason string
}

// specAllowlist is the spec's list (#2506 rev 3/4, "Allowlist"): 23 entries
// less the two part=B entries, which part B deleted, leaving 21.
var specAllowlist = []allowlistEntry{
	// Card header (SPEC-CARD)
	{file: "internal/swarm/lintheader.go", fn: "cardKeyCheck", record: "SPEC-CARD"},
	{file: "internal/swarm/lintheader.go", fn: "cardTypedKeys", record: "SPEC-CARD"},

	// Issue-body card keys

	// Other records
	{file: "internal/secrets/seal.go", fn: "preflight", record: "git-HEAD"},

	// part=B (merge/verdict.go ParseDispositionLine, dispositionWholeLine)
	// moved into typedrec.ParseDisposition; their two entries are gone.
}

// driftAllowlist holds hits that dev gained after the spec's measurement at
// 24f0e1d7 (none of these functions exists there). Each reads a record other
// than RESULT, sits outside part A's PATHS, and carries the commit that added
// it and the reason it stays. A new hit on dev lands here only with both.
var driftAllowlist = []allowlistEntry{
	{file: "internal/swarm/stage.go", fn: "ReadCardBase", record: "SPEC-CARD", since: "5778de35",
		reason: "the card's REPO: header for staging (#3711), read before any RESULT exists"},
	{file: "tools/ci/revertonred.go", fn: "land", record: "git-refspec", since: "a1f1f62c1",
		reason: "the git push refspec HEAD:main, not a RESULT parser"},
	{file: "cmd/nova-sprint/landprune.go", fn: "tidyRefs", record: "git-HEAD", since: "c6e85c5e",
		reason: "skips origin/HEAD, the clone's symbolic ref, in a for-each-ref listing; not a RESULT parser"},
}

var allowlist = append(append([]allowlistEntry{}, specAllowlist...), driftAllowlist...)

type parserChecker struct {
	keys     []string
	words    []string
	sections []string
}

func newParserChecker(c typedrec.ContractDef) *parserChecker {
	return &parserChecker{
		keys:     c.FieldKeys(),
		words:    c.StatusWords(),
		sections: c.SectionNames(),
	}
}

func (pc *parserChecker) match(s string) (tok, form string) {
	for _, k := range pc.keys {
		switch {
		case s == k:
			return k, "bare"
		case strings.HasPrefix(s, k+":"):
			return k, "colon"
		case strings.HasPrefix(s, k+" ") || strings.HasPrefix(s, k+"\t"):
			return k, "space"
		}
	}
	for _, w := range pc.words {
		if s == w {
			return w, "bare"
		}
		if strings.HasPrefix(s, w+" ") || strings.HasPrefix(s, w+":") {
			return w, "space"
		}
	}
	for _, sec := range pc.sections {
		if strings.HasPrefix(s, "## "+sec) {
			return "## " + sec, "heading"
		}
	}
	return "", ""
}

func (pc *parserChecker) scanFile(p, rel string) ([]hit, error) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, p, nil, 0)
	if err != nil {
		return nil, err
	}

	consts := map[string]string{}
	var fold func(e ast.Expr) (string, bool)
	fold = func(e ast.Expr) (string, bool) {
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				s, err := strconv.Unquote(x.Value)
				return s, err == nil
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				a, ok1 := fold(x.X)
				b, ok2 := fold(x.Y)
				if ok1 && ok2 {
					return a + b, true
				}
				if ok1 {
					return a, true
				}
			}
		case *ast.Ident:
			v, ok := consts[x.Name]
			return v, ok
		case *ast.ParenExpr:
			return fold(x.X)
		case *ast.CallExpr:
			// fmt.Sprintf("REPO: %s", x) and fmt.Sprint("REPO: ", x) fold to
			// their constant lead, as "REPO: " + x does: building the label
			// by format must not hide it in parser position.
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) > 0 {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fmt" && (sel.Sel.Name == "Sprintf" || sel.Sel.Name == "Sprint") {
					if s, ok := fold(x.Args[0]); ok {
						if sel.Sel.Name == "Sprintf" {
							s, _, _ = strings.Cut(s, "%")
						}
						return s, true
					}
				}
			}
		}
		return "", false
	}

	// whole is true when e folds with nothing left over: a literal, a folded
	// const or var, or a + of such. A composite-literal element that is whole
	// is a token table's entry; one that splices a runtime value into the
	// label ("REPO: " + repo, fmt.Sprintf("REPO: %s", repo)) is a produced
	// line, output and not a parser. The same expression in parser position
	// (a strings call's argument, ==, a case) is still reported by fold's
	// lead, so the rule opens no hole there.
	var whole func(e ast.Expr) bool
	whole = func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.BasicLit:
			return x.Kind == token.STRING
		case *ast.Ident:
			_, ok := consts[x.Name]
			return ok
		case *ast.ParenExpr:
			return whole(x.X)
		case *ast.BinaryExpr:
			return x.Op == token.ADD && whole(x.X) && whole(x.Y)
		}
		return false
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if g, ok := n.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, sp := range g.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, nm := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := fold(vs.Values[i]); ok {
							consts[nm.Name] = s
						}
					}
				}
			}
		}
		return true
	})

	// A package-level var bound to a constant string and never assigned in the
	// file (no =, no &x, no ++) is a constant in all but name, so it folds too:
	// moving a literal into such a var must not hide a parser (cold HOLD on
	// #3497, item 2; fixture var-shadow).
	assigned := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch y := n.(type) {
		case *ast.AssignStmt:
			for _, l := range y.Lhs {
				if id, ok := l.(*ast.Ident); ok {
					assigned[id.Name] = true
				}
			}
		case *ast.UnaryExpr:
			if y.Op == token.AND {
				if id, ok := y.X.(*ast.Ident); ok {
					assigned[id.Name] = true
				}
			}
		case *ast.IncDecStmt:
			if id, ok := y.X.(*ast.Ident); ok {
				assigned[id.Name] = true
			}
		}
		return true
	})
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, sp := range g.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, nm := range vs.Names {
				if i >= len(vs.Values) || assigned[nm.Name] {
					continue
				}
				if _, isConst := consts[nm.Name]; isConst {
					continue
				}
				if v, ok := fold(vs.Values[i]); ok {
					consts[nm.Name] = v
				}
			}
		}
	}

	var hits []hit
	report := func(e ast.Expr, fnName, shape string) {
		s, ok := fold(e)
		if !ok {
			return
		}
		tok, form := pc.match(s)
		if tok == "" {
			return
		}
		hits = append(hits, hit{
			file:  rel,
			line:  fs.Position(e.Pos()).Line,
			fn:    fnName,
			form:  form,
			tok:   tok,
			shape: shape,
		})
	}

	isShapeCall := func(e ast.Expr) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && (shapePkgs[id.Name] || (id.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sscan"))) {
						found = true
					}
					if sel.Sel.Name == "Text" || sel.Sel.Name == "Bytes" || strings.HasPrefix(sel.Sel.Name, "Find") {
						found = true
					}
				}
			}
			return !found
		})
		return found
	}

	// Package level composite literals
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.VAR {
			for _, sp := range g.Specs {
				if vs, ok := sp.(*ast.ValueSpec); ok {
					varName := ""
					if len(vs.Names) > 0 {
						varName = vs.Names[0].Name
					}
					ast.Inspect(vs, func(n ast.Node) bool {
						if x, ok := n.(*ast.CompositeLit); ok {
							for _, e := range x.Elts {
								if kv, ok := e.(*ast.KeyValueExpr); ok {
									report(kv.Key, varName, "pkg-complit-key")
								} else if whole(e) {
									report(e, varName, "pkg-complit")
								}
							}
						}
						return true
					})
				}
			}
		}
	}

	tokFuncs := map[string]bool{}
	for round := 0; round < 2; round++ {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fnName := fd.Name.Name
			taint := map[string]bool{}
			tainted := func(e ast.Expr) bool {
				t := false
				ast.Inspect(e, func(n ast.Node) bool {
					switch y := n.(type) {
					case *ast.Ident:
						if taint[y.Name] {
							t = true
						}
					case *ast.SliceExpr:
						t = true
					case *ast.CallExpr:
						if id, ok := y.Fun.(*ast.Ident); ok && tokFuncs[id.Name] {
							t = true
						}
						// A value the typed parser returned is typed text: a
						// bare compare on it outside typedrec is a parse.
						if sel, ok := y.Fun.(*ast.SelectorExpr); ok {
							if id, ok := sel.X.(*ast.Ident); ok && id.Name == "typedrec" {
								t = true
							}
						}
					case *ast.IndexExpr:
						if _, isStr := fold(y.Index); !isStr {
							t = true
						}
					}
					return !t
				})
				return t || isShapeCall(e)
			}

			for pass := 0; pass < 3; pass++ {
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch y := n.(type) {
					case *ast.AssignStmt:
						for _, r := range y.Rhs {
							if tainted(r) {
								for _, l := range y.Lhs {
									if id, ok := l.(*ast.Ident); ok {
										taint[id.Name] = true
									}
								}
							}
						}
					case *ast.RangeStmt:
						if tainted(y.X) {
							if id, ok := y.Key.(*ast.Ident); ok {
								taint[id.Name] = true
							}
							if id, ok := y.Value.(*ast.Ident); ok {
								taint[id.Name] = true
							}
						}
					}
					return true
				})
			}

			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if r, ok := n.(*ast.ReturnStmt); ok {
					for _, e := range r.Results {
						if tainted(e) {
							tokFuncs[fnName] = true
						}
					}
				}
				return true
			})

			if round == 0 {
				continue
			}

			reportCmp := func(lit, other ast.Expr, shape string) {
				s, ok := fold(lit)
				if !ok {
					return
				}
				_, form := pc.match(s)
				if form == "bare" {
					if other == nil {
						return
					}
					u := other
					for {
						if p, ok := u.(*ast.ParenExpr); ok {
							u = p.X
						} else {
							break
						}
					}
					// A lookup by constant key, such as c["outcome"] == "DONE", reads a typed value and is not parsing.
					if idx, ok := u.(*ast.IndexExpr); ok {
						if _, isStr := fold(idx.Index); isStr {
							return
						}
					}
					if !tainted(other) {
						return
					}
				}
				report(lit, fnName, shape)
			}

			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && (shapePkgs[id.Name] || (id.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sscan"))) {
							for _, a := range x.Args {
								report(a, fnName, id.Name+"."+sel.Sel.Name)
							}
						}
					}
				case *ast.BinaryExpr:
					if x.Op == token.EQL || x.Op == token.NEQ {
						reportCmp(x.X, x.Y, "==")
						reportCmp(x.Y, x.X, "==")
					}
				case *ast.SwitchStmt:
					for _, st := range x.Body.List {
						for _, e := range st.(*ast.CaseClause).List {
							reportCmp(e, x.Tag, "case")
						}
					}
				case *ast.CompositeLit:
					for _, e := range x.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							report(kv.Key, fnName, "complit-key")
						} else if whole(e) {
							report(e, fnName, "complit")
						}
					}
				}
				return true
			})
		}
	}

	return hits, nil
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "could not find repo root with go.mod")
		dir = parent
	}
}

func TestOneTypedParser(t *testing.T) {
	t.Parallel()

	root := findRepoRoot(t)
	pc := newParserChecker(typedrec.Contract)

	t.Run("fixtures", func(t *testing.T) {
		fixDir := filepath.Join(root, "internal/typedrec/testdata/oneparser")

		// 1. omitted-field
		hits1, err := pc.scanFile(filepath.Join(fixDir, "omitted-field.go"), "omitted-field.go")
		if assert.NoError(t, err, "fixture 1 omitted-field: unexpected error") && assert.Len(t, hits1, 1, "fixture 1 omitted-field: tokens length") {
			assert.Equal(t, "FINDINGS", hits1[0].tok)
		}

		// 2. split-shadow
		hits2, err := pc.scanFile(filepath.Join(fixDir, "split-shadow.go"), "split-shadow.go")
		if assert.NoError(t, err, "fixture 2 split-shadow: unexpected error") && assert.Len(t, hits2, 1, "fixture 2 split-shadow: tokens length") {
			assert.Equal(t, "PROBES", hits2[0].tok)
		}

		// 3. scanner-shadow
		hits3, err := pc.scanFile(filepath.Join(fixDir, "scanner-shadow.go"), "scanner-shadow.go")
		if assert.NoError(t, err, "fixture 3 scanner-shadow: unexpected error") && assert.Len(t, hits3, 1, "fixture 3 scanner-shadow: tokens length") {
			assert.Equal(t, "SUGGEST", hits3[0].tok)
		}

		// 4. byte-shadow
		hits4, err := pc.scanFile(filepath.Join(fixDir, "byte-shadow.go"), "byte-shadow.go")
		if assert.NoError(t, err, "fixture 4 byte-shadow: unexpected error") && assert.Len(t, hits4, 1, "fixture 4 byte-shadow: tokens length") {
			assert.Equal(t, "HEAD", hits4[0].tok)
		}

		// 5. const-concat
		hits5, err := pc.scanFile(filepath.Join(fixDir, "const-concat.go"), "const-concat.go")
		if assert.NoError(t, err, "fixture 5 const-concat: unexpected error") && assert.Len(t, hits5, 1, "fixture 5 const-concat: tokens length") {
			assert.Equal(t, "FLOOR", hits5[0].tok)
		}

		// 6. prefix-table
		hits6, err := pc.scanFile(filepath.Join(fixDir, "prefix-table.go"), "prefix-table.go")
		if assert.NoError(t, err, "fixture 6 prefix-table: unexpected error") && assert.Len(t, hits6, 2, "fixture 6 prefix-table: tokens length") {
			assert.Equal(t, "RED", hits6[0].tok)
		}

		// 8. var-shadow: a never-assigned package var holding "HEAD:" folds like a const.
		hits8, err := pc.scanFile(filepath.Join(fixDir, "var-shadow.go"), "var-shadow.go")
		if assert.NoError(t, err, "fixture 8 var-shadow: unexpected error") && assert.Len(t, hits8, 1, "fixture 8 var-shadow: tokens length") {
			assert.Equal(t, "HEAD", hits8[0].tok)
		}

		// 9. typed-shadow: a string the typed parser returned, compared raw.
		hits9, err := pc.scanFile(filepath.Join(fixDir, "typed-shadow.go"), "typed-shadow.go")
		if assert.NoError(t, err, "fixture 9 typed-shadow: unexpected error") && assert.Len(t, hits9, 1, "fixture 9 typed-shadow: tokens length") {
			assert.Equal(t, "DONE", hits9[0].tok)
			assert.Equal(t, "bare", hits9[0].form)
		}

		// 10. brief-lines: labels spliced with runtime values in a list of
		// lines that is joined and written out are output, not a token table.
		toks := func(name string) []string {
			hs, err := pc.scanFile(filepath.Join(fixDir, name), name)
			require.NoError(t, err, name)
			var out []string
			for _, h := range hs {
				out = append(out, h.tok)
			}
			return out
		}
		assert.Empty(t, toks("brief-lines.go"), "fixture 10 brief-lines")

		// 11. label-prefix: the same REPO: label matched against input is a parser.
		assert.Equal(t, []string{"REPO"}, toks("label-prefix.go"), "fixture 11 label-prefix")

		// 12. sprintf-label: a label built by fmt.Sprintf or fmt.Sprint and
		// compared to input is caught at both sites.
		assert.Equal(t, []string{"REPO", "REPO"}, toks("sprintf-label.go"), "fixture 12 sprintf-label")

		// 7. clean
		hits7, err := pc.scanFile(filepath.Join(fixDir, "clean.go"), "clean.go")
		if assert.NoError(t, err, "fixture 7 clean: unexpected error") {
			assert.Empty(t, hits7, "fixture 7 clean: expected 0 hits")
		}
	})

	t.Run("rev2-rule-red", func(t *testing.T) {
		// Rev 2 rule: 8 tokens only, HasPrefix / TrimPrefix / regexp only.
		// Flags none of fixtures 1-4.
		rev2Tokens := map[string]bool{
			"BRANCH": true, "REPO": true, "PATHS": true, "RED": true,
			"GREEN": true, "CHECK": true, "SCHEMA": true, "ATTEMPT": true,
		}
		scanRev2 := func(path string) int {
			fs := token.NewFileSet()
			f, err := parser.ParseFile(fs, path, nil, 0)
			if err != nil {
				return 0
			}
			count := 0
			ast.Inspect(f, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "strings" {
							if sel.Sel.Name == "HasPrefix" || sel.Sel.Name == "TrimPrefix" {
								if len(call.Args) >= 2 {
									if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
										s, _ := strconv.Unquote(lit.Value)
										for tok := range rev2Tokens {
											if strings.HasPrefix(s, tok+":") || strings.HasPrefix(s, tok+" ") {
												count++
											}
										}
									}
								}
							}
						}
					}
				}
				return true
			})
			return count
		}

		fixDir := filepath.Join(root, "internal/typedrec/testdata/oneparser")
		for _, f := range []string{"omitted-field.go", "split-shadow.go", "scanner-shadow.go", "byte-shadow.go"} {
			n := scanRev2(filepath.Join(fixDir, f))
			assert.Equal(t, 0, n, "rev2 rule unexpectedly flagged fixture %s (got %d hits, want 0)", f, n)
		}
	})

	t.Run("contract-derived", func(t *testing.T) {
		// A synthetic ZZTEST field added to a copy of Contract turns unflagged ZZTEST: into flagged
		contractCopy := typedrec.Contract
		contractCopy.Entries = append(contractCopy.Entries, typedrec.FieldEntry{
			Field: "ZZTEST", Type: "test", Fix: "R",
		})
		pcSynthetic := newParserChecker(contractCopy)

		src := "package test\nimport \"strings\"\nfunc f(l string) bool { return strings.HasPrefix(l, \"ZZTEST:\") }\n"
		tmpFile := filepath.Join(t.TempDir(), "synthetic.go")
		require.NoError(t, os.WriteFile(tmpFile, []byte(src), 0644))

		// Normal contract: 0 hits
		hitsNormal, _ := pc.scanFile(tmpFile, "synthetic.go")
		assert.Empty(t, hitsNormal, "expected 0 hits with standard contract, got %d", len(hitsNormal))

		// Synthetic contract: 1 hit on ZZTEST
		hitsSynth, _ := pcSynthetic.scanFile(tmpFile, "synthetic.go")
		require.Len(t, hitsSynth, 1, "expected 1 hit for ZZTEST with synthetic contract")
		assert.Equal(t, "ZZTEST", hitsSynth[0].tok)
	})

	t.Run("tree", func(t *testing.T) {
		allowMap := make(map[string]map[string]int)
		for _, a := range allowlist {
			if allowMap[a.file] == nil {
				allowMap[a.file] = make(map[string]int)
			}
			allowMap[a.file][a.fn] = 0
		}

		var unexpectedHits []hit
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "testdata" || info.Name() == ".git" || strings.HasSuffix(p, "internal/typedrec") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}

			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}

			hits, err := pc.scanFile(p, rel)
			if err != nil {
				return err
			}

			for _, h := range hits {
				fns := allowMap[rel]
				if fns != nil {
					if _, ok := fns[h.fn]; ok {
						fns[h.fn]++
					} else {
						unexpectedHits = append(unexpectedHits, h)
					}
				} else {
					unexpectedHits = append(unexpectedHits, h)
				}
			}
			return nil
		})
		require.NoError(t, err)

		for _, h := range unexpectedHits {
			assert.Failf(t, "unexpected RESULT parser hit", "%s:%d %s() %s %s %s", h.file, h.line, h.fn, h.form, h.tok, h.shape)
		}

		// Ensure every allowlist entry has at least 1 hit
		for _, a := range allowlist {
			count := allowMap[a.file][a.fn]
			assert.NotEqual(t, 0, count, "stale allowlist entry: %s %s (0 hits)", a.file, a.fn)
		}

		// The spec's list after part B and the deleted packages: 3 entries, none tagged part=B.
		partB := 0
		for _, a := range specAllowlist {
			if a.partB {
				partB++
			}
		}
		assert.Len(t, specAllowlist, 3, "spec allowlist entries")
		assert.Zero(t, partB, "spec allowlist part=B entries")
		// Every drift entry names the commit that added it and why it stays.
		for _, a := range driftAllowlist {
			assert.NotEmpty(t, a.since, "drift allowlist entry %s %s needs since", a.file, a.fn)
			assert.NotEmpty(t, a.reason, "drift allowlist entry %s %s needs reason", a.file, a.fn)
			assert.False(t, a.partB, "drift allowlist entry %s %s must have no part=B", a.file, a.fn)
		}
		// Every drift entry's since is a commit in this history, so a row whose
		// commit is gone (a typo, a rewritten branch) is caught. A shallow
		// checkout (CI fetches two commits) cannot see old commits: there the
		// check is logged and left to a full clone.
		shallow, err := gitrun.Output(context.Background(), gitrun.Options{C: root}, "rev-parse", "--is-shallow-repository")
		require.NoError(t, err, "git rev-parse --is-shallow-repository")
		if shallow == "true" {
			t.Logf("shallow checkout: the drift rows' since commits are checked on a full clone")
		} else {
			for _, a := range driftAllowlist {
				_, err := gitrun.Output(context.Background(), gitrun.Options{C: root}, "rev-parse", "--verify", "--quiet", "--end-of-options", a.since+"^{commit}")
				assert.NoError(t, err, "drift allowlist entry %s %s names since %s, which is no commit in this history", a.file, a.fn, a.since)
			}
		}
	})
}
