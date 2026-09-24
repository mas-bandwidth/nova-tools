package typedrec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type fieldToken struct {
	key  string
	val  string
	line int
}

// Terminal status words.
const (
	StatusDone    = "DONE"
	StatusAbstain = "ABSTAIN"
	StatusBlocked = "BLOCKED"
)

// Defects named in the specification.
const (
	DefectMissing       = "missing"
	DefectUnknown       = "unknown"
	DefectDuplicate     = "duplicate"
	DefectMalformed     = "malformed"
	DefectContradictory = "contradictory"
	DefectOversized     = "oversized"
)

// Size limits.
const (
	MaxFileSize     = 64 * 1024 // 64 KB
	MaxFieldSize    = 4096      // 4096 bytes
	MaxEvidenceSize = 32 * 1024 // 32 KB
)

var (
	keyRegex  = regexp.MustCompile(`^[A-Z][A-Z0-9-]*$`)
	repoRegex = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9._-]+$`)
	hex40     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	priorRe   = regexp.MustCompile(`^#[1-9][0-9]* @[0-9a-f]{12}$`)
)

// Result is the parsed and validated RESULT v2 envelope.
type Result struct {
	Line1     string
	Status    string // DONE, ABSTAIN, BLOCKED
	StatusWhy string

	Schema   string
	Kind     string
	Attempt  int
	Check    string // pass, fail, not-run
	Repo     string
	Branch   string
	Paths    []string
	Red      string
	Green    string
	Prior    string
	PR       int
	Head     string
	Findings int
	Floor    string
	Suggest  string
	Probes   int

	Claims   map[string]string
	Evidence string
	Sections map[string][]string // heading -> list of - bullet rows

	// Validation defect outcome
	Valid  bool
	Field  string
	Defect string
	Line   int

	RawBytes  []byte
	RawSHA256 string
}

// ParseOptions configures verification against trusted card/machinery facts.
type ParseOptions struct {
	ExpectedKind    string
	ExpectedAttempt int
	ExpectedRepo    string
	ExpectedBranch  string
	ExpectedHead    string
	ContractLine    string
}

// Status returns the terminal status ("DONE", "ABSTAIN", "BLOCKED") of a RESULT.md file,
// routing through the legacy adapter if the file has no SCHEMA line.
func Status(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	s := string(data)
	if !strings.Contains(s, "SCHEMA:") && !strings.Contains(s, "SCHEMA ") {
		return LegacyStatus(data)
	}
	res := ParseResult(data)
	return res.Status
}

// ParseResult parses and validates a RESULT.md document according to the typedrec v2 contract.
func ParseResult(raw []byte, opts ...ParseOptions) Result {
	var opt ParseOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	sum := sha256.Sum256(raw)
	res := Result{
		Claims:    make(map[string]string),
		Sections:  make(map[string][]string),
		RawBytes:  raw,
		RawSHA256: hex.EncodeToString(sum[:]),
		Valid:     true,
	}

	fail := func(field, defect string, line int) Result {
		res.Valid = false
		res.Field = field
		res.Defect = defect
		res.Line = line
		return res
	}

	if len(raw) > MaxFileSize {
		return fail("file", DefectOversized, 0)
	}

	norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return fail("line 1", DefectMissing, 0)
	}

	// Line 1: card line 1 verbatim
	res.Line1 = lines[0]
	if strings.TrimSpace(res.Line1) == "" {
		return fail("line 1", DefectMissing, 1)
	}
	if opt.ContractLine != "" {
		want := strings.TrimRight(opt.ContractLine, " \t\r\n")
		got := strings.TrimRight(res.Line1, " \t\r\n")
		// Pulse legacy allows contract prefix match, but v2 requires exact contract match
		if got != want && !strings.HasPrefix(got, want) {
			return fail("line 1", DefectContradictory, 1)
		}
	}

	if len(lines) < 2 {
		return fail("line 2", DefectMissing, 0)
	}

	// Line 2: DONE | ABSTAIN <why> | BLOCKED <why>
	line2 := strings.TrimRight(lines[1], "\r")
	switch {
	case line2 == "DONE":
		res.Status = "DONE"
	case strings.HasPrefix(line2, "ABSTAIN "):
		res.Status = "ABSTAIN"
		res.StatusWhy = strings.TrimSpace(line2[len("ABSTAIN "):])
		if len(res.StatusWhy) < 1 || len(res.StatusWhy) > 512 {
			return fail("line 2", DefectMalformed, 2)
		}
	case strings.HasPrefix(line2, "BLOCKED "):
		res.Status = "BLOCKED"
		res.StatusWhy = strings.TrimSpace(line2[len("BLOCKED "):])
		if len(res.StatusWhy) < 1 || len(res.StatusWhy) > 512 {
			return fail("line 2", DefectMalformed, 2)
		}
	default:
		return fail("line 2", DefectMalformed, 2)
	}

	// Find split point between typed block and evidence block
	bodyStart := len(lines)
	inFence := false
	for i := 2; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.HasPrefix(l, "## ") {
			bodyStart = i
			break
		}
	}

	// Check evidence size if bodyStart < len(lines)
	if bodyStart < len(lines) {
		var evLen int
		for i := bodyStart; i < len(lines); i++ {
			evLen += len(lines[i]) + 1
		}
		if evLen > MaxEvidenceSize {
			return fail("evidence", DefectOversized, bodyStart+1)
		}
	}

	// Parse typed header fields
	seenKeys := make(map[string]bool)
	var typedFields []fieldToken

	for i := 2; i < bodyStart; i++ {
		rawLine := lines[i]
		l := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l) > MaxFieldSize {
			return fail("", DefectOversized, i+1)
		}

		// KEY: value syntax
		colon := strings.IndexByte(l, ':')
		space := strings.IndexByte(l, ' ')
		if colon <= 0 || (space > 0 && space < colon) {
			// Space form or missing colon is malformed in v2
			k := l
			if space > 0 {
				k = l[:space]
			}
			return fail(k, DefectMalformed, i+1)
		}

		key := l[:colon]
		if !keyRegex.MatchString(key) {
			return fail(key, DefectMalformed, i+1)
		}
		if colon+1 >= len(l) || l[colon+1] != ' ' {
			return fail(key, DefectMalformed, i+1)
		}
		val := l[colon+2:]

		if seenKeys[key] {
			return fail(key, DefectDuplicate, i+1)
		}
		seenKeys[key] = true
		typedFields = append(typedFields, fieldToken{key: key, val: val, line: i + 1})
		res.Claims[key] = val
	}

	// Determine KIND
	kind := opt.ExpectedKind
	var kindToken *fieldToken
	for i := range typedFields {
		if typedFields[i].key == "KIND" {
			kindToken = &typedFields[i]
			break
		}
	}
	if kindToken == nil {
		if kind == "" {
			return fail("KIND", DefectMissing, 0)
		}
	} else {
		val := kindToken.val
		if !isValidKind(val) {
			return fail("KIND", DefectMalformed, kindToken.line)
		}
		if kind != "" && val != kind {
			return fail("KIND", DefectContradictory, kindToken.line)
		}
		kind = val
		res.Kind = val
	}

	// Check for unknown keys for this kind
	for _, tf := range typedFields {
		req := Contract.RequirementFor(tf.key, kind)
		if req == ReqUnknown || req == "" {
			return fail(tf.key, DefectUnknown, tf.line)
		}
	}

	// Validate SCHEMA
	schemaVal, hasSchema := res.Claims["SCHEMA"]
	if !hasSchema {
		return fail("SCHEMA", DefectMissing, 0)
	}
	if schemaVal != "v2" {
		return fail("SCHEMA", DefectMalformed, findLine(typedFields, "SCHEMA"))
	}
	res.Schema = schemaVal

	// Validate ATTEMPT
	attemptVal, hasAttempt := res.Claims["ATTEMPT"]
	if !hasAttempt {
		return fail("ATTEMPT", DefectMissing, 0)
	}
	att, err := strconv.Atoi(attemptVal)
	if err != nil || att < 1 || att > 99 {
		return fail("ATTEMPT", DefectMalformed, findLine(typedFields, "ATTEMPT"))
	}
	if opt.ExpectedAttempt > 0 && att != opt.ExpectedAttempt {
		return fail("ATTEMPT", DefectContradictory, findLine(typedFields, "ATTEMPT"))
	}
	res.Attempt = att

	// Validate CHECK
	checkVal, hasCheck := res.Claims["CHECK"]
	if !hasCheck {
		return fail("CHECK", DefectMissing, 0)
	}
	if checkVal != "pass" && checkVal != "fail" && checkVal != "not-run" {
		return fail("CHECK", DefectMalformed, findLine(typedFields, "CHECK"))
	}
	res.Check = checkVal

	// Validate REPO
	repoVal, hasRepo := res.Claims["REPO"]
	if !hasRepo {
		return fail("REPO", DefectMissing, 0)
	}
	if !repoRegex.MatchString(repoVal) {
		return fail("REPO", DefectMalformed, findLine(typedFields, "REPO"))
	}
	if opt.ExpectedRepo != "" && repoVal != opt.ExpectedRepo {
		return fail("REPO", DefectContradictory, findLine(typedFields, "REPO"))
	}
	res.Repo = repoVal

	isDone := res.Status == "DONE"

	// BRANCH
	branchVal, hasBranch := res.Claims["BRANCH"]
	reqBranch := Contract.RequirementFor("BRANCH", kind)
	if isDone && reqBranch == ReqDone && !hasBranch {
		return fail("BRANCH", DefectMissing, 0)
	}
	if hasBranch {
		if len(branchVal) == 0 || len(branchVal) > 200 || !isValidGitRef(branchVal) {
			return fail("BRANCH", DefectMalformed, findLine(typedFields, "BRANCH"))
		}
		if opt.ExpectedBranch != "" && branchVal != opt.ExpectedBranch {
			return fail("BRANCH", DefectContradictory, findLine(typedFields, "BRANCH"))
		}
		res.Branch = branchVal
	}

	// PATHS
	pathsVal, hasPaths := res.Claims["PATHS"]
	reqPaths := Contract.RequirementFor("PATHS", kind)
	if isDone && reqPaths == ReqDone && !hasPaths {
		return fail("PATHS", DefectMissing, 0)
	}
	if hasPaths {
		paths, ok := parsePathsField(pathsVal)
		if !ok {
			return fail("PATHS", DefectMalformed, findLine(typedFields, "PATHS"))
		}
		res.Paths = paths
	}

	// RED
	redVal, hasRed := res.Claims["RED"]
	reqRed := Contract.RequirementFor("RED", kind)
	if isDone && reqRed == ReqDone && !hasRed {
		return fail("RED", DefectMissing, 0)
	}
	if hasRed {
		if len(redVal) < 1 || len(redVal) > MaxFieldSize {
			return fail("RED", DefectMalformed, findLine(typedFields, "RED"))
		}
		res.Red = redVal
	}

	// GREEN
	greenVal, hasGreen := res.Claims["GREEN"]
	reqGreen := Contract.RequirementFor("GREEN", kind)
	if isDone && reqGreen == ReqPass && res.Check == "pass" && !hasGreen {
		return fail("GREEN", DefectMissing, 0)
	}
	if hasGreen {
		if len(greenVal) < 1 || len(greenVal) > MaxFieldSize {
			return fail("GREEN", DefectMalformed, findLine(typedFields, "GREEN"))
		}
		res.Green = greenVal
	}

	// PRIOR
	priorVal, hasPrior := res.Claims["PRIOR"]
	reqPrior := Contract.RequirementFor("PRIOR", kind)
	if isDone && reqPrior == ReqDone && !hasPrior {
		return fail("PRIOR", DefectMissing, 0)
	}
	if hasPrior {
		if !priorRe.MatchString(priorVal) {
			return fail("PRIOR", DefectMalformed, findLine(typedFields, "PRIOR"))
		}
		res.Prior = priorVal
	}

	// PR
	prVal, hasPR := res.Claims["PR"]
	reqPR := Contract.RequirementFor("PR", kind)
	if isDone && reqPR == ReqDone && !hasPR {
		return fail("PR", DefectMissing, 0)
	}
	if hasPR {
		prNum, err := strconv.Atoi(prVal)
		if err != nil || prNum <= 0 {
			return fail("PR", DefectMalformed, findLine(typedFields, "PR"))
		}
		res.PR = prNum
	}

	// HEAD
	headVal, hasHead := res.Claims["HEAD"]
	reqHead := Contract.RequirementFor("HEAD", kind)
	if isDone && reqHead == ReqDone && !hasHead {
		return fail("HEAD", DefectMissing, 0)
	}
	if hasHead {
		if !hex40.MatchString(headVal) {
			return fail("HEAD", DefectMalformed, findLine(typedFields, "HEAD"))
		}
		if opt.ExpectedHead != "" && headVal != opt.ExpectedHead {
			return fail("HEAD", DefectContradictory, findLine(typedFields, "HEAD"))
		}
		res.Head = headVal
	}

	// FINDINGS
	findingsVal, hasFindings := res.Claims["FINDINGS"]
	reqFindings := Contract.RequirementFor("FINDINGS", kind)
	if isDone && reqFindings == ReqDone && !hasFindings {
		return fail("FINDINGS", DefectMissing, 0)
	}
	if hasFindings {
		findNum, err := strconv.Atoi(findingsVal)
		if err != nil || findNum < 0 || findNum > 999 {
			return fail("FINDINGS", DefectMalformed, findLine(typedFields, "FINDINGS"))
		}
		res.Findings = findNum
	}

	// FLOOR
	floorVal, hasFloor := res.Claims["FLOOR"]
	reqFloor := Contract.RequirementFor("FLOOR", kind)
	if isDone && reqFloor == ReqDone && !hasFloor {
		return fail("FLOOR", DefectMissing, 0)
	}
	if hasFloor {
		if floorVal != "HIGH" && floorVal != "MEDIUM" && floorVal != "LOW" && floorVal != "NONE" {
			return fail("FLOOR", DefectMalformed, findLine(typedFields, "FLOOR"))
		}
		if hasFindings {
			if res.Findings == 0 && floorVal != "NONE" {
				return fail("FLOOR", DefectContradictory, findLine(typedFields, "FLOOR"))
			}
			if res.Findings > 0 && floorVal == "NONE" {
				return fail("FLOOR", DefectContradictory, findLine(typedFields, "FLOOR"))
			}
		}
		res.Floor = floorVal
	}

	// SUGGEST
	suggestVal, hasSuggest := res.Claims["SUGGEST"]
	reqSuggest := Contract.RequirementFor("SUGGEST", kind)
	if isDone && reqSuggest == ReqDone && !hasSuggest {
		return fail("SUGGEST", DefectMissing, 0)
	}
	if hasSuggest {
		if suggestVal != "APPROVE" && suggestVal != "HOLD" {
			return fail("SUGGEST", DefectMalformed, findLine(typedFields, "SUGGEST"))
		}
		res.Suggest = suggestVal
	}

	// PROBES
	probesVal, hasProbes := res.Claims["PROBES"]
	reqProbes := Contract.RequirementFor("PROBES", kind)
	if isDone && reqProbes == ReqDone && !hasProbes {
		return fail("PROBES", DefectMissing, 0)
	}
	if hasProbes {
		pNum, err := strconv.Atoi(probesVal)
		if err != nil || pNum < 1 {
			return fail("PROBES", DefectMalformed, findLine(typedFields, "PROBES"))
		}
		res.Probes = pNum
	}

	// Evidence section parsing
	evidenceLines := lines[bodyStart:]
	res.Evidence = strings.Join(evidenceLines, "\n")

	type sectionTracker struct {
		name      string
		line      int
		rows      []string
		completed bool
	}
	var currentSection *sectionTracker
	seenHeadings := make(map[string]int)

	inFence = false
	for idx, rawL := range evidenceLines {
		lineNo := bodyStart + idx + 1
		l := strings.TrimRight(rawL, "\r")

		if strings.HasPrefix(l, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if strings.HasPrefix(l, "## ") {
			// Heading line
			secName := l[3:]
			if _, exists := seenHeadings[secName]; exists {
				return fail("## "+secName, DefectDuplicate, lineNo)
			}
			seenHeadings[secName] = lineNo
			currentSection = &sectionTracker{name: secName, line: lineNo}
			continue
		}

		if currentSection != nil {
			// Check if row: starts with "- " at column 0 and has non-whitespace after
			if strings.HasPrefix(l, "- ") {
				after := l[2:]
				if len(strings.Trim(after, " \t")) > 0 {
					currentSection.rows = append(currentSection.rows, l)
					res.Sections[currentSection.name] = append(res.Sections[currentSection.name], l)
				}
			}
		}
	}

	// Section verification on DONE
	if isDone {
		reqSecs := RequiredSections(kind)
		for _, sec := range reqSecs {
			lineNo, present := seenHeadings[sec]
			if !present {
				return fail("## "+sec, DefectMissing, 0)
			}
			rowCount := len(res.Sections[sec])
			if kind == KindRead && sec == "Findings" {
				if hasFindings && rowCount != res.Findings {
					return fail("FINDINGS", DefectContradictory, findLine(typedFields, "FINDINGS"))
				}
			} else {
				if rowCount == 0 {
					return fail("## "+sec, DefectMissing, lineNo)
				}
			}
		}

		if kind == KindReport && hasProbes {
			if len(res.Sections["Probes"]) != res.Probes {
				return fail("PROBES", DefectContradictory, findLine(typedFields, "PROBES"))
			}
		}
	}

	return res
}

func findLine(fields []fieldToken, key string) int {
	for _, f := range fields {
		if f.key == key {
			return f.line
		}
	}
	return 0
}

func isValidKind(k string) bool {
	for _, valid := range Kinds {
		if valid == k {
			return true
		}
	}
	return false
}

func parsePathsField(val string) ([]string, bool) {
	fields := strings.Fields(val)
	if len(fields) == 0 || len(fields) > 256 {
		return nil, false
	}
	seen := make(map[string]bool)
	for _, p := range fields {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Contains(p, "\\") {
			return nil, false
		}
		if seen[p] {
			return nil, false
		}
		seen[p] = true
	}
	return fields, true
}

func isValidGitRef(ref string) bool {
	if ref == "" || len(ref) > 200 {
		return false
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "//") {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return false
	}
	if strings.ContainsAny(ref, " ~^:?*[\t\n\\") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// ResultEnvelopeV2 represents a parsed and validated RESULT.md v2 document.
type ResultEnvelopeV2 struct {
	ContractLine     string
	Status           string // DONE, ABSTAIN, BLOCKED
	Schema           string // e.g. "v2"
	Attempt          string // e.g. "1"
	Check            string // pass, fail, not-run
	Branch           string
	Repo             string
	Paths            string
	IsFriendApproval bool              // Always false for worker-authored records (Stella boundary)
	Fields           map[string]string // Typed key-value fields
	Body             string            // Human evidence body
}

// KnownResultV2Headers defines the allowed typed headers before markdown sections.
var KnownResultV2Headers = map[string]bool{
	"CHECK":     true,
	"BRANCH":    true,
	"REPO":      true,
	"PATHS":     true,
	"KIND":      true,
	"SCHEMA":    true,
	"ATTEMPT":   true,
	"HEAD":      true,
	"PR":        true,
	"ISSUE":     true,
	"BASE":      true,
	"BASE-SHA":  true,
	"BASE_SHA":  true,
	"HOLD":      true,
	"HOLD_FILE": true,
	"RED":       true,
	"GREEN":     true,
	"LOCATION":  true,
	"COMMAND":   true,
	"TEST":      true,
	"APPLIED":   true,
	"REMAINS":   true,
}

// ValidateResultV2 parses and strictly validates a RESULT.md against v2 envelope rules.
func ValidateResultV2(raw string, kind string) (ResultEnvelopeV2, error) {
	var env ResultEnvelopeV2
	env.Fields = make(map[string]string)

	if len(raw) > MaxFileSize {
		return env, fmt.Errorf("RESULT.md exceeds maximum size limit of %d bytes (got %d)", MaxFileSize, len(raw))
	}

	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return env, fmt.Errorf("RESULT.md v2 wants at least 3 lines, got %d", len(lines))
	}

	// Line 1: contract line
	line1 := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(line1, "RESULT ") && !strings.HasPrefix(line1, "RESULT:") {
		return env, fmt.Errorf("line 1 must begin with RESULT, got %q", line1)
	}
	env.ContractLine = line1

	// Line 2: terminal word strictly DONE, ABSTAIN, BLOCKED
	line2 := strings.TrimSpace(lines[1])
	statusWord := line2
	if idx := strings.IndexByte(line2, ' '); idx >= 0 {
		statusWord = line2[:idx]
	}
	switch statusWord {
	case "DONE", "ABSTAIN", "BLOCKED":
		env.Status = statusWord
	case "Returned", "check-failed", "Verified", "Landed":
		return env, fmt.Errorf("line 2 status %q is prohibited as a worker terminal word; must be DONE, ABSTAIN, or BLOCKED", statusWord)
	default:
		return env, fmt.Errorf("line 2 wants DONE, ABSTAIN, or BLOCKED, got %q", line2)
	}

	seenKeys := make(map[string]bool)
	bodyStart := len(lines)

	// Scan typed header fields before markdown headers
	for i := 2; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			bodyStart = i
			break
		}
		if len(ln) > MaxFieldSize {
			return env, fmt.Errorf("line %d exceeds 4096-byte field limit", i+1)
		}

		var key, val string
		colon := strings.IndexByte(ln, ':')
		space := strings.IndexByte(ln, ' ')

		// Parse either KEY: VAL or KEY VAL
		if colon > 0 && (space < 0 || colon < space) {
			key = strings.ToUpper(strings.TrimSpace(ln[:colon]))
			val = strings.TrimSpace(ln[colon+1:])
		} else if space > 0 {
			candidateKey := strings.ToUpper(strings.TrimSpace(ln[:space]))
			if KnownResultV2Headers[candidateKey] {
				key = candidateKey
				val = strings.TrimSpace(ln[space+1:])
			} else {
				return env, fmt.Errorf("unknown header field %q at line %d", candidateKey, i+1)
			}
		} else {
			return env, fmt.Errorf("malformed header line %d: %q", i+1, ln)
		}

		if !KnownResultV2Headers[key] {
			return env, fmt.Errorf("unknown header field %q at line %d", key, i+1)
		}

		if seenKeys[key] {
			return env, fmt.Errorf("duplicate field %q at line %d", key, i+1)
		}
		seenKeys[key] = true
		env.Fields[key] = val

		switch key {
		case "SCHEMA":
			env.Schema = val
		case "ATTEMPT":
			env.Attempt = val
		case "CHECK":
			if val != "pass" && val != "fail" && val != "not-run" {
				return env, fmt.Errorf("CHECK wants pass, fail, or not-run, got %q", val)
			}
			env.Check = val
		case "BRANCH":
			env.Branch = val
		case "REPO":
			env.Repo = val
		case "PATHS":
			env.Paths = val
		}
	}

	if env.Schema == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `SCHEMA: v2` line")
	}
	if env.Schema != "v2" {
		return env, fmt.Errorf("unsupported SCHEMA %q (wants v2)", env.Schema)
	}
	if env.Attempt == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `ATTEMPT: <n>` line")
	}
	if env.Check == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `CHECK: <pass|fail|not-run>` line")
	}
	if env.Repo == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a REPO line")
	}

	// For DONE status on non-read/non-report kinds, require BRANCH and PATHS.
	if env.Status == "DONE" && kind != "read" && kind != "report" && kind != "" {
		if env.Branch == "" {
			return env, fmt.Errorf("RESULT.md v2 for %s requires BRANCH line", kind)
		}
		if env.Paths == "" {
			return env, fmt.Errorf("RESULT.md v2 for %s requires PATHS line", kind)
		}
	}

	// Extract and validate evidence body
	if bodyStart < len(lines) {
		body := strings.Join(lines[bodyStart:], "\n")
		if len(body) > MaxEvidenceSize {
			return env, fmt.Errorf("human evidence body exceeds %d bytes (got %d)", MaxEvidenceSize, len(body))
		}
		env.Body = body
	}

	return env, nil
}
