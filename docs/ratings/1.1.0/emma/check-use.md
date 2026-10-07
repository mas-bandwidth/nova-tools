# nova-check USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool was exercised cold inside a fresh scratch directory using built binaries from cmd/nova-check. The onboarding experience is clean: quickstart runs cleanly on an example markdown directory with zero setup, link checking pinpoints broken relative targets with file and line coordinates, and spelling detection identifies typos and corrects them in place with the --write flag while respecting code spans. Subcommands like dogfood record, ledger, and gate work seamlessly across local receipt files without requiring a database.

However, several usability frictions prevent a higher score. First, nova-check hygiene requires two separate invocations to surface all required flags: running it with no flags reports that --repo, --base and --head are required, and only after supplying them does it report that --identity is also missing. Second, both links and spelling report green OK with exit code 0 when given an empty directory, claiming success over zero files rather than warning that nothing was evaluated. Third, all refusal messages uniformly point to the global help (run: nova-check help) instead of the relevant verb help (such as run: nova-check links -h). Fourth, capping conventions are inconsistent: hygiene expects --max while other finding-listing verbs expect --fail-max. Fifth, neither quickstart nor dogfood supports --json. Finally, convergence was not tried because it requires an active forge connection through the gh executable and external project ledgers.

A 10/10 would require reporting all missing flags in a single turn across all verbs, warning or failing on zero-file inputs for links and spelling, pointing refusals directly to subcommand help, standardizing capping flags on --fail-max, and providing structured JSON output across all verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check hygiene` | Running with no flags only reports --repo, --base and --head as missing, hiding the required --identity until a second invocation | Report all missing required flags including --identity in the first refusal | S |
| 2 | `nova-check links --dir ./empty` | Reports LINKS OK files=0 links=0 exiting 0 over an empty directory without warning that nothing was checked | Warn on stderr when zero markdown files are found or require at least one file | S |
| 3 | `nova-check links` | Every command refusal directs the caller to run: nova-check help rather than the specific subcommand help | Direct callers to verb help such as run: nova-check links -h | S |
| 4 | `nova-check quickstart --dir ./self --json` | quickstart refuses --json even though the tool standard expects structured output support across all verbs | Implement structured JSON output for quickstart aggregating sub-check findings | M |
| 5 | `nova-check hygiene --repo . --base main --head feat --identity "A <a@b.c>" --fail-max 10` | hygiene rejects --fail-max because it uses --max while other findings verbs expect --fail-max | Standardize on --fail-max across all verbs or accept --max as a synonym | S |
| 6 | `nova-check spelling --dir . --write` | spelling provides no dry-run flag to preview in-place modifications before altering files on disk | Add a dry-run flag to preview replacements without modifying files | S |

## Good, keep
Fast and reliable relative link validation with precise line numbers and clear exit codes.
In-place spelling correction via --write that accurately fixes misspellings while safely preserving code spans.
Completely local dogfood receipt recording and ledger gating requiring no database infrastructure.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a green over zero files (spelling, links) | STILL THERE | `nova-check links --dir ./empty` printed LINKS OK files=0 links=0 excluded=0 |
| two refusals name one problem | STILL THERE | `nova-check hygiene` omitted --identity until --repo, --base and --head were provided |
| every remedy is the whole help | STILL THERE | `nova-check links` printed run: nova-check help instead of verb help |
| link findings, spelling fixes, JSON and dry-run as documented | CHANGED | Links, spelling fixes and JSON work as documented but dry-run is not provided as an explicit flag |
