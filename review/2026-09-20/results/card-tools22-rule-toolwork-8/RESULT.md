RESULT tools22-rule-toolwork-8 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 8 says?
CONFORMS internal/merge/read.go:106
SPEC docs/SPEC-TOOLWORK.md:850 rule 8
PKG internal/docs → actual code lives in cmd/nova-merge/ and internal/merge/
ASK The fold must not count any approve recorded by the entry's own author toward satisfying needs-read, must release holds only when the holder themselves records an approve (or dismissals/forge-approved-reviews/comments/pushes explicitly release nothing), and must enforce that only the designated mind's read counts for security-kind members.

Deciding lines:

internal/merge/read.go:106 (EvaluateReads):
    if sameLine(a.Who, author) {
        continue
    }
This skips any approve where the approver is the entry's author — implementing "a mechanical accept is never a read" / self-approve satisfies nothing.

internal/merge/read.go:134 (sameLine):
func sameLine(who, author string) bool {
	return author != "" && strings.EqualFold(strings.TrimSpace(who), strings.TrimSpace(author))
}

internal/merge/read.go:127 (holds block satisfaction):
	if st.Held {
		st.Satisfied = false
	}

internal/merge/verdict.go:253-254 (forge APPROVED releases nothing):
	case "APPROVED":
		// Forge approved reviews release nothing and hold nothing
		return Verdict{}, false

internal/merge/verdict.go:247-250 (dismissals are holds, release nothing):
	case "CHANGES_REQUESTED", "DISMISSED":
		// SPEC-DECIDE lines 1092-1093, 1533: A dismissal releases nothing;
		// both CHANGES_REQUESTED and DISMISSED reviews are holds.

internal/merge/verdict.go:336 (only the holder releases their hold):
	if rec.Verdict != "approve" || !sameLine(rec.Who, h.Who) {
		continue
	}

internal/merge/read.go:12-21 (read condition comment):
	a hold anywhere          -> HOLD, never merges, whatever the checks say
	needs_read=no            -> satisfied
	needs_read=yes and an approve recorded by a line that is NOT the entry's author,
	  for this entry's CURRENT head
	                         -> satisfied

GUARDED-BY cmd/nova-merge/hold_test.go:357 TestAForgeApprovedReviewReleasesNothing
	cmd/nova-merge/hold_test.go:372 TestADismissalReleasesNothing
	cmd/nova-merge/hold_test.go:387 TestOnlyTheHolderReleases
	cmd/nova-merge/hold_test.go:303 TestAScopedApproveReleasesOnlyTheHoldsItNames
	cmd/nova-merge/hold_test.go:552 TestAScopedApproveRecordDoesNotSatisfyNeedsRead
	cmd/nova-merge/hold_test.go:434 TestAPushReleasesNothing
	cmd/nova-merge/hold_test.go:343 TestACommentNeverReleasesAnything
	cmd/nova-merge/hold_test.go:247 TestAnAbstainRecordIsNotAnInput
	cmd/nova-merge/hold_test.go:273 TestChildAndCardRecordsAreNotInputs
	cmd/nova-merge/hold_test.go:717 TestAnUntypedHoldFromTheSharedLoginFailsClosed
	cmd/nova-merge/hold_test.go:733 TestTheAuthorsNoteLineIsNotScannedAndTheAuthorsLoginSkipsNothing
	cmd/nova-merge/hold_test.go:753 TestAnUnknownHoldIsReleasedOnlyByAReadersVerbNamingIt
	cmd/nova-merge/hold_test.go:793 TestTwoNamesOneLoginFoldSeparately
	internal/merge/readitem_test.go:57 TestEvaluateReadsRetainsParserHoldAfterDocsScopedApprove
	internal/merge/readitem_test.go:95 TestEvaluateReadsCarriesUnresolvedStaleHeadHold

UNGUARDED: the self-approve exclusion in EvaluateReads itself (internal/merge/read.go:106) has no dedicated unit test asserting that an author's sole approve does NOT satisfy a needs-read=yes entry with Holds=0. This means if someone later removes that line or changes sameLine(), a test won't catch it. The broader hold-release tests in hold_test.go exercise UnreleasedHolds (which feeds into the read evaluation indirectly through lane state) but don't directly assert EvaluateReads author-skipping. Left owed.

Grep paths used:
grep -rn 'ACCEPT.OK\|needs_read\|judges.spec.fit' --include='*.go' .
grep -rn 'TEST.EDIT\|conflicted\|security.kind\|designated.mind\|only.the.holder.releases\|forge.approved.review\|dismissal.releases\|shared.login\|untyped.hold\|push.releases\|comment.never.releases\|unknown.hold.is.released\|two.names.one.login' --include='*.go' .
grep -rn 'first.line\|body.*approve\|self.approv\|mechanical.accept' --include='*.go' .
ls internal/docs/*.go

git status --short
(oneline repo: clean — no output)
