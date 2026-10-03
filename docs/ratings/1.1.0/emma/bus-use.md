# nova-bus USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10

## Reasons
The tool behaves predictably and reliably when communicating across clones using local git repositories. The draft and send lifecycle works cleanly end to end, note identities and timestamps are deterministic, and replies reliably link threads without manual header formatting. The wait command handles timeouts gracefully and emits an actionable rearm command upon completion across bounded polling intervals.

A score of 10 would require auto-fetching or warning when reading stale checkouts, allowing absolute paths in receipt commands, providing a sensible default for receipt-max-words, providing structured output flags across inspection verbs, and including actionable remedy hints on invalid integer flags.

Every verb was exercised against local repositories and clones. No remote network services were required.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus inbox --bus bus --as Bo --receipt-max-words 40` | reading without advance inspects the local checkout without fetching from remote or indicating that local tracking is behind | check upstream tracking branch and warn when checkout is behind remote | M |
| 2 | `nova-bus inbox --bus bus --as Bo --receipt-max-words 40 --full --advance --remote origin --branch main` | advance fetches and rebases remote notes into the working tree during push but reports zero open notes for that execution | parse and report incoming notes brought in by the advance rebase | M |
| 3 | `nova-bus receipt --bus bus --as Bo --note bus/from-ada/note.md` | absolute or filesystem paths to note files are rejected as neither an id nor a note that exists | resolve filesystem paths against bus root or document that only relative lane paths and ids are accepted | S |
| 4 | `nova-bus inbox --bus bus --as Bo` | receipt-max-words has no built-in default and requires mandatory configuration | provide a sensible default like 40 when no defaults file or env var is present | S |
| 5 | `nova-bus check --bus bus --max -5` | bad integer values fail with a plain error line without a run remedy command hint | append standard run remedy suggestion to parameter validation messages | S |
| 6 | `nova-bus names --bus bus` | inspection verbs do not support structured machine-readable output flags | implement json output across names check and inbox | M |

## Good, keep
Draft, send, and reply provide a robust end-to-end communication flow with automated threading and header safety.
Deterministic note identifiers and automated merge conflict resolution for metadata files prevent common git collaboration hurdles.
The wait command cleanly manages polling loops and provides exact copy-paste rearm commands upon timing out.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox` inspects local refs without fetching or warning of lag behind remote |
| inbox --advance --bodies does not advance | CHANGED | `nova-bus inbox --bodies --advance` advances safe frontier when full page is drained |
| --advance pulls unread notes in unmentioned | STILL THERE | `nova-bus inbox --full --advance` rebases incoming notes during push without listing them in output |
| receipt path scope is undocumented and misleading on absolute paths | STILL THERE | `nova-bus receipt --note /abs/path` fails with neither an id nor a note that exists |
