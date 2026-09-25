package roadmap

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SyntaxError indicates a lexical, syntactic, or structural failure in the s-expression.
type SyntaxError struct {
	File    string
	Line    int
	Column  int
	Offset  int
	Message string
}

func (e *SyntaxError) Error() string {
	loc := fmt.Sprintf("%d:%d", e.Line, e.Column)
	if e.File != "" {
		loc = fmt.Sprintf("%s:%s", e.File, loc)
	}
	return fmt.Sprintf("%s: syntax error: %s", loc, e.Message)
}

// NodeKind identifies the kind of an S-expression node.
type NodeKind int

const (
	NodeList NodeKind = iota
	NodeKeyword
	NodeString
	NodeInteger
	NodeSymbol
)

// SNode is a single parsed S-expression form with precise position tracking.
type SNode struct {
	Kind   NodeKind
	Value  string   // string content, keyword name (without ':'), or symbol name
	Int    int64    // integer value if NodeInteger
	List   []*SNode // child nodes if NodeList
	Line   int      // 1-based line
	Column int      // 1-based column
	Offset int      // byte offset
}

// IsKeyword reports whether n is a keyword matching name (without leading colon).
func (n *SNode) IsKeyword(name string) bool {
	return n != nil && n.Kind == NodeKeyword && n.Value == name
}

// IsSymbol reports whether n is a symbol matching name.
func (n *SNode) IsSymbol(name string) bool {
	return n != nil && n.Kind == NodeSymbol && n.Value == name
}

// Text returns the string value if string or symbol, or empty string.
func (n *SNode) Text() string {
	if n == nil {
		return ""
	}
	return n.Value
}

type lexer struct {
	file   string
	src    string
	pos    int
	line   int
	col    int
	length int
}

func newLexer(file, src string) *lexer {
	return &lexer{
		file:   file,
		src:    src,
		pos:    0,
		line:   1,
		col:    1,
		length: len(src),
	}
}

func (l *lexer) error(line, col, offset int, msg string) *SyntaxError {
	return &SyntaxError{
		File:    l.file,
		Line:    line,
		Column:  col,
		Offset:  offset,
		Message: msg,
	}
}

func (l *lexer) currentError(msg string) *SyntaxError {
	return l.error(l.line, l.col, l.pos, msg)
}

func (l *lexer) eof() bool {
	return l.pos >= l.length
}

func (l *lexer) peek() rune {
	if l.eof() {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return r
}

func (l *lexer) next() rune {
	if l.eof() {
		return 0
	}
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *lexer) skipWhitespaceAndComments() error {
	for !l.eof() {
		r := l.peek()
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f':
			l.next()
		case r == ';':
			// Consume comment line
			for !l.eof() {
				ch := l.peek()
				if ch == '\n' {
					l.next()
					break
				}
				l.next()
			}
		default:
			return nil
		}
	}
	return nil
}

// isDelimiter reports whether r terminates a symbol, keyword, or integer.
func isDelimiter(r rune) bool {
	if unicode.IsSpace(r) || r == 0 {
		return true
	}
	switch r {
	case '(', ')', ';', '"', '\'', '`', ',', '\\', '|', '#':
		return true
	}
	return false
}

func (l *lexer) readForm() (*SNode, error) {
	if err := l.skipWhitespaceAndComments(); err != nil {
		return nil, err
	}
	if l.eof() {
		return nil, io.EOF
	}

	startLine := l.line
	startCol := l.col
	startPos := l.pos
	r := l.peek()

	switch r {
	case '(':
		l.next()
		var items []*SNode
		for {
			if err := l.skipWhitespaceAndComments(); err != nil {
				return nil, err
			}
			if l.eof() {
				return nil, l.error(startLine, startCol, startPos,
					fmt.Sprintf("unclosed list starting at line %d, column %d; unexpected end of input", startLine, startCol))
			}
			if l.peek() == ')' {
				l.next()
				break
			}
			item, err := l.readForm()
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return &SNode{
			Kind:   NodeList,
			List:   items,
			Line:   startLine,
			Column: startCol,
			Offset: startPos,
		}, nil

	case ')':
		l.next()
		return nil, l.error(startLine, startCol, startPos, "unexpected closing parenthesis ')'")

	case '"':
		return l.readString()

	case ':':
		return l.readKeyword()

	case '#':
		return nil, l.error(startLine, startCol, startPos, "dispatch macro '#' is forbidden; roadmap is data, not executable code")

	case '\'', '`', ',', '\\', '|':
		return nil, l.error(startLine, startCol, startPos, fmt.Sprintf("reader macro or escape character '%c' is forbidden in roadmap data", r))

	case '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// Could be integer or symbol
		return l.readNumberOrSymbol()

	default:
		return l.readSymbol()
	}
}

func (l *lexer) readString() (*SNode, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	l.next() // consume opening '"'
	var sb strings.Builder

	for {
		if l.eof() {
			return nil, l.error(startLine, startCol, startPos,
				fmt.Sprintf("unterminated string starting at line %d, column %d", startLine, startCol))
		}
		r := l.next()
		if r == '"' {
			break
		}
		if r == '\\' {
			if l.eof() {
				return nil, l.error(startLine, startCol, startPos,
					fmt.Sprintf("unterminated escape sequence in string at line %d, column %d", l.line, l.col))
			}
			esc := l.next()
			switch esc {
			case 'n':
				sb.WriteRune('\n')
			case 'r':
				sb.WriteRune('\r')
			case 't':
				sb.WriteRune('\t')
			case '"':
				sb.WriteRune('"')
			case '\\':
				sb.WriteRune('\\')
			default:
				// Preserve literal escaped character
				sb.WriteRune(esc)
			}
		} else {
			sb.WriteRune(r)
		}
	}

	return &SNode{
		Kind:   NodeString,
		Value:  sb.String(),
		Line:   startLine,
		Column: startCol,
		Offset: startPos,
	}, nil
}

func (l *lexer) readKeyword() (*SNode, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	l.next() // consume ':'
	var sb strings.Builder

	for !l.eof() && !isDelimiter(l.peek()) {
		r := l.next()
		if r == ':' {
			return nil, l.error(startLine, startCol, startPos, "invalid keyword with duplicate colon ':'")
		}
		sb.WriteRune(r)
	}

	val := sb.String()
	if val == "" {
		return nil, l.error(startLine, startCol, startPos, "empty keyword ':'")
	}

	return &SNode{
		Kind:   NodeKeyword,
		Value:  val,
		Line:   startLine,
		Column: startCol,
		Offset: startPos,
	}, nil
}

func (l *lexer) readNumberOrSymbol() (*SNode, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	var sb strings.Builder
	for !l.eof() && !isDelimiter(l.peek()) {
		sb.WriteRune(l.next())
	}

	raw := sb.String()
	if num, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return &SNode{
			Kind:   NodeInteger,
			Int:    num,
			Value:  raw,
			Line:   startLine,
			Column: startCol,
			Offset: startPos,
		}, nil
	}

	// Not a valid integer; treat as symbol
	return &SNode{
		Kind:   NodeSymbol,
		Value:  raw,
		Line:   startLine,
		Column: startCol,
		Offset: startPos,
	}, nil
}

func (l *lexer) readSymbol() (*SNode, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	var sb strings.Builder
	for !l.eof() && !isDelimiter(l.peek()) {
		r := l.next()
		if r == ':' {
			return nil, l.error(startLine, startCol, startPos, "symbol cannot contain colon ':'")
		}
		sb.WriteRune(r)
	}

	raw := sb.String()
	if raw == "" {
		return nil, l.error(startLine, startCol, startPos, "empty or unexpected symbol")
	}

	return &SNode{
		Kind:   NodeSymbol,
		Value:  raw,
		Line:   startLine,
		Column: startCol,
		Offset: startPos,
	}, nil
}

// ParseSExpressions parses all S-expression forms in source with strict position tracking.
func ParseSExpressions(file, content string) ([]*SNode, error) {
	lex := newLexer(file, content)
	var nodes []*SNode
	for {
		node, err := lex.readForm()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// Parse parses roadmap data from raw bytes.
func Parse(data []byte) (*Roadmap, error) {
	return ParseNamed("", string(data))
}

// ParseFile parses roadmap data from a file on disk.
func ParseFile(path string) (*Roadmap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read roadmap file: %w", err)
	}
	return ParseNamed(path, string(data))
}

// ParseNamed parses roadmap data with an explicit file name for error reporting.
func ParseNamed(filename, content string) (*Roadmap, error) {
	nodes, err := ParseSExpressions(filename, content)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, &SyntaxError{
			File:    filename,
			Line:    1,
			Column:  1,
			Message: "empty roadmap document; at least one s-expression is required",
		}
	}

	rm := &Roadmap{
		File:         filename,
		Metadata:     make(map[string]any),
		epicMap:      make(map[string]*Epic),
		featureMap:   make(map[string]*Feature),
		criterionMap: make(map[string]*Criterion),
	}

	// We can have either one top-level form `(:schema ...)` or multiple forms.
	var topNodes []*SNode
	if len(nodes) == 1 && nodes[0].Kind == NodeList {
		// Check if the single list is a plist or a list of epics
		if len(nodes[0].List) > 0 && nodes[0].List[0].Kind == NodeKeyword {
			// Top-level plist, e.g. (:schema ... :epics ...)
			topNodes = nodes[0].List
		} else {
			// List of epics, e.g. ((:id "E01" ...) (:id "E02" ...))
			if err := rm.parseEpicList(nodes[0].List); err != nil {
				return nil, err
			}
			rm.buildIndexes()
			return rm, nil
		}
	} else {
		// Flattened or top-level forms
		topNodes = nodes
	}

	// Parse property list at top level
	plist, err := nodesToPlist(filename, topNodes)
	if err != nil {
		return nil, err
	}

	var byFeatureCriteria map[string]byFeatureEntry

	for key, valNode := range plist {
		switch key {
		case "schema":
			rm.Schema = valNode.Text()
		case "title":
			rm.Title = valNode.Text()
		case "epics":
			if valNode.Kind != NodeList {
				return nil, &SyntaxError{
					File:    filename,
					Line:    valNode.Line,
					Column:  valNode.Column,
					Message: ":epics must be a list of epic definitions",
				}
			}
			if err := rm.parseEpicList(valNode.List); err != nil {
				return nil, err
			}
		case "future-plans-v2":
			// If present as an epic-like structure
			if valNode.Kind == NodeList {
				epic, err := parseEpicNode(filename, valNode)
				if err == nil && epic != nil {
					rm.Epics = append(rm.Epics, epic)
				}
			}
		case "verification":
			if valNode.Kind == NodeList {
				verif, bfc, err := parseVerification(filename, valNode.List)
				if err != nil {
					return nil, err
				}
				rm.Verification = verif
				byFeatureCriteria = bfc
			}
		default:
			rm.Metadata[key] = nodeToInterface(valNode)
		}
	}

	// If verification contained :by-feature criteria, attach them to corresponding features
	if len(byFeatureCriteria) > 0 {
		for _, epic := range rm.Epics {
			for _, feat := range epic.Features {
				if entry, ok := byFeatureCriteria[feat.ID]; ok {
					if len(feat.Criteria) == 0 && len(entry.criteria) > 0 {
						feat.Criteria = entry.criteria
						for _, c := range feat.Criteria {
							c.FeatureID = feat.ID
							c.EpicID = epic.ID
							if c.VerificationNotes == "" && entry.tests != "" {
								c.VerificationNotes = entry.tests
							}
						}
					}
					if entry.tests != "" {
						if feat.VerificationNotes == "" {
							feat.VerificationNotes = entry.tests
						} else if !strings.Contains(feat.VerificationNotes, entry.tests) {
							feat.VerificationNotes = feat.VerificationNotes + "; " + entry.tests
						}
					}
				}
			}
		}
	}

	// Also ensure any subfeatures without explicit criteria are populated
	for _, epic := range rm.Epics {
		for _, feat := range epic.Features {
			if len(feat.Criteria) == 0 {
				if subfeats, ok := feat.Fields["subfeatures"].([]string); ok && len(subfeats) > 0 {
					for idx, sf := range subfeats {
						cid := fmt.Sprintf("%s-%02d", feat.ID, idx+1)
						c := &Criterion{
							ID:        cid,
							Title:     sf,
							Status:    feat.Status,
							RawStatus: feat.RawStatus,
							FeatureID: feat.ID,
							EpicID:    epic.ID,
							Line:      feat.Line,
							Column:    feat.Column,
						}
						feat.Criteria = append(feat.Criteria, c)
					}
				}
			}
		}
	}

	rm.buildIndexes()
	return rm, nil
}

type byFeatureEntry struct {
	criteria []*Criterion
	tests    string
}

func parseVerification(file string, nodes []*SNode) (Verification, map[string]byFeatureEntry, error) {
	var v Verification
	v.RawFields = make(map[string]any)
	byFeatureMap := make(map[string]byFeatureEntry)

	plist, err := nodesToPlist(file, nodes)
	if err != nil {
		return v, nil, err
	}

	for k, val := range plist {
		switch k {
		case "measured-at":
			v.MeasuredAt = val.Text()
		case "revision":
			v.Revision = val.Text()
		case "branch":
			v.Branch = val.Text()
		case "suite":
			v.Suite = val.Text()
		case "suite-result":
			v.SuiteResult = val.Text()
		case "criteria-rule":
			v.CriteriaRule = val.Text()
		case "rule":
			v.Rule = val.Text()
		case "verified-features":
			v.VerifiedFeatures = int(val.Int)
		case "verified-acceptance-items":
			v.VerifiedAcceptanceItems = int(val.Int)
		case "by-feature":
			if val.Kind == NodeList {
				for _, fNode := range val.List {
					if fNode.Kind != NodeList {
						continue
					}
					fPlist, err := nodesToPlist(file, fNode.List)
					if err != nil {
						return v, nil, err
					}
					featIDNode, ok := fPlist["feature"]
					if !ok {
						continue
					}
					featID := featIDNode.Text()
					var tests string
					if tNode, ok := fPlist["tests"]; ok {
						tests = tNode.Text()
					}
					var crits []*Criterion
					if cListNode, ok := fPlist["criteria"]; ok && cListNode.Kind == NodeList {
						for _, cNode := range cListNode.List {
							if cNode.Kind != NodeList {
								continue
							}
							c, err := parseCriterionNode(file, cNode)
							if err != nil {
								return v, nil, err
							}
							if c != nil {
								crits = append(crits, c)
							}
						}
					}
					byFeatureMap[featID] = byFeatureEntry{
						criteria: crits,
						tests:    tests,
					}
				}
			}
		default:
			v.RawFields[k] = nodeToInterface(val)
		}
	}

	return v, byFeatureMap, nil
}

func (rm *Roadmap) parseEpicList(nodes []*SNode) error {
	for _, n := range nodes {
		if n.Kind != NodeList {
			continue
		}
		epic, err := parseEpicNode(rm.File, n)
		if err != nil {
			return err
		}
		if epic != nil {
			rm.Epics = append(rm.Epics, epic)
		}
	}
	return nil
}

func parseEpicNode(file string, node *SNode) (*Epic, error) {
	plist, err := nodesToPlist(file, node.List)
	if err != nil {
		return nil, err
	}

	idNode, ok := plist["id"]
	if !ok || idNode.Text() == "" {
		return nil, &SyntaxError{
			File:    file,
			Line:    node.Line,
			Column:  node.Column,
			Message: "epic missing required :id",
		}
	}

	epic := &Epic{
		ID:     idNode.Text(),
		Line:   node.Line,
		Column: node.Column,
		Fields: make(map[string]any),
	}

	for k, val := range plist {
		switch k {
		case "id":
			epic.ID = val.Text()
		case "title":
			epic.Title = val.Text()
		case "status", "state":
			epic.RawStatus = val.Text()
			epic.Status = ParseStatus(val.Text())
		case "depends-on", "deps", "needs":
			epic.Dependencies = parseStringList(val)
		case "verification", "evidence", "tests", "notes":
			epic.VerificationNotes = parseNotes(val)
		case "features":
			if val.Kind == NodeList {
				for _, fNode := range val.List {
					if fNode.Kind != NodeList {
						continue
					}
					feat, err := parseFeatureNode(file, fNode, epic.ID)
					if err != nil {
						return nil, err
					}
					if feat != nil {
						epic.Features = append(epic.Features, feat)
					}
				}
			}
		default:
			epic.Fields[k] = nodeToInterface(val)
		}
	}

	return epic, nil
}

func parseFeatureNode(file string, node *SNode, epicID string) (*Feature, error) {
	plist, err := nodesToPlist(file, node.List)
	if err != nil {
		return nil, err
	}

	idNode, ok := plist["id"]
	if !ok || idNode.Text() == "" {
		return nil, &SyntaxError{
			File:    file,
			Line:    node.Line,
			Column:  node.Column,
			Message: "feature missing required :id",
		}
	}

	feat := &Feature{
		ID:     idNode.Text(),
		EpicID: epicID,
		Line:   node.Line,
		Column: node.Column,
		Fields: make(map[string]any),
	}

	for k, val := range plist {
		switch k {
		case "id":
			feat.ID = val.Text()
		case "title":
			feat.Title = val.Text()
		case "status", "state":
			feat.RawStatus = val.Text()
			feat.Status = ParseStatus(val.Text())
		case "depends-on", "deps", "needs":
			feat.Dependencies = parseStringList(val)
		case "verification", "evidence", "tests", "notes":
			feat.VerificationNotes = parseNotes(val)
		case "subfeatures":
			feat.Fields["subfeatures"] = parseStringList(val)
		case "criteria":
			if val.Kind == NodeList {
				for _, cNode := range val.List {
					if cNode.Kind != NodeList {
						continue
					}
					crit, err := parseCriterionNode(file, cNode)
					if err != nil {
						return nil, err
					}
					if crit != nil {
						crit.FeatureID = feat.ID
						crit.EpicID = epicID
						feat.Criteria = append(feat.Criteria, crit)
					}
				}
			}
		case "items":
			// Items (as in future plans) can be treated as criteria
			if val.Kind == NodeList {
				for _, itemNode := range val.List {
					if itemNode.Kind != NodeList {
						continue
					}
					crit, err := parseCriterionNode(file, itemNode)
					if err != nil {
						return nil, err
					}
					if crit != nil {
						crit.FeatureID = feat.ID
						crit.EpicID = epicID
						feat.Criteria = append(feat.Criteria, crit)
					}
				}
			}
		default:
			feat.Fields[k] = nodeToInterface(val)
		}
	}

	return feat, nil
}

func parseCriterionNode(file string, node *SNode) (*Criterion, error) {
	plist, err := nodesToPlist(file, node.List)
	if err != nil {
		return nil, err
	}

	idNode, ok := plist["id"]
	if !ok || idNode.Text() == "" {
		return nil, &SyntaxError{
			File:    file,
			Line:    node.Line,
			Column:  node.Column,
			Message: "criterion missing required :id",
		}
	}

	c := &Criterion{
		ID:     idNode.Text(),
		Line:   node.Line,
		Column: node.Column,
		Fields: make(map[string]any),
	}

	for k, val := range plist {
		switch k {
		case "id":
			c.ID = val.Text()
		case "title", "text", "description":
			c.Title = val.Text()
		case "status", "state":
			c.RawStatus = val.Text()
			c.Status = ParseStatus(val.Text())
		case "depends-on", "deps", "needs":
			c.Dependencies = parseStringList(val)
		case "verification", "evidence", "tests", "notes":
			c.VerificationNotes = parseNotes(val)
		default:
			c.Fields[k] = nodeToInterface(val)
		}
	}

	return c, nil
}

func (rm *Roadmap) buildIndexes() {
	rm.epicMap = make(map[string]*Epic)
	rm.featureMap = make(map[string]*Feature)
	rm.criterionMap = make(map[string]*Criterion)

	for _, epic := range rm.Epics {
		rm.epicMap[epic.ID] = epic
		for _, feat := range epic.Features {
			feat.EpicID = epic.ID
			rm.featureMap[feat.ID] = feat
			for _, crit := range feat.Criteria {
				crit.FeatureID = feat.ID
				crit.EpicID = epic.ID
				rm.criterionMap[crit.ID] = crit
			}
		}
	}
}

func nodesToPlist(file string, nodes []*SNode) (map[string]*SNode, error) {
	plist := make(map[string]*SNode)
	i := 0
	for i < len(nodes) {
		keyNode := nodes[i]
		if keyNode.Kind != NodeKeyword {
			// Skip leading bare symbols if any (e.g. 'epic' or 'roadmap' header symbol)
			if i == 0 && keyNode.Kind == NodeSymbol {
				i++
				continue
			}
			return nil, &SyntaxError{
				File:    file,
				Line:    keyNode.Line,
				Column:  keyNode.Column,
				Message: fmt.Sprintf("expected keyword for property key, got %s '%s'", nodeKindName(keyNode.Kind), keyNode.Value),
			}
		}

		if i+1 >= len(nodes) {
			return nil, &SyntaxError{
				File:    file,
				Line:    keyNode.Line,
				Column:  keyNode.Column,
				Message: fmt.Sprintf("keyword :%s has no associated value (odd number of elements)", keyNode.Value),
			}
		}

		valNode := nodes[i+1]
		plist[keyNode.Value] = valNode
		i += 2
	}
	return plist, nil
}

func nodeKindName(k NodeKind) string {
	switch k {
	case NodeList:
		return "list"
	case NodeKeyword:
		return "keyword"
	case NodeString:
		return "string"
	case NodeInteger:
		return "integer"
	case NodeSymbol:
		return "symbol"
	default:
		return "token"
	}
}

func parseStringList(node *SNode) []string {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case NodeString, NodeSymbol:
		if node.Value != "" {
			return []string{node.Value}
		}
	case NodeList:
		var res []string
		for _, item := range node.List {
			if item.Kind == NodeString || item.Kind == NodeSymbol {
				if item.Value != "" {
					res = append(res, item.Value)
				}
			}
		}
		return res
	}
	return nil
}

func parseNotes(node *SNode) string {
	if node == nil {
		return ""
	}
	switch node.Kind {
	case NodeString:
		return node.Value
	case NodeList:
		var parts []string
		for _, item := range node.List {
			if s := item.Text(); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "; ")
	default:
		return node.Text()
	}
}

func nodeToInterface(node *SNode) any {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case NodeString, NodeSymbol, NodeKeyword:
		return node.Value
	case NodeInteger:
		return node.Int
	case NodeList:
		var list []any
		for _, child := range node.List {
			list = append(list, nodeToInterface(child))
		}
		return list
	default:
		return node.Value
	}
}
