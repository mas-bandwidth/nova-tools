# Cold-Read Audit: Rowan's PR #4852 (Card Lint Child Rules)

**Date:** 2026-09-30  
**Auditor:** Emma Antigravity (Child Worker 120) <emma@mas-bandwidth.com>  
**Branch under audit:** `origin/rowan/card-lint-child-rules`  
**Head commit:** `b42f550b18b55c4914c193650c1e86b7a70046c5`  
**Prior PR commits:**
- `400f8c4fb82162b0f6261d7b38d793a9c96415d5` ("card lint: every rule the coordinator gives a child, add lints every brief, template --name card")
- `b42f550b18b55c4914c193650c1e86b7a70046c5` ("card lint: the exit-codes rule is nova-sprint's banner line, held equal by a test")
**Target branch:** `origin/sprint/foundation` (`6796ca0b2224c933d7e80713ccbf414c455a07f0`)  
**Merge base:** `993a191281ecc396b7aa786e078229461b64c8a5`  
**Verdict:** **GREEN (ACCEPT)**

---

## 1. Executive Summary

Rowan's PR #4852 implements the child-rule linting subsystem across `nova-swarm` and `nova-sprint`:
1. **Mechanical Enforcement of Child Invariants:** Every rule that the coordinator gives a child worker (previously spread across loose brief files like `SAFETY.md`, `VERBS-COMMON.md`, and `DIRTY-TICK-SLICES.md`) is now formalized as a mechanical check of the card lint (`internal/swarm/lintchild.go`).
2. **Two Check Modes:**
   - **Presence (27 rules):** Verifies that the required rule sentence is quoted verbatim in the card (with whitespace/line breaks folded so formatted cards match). Missing rules are reported at line 1 as `rule-<name>`, with remedies quoting the required sentence and source.
   - **Command Scans (9 checks):** Scans the text outside the `RULES.` section for forbidden commands (`redis-server`, `go clean`, `kill`, `rm -rf` outside job, `git push --force`, `git rebase`, `git stash`, `gh pr merge`, and `go test` without `-timeout 600s`). Negative formulations in prose (e.g. `Never start a redis-server`) are excluded from detection via `childNegation`.
3. **In-Process Brief Linting on `nova-sprint add`:**
   - Holds `--brief` and `--brief-file` to `swarm.LintCardChild` before any writes.
   - Any failure emits formatted `LINT DRIFT brief ...` lines to stderr, bounded by `--max` (default 20, 0 for all), followed by a `LINT MORE brief ...` pointer, and exits with code 2 without mutating the store (`ta.applies()` unchanged).
   - Cards without briefs (`--count` cards, sentinels) are admitted without linting.
4. **Golden Template (`nova-swarm template --name card`):**
   - Outputs a standard child card containing the `RESULT:` contract line, `RULES.` paragraph with all 27 sentences, task description placeholder, and 6 standard steps.
   - Verified clean under both `nova-swarm lint --card` and `nova-swarm lint --card --child-rules` (58 checks, 0 drifts).
5. **Synchronization with Sprint Usage:**
   - In commit `b42f550b1`, the `exit-codes` rule sentence is tied directly to the banner usage line of `nova-sprint` via `TestExitCodesRuleIsTheBannersLine`, preventing rule/tool drift.

All unit and race detector tests pass across `cmd/nova-sprint`, `cmd/nova-swarm`, and `internal/swarm`. Backward compatibility with existing worker cards is preserved.

---

## 2. Commit Analysis & Scope

Diff relative to merge base `993a19128` (16 files changed, +955 / -23 lines):
```
 cmd/nova-sprint/brief_file_functional_test.go |  13 +-
 cmd/nova-sprint/comforts_test.go              |   8 +-
 cmd/nova-sprint/lintbrief_test.go             | 117 ++++++++++
 cmd/nova-sprint/log_test.go                   |   8 +-
 cmd/nova-sprint/verbs.go                      |  36 +++-
 cmd/nova-swarm/lint.go                        |  22 +-
 cmd/nova-swarm/lint_test.go                   |   1 +
 cmd/nova-swarm/lintchild_test.go              | 113 ++++++++++
 cmd/nova-swarm/main.go                        |   5 +-
 deprecated/docs/WORKER-CARDS.md               |  64 ++++++
 docs/CLI.md                                   |   5 +-
 docs/SPEC-SPRINT.md                           |   2 +-
 docs/SPEC-SWARM.md                            |   9 +-
 internal/swarm/lintchild.go                   | 296 ++++++++++++++++++++++++++
 internal/swarm/lintchild_test.go              | 252 ++++++++++++++++++++++
 internal/swarm/templates.go                   |  27 ++-
 16 files changed, 955 insertions(+), 23 deletions(-)
```

---

## 3. Detailed Architectural & Implementation Review

### 3.1 Child Rules Table (`internal/swarm/lintchild.go`)

The core table `CardChildRules` defines 27 rules:
- `worktree`: "Work only in the NEW worktree this card names." (`SAFETY.md`)
- `own-branch`: "Touch only your own branch." (`SAFETY.md`)
- `gocache`: "Export a private GOCACHE (the path this card names) and GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command." (`SAFETY.md`)
- `no-go-clean`: "Never `go clean`, and never clean a shared cache." (`SAFETY.md`)
- `no-redis-server`: "NEVER start a redis-server on this machine." (`SAFETY.md`)
- `no-kill`: "Never kill a process you did not start." (`SAFETY.md`)
- `go-test-timeout`: "Every `go test` gets `-timeout 600s`." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `no-rm-rf`: "No `rm -rf` outside the job directory." (`SAFETY.md`)
- `no-force-push`: "Never force-push." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `no-rebase`: "Never rebase." (`DIRTY-TICK-SLICES.md`)
- `no-stash`: "Do not use git stash (the stash list is shared by every worktree)." (`DIRTY-TICK-SLICES.md`)
- `functional-in-container`: "Functional tests (any test that needs Redis) run ONLY inside the container through `tools/functionalrun` (`--fresh-gocache --deadline 15m`), never against any other store." (`SAFETY.md`)
- `parallel`: "Every new test opens with `t.Parallel()`." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `class-tests`: "Run `go test -count=1 -timeout 600s ./internal/ci/` before each push." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `no-names`: "No names of people, machines or friends in code, comments or docs." (`SAFETY.md`)
- `present-tense`: "Docs and comments in the present tense." (`SAFETY.md`)
- `cite`: "Cite the model or the design section from every function that implements a rule." (`SAFETY.md, VERBS-COMMON.md`)
- `only-named-files`: "Touch only the files this card names; a fix that needs another file goes into your report as a proposed diff, not a commit." (`DIRTY-TICK-SLICES.md`)
- `minimal-diff`: "Keep the diff minimal: every added line traceable to one sentence of this card." (`DIRTY-TICK-SLICES.md`)
- `commit-trailer`: "Commit messages end with `Co-Authored-By: Claude <your model> <noreply@anthropic.com>`." (`SAFETY.md`)
- `pr-line`: "PR bodies end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`." (`SAFETY.md`)
- `never-merge`: "Open PRs against the base this card names; never merge." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `exit-codes`: "exit codes: 0 done, 1 refused, 2 usage or a store that did not answer" (pinned to banner)
- `pr-diffstat`: "The PR body states the diff stat and what was deleted." (`the owner's list of 2026-09-30`)
- `pr-tests`: "The PR body lists the tests, each with what it pins, and every local helper added." (`VERBS-COMMON.md`)
- `report-shape`: "Report under 80 lines: PR number and sha, every test package line, what you could not do and why." (`SAFETY.md, DIRTY-TICK-SLICES.md`)
- `report-not-done`: "\"Not done\" is a welcome report; a green claim you did not run is not." (`SAFETY.md`)

### 3.2 Command Scans (`internal/swarm/lintchild.go`)

9 forbidden command scans outside the `RULES.` section:
1. `step-redis-server` (`no-redis-server`): forbids `redis-server`.
2. `step-go-clean` (`no-go-clean`): forbids `go clean`.
3. `step-kill` (`no-kill`): forbids `kill`, `pkill`, `killall`. Allows killing child's own process via `kill $!` or `kill %<n>`.
4. `step-rm-rf` (`no-rm-rf`): forbids recursive `rm` targeting outside `$PWD`, `<job>`, `<workspace>`, etc.
5. `step-force-push` (`no-force-push`): forbids `--force`, `--force-with-lease`, `-f`, or `+` refspecs.
6. `step-rebase` (`no-rebase`): forbids `git rebase`.
7. `step-stash` (`no-stash`): forbids `git stash`.
8. `step-merge` (`never-merge`): forbids `gh pr merge`.
9. `step-go-test-timeout` (`go-test-timeout`): requires `-timeout` on every `go test` invocation.

**False-positive avoidance:**
- `childNegation` regexp (`never`, `not`, `no`, `don't`, `cannot`, `can't`, `without`, `forbid*`, `refus*`) exempts explanatory prose.
- The `RULES.` section is explicitly skipped during scanning.

### 3.3 Integration in `nova-sprint add` (`cmd/nova-sprint/verbs.go`)

- Both `--brief <text>` and `--brief-file <path>` invoke `lintBrief(brief, c.max, stderr)`.
- If findings exist:
  - Formatted `LINT DRIFT brief <check>: <line>: <excerpt> remedy=...` emitted to stderr.
  - Bounded by `c.max` (if set) with `LINT MORE brief findings=%d remedy=add --max 0`.
  - Exits with status 2: `nova-sprint add: the brief fails the card lint (%d findings); a brief is a child's whole brief and carries every rule the coordinator gives a child; run: nova-swarm template --name card`.
  - No database state is mutated.
- Unbriefed cards (`--count <n>` without brief, sentinels) bypass the lint.

### 3.4 Card Template Conformance (`nova-swarm template --name card`)

- Added `"card"` to `TemplateNames()`.
- Added `templateCard` incorporating `ChildRulesParagraph()`.
- In `WrapTemplate`, `"card"` is explicitly refused with an informative error explaining it is a full card template, not a task wrapper.
- `matchingTemplate` treats `"card"` distinctly so that it is subjected to actual card validation rather than bypassing via the short-circuit for legacy templates.

---

## 4. Test Verification Results

All tests run with `-race`, `NOVA_TEST_NO_HOST=1`, `GOFLAGS=-mod=readonly`, and a private `GOCACHE`.

### 4.1 Sprint Unit & Race Tests
```bash
go test -v -race ./cmd/nova-sprint/...
```
- **Result:** `PASS` (6.112s)
- **Key Tests:**
  - `TestAddRefusesABriefThatFailsTheCardLint`: Validates rejection of bare brief, bare brief-file, and forbidden commands with exit code 2 and zero state mutation; validates acceptance of compliant brief and brief-file.
  - `TestAddBriefLintLinesAreBounded`: Validates `--max 3` truncation with `LINT MORE` line and `--max 0` printing all findings.
  - `TestExitCodesRuleIsTheBannersLine`: Pins the `exit-codes` rule to `banner()` in `verbs.go`.

### 4.2 Swarm Unit & Race Tests
```bash
go test -v -race ./cmd/nova-swarm/... ./internal/swarm/...
```
- **`internal/swarm`:** `PASS` (2.508s)
  - `TestChildRulesTableIsWellFormed`
  - `TestChildTemplateLintsClean`
  - `TestChildRuleMissingIsRefusedNamingIt`
  - `TestChildRuleSurvivesWrapping`
  - `TestChildScansRefuseTheCommandAndAllowTheRest`
  - `TestChildScanNamesTheLine`
  - `TestChildScansSkipTheRulesParagraph`
  - `TestChildRuleSentencesAreNotViolations`
- **`cmd/nova-swarm`:** `PASS` (6.991s)
  - `TestCardTemplateLintsCleanWithTheChildRules`
  - `TestChildRulesAreAskedForByTheFlag`
  - `TestChildScanThroughTheCommand`
  - `TestLintRulesListsTheChildRules`
  - `TestFixtureCardsKeepTheirVerdictsWithoutTheFlag`
  - `TestTheShiftsOwnCardsLintClean`

---

## 5. Edge Cases & Regressions Assessment

1. **Does `add` cleanly reject malformed briefs?**
   Yes. Verified via `TestAddRefusesABriefThatFailsTheCardLint`. Missing rules are reported with exact remedies; forbidden commands are identified by line and command; exit code is 2; and nothing is committed to the sprint store.
2. **Does `template --name card` produce a card that passes its own lint rules?**
   Yes. Manually tested and verified by `TestCardTemplateLintsCleanWithTheChildRules`: `nova-swarm template --name card | nova-swarm lint --card - --child-rules` exits 0 (`LINT OK card=... checks=58 bytes=3418 cap=12000`).
3. **Does existing card formatting still pass or are there regressions?**
   Yes. Existing control cards without `--child-rules` maintain identical behavior (`TestFixtureCardsKeepTheirVerdictsWithoutTheFlag` and `TestTheShiftsOwnCardsLintClean` pass cleanly).
4. **Is negation in prose handled correctly?**
   Yes. Phrases like `Never start a redis-server` and `Do not use git stash` are correctly recognized as non-violating prose by `childNegation` and `childScansSkipTheRulesParagraph`.

---

## 6. Conclusion & Recommendation

PR #4852 is thoroughly engineered, cleanly documented in `docs/SPEC-SWARM.md`, `docs/SPEC-SPRINT.md`, `docs/CLI.md`, and `deprecated/docs/WORKER-CARDS.md`, and backed by comprehensive class and race tests.

**Recommendation:** Proceed to land PR #4852 into `origin/sprint/foundation`.
