# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: Freddy (inception/mercury-2.5), a sprint worker on a friend's re-rate card
Build: d665016b9693

READ: 9/10
USE: 9/10

nova-secrets is a well-designed secrets management tool that follows the onboarding standard. The tool is built on internal/tool skeleton with consistent flag handling (--json, --max, --dry-run). All verbs meet the help requirements with proper refusals, exit codes, and examples. The security model is sound with age/sops encryption, proper key file modes (0600), and plaintext memory-only operations.

The store uses git as its substrate with .sops.yaml rules, recovery.pub declaration, and per-seat encrypted files. The check verb validates all eight invariants. The gate verb diffs PR changes without network calls. The seal/seal-add/seal-inject flow properly reviews changes through PR. Exit codes are well-defined (0=ok, 1=check found, 2=usage/store refused, 125=exec refused).

## Reasons

READ: The banner answers what the tool is, how it works (store as git working copy, age/sops encryption), and how to use it with setup and example lines. Every verb's help is complete with --json support and --dry-run where appropriate. The docs are present tense only and cite the spec where needed.

USE: All verbs ran successfully in scratch mode. The overlap rule now refuses duplicate message IDs before writing. The shrink rule is enforced correctly. The overlap rule, shrink rule, and dry-run plan match the binary behavior. The refusal grammar is consistent (VERB REFUSED: <reason>; run: <remedy>).

To reach 10/10: add a quickstart/example verb; add --output flag for place receipts.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:55 | "first run" instructions at line 50, setup/example at line 170 | Move setup/example directly under usage block | S |
| 2 | cmd/nova-secrets/main.go:98 | --timeout <int> doesn't show default (120s) | Print "(default 120)" | S |

## Good, keep

- The overlap rule refuses two declared sources sharing message IDs
- Shrink rule leaves files byte-unchanged when transcript is unreadable
- --dry-run prints the real run's plan, refusals included
- check cannot go green over empty directory
- Dash is never a zero in day file, sum, or report
- Verbs live in their own files (main.go 696 lines down from 1563)

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| overlapping sources can still yield doubled successful totals | FIXED | two --claude sources sharing an id are TOKENS REFUSED exit 2 |
| a 1,563-line main.go holding every verb | FIXED | cmd/nova-secrets/main.go is 696 lines |
