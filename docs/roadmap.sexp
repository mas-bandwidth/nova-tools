; The nova-tools roadmap: the data ROADMAP.md is generated from.
; Read by internal/roadmap through internal/worklang (data, never evaluated).
; Edit this file, then run `make roadmap`; never edit ROADMAP.md by hand.
; The shape is in internal/roadmap's package comment.
(roadmap "v1"
 :title "nova-tools roadmap"
 :text "This is the work planned after v1.4. The release ladder is fixed (the owner, 2026-10-09): v1.2.1 is
  small fixes; v1.3 is larger fixes, and fixes only; v1.4 is v1.3 plus unit tests, cleanup, dead code,
  adoption of the standard library and modules, and test refactors. Then the line stops, and anything
  outside the ladder is recorded here instead of being worked. nova-sprint's items moved to
  nova-sprint's own roadmap (mas-bandwidth/nova-sprint, ROADMAP.md) at v1.2.3, with its docs and models. Each
  item says what it is and why it waits. Where the code already has part of an item, the item says
  what exists and covers only the gap."

 :scheduled
 ((scheduled "nova-swarm-becomes-nova-worker" :release "v1.3"
   :title "Rename nova-swarm to nova-worker"
   :text "The tool runs one-task AI workers, and its help already says so. The rename covers the binary
    and its cmd directory, help text, docs, the fleet's ansible plays and launchd units (in their own repository), seat
    names such as swarm-hetzner2, and open cards. A nova-swarm shim keeps working for one release.
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
   :date "2026-10-09"))

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
  (group "sprint"
   :title "The sprint machine"
   :text "New verbs, stages and policies for nova-sprint. Each is new capability, so none of it fits the
    fixes-only ladder.")
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
   :text "Directions, not plans. Nothing in v1.x builds toward these yet."))

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
  (item "policy-numbers-as-settings" :group "sprint" :area "nova-config"
   :title "Policy numbers and tool timings as settings"
   :text "Every policy number and tool timing (bounds, waits, widths, deadlines) becomes a named setting
    with its default in one place, instead of a constant in the code."
   :why "New capability."
   :cards 2
   :date "2026-10-09")
  (item "jev-decision-evaluation" :group "sprint" :area "nova-decide"
   :title "Evaluating Jev's decisions"
   :text "An evaluation harness that scores Jev's past decisions against their outcomes, a detector for
    reads that bounced good work, a classifier for why a card is held, and the promotion of a decision
    from shadow to acting once it measures well."
   :why "New capability."
   :cards 4
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
  (item "repository-split" :group "docs" :area "repo"
   :title "Split nova-sprint out of nova-tools"
   :text "nova-sprint's code moves to its own repository: both READMEs say which is which, and nova-tools
    drops cmd/nova-sprint and the sprint packages. Today the code lives here, on dev. The
    mas-bandwidth/nova-sprint repository is a copy seeded from nova-tools, pinned to nova-tools v1.1.0,
    last pushed 2026-10-08."
   :why "An architecture change."
   :cards 2
   :date "2026-10-09")

  ; Far
  (item "self-organizing-workers" :group "far" :area "design"
   :title "Thousands of self-organizing workers"
   :text "A system in which thousands of workers organize themselves: they find work, split it, and
    check each other, with no central dealer. The v1.3 rename retires the nova-swarm name, so no tool
    holds the word swarm when this is designed."
   :why "A direction, not a plan."
   :date "2026-10-09")
 ))
