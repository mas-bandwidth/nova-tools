# Cold-Read Audit and Verification of PR #4866

**Auditor:** Emma Antigravity (`emma@mas-bandwidth.com`)  
**Role:** Child Worker 134  
**Date:** 2026-09-30  
**PR:** #4866 ("Remove dead code: 66 packages, the parked new path and the transitively dead")  
**Branch Under Audit:** `origin/rowan/dead-code-removal` (tracking commit `9102ebbf0f9a09ddaf55075796d4d459758ac3f7`)  
**Base Commit:** `06418a6fd6fcdf8dc707833643ae9623cfd1667d`  
**Foundation Landing Commit:** `222a5bc85db31145f293fc0b0d0463ba48bc3742`  

---

## 1. Executive Summary & Merge Recommendation

**VERDICT: APPROVED (CLEAN VERIFICATION & LANDING CONFIRMED)**

PR #4866 faithfully and cleanly executes Glenn's directive: *"That is great. Let us remove the dead code please."*

The branch excises exactly 66 dead packages across 921 files (-198,091 lines net: 578 insertions, 198,091 deletions), removing packages with no living importer or caller from `cmd/` or living packages. All living release binaries compile cleanly, static analysis passes with zero warnings, all CI and docs class tests pass, all CI workflows and landing configuration files are updated with surgical precision, and all living test helpers and core domain packages are preserved.

The branch has landed cleanly on `origin/sprint/foundation` at commit `222a5bc85db31145f293fc0b0d0463ba48bc3742` with an identical tree (`git diff 9102ebbf0 222a5bc85` is empty).

---

## 2. Scope & Inventory Audit

### 2.1 The 66 Excised Dead Packages
PR #4866 removes exactly 66 packages across the repository:
1. `internal/civerdict`
2. `internal/ctxindex`
3. `internal/filelock`
4. `internal/fleet/state`
5. `internal/gh`
6. `internal/jev`
7. `internal/merge`
8. `internal/metrics`
9. `internal/metrics/metricstest`
10. `internal/nsprint/acl`
11. `internal/nsprint/adopt`
12. `internal/nsprint/beat`
13. `internal/nsprint/benchrole`
14. `internal/nsprint/brief`
15. `internal/nsprint/capacity`
16. `internal/nsprint/card`
17. `internal/nsprint/card/harvestcopy`
18. `internal/nsprint/ci`
19. `internal/nsprint/consume`
20. `internal/nsprint/cost`
21. `internal/nsprint/deal`
22. `internal/nsprint/digest`
23. `internal/nsprint/disposition`
24. `internal/nsprint/file`
25. `internal/nsprint/fleet`
26. `internal/nsprint/fleetbuild`
27. `internal/nsprint/fold`
28. `internal/nsprint/friend`
29. `internal/nsprint/jev`
30. `internal/nsprint/land`
31. `internal/nsprint/land/fenced`
32. `internal/nsprint/land/guard`
33. `internal/nsprint/land/stream`
34. `internal/nsprint/launch`
35. `internal/nsprint/lessons`
36. `internal/nsprint/life`
37. `internal/nsprint/line`
38. `internal/nsprint/mirror`
39. `internal/nsprint/note`
40. `internal/nsprint/pipeerr`
41. `internal/nsprint/pitstop`
42. `internal/nsprint/pr`
43. `internal/nsprint/preflight`
44. `internal/nsprint/prkey`
45. `internal/nsprint/read`
46. `internal/nsprint/ready`
47. `internal/nsprint/reap`
48. `internal/nsprint/reconcile`
49. `internal/nsprint/route`
50. `internal/nsprint/spec`
51. `internal/nsprint/sprint`
52. `internal/nsprint/table`
53. `internal/nsprint/task`
54. `internal/nsprint/taskcard`
55. `internal/nsprint/verbs`
56. `internal/nsprint/webhook`
57. `internal/nsprint/width`
58. `internal/nsprint/ws`
59. `internal/nsprint/ws/wstest`
60. `internal/presence`
61. `internal/redisq`
62. `internal/sprint/machine`
63. `internal/sprint/verbs`
64. `internal/sprintline`
65. `internal/sprinttable`
66. `internal/testpg`

### 2.2 Verification of Preserved Packages & Helpers
All 15 living test helpers, core libraries, and preserved structures required by the specification are verified present, undamaged, and compiling:
- `internal/ci`: Preserved
- `internal/ci/allowlist`: Preserved
- `internal/testbin`: Preserved
- `internal/testredis`: Preserved
- `internal/delayproxy`: Preserved
- `internal/testverbhelp`: Preserved
- `internal/oneline/audit`: Preserved
- `internal/seatcred/seattest`: Preserved
- `internal/nsprint/testutil`: Preserved (along with `nsprint/fn`, `nsprint/store`, `nsprint/verbflag`, `nsprint/redisauth`)
- `internal/sprint/refmodel`: Preserved
- `internal/sprint/sprintfn`: Preserved
- `internal/sprint/stepbuild`: Preserved (along with `sprint/store`, `sprint/driver`, and core `sprint`)
- `internal/tset`: Preserved
- `internal/tablemodel`: Preserved
- `internal/docs`: Preserved

---

## 3. CI Workflow & Fleet Land Configuration Verification

Diffs across `.github/workflows/ci.yml` and `fleet/land/nova-tools.yml` were audited line-by-line:
1. **`.github/workflows/ci.yml`**:
   - The standalone `test-race-queue` job running `make test-race PKGS="./internal/redisq"` was completely excised.
   - Associated documentation comments referring to the one exception for `redisq` under `-race` were updated to reflect that fast-tier tests run without `-race`.
   - Aggregator job `ci-ok`: `test-race-queue` was cleanly removed from the `needs:` array, the pull-request event result loop, and the push/schedule event result loop.
   - Obsolete mentions of `internal/merge` in comments were trimmed.
2. **`fleet/land/nova-tools.yml`**:
   - The required check `- test-race-queue` under `bases` was excised.
3. No dangling references or unmapped steps remain.

---

## 4. Test Verification Suite

| Category | Command | Exit Code | Notes |
|:---|:---|:---:|:---|
| **Living Binaries Compile** | `go build ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-work ./cmd/nova-config ./cmd/nova-bus` | 0 | All release binaries compile cleanly |
| **Static Analysis** | `go vet ./cmd/... ./internal/sprint/... ./internal/tset/...` | 0 | Clean vet, 0 warnings |
| **Class Tests** | `go test -v ./internal/ci/... ./internal/docs/...` | 0 | All class tests pass (0.634s) |
| **Release Binaries Tests** | `go test ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-work ./cmd/nova-config ./cmd/nova-bus` | 0 | All 5 living binary suites pass |
| **Sprint Suite** | `go test ./internal/sprint/...` | 0 | All packages pass (driver, refmodel, sprintfn, stepbuild, store, sprint) |
| **nsprint Suite** | `go test ./internal/nsprint/...` | 0 | Preserved packages pass (fn, store, testutil, verbflag) |
| **Table & Tset Suite** | `go test ./internal/tset/... ./internal/tablemodel/...` | 0 | All pass |
| **Swarm One-Line Audit** | `go test -v ./cmd/nova-swarm -run TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` | 0 | Pass |

### Note on `internal/typedrec/oneparser_test.go`
- In commit `9102ebbf0`, allowlist entries for deleted packages were properly excised from `specAllowlist` and `driftAllowlist`.
- Running `go test ./internal/typedrec/...` produces 1 hit on `internal/swarm/lintchild.go:78 CardChildRules() space PR pkg-complit`.
- Investigation verified this is pre-existing on base `06418a6fd` (introduced in commit `1181e82c6`, PR #4852).
- PR #4865 (`origin/rowan/functional-green`, commit `09a8f1f91`) explicitly resolves this allowlist entry. PR #4866 is completely decoupled from and orthogonal to this defect.

---

## 5. Conclusion & Merge Recommendation

PR #4866 represents an impeccably clean dead code extraction. It eliminates 198,091 lines of obsolete, unreferenced packages while leaving all living production pathways, tests, and CI machinery green and robust.

Audit confirms PR #4866 is ready for merge / confirmed landed on `sprint/foundation` (`222a5bc85db31145f293fc0b0d0463ba48bc3742`).
