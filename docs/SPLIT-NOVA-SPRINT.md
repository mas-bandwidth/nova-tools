# Splitting nova-sprint out of nova-tools

Status: written 2026-10-04. Whole-plan acceptance by the maintainer or the coordinator is not recorded here; the maintainer's individual rulings
(nova-card and nova-work move, nova-decide stays, nova-local stays) are marked in the table, and the borderline cases are
left for the maintainer to correct. This file is the record of the plan as the coordinator's child wrote it (ideas#850), copied from
`tmp/nova-sprint-split/PLAN.md`; the measured figures are as of the seed named below.


Written 2026-10-04, around 12:40 PM ET, by a child of the coordinator (ideas#850). Seed: nova-tools `a3c6359d42` (branch
`integration-2026-10-04`, unchanged for 10 minutes from 12:23 PM). The dependency figures below were
measured on that tree with `go list -deps`, using the functional, slow, perf and race tags for test imports.

The framing (the maintainer, 12:38 to 12:51 PM): nova-tools is a set of **unopinionated building blocks for any AI
workflow**, and every tool in it must be useful to someone who never runs nova-sprint. nova-sprint is **the
opinionated system** built from those blocks, and it ships as **one binary**. The building blocks stay in
nova-tools; where they still use sprint words, they are made general. Only the sprint's own policy moves.

## Every cmd of nova-tools, classified

Each tool is judged by one test: would someone with no nova-sprint use it as it is?

| cmd | class | why (and what a split would look like) |
|---|---|---|
| nova-sprint | **moves** | the opinionated system itself: dealer, tiers, streams, sentinels, readers, judgments, lander, dashboard |
| nova-card | **moves** (maintainer 12:46) | its generator and lint exist for the sprint's card format; becomes `nova-sprint cards generate\|template\|lint` |
| nova-work | **moves** (maintainer 12:51) | becomes `nova-sprint work import\|verify`; the work record repo stays a repo |
| nova-bus | stays | messages between AIs; no sprint import |
| nova-table | stays (sprint-flavoured) | tables of ordered sets; its help still names "the sprint table" and reads `NOVA_SPRINT_REDIS*` |
| nova-redis | stays (sprint-flavoured) | a local store and short-lived values; `fn load` installs a library that still carries the sprint's Lua (card move) |
| nova-config | stays (sprint-flavoured) | fleet rows in PostgreSQL; its kinds include `sprint`, `tier` and `route`, plus the sprint's decide bars (`FieldDecide*`) |
| nova-secrets, nova-sandbox, nova-fuse, nova-memory, nova-cairn, nova-check, nova-self-talk, nova-tokens, nova-version | stay | no sprint import or vocabulary in what they do |
| nova-update | stays (sprint-flavoured) | `report --store` reads every bench's **nova-sprint** build from its beat; that should become "a tool's build", by name |
| nova-ci | stays (borderline) | slowtests is general; `github receipt` writes a receipt into the sprint's store through `internal/nsprint/store`; the rest is this repository's own CI |
| nova-swarm | stays (borderline) | `native`, `step`, `verify`, `lint`, `template`, `slots` and `disk-guard` stand on their own. `member` is the sprint member: it talks to the sprint server over `pkg/sprintwire`, with `lint --decide`, `--member-injects` and `--base-check` beside it. The maintainer's line: the execution stays, and the sprint-specific dealing glue (the wire verbs take, beat and report, plus the brief the member injects) moves. A split would give `member` a general work-server client, with nova-sprint as one server |
| nova-decide | stays (borderline; maintainer: stays) | `ask`, `outcome`, `calibrate` and `findings` are general. `read`, `score`, `attempt`, `grade`, `gate` and `brief` are the sprint's card questions. A split would keep them as schemas the sprint supplies, not as verbs built into nova-decide |
| nova-friend | stays | session wake, a building block; it uses `pkg/sprintwire` for a friend's beat and take, which is the same glue as nova-swarm `member` |

**Moved in this cut:** nova-sprint, nova-card and nova-work, plus the sprint dashboard. The dashboard is already a verb of
the binary (`nova-sprint dashboard`, `internal/sprintdash`), not a program of its own. **Borderline cases, left in place
for the maintainer to correct:** nova-swarm `member`, the card verbs of nova-decide, nova-ci `github receipt`, the sprint Lua in the
nova-redis library, nova-config's `sprint`/`tier`/`route` kinds and decide bars, and nova-update `report --store`.
nova-local does not exist in nova-tools yet (PR 5307). The maintainer confirmed it stays.

## The one binary

```
nova-sprint <verb>           the server, the coordinator's verbs, the dashboard (as now)
nova-sprint card <id>        unchanged: read one card
nova-sprint cards generate   was nova-card generate   (plural "cards": no clash with the read verb card)
nova-sprint cards template   was nova-card template
nova-sprint cards lint       was nova-card lint
nova-sprint work import      was nova-work import
nova-sprint work verify      was nova-work verify
nova-sprint dashboard        unchanged (internal/sprintdash)
```

nova-sprint has no `cards`, `work`, `template` or `lint` verb today, so nothing clashes. In the seed, nova-card and
nova-work still build as separate mains. Folding them into the binary is the first follow-up (section d).

## (a) Packages that move (nova-tools loses them)

From the dependency graph, these are used by nova-sprint, nova-card or nova-work and by no other cmd:
`cmd/nova-sprint`, `cmd/nova-card`, `cmd/nova-work`, `internal/sprint` (with `driver`, `store`, `refmodel`),
`internal/sprintdash`, `internal/cardgen`, `pkg/provbalance`, `internal/workfile`, `internal/workgh`,
`internal/worklang`, `tools/sprintsize`.

- `pkg/provbalance` is a building block (reading a provider's balance). It moves only because no nova-tools cmd
  uses it, and the dead-code rule counts from cmd roots. It comes back when a nova-tools tool exposes it, for example
  `nova-config route balance`.
- `pkg/ci/shrinkonly` is used only by nova-card. Its home is nova-tools' CI, so it is copied, not moved.
- The graph agrees with the brief: nova-swarm, nova-bus, nova-friend and nova-decide use no sprint-only package. Their
  only sprint link is the shared `pkg/sprintwire`.

## (b) Shared packages: copied into nova-sprint `internal/`, later imported from a public nova-tools API

There are 41 packages, all at the same relative path, except that `fleet` became the package fleetrules. Only non-test
files were copied, plus `pkg/swarm/testdata/providers.tsv`, which is embedded. For nova-sprint to import them instead,
nova-tools must first move each one out of `internal/` (to `pkg/` or a module of its own). The surface nova-sprint
uses, measured with a Go AST scan of the moved code:

- `pkg/atomicfile` (4): ExactMode NoReplace Write WriteFile
- `pkg/binstamp` (1): Of
- `pkg/buildinfo` (2): Line Parse
- `pkg/bus` (3): Bus Message Redis
- `pkg/cardcontract` (5): Families For Frame ReadTitle Staged
- `pkg/cardcost` (15): ActualByHarness Cents NoTotal NoUsage ParseTotal ParseUsage Prices PricesOf Sum Tokens Total Unreported Usage WhyNoRoute WhyNoTokens
- `pkg/cardhdr` (19): EndLaunch EndNoCommit EndNoResult EndNothing EndPreExisting EndProvider EndStaging IsRoute KeyValue Model ParseTest ReadModel ReadWho RemainderKey RouteFlash RouteFrontier RouteHeavy RouteList RoutePro
- `pkg/cardlimits` (2): BriefAdvisoryBytes MaxBriefBytes
- `pkg/cardtree` (2): Lint Parse
- `pkg/ci/shrinkonly` (2): DeadCode ShrinkOnly
- `pkg/config` (33): DefaultFriendWidth EnvPG FieldDecideAttemptNoResult FieldDecideAttemptNothingToDo FieldDecideBounce FieldDecideBriefBar FieldDecideGateFlaky FieldDecideGatePreexisting FieldDecideGrade FieldDecideJudgment FieldDecideReview FieldDecideScoreBar FriendBeatKey FriendWidth KindFleet KindFriend KindMachine KindSprint MachineWidth Mem Migrations NewMem OpenFile OpenPG ResolveDSN RouteKey RouteTiers RoutesKey Row SprintKey Store TierKey Widths
- `pkg/decide` (105): AckReason Act ActApply ActList Answer Append Ask Attach AttachBriefs AttemptDecided AttemptDecision AttemptLabel AttemptName AttemptOf AttemptQuestion Backend BriefBar BriefDeadline BriefDropped BriefOf BriefOp BriefWidth Briefs Calibrate CardFilePaths Caused Choice ChoiceOf Choose Chosen ClassDone ClassNeedsPro ClassNoResult ClassNothingToDo ClassProviderFailure ClassWrongScope ConflictError Decided Decision DiffSummary End ErrUnknown Failure Find Fixed FixedAnswer Fixes Flaky Gate GateBars GateInput GradeFlash GradeLabel GradeName GradeOp GradePro GradeQuestion GradeSchema GradeScript GradeState HistoryOf JevHTTP JevModel JevSecret JevTimeout JudgmentInput JudgmentName JudgmentOutcome JudgmentSchema JudgmentState Kinds LabelDropped LabelLanded Land LandLabel Load Make Names Outcome ParseAttempt ParseBriefBar ParseDecided ParseFixed ParseGateBars ParseGateOutput ParseJudgmentBar PaymentRefusal PreExisting Reasons RecordAct RedAgain Schema Score ScoreOp Set SettleGate Sum Top Usage VerbAccept VerbAck VerbDrop VerbOf VerbRework VerbWait
- `pkg/diffcheck` (6): Fragments GeneralityLedger GeneralityRoots Outside UpdateEnv UpdatedRerun
- `pkg/gitrun` (3): Options Output Run
- `pkg/gocache` (4): Bounds Hold Limit Slack
- `pkg/goenv` (1): Clean
- `pkg/hostload` (7): HowCPU HowLoad1 Local MaxPercent Measure Source State
- `pkg/hygiene` (1): MatchGlob
- `pkg/member` (7): Child Config Member New Packet Push Result
- `pkg/nsprint/fn` (6): Library Load Loaded Source Spec Sum
- `pkg/nsprint/redisauth` (3): DefaultPasswordEnv PasswordEnvEnv UserEnv
- `pkg/nsprint/testutil` (1): Start
- `pkg/nsprint/verbflag` (12): Explain FlagSynopsis Help HelpIfAsked Insert IsHelp New Parse Print RecoverWith UsageLineSynopsis Verb
- `pkg/ntable` (86): ApplyBatch ApplyBatches BatchDelta BatchManifest BatchMemberDelta BatchMemberEntry Cell CellAdd CellKeyAt CellText CellsCmd ChangesKey Check Column Count Create DefKey DropDefinition EpochPrefix ErrDrift ErrMalformedManifest ErrNoView ErrUnknownOutcome FieldGuard FnRowDel IdentityKey IsRefusal LimitChangedEntries LimitError LimitFieldValueBytes LimitGuardEntries LimitManifestBytes LimitReadSetMembers LimitTableProps MemberCreateOp MemberExpect MemberMoveOp Members NewReader NewRow None ParseColumns PlaceExpect PropsKeyAt QueueCells QueueReadSetMembers QueueViewState Read ReadAt ReadCmd ReadSetCmd ReadSetMember ReadSetMembers ReadSetResult Receipt Refusal Render RenderOpts RenderTables Row RowDel RowKeyAt RowSet RowSetMany RowsAdd RowsHide RowsKeyAt RuleError SameDefinition Set SetOpts Shape Sort Sum SummaryLine Table Text ValidateBatchManifestRaw ValidateColumns View ViewDelete ViewGet ViewSet ViewState ViewStateResult WriteOptions
- `pkg/onboarding` (8): CompareTranscript ExampleLines Field FirstRun Result SplitShell Steps Transcript
- `pkg/oneline` (7): Cap Err Escape Field ShellWord TailBytes WithRemedy
- `pkg/redisacl` (4): Coordinator Member Render Roles
- `pkg/redisconn` (5): Conn Env Exec Open Options
- `pkg/safepath` (2): NameOK RemoveUnderRoots
- `pkg/sprintwire` (9): Client MaxRequest MaxVerbs Path Request Response Result Tries Worker
- `pkg/subproc` (5): Command Context Long Prepare Tool
- `pkg/swarm` (21): CardHeaderFinding CardHeaderValue CardPaths CardRepoURL ChildRemedy ChildRule ChildRulesParagraph DefaultChildRules DefaultRulesName HeldRules HeldRulesText LintCardChildByReference LintCardChildWith LintCardHeader OwnRulesName ReadCardBase ReadChildRules RulesParagraph StagedBrief Template UnfilledTemplateLines
- `pkg/testbin` (1): WriteExecutable
- `pkg/testguard` (1): RefuseHosts
- `pkg/testkit` (5): Main ReadFile Result Tree WriteFile
- `pkg/testredis` (2): FarLink Start
- `pkg/tool` (11): Call Done Fail Field Fields Flags Out Refuse Text Tool Verb
- `pkg/tty` (1): Size
- `pkg/typedrec` (2): IsFullSha NamesADefect
- with no direct use, copied only because the above import them: `pkg/bounded`, `pkg/delayproxy`,
  `pkg/filelock`, `pkg/goenv`, `pkg/harness`, `pkg/keyshape`, `pkg/log`, `pkg/pkgselect`,
  `pkg/redisfn`, `fleet` (as the package fleetrules)

### The generality pass: one follow-up card per nova-tools package

Each card is "make it usable by a stranger building a different workflow". The vocabulary each one moves to:

| package | sprint concept leaking in (API, comments, errors) | the general form |
|---|---|---|
| `pkg/swarm` | card (275 mentions), brief, reader, coordinator, seat; `CardHeader*`, `CardRepoURL`, `StagedBrief`, `ReadCardBase` | a **task** file with a header and rules: `TaskHeader*`, `TaskRepoURL`, `StagedTask`; "reviewer" in place of reader |
| `pkg/member` | sprint member loop, cards, readers, tiers | a **worker loop** against a work server: beat, take, report, push; the server is an interface and nova-sprint is one implementation |
| `pkg/sprintwire` | "sprint's server", `Worker` verbs | `workwire`: a batch of verbs to a **work server**, request, reply, client; no sprint words in its types |
| `pkg/decide` | card, brief, judgment, tier; `AttemptOf`, `BriefOf`, `ClassNeedsPro` | typed **decisions** over a **subject** (text plus diff). The card questions (read, score, attempt, grade, gate, brief) become schemas the caller supplies, not types built into the package |
| `pkg/cardcontract` | card, sprint, brief, tier, readers | the **task contract**: the frame around a task's work (base, branch, push, finish) |
| `pkg/cardhdr` | card, sprint, friend, tier, route names flash and pro | **task header** keys; routes and tiers are names from config, never built-in constants |
| `pkg/cardcost` | card, sprint, dealt | **task cost**: usage and price per run |
| `pkg/cardtree` | card, sprint, land, coordinator | a **task tree**: parse and lint |
| `pkg/cardlimits` | brief, card | task-text limits |
| `pkg/config` | `KindSprint`, coordinator, friend, tier, seat, `FieldDecide*` (sprint decide bars) | machines, agents, routes and model classes as rows; a workflow registers its own kinds and fields. The sprint's kinds and bars move into nova-sprint's own migrations |
| `internal/nsprint/*` | the package path itself; `NOVA_SPRINT_REDIS_USER` and `_PASSWORD_ENV`; `fn` carries the sprint's Lua (02_card_move.lua) | a package named store (or `redisauth`, `verbflag`, `fn` at the top level); `NOVA_REDIS_*` env names; the card-move Lua moves to nova-sprint as a library it loads itself |
| `pkg/redisacl` | roles coordinator, member, friend; the `sprint` key family | **roles and key families from configuration**; a workflow declares its own family |
| `pkg/ntable` | stream, sentinel, card in comments and examples | rows, cells and ordered sets only |
| `pkg/diffcheck` | card, lander | a **change gate** over a diff |
| `pkg/pkgselect` | deal, dealt, tier | package selection for a test run, sharded by weight |
| `pkg/gocache`, `pkg/hygiene`, `pkg/typedrec` | friend, sprint, card in comments and errors | wording only |
| `fleet` (child-rules.txt) | the child rules of a card | rules for any AI task run in this repository |
| `internal/update` (nova-update `report --store`) | "every bench's nova-sprint build" | a named tool's build on every machine |
| `pkg/ci/shrinkonly` (used by nova-card) | none; its ledgers are nova-tools' own | stays nova-tools CI. The card generator reads it as a source, so the source should be a file format, not this package |

## (c) Docs, specs, TLA, tests, testdata, ledgers that belong to nova-sprint (all moved)

- **Docs**: `docs/SPEC-SPRINT.md`, `docs/SPEC-SPRINT-DASHBOARD.md`, `docs/SPRINT-COORDINATOR.md`,
  `docs/SPRINT-COORDINATOR-SEAT.md`, `docs/SPEC-WORK-V1.md` and `docs/SPEC-WORKLANG.md`; the nova-sprint, nova-card and
  nova-work sections of `docs/CLI.md` and `docs/TESTS.md` (as nova-sprint's `docs/CLI.md` and `docs/TESTS.md`); the
  sprint and work 1.1.0 ratings (`docs/ratings/...`); the font's provenance row (`docs/ASSET-PROVENANCE.md`). The
  dashboard page is embedded in `internal/sprintdash/page`.
- **TLA+**: SprintEvents, DirtyTick, DirtyTickRead, RouteIndex, Level and Land (sprint), and WorkImport (nova-work), with
  their MC modules, cfgs, READMEs, `sprintevents-bench/` and `dirtytick-bench/`, and their 220 `CASES.tsv` and
  `RUNS.tsv` rows. CardContract (nova-swarm and member) and CardMachine (the Lua in `nsprint/fn`) stay in nova-tools
  beside the code they model. The TLC runner is still nova-tools' `tools/tlacheck`, so nova-sprint needs its own runner
  or an imported one.
- **Tests**: every test of the moved packages moved with them. `internal/ci/sprint_tables_lock_class_test.go` became
  the sprint package's tables_lock_test.go, beside `TABLES.lock`. Certification's `tick-gate` job (the
  sprint tick's wall clock) left nova-tools; nova-sprint's CI does not have it yet.
- **Ledgers, in nova-tools**: 410 `deleted-tests.txt` rows; the testify, discarded, remedy, toolanswers and generality
  shards of the moved packages; `compared_examples`, `dead_code_allowlist`, `generality-text/tla`,
  `generality_text_fixtures_allowlist` and the `package-sizes` rows; the catalog and AGENTS.md maps; the named-path
  allowlist (section 3, "another tree"); and the heavy-package lists (certification.yml, its class test, SPEC-CI).
- **Transcripts**: the first-run transcripts live in TESTS.md and CLI.md, so they moved with those sections.

## (d) The order

1. **Done**: the seed is pushed to the nova-sprint repository's `main` (`15f1324`). Gate on a Linux bench: build and vet green;
   the tests have one red, `internal/sprint/store` TestTheTickRaisesReadersBehindWhenReadsWaitTheWindow. That test is
   equally red in nova-tools at the seed commit (an integration conflict, not a split defect).
2. **Done**: nova-tools PR #5309, which removes the moved paths. It is queued with `--auto`. Its head is `b081bb0b84`:
   one commit, plus a merge of dev at `783210bcb` (dev's later edits under the moved paths are dropped, because the seed
   already holds them through the integration). Gate on a Linux bench, run on a simulated PR merge ref (dev plus the branch):
   build, vet and test all green. nova-sprint's own CI on GitHub-hosted runners shows the same single inherited red.
3. **Before the next fleet release (blocking)**: give the fleet a nova-sprint install path. Once #5309 lands,
   `nova-update release build` stops building nova-sprint, nova-work and nova-card from nova-tools. The installed
   nova-sprint stays at the last nova-tools build, which is safe because nothing removes it: it is deliberately not in
   `fleet/retired-tools.txt`. `fleet/tools.yml` needs nova-sprint as a second source to build and install from.
4. **Carry-over into nova-sprint** (open nova-tools PRs whose sprint half is not in the seed): see the lists below,
   including **5307**'s sprint half (the dealer caps local routes by a machine's lanes; `routes` shows serve/lanes),
   **5300** (skipped by the integration), **5308** and **5278**.
5. **Fold** nova-card and nova-work into the binary (the layout above); then delete their mains.
6. **nova-sprint CI**: add the functional tier (redis-server), the tick gate, and the TLC runs.
7. **The generality pass**: one card per package in (b), one layer at a time from the bottom (oneline, atomicfile,
   redisconn and redisfn, then ntable, config, swarm and member, decide). Each one makes the package public in
   nova-tools and replaces nova-sprint's copy with an import.
8. **Borderline tools**: once the maintainer corrects the list, split them (nova-swarm `member`'s glue, nova-decide's card verbs).

## Open nova-tools PRs (step 4); none closed

Classified by diff paths against the moved set. Generic ledgers, CLI.md and TESTS.md are not counted.

**Touch only moved paths: moot for nova-tools once #5309 lands, so carry each into nova-sprint or close it**
- #5015 a beat's age measured at the tick's read
- #5222 an end-to-end twin test for the friend card lifecycle
- #5227 flash cards and routed stores in the store differential fixtures
- #5228 a cold start judges no provider out of funds from stored refusals
- #5230 TestTheDirtyTickDriveOnAStore waits on the twin's state
- #5233 the twin catches up from a row order and row delete
- #5235 finish `--head` defaults to what land accepts
- #5236 proves the friend-card path end to end on the twin
- #5238 the functional tier holds under load
- #5239 tla: a friend's card in DirtyTick
- #5243 a card's tier is its ceiling before a deal
- #5246 a land with no full sha fails
- #5278 the sprint dashboard pulse
- #5306 friends get what machines have (already in the seed)
- #5308 auto sentinels

**Touch both: they need a split (sprint half to nova-sprint, the rest stays)**
- #4956 ledger shards (the ci half stays)
- #5014 sprint VIEW.lock (SPEC-CI, STANDARD and AGENTS rows stay)
- #5149 friend sync SetKeys (FLEET.md)
- #5247 admission contract (the nova-swarm lint half stays)
- #5266 child instructions (child-rules, STANDARD and SPEC-CARD-CONTRACT stay)
- #5268 friend take fence (FRIENDS.md)
- #5275 nova-work help (pkg/tool stays)
- #5281 seat check (the seatcheck package and the functional image stay)
- #5300 spend rules (its nova-config route half stays; the integration skipped it)
- #5307 nova-local (nova-local, nova-config and nova-swarm stay; the dealer half moves)
- #5258 the dev to main promotion. It carries everything and is resolved by order, not by a split.
- These were already merged into the integration seed, so their sprint halves are in nova-sprint and they land in
  nova-tools through the integration: #5282, #5283, #5285, #5287, #5288, #5289, #5291, #5293, #5297, #5298, #5299,
  #5301, #5303, #5304, #5305.

## Notes for the coordinator

- Migration numbers: the integration seed already holds 5299's `0028` (the heavy-tier route harness). **5307 must become
  0029.**
- Merge order: if the integration PR lands on dev after #5309, its changes under the moved paths conflict as
  modify/delete. Resolve them by deleting the files; the seed already has those changes.
- dev is red on its own (vet: `pkg/ntable` `store_cover_test.go` redeclares `coverPipe`; tests: cmd/nova-config,
  cmd/nova-redis and pkg/config). #5309 adds no red of its own.
- nova-sprint's CI uses GitHub-hosted runners. nova-tools' self-hosted runners are registered to nova-tools alone.
