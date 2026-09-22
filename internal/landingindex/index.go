package landingindex

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SpecParagraph represents a markdown specification paragraph or rule entry.
type SpecParagraph struct {
	ID            string   // Canonical identifier (e.g. "E03-F03-02", "waits", "SPEC-CI.md:547", "rule 5")
	DocPath       string   // Path relative to repository root (e.g. "docs/SPEC-CI.md")
	Heading       string   // Nearest section heading
	StartLine     int      // 1-based start line
	EndLine       int      // 1-based end line
	Text          string   // Paragraph body text
	GuardingTests []string // Names of guarding tests mentioned in or linked to this spec
	CoveredFiles  []string // Files mentioned in or covered by this spec
}

// TestCoverage represents a test function and the files/packages it exercises.
type TestCoverage struct {
	TestName     string   // e.g. "TestNoFixedWaitsOnTheCIPath"
	Package      string   // Package directory relative to repo root, e.g. "internal/ci"
	TestFile     string   // Relative path to *_test.go file
	TestCommand  string   // Command to run this test: "go test ./<pkg> -run ^<TestName>$"
	CoveredFiles []string // Non-test implementation files and fixtures covered
}

// SymbolDef represents a declared Go symbol (function, type, etc.).
type SymbolDef struct {
	Name          string   // Symbol identifier (e.g. "CutValidated")
	Package       string   // Package directory relative to repo root
	File          string   // Relative path to definition file
	Line          int      // Definition line
	Kind          string   // "func", "type", "const", "var"
	Signature     string   // Declaration header/signature
	Doc           string   // Docstring / leading comment
	GuardingTests []string // Tests guarding or referencing this symbol
}

// MaxInlinedParagraphLines is the maximum number of text lines copied from a single spec paragraph.
const MaxInlinedParagraphLines = 16

// MaxInlinedParagraphBytes is the maximum raw byte size copied from a single spec paragraph.
const MaxInlinedParagraphBytes = 1200

// MaxTotalInlinedBytes is the maximum total byte budget for the entire inlined context block in a card.
const MaxTotalInlinedBytes = 3500

// MaxTotalInlinedLines is the maximum total lines across all inlined spec paragraphs in a card.
const MaxTotalInlinedLines = 40

// Index holds the three landing indices:
// 1. spec ID -> paragraph
// 2. test -> covered files
// 3. symbol -> definition + guarding test
type Index struct {
	RepoRoot string

	// Spec ID -> SpecParagraph
	SpecsByID map[string]*SpecParagraph

	// Doc:Line -> SpecParagraph (e.g. "SPEC-CI.md:547" or "docs/SPEC-CI.md:547")
	SpecsByDocLine map[string]*SpecParagraph

	// List of all indexed spec paragraphs (sorted for deterministic iteration)
	Specs []*SpecParagraph

	// Test name -> TestCoverage
	TestsByName map[string]*TestCoverage

	// Symbol name -> SymbolDef
	SymbolsByName map[string]*SymbolDef

	// bareAliasDocs maps a bare alias (e.g. "rule 5", "waits") to map[docPath]*SpecParagraph
	bareAliasDocs map[string]map[string]*SpecParagraph

	// ambiguousKeys tracks keys that appeared in multiple documents and are rejected as bare aliases
	ambiguousKeys map[string]bool
}

var (
	specLineRe      = regexp.MustCompile(`(?i)\b(?:docs/)?([A-Za-z0-9_.-]+\.md):(\d+)\b`)
	specCodeRe      = regexp.MustCompile(`\b([A-Z][0-9A-Z]+(?:-[A-Z0-9]+)+)\b`)
	specECodeRe     = regexp.MustCompile(`\b(E\d+(?:\.[0-9]+)*(?:-[A-Za-z0-9]+)*)\b`)
	specRuleRe      = regexp.MustCompile(`(?i)\b(rule\s*\d+)\b`)
	qualifiedRuleRe = regexp.MustCompile(`(?i)\b(?:docs/)?([A-Za-z0-9_.-]+(?:\.md)?)(?:[:#\s]+)(rule\s*\d+)\b`)
	docMentionRe    = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]+\.md|SPEC-[A-Za-z0-9_-]+)\b`)
	testNameRe      = regexp.MustCompile(`\b(Test[A-Za-z0-9_]+)\b`)
	h3BacktickRe    = regexp.MustCompile(`^###\s+` + "`" + `([^` + "`" + `]+)` + "`")
	h3DashRe        = regexp.MustCompile(`^###\s+([^—–-]+)[—–-]`)
	ruleDefRe       = regexp.MustCompile(`(?i)(?:^|\n)\s*(?:(\d+)\.\s+\*\*|(?:\*\*)?rule\s*(\d+)[:.]?)`)
)

// Build constructs all three landing indices from the given repository root directory.
func Build(repoRoot string) (*Index, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}

	idx := &Index{
		RepoRoot:       absRoot,
		SpecsByID:      make(map[string]*SpecParagraph),
		SpecsByDocLine: make(map[string]*SpecParagraph),
		TestsByName:    make(map[string]*TestCoverage),
		SymbolsByName:  make(map[string]*SymbolDef),
		bareAliasDocs:  make(map[string]map[string]*SpecParagraph),
		ambiguousKeys:  make(map[string]bool),
	}

	if err := idx.indexSpecs(); err != nil {
		return nil, err
	}
	if err := idx.indexGoCode(); err != nil {
		return nil, err
	}
	idx.linkSpecGuards()

	return idx, nil
}

// indexSpecs walks markdown files in docs/ (and repo root) and extracts spec paragraphs.
func (idx *Index) indexSpecs() error {
	docsDir := filepath.Join(idx.RepoRoot, "docs")
	var docPaths []string

	// Walk docs/ directory
	_ = filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			docPaths = append(docPaths, p)
		}
		return nil
	})

	// Also check top-level spec markdown files
	rootEntries, _ := os.ReadDir(idx.RepoRoot)
	for _, e := range rootEntries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "SPEC") && strings.HasSuffix(e.Name(), ".md") {
			docPaths = append(docPaths, filepath.Join(idx.RepoRoot, e.Name()))
		}
	}

	sort.Strings(docPaths)

	for _, p := range docPaths {
		relPath, err := filepath.Rel(idx.RepoRoot, p)
		if err != nil {
			relPath = p
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		idx.parseDocParagraphs(relPath, string(raw))
	}

	// Prune ambiguous bare aliases that appear across multiple documents
	for alias, docs := range idx.bareAliasDocs {
		if len(docs) > 1 {
			delete(idx.SpecsByID, alias)
			aliasHyphen := strings.ReplaceAll(alias, " ", "-")
			delete(idx.SpecsByID, aliasHyphen)
			idx.ambiguousKeys[alias] = true
			idx.ambiguousKeys[aliasHyphen] = true
		}
	}

	return nil
}

func docKeyPrefixes(docPath string) []string {
	base := filepath.Base(docPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	prefixes := []string{
		base,
		strings.ToLower(base),
		stem,
		strings.ToLower(stem),
		docPath,
		strings.ToLower(docPath),
	}
	dir := filepath.Dir(docPath)
	dirBase := filepath.Base(dir)
	if dirBase != "" && dirBase != "." && dirBase != "docs" {
		prefixes = append(prefixes, dirBase, strings.ToLower(dirBase))
	}
	return prefixes
}

func (idx *Index) recordAlias(alias, docPath string, prefixes []string, para *SpecParagraph) {
	aliasLower := strings.ToLower(alias)

	if idx.bareAliasDocs[aliasLower] == nil {
		idx.bareAliasDocs[aliasLower] = make(map[string]*SpecParagraph)
	}
	idx.bareAliasDocs[aliasLower][docPath] = para

	if idx.SpecsByID[alias] == nil {
		idx.SpecsByID[alias] = para
	}
	if idx.SpecsByID[aliasLower] == nil {
		idx.SpecsByID[aliasLower] = para
	}

	aliasHyphen := strings.ReplaceAll(aliasLower, " ", "-")
	for _, p := range prefixes {
		// Colon qualified
		idx.SpecsByID[fmt.Sprintf("%s:%s", p, alias)] = para
		idx.SpecsByID[fmt.Sprintf("%s:%s", p, aliasLower)] = para
		idx.SpecsByID[fmt.Sprintf("%s:%s", p, aliasHyphen)] = para

		// Space qualified
		idx.SpecsByID[fmt.Sprintf("%s %s", p, alias)] = para
		idx.SpecsByID[fmt.Sprintf("%s %s", p, aliasLower)] = para
		idx.SpecsByID[fmt.Sprintf("%s %s", p, aliasHyphen)] = para

		// Hash qualified
		idx.SpecsByID[fmt.Sprintf("%s#%s", p, alias)] = para
		idx.SpecsByID[fmt.Sprintf("%s#%s", p, aliasLower)] = para
		idx.SpecsByID[fmt.Sprintf("%s#%s", p, aliasHyphen)] = para

		// Slash qualified
		idx.SpecsByID[fmt.Sprintf("%s/%s", p, alias)] = para
		idx.SpecsByID[fmt.Sprintf("%s/%s", p, aliasLower)] = para
	}
}

// parseDocParagraphs parses one markdown file into paragraphs, indexing each.
func (idx *Index) parseDocParagraphs(docPath, content string) {
	lines := strings.Split(content, "\n")
	baseName := filepath.Base(docPath)
	prefixes := docKeyPrefixes(docPath)

	currentHeading := ""
	headingIndexed := false
	var pLines []string
	pStart := 1

	// Track rules defined or referenced in this document
	docRules := make(map[string]*SpecParagraph)
	docRuleIsDef := make(map[string]bool)

	flushParagraph := func(endLine int) {
		if len(pLines) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(pLines, "\n"))
		pLines = nil
		if text == "" {
			return
		}

		para := &SpecParagraph{
			DocPath:   docPath,
			Heading:   currentHeading,
			StartLine: pStart,
			EndLine:   endLine,
			Text:      text,
		}

		// Extract tests mentioned in paragraph
		for _, m := range testNameRe.FindAllStringSubmatch(text, -1) {
			tName := m[1]
			if !contains(para.GuardingTests, tName) {
				para.GuardingTests = append(para.GuardingTests, tName)
			}
		}

		// Extract file paths mentioned in paragraph
		for _, w := range strings.Fields(text) {
			clean := strings.Trim(w, "()[]`'\",;:")
			if (strings.HasPrefix(clean, "cmd/") || strings.HasPrefix(clean, "internal/") || strings.HasPrefix(clean, "docs/")) &&
				(strings.HasSuffix(clean, ".go") || strings.HasSuffix(clean, ".md") || strings.HasSuffix(clean, ".txt")) {
				if !contains(para.CoveredFiles, clean) {
					para.CoveredFiles = append(para.CoveredFiles, clean)
				}
			}
		}

		// Index by doc lines: baseName:line and docPath:line for all lines in paragraph
		for l := pStart; l <= endLine; l++ {
			idx.SpecsByDocLine[fmt.Sprintf("%s:%d", baseName, l)] = para
			idx.SpecsByDocLine[fmt.Sprintf("%s:%d", docPath, l)] = para
		}

		// Index by heading tag if available (first non-heading content paragraph under heading only)
		isHeadingOnly := strings.HasPrefix(text, "#")
		if currentHeading != "" && !isHeadingOnly && !headingIndexed {
			headingIndexed = true
			var headingKey string
			if m := h3BacktickRe.FindStringSubmatch(currentHeading); len(m) > 1 {
				headingKey = strings.ToLower(strings.TrimSpace(m[1]))
			} else if m := h3DashRe.FindStringSubmatch(currentHeading); len(m) > 1 {
				headingKey = strings.ToLower(strings.TrimSpace(m[1]))
			}
			if headingKey != "" {
				para.ID = headingKey
				idx.recordAlias(headingKey, docPath, prefixes, para)
			}
		}

		// Index by rule codes (E03-F03-02, TC-MB-01, etc.)
		for _, m := range specCodeRe.FindAllStringSubmatch(text, -1) {
			key := m[1]
			if !strings.HasPrefix(key, "SPEC-") && !strings.HasPrefix(key, "YYYY-") {
				idx.recordAlias(key, docPath, prefixes, para)
				if para.ID == "" {
					para.ID = key
				}
			}
		}

		// Index by E-codes (E03, E05.1, etc.)
		for _, m := range specECodeRe.FindAllStringSubmatch(text, -1) {
			key := m[1]
			idx.recordAlias(key, docPath, prefixes, para)
			if para.ID == "" {
				para.ID = key
			}
		}

		// Check for rule definition or rule reference
		if defMatches := ruleDefRe.FindAllStringSubmatch(text, -1); len(defMatches) > 0 {
			for _, dm := range defMatches {
				rNum := dm[1]
				if rNum == "" {
					rNum = dm[2]
				}
				if rNum != "" {
					rKey := fmt.Sprintf("rule %s", rNum)
					docRules[rKey] = para
					docRuleIsDef[rKey] = true
					if para.ID == "" {
						para.ID = fmt.Sprintf("%s:%s", prefixes[2], rKey)
					}
				}
			}
		} else {
			for _, m := range specRuleRe.FindAllStringSubmatch(text, -1) {
				rKey := strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
				if _, exists := docRules[rKey]; !exists {
					docRules[rKey] = para
					docRuleIsDef[rKey] = false
					if para.ID == "" {
						para.ID = fmt.Sprintf("%s:%s", prefixes[2], rKey)
					}
				}
			}
		}

		if para.ID == "" {
			para.ID = fmt.Sprintf("%s:%d", baseName, pStart)
		}
		idx.SpecsByID[para.ID] = para
		idx.Specs = append(idx.Specs, para)
	}

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") {
			flushParagraph(lineNum - 1)
			currentHeading = trimmed
			headingIndexed = false
			pStart = lineNum
			pLines = append(pLines, line)
			flushParagraph(lineNum)
			pStart = lineNum + 1
			continue
		}

		if trimmed == "" {
			flushParagraph(lineNum - 1)
			pStart = lineNum + 1
			continue
		}

		if len(pLines) == 0 {
			pStart = lineNum
		}
		pLines = append(pLines, line)
	}

	flushParagraph(len(lines))

	// Register all resolved document rules
	for rKey, p := range docRules {
		idx.recordAlias(rKey, docPath, prefixes, p)
	}
}

// indexGoCode parses all Go packages under repoRoot (focusing on cmd/ and internal/).
func (idx *Index) indexGoCode() error {
	fset := token.NewFileSet()

	dirs := []string{
		filepath.Join(idx.RepoRoot, "cmd"),
		filepath.Join(idx.RepoRoot, "internal"),
		filepath.Join(idx.RepoRoot, "tools"),
	}

	var goFiles []string
	for _, root := range dirs {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
				goFiles = append(goFiles, p)
			}
			return nil
		})
	}
	sort.Strings(goFiles)

	for _, filePath := range goFiles {
		relPath, err := filepath.Rel(idx.RepoRoot, filePath)
		if err != nil {
			relPath = filePath
		}
		pkgDir := filepath.Dir(relPath)

		fileAst, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		isTest := strings.HasSuffix(filePath, "_test.go")

		if !isTest {
			// Extract symbol definitions
			idx.extractSymbols(fset, relPath, pkgDir, fileAst)
		} else {
			// Extract test functions
			idx.extractTests(fset, relPath, pkgDir, fileAst)
		}
	}

	// Link symbols to their guarding tests by scanning tests AST
	for _, filePath := range goFiles {
		if !strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		fileAst, err := parser.ParseFile(fset, filePath, nil, 0)
		if err != nil {
			continue
		}

		for _, decl := range fileAst.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			testName := fn.Name.Name

			// Look for symbol references inside the test body
			if fn.Body != nil {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.Ident:
						if sym, found := idx.SymbolsByName[node.Name]; found {
							if !contains(sym.GuardingTests, testName) {
								sym.GuardingTests = append(sym.GuardingTests, testName)
							}
						}
					case *ast.SelectorExpr:
						if sym, found := idx.SymbolsByName[node.Sel.Name]; found {
							if !contains(sym.GuardingTests, testName) {
								sym.GuardingTests = append(sym.GuardingTests, testName)
							}
						}
					}
					return true
				})
			}

			// Check naming convention Test<Symbol>...
			for symName, sym := range idx.SymbolsByName {
				if strings.HasPrefix(strings.ToLower(testName), "test"+strings.ToLower(symName)) {
					if !contains(sym.GuardingTests, testName) {
						sym.GuardingTests = append(sym.GuardingTests, testName)
					}
				}
				// Also associate tests in <name>_test.go with symbols in <name>.go
				symBase := strings.TrimSuffix(filepath.Base(sym.File), ".go")
				testBase := strings.TrimSuffix(filepath.Base(filePath), "_test.go")
				if symBase == testBase || strings.Contains(testBase, symBase) {
					if !contains(sym.GuardingTests, testName) {
						sym.GuardingTests = append(sym.GuardingTests, testName)
					}
				}
			}
		}
	}

	return nil
}

func (idx *Index) extractSymbols(fset *token.FileSet, relPath, pkgDir string, fileAst *ast.File) {
	for _, decl := range fileAst.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			symName := d.Name.Name
			line := fset.Position(d.Pos()).Line
			sig := fmt.Sprintf("func %s", symName)
			if d.Recv != nil && len(d.Recv.List) > 0 {
				sig = fmt.Sprintf("func (%s) %s", typeString(d.Recv.List[0].Type), symName)
			}
			doc := ""
			if d.Doc != nil {
				doc = strings.TrimSpace(d.Doc.Text())
			}
			idx.SymbolsByName[symName] = &SymbolDef{
				Name:      symName,
				Package:   pkgDir,
				File:      relPath,
				Line:      line,
				Kind:      "func",
				Signature: sig,
				Doc:       doc,
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					symName := ts.Name.Name
					line := fset.Position(ts.Pos()).Line
					doc := ""
					if d.Doc != nil {
						doc = strings.TrimSpace(d.Doc.Text())
					}
					idx.SymbolsByName[symName] = &SymbolDef{
						Name:      symName,
						Package:   pkgDir,
						File:      relPath,
						Line:      line,
						Kind:      "type",
						Signature: fmt.Sprintf("type %s", symName),
						Doc:       doc,
					}
				}
			}
		}
	}
}

func (idx *Index) extractTests(fset *token.FileSet, relPath, pkgDir string, fileAst *ast.File) {
	// Find sibling implementation files in same package directory
	var siblingFiles []string
	baseNoTest := strings.TrimSuffix(filepath.Base(relPath), "_test.go") + ".go"
	candidateSibling := filepath.Join(pkgDir, baseNoTest)
	if _, err := os.Stat(filepath.Join(idx.RepoRoot, candidateSibling)); err == nil {
		siblingFiles = append(siblingFiles, candidateSibling)
	}

	for _, decl := range fileAst.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		testName := fn.Name.Name

		covered := append([]string{}, siblingFiles...)

		// Inspect string literals in test AST for referenced file paths
		if fn.Body != nil {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				clean := filepath.Clean(val)
				if strings.Contains(clean, "/") && (strings.HasSuffix(clean, ".go") || strings.HasSuffix(clean, ".md")) {
					fullPath := filepath.Join(idx.RepoRoot, clean)
					if _, err := os.Stat(fullPath); err == nil {
						rel, rerr := filepath.Rel(idx.RepoRoot, fullPath)
						if rerr == nil && !contains(covered, rel) {
							covered = append(covered, rel)
						}
					}
				}
				return true
			})
		}

		cmd := fmt.Sprintf("go test ./%s -run ^%s$", pkgDir, testName)
		idx.TestsByName[testName] = &TestCoverage{
			TestName:     testName,
			Package:      pkgDir,
			TestFile:     relPath,
			TestCommand:  cmd,
			CoveredFiles: covered,
		}
	}
}

// linkSpecGuards connects spec paragraphs to test coverage when tests are mentioned.
func (idx *Index) linkSpecGuards() {
	for _, para := range idx.Specs {
		for _, tName := range para.GuardingTests {
			if tc, ok := idx.TestsByName[tName]; ok {
				for _, f := range tc.CoveredFiles {
					if !contains(para.CoveredFiles, f) {
						para.CoveredFiles = append(para.CoveredFiles, f)
					}
				}
			}
		}
	}
}

// LookupSpec looks up a spec paragraph by ID, rule name, or doc:line.
func (idx *Index) LookupSpec(query string) *SpecParagraph {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	qLower := strings.ToLower(q)
	if idx.ambiguousKeys[qLower] {
		return nil
	}
	if p, ok := idx.SpecsByID[q]; ok {
		return p
	}
	if p, ok := idx.SpecsByID[qLower]; ok {
		return p
	}
	if p, ok := idx.SpecsByDocLine[q]; ok {
		return p
	}
	// Try without "docs/" prefix if present
	if strings.HasPrefix(q, "docs/") {
		trimmed := strings.TrimPrefix(q, "docs/")
		trimmedLower := strings.ToLower(trimmed)
		if idx.ambiguousKeys[trimmedLower] {
			return nil
		}
		if p, ok := idx.SpecsByDocLine[trimmed]; ok {
			return p
		}
		if p, ok := idx.SpecsByID[trimmed]; ok {
			return p
		}
		if p, ok := idx.SpecsByID[trimmedLower]; ok {
			return p
		}
	} else {
		if p, ok := idx.SpecsByDocLine["docs/"+q]; ok {
			return p
		}
		if p, ok := idx.SpecsByID["docs/"+q]; ok {
			return p
		}
		if p, ok := idx.SpecsByID[strings.ToLower("docs/"+q)]; ok {
			return p
		}
	}
	// Normalize space vs colon in qualified queries (e.g. "SPEC-CI rule 5" -> "SPEC-CI:rule 5")
	if strings.Contains(q, " ") {
		colonVariant := strings.ReplaceAll(q, " ", ":")
		colonLower := strings.ToLower(colonVariant)
		if p, ok := idx.SpecsByID[colonVariant]; ok {
			return p
		}
		if p, ok := idx.SpecsByID[colonLower]; ok {
			return p
		}
	}
	return nil
}

// InlinedContext represents the extracted context for inlining into cards.
type InlinedContext struct {
	SpecParagraph *SpecParagraph
	GuardingTests []*TestCoverage
	CoveredFiles  []string
}

// FindMatches scans text (issue title, body, or description) and matches indices.
func (idx *Index) FindMatches(text string) []InlinedContext {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	seenSpecs := make(map[*SpecParagraph]bool)
	var results []InlinedContext

	addSpec := func(para *SpecParagraph) {
		if para == nil || seenSpecs[para] {
			return
		}
		seenSpecs[para] = true

		ctx := InlinedContext{
			SpecParagraph: para,
			CoveredFiles:  append([]string{}, para.CoveredFiles...),
		}

		for _, tName := range para.GuardingTests {
			if tc, ok := idx.TestsByName[tName]; ok {
				ctx.GuardingTests = append(ctx.GuardingTests, tc)
				for _, f := range tc.CoveredFiles {
					if !contains(ctx.CoveredFiles, f) {
						ctx.CoveredFiles = append(ctx.CoveredFiles, f)
					}
				}
			}
		}

		results = append(results, ctx)
	}

	// 1. Check doc:line matches (e.g. SPEC-CI.md:547)
	for _, m := range specLineRe.FindAllStringSubmatch(text, -1) {
		ref := fmt.Sprintf("%s:%s", m[1], m[2])
		if p := idx.LookupSpec(ref); p != nil {
			addSpec(p)
		}
	}

	// 2. Check qualified rule references (e.g. SPEC-CI.md:rule 5, SPEC-ALPHA rule 5, SPEC-PULSE:rule 5)
	for _, m := range qualifiedRuleRe.FindAllStringSubmatch(text, -1) {
		docPart := m[1]
		rulePart := m[2]
		if p := idx.LookupSpec(fmt.Sprintf("%s:%s", docPart, rulePart)); p != nil {
			addSpec(p)
		} else if p := idx.LookupSpec(fmt.Sprintf("%s %s", docPart, rulePart)); p != nil {
			addSpec(p)
		}
	}

	// 3. Check rule codes (E03-F03-02, TC-MB-01, etc.)
	for _, m := range specCodeRe.FindAllStringSubmatch(text, -1) {
		if p := idx.LookupSpec(m[1]); p != nil {
			addSpec(p)
		}
	}

	// 4. Check E-codes (E03, E05.1, etc.)
	for _, m := range specECodeRe.FindAllStringSubmatch(text, -1) {
		if p := idx.LookupSpec(m[1]); p != nil {
			addSpec(p)
		}
	}

	// 5. Check bare rule numbers (rule 5, rule 6, etc.)
	for _, m := range specRuleRe.FindAllStringSubmatch(text, -1) {
		rulePart := m[1]
		// If text mentions any specific document name, try to resolve the rule in that document's scope first
		resolved := false
		for _, dm := range docMentionRe.FindAllStringSubmatch(text, -1) {
			docName := dm[1]
			if p := idx.LookupSpec(fmt.Sprintf("%s:%s", docName, rulePart)); p != nil {
				addSpec(p)
				resolved = true
				break
			}
		}
		if !resolved {
			// Bare rule only resolves if unambiguous across all specs
			if p := idx.LookupSpec(rulePart); p != nil {
				addSpec(p)
			}
		}
	}

	// 6. Check known spec IDs
	words := strings.Fields(text)
	for _, w := range words {
		clean := strings.Trim(w, "()`'\",;:.#")
		if clean == "" || strings.HasPrefix(strings.ToLower(clean), "rule") {
			continue
		}
		if p := idx.LookupSpec(clean); p != nil {
			addSpec(p)
		}
	}

	return results
}

// FormatInlinedContext formats the matched index context into markdown suitable for inlining.
func (idx *Index) FormatInlinedContext(text string) string {
	matches := idx.FindMatches(text)
	if len(matches) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("### INLINED SPEC CONTEXT (indices S2)\n")

	totalBytes := len("### INLINED SPEC CONTEXT (indices S2)\n")
	totalLines := 1

	for i, m := range matches {
		if i > 2 {
			break // Cap at top 3 matches to stay strictly bounded
		}
		if totalBytes >= MaxTotalInlinedBytes || totalLines >= MaxTotalInlinedLines {
			b.WriteString("- ... [remaining matches omitted to stay within context budget]\n")
			break
		}

		para := m.SpecParagraph
		headerLine := fmt.Sprintf("- Spec: %s (%s:%d-%d)\n", para.ID, para.DocPath, para.StartLine, para.EndLine)
		b.WriteString(headerLine)
		totalBytes += len(headerLine)
		totalLines++

		if para.Heading != "" {
			hLine := fmt.Sprintf("  Heading: %s\n", para.Heading)
			b.WriteString(hLine)
			totalBytes += len(hLine)
			totalLines++
		}

		// Bound paragraph lines and bytes
		paraLines := strings.Split(para.Text, "\n")
		pBytes := 0
		pLines := 0
		truncated := false

		for _, line := range paraLines {
			if pLines >= MaxInlinedParagraphLines || pBytes+len(line) > MaxInlinedParagraphBytes ||
				totalBytes+len(line)+10 > MaxTotalInlinedBytes || totalLines >= MaxTotalInlinedLines {
				truncated = true
				break
			}
			fmt.Fprintf(&b, "  > %s\n", line)
			lineLen := len(line) + 5
			totalBytes += lineLen
			pBytes += len(line)
			totalLines++
			pLines++
		}
		if truncated {
			truncLine := "  > ... [truncated to bounded line/byte budget]\n"
			b.WriteString(truncLine)
			totalBytes += len(truncLine)
			totalLines++
		}

		if len(m.GuardingTests) > 0 {
			b.WriteString("  Guarding Tests:\n")
			totalBytes += 18
			totalLines++
			for _, tc := range m.GuardingTests {
				if totalBytes+len(tc.TestName)+len(tc.TestCommand)+15 > MaxTotalInlinedBytes {
					break
				}
				gtLine := fmt.Sprintf("  - `%s`: `%s`\n", tc.TestName, tc.TestCommand)
				b.WriteString(gtLine)
				totalBytes += len(gtLine)
				totalLines++
			}
		} else if len(para.GuardingTests) > 0 {
			b.WriteString("  Guarding Tests:\n")
			totalBytes += 18
			totalLines++
			for _, t := range para.GuardingTests {
				if totalBytes+len(t)+10 > MaxTotalInlinedBytes {
					break
				}
				gtLine := fmt.Sprintf("  - `%s`\n", t)
				b.WriteString(gtLine)
				totalBytes += len(gtLine)
				totalLines++
			}
		}

		if len(m.CoveredFiles) > 0 {
			b.WriteString("  Covered Files:\n")
			totalBytes += 17
			totalLines++
			for _, f := range m.CoveredFiles {
				if totalBytes+len(f)+10 > MaxTotalInlinedBytes {
					break
				}
				cfLine := fmt.Sprintf("  - %s\n", f)
				b.WriteString(cfLine)
				totalBytes += len(cfLine)
				totalLines++
			}
		}
		b.WriteString("\n")
		totalBytes++
		totalLines++
	}

	return strings.TrimRight(b.String(), "\n")
}

// FormatContextFor is a package-level helper that builds an index over repoRoot and formats context.
func FormatContextFor(repoRoot string, inputs ...string) string {
	text := strings.Join(inputs, " ")
	if strings.TrimSpace(text) == "" {
		return ""
	}

	idx, err := Build(repoRoot)
	if err != nil || idx == nil {
		return ""
	}

	return idx.FormatInlinedContext(text)
}

func contains(slice []string, val string) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	default:
		return ""
	}
}
