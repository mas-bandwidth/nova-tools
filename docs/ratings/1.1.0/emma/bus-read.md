# nova-bus READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7.5/10
README: 8/10

## Reasons
The tool provides a thoughtful and disciplined communication mechanism over git. It avoids central servers, keeps human-readable markdown notes, and ensures cursors and open backlogs remain manageable across distributed participants. The documentation explains the mental model clearly, and the tool delivers strong protections against lost notes and race conditions across concurrent sessions.

A score of 10 would require breaking down monolithic functions, eliminating 13-parameter calls in favor of option structs, standardizing exit codes for refusals, cleaning up retired flag declarations, and removing historical incident narratives from documentation and comments.

The first place of confusion was docs/CLI.md:462, where the first run walkthrough requires provoking an unancestral cursor refusal on copied example fixtures before running a full scan, rather than providing a clean initial walkthrough.
The first place of boredom was cmd/nova-bus/main.go:1618, where inboxListing spans 467 lines mixing lock acquisition, cursor checking, legacy cutoff parsing, bounded walks, and presentation formatting.
The first place of doubting a claim was cmd/nova-bus/main.go:14, where the banner claims no rejected push ever reaches a caller, yet bounded retries stop at 25 attempts and surface push failures when branches encounter heavy concurrent contention.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/main.go:1618 | inboxListing is a 467-line monolithic function handling diverse responsibilities from locking to formatting | decompose into dedicated helpers for cursor checks, legacy evaluation, and output generation | L |
| 2 | cmd/nova-bus/main.go:2171 | advanceCursorTo requires 13 positional arguments leading to fragile call sites | encapsulate parameters into an advanceOpts struct | M |
| 3 | cmd/nova-bus/main.go:1659 | inboxListing outputs INBOX REFUSED for invalid cursors but exits with code 1 instead of standard code 2 | harmonize refusal exit code to 2 or clarify domain rejection versus invocation failure | M |
| 4 | cmd/nova-bus/main.go:2639 | retired flags beat and beat-lease remain declared in flag sets rather than being removed | remove retired flag registrations and reject them through standard unknown flag handling | S |
| 5 | cmd/nova-bus/draft.go:40 | retired flag file is still registered on draft command rather than being removed | drop retired file flag registration on draft | S |
| 6 | cmd/nova-bus/main.go:14 | banner claims no rejected push reaches a caller despite retries being bounded by attempts flag | qualify claim to state bounded retries mitigate contention up to the attempt limit | S |
| 7 | docs/CLI.md:622 | command reference contains historical incident narratives and ticket numbers | remove historical bug anecdotes and describe current single-receipt-per-lane design | S |

## Good, keep
The cursor-based change detection guarantees read costs scale with new activity rather than total history size.
Unambiguous note identification and lane-based directory layout make the repository structure easy to navigate and inspect.
Strict roster validation and automated preflight checks prevent malformed note headers and recipient mistakes before commits occur.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 467-line function | STILL THERE | cmd/nova-bus/main.go:1618 |
| 16-parameter calls | CHANGED | cmd/nova-bus/main.go:2171 |
| war-story comments | STILL THERE | cmd/nova-bus/main.go:1699 |
| REFUSED at exit 1 | STILL THERE | cmd/nova-bus/main.go:1659 |
| retired flags still declared | STILL THERE | cmd/nova-bus/main.go:2639 |
| prose promises more than bounded retries deliver | STILL THERE | cmd/nova-bus/main.go:14 |
| README rated 6.5 to 7 by one rater and 8.4 by another | CHANGED | README.md:24 |
