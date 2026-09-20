RESULT tools22-pre-1730-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1730 at head 594881168a42: nova-pulse accept --selftest: the gate's own negative control -- twelve one-edit seeds, control
PREREAD 1730 claims=9 proven=8 unproven=1 defects=2 high=1 medium=1

PR 1730, HEAD 594881168a420449bb9e3c32ac424016e3dc490c, BASE rowan/toolwork-t03-accept, MERGE-BASE 12f6027fd731 (older than 5298f6be12ea), BEHIND 0, FILES 8 production, 34 test+testdata, LINES +1514 -19

1. `accept --selftest` builds a fixture repo, runs the known-good fix (must be ACCEPT OK), then applies 12 one-edit seeds, each must be ACCEPT REJECT with its token and no other. PROVEN-BY internal/pulse/selftest_test.go:101 TestSelftestPassesOverTheShippedFixturesAndWritesTheControl — asserts PASS, every seed ok, accepted=1/1 rejected=12/12
2. `control=<id>` is sha12 of (build identity, fixture digest, cert id). PROVEN-BY internal/pulse/selftest_test.go:173 TestControlIDChangesWithTheBuild — asserts each of three inputs changes id, is 12 hex chars
3. No ACCEPT OK without a passing selftest on file for this control id. PROVEN-BY internal/pulse/selftest_test.go:211 TestAcceptOKWithoutAControlOnFileIsRefused — asserts ABSTAIN control-stale without control; runs selftest and succeeds with fixtures
4. A card touching gate sources has the base's seeds run against the head's gate (gate-weakened). PROVEN-BY internal/pulse/selftest_test.go:245 TestAcceptRejectsACardThatWeakensTheGate — asserts REJECT gate-weakened when gate fails, ACCEPT OK when gate passes
5. Each seed is one edit, counted from what git applied, never from the fixture. PROVEN-BY internal/pulse/selftest_test.go:152 TestSelftestEverySeedIsOneEdit — asserts two-line seed is REFUSED by count, exit 2
6. Passing selftest is recorded under `<root>/accept/control/<id>`. PROVEN-BY internal/pulse/selftest_test.go:101 TestSelftestPassesOverTheShippedFixturesAndWritesTheControl — asserts exactly one control file with the right name
7. Without fixtures and no control on file, ABSTAIN control-stale. PROVEN-BY internal/pulse/selftest_test.go:211 TestAcceptOKWithoutAControlOnFileIsRefused — first leg asserts ABSTAIN control-stale with no fixtures, no control
8. The binary embeds the fixture tree via go:embed, so --selftest works without --fixtures. UNPROVEN — every test loads fixtures from os.DirFS("../../cmd/nova-pulse/testdata/accept"), never from the embedded fs; no test exercises the embed path
9. The `control=` field appears in both ACCEPT OK and ACCEPT REJECT lines. PROVEN-BY-EXISTING internal/pulse/accept_test.go:199 TestAcceptOKOnAGoodFix — asserts control=[0-9a-f]{12} in OK output (moved from hardcoded control=- to computed control)

DEFECT high internal/pulse/accept.go:1231 (gateWeakened) — The head's gate binary is built with `go build -o <binDir>` where `<binDir>` (under g.runDir) is outside the wall's write set (g.wt, g.gocache, g.home). Under a real nova-sandbox wall, the build is denied, causing every card touching gate sources to ABSTAIN toolchain instead of REJECT gate-weakened. The sole test (TestAcceptRejectsACardThatWeakensTheGate) uses a fake sandbox that enforces nothing. Fix: build the binary inside the worktree (g.wt) or add binDir to execWalled's write flags.

DEFECT medium internal/pulse/accept.go:1232 (gateWeakened) — The inner binary is run through the wall, but its selftest creates directories and files under `--root <gwRoot>` (filepath.Join(g.runDir, "gw")) with `os.MkdirAll`, `os.CreateTemp` and `os.WriteFile`, all of which the outer wall denies because `gwRoot` is not in the write set. Every gate-touching card ABSTAINs at the first file operation. Fix: pass a writable subdirectory of g.wt or g.home as the inner --root, or add the inner root to the wall's write flags.

1. Why does `gateWeakened` build the binary outside the worktree (g.runDir/gate-bin) rather than inside the worktree (g.wt/gate-bin), which would already be in the wall's write set?
2. The inner selftest's `parseCardHeaderText` uses `os.CreateTemp("", ...)` — does nova-sandbox redirect $TMPDIR, or would every inner --selftest fail at that call under the real wall?
3. `GateSources` includes `cmd/nova-pulse/**` and `cmd/nova-sandbox/**` but the fixture tree lives at `cmd/nova-pulse/testdata/accept/` — is a fixture-only change (no gate code change) intentionally caught by gate-weakened?
4. `controlOnFile` uses `os.Stat` (follows symlinks) — is there a trust model for the control directory, or could a symlink from an earlier run's id to /dev/null serve as a passing record?

Left owed: I read all production files and all test files in full. The testdata seed patches and seed.txt specs were read exhaustively by a subagent. I did not individually verify every seed's patch content against the fix — I spot-checked three (fix-reverted, vacuous, wide) and they matched the description.

```
git status --short

```
git rev-parse HEAD
594881168a420449bb9e3c32ac424016e3dc490c===FILE=== card-tools22-pre-1730-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1730-r2	1	2026-09-20T19:08:31Z	2026-09-20T19:19:15Z	0	openrouter	deepseek/deepseek-v4-flash	506538	12384	0	1571584	15444	0.0312
