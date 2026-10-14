; The nova-tools roadmap: the data ROADMAP.md is generated from.
; Same shape as nova-sprint's docs/roadmap.sexp (internal/roadmapdoc's package comment):
; data, never evaluated. Edit this file, then regenerate ROADMAP.md with nova-sprint's
; generator, from a nova-sprint checkout:
;   go build -o /tmp/roadmap ./tools/roadmap && (cd <this repo> && /tmp/roadmap --file docs/roadmap.sexp --out ROADMAP.md)
; Never edit ROADMAP.md by hand. Created 2026-10-14 when the open sprint cards of this
; repository were moved out of the stopped sprint.
(roadmap "v1"
 :title "Nova Tools roadmap"
 :text "1.0.0 is the first release of nova-tools. It is cut from `main` when the set below is solid: every tool is tested, its documentation describes it as it is (docs/CLI.md), and every friend has audited 1.0.0 on `main` at the candidate sha and given a last review OK. A green CI run alone is not that bar. The 1.0.0 set is fifteen tools, and nothing outside it ships in 1.0.0: nova-bus (messages and replies between friends, over git); nova-cairn (session notes: explicit checkpoints, source pointers and a bounded index); nova-check (broken links, branch hygiene and other problems in your records); nova-ci (packages over the test-time budget, and ci-ok's run receipt); nova-config (the permanent configuration in Postgres, applied into Redis); nova-fuse (a recorded decision to stop reading a source); nova-memory (matching sources from your Markdown records); nova-redis (the Redis store: `serve` runs it, `spill` and `recall` keep scratch with a TTL, `fn load` puts this binary's function library on it and `fn check` compares without changing anything); nova-sandbox (filesystem restrictions for a command); nova-secrets (encrypted storage, and selected credentials delivered to a child command); nova-self-talk (flagged sentence patterns in how you write about yourself); nova-table (tables over Redis, every cell an ordered set, and their live views); nova-tokens (token usage by model and repository, with gaps shown); nova-update (declared versions checked, and one chosen update applied); nova-version (installed tool identities). The items below are planned work after 1.0.0: open sprint cards moved out of the stopped sprint on 2026-10-14, each naming its card, its tier, its base branch and any pushed work to resume from."

 :groups
 (
  (group "export-testify-tools" :title "Moved out of the sprint on 2026-10-14: testify-tools"
   :text "The open cards of sprint stream testify-tools, moved out of the stopped sprint on 2026-10-14, one item per card. Landed cards of the stream are in git and are not listed. Each item names its card, its tier, its base branch and any pushed work to resume from.")
  (group "export-tools-v1224" :title "Moved out of the sprint on 2026-10-14: tools-v1224"
   :text "The open cards of sprint stream tools-v1224, moved out of the stopped sprint on 2026-10-14, one item per card. Landed cards of the stream are in git and are not listed. Each item names its card, its tier, its base branch and any pushed work to resume from.")
 )

 :items
 (
  (item "docs-sandbox-and-friend-commands-tests-are-tables-on-testifyb" :group "export-testify-tools" :area "nova-tools"
   :title "Convert the test files matching PATHS (packages internal/docs, cmd/nova-sandbox, pkg/sandbox, cmd/nova-friend) to testify and table-driven tests on the mini-modules, delete the copied helpers, and..."
   :text "Convert the test files matching PATHS (packages internal/docs, cmd/nova-sandbox, pkg/sandbox, cmd/nova-friend) to testify and table-driven tests on the mini-modules, delete the copied helpers, and apply the advanced testify features where they save lines. Only the files matching PATHS change."
   :why "Moved out of the stopped sprint on 2026-10-14: neither rocketnet nor v1.3 work."
   :date "2026-10-14"
   :origin "sprint card docs-sandbox-and-friend-commands-tests-are-tables-on-testifyb (stream testify-tools, column ready, tier pro, base sprint/testify-tools), moved out of the sprint 2026-10-14"
   :kept (:sprint-card "docs-sandbox-and-friend-commands-tests-are-tables-on-testifyb" :sprint-stream "testify-tools" :sprint-column "ready" :tier "pro" :sprint-base "sprint/testify-tools" :needs "testkit-must-wraps-the-repeated-assert-shapes,testkit-verb-runs-a-tool-and-checks-code-and-streams,testkit-fsx-sets-up-files-for-a-test,testkit-repotree-walks-the-tree-once-for-every-class-test,testkit-onboardtest-checks-the-onboarding-standard-for-a-tool" :pushed-head "" :pushed-branch ""))
  (item "nova-config-compare-names-the-act-behind-a-changed-row" :group "export-tools-v1224" :area "nova-tools"
   :title "nova-config compare names the recorded act behind a changed row, so a deliberate edit during an adopt is not a revert trigger (v1.2.24)"
   :text "Two verbs and their pure core, in pkg/config/snapshotcmp.go (NEW) and cmd/nova-config/compare.go (NEW):"
   :exists "Work was pushed: head nova-config-compare-names-the-act-behind-a-changed-row.w6 on branch sprint/nova-config-compare-names-the-act-behind-a-changed-row.w6.g1.e20 (latest attempt branch; the head may be an earlier commit on it) (attempt 6). Resume from it; do not start over."
   :why "Moved out of the stopped sprint on 2026-10-14: neither rocketnet nor v1.3 work."
   :date "2026-10-14"
   :origin "sprint card nova-config-compare-names-the-act-behind-a-changed-row (stream tools-v1224, column review, tier pro, base sprint/v1.2.24-tools), moved out of the sprint 2026-10-14"
   :kept (:sprint-card "nova-config-compare-names-the-act-behind-a-changed-row" :sprint-stream "tools-v1224" :sprint-column "review" :tier "pro" :sprint-base "sprint/v1.2.24-tools" :needs "" :pushed-head "nova-config-compare-names-the-act-behind-a-changed-row.w6" :pushed-branch "sprint/nova-config-compare-names-the-act-behind-a-changed-row.w6.g1.e20 (latest attempt branch; the head may be an earlier commit on it)"))
))
