# nova-secrets USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7.5/10

## Reasons
The tool provides reliable, zero-leak credential handling when operating on a properly initialized store. Key generation produces correctly formatted public rule stanzas, seal safely manages branch isolation without leaking values into command arguments, names provides clean inventory inspection with structured JSON, and exec successfully injects decrypted credentials directly into child process environments without touching disk.

A score of 10 would require reconciling the conflicting machine registry schemas between place and gate, reporting all gate validation errors in a single turn, providing clear key mismatch diagnostics instead of raw sops exit 128, appending next-command hints to invalid flag value refusals, and supporting structured JSON across all inspection verbs.

Verbs requiring live GitHub infrastructure (seal without --no-pr, seat inject without --no-pr) and network targets (place without --dry-run) were evaluated via their --help and --dry-run modes.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets place --machines fleet.tsv` | accepts seven-column fleet registry and treats architecture as home directory planning garbage remote paths | validate registry column layout and extract proper home directory | M |
| 2 | `nova-secrets exec --key ./keys/wrong.key` | opening a seat with an unauthorized key outputs raw sops exit 128 without diagnosing recipient mismatch | verify key against seat recipients and report identity mismatch | M |
| 3 | `nova-secrets gate --store . --base main --head bad` | gate halts at the first invalid change without reporting subsequent rule or file errors | aggregate all diff violations into one comprehensive refusal | M |
| 4 | `nova-secrets names --max notanumber` | refusal explains expected flag type but omits actionable next command remedy pointer | append standard run hint when flag value parsing fails | S |
| 5 | `nova-secrets check --store ./store` | rejects local store lacking upstream tracking ref without providing an offline verification mode | allow explicit flag for local repository validation | S |
| 6 | `nova-secrets check --json` | structured output is supported only by names and rejected by check and other inspection verbs | implement json flag across all inspection commands | M |

## Good, keep
Clean credential isolation ensuring secrets only reach child environments without persisting to disk or logging.
Comprehensive dry-run planning for store modifications and seat injections that verifies state changes beforehand.
Safe branch management in seal and seat inject that leaves the working tree on its starting branch upon completion.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| place accepts the gate's registry format and plans a garbage path | STILL THERE | `nova-secrets place --machines fleet.tsv --dry-run` plans path=darwin/arm64/.config/nova-secrets/KEY.env |
| the gate names one problem at a time | STILL THERE | `nova-secrets gate` exits immediately on first file violation without reporting rule errors |
| a wrong key gives sops failed: exit 128 | STILL THERE | `nova-secrets exec` prints sops failed: exit 128 with transcript withheld |
| the bad-value refusal lacks a next command | STILL THERE | `nova-secrets names --max notanumber` prints explanation without run remedy pointer |
