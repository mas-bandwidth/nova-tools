RESULT tools22-rule-toolwork-2-L798 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 2 says?
CONFORMS internal/merge/reviewers.go:180
SPEC docs/SPEC-TOOLWORK.md:798 rule 2
PKG internal/merge
ASK The code must resolve `who` from the typed `DISPOSITION who=` line through the reviewer file's `who`/`logins`/`may-hold` mapping, default to `unknown` when no typed line or no mapping exists; treat `who=unknown` holds as valid holds that are never dropped; scan all comments from logins in the reviewer file (the author's login excuses nothing); and skip only the entry author's own `verdict=NOTE` line.

Deciding lines:
- `internal/merge/reviewers.go:180` (`ResolveWho`): maps login+typedWho through reviewer file, returning `"unknown"` when no match
- `internal/merge/verdict.go:153` (`ParseComment` calls `ResolveWho` to set `Who` for typed HOLD)
- `internal/merge/verdict.go:186-197` (untyped holds → `who="unknown"`)
- `internal/merge/verdict.go:200-217` (pending comments → `who="unknown"`)
- `internal/merge/verdict.go:321-328` (`UnreleasedHolds`: absent holder becomes `who=unknown`, not dropped; `who=unknown` hold persists)
- `internal/merge/verdict.go:141-147` (`ParseComment`: foreign login skipped via `IsScanned`; author's `verdict=NOTE` line skipped via `IsAuthorNote`)
- `internal/merge/verdict.go:116-137` (`IsAuthorNote`): only `DISPOSITION who=<author> verdict=NOTE` is recognized as skip; author login does not exempt other comments)

GUARDED-BY internal/merge/verdict_test.go:63 TestParseCommentHoldRule (typed HOLD with `who=` resolution through reviewer file)
GUARDED-BY internal/merge/verdict_test.go:240 TestUnliftedHoldsTreatsHolderAbsentFromReviewersFileAsUnknownHold (absent holder → who=unknown, not dropped)
GUARDED-BY internal/merge/verdict_test.go:293 TestAuthorLoginExcusesNothingOnSharedLoginNote (author login does not skip; shared login with `verdict=NOTE` not treated as author note)
GUARDED-BY cmd/nova-merge/hold_test.go:733 TestTheAuthorsNoteLineIsNotScannedAndTheAuthorsLoginSkipsNothing (author's own NOTE line skipped; author's HOLD not skipped)
GUARDED-BY cmd/nova-merge/hold_test.go:717 TestAnUntypedHoldFromTheSharedLoginFailsClosed (untyped bold hold → who=unknown)

Grep patterns run: `who=`, `may-hold`, `MayHold`, `Disposition.*who`, `DISPOSITION`, `ResolveWho`, `IsAuthorNote`, `IsScanned`, `UnreleasedHolds` in `internal/merge/` and `cmd/nova-merge/`

Left owed: none
`git status --short` (from repo):