package merge

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// Verdict is one piece of evidence regarding a hold or approval.
// It can come from (a) a lane read record, (b) a forge review, or (c) a forge comment.
type Verdict struct {
	ID       string   // e.g. "record:2026-09-19T02:34:25Z", "comment:12345", "review:67890"
	Who      string   // resolved name or "unknown"
	Word     string   // "hold", "approve", "abstain", "pending", "note"
	Head     string   // commit SHA it binds to
	At       string   // ISO 8601 timestamp (created_at / submitted_at)
	Source   string   // "record", "review", "comment-rule", "comment-decided", "comment-pending"
	Scope    string   // optional scope text
	Releases []string // hold IDs released by scoped approve
	Conf     string   // confidence string, default "-"
	Carried  bool     // whether it binds to an earlier head than current
	RawID    int64    // numeric ID from forge (for tie-breaking)
	Foreign  bool     // true if login outside reviewer file
	Kind     string   // "line", "child", "card"
}

// StripQuotedAndCode removes quotes (lines starting with >) and fenced code blocks.
// S6: "Quoted material is removed mechanically before the evidence is framed:
// every line whose first non-space character is >, and every fenced code block,
// is dropped, because a status comment that quotes a hold is not a hold".
func StripQuotedAndCode(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ParseDispositionLine parses a typed DISPOSITION line:
// DISPOSITION who=<name> head=<sha40> verdict=HOLD [scope="<text>"]
func ParseDispositionLine(line string) (who, head, verdict, scope string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "DISPOSITION") {
		return "", "", "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "DISPOSITION"))
	fields := parseKeyValueFields(rest)
	who = fields["who"]
	head = strings.ToLower(fields["head"])
	verdict = strings.ToUpper(fields["verdict"])
	scope = fields["scope"]
	if verdict != "" {
		return who, head, verdict, scope, true
	}
	return "", "", "", "", false
}

func parseKeyValueFields(s string) map[string]string {
	res := make(map[string]string)
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			break
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var val string
		if len(s) > 0 && s[0] == '"' {
			s = s[1:]
			closeQuote := strings.IndexByte(s, '"')
			if closeQuote >= 0 {
				val = s[:closeQuote]
				s = s[closeQuote+1:]
			} else {
				val = s
				s = ""
			}
		} else {
			sp := strings.IndexFunc(s, unicode.IsSpace)
			if sp >= 0 {
				val = s[:sp]
				s = s[sp+1:]
			} else {
				val = s
				s = ""
			}
		}
		res[key] = val
	}
	return res
}

// IsAuthorNote checks if the body contains a typed note by the pull request's author:
// DISPOSITION who=<author> verdict=NOTE
// SPEC-DECIDE lines 1017-1019, 1565: the author's login excuses nothing (S6).
func IsAuthorNote(body, author string, rsOpt ...*ReviewerSet) bool {
	if author == "" {
		return false
	}
	var rs *ReviewerSet
	if len(rsOpt) > 0 {
		rs = rsOpt[0]
	}
	lines := strings.Split(body, "\n")
	for _, l := range lines {
		who, _, verdict, _, ok := ParseDispositionLine(l)
		if ok && strings.EqualFold(verdict, "NOTE") {
			if rs != nil && rs.IsLogin(who) && !rs.HasWho(who) {
				continue
			}
			if strings.EqualFold(who, author) {
				return true
			}
		}
	}
	return false
}

// ParseComment converts a GitHub comment into a Verdict under SPEC-DECIDE reading 3 rules.
func ParseComment(id int64, login, rawBody, at string, rs *ReviewerSet, author, currentHead string, ignoreUntyped bool) (Verdict, bool) {
	if rs != nil && !rs.IsScanned(login) {
		return Verdict{Foreign: true}, false
	}
	clean := StripQuotedAndCode(rawBody)
	if IsAuthorNote(clean, author, rs) {
		return Verdict{}, false
	}

	lines := strings.Split(clean, "\n")
	for _, l := range lines {
		typedWho, typedHead, verdict, scope, ok := ParseDispositionLine(l)
		if ok && strings.EqualFold(verdict, "HOLD") {
			resolvedWho, _ := rs.ResolveWho(login, typedWho)
			h := typedHead
			if h == "" {
				h = currentHead
			}
			v := Verdict{
				ID:     fmt.Sprintf("comment:%d", id),
				Who:    resolvedWho,
				Word:   "hold",
				Head:   h,
				At:     at,
				Source: "comment-rule",
				Scope:  scope,
				RawID:  id,
				Conf:   "-",
				Kind:   "line",
			}
			return v, true
		}
	}

	// Untyped check: rule table hold tokens
	// 1. First non-empty line begins with HOLD token
	first := firstNonEmptyLine(clean)
	firstWord := firstToken(first)
	isHoldFirstLine := strings.EqualFold(firstWord, "HOLD")

	// 2. Word HOLD as heading or inside bold anywhere in unquoted body
	isHoldHeadingOrBold := containsHoldHeadingOrBold(clean)

	if isHoldFirstLine || isHoldHeadingOrBold {
		// SPEC-DECIDE lines 1037-1040: untyped comment binds to current head
		head := currentHead
		v := Verdict{
			ID:     fmt.Sprintf("comment:%d", id),
			Who:    "unknown",
			Word:   "hold",
			Head:   head,
			At:     at,
			Source: "comment-rule",
			RawID:  id,
			Conf:   "-",
			Kind:   "line",
		}
		return v, true
	}

	// Untyped comment without hold
	if rs != nil && rs.LoginMayHold(login) {
		if ignoreUntyped {
			return Verdict{}, false
		}
		v := Verdict{
			ID:     fmt.Sprintf("comment:%d", id),
			Who:    "unknown",
			Word:   "pending",
			Head:   currentHead,
			At:     at,
			Source: "comment-pending",
			RawID:  id,
			Conf:   "-",
			Kind:   "line",
		}
		return v, true
	}

	return Verdict{}, false
}

// ParseReview converts a GitHub pull request review into a Verdict.
func ParseReview(id int64, login, rawBody, state, commitID, submittedAt string, rs *ReviewerSet, author, currentHead string) (Verdict, bool) {
	if rs != nil && !rs.IsScanned(login) {
		return Verdict{Foreign: true}, false
	}
	clean := StripQuotedAndCode(rawBody)

	var resolvedWho string
	var scope string
	lines := strings.Split(clean, "\n")
	for _, l := range lines {
		typedWho, _, _, s, ok := ParseDispositionLine(l)
		if ok {
			resolvedWho, _ = rs.ResolveWho(login, typedWho)
			scope = s
			break
		}
	}
	if resolvedWho == "" {
		resolvedWho = "unknown"
	}

	stateUpper := strings.ToUpper(strings.TrimSpace(state))
	switch stateUpper {
	case "CHANGES_REQUESTED", "DISMISSED":
		// SPEC-DECIDE lines 1092-1093, 1533: A dismissal releases nothing;
		// both CHANGES_REQUESTED and DISMISSED reviews are holds.
		head := strings.ToLower(strings.TrimSpace(commitID))
		if head == "" {
			head = currentHead
		}
		v := Verdict{
			ID:     fmt.Sprintf("review:%d", id),
			Who:    resolvedWho,
			Word:   "hold",
			Head:   head,
			At:     submittedAt,
			Source: "review",
			Scope:  scope,
			RawID:  id,
			Conf:   "-",
			Kind:   "line",
		}
		return v, true
	case "APPROVED":
		// Forge approved reviews release nothing and hold nothing
		return Verdict{}, false
	default:
		// COMMENTED review: inspect body like comment
		return ParseComment(id, login, rawBody, submittedAt, rs, author, currentHead, false)
	}
}

// UnliftedHolds is the canonical Reading 3 fold over all input verdicts.
// It extracts lane record approvals/holds and folds comments/reviews against them.
func UnliftedHolds(vs []Verdict, currentHead, author string, rs *ReviewerSet) []Verdict {
	var holds []Verdict
	var reads []Read
	for _, v := range vs {
		if v.Kind != "" && v.Kind != "line" {
			continue
		}
		if v.Source == "record" {
			reads = append(reads, Read{
				Who:      v.Who,
				Verdict:  v.Word,
				Head:     v.Head,
				At:       v.At,
				Scope:    v.Scope,
				Releases: v.Releases,
			})
		}
		if v.Word == "hold" || v.Word == "pending" || v.Source == "comment-pending" {
			if v.Word == "" || v.Word == "unknown" {
				v.Word = "pending"
			}
			holds = append(holds, v)
		}
	}
	return UnreleasedHolds(holds, reads, currentHead, author, rs)
}

// UnreleasedHolds folds active holds against lane read records.
// It returns only the holds (or pending comments) that remain unreleased.
func UnreleasedHolds(holds []Verdict, reads []Read, currentHead, author string, rs *ReviewerSet) []Verdict {
	var unreleased []Verdict

	for _, h := range holds {
		if h.Word != "hold" && h.Word != "pending" && h.Source != "comment-pending" {
			continue
		}
		if h.Kind != "" && h.Kind != "line" {
			continue
		}

		// Holder's may-hold permission check:
		// S6 / line 1114: if a named holder's may-hold is removed in the reviewer file,
		// their hold is no longer active.
		// SPEC-DECIDE line 1016: absent from reviewer file becomes who=unknown (never dropped).
		if h.Who != "unknown" && rs != nil {
			if rs.IsExplicitlyDisallowed(h.Who) {
				continue
			}
			if !rs.HasWho(h.Who) {
				h.Who = "unknown"
			}
		}

		released := false

		if h.Who != "unknown" {
			// A hold with a named who is released ONLY by that who recording APPROVE
			// at the current head.
			for _, rec := range reads {
				if rec.Verdict != "approve" || !sameLine(rec.Who, h.Who) {
					continue
				}
				if !headMatch(rec.Head, currentHead) {
					continue
				}
				// Same-time approval does not release hold (tie rule)
				if rec.At <= h.At {
					continue
				}
				if rec.Scope == "" {
					// Unscoped APPROVE releases every hold of that who
					released = true
					break
				}
				if releasesContains(rec.Releases, h.ID) {
					// Scoped APPROVE naming this hold ID
					released = true
					break
				}
			}
		} else {
			// who=unknown hold or source=comment-pending:
			// Released when a may-hold reader who is not the author records at current head
			// and later than the comment:
			// - either a HOLD of their own (takes it over under their name),
			// - or an APPROVE whose --releases names it by comment:<id>.
			for _, rec := range reads {
				if sameLine(rec.Who, author) {
					continue
				}
				if rs != nil && !rs.MayHold(rec.Who) {
					continue
				}
				if !headMatch(rec.Head, currentHead) {
					continue
				}
				if rec.At <= h.At {
					continue
				}
				if rec.Verdict == "hold" {
					// Takes over the unknown hold
					released = true
					break
				}
				if rec.Verdict == "approve" && releasesContains(rec.Releases, h.ID) {
					released = true
					break
				}
			}
		}

		if !released {
			h.Carried = !headMatch(h.Head, currentHead)
			if h.Conf == "" {
				h.Conf = "-"
			}
			unreleased = append(unreleased, h)
		}
	}

	// Order unreleased holds: older first by At, ties broken by RawID
	sort.SliceStable(unreleased, func(i, j int) bool {
		if unreleased[i].At != unreleased[j].At {
			return unreleased[i].At < unreleased[j].At
		}
		return unreleased[i].RawID < unreleased[j].RawID
	})

	return unreleased
}

// headMatch compares a candidate sha against target sha.
// Supports full sha comparison and prefix matching for valid 7-40 character prefixes.
func headMatch(candidate, target string) bool {
	c := strings.ToLower(strings.TrimSpace(candidate))
	t := strings.ToLower(strings.TrimSpace(target))
	if c == "" || t == "" {
		return false
	}
	if c == t {
		return true
	}
	if len(c) >= 7 && len(c) <= 40 && len(t) >= len(c) {
		if strings.HasPrefix(t, c) {
			return true
		}
	}
	if len(t) >= 7 && len(t) <= 40 && len(c) >= len(t) {
		if strings.HasPrefix(c, t) {
			return true
		}
	}
	return false
}

func releasesContains(releases []string, id string) bool {
	id = strings.TrimSpace(id)
	for _, r := range releases {
		if strings.EqualFold(strings.TrimSpace(r), id) {
			return true
		}
	}
	return false
}

func firstNonEmptyLine(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			return s
		}
	}
	return ""
}

func firstToken(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[0], ".,:;!?()[]*#_`")
}

func firstSHA(line string) string {
	clean := strings.NewReplacer("*", " ", "_", " ", "`", " ", "#", " ", ">", " ").Replace(line)
	for _, f := range strings.Fields(clean) {
		f = strings.Trim(f, ".,:;!?()[]")
		if len(f) < 7 || len(f) > 40 || !isHex(f) {
			continue
		}
		return strings.ToLower(f)
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}

func containsHoldHeadingOrBold(body string) bool {
	lines := strings.Split(body, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		// Heading: # HOLD ...
		if strings.HasPrefix(trimmed, "#") {
			words := strings.Fields(trimmed)
			for _, w := range words {
				if strings.Trim(w, ".,:;!?()[]*#_`") == "HOLD" {
					return true
				}
			}
		}
		// Bold: **HOLD...** or __HOLD...__
		if containsBoldWord(trimmed, "HOLD") {
			return true
		}
	}
	return false
}

func containsBoldWord(line, word string) bool {
	for _, marker := range []string{"**", "__"} {
		idx := 0
		for {
			start := strings.Index(line[idx:], marker)
			if start == -1 {
				break
			}
			start += idx + len(marker)
			end := strings.Index(line[start:], marker)
			if end == -1 {
				break
			}
			inner := line[start : start+end]
			for _, f := range strings.Fields(inner) {
				if strings.Trim(f, ".,:;!?()[]`") == word {
					return true
				}
			}
			idx = start + end + len(marker)
		}
	}
	return false
}

// ParseForgeVerdicts reads GitHub API comments and reviews and decodes them via ParseComment and ParseReview.
func ParseForgeVerdicts(comments, reviews string, n int, rs *ReviewerSet, author, currentHead string, ignoreUntyped bool) ([]Verdict, error) {
	var out []Verdict
	var rawComments []struct {
		ID        int64                  `json:"id"`
		User      struct{ Login string } `json:"user"`
		Body      string                 `json:"body"`
		CreatedAt string                 `json:"created_at"`
	}
	if err := decodeArrays(comments, &rawComments); err != nil {
		return nil, fmt.Errorf("pull request %d's comments did not answer JSON this tool can read: %w", n, err)
	}
	for _, c := range rawComments {
		if v, ok := ParseComment(c.ID, c.User.Login, c.Body, c.CreatedAt, rs, author, currentHead, ignoreUntyped); ok {
			out = append(out, v)
		}
	}

	var rawReviews []struct {
		ID          int64                  `json:"id"`
		User        struct{ Login string } `json:"user"`
		Body        string                 `json:"body"`
		State       string                 `json:"state"`
		SubmittedAt string                 `json:"submitted_at"`
		CommitID    string                 `json:"commit_id"`
	}
	if err := decodeArrays(reviews, &rawReviews); err != nil {
		return nil, fmt.Errorf("pull request %d's reviews did not answer JSON this tool can read: %w", n, err)
	}
	for _, r := range rawReviews {
		if v, ok := ParseReview(r.ID, r.User.Login, r.Body, r.State, r.CommitID, r.SubmittedAt, rs, author, currentHead); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func decodeVerdicts(comments, reviews string, n int) ([]Verdict, error) {
	return ParseForgeVerdicts(comments, reviews, n, nil, "", "", false)
}

func decodeArrays(raw string, into interface{}) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	all := []json.RawMessage{}
	for {
		var page []json.RawMessage
		err := dec.Decode(&page)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		all = append(all, page...)
	}
	joined, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(joined, into)
}
