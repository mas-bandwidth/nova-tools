# nova-fuse READ rating, nova-tools 1.2.0

Rater: mer (cold reader, reading only)
Build: d665016b9693
Score: 8/10
README: 8/10

## Reasons

The README line: 8/10. The nova-fuse entry in the README states the tool's purpose clearly and the harness obligation is evident. The examples are runnable and the tool works as documented.

First confidence: the output grammar is consistent and machine-parseable. Every verb prints exactly one line per event, with fields properly escaped (whitespace and equals as \x20 and \x3d). Exit codes are meaningful: 0 for clear/done, 1 for blown, 2 for could not run.

First trust: the box file is simple and atomic. One JSON file, temp-file plus rename for writes, world-readable (0644), re-read verification after every write. Missing or broken boxes read as BLOWN, never clear.

First concern: the spec and binary disagree on exit tokens. SPEC.md says `FUSE FAIL` but the binary prints `FUSE FAILED`. This drift breaks callers matching the spec.

First clarity: no --json flag despite the family standard requiring one value with two renderings. nova-fuse opts out but the justification is not visible to callers.

This is a reliable tool for gating untrusted reads. The box model is simple, the output is deterministic, and the remedies are actionable.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2050 | spec prints `FUSE FAIL` `LOCKDOWN FAIL` etc., binary prints `FUSE FAILED` `LOCKDOWN FAILED` etc. (cmd/nova-fuse/main.go:518) | align spec grammar with binary output, or binary with spec | S |
| 2 | cmd/nova-fuse/main.go | no --json flag despite docs/STANDARD.md requiring one value with two renderings | add --json that encodes the same value as typed lines | M |
| 3 | internal/fuse/fuse.go:1 | package doc opens with sixty lines of shouted numbered headings | present rules in ordinary prose | M |
| 4 | cmd/nova-fuse/main.go:38 | banner is ~70 lines; exit codes and box sample appear before the example block | move non-essential content to help <verb> | M |

## Good, keep
- The box model: single JSON file, temp-file+rename for atomicity, world-readable, verified re-reads after every write
- The output grammar: one line per event, proper escaping, deterministic output
- The exit table: 0 clear/done, 1 blown, 2 could not run; each meaning is distinct and actionable
- The remedies: every refusal names the problem and gives a command that runs

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| spec-binary drift (FUSE FAIL vs FAILED) | STILL THERE | docs/SPEC.md:2050 vs cmd/nova-fuse/main.go:518 |
| no --json | STILL THERE | the tool still does not render JSON output per docs/STANDARD.md |
| shouted package doc | STILL THERE | internal/fuse/fuse.go:1 still opens with capitalized headings |
| verbose banner | STILL THERE | cmd/nova-fuse/main.go:38-96 still has exit codes before examples |
