# nova-doctor READ and USE rating, nova-tools 1.2.0

Rater: mercury-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 6.5/10
USE: 6/10

nova-doctor: says what is missing for the nova tools to work, and the one line that fixes each. The tool has two verbs: run and version.

## Reasons

READ. The README shows the run verb examples but does not list all available checks. The spec is in docs/SPEC-DOCTOR.md but the README does not mention how to discover available checks. What holds the score: run --help would be useful to see available checks.

USE. Running nova-doctor run --local checks the local machine. Running with --json gives machine-readable output. What holds the score: there is no way to list available checks without running them, and the output does not clearly indicate what "fixed" vs "missing" means.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-doctor/main.go:help | run --help does not list available checks | add a list of check names in the help output | M |
| 2 | cmd/nova-doctor/main.go:run | no --list-checks flag | add --list-checks that outputs available check names | S |
| 3 | cmd/nova-doctor/main.go:run | output does not distinguish between "fixed" and "missing" clearly | add a status field to each check result | S |

## Good, keep

- Simple, focused tool that does one thing well.
- JSON output option for machine-readable results.
- Run command works locally without external dependencies.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a minimal, functional check runner.
