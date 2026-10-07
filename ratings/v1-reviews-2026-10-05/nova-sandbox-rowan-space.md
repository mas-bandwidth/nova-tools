# nova-sandbox review, 2026-10-05

Rater: inception/mercury-2.5
Build: 3baf15489a3743b10a26a7b016f680c46b4e4f9e
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The help is clear and complete. Every refusal names its remedy (no --write, no --secret on probe, paths that don't exist). The JSON output would be valuable for automation but is not documented in the CLI reference. The --net-deny flag is loud (prints net=denied) rather than silent, which is correct per the spec.

The probe verb is the first thing I wanted to try. It correctly refuses when --secret points to a missing file (good). The five-step probe checks that the wall actually works.

The tool has no quickstart verb (by design per the spec: every verb needs paths the tool must not invent). This is correct but means first users must read the example block carefully.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md, nova-sandbox section | --json flag is not listed in the usage line or any verb help; the spec says every verb accepts --json but only the bare form is shown | add --json to every verb's synopsis line (check, probe, policy, run, reap, worktree, egress plan/apply/check/drop, version, help) | S |
| 2 | nova-sandbox help | --gpu flag is missing from the flags list in the help output (line 22 shows it but it's not documented in the usage section) | document --gpu under the flags section in main.go | S |
| 3 | docs/CLI.md | The run verb help doesn't document --out, --artifact, or --out-max-bytes (handoff flags) which are critical for cards that need to commit artifacts | add handoff section to run verb documentation in CLI.md | M |

## Good, keep
The probe verb proves the wall before the job runs (five checks, not two). Every path is explicit and nothing is guessed (no HOME default, no --write default). The net=nopromise vs net=denied distinction is loud and visible.
