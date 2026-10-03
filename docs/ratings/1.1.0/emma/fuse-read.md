# nova-fuse READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8/10

## Reasons
The tool provides an uncompromising fail-closed safety mechanism for halting ingestion from untrusted or compromised sources. Its design favors safety over convenience: unreadable files fail closed, emergency lockdowns cannot be lifted by the tool, and state is stored in an inspectable JSON box with atomic write guarantees.

A score of 10 would require implementing advisory locking to prevent lost concurrent updates, enabling strict JSON decoding to reject corrupted schema extensions, consolidating redundant verb declarations, adopting shared tool formatting conventions, and removing historical narratives and shouted commentary.

The first place of confusion was docs/CLI.md:330, where subcommands refuse standard -h flags with exit code 2 to prevent confusing help with permission, creating an exception to the universal flag convention of the suite.
The first place of boredom was cmd/nova-fuse/main.go:195, where parseBoxWith spends 75 lines hand-rolling flag set silencing, single-value enforcement wrappers, and positional delimiter parsing.
The first place of doubting a claim was cmd/nova-fuse/main.go:16, where the banner claims blowing a fuse is cheap and instant, whereas atomic file replacement without locking can silently drop concurrent writes under contention.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/fuse/fuse.go:244 | unsynchronized read-modify-write allows concurrent writers to silently overwrite earlier updates | wrap read-modify-write sequences with advisory file locking | M |
| 2 | internal/fuse/fuse.go:226 | standard json unmarshal permits arbitrary unknown fields in the box file without schema validation | decode with json.NewDecoder and DisallowUnknownFields to reject foreign keys | S |
| 3 | cmd/nova-fuse/help.go:17 | verb list is duplicated across fuseHelps, main.go usage text, and subcommand dispatch refusals | derive usage listings and validation checks from a single centralized verb registry | M |
| 4 | cmd/nova-fuse/main.go:128 | tool hand-rolls custom output formatting and refusal grammar rather than adopting internal/tool | integrate with standard tool skeleton while retaining exit code semantics | L |
| 5 | cmd/nova-fuse/main.go:121 | comments contain historical narratives about previous implementation sizes and shout in all caps | rewrite comments into concise present-tense descriptions of current behavior | S |
| 6 | cmd/nova-fuse/main.go:195 | flag parser is wrapped with custom discard streams and single-value wrappers across 75 lines | factor custom flag parsing primitives into a reusable helper | M |

## Good, keep
World-readable JSON box with atomic replacement guarantees that readers never observe partially written or corrupt state.
Case and whitespace normalization prevents trivial bypasses using alternative casing or control characters.
Strict refusal on absent or unreadable boxes enforces a safe fail-closed posture across all reading paths.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect | STILL THERE | cmd/nova-fuse/main.go:128 |
| shouted and historical comments | STILL THERE | cmd/nova-fuse/main.go:121 |
| three copies of its verb list | STILL THERE | cmd/nova-fuse/help.go:17 |
| permissive box decoding | STILL THERE | internal/fuse/fuse.go:226 |
| lost concurrent updates | STILL THERE | internal/fuse/fuse.go:32 |
| README rated 6.5 to 7 by one rater and 8.4 by another | CHANGED | README.md:36 |
