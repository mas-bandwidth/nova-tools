package typedrec_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// specAllowlist is the spec's list (#2506 rev 3/4, "Allowlist"): 23 entries,
// the two part=B entries included. B deletes those two and leaves 21.
var specAllowlist = []allowlistEntry{
	// Card header (SPEC-CARD)
	{file: "internal/pulse/cut_template.go", fn: "ValidateCardV2", record: "SPEC-CARD"},
	{file: "internal/pulse/cut_template.go", fn: "ApplyDependsOn", record: "SPEC-CARD"},
	{file: "internal/pulse/harvest.go", fn: "isV2CardContent", record: "SPEC-CARD"},
	{file: "internal/pulse/manager.go", fn: "attemptOf", record: "SPEC-CARD"},
	{file: "internal/pulse/manager.go", fn: "reissue", record: "SPEC-CARD"},
	{file: "internal/pulse/harvestguard.go", fn: "cardRepo", record: "SPEC-CARD"},
	{file: "internal/swarm/lintheader.go", fn: "cardKeyCheck", record: "SPEC-CARD"},
	{file: "internal/swarm/lintheader.go", fn: "cardTypedKeys", record: "SPEC-CARD"},
	{file: "internal/nsprint/card/lint.go", fn: "requiredKeys", record: "SPEC-CARD"},
	{file: "internal/prereview/prereview.go", fn: "ParseCard", record: "SPEC-CARD"},

	// Issue-body card keys
	{file: "internal/nsprint/file/file.go", fn: "requiredKeys", record: "Issue-body"},
	{file: "internal/nsprint/file/file.go", fn: "TaskTitle", record: "Issue-body"},

	// Other records
	{file: "internal/pulse/fleetstandard.go", fn: "fleetStandardValues", record: "fleet-standard"},
	{file: "internal/pulse/manager.go", fn: "handleNotes", record: "bus-note"},
	{file: "internal/nsprint/consume/okfriend.go", fn: "onHarvested", record: "Lua-reply"},
	{file: "internal/nsprint/harvest/harvest.go", fn: "recordPR", record: "Lua-reply"},
	{file: "internal/wake/bus.go", fn: "waitBookkeeping", record: "wake-bus"},
	{file: "internal/nsprint/deal/ready.go", fn: "entryWhy", record: "Redis-outcome"},
	{file: "internal/secrets/seal.go", fn: "RunSeal", record: "git-HEAD"},
	{file: "internal/swarm/wall.go", fn: "WallCommits", record: "git-HEAD"},
	{file: "internal/swarm/wall.go", fn: "repoCommits", record: "git-HEAD"},

	// part=B
	{file: "internal/merge/verdict.go", fn: "ParseDispositionLine", record: "disposition", partB: true},
	{file: "internal/merge/verdict.go", fn: "dispositionWholeLine", record: "disposition", partB: true},
}

// driftAllowlist holds hits that dev gained after the spec's measurement at
// 24f0e1d7 (none of these functions exists there). Each reads a record other
// than RESULT, sits outside part A's PATHS, and carries the commit that added
// it and the reason it stays. A new hit on dev lands here only with both.
var driftAllowlist = []allowlistEntry{
	{file: "cmd/nova-merge/batch.go", fn: "bodyPaths", record: "SPEC-CARD", since: "92251bcc",
		reason: "the card's PATHS header line in a swarm member's PR body, not a RESULT field"},
	{file: "cmd/nova-merge/integrate.go", fn: "integrateSteps", record: "integrate-steps", since: "21f69fa8",
		reason: "nova-merge's own step names (HEADS ... PR CI ... REVERIFY); PR is a step word, not the RESULT key"},
	{file: "cmd/nova-swarm/nativeevent.go", fn: "failWord", record: "verdict", since: "7644669f",
		reason: "first word of a nova-swarm native verdict line (BLOCKED, RED), an event record, not RESULT line 2"},
	{file: "internal/swarm/sparse.go", fn: "cardPATHS", record: "SPEC-CARD", since: "dd08d6e3",
		reason: "the card's PATHS header for the sparse checkout, read before any RESULT exists"},
	{file: "internal/nsprint/brief/lint.go", fn: "briefPaths", record: "brief", since: "1db79631",
		reason: "the child brief's PATHS: line, compared with the task title's PATHS (#3154)"},
	{file: "internal/nsprint/ready/ready.go", fn: "entryBlocker", record: "Redis-outcome", since: "26a5ddb0",
		reason: "the Redis card hash outcome field, the same typed value as nsprint/deal/ready.go entryWhy"},
	{file: "internal/post/issue/issue.go", fn: "RequiredFields", record: "card-schema-section", since: "1e58a3b0",
		reason: "the key names a v2 card-schema section must carry when filed as an issue, not a RESULT parse"},
	{file: "internal/sprintline/line.go", fn: "ParseSuggest", record: "calibration", since: "0c89f897",
		reason: "the KIND report lines of nova-pulse sprint calibration stdout"},
	{file: "internal/sprintline/line.go", fn: "suggestOf", record: "calibration", since: "0c89f897",
		reason: "the SUGGEST lines of nova-pulse sprint calibration stdout"},
	{file: "internal/nsprint/task/take.go", fn: "Take", record: "task-take-status", since: "7aebc02f",
		reason: "task take status BLOCKED, the ns task-take function's reply word, not a RESULT field"},
	{file: "internal/nsprint/disposition/line.go", fn: "Parse", record: "DISPOSITION", since: "73980a14",
		reason: "first line of a typed DISPOSITION/REPAIR comment (#3092), a friend read record, not RESULT line 2"},
	{file: "internal/nsprint/task/take.go", fn: "DoneTyped", record: "task-done-status", since: "50fbecdb",
		reason: "task done status DONE, the ns task-done function's reply word, not a RESULT field"},
	{file: "internal/nsprint/land/eval_records.go", fn: "ParseInboundObjection", record: "objection", since: "362dde93",
		reason: "first word of an inbound comment/review or PR body (HOLD, BLOCKED), the lander's B2 objection record (#3139), not RESULT line 2"},
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
		}
		return "", false
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
								} else {
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
						} else {
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
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root with go.mod")
		}
		dir = parent
	}
}

func TestOneTypedParser(t *testing.T) {
	root := findRepoRoot(t)
	pc := newParserChecker(typedrec.Contract)

	t.Run("fixtures", func(t *testing.T) {
		fixDir := filepath.Join(root, "internal/typedrec/testdata/oneparser")

		// 1. omitted-field
		hits1, err := pc.scanFile(filepath.Join(fixDir, "omitted-field.go"), "omitted-field.go")
		if err != nil || len(hits1) != 1 || hits1[0].tok != "FINDINGS" {
			t.Errorf("fixture 1 omitted-field: got %v, err %v", hits1, err)
		}

		// 2. split-shadow
		hits2, err := pc.scanFile(filepath.Join(fixDir, "split-shadow.go"), "split-shadow.go")
		if err != nil || len(hits2) != 1 || hits2[0].tok != "PROBES" {
			t.Errorf("fixture 2 split-shadow: got %v, err %v", hits2, err)
		}

		// 3. scanner-shadow
		hits3, err := pc.scanFile(filepath.Join(fixDir, "scanner-shadow.go"), "scanner-shadow.go")
		if err != nil || len(hits3) != 1 || hits3[0].tok != "SUGGEST" {
			t.Errorf("fixture 3 scanner-shadow: got %v, err %v", hits3, err)
		}

		// 4. byte-shadow
		hits4, err := pc.scanFile(filepath.Join(fixDir, "byte-shadow.go"), "byte-shadow.go")
		if err != nil || len(hits4) != 1 || hits4[0].tok != "HEAD" {
			t.Errorf("fixture 4 byte-shadow: got %v, err %v", hits4, err)
		}

		// 5. const-concat
		hits5, err := pc.scanFile(filepath.Join(fixDir, "const-concat.go"), "const-concat.go")
		if err != nil || len(hits5) != 1 || hits5[0].tok != "FLOOR" {
			t.Errorf("fixture 5 const-concat: got %v, err %v", hits5, err)
		}

		// 6. prefix-table
		hits6, err := pc.scanFile(filepath.Join(fixDir, "prefix-table.go"), "prefix-table.go")
		if err != nil || len(hits6) != 2 || hits6[0].tok != "RED" {
			t.Errorf("fixture 6 prefix-table: got %v, err %v", hits6, err)
		}

		// 8. var-shadow: a never-assigned package var holding "HEAD:" folds like a const.
		hits8, err := pc.scanFile(filepath.Join(fixDir, "var-shadow.go"), "var-shadow.go")
		if err != nil || len(hits8) != 1 || hits8[0].tok != "HEAD" {
			t.Errorf("fixture 8 var-shadow: got %v, err %v", hits8, err)
		}

		// 7. clean
		hits7, err := pc.scanFile(filepath.Join(fixDir, "clean.go"), "clean.go")
		if err != nil || len(hits7) != 0 {
			t.Errorf("fixture 7 clean: expected 0 hits, got %d: %v", len(hits7), hits7)
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
			if n := scanRev2(filepath.Join(fixDir, f)); n != 0 {
				t.Errorf("rev2 rule unexpectedly flagged fixture %s (got %d hits, want 0)", f, n)
			}
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
		if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		// Normal contract: 0 hits
		hitsNormal, _ := pc.scanFile(tmpFile, "synthetic.go")
		if len(hitsNormal) != 0 {
			t.Errorf("expected 0 hits with standard contract, got %d", len(hitsNormal))
		}

		// Synthetic contract: 1 hit on ZZTEST
		hitsSynth, _ := pcSynthetic.scanFile(tmpFile, "synthetic.go")
		if len(hitsSynth) != 1 || hitsSynth[0].tok != "ZZTEST" {
			t.Errorf("expected 1 hit for ZZTEST with synthetic contract, got %v", hitsSynth)
		}
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
		if err != nil {
			t.Fatal(err)
		}

		if len(unexpectedHits) > 0 {
			t.Errorf("found %d unexpected RESULT parser hit(s):", len(unexpectedHits))
			for _, h := range unexpectedHits {
				t.Errorf("  %s:%d %s() %s %s %s", h.file, h.line, h.fn, h.form, h.tok, h.shape)
			}
		}

		// Ensure every allowlist entry has at least 1 hit
		for _, a := range allowlist {
			count := allowMap[a.file][a.fn]
			if count == 0 {
				t.Errorf("stale allowlist entry: %s %s (0 hits)", a.file, a.fn)
			}
		}

		// The spec's list is 23 entries with exactly two part=B (B leaves 21).
		partB := 0
		for _, a := range specAllowlist {
			if a.partB {
				partB++
			}
		}
		if len(specAllowlist) != 23 || partB != 2 {
			t.Errorf("spec allowlist: %d entries, %d part=B; want 23 and 2", len(specAllowlist), partB)
		}
		// Every drift entry names the commit that added it and why it stays.
		for _, a := range driftAllowlist {
			if a.since == "" || a.reason == "" || a.partB {
				t.Errorf("drift allowlist entry %s %s needs since and reason and no part=B", a.file, a.fn)
			}
		}
	})
}
