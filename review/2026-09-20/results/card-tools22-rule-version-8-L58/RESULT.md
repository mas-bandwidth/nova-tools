RESULT tools22-rule-version-8-L58 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 8 says?
ABSENT
SPEC docs/SPEC-VERSION.md:58 rule 8
PKG internal/update (called by cmd/nova-version and cmd/nova-update)
ASK The apply --sha verb must, after building all binaries and reading back each stamp, refuse exit 2 naming both binaries and both stamps if any two report different versions, discarding the staged build so --bin is never left mixed.
The apply --sha verb does not exist in this codebase. No flag parsing for --sha exists; no postflight stamp-consistency gate is performed before atomic install; no "mixed set" refusal for apply exits with exit 2.
Where it is referenced:
  internal/update/snapverb.go:189 — the SNAPSHOT verb detects mixed stamps and suggests "rebuild the set under one stamp with nova-update apply --sha" as a remedy, but --sha is not implemented anywhere.
Grep evidence:
  grep -rn "'--sha'\|\"--sha\"\|--sha" --include='*.go' .   -> no matches
  grep -rn "func Test.*ApplySha\|TestMixedSet\|mixed.stamp.*pair" --include='*_test.go' . -> no matches
  grep -rn "apply --sha\|apply_sha\|applySha" --include='*.go' . -> only snapverb.go:189 reference text
  ls cmd/nova-version/ -> main.go version_test.go firstrun_test.go friendsequence_test.go examplelines_test.go testdata/ (no apply-related files)
  ls cmd/nova-update/ -> main.go (delegates entirely to update.Main)
  grep -n "\"sha\"" internal/update/cli.go -> nothing; apply verb flags at line 215 define only --version, no --sha/--repo/--bin
Tests listed in spec red-tests section that would guard this:
  TestApplyShaRefusesAMixedSetNamingThePair - ABSENT
  TestApplyShaRefusesALostStamp - ABSENT
  TestApplyShaPublishesTheWholeSetAtomically - ABSENT
  TestStampCheckAlsoVerifiesSourceMetadata - ABSENT
Left owed
git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
