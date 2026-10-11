# nova-secrets READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7/10
README: 8/10

## Reasons
The tool addresses a critical security boundary for autonomous agents: delivering credentials into ephemeral child command environments from an encrypted git-backed store without leaking secrets to arguments, disks, or logs. Its cryptographic design based on age and sops is sound, the immutability guarantees of recovery keys are thoughtful, and the presence of dry-run capabilities across state-changing verbs provides essential safety.

A score of 10 would require decoupling unrelated subsystem rules from exec, standardizing refusal grammar and exit codes across all verbs, updating package documentation to reflect modern multi-verb architecture, making onboarding examples portable across operating systems, and opening documentation with first-run credentials guidance rather than pull-request gating.

The first place of confusion was docs/CLI.md:1710, where the command reference opens with pull request gating in CI rather than explaining how to initialize a store, generate keys, or execute commands.
The first place of boredom was cmd/nova-secrets/main.go:210, where disallowedVerbs contains nineteen essay-length rejection explanations for unadopted verbs rather than concise Unix diagnostics.
The first place of doubting a claim was docs/SPEC-SECRETS.md:34, which claims the tool is about two hundred lines of Go over two binaries, while the codebase comprises thousands of lines across dozens of files invoking git, gh, ssh, sops, and age-keygen.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:276 | exec embeds table and sprint specific command restrictions intercepting redis-cli writes | move external tool policy to higher-level wrappers | M |
| 2 | pkg/secrets/gate.go:252 | gate formats errors using custom GATE REFUSE syntax and halts on the first problem encountered | report all rule violations in one standard refusal | M |
| 3 | cmd/nova-secrets/main.go:414 | exec uses custom SECRETS EXEC FAIL syntax with exit code 125 rather than standard refusal conventions | harmonize refusal output and return standard exit code 2 | S |
| 4 | pkg/secrets/secret.go:2 | package documentation is stale claiming the package provides only four verbs | update doc comments to cover all current verbs | S |
| 5 | cmd/nova-secrets/main.go:81 | example block hardcodes platform-specific Homebrew binary paths for age-keygen and sops | rely on PATH lookups or portable path references | S |
| 6 | docs/CLI.md:1710 | reference documentation opens with CI gate verification instead of credential onboarding | restructure docs to lead with first-run keygen and exec | S |
| 7 | docs/SPEC-SECRETS.md:34 | specification claims the tool is about two hundred lines of Go over two binaries | update specification to match actual scope and dependencies | S |

## Good, keep
Clean credential isolation ensuring secrets only reach child environments without persisting to disk or logging.
Explicit preflight validation requiring stores to be cleanly tracked git working copies before executing commands.
Comprehensive dry-run planning for store modifications and seat injections that verifies state changes beforehand.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a table special case inside exec | STILL THERE | cmd/nova-secrets/main.go:276 |
| a stale package doc | STILL THERE | pkg/secrets/secret.go:2 |
| its own refusal grammar | STILL THERE | cmd/nova-secrets/main.go:414 |
| the advertised first sitting is incomplete and platform-specific | STILL THERE | cmd/nova-secrets/main.go:81 |
| README rated 6.5 to 7 from one rater and 8.4 from another | CHANGED | README.md:30 |
