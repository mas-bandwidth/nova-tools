RESULT tools22-rule-tokens-30 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 30 says?
ABSENT
SPEC docs/SPEC-TOKENS.md:2350 rule 30
PKG internal/tokens
ASK implement the publish verb's remote URL validation, pushurl mismatch detection, insteadOf honoring, credential redaction, --repo flag requirement, unknown-URL-shape refusal, bare-fixture remote matching, and concurrent-publisher lock arbitration.
The publish verb (rules 22–31) is explicitly "Not shipped" and "struck from this draft": docs/SPEC-TOKENS.md lines 637–639. The binary has no `publish` case and no `PUBLISH` output lines. `records publish` is refused as an unknown subcommand, exit 2, by `cmd/nova-tokens/boundary_test.go:571` `TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided`, guarded by `cmd/nova-tokens/main.go:185` which prints `unknown subcommand "records"` for any `records` invocation.

Grep results: searched `internal/tokens/` and `cmd/nova-tokens/` for `pushurl`, `insteadOf`, `get-url`, `password`, `user:password`, `redacted`, `publish`, `PUBLISH`. Found no implementation of any rule-30 behaviour. The lock infrastructure (`lock.go`, `lock_unix.go`) exists for the fold lock but is not wired to a publish lock; `remoteLike` in `repo.go:264` exists for attribution only. No code parses git remote URLs, validates pushurl vs fetch URL, honours insteadOf rewrites, redacts credentials, or handles the race-witness scenarios.

Left owed: implement the entire publish verb and all of rules 22–31.
```
$ git status --short
```