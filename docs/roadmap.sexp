; The nova-tools roadmap: the data ROADMAP.md is generated from.
; Read by internal/roadmap through internal/roadmap/sexp (data, never evaluated).
; Edit this file, then run `make roadmap`; never edit ROADMAP.md by hand.
; The shape is in internal/roadmap's package comment.
(roadmap "v1"
 :title "nova-tools roadmap"
 :text "This is the work planned after v1.4. The release ladder is fixed (the owner, 2026-10-09): v1.2.1 is
  small fixes; v1.3 is larger fixes, and fixes only; v1.4 is v1.3 plus unit tests, cleanup, dead code,
  adoption of the standard library and modules, and test refactors. Then the line stops, and anything
  outside the ladder is recorded here instead of being worked. nova-sprint, nova-card and
  nova-work moved to mas-bandwidth/nova-sprint with the split (v1.2.3), and their items are in that
  repository's ROADMAP.md. Point-release work is in FIXES.md, not here. Each
  item says what it is and why it waits. Where the code already has part of an item, the item says
  what exists and covers only the gap."

 :scheduled
 ((scheduled "nova-swarm-becomes-nova-worker" :release "v1.3"
   :title "Rename nova-swarm to nova-worker"
   :text "The tool runs one-task AI workers, and its help already says so. The rename covers the binary
    and its cmd directory, help text, docs, the fleet's ansible plays and launchd units (in their own repository), seat names, and open cards. A nova-swarm shim keeps working for one release.
    No tool is named swarm after it."
   :date "2026-10-09")
  (scheduled "docs-pass-v1-3" :release "v1.3"
   :title "Documentation pass with the rename"
   :text "Plain English throughout, every doc matching the renamed tool. They are called workers
    everywhere; the one exception is README.md, where the bees are pictured."
   :date "2026-10-09"))

 :done
 ((done "tier-defaults-direct-providers"
   :title "Tier defaults on direct providers"
   :text "flash is Mercury 2.5, pro is DeepSeek 4.1 Flash and heavy is DeepSeek v4 Pro, each called on
    its own provider's API. Mercury 3 becomes heavy when v1.3's global per-model rate limit lands.
    Direct providers come first; OpenRouter and OpenCode are spillover only. Clean results start at
    2026-10-09 22:01Z."
   :date "2026-10-09")
  (done "repository-split" :title "nova-sprint split out of nova-tools"
   :text "nova-sprint, nova-card and nova-work, with their models and docs, live in mas-bandwidth/nova-sprint
    from v1.2.3; CI here refuses their paths."
   :date "2026-10-10"))

 :groups
 ((group "lessons"
   :title "Lessons from Prime Agent's rewrite"
   :text "Prime Intellect's write-up of rewriting Prime Agent with AI workers
    (https://www.primeintellect.ai/blog/prime-agent-rust) names what made a large AI-built rewrite
    hold together. Each item is one of those lessons, checked against nova's code at dev 9ff68178:
    what already exists is named by file and line, and the item covers the rest.")
  (group "measure"
   :title "Measure and rate"
   :text "Measurements and ratings that only mean something once the code under them stops changing.")
  (group "friends"
   :title "Friends"
   :text "New ways for friends to connect and work.")
  (group "ops"
   :title "Setup, release and operations"
   :text "Setting machines up, installing and releasing safely, and the checks that keep a fleet honest.")
  (group "docs"
   :title "Docs, models and the repository"
   :text "Documentation suites, the TLA+ ledger, and where nova-sprint's code lives.")
  (group "far"
   :title "Far"
   :text "Directions, not plans. Nothing in v1.x builds toward these yet.")
  (group "tests" :title "Tests and test tiers"
   :text "Test tiers, test speed and flaky tests.")
  (group "quality" :title "Cleanup, dead code and debt"
   :text "Shrinking, dead code removal, lint and debt work that waits for the code under it to settle."))

 :items
 (
  ; Lessons from Prime Agent's rewrite
  (item "one-shot-workers" :group "lessons" :area "nova-friend"
   :title "One-shot workers per card, friends included"
   :text "Every card gets a fresh worker session that ends when the card ends. No worker sits idle
    between cards holding a session, and no session carries one card's context into the next.
    Long-lived sessions are kept only for coordination: the seat and the friends' chat. This changes
    the friend default from batch to one-shot, makes a lane one session, and changes the friend
    daemon's lifecycle to match."
   :exists "nova-swarm runs one task per launch (cmd/nova-swarm/main.go:1). Friends have a
    one-shot mode (internal/friend/lanes.go:26), but the default is batch
    (internal/config/kind.go:213-215). The gap is one-shot as the default for every friend, and batch
    delivery retired for work."
   :why "A change of the default delivery model, not a fix."
   :date "2026-10-09")
  (item "small-nodes-and-disposable-sandboxes" :group "lessons" :area "ops"
   :title "Orchestration on small nodes, builds in disposable sandboxes"
   :text "The coordinator, the sprint server and the stores run on small machines that do nothing else.
    Every build and test runs in a sandbox made for one run and thrown away after it, on a bench, so no
    build loads the coordinating machine and no run inherits another run's leftovers. This moves the
    sprint server and the seat's tools off the machine that coordinates, and makes the container functional tier (under
    setup, release and operations) the way every gate runs."
   :exists "The lander's tree gate runs on ring benches over ssh, staged from each bench's
    mirror (internal/bench/stage_mirror.go:1-40). nova-sandbox confines one command's filesystem reach,
    on darwin only (cmd/nova-sandbox/main.go:1-6); it is not a throwaway machine. The gap is where the
    coordinator itself runs, and a sandbox made and discarded per run."
   :why "A change of where things run, not a fix."
   :date "2026-10-09")
  (item "parity-harness" :group "lessons" :area "quality"
   :title "A differential parity harness for rewrites"
   :text "When a tool is rewritten, the old and the new run the same inputs side by side and their
    outputs are diffed against goldens: terminal frames, transcripts, model requests and protocol
    messages. Beside the diffs, a feature ledger marks each feature of the old tool as matching,
    partial or missing in the new one, and the rewrite ships when no row is missing that the owner has not
    waived. First use: nova-local to barrio. The v1.3 rename of nova-swarm to nova-worker is a rename,
    not a rewrite; its guard is the one-release shim and the existing tests."
   :why "New test machinery, and its first user, barrio, is itself after v1.4."
   :date "2026-10-09")
  (item "performance-hillclimb" :group "lessons" :area "quality"
   :title "A performance hillclimb loop"
   :text "Performance work runs as a loop. Profile, and write each idea down as a hypothesis in a
    backlog. Run the current build and the candidate interleaved on the same bench, many rounds, so
    noise falls on both alike. Keep a change only when it wins the interleaved runs and two reviewers
    agree it does not change behavior. There are no numeric targets: the loop keeps the wins it can
    show. Candidates: the sprint tick, the dashboard's one-second update, and the lander's gate wall."
   :why "Performance work is neither a fix nor v1.4 cleanup."
   :date "2026-10-09")
  (item "dogfooding-stays-required" :group "lessons" :area "quality"
   :title "Parity checks are not proof: dogfooding stays required"
   :text "A parity harness or a verifier shows only what it exercises. A path no golden covers, a model
    that behaves differently under load, a friend's harness on a bad day: none of these shows up in a
    diff. So dogfooding on real work, by the coordinator and the friends, stays a required gate for every
    release, beside the parity and verifier gates and never replaced by them."
   :why "A rule for the stages above; it lands with them."
   :date "2026-10-09")

  ; Measure and rate
  (item "measure-each-tier" :group "measure" :area "nova-config"
   :title "Measure each tier against clean results"
   :text "Each tier's default model (see Done above) is measured on real cards: throughput first, then
    cost per landed card, then wall clock (the owner, 2026-10-07). The baseline is the clean results from
    2026-10-09 22:01Z, when the direct-provider defaults took effect; nothing before it is compared.
    The numbers go to the owner with each tier's spillover routes measured the same way."
   :why "A measurement, not a fix. Mercury 3 on heavy waits for v1.3's global rate limit."
   :date "2026-10-09")
  (item "rerate-every-tool-at-v1-4" :group "measure" :area "quality"
   :title "Rate every tool cold at the v1.4 release"
   :text "When v1.4 is cut, the friends rate every tool again, cold, from its help and its spec alone,
    the way the v1.2.0 ratings were done, and the docs are read cold against the ratings."
   :why "A rating is evaluation, not a fix; rating v1.2.0 now measures a tree v1.3 and v1.4 will change."
   :cards 29
   :date "2026-10-09")

  ; The sprint machine
  (item "policy-numbers-as-settings" :group "ops" :area "nova-config"
   :title "Policy numbers and tool timings as settings"
   :text "Every policy number and tool timing (bounds, waits, widths, deadlines) becomes a named setting
    with its default in one place, instead of a constant in the code."
   :why "New capability."
   :cards 2
   :date "2026-10-09")

  ; Friends
  (item "friend-routes-and-verbs" :group "friends" :area "nova-friend"
   :title "New friend routes and verbs"
   :text "Codex and Claude Code routes into an open chat; nova-friend verbs for peers, screen, watch and
    renew; presence kept off the sprint server; a bus store that does not need the sprint; and the
    friend daemons adopted by the fleet."
   :why "New capability."
   :cards 9
   :date "2026-10-09")
  (item "dsh-harness" :group "friends" :area "nova-friend"
   :title "The DSH harness"
   :text "DSH as a friend harness (union slice C)."
   :exists "Pull request 5507 makes dsh a card runner and a spender for one-shot cards."
   :why "Union slice C, parked by the owner, 2026-10-09."
   :date "2026-10-09")

  ; Setup, release and operations
  (item "setup-planners" :group "ops" :area "setup"
   :title "Setup planners and credential seats"
   :text "Union slice B: a cold setup of a new machine accepted end to end, its launchd units, its Redis
    stores, nova-up for the fleet, a local-only mode, and one nova root layout."
   :why "Union slice B, parked by the owner, 2026-10-09."
   :cards 7
   :date "2026-10-09")
  (item "doctor-preflight" :group "ops" :area "nova-doctor"
   :title "One dependency-aware doctor"
   :text "Union slice H: one nova-doctor that knows the order of the dependencies and checks them as a
    preflight, before anything starts."
   :why "Union slice H, parked by the owner, 2026-10-09."
   :cards 1
   :date "2026-10-09")
  (item "container-functional-tier" :group "ops" :area "ci"
   :title "The functional tier in containers, layers 2 to 8"
   :text "ideas#826: a runner verb, a fixture guard and CI legs for the container tier, then the sprint
    machine, its functional tests and the dashboard running in containers."
   :exists "make test-functional-container runs the functional tier in one container per run
    (tools/functionalrun)."
   :why "New capability."
   :cards 6
   :date "2026-10-09")

  ; Docs, models and the repository
  (item "tla-ledger-union" :group "docs" :area "tla"
   :title "The TLA+ ledger union"
   :text "Union slice M: the TLA+ ledger work from the union manifest."
   :why "Union slice M, parked by the owner, 2026-10-09."
   :date "2026-10-09")

  ; Far
  (item "self-organizing-workers" :group "far" :area "design"
   :title "Thousands of self-organizing workers"
   :text "A system in which thousands of workers organize themselves: they find work, split it, and
    check each other, with no central dealer. The v1.3 rename retires the nova-swarm name, so no tool
    holds the word swarm when this is designed."
   :why "A direction, not a plan."
   :date "2026-10-09")
 
  (item "bus-line-output-facts" :group "ops"
   :title "nova-bus2 output lines state the reason, counts and digests a caller needs"
   :text "The ack, send and recv lines of nova-bus2 carry the facts a caller needs: why an ack was false,
    the byte count and digest of a send, that an empty wait is a result and not a failure, and how
    many messages wait behind the oldest one. Text, JSON and help all say the same thing."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "bus-address-and-identity-defaults" :group "ops"
   :title "nova-bus2 finds its Redis address and caller identity without per-call flags"
   :text "The bus Redis address becomes a field of the fleet row in nova-config, and the sender identity
    gets an environment default, so no caller types them on every call. Every help text states the
    same address precedence, in the order the code applies it."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "bus-redis-loopback-tailnet-guard" :group "ops"
   :title "nova-bus2 refuses a Redis address outside loopback and the tailnet range"
   :text "Before any dial, nova-bus2 refuses a Redis host that is neither loopback nor in the tailnet
    range, with one line and exit code 2. The tailnet is the boundary and there are no ACLs."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "ci-class-spec-and-unit-socket-tests" :group "tests"
   :title "Class tests for the CI spec and for unit socket handling"
   :text "A set of small work steps adds the class tests that hold the CI spec rules and the socket
    behavior of the unit tier. Each step has its own paths, commit and verdict."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "tool-contract-tests" :group "tests"
   :title "Contract tests between the tools: bus, check, config, release, secrets, tokens, version"
   :text "Each tool pair gets a contract test that pins what one tool promises the other, covering the
    bus, nova-check in CI, config, dev release, memory and Redis, secrets, tokens with update, and
    version handling."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "dead-code-ledger-shrink" :group "quality"
   :title "Delete unreachable code and shrink the dead code ledger"
   :text "Packages with functions unreachable from the command roots lose them, and the shrink-only dead
    code ledger drops the matching rows. Removals must keep the functional tests green, so call
    sites are checked first."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); issue #4312")
  (item "generality-ledger-name-removal" :group "quality"
   :title "Remove host, machine and person names from code, docs and fixtures"
   :text "Every row the generality ledger lists is replaced with a generic word, file by file, and the
    ledger shrinks to match."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); card moved out of the sprint (work record, 2026-10-04)")
  (item "named-paths-ledger-shrink" :group "quality"
   :title "Shrink the named-paths ledger to empty"
   :text "The named-paths ledger lists names in the tree that look like repository paths but are not. Each
    row is fixed by rewording or by making the path real, then removed from the ledger."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "forge-merge-spelling-ledger-shrink" :group "quality"
   :title "Remove the last forge auto-merge spelling from the secrets seal"
   :text "One row in the forge-merge-spelling ledger remains for the secrets store's own pull request. It
    is replaced by the batch path so the ledger can empty."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "serial-tests-ledger-shrink" :group "tests"
   :title "Make serial tests parallel-safe and empty the serial-tests ledger"
   :text "Listed tests do not open with t.Parallel because they share an environment, clock or path. Each
    gets a per-test seam so it runs in parallel and leaves the ledger."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); PR #4698; PR #4729")
  (item "unit-tier-slow-test-budget" :group "tests"
   :title "Bring slow test packages under their time budget"
   :text "Unit-tier, class-test and functional CI packages that run over their budget are measured and
    sped up or split until their allowlist rows clear."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); PR #4621; PR #4627; PR #4632")
  (item "bus-store-independent-of-sprint" :group "ops"
   :title "nova-bus and nova-friend do not need the sprint store"
   :text "The bus and friend tools stop importing the sprint's login and fleet row. A user with no sprint
    can run them from the bus address alone."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "bus-wait-and-friend-watch-verbs" :group "friends"
   :title "Wake a coordinator with verbs, not scripts"
   :text "nova-bus wait blocks until the first real message, and a nova-friend verb wraps it for a
    coordinator of friends. This replaces the hand watch script."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "friend-presence-from-the-session" :group "friends"
   :title "Presence reflects the session, not the app or the sprint server"
   :text "A friend reads up only when the session can take a turn, and presence does not depend on the
    sprint server answering. Loops that beat on behalf of an open app are removed."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "friend-broken-session-and-limits-mean-down" :group "friends"
   :title "A broken session or a limit means down"
   :text "A provider refusal, a usage limit or an empty balance is noticed once, said once, and not fed
    more turns. The friend reads down until it recovers."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "friend-delivery-batching-and-receipts" :group "friends"
   :title "Friend messages are batched, receipted and never silently lost"
   :text "Pending messages go as one turn, each carries a receipt from delivery to returned work, the
    failed-delivery count survives a restart, and a deaf friend is detected."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04); issue #5192; card from the sprint store
    (2026-10-10); card moved out of the sprint (work record, 2026-10-04)")
  (item "package-test-harness-steps" :group "tests"
   :title "Per-package test harness steps"
   :text "Stepwise work cards add or repair tests and checks per package across the tools. Each step has
    its own paths, commit and verdict."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "release-status-grammar" :group "quality"
   :title "One status grammar for the release verbs"
   :text "nova-update release uses the standard's three status words, one cap flag and one refusal shape.
    The remaining FAIL sites are converted."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "model-tests-per-package" :group "tests"
   :title "Model-based tests for the bus and config packages"
   :text "Add model tests for the bus and config packages, one work step each, with the test checked
    against the package model."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "prose-pass-per-package" :group "quality"
   :title "Prose pass over each tool package"
   :text "Reword the help, comments and messages of each tool package for plain English and short
    sentences. Each package is a small tree of work steps with its own paths and commit."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "read-pass-per-package" :group "quality"
   :title "Read-through of each tool package for defects and debt"
   :text "Walk each tool package in order, read it against its rules, and record or fix what is found.
    Each package is a small tree of work steps with its own paths and commit."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "bench-run-verb" :group "ops"
   :title "A verb that runs a card or read on a remote bench machine"
   :text "Every card, read and coordinator run on a Linux bench uses a recipe of ssh, rsync and
    environment setup typed into each brief. A real verb does the sync, the run and the collection
    of results."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-one-shot-lanes-replace-runner-scripts" :group "friends"
   :title "One-shot friend lanes replace the hand-written runner scripts"
   :text "nova-friend runs cards through each harness in one-shot lanes with a limit-aware pause, and the
    stopgap shell runners are deleted."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card moved out of the sprint (work
    record, 2026-10-04)")
  (item "tool-cold-read-fix-steps" :group "quality"
   :title "Fix the defects found by cold reads of the tools"
   :text "Cold readers of config, fuse, memory, redis, self-talk, tokens and update found defects. Each is
    fixed as a small ordered step with its own check and commit."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "shrink-small-tool-packages" :group "quality"
   :title "Shrink every tool package in small ordered steps"
   :text "Each tool package loses dead code, duplicated helpers and lint findings in small checked steps,
    with behaviour and tests unchanged."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "sandbox-symlink-spelling-unreadable" :group "ops"
   :title "Make a symlink spelling of a granted path readable in the darwin sandbox"
   :text "On darwin the sandbox policy keeps only the resolved path, so reading a granted path by its
    symlink spelling fails inside the wall. Grant read on the link itself as well as on its target."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "fuse-box-write-and-parse-hardening" :group "ops"
   :title "Harden nova-fuse box writes, box parsing and refusal lines"
   :text "Quarantine and lockdown are unlocked read-modify-write cycles, so a losing run can report
    success and be overwritten. The box reader accepts misspelled, duplicated or null keys as clear,
    and one refusal line echoes unescaped shell characters."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "bounded-work-on-large-input" :group "ops"
   :title "Bound the work done on very large or hostile input files"
   :text "The memory index reads whole files with no size limit, and nova-check links takes quadratic time
    on a line of unclosed brackets. Add size limits and deadlines so one planted file cannot exhaust
    memory or time."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "table-reserved-key-families" :group "ops"
   :title "Reserve the view key family in table member and epoch keys"
   :text "The reservations for epoch keys and member prefixes cover table keys but not the view family
    that the view verbs own. Extend the reserved set so a user key cannot collide with view state."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "secrets-gate-hand-sealed-seats" :group "ops"
   :title "Make the secrets gate tell hand-sealed files from verb-made ones"
   :text "A file sealed by hand looks the same to the gate as a file a verb wrote, and the refusal line
    prints the rule position instead of naming the rule. Mark verb-made files and name the failed
    rule in the refusal."
   :date "2026-10-10"
   :release "v1.3"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "adopt-testify-assertions" :group "tests"
   :title "Adopt the testify assertion library in tests"
   :text "Hand-written test assertions are replaced with the testify assertion library by a scripted,
    mechanical rewrite. The change is checked by running the tests."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "tests-hermetic-package-env" :group "tests"
   :title "Package tests run in a hermetic environment"
   :text "Tests for the secrets, update, tokens, version and test-binary packages build their own
    temporary environment instead of reading the real one. This makes them repeatable on any
    machine."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); PR #5519")
  (item "tests-injected-clock" :group "tests"
   :title "Tests use an injected clock and never wait on the wall clock"
   :text "A shared test clock replaces real sleeps, fixed waits and tight elapsed-time assertions, so
    timing tests are fast and do not redden on loaded runners."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); issue #4221; issue #2452; issue #2460")
  (item "docs-cli-reference-generated-and-checked" :group "docs"
   :title "Generate the CLI reference from the tools and check it in CI"
   :text "The CLI reference is built from each tool's own usage text, and a test fails when the docs and
    the binaries drift apart."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store
    (2026-10-10); issue #3836")
  (item "docs-concept-and-onboarding-pages" :group "docs"
   :title "Architecture, glossary, onboarding and per-tool readmes"
   :text "Write an architecture page, glossaries, a friend onboarding guide and a standalone readme for
    every tool, so a stranger can start from the docs alone."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "no-shell-scripts-in-shipped-tree" :group "quality"
   :title "No shell scripts in anything that ships"
   :text "The tree still tracks a few shell scripts (a notes script and test fixtures). Each becomes a Go
    verb or a Go test helper."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04)")
  (item "bus-client-network-deadlines" :group "ops"
   :title "Bound every network wait in the bus client"
   :text "List every deadline the bus client sets (dial, read, write, blocking wait margin) and add the
    missing ones, so a loaded machine cannot stall a bus call without limit."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "ci-tool-class-checks" :group "ops"
   :title "Check CI tool classes"
   :text "Make the CI checks that classify tools run and report per class."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "decide-help-smallest-example" :group "docs"
   :title "nova-decide help: small schema and one result line"
   :text "The help page shows a small schema, state and answers, and one result line saying exit 0 means
    recorded, not approved."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "goroutine-leak-checks-in-tests" :group "tests"
   :title "Goroutine leak checks in tests"
   :text "Add goleak checks to library tests so leaked goroutines fail the test."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "darwin-sandbox-directory-lookup" :group "ops"
   :title "Allow directory lookup in the darwin sandbox profile"
   :text "The darwin wall denies a directory membership lookup that stalls sqlite3 and git for about 0.8 s
    per launch; allow it with the measurement in the commit."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "vet-clean-table-tests" :group "quality"
   :title "Make ntable tests vet-clean"
   :text "go vet fails on the ntable test files; fix them with the cause named and keep vet in the gate."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "push-notifications-required-at-setup" :group "ops"
   :title "Push notifications are required before inbox tools work"
   :text "nova-bus and the sprint refuse to work until the coordinator has wired push notifications to its
    inbox, and audit that the push arrives."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "adoption-pipeline-automated" :group "ops"
   :title "Adopting a new build is an automated pipeline"
   :text "Building, rollback copies, cold read, switch and fleet push for an adoption run as a machine
    pipeline that surfaces judgments to the coordinator."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04); issue #4306")
  (item "friend-session-auto-recovery" :group "friends"
   :title "A broken friend session renews itself"
   :text "The friend daemon renews a session it marked broken, in the same harness, and exposes the
    renewal as a verb. No person has to renew it by hand."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "friend-back-up-detection-and-adopt" :group "friends"
   :title "Friends and machines noticed back up and made current"
   :text "A hold or down state carries its cause and end time, and the tick notices when it ends.
    Returning friends and fleet machines adopt the latest tools before taking work."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "spend-reconciliation-and-route-price-refresh" :group "measure"
   :title "Captured spend matches the provider's account"
   :text "Price every run, read and retry so recorded spend matches the provider's own account. Route
    prices are refreshed from the provider list instead of set once by hand."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04); issue #2171")
  (item "config-dsn-keyword-test-fix" :group "tests"
   :title "Fix the red DSN keyword flag test in config"
   :text "The config test for a DSN keyword flag with a password fails; either the test or the resolver is
    wrong and the fix says which."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-delivery-test-flaky" :group "tests"
   :title "Fix the flaky deferred delivery test in the friend package"
   :text "The deferred delivery test fails in some runs; the race is found and fixed with an injected
    clock and no real time."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "tokens-reads-redis-bus" :group "ops"
   :title "nova-tokens reads the Redis bus log"
   :text "nova-tokens --bus reads the Redis bus log instead of the old note directories, and the spec,
    test transcript and dead fixtures are updated."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "bus-message-sender-authority" :group "friends"
   :title "Mark bus messages by sender authority in a friend's session"
   :text "A friend delivers a bus message as an instruction only when it comes from the coordinator seat
    holder. Every other message, and every message while the seat is unknown, is delivered as data."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "never-rewrite-shared-refs-test" :group "tests"
   :title "Class test that nothing rewrites a shared ref"
   :text "A CI class test greps Go sources, scripts, workflows and card templates for force-push and
    history-rewrite patterns against shared refs, with a reasoned allowlist."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "secrets-never-in-errors-test" :group "tests"
   :title "Class test that secret-shaped inputs never appear in errors"
   :text "Drive secret-shaped strings through every exported Open, Parse, Dial and New function and fail
    if a marker appears in an error. A DSN parse error once printed a password."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "class-tests-duplicate-and-unused-surface" :group "quality"
   :title "Class tests for duplicate function bodies and unused verbs and flags"
   :text "One CI class test hashes normalised function bodies to find duplicate paths. Another finds verbs
    and flags no test, doc or caller uses."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "retire-stopgap-scripts" :group "quality"
   :title "Operator and stopgap scripts become tested verbs"
   :text "Every hand script that runs the fleet or the coordinator becomes a documented, tested verb, so a
    stranger can adopt the tools without private scripts."
   :date "2026-10-10"
   :release "v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); issue #1142; issue #2051; card moved
    out of the sprint (work record, 2026-10-04)")
  (item "friend-token-budgets" :group "friends"
   :title "Per-card token cap and cheaper deliveries for per-token friends"
   :text "Every one-shot friend lane counts tokens and stops at a per-card cap. Briefs cite rules by
    reference and each prompt starts with one stable prefix to cut tokens per landed card."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "cards moved out of the sprint (work record, 2026-10-04); card from the sprint store (2026-10-10)")
  (item "script-verify-wired" :group "quality"
   :title "Re-land and wire script-card self-verification"
   :text "A script card whose head equals its program's output needs no model read. The verifier was
    implemented but never wired into the worker, so it was removed as dead code. Re-land it and wire
    it."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "route-applies-to-executor-class" :group "ops"
   :title "A route says where it is applied: friends, fleet or local"
   :text "A route gets a mask for which executor classes use it, rather than being switched on or off for
    everyone. Pro routes then serve friends and flash routes serve the fleet."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-daemon-liveness" :group "friends"
   :title "Show and alarm friend daemon state, and ping every friend each second"
   :text "A test pins the installed service definition, and a view shows which binary each friend daemon
    runs and alarms when one died or was not reinstalled. A nova-friend verb replaces the hand ping
    loop and marks a friend down after ten seconds without a pong."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-lane-wall-profile" :group "friends"
   :title "Run every friend lane inside a wall profile taken from its friend row"
   :text "A lane child runs inside the wall, with writes allowed only to the friend's own working
    directories. This replaces stopgap runners that run without a wall."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "card moved out of the sprint (work record, 2026-10-04)")
  (item "redisconn-adoption-in-tools" :group "quality"
   :title "Tools open Redis through the redisconn package"
   :text "nova-config and other tools open Redis through the shared connection package, which explains its
    own failures, and delete their wrappers."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #4506; PR #4518")
  (item "tokens-daily-collate-verb" :group "ops"
   :title "nova-tokens gains the daily collate verb"
   :text "The daily token collation moves into nova-tokens as a verb."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4594")
  (item "self-check-reconcile-verb" :group "quality"
   :title "A reconcile verb compares self-check answer files to the baseline"
   :text "The verb checks answer files against the baseline question set and reports the accounting."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4597")
  (item "update-revert-on-red-cancelled-run" :group "ops"
   :title "A cancelled workflow run counts as red for revert-on-red"
   :text "Revert-on-red treats cancelled runs as failures so a cold-cache cancellation does not leave a
    bad main."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #4600")
  (item "ci-queue-and-flake-verbs" :group "ops"
   :title "nova-ci prints merge-queue status and detects flaky tests"
   :text "A queue verb prints each entry with failure receipts. A flake verb reruns tests in isolated
    processes to separate stable from flaky."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4605; PR #4612")
  (item "check-exec-deadline-rule" :group "quality"
   :title "nova-check flags exec.CommandContext calls without a deadline"
   :text "An AST rule flags command contexts that carry no timeout or deadline."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4614")
  (item "update-install-verification-matrix" :group "tests"
   :title "An install verification matrix runs across darwin, linux and wsl2"
   :text "Functional tests verify nova-update install on each bench platform."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #4615")
  (item "table-batch-model-and-replay" :group "docs"
   :title "A TLA+ model of atomic table batches and a replay check against real Redis"
   :text "The model covers guards, moves, scores, creation and removal, and tlacheck replays captured
    execution traces against it."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4618")
  (item "table-multi-table-batches-and-receipt-pages" :group "far"
   :title "Atomic batches across several tables and gap-refusing receipt pages"
   :text "One batch applies across 2 to 16 tables, and receipt pages refuse missing history. Creates over
    256 cells are refused before scoring."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4714; PR #4715; PR #4721")
  (item "tool-output-and-input-bounds" :group "quality"
   :title "Listing verbs take a max flag and stdin input is bounded"
   :text "Config, table, swarm and secrets list verbs get a max flag (default 20, 0 for unlimited). Cairn
    reuses its computed index and bounds stdin at 10 MiB."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #4628; PR #4630; PR #4631; PR #4633; PR #4638")
  (item "swarm-hold-zero-findings-malformed" :group "quality"
   :title "A HOLD report with zero findings is malformed"
   :text "A verdict of HOLD cannot have no findings, so the result check classes it as malformed."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #4635")
  (item "cairn-bench-store-open-and-append-lock" :group "quality"
   :title "Cairn open works on a bench store and an append is one critical section"
   :text "Open creates the session file in the store's own shape. An append is one critical section per
    store and empty words are refused."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #4700; PR #4702")
  (item "ci-ledger-shard-diagnostics" :group "quality"
   :title "CI ledger failures name their shard and zero-debt shards stay clean"
   :text "Package-ledger failures name the owning shard file and a shard at zero debt needs no hand edit."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #4956")
  (item "swarm-harness-error-cause-recorded" :group "friends"
   :title "The harness prints its error lines so an unknown error records its cause"
   :text "Every launch runs the harness with error lines printed so a failure reported only as an unknown
    error is recorded with its real cause."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #5028")
  (item "pin-mutation-gaps-swarm-ci" :group "tests"
   :title "Tests pin the surviving mutation gaps in swarm and ci"
   :text "Added tests kill the mutants that survived in the swarm and ci packages."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5073")
  (item "docs-and-comments-present-tense" :group "quality"
   :title "Docs and code comments describe current behavior only"
   :text "History, names, dates and past-tense rationale are removed from docs and comments, broken
    comment fragments are repaired, and generality ledgers shrink."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5103; PR #5109; PR #5110; PR #5128; PR #5130; PR #5143; PR #5144; PR #5226")
  (item "ci-pinned-redis-server" :group "ops"
   :title "install-redis-server puts the pinned Redis first and testredis refuses others"
   :text "The installer keeps a PATH redis-server only if it reports the pinned version, and the test
    helper refuses any other version."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #5155; issue #5151")
  (item "sandbox-temp-and-symlink-containment" :group "quality"
   :title "Contain the default temp directory; refuse symlinks on swarm auth, config, usage writes"
   :text "The default temp directory is checked against the write directories. Auth, job config and usage
    writes refuse a symlink at or below the data home."
   :date "2026-10-10"
   :release "v1.3"
   :origin "PR #5248; PR #5251")
  (item "kind-classification-single-source" :group "quality"
   :title "Read kind classification from the one name file"
   :text "The kind list's third field is the only gated or ungated classification, and the second name set
    is deleted. Acceptance stays unchanged."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5253")
  (item "swarm-child-instructions-job-profile" :group "docs"
   :title "Align child instructions with the staged job profile and name report destinations"
   :text "Worker instructions follow the staged job profile: ownership of cache and gates, truthful
    attribution, and plain versus command finish. Plain profiles name the result file and PR body as
    report destinations."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5266; PR #5271")
  (item "functional-tier-darwin-pool-and-fixtures" :group "tests"
   :title "Run Darwin-only functional packages on the Mac pool and repair functional fixtures"
   :text "Darwin-only packages selected for functional tests are routed to the configured Mac pool instead
    of dropping out of the Linux matrix. Functional fixtures are repaired."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5272; PR #5503")
  (item "friend-foreground-wait-presence" :group "friends"
   :title "nova-friend scopes presence to a foreground harness wait"
   :text "A watch-only command runs a harness wait and beats the sprint server while the child is owned.
    It cancels on context cancellation, parent exit, pipe closure or beat failure."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #5273")
  (item "nova-local-provider-per-machine" :group "far"
   :title "nova-local: run local models, and local as a provider per fleet machine"
   :text "Restore nova-local built to its spec, with local as a provider per fleet machine that the sprint
    deals to like any route."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "PR #5307; cards moved out of the sprint (work record, 2026-10-04)")
  (item "test-speed-compaction-and-parallel" :group "tests"
   :title "Shorten test waits, compact table tests and run tests in parallel"
   :text "Raise short wait bounds, merge similar tests into table-driven tests, and pass environment
    through parameters so tests run in parallel."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5310; PR #5328; PR #5345")
  (item "modernize-tree-with-go-fix" :group "quality"
   :title "Run go fix and the modernize analyzers over the tree"
   :text "Apply the standard library analyzers across the tree, one commit per analyzer, on all platforms
    and build tags."
   :date "2026-10-10"
   :release "v1.4"
   :origin "PR #5313")
  (item "loop-log-rotation" :group "ops"
   :title "Loop logs rotate by size and age"
   :text "Member and reader logs grow without bound today. A rule in the logs spec and the loop
    definitions bounds them."
   :date "2026-10-10"
   :release "v1.3"
   :origin "nova-sprint issue #41")
  (item "bench-hygiene-home-guard" :group "ops"
   :title "The bench hygiene script refuses an unsafe HOME"
   :text "The script runs with an empty, root, relative or one component HOME. It refuses those, since
    every path it may remove is built from HOME."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1282")
  (item "bus-per-session-cursor" :group "friends"
   :title "nova-bus gives each session of one participant its own cursor"
   :text "Two sessions of one participant share a single cursor, so the second has no usable inbox advance
    or wait. A per session cursor fixes this."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1401")
  (item "bench-spotlight-exclusion" :group "ops"
   :title "Darwin benches exclude work trees and temp dirs from indexing"
   :text "The system indexer loads the machine while tests run, which skews measurements. Setup excludes
    the work tree and the temp dir from indexing."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1432")
  (item "sandbox-cli-reporting-defects" :group "quality"
   :title "nova-sandbox reports the right cause and the right backend"
   :text "An ssh host alias origin is reported as an unreachable forge, and a dot repo is refused. The
    policy verb prints the darwin profile under the landlock backend."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1452; issue #1469")
  (item "self-talk-first-person-absolutes" :group "quality"
   :title "nova-self-talk detects plain first person absolutes"
   :text "The three plainest absolute claims about what the writer permanently is or cannot do return no
    claims. The detector counts them."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1468")
  (item "swarm-dispatcher-asks-ladder" :group "friends"
   :title "The pool dispatcher asks the model ladder"
   :text "The dispatcher picks a model from the worker and profile files and never asks the ladder. A card
    the ladder marks for a child or the bus must not run on a mechanical model."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1486")
  (item "swarm-native-budget-caps-and-idle-watch" :group "friends"
   :title "Token, call, cost and in-flight limits per card and per route"
   :text "The native path enforces token, call and cost caps and an idle watch, budgets follow each
    route's cost and caching, and requests in flight are capped per route."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1545; issue #1811; issue #2580; issue #5094; issue #917")
  (item "swarm-native-result-honesty" :group "quality"
   :title "nova-swarm native reports an honest result and one reason"
   :text "A run that produced nothing is not reported as delivered, one line never carries two reason
    keys, and the input limit class comes from a structured signal instead of a transcript
    heuristic."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1844; issue #1611; issue #163")
  (item "swarm-slot-lease-release-by-id" :group "ops"
   :title "The dispatcher releases a slot lease by its own id"
   :text "Release removes every lease with the same owner and label. It releases only the one lease it
    took."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1582")
  (item "swarm-native-capture-spoofing" :group "ops"
   :title "A card cannot spoof the native result or the idle watch"
   :text "A card can rewrite the harness capture file to turn a denial into an OK, and write files the
    monitor reads as liveness. The supervisor reads these from places a card cannot write."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1892; issue #1893")
  (item "ci-drop-hosted-windows-smoke" :group "ops"
   :title "The smoke job drops its hosted windows leg"
   :text "Native windows is no longer supported, only WSL. The remaining hosted windows leg of the smoke
    job is removed."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #1487")
  (item "spec-work-harness-wake-table" :group "docs"
   :title "SPEC-WORK records which harnesses a process can wake"
   :text "The per harness table states the non interactive run verb and whether a process can start a
    bounded run. Some harnesses cannot be woken and one is now refused."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #1519")
  (item "ci-wallclock-law-covers-all-units" :group "tests"
   :title "The wall clock bound law sees string flags and sub second units"
   :text "The test that bans short wall clock bounds only matches the literal seconds unit. Millisecond
    literals and string flag values slip past it."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #1604")
  (item "swarm-order-dependent-test" :group "tests"
   :title "TestARouteAtItsCapHoldsTheRestBack no longer depends on test order"
   :text "The test passes only in a full package run and fails alone or when any test is added. It is made
    independent."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #1927")
  (item "update-adopt-partial-release-dir" :group "ops"
   :title "Release adopt does not trust a partial release directory"
   :text "A killed transfer leaves a partial release dir that the next adopt treats as complete. Adopt
    verifies completeness or uses a temporary dir renamed at the end."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1981")
  (item "swarm-test-hang-capped-flags" :group "tests"
   :title "A swarm test hangs the whole package under the gate's capped flags"
   :text "One idle-watch test hangs for the full timeout under capped gate flags. It must finish fast and
    not stall the package."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1983")
  (item "release-draft-upload-boundary" :group "ops"
   :title "Release upload must respect the draft then publish sequence"
   :text "The release workflow created a separate published release instead of uploading to the reviewed
    draft. Upload goes to the draft, then it is published."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #199")
  (item "swarm-provider-error-classify-requeue" :group "friends"
   :title "Classify provider errors and re-queue on another route"
   :text "A provider server error ends a card with no result after in-place retries. The verdict names
    PROVIDER and the card is re-queued on another route."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2001; issue #2011")
  (item "fleet-scripts-bash32-lint" :group "ops"
   :title "Fleet scripts run under bash 3.2 and are linted for it"
   :text "Launchers run under the old macOS bash and must not use newer builtins or rely on zsh word
    splitting. A lint rejects them."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2012")
  (item "secrets-seal-branch-and-bulk-add" :group "ops"
   :title "Secrets seal leaves the store on a branch; add a bulk-add verb"
   :text "Seal without a pull request leaves the store on a local branch and exec then refuses every card.
    Seal restores the store, and one verb adds a name to many seats."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2016; card from the sprint store (2026-10-10)")
  (item "fleet-sampler-never-reads-zero" :group "ops"
   :title "A missed or wedged fleet sample never reads as zero"
   :text "A partial or missed sample read as a full machine and a wedge froze the width table. The view
    reads slot stores directly and shows NA for unreachable hosts."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2017")
  (item "bench-standard-uniform-check" :group "ops"
   :title "Bench standard covers real differences and checks reliably"
   :text "The bench check gave different verdicts on one machine and missed ssh limits, toolchain
    versions, layout and users. The manifest covers them and the check builds its own environment."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2018; issue #2052; issue #2053; issue #2054; issue #2055; issue #3201")
  (item "update-adopt-at-frozen-sha" :group "ops"
   :title "Adopt levels the fleet at a frozen sha from the nearest bench"
   :text "Adopt takes an explicit sha instead of chasing a moving dev, and large payloads come from the
    nearest datacenter machine."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2023")
  (item "guard-tests-for-unguarded-commits" :group "tests"
   :title "Add tests that guard code reverted without any test failing"
   :text "Three landed commits can be reverted with no test going red. Each gets a test that fails when
    the change is reverted."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2024; issue #2025; issue #2026")
  (item "harvest-refuse-stale-base" :group "ops"
   :title "Harvest refuses a returned branch based on a stale head"
   :text "A returned branch based on an old head would have reverted merged work. Harvest checks the diff
    against the current target and rebases, re-gates or refuses."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2032")
  (item "native-launch-capacity-ceiling" :group "ops"
   :title "Enforce the per-machine card ceiling before launch"
   :text "A machine reached extreme load and fell off the network before the guard acted. The slot store
    enforces a ceiling from measured memory and a process limit before launch."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2033")
  (item "bus-draft-no-overwrite" :group "ops"
   :title "Bus draft never overwrites a file and send refuses the template"
   :text "Draft overwrote an existing note and send accepted the untouched template, so an empty note went
    out. Draft refuses to overwrite and send refuses the template."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2043")
  (item "gate-branch-on-bench-verb" :group "ops"
   :title "Run a branch's gate on a bench as one verb"
   :text "A verb bundles, copies and runs a branch's gate on a bench, keyed by sha and package set, so
    nothing is built on the coordinator."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2048")
  (item "swarm-idle-reap-verdict" :group "friends"
   :title "Idle-reaped cards report their real cause and are not wrongly reaped"
   :text "A reaped card reports a wall refusal that names nothing, and the harness shell cap silenced
    working cards. The verdict states idle reaping and the idle watch accounts for the shell cap."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2157; issue #2158; issue #2577; issue #2579; issue #2585")
  (item "launcher-base-staging-darwin" :group "ops"
   :title "Pinned base staging works on darwin launchers"
   :text "The pre-model base clone exists only in Linux launchers, so a pinned base is silently absent on
    darwin. Both paths stage it."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2163")
  (item "fleet-capacity-central-config" :group "ops"
   :title "Fleet capacity lives in one config, applied without restarts"
   :text "Per-machine capacity is scattered in launcher constants and loop arguments, so raising it needs
    a restart under load. One config holds it and is read live."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2164")
  (item "swarm-native-no-wall-fake-harness" :group "tests"
   :title "Fake harness hangs inside the wall; document no-wall"
   :text "A local fake harness hangs silently until the deadline inside the wall, and the no-wall flag is
    missing from help. It fails fast and help lists the flag."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2168")
  (item "check-nocode-staged-verb" :group "ops"
   :title "nova-check nocode reads the staged index by destination"
   :text "Wire the staged verb: required directory, root test, base detection, exit codes, and content
    read by destination object through one framed batch."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2295; issue #2296")
  (item "spec-pin-boot-pin-file" :group "tests"
   :title "Tests pin every rule each tool's spec states"
   :text "Each tool's spec rules (refusals, ordering, bounds, edge cases) get a test that fails when the
    behaviour drifts."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2304; issue #2298; issue #2299; issue #2300; issue #2301; issue #2303; issue #2305; issue
    #2306; issue #2307; issue #2293; issue #2309; issue #2310; issue #2311; issue #2313; issue
    #2297; issue #2376; issue #2377; issue #2172; issue #2173; issue #2174; issue #2257; issue
    #2285; issue #2286; issue #2193; issue #2194")
  (item "secrets-demanded-tests-body-audit" :group "tests"
   :title "Confirm the demanded secrets tests assert their rules"
   :text "All demanded tests exist by name, but readings disagree whether some bodies assert the rule.
    Audit and strengthen those bodies."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2314")
  (item "swarm-profile-catalog-validation-tests" :group "tests"
   :title "Test swarm profile catalog validation, paths, digest and read bounds"
   :text "Tests pin the catalog's strict JSON refusals, absolute path rules, whole-catalog digest and read
    size and time bounds."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2365; issue #2367")
  (item "swarm-attempt-evidence-and-launch-record" :group "tests"
   :title "Implement and test attempt evidence publication and the protected launch record"
   :text "The worker publishes task, prompt, profile and manifest evidence in order and recovers from it.
    The launch record and its reservation refusals are pinned by tests."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2370; issue #2371")
  (item "card-repo-staging-from-mirror" :group "ops"
   :title "Stage card repositories from the bench mirror at the named base, never from the network"
   :text "A card gets its repo from the local mirror at the base it names, the staged tree is checked
    against that base, and lint refuses a URL clone. Adopt refreshes the mirrors."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2378; issue #2383")
  (item "worker-startup-preflight-and-bench-readiness" :group "ops"
   :title "Preflight the worker start and mark a bench that cannot run cards as down"
   :text "A short startup check refuses with a named cause before any model call. A bench whose harness
    cannot start inside the wall is down for cards."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2384; issue #2388")
  (item "darwin-silent-harness-idle-kills" :group "friends"
   :title "Fix darwin cards that go silent under the wall and are idle-killed"
   :text "On macOS benches cards and their subagents go quiet after minutes and are killed as idle, while
    Linux benches succeed. The cause is found and fixed."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2533; issue #2535; issue #2058; issue #2328")
  (item "fleet-toolchain-pins-and-card-env" :group "ops"
   :title "Pin toolchains and declare a card environment on every bench"
   :text "The playbook pins one toolchain set on every bench and writes an allowlisted card environment.
    Benches then gate the same legs alike."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2415; issue #2553; issue #2554; issue #1500; issue #1948; issue #1993")
  (item "fleet-sshd-limits-and-loop-supervisors" :group "ops"
   :title "Set sshd limits and supervise coordinator loops on every host"
   :text "Setup raises the sshd session and startup limits on macOS benches, and every long-running loop
    runs under systemd or launchd with a durable wake instead of nohup."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2426; issue #2551; issue #2459")
  (item "worker-question-ending-asked" :group "friends"
   :title "End a card as asked when its last turn is a question"
   :text "A headless card whose last turn asks the user a question ends at once as asked instead of
    holding its slot to the deadline."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2548")
  (item "central-log-shipper" :group "ops"
   :title "Ship bench and loop logs to one queryable place"
   :text "Install a log shipper on every bench that sends harness, bench and loop logs to one log store,
    so faults are found by query and not by grep over ssh."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2557")
  (item "model-gateway-evaluation" :group "far"
   :title "Evaluate a model gateway for timeouts, retries and cost rows"
   :text "Evaluate a gateway in front of every harness that gives body and read deadlines, one retry,
    per-provider rate limits and a cost row per request, with numbers on the added hop."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2558")
  (item "bus-listing-output-honesty" :group "ops"
   :title "Bus and swarm listings say what they read and cap their output"
   :text "nova-bus inbox must report a bounded walk and open items, nova-bus check --full prints a capped
    list with a count, and empty swarm listings print a count line, never zero bytes."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2568; issue #2574; issue #2573; issue #2308")
  (item "bus-wait-recover-stale-lock" :group "ops"
   :title "nova-bus wait recovers a stale index lock and a dirty lane file"
   :text "When a checkout holds a stale git lock or a dirty lane file, nova-bus wait recovers or names the
    exact remedy once instead of failing every tick."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2627")
  (item "check-hygiene-stray-coverage" :group "ops"
   :title "Hygiene checks share one source with the harvest lists"
   :text "The hygiene stray-name list and secret key shapes come from the same source the harvest uses, so
    they cannot drift."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2570; issue #1899")
  (item "sandbox-wrap-denial-notes" :group "ops"
   :title "The sandbox wrap verb prints denial lines and platform notes"
   :text "SANDBOX DENIED lines and the macOS sandbox note are wired into the wrap verb as well as run, so
    a contained command that fails names the path it was refused."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2581")
  (item "card-env-allowlist-exfil-fence" :group "ops"
   :title "Card environment is an allowlist and harvest refuses files from outside the job tree"
   :text "A card runs with an allowlisted environment, the sandbox prints refusals before work starts, and
    harvest refuses a PR that adds files from outside the job tree or matching a secret shape."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2609")
  (item "worker-plan-mode-subagent-eval" :group "far"
   :title "Evaluate plan-then-execute cards and worktree-isolated subagents"
   :text "Evaluate a plan turn that a reader approves before execution, bounded multiple-choice questions
    instead of free-text asks, and worktree-isolated subagents, lifting the no-subagents rule only
    on a measured success rate."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2590; issue #2591")
  (item "decide-event-optional-fields" :group "quality"
   :title "decide events carry confidence and floor only when present"
   :text "Confidence and floor in a decision event use presence-aware types so a missing value is not
    emitted as a false zero."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2633")
  (item "tokens-bulk-session-ingest" :group "ops"
   :title "nova-tokens fold ingests a whole session store for one provider"
   :text "The fold accepts a directory of provider session exports in one flag instead of one flag per
    file."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2671")
  (item "gh-rest-only-verbs" :group "ops"
   :title "Tools use REST with conditional requests and a shared fact cache for GitHub"
   :text "Every tool verb that reads GitHub uses REST calls with conditional requests and one shared
    per-tick cache of PR facts, so the GraphQL secondary limit stops nothing."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2732")
  (item "ci-cancelled-under-load" :group "ops"
   :title "CI runs on card PRs are not cancelled under load"
   :text "CI runs at the head of card PRs are cancelled mid-job when many PRs open together; the
    concurrency rule is fixed so each head gets one full run."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2795")
  (item "route-provider-pinning-and-logging" :group "measure"
   :title "Pin the upstream provider per route and log it in usage rows"
   :text "Routes through an aggregator pin the provider order and forbid fallbacks, so the same model does
    not cost many times more through another door. The chosen provider is logged per usage row so
    cost can be traced."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3151")
  (item "human-wait-push-notice" :group "ops"
   :title "Push one notice when a human-path step or decision passes its deadline"
   :text "A step that waits on a person, or a decision past its deadline, sends one push notice over the
    private network. It fires once and stays quiet after."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3161")
  (item "release-adopt-per-machine-platform" :group "ops"
   :title "Release adopt picks each machine's platform from the registry"
   :text "With no platform flag, adopt sends the wrong binary to machines of another platform. It reads
    each machine's OS and architecture from the registry so a mixed list adopts in one call."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3225")
  (item "swarm-job-dir-removal-at-card-end" :group "ops"
   :title "Every card, lane and pool removes what it made when it ends"
   :text "Job directories, finished slots and small launch files are removed at card end by default, with
    results kept elsewhere, so disk is never cleaned by hand."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3317; card from the sprint store (2026-10-10); PR #5125; nova-sprint issue #37;
    nova-sprint issue #39; nova-sprint issue #40")
  (item "ssh-exec-sites-onto-bench-runner" :group "quality"
   :title "Move the remaining ssh exec sites onto the shared bench runner"
   :text "Seventeen call sites still run ssh their own way. They move onto the one bench runner with its
    host guard."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #3350")
  (item "sandbox-socket-test-race" :group "tests"
   :title "Fix the sandbox unix-socket wall test racing its listener"
   :text "The test treats the socket file existing as ready, but the file appears before the socket
    accepts. It waits for a real accepted connection."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3391")
  (item "wake-serve-blind-commit-limit" :group "ops"
   :title "nova-wake serve reports blindness instead of healthy when far behind"
   :text "Serve cannot see a reader whose cursor is over the commit limit behind, yet ends with zero
    failures and exit zero. It raises the limit or exits with a failure."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3445")
  (item "tokens-redis-user-auth" :group "ops"
   :title "nova-tokens ledger and report accept a Redis user"
   :text "The documented Redis call authenticates as the default user and is refused. A user flag makes it
    work against the fleet store."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3461")
  (item "tokens-ledger-publish-and-per-unit" :group "measure"
   :title "nova-tokens publishes day rows and reports cost per unit"
   :text "A verb publishes per-day, per-model, per-repository token rows to the ledger repository. Turns
    are joined to queue leases to report cost per unit by kind."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3465; issue #3486")
  (item "swarm-doctor-checks-running-executable" :group "ops"
   :title "nova-swarm doctor checks the running executable, not only PATH"
   :text "The launch preflight refuses a new binary found on PATH but passes the same binary by absolute
    path. It checks the executable that is actually running."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3469")
  (item "swarm-providers-table-verb" :group "docs"
   :title "A nova-swarm verb prints the providers table and the launcher argv"
   :text "No verb shows the embedded providers table or the one launcher's command line in a dry run. A
    providers verb prints both."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3471")
  (item "seat-key-isolation-per-user" :group "far"
   :title "One unix user per seat so a friend's key is not readable by every process"
   :text "All friend processes share one user and key files are readable by all. Separate users per seat
    make the claim that a friend cannot forge another's line hold."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3500")
  (item "cli-help-and-usage-consistency" :group "docs"
   :title "Every verb answers --help with accurate usage and runnable examples"
   :text "Each verb prints its own help for --help, usage matches what the parser accepts, example lines
    run when pasted, and a refusal points to the help."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3508; issue #3509; issue #3517; issue #3549; issue #2575; issue #1451; issue #1656")
  (item "swarm-slots-list-summary-line" :group "ops"
   :title "nova-swarm slots list prints a summary line and the live leases"
   :text "The verb prints nothing even with live leases. It prints a summary line like the other read
    verbs, even at zero."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #3523")
  (item "tailnet-policy-gitops" :group "ops"
   :title "Manage the tailnet policy file through GitOps"
   :text "The tailnet access policy lives in the repository and an action applies it, once the verb's home
    is decided."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3535")
  (item "bench-credential-store" :group "far"
   :title "A bench credential store with find and put on any platform"
   :text "A store with find and put works on every platform, using the system keychain where one exists.
    It is compared against the internal secrets package."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3537")
  (item "bus-over-redis-streams" :group "ops"
   :title "Bus over Redis streams: post, read, pending, tail, list, reply"
   :text "People and friends talk to the coordinator over Redis streams with a consumer group per
    recipient, replacing the old note-directory bus."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3865")
  (item "swarm-wall-diagnostics" :group "friends"
   :title "Record request timings so a wall names the provider, sandbox or model"
   :text "The swarm result records per-request send, first-byte and done times so a wall ending says who
    stalled."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3785")
  (item "swarm-card-prompts-per-model" :group "measure"
   :title "One swarm card template per model family, measured by ok rate"
   :text "The card text, not the model, drives many failures. Each model family gets a tuned template,
    compared by ok rate on the same issues."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #3956")
  (item "tests-never-touch-repo-or-real-store" :group "tests"
   :title "Class guards: tests never write into the tree or reach the real store"
   :text "Class tests and a testutil guard stop tests from writing into the repository, reaching the real
    fleet store, or leaving serial-only environment edits."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #4137; issue #4193; issue #4194")
  (item "unit-and-functional-test-suites" :group "tests"
   :title "Two suites: mocked parallel unit tests and isolated functional programs"
   :text "Unit tests mock all services and run in parallel under a minute. Functional tests are a few
    isolated long-lived programs safe to run in parallel."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #4182")
  (item "ci-hygiene-and-single-ci" :group "ops"
   :title "CI hygiene and one CI of record"
   :text "Runners are cleaned before and after jobs, test processes are bounded, a slow CI is an event,
    and one CI system is chosen as the record."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #4183; issue #4184")
  (item "ci-jobs-fit-two-minute-cap" :group "ops"
   :title "Every CI job fits the two-minute cap"
   :text "Whole-tree, nightly, certification and release jobs are split into parallel matrix legs or
    functional programs, each under two minutes."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #4216; issue #4218; issue #4219; issue #4220")
  (item "redis-acl-single-source" :group "ops"
   :title "Redis ACL rows have one source rendered by the play"
   :text "The seat rules live once and the play renders them, so running the play never drops a seat."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #4199")
  (item "table-batch-unplaced-scan" :group "quality"
   :title "A batch create of unplaced table members scans every cell"
   :text "Creating or moving an unplaced member checks every cell, so hold time grows with table size.
    Index the placements so the check is constant time."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #4713")
  (item "ci-selection-covers-doc-readers" :group "ops"
   :title "CI package selection covers tests that read docs"
   :text "A docs-only change must run the Go tests that read those docs. Selection needs to know which
    tests depend on which doc files."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #4728")
  (item "secrets-age-remedy-per-platform" :group "ops"
   :title "The old-age remedy names the platform's own install path"
   :text "nova-secrets tells Linux users to use Homebrew. It should name the install path for each
    platform, pinned by a test."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5016")
  (item "tokens-prose-pass-defects" :group "quality"
   :title "Fix four defects the tokens prose pass left"
   :text "Fix the shrink rule in the day fold, a real-time wait in a test, names used as fixtures and a
    t.Setenv misuse."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5088")
  (item "config-row-notes-and-reasons" :group "ops"
   :title "Config rows carry a note and history carries a reason"
   :text "A route or machine row holds a note saying why it is set as it is, shown by list. The config
    history records the actor's reason."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #5101")
  (item "fleet-disk-guard-and-cache-caps" :group "ops"
   :title "Every machine has a disk guard that cleans and caps caches"
   :text "Cap failed-launch retention and every Go build cache by bytes. Clean leftover clones in the temp
    directory. Give each machine a guard whose loop finds its tools."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5116; issue #5119; issue #5120; issue #5123; issue #5198; issue #1139; PR #5244")
  (item "secrets-store-discovery-verbs" :group "ops"
   :title "Verbs list secrets stores and map a role to its secret"
   :text "No command prints where the stores are, and none maps a config role to its secret name. Add both
    so a cold holder need not guess."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #5152")
  (item "ci-ancestry-absent-promotion-ref" :group "ops"
   :title "An absent promotion branch must not excuse deletions"
   :text "When the remote promotion branch is absent, a retained local ref still excuses deletions.
    Invalidate it."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5161")
  (item "generality-scan-more-suffixes" :group "tests"
   :title "The generality text scan covers html, js and css"
   :text "The scan reads a fixed suffix list that misses shipped web files. Add them so a name in a
    stylesheet is found."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5165")
  (item "help-time-budget-flake" :group "tests"
   :title "The help-time budget is not a wall-clock unit test"
   :text "A 50 ms wall-clock limit on help answers fails under load. Move the timing out of the unit tier
    or count work instead."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5185")
  (item "bus-inbox-fetch-before-read" :group "ops"
   :title "nova-bus inbox fetches before it reads"
   :text "A stale clone reads as silence. The inbox must fetch first or say how old its view is."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5270")
  (item "contributing-stdlib-rule-mismatch" :group "docs"
   :title "The standard-library rule matches go.mod and is enforced"
   :text "The contributing guide says standard library only, while go.mod has direct third-party requires.
    Reword the rule to match, or add a check."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #5492")
  (item "check-links-ignore-quotes-and-code" :group "ops"
   :title "Link checks skip quotes and code and handle root-relative URLs"
   :text "nova-check links and nova-memory verify read link syntax inside quoted text, code spans and
    fences, and treat a root-relative URL as a missing file. Fix all six cases."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5493")
  (item "check-timestamps-and-commitments-verbs" :group "ops"
   :title "nova-check gains timestamps and commitments verbs"
   :text "One verb refuses typed or masked clocks in a staged diff or commit message. Another checks that
    no commitment moved between releases, so the script can leave the seed."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #5495; issue #5515")
  (item "guard-outbound-text-screen" :group "far"
   :title "nova-guard screens outbound text"
   :text "A tool reads a file and answers unproven-clean, flagged or could-not-verify, using patterns from
    the line's own config. It refuses without config."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #5496")
  (item "go-mod-tidy-nightly-jobs" :group "ops"
   :title "Nightly and certification jobs fail on an untidy go.mod"
   :text "Every job exits at the go.mod update check. Tidy the module file and add a check that keeps it
    tidy."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #5497")
  (item "version-snapshot-help-and-manifest" :group "ops"
   :title "nova-version snapshot appears in help and honours a manifest"
   :text "Help omits the snapshot verb, and the snapshot reports every copy on the path. Scope it to the
    adopted manifest and test help against dispatch."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #622")
  (item "decider-interface-and-classify" :group "far"
   :title "A decider interface with an untrusted frame and a generic classify verb"
   :text "One Decider interface with rules, local, remote and none deciders, a fixed untrusted frame, a
    tamper screen and a privacy class per provider. A generic classify verb stands on it."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1616")
  (item "hygiene-check-package" :group "quality"
   :title "One hygiene check package for identity, diff bounds, stray files and key shapes"
   :text "A single entry point checks commit identity, out-of-path changes, stray files and secret shapes.
    It never prints matched secret text, and nova-check exposes it."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1647")
  (item "sandbox-toolchain-flag-darwin" :group "ops"
   :title "nova-sandbox toolchain flag on darwin for cc, make and sqlite3"
   :text "A flag adds the narrowest measured roots for each toolchain leg inside the wall, so the C
    compiler and make work on darwin. The native default uses the go leg with the same roots given
    to the harness fence."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1662")
  (item "bench-leg-certification" :group "ops"
   :title "Certify a fleet machine leg by leg inside the wall"
   :text "A table names each toolchain leg with a probe. A certify verb runs every probe inside the
    sandbox, and the router refuses cards that need an uncertified leg."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1663")
  (item "sandbox-linux-closure" :group "ops"
   :title "Close the open nova-sandbox gaps on Linux before certifying walled legs"
   :text "A private writable tmp under the Landlock policy and the related open defects must close first.
    Until then a Linux machine certifies only legs that need no wall."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1664; issue #1495; issue #1737; issue #2162")
  (item "job-clone-identity-staging" :group "ops"
   :title "Job clones get their git identity from the pool and cannot link outside the job root"
   :text "The launcher writes local git identity and disables signing and hooks from the pool's identity
    table, and a pool without one is refused. No symlink may leave the job root."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #1665")
  (item "spec-sections-move-home" :group "docs"
   :title "Move each toolwork spec section into the spec of the tool that holds it"
   :text "As each section is implemented its rules move into that tool's own spec, and the index file
    stays as a pointer."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #1668")
  (item "bench-power-and-sleep-policy" :group "ops"
   :title "Fleet machines sleep when idle and stay awake and reachable while leased"
   :text "Idle machines sleep by default and wake on demand. While leased they hold power settings and a
    network keepalive, and status marks an unreachable machine down."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #1936; issue #2038")
  (item "harness-memory-per-card" :group "friends"
   :title "Cut the memory each card's harness process holds"
   :text "Each card runs a full harness process of several hundred megabytes, which sets every machine's
    width. Options to measure are a shared server per machine, heap limits and compressed swap."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2020")
  (item "adoption-ledger-generated" :group "ops"
   :title "A generated adoption ledger of tools and verbs with a stage and evidence per row"
   :text "The table of what is being adopted, with its stage and evidence, is generated from the pull
    requests instead of rebuilt by hand."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2068")
  (item "bus-wait-on-note-wake" :group "ops"
   :title "nova-bus wait --on-note wake, addressing and refusals"
   :text "Implement the wake on notes addressed by To:, with Cc: as opt-in, plus its empty-tick, rearm and
    flag refusal behaviour."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2177; issue #2178")
  (item "bus-receipt-verdict-and-send-name" :group "ops"
   :title "nova-bus receipt --verdict and a distinct send process name"
   :text "Add the receipt verdict verb that writes one receipt note and record in a single commit. Give
    send its own process name so killing a wait never kills a send."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2179; issue #2180")
  (item "token-report-postgres-parity-test" :group "tests"
   :title "Monthly token report equals the folded day files"
   :text "Test that the report over the database ledger equals the folded day files to the token for every
    type, day, model and repo."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2201")
  (item "toolwork-transcript-class-tests" :group "tests"
   :title "Every documented transcript is executed line for line"
   :text "One shared comparator runs each tool's documented transcript line for line, platform-specific
    sections name their CI leg, and the list of unexecuted examples only shrinks."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2217; issue #2218; issue #2007; issue #1455; issue #1549; issue #1722; issue #1875; issue
    #1890; cards moved out of the sprint (work record, 2026-10-04); issue #1652; issue #1653; issue
    #1654; issue #1657; card from the sprint store (2026-10-10); issue #1667; issue #3536")
  (item "fleet-iac-module-per-role" :group "ops"
   :title "Declare the fleet as infrastructure code, one module per role"
   :text "Describe each machine role as a module, use read-each-plan data sources as the drift witness,
    and make a new machine one apply with the standard script narrowed to a witness."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2225; issue #2229; issue #2230")
  (item "fleet-iac-no-secrets-in-state" :group "ops"
   :title "Forbid any secret in fleet infrastructure state or output"
   :text "Test that no secret value appears in state, outputs or written local files, and that a secret
    output is refused as a defect."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2231")
  (item "swarm-puller-rename-and-lease-tests" :group "tests"
   :title "Swarm puller: atomic card take, capacity line, and lease required"
   :text "Prove that two pullers cannot take one card, that job requests carry the capacity line and load
    gate, and that a pull without a bench slot lease is refused."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2233; issue #2238")
  (item "play-journal-view-and-memory-recall" :group "far"
   :title "Browse shared moments and recall original words with context"
   :text "Design a calm local view of existing journals and records, and a memory search that returns
    original words with who said them and later corrections, without rewriting the record."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #223; issue #224")
  (item "ci-runner-timeouts-under-load" :group "ops"
   :title "CI and cards share a machine without starving CI"
   :text "Cards run in the idle CPU class and yield to CI shards, runner capacity is reserved, and dealing
    goes by load per core."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2274; issue #4293; PR #5029")
  (item "redis-layer1-client-contract" :group "ops"
   :title "Redis Layer 1 client contract with file fallback and four named uses"
   :text "Deliver the internal client contract where every ephemeral use names a key, an owner and a file
    fallback, never makes Redis the authority, and is tested with kill-and-read round trips for
    wake, slots, budgets and plan state."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2276; issue #2277")
  (item "redis-spill-recall-owner-ttl" :group "ops"
   :title "nova-redis spill and recall with owner prefix and required TTL"
   :text "Add spill and recall for scratch values under an owner prefix and required TTL, refuse writes
    without them, and refuse recall of a missing or expired key."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2278; issue #2279")
  (item "redis-bench-store-verbs" :group "ops"
   :title "nova-redis: status, check, presence, local-only bind, auth from secrets"
   :text "nova-redis gains status, check, presence, version and help verbs with the exit-code contract its
    spec states. It binds only to localhost and the tailnet, takes auth from nova-secrets at run
    time and keeps persistence off."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2280; issue #2281; issue #2282")
  (item "adopt-digest-source-precedence" :group "ops"
   :title "Make adopt obey the stated precedence among digest sources"
   :text "The release spec says a typed --expect-sums wins over --expect-sums-from, which wins over
    --repo. Adopt follows that order and a test proves it."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #2284")
  (item "version-moved-verb" :group "ops"
   :title "Add nova-version moved, reading each build and refusing with one remedy"
   :text "A moved verb reads each commit's built binaries to report added and removed flags, never a
    hand-written list. It refuses with exit 2 and one remedy for a missing flag, an unresolved
    commit or a binary with no help."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2288; issue #2289")
  (item "update-apply-sha-build-mode" :group "ops"
   :title "Add nova-update apply --sha: build the whole set under one stamp"
   :text "Apply gains a build mode that builds every command from one commit under a single stamp and
    publishes the set atomically. It refuses a mixed or lost stamp."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #2290; issue #2291")
  (item "nocode-classifier-single-function" :group "quality"
   :title "Parameterise the nova-check nocode classifier into one shared function"
   :text "The audit and a future staged mode call the same classifier function instead of two copies."
   :date "2026-10-10"
   :release "v1.4"
   :origin "issue #2294")
  (item "swarm-remote-bench-deadline-and-idle" :group "friends"
   :title "Worker benches: remote deadline and idle kill, one bench line per bench"
   :text "Deadline and idle stay local, and a timeout sends a terminate to the ssh process group and then
    a remote kill by process group. An unreachable bench abstains with a reason and each bench gets
    one line in the batch packet."
   :date "2026-10-10"
   :release "v1.3"
   :origin "issue #607")
  (item "swarm-shared-clone-per-job" :group "measure"
   :title "Worker efficiency: avoid a full repository clone for every job"
   :text "Every job clones the repository for itself, which repeats the same work. Share a cached clone or
    a reference clone across jobs."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #80")
  (item "swarm-card-as-stateless-pipeline" :group "measure"
   :title "Cut turns per card: stateless model calls and capped reasoning on reads"
   :text "Card cost is context times harness turns, because every tool call resends the context. A card
    whose steps are named runs as a pipeline of stateless calls with the harness running the tools,
    and reads cap their reasoning."
   :date "2026-10-10"
   :release "after v1.4"
   :origin "issue #855; issue #856")
  (item "adopt-window-and-dry-run-fixes" :group "ops"
   :title "Adopt waits only for agents it stopped and dry-runs honestly"
   :text "The adopt window waits only for the agents the adopt itself stopped, and its dry run reports
    what each step would do instead of saying would for every step."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "friend-open-session-delivery" :group "friends"
   :title "Messages reach each harness's open session reliably"
   :text "Delivery into the open session of each harness is measured and reliable, and a message never
    waits behind every older one."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10); cards moved out of the sprint (work record,
    2026-10-04); card moved out of the sprint (work record, 2026-10-04)")
  (item "bench-run-confined-to-directory" :group "ops"
   :title "A bench run lives and dies inside its own directory"
   :text "Every run on a bench (gate, reader lane, friend lane copy) is confined to its own directory and
    leaves nothing behind."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "bench-slot-lease-model" :group "docs"
   :title "TLA+ model of bench slot leases"
   :text "Model the bench slot lease store in TLA+ so a grant never exceeds capacity, and check it with
    TLC."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "friend-daemons-adopted-and-cold-setup" :group "ops"
   :title "Friend daemons run on nova-tools alone, proven by a cold setup"
   :text "Retire hand-installed friend daemons and prove a cold setup end to end with a report."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10); cards moved out of the sprint (work record,
    2026-10-04); card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-daemon-lane-reads" :group "friends"
   :title "The friend daemon serves its reader row through one-shot lanes"
   :text "A friend daemon with one-shot lanes also beats a reader queue and runs asked reads as one-shots
    within the row's tiers and read width."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "runner-one-lane-engine-at-width" :group "friends"
   :title "One lane engine keeps a one-shot friend at width"
   :text "A one-shot friend has exactly one lane engine, installed, upgraded and retired by the adopt, and
    nova-runner keeps it at its width."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "friend-unit-test-coverage" :group "tests"
   :title "Unit tests for the friend state directory choice and watch cursor"
   :text "Uncovered functions in the friend state file get unit tests to raise package coverage."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card from the sprint store (2026-10-10)")
  (item "config-write-applies-itself" :group "ops"
   :title "A nova-config write reaches the fleet copy by itself within seconds"
   :text "Every config write is applied to the Redis copy automatically, without a manual apply step."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "nova-delete-one-literal-path" :group "ops"
   :title "nova-delete moves one literal path to quarantine and refuses anything else"
   :text "The delete verb accepts a single literal path, moves it to quarantine, and refuses globs and
    trees."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "single-nova-root-layout" :group "ops"
   :title "Every default path derives from one nova root"
   :text "Defaults for friend, bud and coordinator working directories derive from one root set per
    machine in nova-config."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10); card moved out of the sprint (work record, 2026-10-04)")
  (item "swarm-job-lease-model" :group "docs"
   :title "TLA+ model: one live run holds a swarm job lease"
   :text "A model checks that only one live run holds the job lease, with the launcher pid and per-run
    nonce."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "text-scanner-speed-and-host-digit-fixes" :group "ops"
   :title "Fix the text scanners: large input speed and host names with digits"
   :text "The self-talk scanner must scan a megabyte of installation text within its time bound. The
    generality tokenizer must catch a host name that ends in digits."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "split-large-tool-files-by-verb" :group "quality"
   :title "Split the large bus and fuse tool files into verb files"
   :text "The bus and fuse tools move each verb handler into its own file, checked step by step. Behaviour
    and tests stay the same."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card from the sprint store (2026-10-10); cards moved out of the sprint (work record, 2026-10-04)")
  (item "bus-trims-acknowledged-keepalives" :group "ops"
   :title "The bus trims acknowledged keepalive messages"
   :text "Keepalive messages that every reader has acknowledged are trimmed from the bus log, so the log
    stops growing without bound."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "core-module-tla-models" :group "docs"
   :title "TLA+ models for the core state packages"
   :text "Each package that owns state (bus, cairn, card tree, config, decision record, secrets, update,
    disk guard) gets a TLA+ model beside it, checked with TLC."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10); cards moved out of the sprint (work record, 2026-10-04)")
  (item "friend-lane-tla-models" :group "docs"
   :title "TLA+ models for friend lanes, delivery, presence and session checks"
   :text "Model the friend daemon's one-shot lanes, message delivery and acks, presence across batch
    turns, the session check file and the lane governor, and check them with TLC."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10); card moved out of the sprint (work record, 2026-10-04)")
  (item "friend-harness-version-pinned-under-the-wall" :group "friends"
   :title "Pin the friend harness version, including under the lane wall"
   :text "A friend's harness runs at the pinned version in the daemon as well as in the lanes, so an
    unpinned upgrade cannot break the lane wall. The wall is tried against a new harness before it
    is adopted."
   :date "2026-10-10"
   :release "v1.3"
   :origin "card from the sprint store (2026-10-10)")
  (item "tlacheck-unit-coverage" :group "tests"
   :title "Unit tests for the TLA check bench, transport lines and fetch"
   :text "Raise the unit coverage of the TLA check tool by testing its bench runner, transport lines and
    fetch paths."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card from the sprint store (2026-10-10)")
  (item "tools-adopt-shared-toolkit-packages" :group "quality"
   :title "Move each tool onto the shared toolkit packages"
   :text "Each tool (bus, check, dev, fuse, release, secrets, update, version) replaces its private
    helpers with the shared toolkit packages, one step at a time, with a check per step."
   :date "2026-10-10"
   :release "v1.4"
   :origin "card from the sprint store (2026-10-10); cards moved out of the sprint (work record,
    2026-10-04); card moved out of the sprint (work record, 2026-10-04)")
  (item "release-sums-origin-and-tag-check" :group "ops"
   :title "The release checks where its sums came from and its tag"
   :text "The sprint release sums come from the same directory as the binaries, which proves integrity but
    not origin, and the tag is not compared with --version or a pin."
   :date "2026-10-10"
   :release "v1.3"
   :origin "nova-tools PR #5571 cold read")
  (item "deadcode-covers-library-packages" :group "quality"
   :title "The dead-code check reaches library packages again"
   :text "TestDeadCode excludes pkg/ since the split; a reachability walk with library roots catches dead
    unexported code there, documented in SPEC-CI with a reversed witness."
   :date "2026-10-10"
   :release "v1.3"
   :origin "seat decision after the repository split")))
