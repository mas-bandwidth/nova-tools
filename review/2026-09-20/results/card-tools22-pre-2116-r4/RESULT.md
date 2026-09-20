RESULT tools22-pre-2116-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2116 at head d0e2b533c677: cut --kind writes typed headers so lint --card accepts the card (#1852)
PREREAD 2116 claims=7 proven=7 unproven=0 defects=1 high=0

PR 2116
HEAD d0e2b533c677d7a859d82c101c6e128f3a7321f1
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE 86abcf23dd6cd95668ae1a865e11e29556996b03
BEHIND 2
FILES 3 production, 2 test
LINES +330 -7

COMMITS
d0e2b53 nova-pulse: cut --kind refuses --test with a line break (#1852)
ac23bf6 nova-pulse: cut --kind writes KIND/PATHS/TEST so lint --card accepts the card (#1852)

CLAIMS

1. `cut --kind fix` writes five typed header lines (KIND, PATHS, TEST, LEGS, SOURCE) on every generated card.
   PROVEN-BY cmd/nova-pulse/cut_kind_test.go:42 TestCutKindFixCLIWritesTypedHeaders — asserts each of
   KIND, PATHS, TEST, SOURCE appears verbatim in the produced card-1.md.

2. A `cut --kind <any-kind>` generated card passes `lint --card` typed-header checks with zero findings.
   PROVEN-BY internal/pulse/cutkind_test.go:357 TestCutKindEveryKindPassesLintCardHeader — iterates all
   five kinds (read, fix, replay, spec, rebase), generates cards, calls swarm.LintCardHeader on each,
   fails if any findings are returned.

3. Named --paths and --test flags land as PATHS: and TEST: values on the card instead of defaulting to none.
   PROVEN-BY internal/pulse/cutkind_test.go:379 TestCutKindNamedPathsAndTestLandOnTheCard — constructs a
   fix card with explicit paths and test values, asserts they appear verbatim in PATHS:/TEST: lines.

4. `--test` with an embedded LF or CRLF is refused with exit code 2 and a CUT REFUSED message naming --test.
   PROVEN-BY internal/pulse/cutkind_test.go:410 TestCutKindRejectsTestWithLineBreak — feeds LF and CRLF
   variants through cutKind, asserts exit == 2, stderr contains "CUT REFUSED" / "--test" / parentheses.

5. `--paths` values are validated by hygienepaths.ValidatePaths (rejects path-climbing like ../).
   PROVEN-BY internal/pulse/cutkind_test.go:315+ TestCutKindRefusalsNameTheirRemedy ("paths climb" case, line ~450)
   — passes Paths: "../elsewhere.go" and asserts the refusal mentions --paths.

6. `--test` values that are not valid Go test names are refused with exit code 2.
   PROVEN-BY internal/pulse/cutkind_test.go:315+ TestCutKindRefusalsNameTheirRemedy ("test not a name", line ~451)
   — passes Test: "not-a-test" and asserts the refusal mentions --test.

7. The contract line for a fix card carries sha=<sha12>, and SOURCE: does not duplicate the repo name.
   PROVEN-BY internal/pulse/cutkind_test.go:321 TestCutKindFixCardPassesLintCardHeader — regex-matches sha=[0-9a-f]{12}
   in line 1, checks SOURCE is owner/repo#n, and asserts no "repo repo" duplication string.

DEFECTS

medium internal/pulse/cutkind.go:280 kindSourceLine — `replay` and `spec` fall through to the default case which
returns only `in.Repo`; the old per-kind formatting produced SOURCE: `<repo> <names>` for replay and
SOURCE: `<repo> spec` for spec — the replay names and the spec keyword are silently dropped from the
typed header — other tools parsing the card may rely on those identifiers — restore them via switch cases:
case "replay": return fmt.Sprintf("%s %s", in.Repo, in.Names); case "spec": return in.Repo + " spec" —
but TestCutKindEveryKindPassesLintCardHeader (line 357) does not assert SOURCE: content, it only checks
lint --card returns zero findings, so the defect would be caught by asserting the exact source string per kind.

QUESTIONS

1. The comment at kindGoTestName references lintheader.go as the shared gate that defines this regex.
   Can you confirm kindGoTestName (`^Test[A-Za-z0-9_]*$`) exactly matches the pattern used by lint --card
   so that a value accepted by cut will never bounce downstream?

2. The renderKindCard refactor removed individual `fmt.Fprintf(&b, "SOURCE: ...")` calls per kind and
   centralized into kindSourceLine. For read and rebase kinds, the old format was `SOURCE: <repo>#<pr>`
   and the new one preserves that. For fix it was `SOURCE: <repo>#<issue>` (preserved). But replay/spec
   changed from `<repo> <context>` to just `<repo>` — was this intentional or did the switch-case exhaust
   the known kinds incorrectly?

3. `kindTestProblem` uses `strings.Fields(v)` which normalizes whitespace (tabs→spaces, collapse runs).
   Should `kindTestLine` normalize its output the same way, so that two identical logical test specs
   passed with different whitespace produce identical cards?

4. The PR adds `contractSHA12(renderKindCard(...))` wrapping in CutKind (present in the PR HEAD file but
   not visible in my merge-base diff). Is sha=<sha12> a feature introduced by this PR or present in the base?
   If the latter, did removing the old `SOURCE:` from within renderKindCard accidentally shift where
   the hash boundary falls?

Left owed
internal/hygiene/paths.go: ValidatePaths implementation logic; internal/swarm/lintheader.go: lint --card
typed-header grammar that cut must conform to; lanes.Write integration after CutKind — do not know whether
the lane writing layer consumes the new header fields; go build/go vet verification — did not run.

git status --short
 ?? .lease
 ?? .nova-sandbox-tmp/
 ?? RESULT.md
 ?? harness-output.log
 ?? opencode.json
 ?? repo/
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
