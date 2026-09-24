package typedrec

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	doneWithTokenRe  = regexp.MustCompile(`^DONE\s+\S+$`)
	resultDoneLineRe = regexp.MustCompile(`^RESULT\s+\S+\s+DONE$`)
	keyDoneRe        = regexp.MustCompile(`(?i)^(STATUS|VERDICT):\s*DONE$`)
)

// LegacyStatus returns the status ("DONE", "ABSTAIN", "BLOCKED") of a legacy RESULT.md file
// that does not carry a SCHEMA line.
func LegacyStatus(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if bytes.Contains(data, []byte("FAILED")) {
		return ""
	}
	norm := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	if len(lines) == 0 {
		return ""
	}

	// Check line 2 if available
	if len(lines) >= 2 {
		line2 := strings.TrimSpace(lines[1])
		switch {
		case strings.HasPrefix(line2, "ABSTAIN"):
			return "ABSTAIN"
		case strings.HasPrefix(line2, "BLOCKED"):
			return "BLOCKED"
		case strings.HasPrefix(line2, "DONE") || line2 == "**DONE**":
			return "DONE"
		}
	}

	// Line 1 prefix check (some very old files started immediately with status)
	line1 := strings.TrimSpace(lines[0])
	if strings.HasPrefix(line1, "ABSTAIN") {
		return "ABSTAIN"
	}

	// Scan lines for queue.go legacy matchers
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "DONE" || trimmed == "**DONE**" {
			return "DONE"
		}
		if doneWithTokenRe.MatchString(trimmed) {
			return "DONE"
		}
		if resultDoneLineRe.MatchString(trimmed) {
			return "DONE"
		}
		if keyDoneRe.MatchString(trimmed) {
			return "DONE"
		}
	}
	return ""
}

// LegacyClassify implements pulse's legacy classification logic for non-v2 files.
func LegacyClassify(contract, body string, isPro bool) (state, branch, repo string, lines []string) {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	lines = strings.Split(norm, "\n")
	if len(lines) < 2 {
		return "mismatch", "", "", lines
	}

	line1 := strings.TrimSpace(firstNonEmptyLine(lines))
	want := strings.TrimRight(strings.TrimSpace(contract), " \t")
	if want == "" || !strings.HasPrefix(strings.TrimRight(line1, " \t"), want) {
		return "mismatch", "", "", lines
	}

	line2 := strings.TrimSpace(lines[1])
	if line2 == "" {
		return "mismatch", "", "", lines
	}
	if strings.HasPrefix(line2, "ABSTAIN") {
		return "abstain", "", "", lines
	}
	if strings.HasPrefix(line2, "BLOCKED") {
		return "mismatch", "", "", lines
	}
	if !strings.HasPrefix(line2, "DONE") {
		return "mismatch", "", "", lines
	}
	if len(lines) <= 2 {
		return "mismatch", "", "", lines
	}

	for _, l := range lines[2:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") {
			break
		}
		if strings.HasPrefix(t, "BRANCH ") {
			branch = strings.TrimSpace(strings.TrimPrefix(t, "BRANCH "))
		} else if strings.HasPrefix(t, "BRANCH: ") {
			branch = strings.TrimSpace(strings.TrimPrefix(t, "BRANCH: "))
		}
		if strings.HasPrefix(t, "REPO ") {
			repo = strings.TrimSpace(strings.TrimPrefix(t, "REPO "))
			repo = strings.TrimPrefix(repo, "github.com/")
		} else if strings.HasPrefix(t, "REPO: ") {
			repo = strings.TrimSpace(strings.TrimPrefix(t, "REPO: "))
			repo = strings.TrimPrefix(repo, "github.com/")
		}
	}
	if branch == "" || branch == "main" || branch == "master" {
		return "mismatch", branch, repo, lines
	}
	if isPro && !hasRedLineLegacy(lines) {
		return "refused", branch, repo, lines
	}
	return "done", branch, repo, lines
}

func firstNonEmptyLine(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

func hasRedLineLegacy(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "RED:") || strings.HasPrefix(t, "RED ") || strings.HasPrefix(t, "red:") || strings.HasPrefix(t, "red ") {
			return true
		}
	}
	return false
}

// CleanResultSection extracts and orders the essential RESULT.md lines so that:
// line 1 is the contract line, line 2 is DONE, followed by BRANCH, REPO, prior,
// and the verbatim red and green lines, followed by any remaining result lines.
// If red: or green: lines are missing from resultLines, they are looked for in report.
func CleanResultSection(resultLines []string, report string) []string {
	if len(resultLines) == 0 {
		return nil
	}
	var out []string
	line1 := strings.TrimSpace(firstNonEmptyLine(resultLines))
	out = append(out, line1)

	var remaining []string
	foundLine1 := false
	for _, l := range resultLines {
		t := strings.TrimSpace(l)
		if !foundLine1 && t == line1 {
			foundLine1 = true
			continue
		}
		remaining = append(remaining, l)
	}

	doneLine := ""
	var rest []string
	for i, l := range remaining {
		t := strings.TrimSpace(l)
		if i == 0 && (strings.HasPrefix(t, "DONE") || strings.HasPrefix(t, "done")) {
			doneLine = t
			continue
		}
		if t == "DONE" && doneLine == "" {
			doneLine = t
			continue
		}
		rest = append(rest, l)
	}
	if doneLine != "" {
		out = append(out, doneLine)
	} else if len(remaining) > 0 && strings.TrimSpace(remaining[0]) == "DONE" {
		out = append(out, "DONE")
		rest = remaining[1:]
	}

	var headers []string
	var redLines []string
	var greenLines []string
	var others []string

	for _, l := range rest {
		t := strings.TrimSpace(l)
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(t, "BRANCH ") || strings.HasPrefix(t, "BRANCH:"):
			headers = append(headers, l)
		case strings.HasPrefix(t, "REPO ") || strings.HasPrefix(t, "REPO:"):
			headers = append(headers, l)
		case strings.HasPrefix(lower, "prior:") || strings.HasPrefix(lower, "prior "):
			headers = append(headers, l)
		case strings.HasPrefix(lower, "red:") || strings.HasPrefix(lower, "red "):
			redLines = append(redLines, l)
		case strings.HasPrefix(lower, "green:") || strings.HasPrefix(lower, "green "):
			greenLines = append(greenLines, l)
		default:
			others = append(others, l)
		}
	}

	if len(redLines) == 0 {
		if r := findRedLineInReport(report); r != "" {
			redLines = append(redLines, r)
		}
	}
	if len(greenLines) == 0 {
		if g := findGreenLineInReport(report); g != "" {
			greenLines = append(greenLines, g)
		}
	}

	out = append(out, headers...)
	out = append(out, redLines...)
	out = append(out, greenLines...)
	out = append(out, others...)
	return out
}

func findRedLineInReport(report string) string {
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "red:") || strings.HasPrefix(strings.ToLower(t), "red ") {
			return t
		}
		if strings.HasPrefix(strings.ToUpper(t), "EVIDENCE:") {
			parts := strings.Split(t[len("EVIDENCE:"):], "|")
			for _, p := range parts {
				pt := strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(pt), "red:") || strings.HasPrefix(strings.ToLower(pt), "red ") {
					return pt
				}
			}
		}
	}
	return ""
}

func findGreenLineInReport(report string) string {
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "green:") || strings.HasPrefix(strings.ToLower(t), "green ") {
			return t
		}
		if strings.HasPrefix(strings.ToUpper(t), "EVIDENCE:") {
			parts := strings.Split(t[len("EVIDENCE:"):], "|")
			for _, p := range parts {
				pt := strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(pt), "green:") || strings.HasPrefix(strings.ToLower(pt), "green ") {
					return pt
				}
			}
		}
	}
	return ""
}

// ParsePaths extracts declared PATHS from a card or result text.
func ParsePaths(text string) (globs []string, declared bool) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(t, "PATHS:"):
			rest = strings.TrimSpace(strings.TrimPrefix(t, "PATHS:"))
		case strings.HasPrefix(t, "PATHS "):
			rest = strings.TrimSpace(strings.TrimPrefix(t, "PATHS "))
		default:
			continue
		}
		if rest == "" || rest == "none" {
			return nil, true
		}
		for _, g := range strings.FieldsFunc(rest, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			if g != "none" {
				globs = append(globs, g)
			}
		}
		return globs, true
	}
	return nil, false
}

// ReadVerdictResult holds the verdict parsed from a read card result.
type ReadVerdictResult struct {
	Repo string
	PR   int
	Say  string // APPROVE or HOLD
	Head string
}

// ParseReadVerdict extracts the read verdict from a read card's lines.
func ParseReadVerdict(lines []string) *ReadVerdictResult {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "PR") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimPrefix(t, "PR"), ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil {
			continue
		}
		v := &ReadVerdictResult{PR: n}
		for _, f := range strings.Fields(rest) {
			switch {
			case f == "APPROVE" || f == "HOLD":
				v.Say = f
			default:
				if s, ok := strings.CutPrefix(f, "head="); ok {
					v.Head = s
				}
				if s, ok := strings.CutPrefix(f, "repo="); ok {
					v.Repo = strings.TrimPrefix(s, "github.com/")
				}
			}
		}
		if v.Say == "" {
			continue
		}
		return v
	}
	return nil
}

// AbstainReason extracts the reason token from an abstain card, returning "" if not abstained.
func AbstainReason(lines []string) string {
	for i, l := range lines {
		if i > 1 {
			break
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(l), "ABSTAIN")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		for _, f := range fields {
			if v, ok := strings.CutPrefix(f, "reason="); ok {
				return strings.Trim(v, ":,")
			}
		}
		if len(fields) > 0 {
			return strings.Trim(fields[0], ":,")
		}
		return "unsaid"
	}
	return ""
}

// BranchAndRepo extracts branch and repo from result lines.
func BranchAndRepo(lines []string) (branch, repo string) {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		for _, p := range []string{"BRANCH:", "BRANCH"} {
			if v, ok := strings.CutPrefix(t, p); ok && branch == "" {
				branch = strings.TrimSpace(v)
			}
		}
		for _, p := range []string{"REPO:", "REPO"} {
			if v, ok := strings.CutPrefix(t, p); ok && repo == "" {
				repo = strings.TrimPrefix(strings.TrimSpace(v), "github.com/")
			}
		}
	}
	return branch, repo
}

// ClassifyResult classifies a result file according to v2 or legacy rules, exactly matching harvest expectations.
func ClassifyResult(model, contract, body string, expectedSchema, expectedAttempt string) (state, branch, repo string, resultLines []string) {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	line1 := strings.TrimSpace(firstNonEmptyLine(lines))
	want := strings.TrimRight(strings.TrimSpace(contract), " \t")
	if want == "" || !strings.HasPrefix(strings.TrimRight(line1, " \t"), want) {
		return "mismatch", "", "", lines
	}

	var rawSchema, rawCheck, rawAttempt string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") {
			break
		}
		if strings.HasPrefix(t, "SCHEMA:") || strings.HasPrefix(t, "SCHEMA ") {
			rawSchema = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "SCHEMA:"), "SCHEMA "))
		}
		if strings.HasPrefix(t, "CHECK:") || strings.HasPrefix(t, "CHECK ") {
			rawCheck = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "CHECK:"), "CHECK "))
		}
		if strings.HasPrefix(t, "ATTEMPT:") || strings.HasPrefix(t, "ATTEMPT ") {
			rawAttempt = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "ATTEMPT:"), "ATTEMPT "))
		}
	}

	if rawSchema != "" && rawSchema != "v2" {
		return "mismatch", "", "", lines
	}

	expSchema := expectedSchema
	if expSchema == "" {
		if rawSchema == "v2" {
			expSchema = "v2"
		} else {
			expSchema = "legacy"
		}
	}

	if expSchema == "v2" {
		if rawSchema != "v2" || rawCheck == "" {
			return "mismatch", "", "", lines
		}
		if rawAttempt != "" && expectedAttempt != "" && rawAttempt != expectedAttempt {
			return "mismatch", "", "", lines
		}
		return ClassifyV2Result(contract, body, lines, expectedAttempt, model == "pro")
	}

	if rawSchema != "" && rawSchema != "legacy" {
		return "mismatch", "", "", lines
	}
	return LegacyClassify(contract, body, model == "pro")
}

// ClassifyV2Result classifies a v2 envelope.
func ClassifyV2Result(contract, body string, lines []string, expectedAttempt string, isPro bool) (state, branch, repo string, resultLines []string) {
	kind := KindFromContract(contract)
	if kind == "" {
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "## ") {
				break
			}
			if strings.HasPrefix(t, "KIND:") || strings.HasPrefix(t, "KIND ") {
				k := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "KIND:"), "KIND "))
				if isValidKind(k) {
					kind = k
					break
				}
			}
		}
	}

	env, err := ValidateResultV2(body, kind)
	if err != nil {
		return "mismatch", "", "", lines
	}

	repo = strings.TrimPrefix(env.Repo, "github.com/")

	if expectedAttempt != "" && env.Attempt != expectedAttempt {
		return "mismatch", "", repo, lines
	}

	switch env.Status {
	case StatusAbstain:
		return "abstain", "", repo, lines
	case StatusBlocked:
		return "mismatch", "", repo, lines
	case StatusDone:
		if env.Check != "pass" {
			return "returned", "", repo, lines
		}
		branch = env.Branch
		if branch == "" || branch == "main" || branch == "master" {
			return "mismatch", branch, repo, lines
		}
		if isPro && !hasRedLineLegacy(lines) {
			return "refused", branch, repo, lines
		}
		return "done", branch, repo, lines
	default:
		return "mismatch", "", repo, lines
	}
}

// KindFromContract extracts the kind from a contract line if possible.
func KindFromContract(contract string) string {
	f := strings.Fields(contract)
	for _, w := range f {
		w = strings.TrimSuffix(w, ":")
		if isValidKind(w) {
			return w
		}
	}
	return ""
}

// ResultState returns line 2's status word, "" when the file has no line 2. A card that
// still carries the template's `<-` arrow or its `<why>` placeholder is TEMPLATE.
func ResultState(lines []string) string {
	seen := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !seen {
			seen = true
			continue
		}
		f := strings.Fields(t)
		for _, w := range f {
			if w == "<-" || strings.Contains(w, "<why>") {
				return "TEMPLATE"
			}
		}
		return strings.TrimRight(f[0], ":,;.")
	}
	return ""
}

// DispositionClaim is a parsed typed DISPOSITION line claim.
type DispositionClaim struct {
	Who       string
	Head      string
	Verdict   string
	Score     string
	Scope     string
	Whole     bool
	Authority string
	Valid     bool
	Field     string
	Defect    string
}

// ParseDispositionClaims extracts and parses DISPOSITION lines from raw text.
func ParseDispositionClaims(raw []byte) []DispositionClaim {
	var claims []DispositionClaim
	norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if !strings.HasPrefix(trimmed, "DISPOSITION") {
			continue
		}
		rest := trimmed[len("DISPOSITION"):]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		fields, whole := parseDispositionKV(rest)
		who := fields["who"]
		head := strings.ToLower(fields["head"])
		verdict := strings.ToUpper(fields["verdict"])
		score := fields["score"]
		scope := fields["scope"]

		claim := DispositionClaim{
			Who:       who,
			Head:      head,
			Verdict:   verdict,
			Score:     score,
			Scope:     scope,
			Whole:     whole,
			Authority: "unverified",
			Valid:     true,
		}

		if who == "" {
			claim.Valid = false
			claim.Field = "who"
			claim.Defect = "missing"
		} else if verdict == "" {
			claim.Valid = false
			claim.Field = "verdict"
			claim.Defect = "missing"
		} else if verdict != "APPROVE" && verdict != "HOLD" {
			claim.Valid = false
			claim.Field = "verdict"
			claim.Defect = "malformed"
		} else if verdict == "APPROVE" && head == "" {
			claim.Valid = false
			claim.Field = "head"
			claim.Defect = "missing"
		}

		claims = append(claims, claim)
	}
	return claims
}

func parseDispositionKV(s string) (map[string]string, bool) {
	fields := make(map[string]string)
	s = strings.TrimSpace(s)
	whole := true
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			whole = false
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		if s == "" {
			fields[key] = ""
			break
		}
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				whole = false
				fields[key] = s[1:]
				break
			}
			fields[key] = s[1 : end+1]
			s = strings.TrimSpace(s[end+2:])
		} else {
			sp := strings.IndexAny(s, " \t")
			if sp < 0 {
				fields[key] = s
				s = ""
			} else {
				fields[key] = s[:sp]
				s = strings.TrimSpace(s[sp+1:])
			}
		}
	}
	return fields, whole
}

// LineTwo is prereview's DONE rule over a RESULT text (#2506 part A moved it
// here from prereview.doneCheck): line 2, CRLF normalised and trimmed, must be
// the bare word DONE. Not "DONE." and not "DONE (with notes)". present is false
// when the text has no second line; line2 is the trimmed line as written.
func LineTwo(text string) (line2 string, present, done bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return "", false, false
	}
	line2 = strings.TrimSpace(lines[1])
	return line2, true, line2 == StatusDone
}
