RESULT tools22-rule-tokens-28-L2334 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOKENS.md:2334 rule 28
PKG internal/tokens
ASK The publish verb must exist and produce a bus checkout byte-identical to the ledger while writing no note; report --ledger must exit 2; and a same-day report and publish must land two artifacts whose bytes differ without either verb running the other's subprocess.
The `publish` verb is explicitly not shipped. docs/SPEC-TOKENS.md:637-639 says "Not shipped. The verb is struck from this draft: the binary has no `publish` case and no `PUBLISH` output lines". `cmd/nova-tokens/main.go:162-184` has no `case "publish"`. `cmd/nova-tokens/boundary_test.go:571-589` (`TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided`) confirms `records publish` is refused as an unknown subcommand, exit 2.
`report --ledger` without `--month` IS exit 2 via `cmd/nova-tokens/reportledger.go:17-18` (flag validation in `cmdReportLedger`), but this single clause cannot carry the whole rule.
The third clause (differing bytes between report and publish artifacts) has no code to evaluate because `publish` does not exist.
Greps: `grep -rn '"publish"' --include='*.go' cmd/nova-tokens/`, `grep -rn 'ledger' --include='*.go' internal/tokens/`, `grep -rn 'cmdPublish|cmdReportLedger' --include='*.go' .`, read `cmd/nova-tokens/main.go` (the verb switch), `cmd/nova-tokens/reportledger.go`, `cmd/nova-tokens/boundary_test.go`.
UNGUARDED (the `publish` absence is explicit and guarded by `TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided`; there is no test that `report --ledger` without `--month` exits 2)
Left owed: none — the verdict holds without running a test.
git status --short: nothing
