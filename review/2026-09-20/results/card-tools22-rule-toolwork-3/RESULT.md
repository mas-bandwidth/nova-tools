RESULT tools22-rule-toolwork-3 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 3 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:924 rule 3
PKG internal/docs
ASK A class test `TestEveryTranscriptIsExecutedLineForLine` in `internal/ci` that walks `docs/TESTS.md` sections, fails for any tool whose package has no test calling `onboarding.CompareTranscript` on its section, fails for any `firstrun_test.go` that compares any other way, and enforces a shrink-only allowlist.
GREPS:
  `grep -rn "TestEveryTranscriptIsExecutedLineForLine" --include='*.go' .` — no results
  `grep -rn "CompareTranscript" --include='*.go' .` — no results (the actual function is `onboarding.Compare` at `internal/onboarding/transcript.go:627`)
  `ls internal/ci/testdata/` — no transcript allowlist file; `prmerge_allowlist.txt` exists but is for a different class test
  The SPEC itself at line 957-958 lists `every-transcript-is-executed-line-for-line` as a test that "fails today", confirming it is a planned (T7, #1652) but unimplemented test.
Left owed: the class test described in rule 3 does not exist; every `firstrun_test.go` in cmd/ currently compares by other means (e.g., `printed map[string]bool` at SPEC:906-908, or direct use of `onboarding.Compare`), and none of the specified components (`CompareTranscript`, `Volatile`, the allowlist, the three-seeded-reds proof per tool) are present.

```
git status --short
```
(nothing)