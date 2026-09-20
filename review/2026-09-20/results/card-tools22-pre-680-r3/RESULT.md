RESULT tools22-pre-680-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#680 at head d44981302966: nova-tools #675 fixed with its red test first: nova-pulse cut --validate-contract: preflight lo
PREREAD 680 claims=4 proven=1 unproven=0 defects=3 high=0

PR 680
HEAD d44981302966d82e4f614bd6ff05e74bf004e92b
BASE dev
MERGE-BASE 7e7f185203ccc9ed20674a3c021348a9718a8f21
BEHIND 99
FILES 4 production, 1 test
LINES +107 -17

1. Add `--validate-contract` flag to `nova-pulse cut` to preflight candidate locators before writing any card file
2. When enabled, run `gh repo view <owner/repo>` on each candidate's locator; if non-zero, refuse with reason and exit 2 without writing cards
3. Return exit code 2 on refusal, 0 on success, 1 when any candidate was skipped
4. Prevent dead repos from spending admission and scaffold before the cut abstains (issue #675)

CLAIMS
1. PROVEN-BY internal/pulse/cut_test.go:97 TestCutValidateContractRefusesUnresolvableLocator — asserts that --validate-contract refuses unresolvable locators, prints CUT REFUSED with reason, exits 2, and writes no card file

DEFECTS
MEDIUM internal/pulse/cut.go:177 — locatorUnresolvable calls `gh repo view` without verifying gh CLI is installed — if gh is missing, stderr shows command-not-found instead of "does not resolve" which breaks parsers expecting the documented error format
MEDIUM internal/pulse/cut.go:182 — timeout for gh call is hardcoded 120s with no user override — slow or hung gh invocations block the entire cut operation with no way to tune it via CLI or env
LOW docs/CLI.md:806 — error line CUT REFUSED locator=<owner/repo> says "check gh auth and the repo name" but does not mention gh must be installed or on PATH, which is the primary failure mode

QUESTIONS
1. Is gh CLI a hard runtime dependency for nova-pulse when --validate-contract is used, or should the tool fall back to skipping candidates when gh is unavailable?
2. Should there be an env var or flag to configure the locator resolution timeout instead of the hardcoded 120s?
3. The test fakes gh with Exit:1 but does not verify what happens if gh returns exit code 127 (not found) — is that case intentionally left to the OS error?

Left owed
No production files omitted; read full contents of cmd/nova-pulse/main.go, internal/pulse/cut.go, internal/pulse/cut_test.go, docs/CLI.md, docs/SPEC-PULSE.md, internal/pulse/cli.go. No test files omitted.

git status --short
M repo
git rev-parse HEAD
d44981302966d82e4f614bd6ff05e74bf004e92b
