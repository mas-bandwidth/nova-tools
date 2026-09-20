RESULT tools22-rule-version-9-L88 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 9 says?
ABSENT
SPEC docs/SPEC-VERSION.md:88 rule 9
PKG cmd/nova-version
ASK An implementation would have to run the `moved` verb's per-revision help inventory (or read two --from/--to TSVs) and emit `deleted=1 added=1 renamed=0` for a tool only at --from and a differently named tool only at --to with identical helps, and `renamed=1` only when the commit message or a MOVED file states the rename.
Deciding lines — the `nova-version` verb block has no `moved` verb at all, so nothing can count renames:
  internal/update/cli.go:79-83  versionVerbs = `nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>]\n
                              nova-version diff --from <a.tsv> --to <b.tsv>\n
                              nova-version report --file <manifest: ...>\n
                              nova-version send --file <manifest: ...>\n
                              nova-version help`
  internal/update/cli.go:165-175  verb dispatch handles only help/version/snapshot/diff/report/send — no "moved".
  cmd/nova-version/main.go:19     main() { os.Exit(update.Main("nova-version", os.Args[1:], version, os.Stdout, os.Stderr)) } — the whole verb set lives in internal/update/cli.go, which has no moved.
Greps — all in repo/, read-only:
  grep -rn "renamed=|MOVED OK|TestMovedNeverInfersARename" --include='*.go' .   → nothing
  grep -rn "moved --from" .   → only docs/SPEC-VERSION.md
  grep -rln "TestMovedNeverInfersARename" .   → only docs/SPEC-VERSION.md
  grep -rn "moved" internal/update/ cmd/   → no moved verb (only unrelated "the base moved" comments)
  grep -rn "func Test" --include='*_test.go' cmd/nova-version/   → 9 tests (examplelines, firstrun, friendsequence, version_test); none is TestMovedNeverInfersARename.
  ls cmd/nova-version/   → examplelines_test.go firstrun_test.go friendsequence_test.go main.go testdata version_test.go
The test the rule names does not exist, the `moved` verb it exercises does not exist, and no `renamed=` output string exists anywhere in the tree. docs/SPEC-VERSION.md is the only file carrying any of them.
Left owed