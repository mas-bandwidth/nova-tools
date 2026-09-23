package merge

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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
	who = normWho(fields["who"])
	head = strings.ToLower(fields["head"])
	verdict = strings.ToUpper(fields["verdict"])
	scope = fields["scope"]
	if verdict != "" {
		return who, head, verdict, scope, true
	}
	return "", "", "", "", false
}

// dispositionWholeLine parses a line that is a DISPOSITION line WHOLE and nothing else:
// the first token is exactly DISPOSITION, and everything after it is key=value fields with
// no stray prose and no repeated key.
//
// nova-tools #2550, second ask: "the DISPOSITION line must be the whole line (no smuggled
// verdict inside a multi-line body)". This strict form gates the RELEASING verdict only --
// a typed APPROVE. A HOLD keeps the lenient ParseDispositionLine, on purpose: tightening
// the parser on the holding side would turn a sloppily typed HOLD into a non-hold, and a
// rule about smuggling must never fail open on the side that stops a merge.
func dispositionWholeLine(line string) (map[string]string, bool) {
	line = strings.TrimSpace(line)
	const word = "DISPOSITION"
	if !strings.HasPrefix(line, word) {
		return nil, false
	}
	rest := line[len(word):]
	if rest != "" && !isSpaceByte(rest[0]) {
		// DISPOSITIONS, DISPOSITION: and friends are not the typed line.
		return nil, false
	}
	return parseKeyValueFieldsStrict(rest)
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\v' || b == '\f'
}

// parseKeyValueFieldsStrict is parseKeyValueFields with the tolerance taken out: every
// token must belong to a key=value field, keys must be bare words, a quoted value must
// close, and no key may be given twice. Anything else and the line is not a typed line.
func parseKeyValueFieldsStrict(s string) (map[string]string, bool) {
	res := make(map[string]string)
	for {
		s = strings.TrimLeft(s, " \t\r\n\v\f")
		if s == "" {
			return res, true
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, false
		}
		key := s[:eq]
		if !isBareKey(key) {
			return nil, false
		}
		s = s[eq+1:]
		var val string
		if len(s) > 0 && s[0] == '"' {
			s = s[1:]
			closeQuote := strings.IndexByte(s, '"')
			if closeQuote < 0 {
				return nil, false
			}
			val = s[:closeQuote]
			s = s[closeQuote+1:]
			if s != "" && !isSpaceByte(s[0]) {
				return nil, false
			}
		} else {
			sp := strings.IndexFunc(s, unicode.IsSpace)
			if sp >= 0 {
				val, s = s[:sp], s[sp:]
			} else {
				val, s = s, ""
			}
		}
		if _, dup := res[key]; dup {
			return nil, false
		}
		res[key] = val
	}
}

func isBareKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
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
	if len(rawBody) > MaxCommentBodyBytes {
		rawBody = strings.Clone(rawBody[:MaxCommentBodyBytes])
	}
	clean := StripQuotedAndCode(rawBody)
	if IsAuthorNote(clean, author, rs) {
		return Verdict{}, false
	}

	lines := strings.Split(clean, "\n")
	hasTypedApprove := false
	for _, l := range lines {
		typedWho, typedHead, verdict, scope, ok := ParseDispositionLine(l)
		if ok {
			if strings.EqualFold(verdict, "HOLD") {
				resolvedWho, _ := rs.ResolveWho(login, typedWho)
				h := typedHead
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
			if strings.EqualFold(verdict, "APPROVE") {
				hasTypedApprove = true
			}
		}
	}

	// Untyped check: a comment yields HOLD from a verdict-SHAPED line (nova-tools
	// #2631, #2454). The word HOLD inside a body -- a bold aside inside a bullet about somebody
	// else's hold, a heading recapping one, a quoted line -- is never a verdict; a LINE
	// that is, on its own, one of the recognised shapes is.
	//
	// Independent HOLD evidence takes precedence over a typed or explicit APPROVE in the same
	// comment, preserving unresolved HOLD evidence even in mixed-message comments (#2454).
	holdLine := ""
	for _, l := range lines {
		if isHoldShapedLine(l) {
			holdLine = strings.TrimSpace(l)
			break
		}
	}
	if holdLine != "" {
		// SPEC-DECIDE lines 1037-1040: untyped comment binds to current head
		head := currentHead
		who := deriveHoldWho(holdLine)
		v := Verdict{
			ID:     fmt.Sprintf("comment:%d", id),
			Who:    who,
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

	// A typed APPROVE (nova-tools #2550), read when no hold-shaped line is present:
	if v, ok := approveVerdict(id, login, lines, at, rs, currentHead); ok {
		return v, true
	}

	// If no HOLD was recognized, handle approvals:
	// Strictly no comment promotion to APPROVE: keep typed and explicit APPROVE inert.
	// They do not grant approval, and they do not fall through to comment-pending (#2454).
	firstWord := ""
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			firstWord = firstToken(t)
			break
		}
	}
	if hasTypedApprove || strings.EqualFold(firstWord, "APPROVE") {
		return Verdict{}, false
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

// approveVerdict reads a typed APPROVE off a comment body's lines.
//
// Two guards, both from nova-tools #2550:
//
//   - the DISPOSITION must be the WHOLE line (dispositionWholeLine), so a release cannot
//     be smuggled into a sentence in a long body;
//   - its head= is kept as typed and compared later by headMatch, which is the same
//     prefix rule a typed HOLD's head= gets. `head=5adf9cb2` and the whole 40-character
//     object name are the same head to this tool, on both verdicts, or a friend who
//     abbreviates holds a pull request forever without meaning to.
//
// A who the reviewer file does not map to this login resolves to "unknown", and ambiguity
// approves nothing (S6), so no verdict is returned at all: the comment falls through to
// the pending branch and fails closed.
func approveVerdict(id int64, login string, lines []string, at string, rs *ReviewerSet, currentHead string) (Verdict, bool) {
	for _, l := range lines {
		fields, ok := dispositionWholeLine(l)
		if !ok {
			continue
		}
		if !strings.EqualFold(fields["verdict"], "APPROVE") {
			continue
		}
		resolvedWho, _ := rs.ResolveWho(login, fields["who"])
		if resolvedWho == "" || resolvedWho == "unknown" {
			continue
		}
		head := strings.ToLower(strings.TrimSpace(fields["head"]))
		if head == "" {
			head = currentHead
		}
		return Verdict{
			ID:     fmt.Sprintf("comment:%d", id),
			Who:    resolvedWho,
			Word:   "approve",
			Head:   head,
			At:     at,
			Source: "comment-rule",
			Scope:  fields["scope"],
			RawID:  id,
			Conf:   "-",
			Kind:   "line",
		}, true
	}
	return Verdict{}, false
}

// ParseReview converts a GitHub pull request review into a Verdict.
func ParseReview(id int64, login, rawBody, state, commitID, submittedAt string, rs *ReviewerSet, author, currentHead string, ignoreUntyped bool) (Verdict, bool) {
	if rs != nil && !rs.IsScanned(login) {
		return Verdict{Foreign: true}, false
	}
	if len(rawBody) > MaxCommentBodyBytes {
		rawBody = strings.Clone(rawBody[:MaxCommentBodyBytes])
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
		v, ok := ParseComment(id, login, rawBody, submittedAt, rs, author, currentHead, ignoreUntyped)
		if ok {
			v.ID = fmt.Sprintf("review:%d", id)
			if v.Word == "hold" && v.Source == "comment-rule" {
				v.Source = "review"
			}
		}
		return v, ok
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
	return releaseSameFriendSupersededHolds(UnreleasedHolds(holds, reads, currentHead, author, rs), vs, currentHead)
}

// releaseSameFriendSupersededHolds is nova-tools #2550's rule (a HOLD at a SUPERSEDED
// head is released by a later typed verdict from the same friend at the CURRENT head),
// amended by the coordinator's 2026-09-22 4:55 PM decision to cover a HOLD still AT the
// current head too:
//
//	A HOLD by friend X at head H, however it was written, is released by X's LATER typed
//	`DISPOSITION who=x head=H verdict=APPROVE` comment at that same head H; the last
//	typed verdict per friend at head wins, whether H is superseded or current.
//
// The reasoning given for lifting SPEC-DECIDE reading 3's older "a comment releases
// nothing at the current head; only the holder's lane record does" restriction: Glenn's
// one-read rule is about typed lines, and a friend's own typed DISPOSITION comment IS
// their lane record for their own hold. It never was evidence good enough for a
// DIFFERENT friend's hold or for the needs_read approval gate (read.go's EvaluateReads,
// which remains lane-record-only and is untouched here) -- only for superseding one's
// own earlier word with one's own later word, the same friend, the same head.
//
// Measured control: #2522 comment 5766104067, Stella's untyped prose hold ("HOLD --
// Stella, independent contract/source read..."), binds to the current head because an
// untyped comment always does (SPEC-DECIDE lines 1037-1040); her own later typed
// `DISPOSITION who=stella head=<that same head> verdict=APPROVE score=9` comment
// (5783400393) now releases it. Emma's typed APPROVE at that same head does not: the
// rule is same-friend, never "somebody approved".
//
// What this does NOT do, and must not:
//
//   - An APPROVE at some OTHER, non-matching head releases nothing. Release keys on the
//     head, never on "somebody approved at some point".
//   - A hold with who=unknown is left alone. An untyped hold-shaped line with no
//     attributable name has no author to match, so nothing can supersede it this way;
//     SPEC-DECIDE reading 3's other release path (a different may-hold reader's lane
//     record naming it) is unaffected.
//   - The needs_read approval gate (read.go) still counts only lane records. This
//     decision is about a hold's own author superseding themselves, not about who counts
//     as a second friend's read.
func releaseSameFriendSupersededHolds(unreleased []Verdict, vs []Verdict, currentHead string) []Verdict {
	var kept []Verdict
	for _, h := range unreleased {
		if h.Who == "unknown" || h.Who == "" {
			kept = append(kept, h)
			continue
		}
		if !supersededByVerdictAtHead(h, vs, currentHead) {
			kept = append(kept, h)
		}
	}
	return kept
}

func supersededByVerdictAtHead(h Verdict, vs []Verdict, currentHead string) bool {
	for _, v := range vs {
		if v.Kind != "" && v.Kind != "line" {
			continue
		}
		if v.Word != "approve" && v.Word != "hold" {
			continue
		}
		// Only a TYPED word counts as the friend's own lane record: Source "record" (a
		// `nova-merge read`) or "comment-rule" (a typed DISPOSITION line, ParseComment's
		// only way to produce Word "approve" or a named "hold"). A forge-native review
		// click never reaches here as an approve at all -- ParseReview drops an APPROVED
		// review outright (SPEC-DECIDE reading 3) -- but excluding every other Source
		// here too means a verdict shaped like one, real or synthetic, still supersedes
		// nothing (TestAForgeApprovedReviewReleasesNothing, TestACommentNeverReleasesAnything).
		if v.Source != "record" && v.Source != "comment-rule" {
			continue
		}
		if !sameLine(v.Who, h.Who) {
			continue
		}
		if !headMatch(v.Head, currentHead) {
			continue
		}
		// The tie rule of UnreleasedHolds: a verdict stamped at or before the hold is
		// not later than it, and a verdict with no stamp is not evidence of order.
		if v.At == "" || v.At <= h.At {
			continue
		}
		if v.Word == "approve" {
			// A scoped APPROVE releases only what it names, the same way it does at head.
			if v.Scope != "" && !releasesContains(v.Releases, h.ID) {
				continue
			}
		} else {
			// A later HOLD replaces the held one only when it is the same topic: a
			// friend's new hold on "docs" is not their last word on an unrelated,
			// still-standing "parser" hold (TestAScopedApproveReleasesOnlyTheHoldsItNames).
			if v.Scope != h.Scope {
				continue
			}
		}
		return true
	}
	return false
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
				if len(rec.Releases) > 0 {
					// When explicit release IDs are supplied, only those exact IDs are released.
					// Do not release all of the author's holds.
					if releasesContains(rec.Releases, h.ID) {
						released = true
						break
					}
					continue
				}
				if rec.Scope == "" {
					// Unscoped APPROVE (without explicit release IDs) releases every hold of that who
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

// holdNamePrefix reads a friend's name off an untyped HOLD's leading prose, exactly as the
// bash lander already did: a name first ("Stella: HOLD ...") or HOLD first, then a name
// ("HOLD -- Stella, ..."), for the fixed set of reviewers the lander recognised this way.
var holdNamePrefix = regexp.MustCompile(`(?i)^(?:hold[^a-z]*)?(emma|stella|johnny|glenn)\b`)

// holdVerdictLine, holdParenLine and holdReviewLine are the remaining recognised line
// shapes nova-tools #2631 named as a HOLD without a typed DISPOSITION line: `Verdict:
// HOLD`, `(HOLD)`, and `<name> review: HOLD`. `HOLD ...` itself (including `HOLD #n` and
// `HOLD at <sha>`) is matched directly in isHoldShapedLine by its first token.
var (
	holdVerdictLine = regexp.MustCompile(`(?i)^verdict:\s*\**\s*hold\b`)
	holdParenLine   = regexp.MustCompile(`(?i)\(\s*hold\s*\)`)
	holdReviewLine  = regexp.MustCompile(`(?i)^(?:emma|stella|johnny|glenn)\s+review:\s*\**\s*hold\b`)
)

// isHoldShapedLine reports whether a single line, taken on its own, is one of the
// recognised untyped HOLD shapes (nova-tools #2631): `HOLD ...` as the line's own first
// word (which also covers `HOLD #n`, `HOLD at <sha>`, and `HOLD -- Name, ...`), `Verdict:
// HOLD`, `(HOLD)`, a standalone bold/underscored `**HOLD:**` marker, or `<name> review:
// HOLD`. ParseComment checks every line, not only the first, so a standalone HOLD line may
// still follow a leading DISPOSITION/NOTE line -- but the word HOLD used AS PROSE inside a
// longer line, such as a bullet reading "Johnny's HOLD Conclusively Satisfied", is never a
// verdict: read on its own, that line's first token is "Johnny's", not HOLD, its one bold
// span carries three other words besides HOLD, and it matches none of the other shapes
// either. Reading it as a hold dropped APPROVE comments whose body happened to praise a
// HOLD by name (#2587 comment 5782779847, #2616 comment 5782724650) -- the distinction from
// a genuine `**HOLD:**` marker is exactly that the marker's bold span names nothing else.
func isHoldShapedLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.EqualFold(firstToken(strings.TrimLeft(line, "# ")), "HOLD") {
		return true
	}
	if holdVerdictLine.MatchString(line) || holdParenLine.MatchString(line) || holdReviewLine.MatchString(line) {
		return true
	}
	return hasStandaloneBoldHold(line)
}

// hasStandaloneBoldHold reports whether line contains a bold (`**...**`) or underscored
// (`__..__`) span whose content is the word HOLD alone (plus trailing punctuation such as
// a colon) and nothing else -- "Please note: **HOLD:** we need clarification" -- as
// distinct from a bold span that merely mentions HOLD among other words, such as
// "**Johnny's HOLD Conclusively Satisfied**", which is prose about a hold, not a marker.
func hasStandaloneBoldHold(line string) bool {
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
			inner := strings.Fields(line[start : start+end])
			if len(inner) == 1 && strings.EqualFold(strings.Trim(inner[0], ".,:;!?()[]`"), "HOLD") {
				return true
			}
			idx = start + end + len(marker)
		}
	}
	return false
}

// deriveHoldWho reads the friend's name off an untyped HOLD's own line via holdNamePrefix.
// A hold with a name is that friend's hold even when nobody typed a DISPOSITION line
// (nova-tools #2615 follow-up; #2522 comment 5766104067, "HOLD -- Stella, independent
// contract/source read..."); it returns "unknown" -- same as before this fix -- when the
// line names none of the fixed set of reviewers.
func deriveHoldWho(line string) string {
	m := holdNamePrefix.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "unknown"
	}
	return normWho(m[1])
}

// MaxCommentBodyBytes is the upper bound on a single comment or review body, in bytes.
// Matches execOutputCap and ChildCap (64 KiB): remote comment bodies are clamped to this
// ceiling during projection and decoding so that oversized payloads cannot cause
// unbounded heap allocation.
const MaxCommentBodyBytes = 64 * 1024

// boundedBody clamps remote payload strings to MaxCommentBodyBytes upon unmarshaling.
type boundedBody string

func (b *boundedBody) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if len(s) > MaxCommentBodyBytes {
		s = strings.Clone(s[:MaxCommentBodyBytes])
	}
	*b = boundedBody(s)
	return nil
}

// ParseForgeVerdicts reads GitHub API comments and reviews and decodes them via ParseComment and ParseReview.
func ParseForgeVerdicts(comments, reviews string, n int, rs *ReviewerSet, author, currentHead string, ignoreUntyped bool) ([]Verdict, error) {
	var out []Verdict
	var rawComments []struct {
		ID        int64                  `json:"id"`
		User      struct{ Login string } `json:"user"`
		Body      boundedBody            `json:"body"`
		CreatedAt string                 `json:"created_at"`
	}
	if err := decodeArrays(comments, &rawComments); err != nil {
		return nil, fmt.Errorf("pull request %d's comments did not answer JSON this tool can read: %w", n, err)
	}
	for _, c := range rawComments {
		if v, ok := ParseComment(c.ID, c.User.Login, string(c.Body), c.CreatedAt, rs, author, currentHead, ignoreUntyped); ok {
			out = append(out, v)
		}
	}

	var rawReviews []struct {
		ID          int64                  `json:"id"`
		User        struct{ Login string } `json:"user"`
		Body        boundedBody            `json:"body"`
		State       string                 `json:"state"`
		SubmittedAt string                 `json:"submitted_at"`
		CommitID    string                 `json:"commit_id"`
	}
	if err := decodeArrays(reviews, &rawReviews); err != nil {
		return nil, fmt.Errorf("pull request %d's reviews did not answer JSON this tool can read: %w", n, err)
	}
	for _, r := range rawReviews {
		if v, ok := ParseReview(r.ID, r.User.Login, string(r.Body), r.State, r.CommitID, r.SubmittedAt, rs, author, currentHead, ignoreUntyped); ok {
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
