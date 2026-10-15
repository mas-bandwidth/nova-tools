; The nova-tools roadmap: the data docs/ROADMAP.md is generated from.
; Same shape as nova-sprint's docs/roadmap.sexp (internal/roadmapdoc's package comment):
; data, never evaluated. Edit this file, then run `make roadmap`; never edit docs/ROADMAP.md by hand.
; (The hand-written ROADMAP.md at the repository root is the release plan; this file holds the
; planned work and bugs: cards moved out of the sprint, and the open issues, moved here in the
; v1.4.0 freeze, 2026-10-15, when ideas and bugs moved onto the roadmap for good.)
(roadmap "v1"
 :title "nova-tools roadmap"
 :text "Planned work and known bugs for nova-tools. Each item names its origin (a sprint card moved out of the sprint on 2026-10-15, or a GitHub issue moved here in the v1.4.0 freeze), its release where one is set, and any pushed work to resume from. Landed work is in git. The release plan is the hand-written ROADMAP.md at the root."

 :groups
 (
  (group "github-issues" :title "Moved from GitHub issues on 2026-10-15"
   :text "Open GitHub issues of mas-bandwidth/nova-tools, moved here in the v1.4.0 freeze (Glenn, 2026-10-15): ideas and bugs live on the roadmap, never as GitHub issues. Each item names its issue; the issue is closed with a pointer here.")
  (group "export-v13-blockers" :title "Moved out of the sprint on 2026-10-15: v13-blockers"
   :text "Open cards of sprint stream v13-blockers, moved out on 2026-10-15 when Glenn scoped the sprint down; one item per card with its pushed heads and any head not on origin.")
  (group "export-nova-local" :title "Moved out of the sprint on 2026-10-15: nova-local"
   :text "Open cards of sprint stream nova-local, moved out on 2026-10-15 when Glenn scoped the sprint down; one item per card with its pushed heads and any head not on origin.")
  (group "export-tool-review-2026-10-15" :title "Moved out of the sprint on 2026-10-15: tool-review-2026-10-15"
   :text "Open cards of sprint stream tool-review-2026-10-15, moved out on 2026-10-15 when Glenn scoped the sprint down; one item per card with its pushed heads and any head not on origin.")
  (group "export-v15-features" :title "Moved out of the sprint on 2026-10-15: v15-features"
   :text "Open cards of sprint stream v15-features, moved out on 2026-10-15 when Glenn scoped the sprint down; one item per card with its pushed heads and any head not on origin.")
  (group "export-v15-merging" :title "Moved out of the sprint on 2026-10-15: target v1.5"
   :text "Cards that were merging when Glenn moved the rest into v1.5 (2026-10-15); one item per card with its pushed heads and any head not on origin.")
  (group "after-rocketnet" :title "Resume after the rocketnet milestone"
   :text "Cards moved out of the sprint on 2026-10-15 when Glenn said: \"move the schema cards out to the roadmap. We will continue them only after we have rocketnet in the bag.\" One item per card with its pushed heads and any head not on origin.")
 )

 :items
 (
  (item "issue-199" :group "github-issues" :area "nova-tools"
   :title "Respect intended draft release and publication boundary during artifac"
   :text "## Observed release orchestration mismatch For v0.14.0, the coordinator created draft release387729032 with reviewed notes. The existing release workflow34724270698 attempt2 passed its tests and created a separate published release387734569 at23:27:37Z with56assets and generated notes; the draft remained asset-free. The expected draft upload/then-publication sequence did not happen. The workflow a"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/199, opened 2026-09-12; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "199" :source "https://github.com/mas-bandwidth/nova-tools/issues/199"))
  (item "issue-917" :group "github-issues" :area "nova-tools"
   :title "Muse (Zen contributor-free) stalls under fleet-wide concurrency: reque"
   :text "Stress test 2026-09-17 03:30-03:55Z: ~100 Muse cards launched across hulk (38), vision (26), space (17), mini (4) plus 25 on the Studio, all on opencode/muse-spark-1.3-contributor-free through the swarm seats. Observed: hulk and vision produced 0 results in 13 minutes with load 0.5-2.0 (the harnesses idle, waiting on the model); their logs froze mid-tool-call (Read repo/docs/SPEC-PULSE.md) with ou"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/917, opened 2026-09-17; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "917" :source "https://github.com/mas-bandwidth/nova-tools/issues/917"))
  (item "issue-1282" :group "github-issues" :area "nova-tools"
   :title "scripts/bench-hygiene.sh (dev, after #1240) lacks the HOME guard: runs"
   :text "Found by running the coordinator's hostile-input test (tests/bench-hygiene-hostile.sh on the review PR #1263) against dev's scripts/bench-hygiene.sh on hulk before installing it: 36 of 40 cases pass; the four HOME cases fail. With `HOME=`, `HOME=/`, `HOME=relative/home` or `HOME=/onlyone` the script proceeds (roots resolve to nonsense, nothing was deleted in the fixture, but the guard is the point"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1282, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1282" :source "https://github.com/mas-bandwidth/nova-tools/issues/1282"))
  (item "issue-1401" :group "github-issues" :area "nova-tools"
   :title "nova-bus: two sessions of one participant share a cursor, so the secon"
   :text "Found in dogfood round 5, v0.16.0-dev.2b6d6ba5. Two sessions of the same participant were on the bus at once: the main session on one machine and a second working session on a laptop, both `Rowan`, both writing in `from-rowan/`. The roster tolerates the session suffix in `From:`/`To:`, which is good. The cursor does not: it is one per name. So from the second session: - `inbox --as Rowan --advance"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1401, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1401" :source "https://github.com/mas-bandwidth/nova-tools/issues/1401"))
  (item "issue-1432" :group "github-issues" :area "nova-tools"
   :title "darwin benches measure Spotlight as much as the tests: exclude the wor"
   :text "Found while measuring `cmd/nova-wake` on the Air (darwin/arm64) for the test-duration budget work (#1411, #1428). Mid-measurement, `ps` on the Air: ``` 105.6 mds_stores 34.5 mds 28.0 mdworker_shared -s 24.8 mdworker_shared -s 21.0 mdworker_shared -s ``` Load average 6.2 on an 8-core laptop, with **no test running at that instant**. Spotlight is indexing the files the suite creates — the clone, the"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1432, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1432" :source "https://github.com/mas-bandwidth/nova-tools/issues/1432"))
  (item "issue-1451" :group "github-issues" :area "nova-tools"
   :title "Missing-flag refusals: 116 of 187 verbs do not name the door (`; run: "
   :text "Found in dogfood round 6 on darwin/arm64, v0.16.0-dev.33feb46c: the hostile call the release round asks for, run mechanically. Every verb the binaries declare (the ledger's `--tools` list, 227 rows; help, version and the verbless forms left out) was run bare, with no flags, stdin closed, in an empty scratch directory under a 10 s alarm: 193 calls. **What is right:** 187 of 193 exit 2. None hung, n"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1451, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1451" :source "https://github.com/mas-bandwidth/nova-tools/issues/1451"))
  (item "issue-1452" :group "github-issues" :area "nova-tools"
   :title "nova-sandbox worktree: an origin that is an ssh host alias reads as \"t"
   :text "Found in dogfood round 6, v0.16.0-dev.33feb46c, darwin/arm64, on a real PR (#1444). **1. An ssh-alias origin is reported as a network failure.** The checkout's origin is `git@github-rowan:mas-bandwidth/nova-tools.git`, where `github-rowan` is a `Host` alias in `~/.ssh/config` (HostName github.com, a dedicated key). That is how a machine carries a second GitHub identity, and it is how this fleet's "
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1452, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1452" :source "https://github.com/mas-bandwidth/nova-tools/issues/1452"))
  (item "issue-1455" :group "github-issues" :area "nova-tools"
   :title "help example lines: 28 of 61 runnable lines exit 2 when pasted from a "
   :text "Found in dogfood round 6, darwin/arm64, v0.16.0-dev.33feb46c. Every `example:` line the 21 tools print (90 lines) was taken from `<tool> help` and, where safe, run as printed from the root of a checkout, stdin closed, 20 s limit. 29 were skipped by rule (anything that pushes, publishes, contacts the forge, acts on a machine, or needs a key). Of the 61 run: 31 exit 0, 1 exits 1, 1 exits 3 (nova-dec"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1455, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1455" :source "https://github.com/mas-bandwidth/nova-tools/issues/1455"))
  (item "issue-1468" :group "github-issues" :area "nova-tools"
   :title "nova-self-talk: the three plainest first-person absolutes return claim"
   :text "Measured 2026-09-18 under WSL2 on v0.16.0-dev.33feb46c, on a three-line file: ``` I cannot ever get this right. I always break the build. Nothing I do works. ``` `nova-self-talk <file>` → `SELFTALK OK files=1 claims=0 standing=0 installations=0 dated=0` exit 0. The help defines the first class as *a first-person claim, in negative vocabulary, about what the writer permanently IS or permanently CAN"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1468, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1468" :source "https://github.com/mas-bandwidth/nova-tools/issues/1468"))
  (item "issue-1469" :group "github-issues" :area "nova-tools"
   :title "nova-sandbox policy on linux prints the darwin sandbox-exec profile te"
   :text "Measured 2026-09-18 under WSL2 (kernel 6.18.33.2, landlock) on v0.16.0-dev.33feb46c: `HOME=<job home> nova-sandbox policy --read ~/sdk --write <scratch> --net-deny` answers `POLICY OK backend=landlock read=1 write=1 bytes=7278 gpu=none` and then prints the text beginning `;; nova-sandbox — darwin sandbox-exec profile, TEMPLATE.` The verdict names landlock; the body is the macOS profile, which no l"
   :why "Kind: bug."
   :date "2026-10-15"
   :release "v1.5"
   :origin "https://github.com/mas-bandwidth/nova-tools/issues/1469, opened 2026-09-18; moved here 2026-10-15 in the v1.4.0 freeze"
   :kept (:kind "bug" :github-issue "1469" :source "https://github.com/mas-bandwidth/nova-tools/issues/1469"))
  (item "local-config-provider-endpoint" :group "export-nova-local" :area "nova-tools"
   :title "local-config-provider-endpoint"
   :text "RESULT: local-config-provider-endpoint sha=<sha12> tier: pro REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: nova-config's provider kind has the field endpoint, Source: Glenn, ~14:13Z 2026-10-15: \"Please add the work for nova-local to the current sprint,... Work audited 2026-10-15. Pushed heads: da9aa4e5dae438df9f4f3e2874f4cf6bf6496290 (hetzner2; on origin: sprint/local-config-provider-endpoint.w1.g3.e21); 2ea176f86e2b5b054348112537c7dd810db1c18a (amd-2; on origin: sprint/local-config-provider-endpoint.w3.g1.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "local-config-provider-endpoint" :sprint-stream "nova-local" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "local-spec-fleet-provider-per-machine" :pushed-head "2ea176f86e2b5b054348112537c7dd810db1c18a" :pushed-branch "sprint/local-config-provider-endpoint.w3.g1.e21"))

  (item "local-serve-prints-the-fleet-rows" :group "export-nova-local" :area "nova-tools"
   :title "local-serve-prints-the-fleet-rows"
   :text "RESULT: local-serve-prints-the-fleet-rows sha=<sha12> tier: pro REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: nova-local serve over a tailnet --base with --tier, Source: Glenn, ~14:13Z 2026-10-15: \"Please add the work for nova-local to the current sprint,... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "local-serve-prints-the-fleet-rows" :sprint-stream "nova-local" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "local-config-provider-endpoint" :pushed-head "" :pushed-branch ""))

  (item "tr15-config-r4" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. Pushed heads: 13f87e2c33e9e2ae39bb0eff821e44855e8a6d21 (superman; on origin: sprint/tr15-config-r4.w1.g5.e21); 86fcd8728023af6bc58a5fb9e8492fc527a4f0b4 (vision; on origin: sprint/tr15-config-r4.w2.g3.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-config-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "review" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "86fcd8728023af6bc58a5fb9e8492fc527a4f0b4" :pushed-branch "sprint/tr15-config-r4.w2.g3.e21"))

  (item "tr15-config-r5" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-config-r5" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-local-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. Pushed heads: 6eec8cf57ebd08d09f61805fd24952973cf2f675 (emma; on origin: sprint/tr15-local-r2.w1.g1.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-local-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "6eec8cf57ebd08d09f61805fd24952973cf2f675" :pushed-branch "sprint/tr15-local-r2.w1.g1.e21"))

  (item "tr15-memory-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. Pushed heads: 1c85b1065b93a1f77a52b8560c0f41d31d4eab06 (emma; on origin: sprint/tr15-memory-r2.w1.g1.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-memory-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "1c85b1065b93a1f77a52b8560c0f41d31d4eab06" :pushed-branch "sprint/tr15-memory-r2.w1.g1.e21"))

  (item "tr15-sandbox-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. Pushed heads: 8532dea537881ed2735212d7ff59286aab5cadf7 (emma; on origin: sprint/tr15-sandbox-r2.w1.g1.e21, sprint/tr15-sandbox-r2.w1.g2.e21). Also preserved from batman (retired 2026-10-15): 306af4891385a0656f3fc41f46bfafd4ca5ce1e9 (not otherwise on origin), nova-tools branch preserve/tr15-sandbox-r2-batman."
   :date "2026-10-15"
   :kept (:sprint-card "tr15-sandbox-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "review" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "8532dea537881ed2735212d7ff59286aab5cadf7" :pushed-branch "sprint/tr15-sandbox-r2.w1.g1.e21"))

  (item "tr15-sprint-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-sprint-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-sprint-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-swarm-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-swarm-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-swarm-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-table-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-table-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-table-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-tokens-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-tokens-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-tokens-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-up-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-up-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-up-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-up-r4" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-up-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "review" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-update-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-update-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-update-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-version-r2" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/tr15-version-r2.w1.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-version-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "" :pushed-branch ""))

  (item "tr15-config-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-config AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-config-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-config-r1,tr15-config-r2,tr15-config-r3,tr15-config-r4,tr15-config-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-friend-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-friend AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-friend-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-friend-r1,tr15-friend-r2,tr15-friend-r3,tr15-friend-r4,tr15-friend-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-hook-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-hook AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-hook-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-hook-r1,tr15-hook-r2,tr15-hook-r3,tr15-hook-r4,tr15-hook-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-local-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-local AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-local-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-local-r1,tr15-local-r2,tr15-local-r3,tr15-local-r4,tr15-local-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-memory-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-memory AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-memory-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-memory-r1,tr15-memory-r2,tr15-memory-r3,tr15-memory-r4,tr15-memory-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-redis-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-redis AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-redis-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-redis-r1,tr15-redis-r2,tr15-redis-r3,tr15-redis-r4,tr15-redis-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-sandbox-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-sandbox AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-sandbox-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-sandbox-r1,tr15-sandbox-r2,tr15-sandbox-r3,tr15-sandbox-r4,tr15-sandbox-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-secrets-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-secrets AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-secrets-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-secrets-r1,tr15-secrets-r2,tr15-secrets-r3,tr15-secrets-r4,tr15-secrets-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-self-talk-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-self-talk AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-self-talk-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-self-talk-r1,tr15-self-talk-r2,tr15-self-talk-r3,tr15-self-talk-r4,tr15-self-talk-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-sprint-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-sprint AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-sprint-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-sprint-r1,tr15-sprint-r2,tr15-sprint-r3,tr15-sprint-r4,tr15-sprint-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-swarm-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-swarm AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-swarm-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-swarm-r1,tr15-swarm-r2,tr15-swarm-r3,tr15-swarm-r4,tr15-swarm-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-table-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-table AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-table-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-table-r1,tr15-table-r2,tr15-table-r3,tr15-table-r4,tr15-table-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-tokens-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-tokens AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-tokens-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-tokens-r1,tr15-tokens-r2,tr15-tokens-r3,tr15-tokens-r4,tr15-tokens-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-up-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-up AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-up-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-up-r1,tr15-up-r2,tr15-up-r3,tr15-up-r4,tr15-up-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-update-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-update AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-update-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-update-r1,tr15-update-r2,tr15-update-r3,tr15-update-r4,tr15-update-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-version-summary" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "nova-version AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-version-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-version-r1,tr15-version-r2,tr15-version-r3,tr15-version-r4,tr15-version-r5" :pushed-head "" :pushed-branch ""))

  (item "tr15-rank-and-fix" :group "export-tool-review-2026-10-15" :area "nova-tools"
   :title "tr15-rank-and-fix"
   :text "RESULT: tr15-rank-and-fix sha=750b0842b883 tier: heavy REPO: mas-bandwidth/nova-tools BASE: v1.3/comms KIND: research PRIORITY: normal STREAM: tool-review-2026-10-15 Source: Glenn, 14:00Z 2026-10-15: \"And then I want you to create cards for small things we can... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "tr15-rank-and-fix" :sprint-stream "tool-review-2026-10-15" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "tr15-bus-summary,tr15-cairn-summary,tr15-check-summary,tr15-ci-summary,tr15-config-summary,tr15-decide-summary,tr15-doctor-summary,tr15-friend-summary,tr15-fuse-summary,tr15-hook-summary,tr15-local-summary,tr15-memory-summary,tr15-redis-summary,tr15-sandbox-summary,tr15-secrets-summary,tr15-self-talk-summary,tr15-swarm-summary,tr15-table-summary,tr15-tokens-summary,tr15-up-summary,tr15-update-summary,tr15-version-summary,tr15-sprint-summary" :pushed-head "" :pushed-branch ""))

  (item "heartbeat-challenges-only-after-silence" :group "export-v13-blockers" :area "nova-tools"
   :title "heartbeat-challenges-only-after-silence"
   :text "RESULT: heartbeat-challenges-only-after-silence sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: nova-hook serve challenges a friend only Source: (card names no SOURCE... Work audited 2026-10-15. Pushed heads: 60b1227e708d78870825daa7698668030d90b500 (stella; on origin: sprint/heartbeat-challenges-only-after-silence.w1.g1.e21, sprint/heartbeat-challenges-only-after-silence.w6.g1.e21); e021896ae2f07979ac31dc92d3076272abcd93e5 (stella; on origin: sprint/heartbeat-challenges-only-after-silence.w1.g1.e21, sprint/heartbeat-challenges-only-after-silence.w6.g1.e21); 4140084539c8a9192435c57df08d239f48354580 (emma; on origin: sprint/heartbeat-challenges-only-after-silence.w7.g2.e21). Not on origin: not on origin: 3a1b432ef728960cbd0eb2138e0778861159fc92 (rowan, sprint/heartbeat-challenges-only-after-silence.w8.g1.e21). Working branch, not on origin: sprint/heartbeat-challenges-only-after-silence.w9.g1.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "heartbeat-challenges-only-after-silence" :sprint-stream "v13-blockers" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "" :pushed-head "4140084539c8a9192435c57df08d239f48354580" :pushed-branch "sprint/heartbeat-challenges-only-after-silence.w7.g2.e21"))

  (item "tools-pkg-swarm-dead-code" :group "export-v13-blockers" :area "nova-tools"
   :title "tools-pkg-swarm-dead-code"
   :text "RESULT: tools-pkg-swarm-dead-code sha=<sha12> tier: heavy REPO: mas-bandwidth/nova-tools BASE: v1.3/comms SOURCE: Glenn, 2026-10-15 13:16Z: card the missing contraction Source: Glenn, 2026-10-15 13:16Z: card the missing contraction and dead-code pieces as blockers;... Work audited 2026-10-15. Pushed heads: c1a0113074196b20b03e03a46e91c429a9425ec7 (rowan; on origin: sprint/tools-pkg-swarm-dead-code.w1.g1.e21); 4c894d538c4e07ca5ec8269523f8de61cc5002d4 (rowan; on origin: sprint/tools-pkg-swarm-dead-code.w2.g1.e21, sprint/tools-pkg-swarm-dead-code.w3.g1.e21); 065a9f3aaa3810486e024e89a2d9fed6a5c37860 (rowan; on origin: sprint/tools-pkg-swarm-dead-code.w3.g1.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "tools-pkg-swarm-dead-code" :sprint-stream "v13-blockers" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "swarm-tools-takes-the-sprint-hunks" :pushed-head "065a9f3aaa3810486e024e89a2d9fed6a5c37860" :pushed-branch "sprint/tools-pkg-swarm-dead-code.w3.g1.e21"))

  (item "friend-daemon-tells-state-transitions-on-the-bus" :group "export-v13-blockers" :area "nova-tools"
   :title "friend-daemon-tells-state-transitions-on-the-bus"
   :text "RESULT: friend-daemon-tells-state-transitions-on-the-bus sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: the friend daemon tells the Source: Glenn 13:57Z 2026-10-15: \"Event driven push notifications are vastly superior to... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "friend-daemon-tells-state-transitions-on-the-bus" :sprint-stream "v13-blockers" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "friend-status-transitions-push-to-the-coordinator" :pushed-head "" :pushed-branch ""))

  (item "members-publish-their-gate-facts" :group "export-v13-blockers" :area "nova-tools"
   :title "members-publish-their-gate-facts"
   :text "RESULT: members-publish-their-gate-facts sha=<sha12> tier: pro CARRY: members-publish-their-gate-facts attempt 5 head=e5f0c0df9bdbc72110e26f895ed67c1ec3e3db91 REPO: Source: Glenn 13:51Z 2026-10-15: \"Please card all the optimizations for architectures.\" The... Work audited 2026-10-15. Pushed heads: 96adf98da9835407a26dc60497554bcad79b46d7 (hetzner2; on origin: sprint/members-publish-their-gate-facts.w1.g3.e21); 26b3d0435222bbd4511ae0e58ca3520c02ab2e3f (b2; on origin: sprint/members-publish-their-gate-facts.w3.g1.e21); e5f0c0df9bdbc72110e26f895ed67c1ec3e3db91 (batman; on origin: sprint/members-publish-their-gate-facts.w4.g1.e21). Working branch, not on origin: sprint/members-publish-their-gate-facts.w7.g3.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "members-publish-their-gate-facts" :sprint-stream "v13-blockers" :sprint-column "working" :tier "heavy" :sprint-base "v1.3/comms" :needs "" :pushed-head "e5f0c0df9bdbc72110e26f895ed67c1ec3e3db91" :pushed-branch "sprint/members-publish-their-gate-facts.w4.g1.e21"))

  (item "nova-swarm-grant-is-a-sprintwire-client" :group "export-v13-blockers" :area "nova-tools"
   :title "nova-swarm-grant-is-a-sprintwire-client"
   :text "RESULT: nova-swarm-grant-is-a-sprintwire-client sha=<sha12> tier: pro REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: `nova-swarm grant add --bench <b> --to <name> Source: the push audit of 2026-10-15 (nova-tools abd4a6d, nova-sprint 3e51974), item 7. The card... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "nova-swarm-grant-is-a-sprintwire-client" :sprint-stream "v13-blockers" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "bench-grants-owned-by-the-sprint-server" :pushed-head "" :pushed-branch ""))

  (item "members-produce-finish-scope-evidence" :group "export-v13-blockers" :area "nova-tools"
   :title "members-produce-finish-scope-evidence"
   :text "RESULT: members-produce-finish-scope-evidence sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: a machine member's ok finish carries Source: the coordinator, epoch 21 (2026-10-15): the nova-sprint card... Work audited 2026-10-15. Pushed heads: ed2b7aab96ff660688fbf2e87df83d122b3e43e1 (stella; on origin: sprint/members-produce-finish-scope-evidence.w1.g1.e21, sprint/members-produce-finish-scope-evidence.w1.g2.e21). "
   :date "2026-10-15"
   :kept (:sprint-card "members-produce-finish-scope-evidence" :sprint-stream "v13-blockers" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "" :pushed-head "ed2b7aab96ff660688fbf2e87df83d122b3e43e1" :pushed-branch "sprint/members-produce-finish-scope-evidence.w1.g1.e21"))

  (item "every-merge-builds-the-release" :group "export-v13-blockers" :area "nova-tools"
   :title "every-merge-builds-the-release"
   :text "RESULT: every-merge-builds-the-release sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms KIND: feature PRIORITY: blocker STREAM: v13-blockers Source: The owner, 2026-10-15, approving four layers for the fast release pipeline: \"no more... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "every-merge-builds-the-release" :sprint-stream "v13-blockers" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "" :pushed-head "" :pushed-branch ""))

  (item "release-push-deploy" :group "export-v13-blockers" :area "nova-tools"
   :title "release-push-deploy"
   :text "RESULT: release-push-deploy sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms KIND: feature PRIORITY: blocker STREAM: v13-blockers STOP: Source: The owner, 2026-10-15, approving four layers for the fast release pipeline, layer 3:... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "release-push-deploy" :sprint-stream "v13-blockers" :sprint-column "waiting" :tier "" :sprint-base "v1.3/comms" :needs "every-merge-builds-the-release" :pushed-head "" :pushed-branch ""))

  (item "b-hook-serve-takes-the-pass-when-every-changes" :group "export-v13-blockers" :area "nova-tools"
   :title "b-hook-serve-takes-the-pass-when-every-changes"
   :text "RESULT: hook-serve-takes-the-pass-when-every-changes sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms KIND: bugfix PRIORITY: blocker STREAM: Source: the coordinator, 2026-10-15, relaying the friend's finding (paraphrased); and the owner,... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/b-hook-serve-takes-the-pass-when-every-changes.w1.g2.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :kept (:sprint-card "b-hook-serve-takes-the-pass-when-every-changes" :sprint-stream "v13-blockers" :sprint-column "working" :tier "" :sprint-base "v1.3/comms" :needs "" :pushed-head "" :pushed-branch ""))

  (item "swarm-tools-takes-the-depends-grammar" :group "export-v13-blockers" :area "nova-tools"
   :title "swarm-tools-takes-the-depends-grammar"
   :text "RESULT: swarm-tools-takes-the-depends-grammar sha=<sha12> tier: heavy REPO: mas-bandwidth/nova-tools BASE: v1.3/comms KIND: feature PRIORITY: high STREAM: v13-blockers Source: coordinator, 2026-10-15. nova-sprint#413 (card a-need-names-what-it-consumes) rewrote... Work audited 2026-10-15. No pushed head. "
   :date "2026-10-15"
   :kept (:sprint-card "swarm-tools-takes-the-depends-grammar" :sprint-stream "v13-blockers" :sprint-column "ready" :tier "" :sprint-base "v1.3/comms" :needs "" :pushed-head "" :pushed-branch ""))

  (item "bus-send-force-reaches-a-down-friend" :group "export-v15-features" :area "nova-tools"
   :title "bus-send-force-reaches-a-down-friend"
   :text "RESULT: bus-send-force-reaches-a-down-friend sha=<sha12> tier: heavy REPO: mas-bandwidth/nova-tools BASE: v1.5 KIND: feature PRIORITY: high STREAM: v15-features STOP: Source: the owner, 2026-10-15, verbatim: \"You should add a \"--force\" flag that lets you send... Work audited 2026-10-15. No pushed head. Working branch, not on origin: sprint/bus-send-force-reaches-a-down-friend.w1.g2.e21 (emma, working when dropped). "
   :date "2026-10-15"
   :release "v1.7"
   :kept (:sprint-card "bus-send-force-reaches-a-down-friend" :sprint-stream "v15-features" :sprint-column "working" :tier "" :sprint-base "v1.5" :needs "" :pushed-head "" :pushed-branch ""))

  (item "member-stage-refuses-a-base-without-its-landed-needs" :group "export-v15-merging" :area "nova-tools"
   :title "member-stage-refuses-a-base-without-its-landed-needs"
   :text "RESULT: member-stage-refuses-a-base-without-its-landed-needs sha=<sha12> tier: frontier REPO: mas-bandwidth/nova-tools BASE: v1.3/comms STOP: the Source: Glenn 13:18Z: \"card all 14 check and lint proposals plus the 4 loosenings as blockers, top-ranked first\"; the sprint-21 failure review (epoch 21, nova-sprint log and card Heads: f43e95ecf8ca (emma; origin sprint/member-stage-refuses-a-base-without-its-landed-needs.w1.g4.e21); 50aa30932920 (emma; origin sprint/member-stage-refuses-a-base-without-its-landed-needs.w2.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "member-stage-refuses-a-base-without-its-landed-needs" :sprint-stream "v13-blockers" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "packet-carries-the-landed-state-of-its-needs" :pushed-head "50aa30932920cebd2151f99945321691f9520e44" :pushed-branch "sprint/member-stage-refuses-a-base-without-its-landed-needs.w2.g1.e21"))

  (item "local-native-launches-at-the-endpoint" :group "export-v15-merging" :area "nova-tools"
   :title "local-native-launches-at-the-endpoint"
   :text "RESULT: local-native-launches-at-the-endpoint sha=<sha12> tier: pro CARRY: local-native-launches-at-the-endpoint attempt 4 Source: Glenn, ~14:13Z 2026-10-15: \"Please add the work for nova-local to the current sprint, low priority.\"; the nova-tools roadmap item nova-local-provider-per-machine (\"Restore Heads: d5090c6309dd (hetzner2; origin sprint/local-native-launches-at-the-endpoint.w3.g1.e21); 5859fdd34869 (vision; origin sprint/local-native-launches-at-the-endpoint.w6.g1.e21). Also preserved from batman (retired 2026-10-15): d1ab05bd1ac3f1ce2de510c925a717242f67fccd (not otherwise on origin), nova-tools branch preserve/local-native-launches-at-the-endpoint-batman."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "local-native-launches-at-the-endpoint" :sprint-stream "nova-local" :sprint-column "merging" :tier "pro" :sprint-base "v1.3/comms" :needs "local-spec-fleet-provider-per-machine" :pushed-head "5859fdd34869bc40bc1de8886118a8c3565762ca" :pushed-branch "sprint/local-native-launches-at-the-endpoint.w6.g1.e21"))

  (item "tr15-decide-summary" :group "export-v15-merging" :area "nova-tools"
   :title "nova-decide AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 126fdb00df53 (amd-2; origin sprint/tr15-decide-summary.w1.g1.e21); d19fddc4b778 (superman; origin sprint/tr15-decide-summary.w2.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-decide-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-decide-r1,tr15-decide-r2,tr15-decide-r3,tr15-decide-r4,tr15-decide-r5" :pushed-head "d19fddc4b7786d834720f881ed59e54d4d988994" :pushed-branch "sprint/tr15-decide-summary.w2.g1.e21"))

  (item "tr15-doctor-summary" :group "export-v15-merging" :area "nova-tools"
   :title "nova-doctor AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: b27e93d9f028 (amd-3; origin sprint/tr15-doctor-summary.w1.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-doctor-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-doctor-r1,tr15-doctor-r2,tr15-doctor-r3,tr15-doctor-r4,tr15-doctor-r5" :pushed-head "b27e93d9f0284fd6d13e0ade5a463feae035b352" :pushed-branch "sprint/tr15-doctor-summary.w1.g1.e21"))

  (item "tr15-fuse-summary" :group "export-v15-merging" :area "nova-tools"
   :title "nova-fuse AI-usability review summary, 2026-10-15"
   :text "Reviews: <n> (R1..Rn, in byte order of file name) Build: <each review's Build:, deduplicated> Score: <median of the reviews' scores>/10 (range Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: fdfc98ffd2da (batman; origin sprint/tr15-fuse-summary.w1.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-fuse-summary" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-fuse-r1,tr15-fuse-r2,tr15-fuse-r3,tr15-fuse-r4,tr15-fuse-r5" :pushed-head "fdfc98ffd2da2602fbca78d84dcbb56a43a6d8f4" :pushed-branch "sprint/tr15-fuse-summary.w1.g1.e21"))

  (item "tr15-friend-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: f0c76f2b50d8 (amd-2; origin sprint/tr15-friend-r4.w1.g5.e21); 5aaa1f89aff3 (amd-2; origin sprint/tr15-friend-r4.w3.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-friend-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "5aaa1f89aff3506cefcd32fdfc214269e6da94b2" :pushed-branch "sprint/tr15-friend-r4.w3.g1.e21"))

  (item "tr15-hook-r2" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 955954041edd (emma; origin sprint/tr15-hook-r2.w1.g1.e21); not on origin: cbf72ea09af31efd4862dd1b77a89c7954e59cd9 (emma, sprint/tr15-hook-r2.w3.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-hook-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "955954041edda439e228913af5ef000372622ee6" :pushed-branch "sprint/tr15-hook-r2.w1.g1.e21"))

  (item "tr15-hook-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 747f130582ea (hetzner2; origin sprint/tr15-hook-r4.w1.g5.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-hook-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "747f130582ead1c54d376ce669f9d75aceabd031" :pushed-branch "sprint/tr15-hook-r4.w1.g5.e21"))

  (item "tr15-memory-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: c45e14338ef2 (superman; origin sprint/tr15-memory-r4.w1.g3.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-memory-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "c45e14338ef2b0445709be4bc750a51e6d38edda" :pushed-branch "sprint/tr15-memory-r4.w1.g3.e21"))

  (item "tr15-redis-r2" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 3dd1c8611bd5 (emma; origin sprint/tr15-redis-r2.w1.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-redis-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "3dd1c8611bd55cfb48990a4fd042f80a6c0733da" :pushed-branch "sprint/tr15-redis-r2.w1.g1.e21"))

  (item "tr15-sandbox-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 169d71e80082 (amd-1; origin sprint/tr15-sandbox-r4.w1.g3.e21); 416591184213 (amd-3; origin sprint/tr15-sandbox-r4.w3.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-sandbox-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "41659118421319af53c716ffb676d0fad8dd4f81" :pushed-branch "sprint/tr15-sandbox-r4.w3.g1.e21"))

  (item "tr15-secrets-r2" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 2788381d3b9a (emma; origin sprint/tr15-secrets-r2.w1.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-secrets-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "2788381d3b9ad8a0c3538d0a3a8c57533897f56f" :pushed-branch "sprint/tr15-secrets-r2.w1.g1.e21"))

  (item "tr15-self-talk-r2" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 7b77223cb3a9 (emma; origin sprint/tr15-self-talk-r2.w1.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-self-talk-r2" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "7b77223cb3a91f11a5ac10996a8fd657beba29be" :pushed-branch "sprint/tr15-self-talk-r2.w1.g1.e21"))

  (item "tr15-sprint-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 25290e529d31 (batman; origin sprint/tr15-sprint-r4.w1.g3.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-sprint-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "25290e529d3138c324fd427f4f75da010afdf709" :pushed-branch "sprint/tr15-sprint-r4.w1.g3.e21"))

  (item "tr15-swarm-r4" :group "export-v15-merging" :area "nova-tools"
   :title "nova-<tool> AI-usability review, 2026-10-15"
   :text "Reviewer: <your model as you know it; no person, machine or friend> Build: <the 12-hex head you built> Score: <n>/10 Source: Glenn, ~13:59Z 2026-10-15: \"Can you please also do another round of 'Is this a good tool for AIs to use?' per-tool in nova tools and sprint, each friend who is up reviews each.\" Heads: 0cecf306b417 (hetzner1; origin sprint/tr15-swarm-r4.w1.g3.e21); 74bcda5e029c (amd-1; origin sprint/tr15-swarm-r4.w2.g1.e21)."
   :why "Moved out of the sprint by Glenn 2026-10-15 to v1.5 (was merging)."
   :date "2026-10-15"
   :release "v1.5"
   :kept (:sprint-card "tr15-swarm-r4" :sprint-stream "tool-review-2026-10-15" :sprint-column "merging" :tier "" :sprint-base "v1.3/comms" :needs "tr15-research-dir" :pushed-head "74bcda5e029c4ef2afd0dd13d91bebd67b465e58" :pushed-branch "sprint/tr15-swarm-r4.w2.g1.e21"))

  (item "l1-nova-bus-reader-builder-and-show" :group "after-rocketnet" :area "nova-tools"
   :title "C12: nova-bus reads both forms, builds typed envelopes with refusals, and shows the text form"
   :text "Resume after the rocketnet milestone. THE TASK. In nova-bus: recv dual-reads legacy and typed envelopes (any of the three forms); a builder validates a typed envelope from flags and Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-nova-bus-reader-builder-and-show" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-defs-bus-envelope" :pushed-head "" :pushed-branch ""))

  (item "l1-nova-bus-native-binary-form" :group "after-rocketnet" :area "nova-tools"
   :title "C31: nova-bus carries the envelope in its native binary form, with no base64 on the bus itself"
   :text "Resume after the rocketnet milestone. THE TASK. Give the bus a native binary carrier: the typed Envelope's binary wire goes into a dedicated stream field as bytes, beside or instead of Source: Owner 2026-10-15: nova-bus gets a native binary form (card it in nova-tools Layer 1); the schema-go card Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-nova-bus-native-binary-form" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-defs-bus-envelope" :pushed-head "" :pushed-branch ""))

  (item "l1-pong-dual-read" :group "after-rocketnet" :area "nova-tools"
   :title "C13: the pong is read in both forms, refused by name when malformed, never read as silence"
   :text "Resume after the rocketnet milestone. THE TASK. Make the friend machine's pong reader accept both forms through pkg/schemaread, refuse every malformed or clamped pong by name, and count Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-pong-dual-read" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-defs-pong" :pushed-head "" :pushed-branch ""))

  (item "l1-hook-stamp-typed" :group "after-rocketnet" :area "nova-tools"
   :title "C14: the hook lease stamp is typed, with int64 millisecond times, refused by name when malformed"
   :text "Resume after the rocketnet milestone. THE TASK. Replace the hook's stamp parse with the typed stamp through pkg/schemaread, keeping the legacy read for the window and counting Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-hook-stamp-typed" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-defs-ack-beat-hook-stamp" :pushed-head "" :pushed-branch ""))

  (item "l1-seat-hook-typed-reader" :group "after-rocketnet" :area "nova-tools"
   :title "C15: the seat hook reads typed and legacy stamps alike"
   :text "Resume after the rocketnet milestone. THE TASK. Make the seat's hook reader (the coordinator's seat path in hook/) read the typed stamp and the legacy JSON stamp alike, refusing malformed Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-seat-hook-typed-reader" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-defs-ack-beat-hook-stamp" :pushed-head "" :pushed-branch ""))

  (item "l1-writers-switch" :group "after-rocketnet" :area "nova-tools"
   :title "C30: writers switch to the table form, only after readers adopted, and refuse instead of falling back"
   :text "Resume after the rocketnet milestone. THE TASK. Switch nova-friend's pong writer and nova-bus send to the table form, refusing on any validation failure rather than writing legacy. Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-writers-switch" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "heavy" :sprint-base "v1.5-schema-tables" :needs "l1-nova-bus-reader-builder-and-show,l1-nova-bus-native-binary-form,l1-defs-pong" :pushed-head "" :pushed-branch ""))

  (item "l2-tla-card-contract-result-typed" :group "after-rocketnet" :area "nova-tools"
   :title "C17: CardContract gains 'result typed before review'"
   :text "Resume after the rocketnet milestone. THE TASK. Add to tla/CardContract.tla the rule that no result reaches review unless its frame validated, with a Broken config that lets an untyped Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l2-tla-card-contract-result-typed" :sprint-stream "v13-schema-adoption" :sprint-column "ready" :tier "" :sprint-base "v1.5-schema-tables" :needs "" :pushed-head "" :pushed-branch ""))

  (item "l2-defs-card-jobpacket-result" :group "after-rocketnet" :area "nova-tools"
   :title "C18: the Layer 2 definitions: card, job packet and result"
   :text "Resume after the rocketnet milestone. THE TASK. Write schemas/card with the Card (the upstream tables/cards Card plus nova's 16 KiB body bound in the facade), JobPacket and Result of Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l2-defs-card-jobpacket-result" :sprint-stream "v13-schema-adoption" :sprint-column "ready" :tier "" :sprint-base "v1.5-schema-tables" :needs "nova-tools-strict-read-and-form-counter" :pushed-head "" :pushed-branch ""))

  (item "l2-brief-parse-parity" :group "after-rocketnet" :area "nova-tools"
   :title "C19: the table parse of a brief matches today's parser on every admitted brief"
   :text "Resume after the rocketnet milestone. THE TASK. Add the table parse of a card beside pkg/cardhdr's text parse and prove parity over a corpus of real briefs (with no person's name, key or Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l2-brief-parse-parity" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "l2-defs-card-jobpacket-result" :pushed-head "" :pushed-branch ""))

  (item "l2-result-table-reader" :group "after-rocketnet" :area "nova-tools"
   :title "C20: the result is read as a table; no shape fault is ever blamed on a worker"
   :text "Resume after the rocketnet milestone. THE TASK. Read RESULT.md as the Result table (dual-read with the two historical text shapes, counted per form), citing CardContract's Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l2-result-table-reader" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "l2-defs-card-jobpacket-result" :pushed-head "" :pushed-branch ""))

  (item "l2-friend-report-gateway" :group "after-rocketnet" :area "nova-tools"
   :title "C21: the friend outbox gateway refuses an untyped report by name and leaves nothing unread"
   :text "Resume after the rocketnet milestone. THE TASK. Transcode REPORT.md through the Result table at the outbox gateway, refusing by name and surfacing the refusal instead of leaving the Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l2-friend-report-gateway" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "l2-result-table-reader" :pushed-head "" :pushed-branch ""))

  (item "l3-defs-and-refusals-whole" :group "after-rocketnet" :area "nova-tools"
   :title "C23: refusal and finding definitions; every verb's --json refusal is one whole object"
   :text "Resume after the rocketnet milestone. THE TASK. Define the Refusal and Finding tables in schemas/refusal, generate pkg/refusalwire, and make every verb's --json refusal one whole typed Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l3-defs-and-refusals-whole" :sprint-stream "v13-schema-adoption" :sprint-column "ready" :tier "" :sprint-base "v1.5-schema-tables" :needs "" :pushed-head "" :pushed-branch ""))

  (item "l5-retire-legacy-result-parser" :group "after-rocketnet" :area "nova-tools"
   :title "C26: delete the legacy result parser once the per-form counter shows zero legacy reads"
   :text "Resume after the rocketnet milestone. THE TASK. Remove the legacy RESULT.md and REPORT.md text parsers once the counter proves no legacy read Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l5-retire-legacy-result-parser" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "l2-signoff-fence,l2-result-table-reader,l2-friend-report-gateway" :pushed-head "" :pushed-branch ""))

  (item "l1-defs-bus-envelope" :group "after-rocketnet" :area "nova-tools"
   :title "C10a: the bus area and the Envelope, generated into pkg/buswire"
   :text "Resume after the rocketnet milestone. THE TASK. Write schemas/bus/envelope.schema with the variable table of UNION.md section 4.1, Envelope, with the Kind enum and the Name element, Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-defs-bus-envelope" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "nova-schema-conformance-ci,l1-tla-typed-frames,nova-tools-strict-read-and-form-counter,table-wire-over-text-transports" :pushed-head "" :pushed-branch ""))

  (item "l1-defs-ack-beat-hook-stamp" :group "after-rocketnet" :area "nova-tools"
   :title "C10c: the hook area (ack, beat, hook stamp), generated into pkg/hookwire"
   :text "Resume after the rocketnet milestone. THE TASK. Write the variable tables of UNION.md section 4.2 into the hook area: schemas/hook/ack.schema (Ack: id, stage, push, reason, with the Stage Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-defs-ack-beat-hook-stamp" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "nova-schema-conformance-ci,l1-tla-typed-frames,nova-tools-strict-read-and-form-counter,table-wire-over-text-transports" :pushed-head "" :pushed-branch ""))

  (item "l1-defs-pong" :group "after-rocketnet" :area "nova-tools"
   :title "C10b: the pong area, generated into pkg/pongwire"
   :text "Resume after the rocketnet milestone. THE TASK. Write schemas/pong/pong.schema with the variable table of UNION.md section 4.2, Pong: friend, nonce text(32), queue, working, width with Source: nova-sprint docs/research/schema-tables/UNION.md (read) at 98f8a280 (branch sprint/schema-adoption-union-fixes.w2.g1.e21) section Heads: none."
   :date "2026-10-15"
   :release "after the rocketnet milestone"
   :kept (:sprint-card "l1-defs-pong" :sprint-stream "v13-schema-adoption" :sprint-column "waiting" :tier "" :sprint-base "v1.5-schema-tables" :needs "nova-schema-conformance-ci,l1-tla-typed-frames,nova-tools-strict-read-and-form-counter,table-wire-over-text-transports" :pushed-head "" :pushed-branch ""))

))
