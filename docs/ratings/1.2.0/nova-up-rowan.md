# nova-up READ and USE rating, nova-tools 1.2.0

Rater: a cold rater, mercurys-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 6.5/10
USE: 6/10

nova-up: sets up nova on one machine, from nothing to a first sprint. The tool has verbs: up, version, help.

## Reasons

READ. The README is minimal but shows the dry-run example. The spec is in docs/SPEC-UP.md. What holds the score: there is no detailed first-run transcript showing actual output, and the up verb's flags are not documented in the README.

USE. The --local --dry-run mode works without making changes. What holds the score: there is no way to see what steps the up command will take without running it.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-up/main.go:help | up --help does not list the setup steps that will be taken | add a list of setup steps in up help | M |
| 2 | cmd/nova-up/main.go:up | no verbose mode to show what is being configured | add --verbose to show each setup step | S |
| 3 | README.md | no complete first-run transcript with real output | add a transcript showing up --dry-run output | M |

## Good, keep

- Dry-run mode works correctly.
- Minimal tool that does one thing.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a minimal setup tool.
