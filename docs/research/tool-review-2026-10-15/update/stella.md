# nova-update AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 34ea5c0cb662
Score: 8.5/10 (46/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-update help` -> `nova-update: compare installed tools with their latest releases, and update one when asked` (first run from help alone worked end to end: example -> report -> status -> apply --dry-run) |
| 2 | refusals | 5 | `nova-update report` -> `REPORT REFUSED: missing --file; refusing to guess; run: nova-update report -h` (each of missing flag, unknown flag, unknown verb, bad value names its cause and the next command) |
| 3 | output | 5 | `nova-update report --file versions.tsv --json` -> `{"result":{"verb":"report","status":"ok",...}}` (one JSON object mirroring the typed lines; stable `VERB OK/FAILED/REFUSED key=value` grammar) |
| 4 | dry-run | 3 | `nova-update apply --file versions.tsv go --dry-run` -> `APPLY NOTE dry run: nothing installed, nothing written` (apply and the release verbs have --dry-run, but the writes `example --out` and `report --snapshot` reject it: `EXAMPLE REFUSED: unknown flag --dry-run`) |
| 5 | idempotent | 1 | `nova-update apply -h` -> no `--op` flag on any write verb (example, apply, report, watch, and every release verb list no `--op`; idempotency is only incidental, e.g. example's `unchanged=true`) |
| 6 | no prompts | 5 | `nova-update example --out versions.tsv` -> `EXAMPLE OK wrote=versions.tsv entries=1` (every command ran to completion; nothing waited on an interactive answer) |
| 7 | exit codes | 5 | `nova-update check --file bad.tsv` -> exit 1 with `CHECK FAILED ... unknown=1` (0 current, 1 tool said NO, 2 could not run, exactly as the banner states) |
| 8 | one word | 4 | `nova-update help` -> `the verbs are example, check, status, apply, report, watch, adoption, release, version` (each verb is one word and distinct, but a sibling tool nova-version shares the manifest reader and report, splitting the surface) |
| 9 | push | 5 | `nova-update report --draft --as me --to you` -> `Subject: versions on - at 2026-10-15T18:46:56Z` (delivery is a push through nova-bus; nothing polls remote state) |
| 10 | no leaks | 5 | `nova-update check --file bad.tsv` -> `CHECK UNKNOWN ... reason=not_found (install nonexistentxyz123 or supply its executable path; searched PATH=...)` (no secret or credential appears in output) |
| 11 | true help | 3 | `nova-update status --file pins.tsv` -> `STATUS EQUAL ... installed=version latest=version ... exit=0` (help advertises the `local:<argv>` pin pattern, but it mis-parses and reports a false current with a green exit) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-update status --file pins.tsv` | a pin whose installed is `go version` and latest is `local:go version` reports `STATUS EQUAL name=goversion kind=pin installed=version latest=version` and exits 0, so the help's own advertised local pattern answers a lie with a green exit and an AI trusts a pin that was never read | for kind pin, take only a version-shaped token from the line, and refuse loudly with the two-token shape when no token is version-shaped | M |
| 2 | `nova-update apply -h` | no write verb takes `--op`, so a timeout retry of an apply or a snapshot write is not provably idempotent; the same write run again is not guaranteed to change nothing | add `--op <id>` to apply (record the done op and replay it unchanged) and to report --snapshot | M |
| 3 | `nova-update example --out t.tsv --dry-run` | `EXAMPLE REFUSED: unknown flag --dry-run; the flags are --json, --out`; the same on `report --snapshot`, so a cold AI following the one-flag-per-write rule meets a refusal and cannot preview a write | accept `--dry-run` on example --out and report --snapshot: print what would be written and write nothing | S |
| 4 | `nova-update report --bogus` | `REPORT REFUSED: unknown flag --bogus; the flags are ...` names only the unknown flag, hiding the also-missing `--file` until the next run, while the manifest reader and release cut name every problem at once | collect flag problems the way the manifest reader does and print them together in one refusal | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-update report -h` | the same `THE MANIFEST` essay is duplicated verbatim in the -h of check, status, apply and report, making each a wall of prose | move the manifest rules under `report -h` and cut the banner back to the three onboarding answers, linking once | M |
| 2 | nova-update vs nova-version | two binaries in one package share the manifest reader and report, so the banner needs a paragraph telling a reader which to reach for | give nova-version its own package, or state the choice once in the README table | L |
| 3 | `nova-update apply` on a fixed target | `latest` refuses a bare literal version and the remedy names only the four locators, not the two routes that work (apply --version, or a local: wrapper that prints the version) | accept a bare version in latest as a literal target, or extend the refusal remedy with the two working routes | S |

## Top 3 fixes
1. Fix the pin reader: accept only a version-shaped token for kind pin or refuse loudly, so the advertised `local:<argv>` pattern never reports a false CURRENT.
2. Add `--op <id>` to the write verbs (apply, snapshot) so a retry after a timeout is provably idempotent.
3. Add `--dry-run` to example --out and report --snapshot, the two writes that still reject the standard preview flag.

## Good, keep
- The refusal discipline is the best part: every refusal names its cause, the valid vocabulary, and the exact next command, and manifest and release refusals name every problem at once with line numbers.
- The first run from help alone works end to end with zero setup, `--dry-run` where it exists writes nothing, and `--json` mirrors the typed lines exactly from the same value.
- Exit codes are truthful as documented, the local apply loop rechecks EQUAL, and the version line vouches for its own build.

## What could not be tried
- `apply` for real (it installs software; this card installs nothing) — judged from `apply --dry-run`.
- `report --draft/--send/--snapshot` delivery and `watch` delivery (need the nova-bus Redis bus) — judged from the draft and help.
- `report --store <host:port>` (needs a fleet Redis store).
- `adoption` (needs an adoption ledger, not the tool manifest).
- `release cut/build/install/adopt/pull/cycle` (act on the project's own release pipeline, forge and ssh machines) — judged from help and the documented `--dry-run`; only their refusals were run.
