package merge

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Verdict is one piece of evidence regarding a hold or approval.
// It can come from (a) a lane read record, (b) a forge review, or (c) a forge comment.
type Verdict struct {
	ID       string   // e.g. "record:2026-09-19T02:34:25Z", "comment:12345", "review:67890"
	Who      string   // resolved name or "unknown"
	Word     string   // "hold", "approve", "abstain", "pending", "note"
	Head     string   // commit SHA it binds to
	At       string   // ISO 8601 timestamp (created_at / submitted_at)
	Source   string   // "record", "review", "comment-rule", "comment-decided", "comment-pending", "comment-truncated"
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

// ParseDispositionLine is the lenient view of a typed DISPOSITION line,
// DISPOSITION who=<name> head=<sha> verdict=HOLD [scope="<text>"]. The one
// parser is typedrec.ParseDisposition (#2506 part B); this keeps the
// (who, head, verdict, scope, ok) shape for callers that only want those.
func ParseDispositionLine(line string) (who, head, verdict, scope string, ok bool) {
	c, ok := typedrec.ParseDisposition(line)
	if !ok {
		return "", "", "", "", false
	}
	return c.Who, c.Head, c.Verdict, c.Scope, true
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
	// nova-tools #3443: verdict lines are read over the WHOLE body. A body this tool
	// cannot read whole is refused as a hold that names the clamp, never read clear.
	if len(rawBody) > MaxParseBodyBytes {
		return truncatedBodyHold(fmt.Sprintf("comment:%d", id), id, at, currentHead), true
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
	// that is, on its own, one of the recognised shapes is. Every line is checked, not
	// only the first, because a genuine standalone `**HOLD: ...**` line can follow a
	// leading DISPOSITION/NOTE line (TestAuthorLoginExcusesNothingOnSharedLoginNote) --
	// but "Johnny's HOLD Conclusively Satisfied" is a bullet ABOUT a hold, not a line
	// whose own shape is HOLD, so it never matches isHoldShapedLine on any line by itself.
	//
	// Independent HOLD evidence takes precedence over a typed or explicit APPROVE in the same
	// comment, preserving unresolved HOLD evidence even in mixed-message comments (#2454).
	holdLine, holdIdx := "", -1
	for i, l := range lines {
		if isHoldShapedLine(l) {
			holdLine, holdIdx = strings.TrimSpace(l), i
			break
		}
	}
	if holdLine != "" {
		// SPEC-DECIDE lines 1037-1040: untyped comment binds to current head
		head := currentHead
		who := attributeUntypedHold(lines, holdIdx)
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
//   - the DISPOSITION must be the WHOLE line (typedrec.ParseDisposition's Whole), so a release cannot
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
		c, ok := typedrec.ParseDisposition(l)
		if !ok || !c.Whole || c.Verdict != "APPROVE" {
			continue
		}
		// #2506 part B: a refused claim approves nothing. An APPROVE with no
		// head= is refused as field=head defect=missing; it no longer binds to
		// the current head.
		if !c.Valid {
			continue
		}
		resolvedWho, _ := rs.ResolveWho(login, c.Who)
		if resolvedWho == "" || resolvedWho == "unknown" {
			continue
		}
		head := strings.TrimSpace(c.Head)
		return Verdict{
			ID:     fmt.Sprintf("comment:%d", id),
			Who:    resolvedWho,
			Word:   "approve",
			Head:   head,
			At:     at,
			Source: "comment-rule",
			Scope:  c.Scope,
			RawID:  id,
			Conf:   "-",
			Kind:   "line",
		}, true
	}
	return Verdict{}, false
}

// ParseReview converts a GitHub pull request review into a Verdict.
//
// ignoreUntyped is the same --untyped-comments=ignore switch ParseComment honours: a
// prose COMMENTED review from a login that may hold is dropped instead of becoming a
// pending verdict (before this, reviews always passed false, so the switch never reached
// them and the only release was a lane-record APPROVE naming the review).
//
// A typed `DISPOSITION who=<name> head=<sha> verdict=APPROVE` whole line in a COMMENTED
// or APPROVED review is read exactly as the same line in a comment: a typed approve
// (Source "comment-rule") that counts as that friend's read for their own holds. A forge
// APPROVED click with no typed line still releases and holds nothing.
//
// Every verdict read off a review carries the id review:<id>, never comment:<id>; a lane
// record's --releases may name it either way (releasesContains accepts the older
// comment:<id> form for a review hold). Holds are keyed by who=, not by the surface the
// line arrived on (owner ruling 2026-09-23 7:05 PM ET; #3278): the holder's own typed
// release clears the hold whether it came as a comment or a review.
func ParseReview(id int64, login, rawBody, state, commitID, submittedAt string, rs *ReviewerSet, author, currentHead string, ignoreUntyped bool) (Verdict, bool) {
	if rs != nil && !rs.IsScanned(login) {
		return Verdict{Foreign: true}, false
	}
	// nova-tools #3443: an over-cap body holds whatever the review's state, since
	// an APPROVE read off a prefix would release a HOLD this tool never saw.
	if len(rawBody) > MaxParseBodyBytes {
		head := strings.ToLower(strings.TrimSpace(commitID))
		if head == "" {
			head = currentHead
		}
		return truncatedBodyHold(fmt.Sprintf("review:%d", id), id, submittedAt, head), true
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
		// A forge approved click releases nothing and holds nothing; a typed APPROVE line
		// in its body is a typed read, the same as in a COMMENTED review.
		head := strings.ToLower(strings.TrimSpace(commitID))
		if head == "" {
			head = currentHead
		}
		if v, ok := approveVerdict(id, login, lines, submittedAt, rs, head); ok {
			return asReviewVerdict(v, id), true
		}
		return Verdict{}, false
	default:
		// COMMENTED review: inspect body like comment, honouring --untyped-comments.
		v, ok := ParseComment(id, login, rawBody, submittedAt, rs, author, currentHead, ignoreUntyped)
		if !ok {
			return v, ok
		}
		v = asReviewVerdict(v, id)
		if v.Word == "hold" && v.Source == "comment-rule" {
			v.Source = "review"
		}
		return v, true
	}
}

// asReviewVerdict re-keys a verdict read off a review's body to review:<id>.
func asReviewVerdict(v Verdict, id int64) Verdict {
	v.ID = fmt.Sprintf("review:%d", id)
	return v
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
	return releaseSameFriendSupersededHolds(UnreleasedHolds(holds, reads, currentHead, author, rs), vs)
}

// releaseSameFriendSupersededHolds is nova-tools #2550's rule (a HOLD is released by a
// later typed verdict from the same friend), as amended twice:
//
//   - the coordinator's 2026-09-22 4:55 PM decision: it covers a HOLD still AT the
//     current head too, not only one at a superseded head;
//   - Glenn's lander-keys-reads-by-who ruling (2026-09-23): the releasing verdict may be
//     at ANY head. A hold by X is released when X's last typed verdict written after
//     the hold is APPROVE, whichever head that APPROVE names.
//
// The 2026-09-23 amendment is measured: the gate dropped 57 distinct pull requests with
// "carries an unreleased HOLD", and five (#2619 johnny, #2628 stella, #2707 rowan, #2879
// rowan, #3080 johnny) were holds whose author's own later typed APPROVE sat at a head
// the branch had since moved past. #2879: rowan HOLD 6 at fc15f98d, rowan APPROVE 8 at
// fc15f98d 30 minutes later, head now 8984b941 -- the hold pinned because the APPROVE was
// not at the current head (TestAHoldIsReleasedByTheHoldersLaterApproveAtAnOlderHead).
// The APPROVE is the holder's own last word on their own hold; the head it names says
// what they read, not whether they still hold.
//
// The reasoning for letting a typed comment release at all: Glenn's one-read rule is
// about typed lines, and a friend's own typed DISPOSITION comment IS their lane record
// for their own hold. It never was evidence good enough for a DIFFERENT friend's hold or
// for the needs_read approval gate (read.go's EvaluateReads, which remains
// lane-record-only and is untouched here) -- only for superseding one's own earlier word
// with one's own later word.
//
// What this does NOT do, and must not:
//
//   - A DIFFERENT friend's APPROVE releases nothing, at any head.
//   - A verdict with no stamp is no evidence of order and releases nothing.
//   - A scoped APPROVE releases only the holds it names; a later HOLD replaces an
//     earlier one only on the same scope. The later HOLD itself stands on its own, so
//     a friend whose last word is HOLD still pins (TestAHoldAfterTheHoldersOlderHeadApproveStillPins).
//   - A hold with who=unknown is left alone. An untyped hold-shaped line with no
//     attributable name has no author to match, so nothing can supersede it this way;
//     SPEC-DECIDE reading 3's other release path (a different may-hold reader's lane
//     record naming it) is unaffected.
//   - The needs_read approval gate (read.go) still counts only lane records, at head.
func releaseSameFriendSupersededHolds(unreleased []Verdict, vs []Verdict) []Verdict {
	var kept []Verdict
	for _, h := range unreleased {
		if h.Who == "unknown" || h.Who == "" {
			kept = append(kept, h)
			continue
		}
		if !supersededByHoldersLaterVerdict(h, vs) {
			kept = append(kept, h)
		}
	}
	return kept
}

// supersededByHoldersLaterVerdict reports whether the holder of h wrote a later typed
// verdict, at any head, that speaks to h: an APPROVE (unscoped, or scoped and naming
// h), or a HOLD on the same scope, which replaces h and stands in its own right.
func supersededByHoldersLaterVerdict(h Verdict, vs []Verdict) bool {
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
		// No head check: the ruling is ANY head. The tie rule of UnreleasedHolds
		// stays: a verdict stamped at or before the hold is not later than it, and a
		// verdict with no stamp is not evidence of order.
		if v.At == "" || v.At <= h.At {
			continue
		}
		if v.Word == "approve" {
			// A scoped APPROVE releases only what it names.
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

// releasesContains reports whether a --releases list names the hold id. A hold read off
// a review (review:<id>) is also named by the older comment:<id> form, which nova-merge
// minted for reviews before they carried their own prefix.
func releasesContains(releases []string, id string) bool {
	id = strings.TrimSpace(id)
	legacy := ""
	if len(id) > len("review:") && strings.EqualFold(id[:len("review:")], "review:") {
		legacy = "comment:" + id[len("review:"):]
	}
	for _, r := range releases {
		r = strings.TrimSpace(r)
		if strings.EqualFold(r, id) || (legacy != "" && strings.EqualFold(r, legacy)) {
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

// MaxCommentBodyBytes is the upper bound on a comment or review body that is STORED
// or PRINTED, in bytes. Matches execOutputCap and ChildCap (64 KiB). It is never the
// bound on what is PARSED for verdict lines: that is MaxParseBodyBytes (nova-tools
// #3443; #2512 parsed a 64 KiB prefix as the whole body, so a HOLD past it read clear).
const MaxCommentBodyBytes = 64 * 1024

// MaxParseBodyBytes is the hard cap on a comment or review body read for verdict
// lines: 1 MiB, far above GitHub's 65,536-character body limit (at most 256 KiB of
// UTF-8). Verdict lines are parsed over the whole body up to this cap; a body beyond
// it is refused as a hold (truncatedBodyHold), never read as clear.
const MaxParseBodyBytes = 1 << 20

// boundedBody bounds a remote payload string upon unmarshaling to MaxParseBodyBytes+1
// bytes: memory stays bounded, and a body that was cut is still longer than
// MaxParseBodyBytes, so ParseComment and ParseReview see it was not read whole and
// refuse it (nova-tools #3443).
type boundedBody string

func (b *boundedBody) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if len(s) > MaxParseBodyBytes+1 {
		s = strings.Clone(s[:MaxParseBodyBytes+1])
	}
	*b = boundedBody(s)
	return nil
}

// truncatedBodyHold is the fail-closed verdict for a body over MaxParseBodyBytes
// (nova-tools #3443). No evidence is not negative evidence: a body this tool could not
// read whole is not a body with no HOLD. It is a hold with who=unknown (no line was
// read, so nobody's own later word can supersede it) and source=comment-truncated,
// which the LAND REFUSED and BATCH DROP lines print, and --untyped-comments=ignore
// does not drop it, since it is not an untyped comment but an unread one.
func truncatedBodyHold(verdictID string, rawID int64, at, head string) Verdict {
	return Verdict{
		ID:     verdictID,
		Who:    "unknown",
		Word:   "hold",
		Head:   head,
		At:     at,
		Source: "comment-truncated",
		RawID:  rawID,
		Conf:   "-",
		Kind:   "line",
	}
}

// friendNameRE is the fixed set of friend names, case-insensitive, whole words.
var friendNameRE = regexp.MustCompile(`(?i)\b(emma|stella|johnny|glenn)\b`)

// whoAnchorBefore is an identity slot immediately before a friend name:
// who=<name>, optional space around =, optional quote. A bare mention is not one.
var whoAnchorBefore = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])who\s*=\s*["']?$`)

// holdPinTokenRE removes sha=/head= pins so the header can be asked whether
// anything but a pin remains.
var holdPinTokenRE = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])(?:sha|head)="?[0-9a-fA-F]{7,40}"?`)

// attributeUntypedHold is deriveHoldWho, plus the nova-tools #2710 header rule
// for "HOLD sha=<hex>" where the friend's name is not adjacent to HOLD (#2713's
// 43f1df17, recut). The header belongs to the HOLD line ParseComment located
// (lines[holdIdx]), not to the body's first line: a standalone HOLD line may
// follow a leading NOTE/prose line, and it is read on its own (Stella, #3388
// comment 5804827993). The header is the rest of that line after HOLD; if the
// rest is only pins and punctuation, it extends to the next non-blank line.
// A single anchored identity attributes: who=<name> as a bounded key=value
// field, or <Name>: at the very start of the post-pin header. A free-form
// mention does not -- "Stella delta read clears the original defect" and "I
// asked Stella: please verify this" both stay unknown -- because under the
// #3278 ruling the holder's own later typed APPROVE releases the hold at any
// head, so a mention read as the writer would let the mentioned friend release
// somebody else's HOLD on a shared login. Two names, or none, stay unknown, and
// an unattributed HOLD stays held. A name deriveHoldWho already reads
// ("HOLD -- Stella", "Stella: HOLD") is kept. A sha= pin only shapes the
// header here; it does not move the head the hold binds to.
func attributeUntypedHold(lines []string, holdIdx int) string {
	if holdIdx < 0 || holdIdx >= len(lines) {
		return "unknown"
	}
	holdLine := strings.TrimSpace(lines[holdIdx])
	if who := deriveHoldWho(holdLine); who != "unknown" {
		return who
	}
	if !strings.EqualFold(firstToken(holdLine), "HOLD") {
		return "unknown"
	}
	header := stripHoldPins(stripLeadingHoldWord(holdLine))
	if headerIsPunctuationOnly(header) {
		if next := nextNonBlankLine(lines, holdIdx+1); next != "" {
			header = header + " " + stripHoldPins(next)
		}
	}
	return anchoredFriendWho(header)
}

// nextNonBlankLine is the first non-blank line at or after lines[from], trimmed.
func nextNonBlankLine(lines []string, from int) string {
	for i := from; i < len(lines); i++ {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func stripLeadingHoldWord(line string) string {
	line = strings.TrimSpace(line)
	if len(line) >= 4 && strings.EqualFold(line[:4], "HOLD") {
		rest := line[4:]
		if rest == "" {
			return rest
		}
		r := rest[0]
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return rest
		}
	}
	return line
}

// stripHoldPins removes sha=/head= pins, keeping a space where each stood.
func stripHoldPins(s string) string {
	return holdPinTokenRE.ReplaceAllString(" "+s, " ")
}

func headerIsPunctuationOnly(s string) bool {
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}

func isASCIILetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// anchoredFriendWho is the one friend the post-pin header names as an
// identity. who=<name> anywhere as a bounded field counts; <Name>: counts only
// as the header's first word. A name with neither, or a second friend's name
// beside the first, does not: a mention is not a signature.
func anchoredFriendWho(header string) string {
	header = strings.TrimLeftFunc(header, func(r rune) bool { return !isASCIILetter(r) })
	idxs := friendNameRE.FindAllStringSubmatchIndex(header, -1)
	if len(idxs) == 0 {
		return "unknown"
	}
	who := ""
	anchored := false
	for _, m := range idxs {
		n := normWho(header[m[2]:m[3]])
		if n == "" {
			continue
		}
		if who == "" {
			who = n
		} else if who != n {
			return "unknown"
		}
		if friendNameIsAnchored(header, m[2], m[3]) {
			anchored = true
		}
	}
	if who == "" || !anchored {
		return "unknown"
	}
	return who
}

// friendNameIsAnchored: header[start:end] is a friend name. It is an identity
// when it is the value of a who= field, or when it is the header's first word
// and a colon follows it (bold/underscore markers between are allowed).
func friendNameIsAnchored(header string, start, end int) bool {
	if start < 0 || end > len(header) || start > end {
		return false
	}
	if whoAnchorBefore.MatchString(header[:start]) {
		return true
	}
	if start != 0 {
		return false
	}
	rest := strings.TrimLeft(header[end:], " \t*_`")
	return strings.HasPrefix(rest, ":")
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
