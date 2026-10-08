# nova-self-talk READ and USE rating, nova-tools 1.2.0

Rater: gpt-5.6-terra via Codex CLI
Build: 0d56536c3d61
READ: 8/10
USE: 8/10

The exercised binary identified itself as `nova-self-talk v1.2.0-dev.0d56536c darwin/arm64 go1.27.1`. Its `cmd/nova-self-talk`, `internal/selftalk`, `docs/SPEC.md`, and `docs/CLI.md` content is unchanged from the available v1.2 release-source snapshot `7acb90e18a76`. No matching published v1.2.0 release asset was available from GitHub, so this rating does not claim a separately installed release artifact.

## Reasons

READ. The cold help starts with what the tool detects, how it reads, and a first run that needs only the binary. It explains the two disjoint classes, says that the result is advisory rather than an automatic edit, documents the streams and exit behavior, and points to `shapes` for the live table. The cold spec supports those claims with the detector boundaries and the rule-document safety rationale. The live `shapes --json` result exposes 22 rows, including the matching pattern, a positive specimen, and a near miss for each rule.

READ remains 8 rather than 10 because the main banner is a 94-line manual before its examples, while the per-verb help that should make it skimmable is incomplete or misleading. `scan -h` omits its required `<file>...`; `example -h` omits `<dir>`; `scan -h` calls basename flags `<value>`; and the non-scan verb help repeats scan's finding exit table. `help frobnicate` silently returns the full banner at exit 0, so a caller cannot distinguish an unknown requested verb from ordinary help.

USE. In a job-owned scratch directory, `example` wrote the two built-in pages, bare and named scans produced the expected dated count, two findings, stream split, and exit 1, and JSON preserved the detector data. `shapes --json`, `--rule-doc`, `--skip`, and `--json` all ran against those throwaway pages. Flags after a file are accepted, improving on the 1.1.0 trailing-flag finding. A path given to `--skip` is correctly refused with the basename remedy.

USE remains 8 rather than 10 because `--max abc` is a refusal but says only `parse error`, not that the value must be a non-negative integer; the incomplete verb help makes the first correction harder than it should be; and an unknown help target succeeds instead of refusing. The tool's scan and write behavior is otherwise clear, bounded, and local: no live store, server, or network was used.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-self-talk/main.go:28-121 | The primary help is a 94-line manual before its examples, making the first interaction harder to scan. | Keep the orientation, usage, and examples in the banner; move the output inventory and extended flag explanation to verb help and docs. | M |
| 2 | `nova-self-talk scan -h`, `nova-self-talk example -h` | Both usage lines omit required positional arguments (`<file>...` and `<dir>`). | Render each verb's complete invocation in its own help. | S |
| 3 | `nova-self-talk shapes -h`, `example -h`, `version -h` | Non-scan verb help repeats scan's finding exit table, including exit 1 and all-skipped wording that do not apply. | Give every verb its own exit contract: 0 on completion and 2 on bad invocation where applicable. | S |
| 4 | `nova-self-talk scan -h` | `--skip` and `--rule-doc` display `<value>` although the banner requires a basename. | Label the parameter `<basename>` in the generated flag help. | S |
| 5 | `nova-self-talk help frobnicate` | An unknown help target prints the full banner and exits 0, indistinguishable from a valid request for generic help. | Refuse with exit 2, name `frobnicate`, and list the available verbs. | S |
| 6 | `nova-self-talk --max abc scratch/pages/journal.md` | The refusal is only `parse error`, with no accepted range or value-specific remedy. | Say that `--max` requires a whole number greater than or equal to zero and include the received value. | S |

## Good, keep

- The first run is genuinely self-contained: `example` wrote only the two requested scratch pages and named the next scan command.
- The two detector classes, their licences, matched text, and near misses are inspectable through `shapes --json` rather than hidden behind a score.
- The scan records a dated claim without treating it as failure, reports findings on stderr in source order, and keeps the closing totals and scope warning visible.
- Basename validation refuses a path-bearing `--skip` value and gives the precise correction.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| Flags after a positional file were refused (1.1.0 USE). | FIXED | `scratch/pages/journal.md --max 1` ran and exited 1 for its real findings, not a flag-order refusal. |
| `INSTALLATION` was introduced without a useful gloss (1.1.0 READ). | FIXED | The banner calls it a neutral-worded standing verdict and gives its four shape families. |
| The banner was an extensive terminal essay (1.1.0 READ). | STILL THERE | `help` remains 94 lines before the examples. |
| A bad `--max` value had an unhelpful parser refusal (earlier v1.2 review). | STILL THERE | `--max abc` returns `parse error` without the valid range. |
| Per-verb help was incomplete (earlier v1.2 review). | STILL THERE | `scan -h` and `example -h` omit their required positional arguments. |
