; The nova-tools point releases: the data FIXES.md is generated from.
; Read by internal/roadmap through internal/roadmap/sexp (data, never evaluated).
; Edit this file, then run `make roadmap`; never edit FIXES.md by hand.
; The shape is in internal/roadmap/fixes.go.
(fixes "v1"
 :title "nova-tools fixes"
 :text "The point releases after v1.2.2: what each one shipped, is shipping or will ship. A point release
  carries fixes only; new work is in ROADMAP.md. nova-sprint's point releases are in that repository's
  FIXES.md. The point releases after v1.2.10 (the owner, 2026-10-11): v1.2.11 small fixes, v1.2.12 tests and
  models only, v1.2.13 contraction, v1.2.14 the next small fixes, each release's scope below. Point
  releases continue until only architectural items are open; those are v1.3's re-architecture, and
  ROADMAP.md says how the two fit. An area stops taking point fixes when a fix there is the same problem
  in another form, or would be its third fix: from then on it is architectural, and its v1.3 item names
  the point fixes it replaces. Friend work is dogfooded within the week once more friends are online:
  the current point release adopted, friends alone first, then friends and fleet together, and each
  friend bug found becomes a point-release card."
 :releases
 ((release "v1.2.3" :status "shipped" :date "2026-10-10"
   :text "The split: nova-sprint, nova-card and nova-work, with their models and docs, leave this
    repository for mas-bandwidth/nova-sprint.")
  (release "v1.2.4" :status "shipped" :date "2026-10-10"
   :text "One fix: the sprint store's function library is back at its v1.2.2 digest, so an adopt can load it.")
  (release "v1.2.5" :status "in-progress"
   :text "The roadmap and this page come back as data, and the open issues move into the roadmap.")
   (release "v1.2.6" :status "planned"
    :text "The fixes held during the split, the point-release candidates found on 2026-10-10, the owed
     tests, and the sprint store cards that are point-release work. Each is re-applied or written in
     this repository, then cut as one release.")
   (release "v1.2.8" :status "planned"
    :text "The fleet-install fixes found while dogfooding v1.2.5 and later.")
   (release "v1.2.9" :status "planned"
    :text "Worker, adopt and CI fixes: idle-reaped cards report their real cause, a HOLD with zero findings
     is malformed, the dispatcher releases a slot lease by its own id, a worker card whose last turn is a
     question ends as asked, the adopt trusts no partial release directory, waits only for agents it
     stopped and dry-runs honestly, CI runs are not cancelled under load and a cancelled run counts as
     red, and the doctor checks the running executable.")
   (release "v1.2.10" :status "shipped" :date "2026-10-11"
    :text "Cut from dev between sprint releases, paired with nova-sprint v1.2.10: the limited inception route behind the global rate budget, a draining member that says so on its beat, nova-friend acceptance --dry-run, and the queue and certification fixes.")
   (release "v1.2.11" :status "planned" :date "2026-10-11"
    :text "Small bug fixes, one concern each, and no model change. They run as cards on v1.2.10 once it is
     adopted, so the sprint dogfoods the release it just cut.")
   (release "v1.2.12" :status "planned" :date "2026-10-11"
    :text "Tests and models only, no product change. More unit and functional tests, and every TLA+ model
     checked against the code it claims to model (its state, actions and invariants) and run again with
     TLC on a bench; owed models and stale or timed-out records are made real. The new tests are written
     against behaviour (the verbs, the store's steps, the wire, the TLA+ invariants), not internals, so
     the same tests run unchanged against v1.3 and say whether it keeps the behaviour. TLC runs where the
     cards run first. A real defect a new test finds is written as a v1.2.14 card, never fixed inside a
     test card.")
   (release "v1.2.13" :status "planned" :date "2026-10-11"
    :text "Contraction: fewer lines, no behaviour change, with v1.2.12's tests and checked models as the net
     under it. Passes in this order, one writer per package: remove dead and stale code; adopt the Go
     standard library or established packages where they replace hand-rolled code; run go fix (and gopls
     modernize) over all Go code; pull code the tools share into modules, then code the modules share
     into modules; make each test minimal, on test frameworks and shared helper modules. The module pass also covers
     the copies across the two repositories: 59 same-named packages exist as separate copies in
     nova-sprint and nova-tools (about 71,500 shared lines, measured 2026-10-11 at nova-sprint 15c779c
     and nova-tools 2a648d8). Each gets one owner, nova-sprint as with nova-roadmap, and the other
     repository imports it: the identical copies first, then the diverged ones (member, fleet, cardtree,
     tool, and parts of friend and swarm), reconciled under v1.2.12's tests. The first named entries are
     the nova-roadmap extraction and the brief boilerplate moving into the card contract. Measured as
     lines per repository and package, product against test, from a baseline taken before the first
     pass.")
   (release "v1.2.14" :status "planned" :date "2026-10-11"
    :text "The next small fixes, one concern each, including the defects v1.2.12's tests find.")
   (release "v1.2.15" :status "planned"
    :text "The next small product fixes, one concern each."))
 :items
 ((fix "split-sprint-tools-leave" :release "v1.2.3" :status "shipped"
   :title "The sprint tools leave nova-tools"
   :text "Their commands, packages, models and docs move to mas-bandwidth/nova-sprint, and CI refuses their paths here."
   :origin "the split, layers L5 to L7; PR #5591")
  (fix "lua-digest-restore" :release "v1.2.4" :status "shipped"
   :title "Six Lua comment lines restored"
   :text "The function library's digest is its v1.2.2 value again, so the adopt's shadow tick accepts it."
   :origin "PR #5592, head 7d1dc0c06")
  (fix "roadmap-and-fixes-as-data" :release "v1.2.5" :status "shipped"
   :title "ROADMAP.md and FIXES.md generated from s-expression data"
   :text "docs/roadmap.sexp and docs/fixes.sexp are the data; `make roadmap` writes both pages, and a test
    fails when a page differs from what its data renders."
   :origin "PR #5533")
  (fix "card-tells-child-its-wall" :release "v1.2.6" :status "shipped"
   :title "The card tells the child its wall: deadline and temporary directory"
   :text "The worker card tells the child its deadline, and the native wall passes the slot temporary
    directory so TMPDIR is the slot one."
   :origin "nova-tools PR #5575, head 4725d7d7d; landed in dev as 7dc8304560 via PR #5594")
  (fix "a-reader-writes-scratch-inside-its-job" :release "v1.2.6" :status "shipped"
   :title "A reader writes its scratch inside its job"
   :text "A reader's child is given a writable scratch directory inside its job: TMPDIR, TMP and TEMP
    name $JOB/tmp, made before the child starts and removed with the job, and the read brief says so.
    A write the wall refuses outside the job ends the read in the reader's own environment, not as a
    missing verdict, and the read is run once more on the same reader instead of spending it and
    sending the card to cannot ask."
   :origin "measured 2026-10-11 by the seat's inbox pass: reads returned no verdict because the reader wrote under /tmp and the card wall refused it")
  (fix "walled-lane-tokens-read" :release "v1.2.6" :status "shipped"
   :title "A walled lane tokens are read where its harness writes them"
   :text "Walled sessions live under the wall home, tokens are read from there first, and a session in no
    database is unread rather than zero, so the token cap can trip."
   :origin "nova-tools PR #5590, head 0c4fd7ea; landed in dev as 1babe4a43f via PR #5595")
  (fix "hosted-tests-green-on-main" :release "v1.2.6" :status "planned"
   :title "The hosted tests are green on main and nightly"
   :text "Two hosted tests are red on main and nightly (the fork bomb wall cap and jobs as worktrees of
    one mirror); make them pass there."
   :origin "v1.2.5 candidate list")
  (fix "friend-provider-key-after-move" :release "v1.2.6" :status "shipped"
   :title "A friend AI moved to another machine uses the provider key that worked"
   :text "After a move the friend AI ran with a stored provider key that differs from the working one, and
    the provider refused it. The stored key is replaced by the owner, then the friend AI restarts
    through the play."
   :origin "v1.2.5 candidate list")
  (fix "release-build-refuses-without-sprint-release" :release "v1.2.6" :status "shipped"
   :title "release build refuses to ship without the sprint release"
   :text "nova-update release build without --sprint-release ships no sprint tools and succeeds; it
    refuses unless an explicit opt-out flag is given."
   :origin "nova-tools PR #5571 cold read")
  (fix "changelog-lists-merge-queue-merges" :release "v1.2.6" :status "shipped"
   :title "The release changelog lists merge-queue merges"
   :text "The cut changelog says no pull request merged since the previous tag although the merge queue
    merged many; the listing includes merge-queue merges."
   :origin "v1.2.4 cut note")
  (fix "worker-writes-result-before-deadline" :release "v1.2.14" :status "planned"
   :title "A child ended at its deadline with a commit and no result gets a report"
   :text "The card already tells the child to finish at 80 percent of its wall; for a child that works on
    to the deadline anyway, the run now writes a RESULT.md naming the branch, the commit count and the
    head when the deadline ended it with commits in the repository and no report of its own.
    The report has no findings head, so it is never scored as the child's own work."
   :origin "fault 9, 2026-10-10")
  (fix "push-gate-names-the-staged-commit-it-wants" :release "v1.2.14" :status "in-progress"
   :title "The push gate names the staged commit it wants"
   :text "A refusal that the result's head does not descend from the staged commit now tells three cases
    apart: the staged commit held by no branch or HEAD of the checkout (the branch was cut again or
    reset), a head that parted from a staged commit the checkout does hold (naming their merge base,
    \"none\" for unrelated histories), and git failing in the push repository. It no longer quotes git's
    own \"Not a valid commit name\", and the first two say to put the commits on top of the staged
    commit and finish again."
   :origin "seat inbox pass 2026-10-11 ~03:38Z, epoch 16: 32 push refusals in an hour read \"the result's head X does not descend from the staged commit S: fatal: Not a valid commit name S\"; re-scoped against nova-tools dev 2314d43 on 2026-10-11")
   (fix "swarm-lint-reads-quoted-code-as-quoted" :release "v1.2.14" :status "planned"
    :title "The card lint reads a Go module arrow as an arrow, and no fenced scratch"
    :text "no-parent-path reads a `>` as a shell redirect only where it is not the second half of
     `=>` or `->`, so the `> ` of a pasted Go module `replace tblfu1 => ../fu1` line is an arrow,
     not a destination the worker writes to. scratch-absolute reads no text inside a fenced
     block, for the word and for the line it names, so a bare variable named `scratch` in quoted
     code is no longer refused as a relative scratch path (issues #1994, #2302, #3470)."
    :origin "the point-release plan (tmp/point-release-plan-v1.2.11-v1.2.12.md), 2026-10-11: swarm-lint false refusals; code read at nova-tools dev 2314d43")
   (fix "nova-friend-raises-a-refused-verb-and-proves-its-harness-first" :release "v1.2.14" :status "planned"
    :title "A refused verb is raised, and a lane proves its harness before it takes"
    :text "A sprint server that refuses a friend verb (friend cards, friend beat) is counted per verb; the
     third consecutive refusal raises it once — a down beat naming the reason and one word to the
     coordinator — never only logs, and a server that answers again clears it. A claude lane proves
     its harness first (claude -p ok --output-format json --max-turns 1 with the row's config_dir as
     CLAUDE_CONFIG_DIR), re-proved after two instant failures, before any take; a
     harness that cannot answer takes no card, beats down with the reason, and re-proves every minute."
    :origin "nova-friend-raises-a-refused-verb-and-proves-its-harness-first.w6, 2026-10-11: the coordinator's friend daemon ran 11 hours refused \"You must hook in first\", and a lane's unsigned --config-dir burned ~35 card attempts in 6 minutes")
  (fix "adopt-restarts-what-it-replaced" :release "v1.2.6" :status "shipped"
   :title "Members are restarted and checked after adoption"
   :text "After installing, the play restarts every member, reader and friend daemon whose binary it
    replaced, fails each running process that does not report the installed version, prints a
    refusal's cause whole, and ends with one table: host, version running, members and readers up,
    harness present, disk free."
   :origin "bug 13, fleet/tools.yml")
  (fix "draining-member-word-is-this-ticks" :release "v1.2.6" :status "shipped"
   :title "A draining member's beat carries this tick's no-room word"
   :text "The member asks Room every tick before the drain return, so a member that drained under its
    disk floor stops saying no room once the disk is freed (nova-sprint tla/NoRoom.tla,
    WordIsThisTicks)."
   :origin "v1.2.5 candidate 5; the PR #5569 follow-up")
  (fix "friend-lane-takes-before-it-runs" :release "v1.2.6" :status "in-progress"
   :title "A friend's lane runs only a card the server has taken for her"
   :text "A free lane takes its card by id and starts nothing until the take is answered OK; a refused
    take (the machine STOPPED) is asked again on a later pass, a batch daemon takes her ready cards, and
    a card set aside on a refused take or finish is taken again (tla/FriendLaneTake.tla)."
   :origin "a friend host, 2026-10-10 20:14Z: a one-shot friend's 23 lanes ran ready cards and set them aside")
  (fix "adopt-removes-friend-units-that-moved" :release "v1.2.6" :status "shipped"
   :title "The adopt removes the unit of a friend who moved hosts"
   :text "The friends step of the tools play splits the stale friend daemons live lists by what launchd
    holds: it reinstalls the ones the host runs (the friends table's place for them) and removes the
    unit of a friend who no longer lives there (installed, not loaded) with nova-friend uninstall."
   :origin "dogfood 2026-10-10: stale friend unit on the seat host")
  (fix "friend-take-names-its-epoch" :release "v1.2.6" :status "in-progress"
   :title "A friend's batch take names the epoch of her ready cards"
   :text "The server refuses every worker verb that names no epoch; the batch take sent none, so a
    batch friend's ready cards were never taken. The take now names the newest epoch her row holds, and
    a lane's take names the epoch her row says for the card."
   :origin "a friend host, 2026-10-10 20:53Z: take REFUSED, a worker's verb names the epoch")
  (fix "friend-lane-wall-denies-the-seats-self" :release "v1.2.6" :status "in-progress"
   :title "A friend's lane wall always has a coordinator self to deny"
   :text "nova-friend install takes --deny-self and writes it into the agent, the seat play passes
    nova_friend_deny_self to a reinstall whose plist records none, and with no flag the daemon denies
    the seat's working root, so no lane exits 125 with bad_profile for a missing deny list."
   :origin "a one-shot friend host, 2026-10-10: every lane exit 125, the daemon ran with no --deny-self")
   (fix "fleet-loops-refuses-version-mismatch" :release "v1.2.14" :status "planned"
    :title "A fleet loops play refuses when its checkout version differs from the adopted release"
    :text "Fleet plays run only from the checkout of the adopted release, and refuse when the
     checkout's version differs from the release installed on the host."
    :origin "dogfood 2026-10-10 evening: fleet-only restart")
   (fix "a-lane-beyond-the-cap-keeps-the-card-the-server-gave-it" :release "v1.2.14" :status "planned"
    :title "A lane beyond the cap keeps the card the server gave it"
    :text "With server lanes a lane past the live cap a rate limit lowered keeps the card it holds
     between turns, counting its turns, and says so once; it no longer logs a hand-back to another
     lane that never reaches the server, resets the card's turns, or leaves a machine STOP with no
     stop-return for the card. A world with no server keeps the local hand-back."
    :origin "card a-lane-beyond-the-cap-keeps-the-card-the-server-gave-it.w1; the coordinator's 2026-10-11 audit of pkg/friend v1.2.14")
   (fix "a-failed-acted-receipt-is-written-again" :release "v1.2.14" :status "planned"
    :title "An acted receipt a failed stamp owes is written again"
    :text "When a turn ends at exit 0 the daemon stamps its messages acted, then acks them. A stamp the
     store refused left the receipt at read or delivered, so a daemon restarted before its ack landed
     pushed the redelivery into a second turn. The daemon now keeps the ids whose acted receipt a failed
     stamp owes, at most ActedKept, oldest dropped first, and writes them acted again in one stamp
     before its next read once the store answers; a repeat is harmless, a receipt moving only forward.
     A failed read stamp is not owed: only acted keeps a message from a second turn. A restart while
     the store stays down still redelivers, at least once."
    :origin "card a-failed-acted-receipt-is-written-again.w2; the coordinator's 2026-10-11 audit of nova-tools pkg/friend v1.2.14 (possible double delivery on the bus after a failed stamp, pkg/friend/daemon.go near 1126 and near 1503)")
   (fix "secrets-remedy-names-the-platforms-install" :release "v1.2.14" :status "planned"
    :title "The secrets tool remedies name the platform's install, not always Homebrew"
    :text "CheckSopsVersion and CheckAgeKeygenVersion hard-coded brew in all four of their remedies,
     so a Linux reader short of sops or age-keygen was told to run a command the platform does not
     have (nova-tools issue #5016). One helper builds each remedy from the tool, absent-or-old, and
     the platform, copying internal/up/layout.go's tools table: brew on darwin, go install for sops
     elsewhere, apt for age-keygen on linux."
    :origin "issue #5016; the point-release plan (tmp/point-release-plan-v1.2.11-v1.2.12.md), 2026-10-11")
   (fix "friend-lanes-are-thin-clients" :release "v1.2.6" :status "in-progress"
    :title "A friend's lanes are thin clients of the sprint server"
    :text "Each lane asks the server what it holds (take --lane), runs exactly that, and reports by the
     ids, epoch and lane the server gave; a refusal drops the job and the lane asks again. lanes.json
     keeps sessions and owed stop-returns only, never what a lane holds, and reads go through the
     same lane loop. The model is tla/FriendLane.tla."
    :origin "a one-shot friend host, 2026-10-10: took logged while the server showed the card ready, and progress sent at epoch 0")
   (fix "friend-wall-reads-its-pinned-harness" :release "v1.2.6" :status "shipped"
   :title "A friend's lane wall reads the pinned harness it runs"
   :text "nova-friend install takes --wall-reads and writes it into the agent, and the seat play passes
    nova_friend_wall_reads to a reinstall whose plist records none, so a pinned harness whose shim
    execs a directory under the home no longer exits 126 (Operation not permitted) for every lane."
   :origin "a one-shot friend host, 2026-10-10: every lane exit 126 after the harness pin; the wall denied reading the pinned harness directory")
  (fix "friends-snap-to-the-new-epoch" :release "v1.2.6" :status "shipped"
   :title "A friend's new epoch retires her older-epoch items and starts empty"
   :text "On the sprint's new epoch the daemon moves every older-epoch inbox and outbox item to the
    retired folder named for its epoch, kept, never deleted, and the live folders start the new epoch
    empty; on the same signal the lanes forget their sessions and the present is owed, so a full or
    stale session never carries across a clear."
   :origin "the seat ledger v1.2.4-held-2026-10-10.md friend snap, 2026-10-10: 323 inbox items and 535 outbox REPORTs of epoch 15, each refused (not on her row)")
  (fix "a-friend-session-full-or-missing-starts-fresh" :release "v1.2.6" :status "shipped"
   :title "A friend's lane session full or missing starts a fresh one itself"
   :text "A provider's context-limit refusal on a lane's session, or a session the harness no longer
    has, no longer counts the card's turns against it and waits for a hand session plus a restart: the
    lane starts a fresh session itself, seeded from the friend's own files, its card continues in it
    (once per card; after that the refusal is the card's business again), and the seat is told once
    per renewal."
   :origin "the owner 2026-10-10: what else is manual that should be automatic from the machine")
  (fix "worker-brief-never-rewrites-history" :release "v1.2.6" :status "planned"
   :title "The worker brief says never amend, rebase or reset onto origin"
   :text "The child rule no-rewrite-history in fleet/child-rules.txt, byte for byte the copy nova-sprint embeds
    (its add refuses a held rules file that differs), stops children rewriting history, which caused the
    does-not-descend refusals. A test pins the file's digest in both repositories."
   :origin "v1.2.5 candidate list")
  (fix "friend-width-fits-slots" :release "v1.2.6" :status "shipped"
   :title "A friend's width fits within her slots"
   :text "nova-config refuses a friend width larger than her slots and names both values, so one-shot
    lanes cannot silently halve the friend's available capacity."
   :origin "dogfood 2026-10-10: one-shot friend at width 32 with 16 slots")
  (fix "no-friend-runs-in-batch-mode" :release "v1.2.6" :status "shipped"
   :title "No friend runs in batch mode"
   :text "A friend's delivery mode is one-shot always: nova-config refuses --mode batch, a stored batch row
    reads as one-shot (config.FriendMode, the reader nova-sprint's friend sync writes onto her roster
    row), and her daemon runs one card per lane."
   :origin "dogfood 2026-10-10: a friend row still mode=batch")
  (fix "one-shot-friend-presence-needs-no-session" :release "v1.2.6" :status "shipped"
   :title "A one-shot friend's presence needs no session"
   :text "A one-shot opencode friend has no long-lived session by design: her presence comes from the
    daemon and its lanes, never a growing session. Her session check goes in as a fresh turn of its
    own (opencode run with no --session), so a session that is full (past the model's context) or
    missing is never the reason she is down."
   :origin "the seat ledger v1.2.4-held-2026-10-10.md, two friends down after the adopt: one past the 260000-token context refused every turn, one with no opencode session in its directory")
  (fix "a-reader-loop-redials-after-a-server-restart" :release "v1.2.6" :status "shipped"
   :title "A reader loop re-dials the sprint server after its restart"
   :text "The sprint connection a reader beats and takes through is rebuilt after any error and backed
    off one second doubling to thirty (cmd/nova-swarm/sprintredial.go, tla/LoopRedial.tla), so a
    server restart drops the connection and the reader re-dials and keeps beating, instead of parking
    on the dead socket; fleet/loops.yml takes nova_loop_restart=<records> to restart a hung loop
    through the play, never by hand."
   :origin "measured 2026-10-11 01:05-01:20Z: after the v1.2.6 adopt every Linux reader loop stopped at its last connection-refused line while the member loops on the same hosts recovered, 24 reads waited with 0 reading and the server raised cannot ask (0 free readers) until each reader unit was restarted by hand")
  (fix "worker-question-ending-asked" :release "v1.2.9" :status "shipped"
   :title "End a card as asked when its last turn is a question"
   :text "A headless card whose last turn asks the user a question ends at once as asked instead of
     holding its slot to the deadline."
   :origin "issue #2548")
  (fix "fleet-built-from-release-not-source" :release "v1.2.8" :status "shipped"
   :title "Every installed tool is checked against the release's bytes"
   :text "After the install and the friends' reinstall, tools.yml lists each machine's bin directory with
    the sha256 of every file and compares it with the platform's SHA256SUMS; a tool built or copied on
    the machine, which prints the release's version and holds another toolchain's bytes, refuses the
    play (TOOLS REFUSED step=identity) and is named. --check says WOULD-REPLACE and refuses nothing.
    fleet/tools_test.go pins the check and that no task compiles a tool."
   :origin "dogfood 2026-10-10: after the v1.2.5 adopt one machine's nova-friend reported go1.27.1 while every other tool reported go1.26.6")
  (fix "seat-friend-acceptance-run" :release "v1.2.8" :status "in-progress"
   :title "The friend acceptance run runs a lane under the real wall"
   :text "Each friend placed on the host has an acceptance task: nova-friend acceptance runs the friend's own
    lane program inside the very wall the daemon's lane children run inside, built by the one function run
    and acceptance share from the plist's recorded flags, writes RESULT.md, and exits with the wall's real
    code (125 when the profile will not build, 126 when the program cannot execute). A separate assert
    fails the host on a non-zero exit with the friend's name, the exit code and the output line. No model
    is called and no card is taken."
   :origin "dogfood 2026-10-10: a friend exit 126 after an adopt reported done")
  (fix "adopt-window-and-dry-run-fixes" :release "v1.2.9" :status "shipped"
   :title "Adopt waits only for agents it stopped and dry-runs honestly"
   :text "The adopt window waits only for the agents the adopt itself stopped, and its dry run reports
    what each step would do instead of saying would for every step."
   :origin "card from the sprint store (2026-10-10)")
  (fix "swarm-idle-reap-verdict" :release "v1.2.9" :status "shipped"
   :title "Idle-reaped cards report their real cause and are not wrongly reaped"
   :text "A reaped card reports a wall refusal that names nothing, and the harness shell cap silenced
    working cards. The verdict states idle reaping and the idle watch accounts for the shell cap."
   :origin "issue #2157; issue #2158; issue #2577; issue #2579; issue #2585")
  (fix "swarm-hold-zero-findings-malformed" :release "v1.2.9" :status "shipped"
   :title "A HOLD report with zero findings is malformed"
   :text "A verdict of HOLD cannot have no findings, so the result check classes it as malformed."
   :origin "PR #4635")
  (fix "update-adopt-partial-release-dir" :release "v1.2.9" :status "shipped"
   :title "Release adopt does not trust a partial release directory"
   :text "A killed transfer leaves a partial release dir that the next adopt treats as complete. Adopt
    verifies completeness or uses a temporary dir renamed at the end."
   :origin "issue #1981")
  (fix "swarm-slot-lease-release-by-id" :release "v1.2.9" :status "shipped"
   :title "The dispatcher releases a slot lease by its own id"
   :text "Release removes every lease with the same owner and label. It releases only the one lease it took."
   :origin "issue #1582")
  (fix "ci-cancelled-under-load" :release "v1.2.9" :status "shipped"
   :title "CI runs on card PRs are not cancelled under load"
   :text "CI runs at the head of card PRs are cancelled mid-job when many PRs open together; the
    concurrency rule is fixed so each head gets one full run."
   :origin "issue #2795")
  (fix "update-revert-on-red-cancelled-run" :release "v1.2.9" :status "shipped"
   :title "A cancelled workflow run counts as red for revert-on-red"
   :text "Revert-on-red treats cancelled runs as failures so a cold-cache cancellation does not leave a
    bad main."
   :origin "PR #4600")
  (fix "friend-follows-adopted-build" :release "v1.2.6" :status "in-progress"
   :title "Friend daemons follow the adopted release automatically"
   :text "When the adopted build for a line changes, each friend host installs it through the play and proves its lanes, with no hand adopt per host."
   :origin "the owner 2026-10-10: what else is manual that should be automatic from the machine")
  (fix "swarm-doctor-checks-running-executable" :release "v1.2.9" :status "shipped"
   :title "nova-swarm doctor checks the running executable, not only PATH"
   :text "The launch preflight refuses a new binary found on PATH but passes the same binary by absolute
    path. It checks the executable that is actually running."
   :origin "issue #3469")
  (fix "inception-behind-the-limiter-proxy" :release "v1.2.10" :status "shipped"
   :title "A limited inception route takes the global rate budget on every request"
   :text "A fleet member launches with --model only, so the job's generated opencode config named no
    baseURL for inception and the budget proxy never opened: a limited route's requests took no
    per-request budget. When a budget server is named, inception's entry is now given its API base
    from the providers table (https://api.inceptionlabs.ai/v1), the proxy opens in front of it and
    the harness is pointed at the proxy; the harness's own header and request timeouts are turned off
    on that route so a request waiting on its budget stalls and is never aborted. A route with no limit
    is unchanged; deepseek is not seeded."
   :origin "the card tmp/next-sprint-cards/inception-behind-the-limiter-proxy.md (the seat, 2026-10-11 ~02:55Z: the first Mercury 3 take on a limited route logged that inception had no http baseURL and the rpm bucket stayed at in_use=0)")

  (fix "v1-2-12-inventory" :release "v1.2.12" :status "planned"
   :title "Inventory the tests and models before v1.2.12's first test card"
   :text "Coverage per package, the functional tier's state, each TLA+ model against the code it claims
    to model, and each RUNS.tsv record's age and result. The tests and model cards of v1.2.12 are cut
    from it."
   :origin "the owner, 2026-10-11: the v1.2.12 scope (inventory first)")

  (fix "tla-friendenvelope-gives-up-and-acks" :release "v1.2.12" :status "planned"
   :title "FriendEnvelope models the give-up, the ack that can fail and the present's MaxBatch read cap"
   :text "The module's header named internal/friend/daemon.go (it is pkg/friend/daemon.go) and claimed the
    MaxBatch read cap was removed, and its Accept was atomic. It now gives up a failed message once it
    reaches MaxDeliveries (a GiveUp action, daemon.go settle), restates AckOnlyAccepted as only an
    accepted or given-up turn acks, keeps the MaxBatch read cap while a present is owed, splits Accept
    into the exit and the ack with the ack able to fail, and corrects the header and the
    MCFriendEnvelopeFailureReorders name. A new reversed witness giveupearly gives up before
    MaxDeliveries and violates GivenUpOnlyAtTheBound. Tests and models only, no product change."
   :origin "the card tla-friendenvelope-gives-up-and-acks.w1")

  (fix "cover-friend-read-lane-step" :release "v1.2.12" :status "planned"
   :title "Cover the read lane's step with unit tests"
   :text "readLaneStep (pkg/friend/read_lanes.go:444) had no unit test: 0 of 46 statements run under
    -cover, and every existing read test set Daemon.Sprint but not Daemon.Take, so all took the
    legacy path. A new pkg/friend/read_lane_step_test.go drives a Daemon with Take set (serverLanes)
    through the fake-clock rig under synctest: a read lane takes, runs exactly the read the server
    names, reports its verdict at the server's epoch and lane; refuses, idle holds, a held read with
    no packet yet, a read another lane runs, the read slots, and STOPPED are covered. 100.0% of
    readLaneStep's statements now run. Tests and models only, no product change."
   :origin "the card cover-friend-read-lane-step.w1 (the coordinator, 2026-10-11: tmp/v1.2.12-inventory-2026-10-11.md section 8 item C8)")

  (fix "cover-member-push-with-a-scripted-git" :release "v1.2.12" :status "planned"
   :title "The member's push pinned in the unit tier with a scripted git"
   :text "cmd/nova-swarm/memberpush_behaviour_test.go drives (g *gitPusher) Push (memberpush.go:88) with a
    git that answers by argv and records every call's directory and env, a sleep that records the waits,
    and gh off PATH, so the head's push, the launch's refs, the waits and the pull-request note are
    checked with no real git and no clock. Push's unit statement coverage moves from 0.0% to 94.2%. The
    checkoutTip not-a-descendant branch reads a *exec.ExitError a fake cannot build, so it stays in the
    slow tier."
   :origin "card cover-member-push-with-a-scripted-git.w2, from tmp/v1.2.12-inventory-2026-10-11.md section 8 item C10")

  (fix "cover-native-prepare-refusals-and-report" :release "v1.2.12" :status "planned"
   :title "Cover prepare's refusals and report's outcomes"
   :text "Tests and models only, no product change. Eleven unit tests in cmd/nova-swarm/
    native_prepare_behaviour_test.go pin the refusals nativeRun's prepare makes before it spends
    anything -- a model with no provider prefix, a worker description pinning another model, an unset
    worker secret, a slot outside the root, a walking label, a results root inside the job directory,
    a job or slot lease held by a live run, a public worker with an unlisted repo (exit 1), and a
    loose auth file whose leases are released -- driving nativeRun with a nativeRunConfig built the
    way native_test.go builds one; prepare's unit statement coverage moves from 39.3% to 54.1%. Three
    functional tests in native_report_functional_test.go pin report's outcomes on a run of the fake
    harness buildShared builds: a child that exits non-zero ends failed, one that exits 0 and writes
    no RESULT.md is harness-silent, and one whose last turn asked a question has the asked result
    written. The public-worker test spells its clone URL from separate literals, as pkg/swarm's own
    publicclass_test.go does, so no test names a real network host and the CI net class test stays
    green."
   :origin "the card cover-native-prepare-refusals-and-report.w3 (stream v1212), from the v1.2.12 inventory's section 8 item C11 (tmp/v1.2.12-inventory-2026-10-11.md)")

  (fix "cover-member-end-and-script-read" :release "v1.2.12" :status "planned"
   :title "Unit coverage for the launch's end and the script read"
   :text "Two member steps had branches no test reached: (m *Member) endEnded, the end of a launch
    (a read posts its report alone; a work card pushes, at most pushWidth at once, or records why not)
    at member.go:1913, and ScriptVerifier.Verify, the script read (it checks out the start commit, runs
    the class's program under its deadline, and compares the diff with the head's byte for byte) at
    member.go:2418. Two new test files drive both through Member.Tick and Verify over the package's
    fakes and real git on temporary repositories: end_ended_behaviour_test.go and
    script_verify_behaviour_test.go. With -cover at dev 58d2867, endEnded 27.3% to 93.9% and Verify
    79.2% to 87.5%. No product change."
   :origin "the card cover-member-end-and-script-read.w2 (tmp/v1.2.12-inventory-2026-10-11.md section 8 item C12)")

  (fix "nt-tokens-reentered-child-keeps-its-cover-dir" :release "v1.2.12" :status "planned"
   :title "A re-entered test child keeps the parent's coverage directory"
   :text "TestEveryPathIsAFlagAndNoEnvironmentIsConsulted builds bait under t.TempDir(), points
    TMPDIR, HOME and XDG_DATA_HOME at it, and re-enters its own binary (reenterTest) so the
    body owns the process. Under -cover the child, given no -test.gocoverdir, builds its own
    coverage directory in TMPDIR at exit; by then the test's cleanup has removed the bait, so
    the child exits non-zero with \"error setting GOCOVERDIR: bad os.MkdirTemp return\" and the
    parent reads that as the tool's failure. reenterTest now passes the parent's own
    -test.gocoverdir to the child when one is set, so the coverage teardown touches no path the
    test took away, and the test still fails if the tool itself reads TMPDIR."
   :origin "card nt-tokens-reentered-child-keeps-its-cover-dir, v1.2.12 inventory card A8 (tmp/v1.2.12-inventory-2026-10-11.md)")

  (fix "nt-usage-limit-test-reads-beats-without-an-order" :release "v1.2.12" :status "planned"
   :title "TestUsageLimitMarksDownUntilReset reads the beat and asked order the product promises"
   :text "The beats seen are an optional leading up, then down with the until and the reason until the
    reset, then up after the wake, and never up between the limit and the reset; the asked order asserts
    the set (the message and one wake turn) and the one order the gate documents (the wake before the
    message goes in). The old assertion assumed the daemon's first beat ran before the delivery that hits
    the limit inside one synctest instant, which the scheduler does not fix. Test-side only; no product
    change."
   :origin "the v1.2.12 plan (tmp/point-release-plan-v1.2.11-v1.2.12.md, the nova-tools certification
    fix, 2026-10-11): TestUsageLimitMarksDownUntilReset ordering assumption; rechecked at dev 7b6cccbc2 on
    2026-10-11: still open")

  (fix "nt-friend-token-cap-test-passes-repeated" :release "v1.2.12" :status "planned"
   :title "A repeated run of the one-shot token-cap test passes"
   :text "TestOneShotLaneStopsAtTheTokenCapAndHoldsWithTheReason failed under -count>1 in its opencode
    over-cap case: the injected clock's ticks could reach the cap before the fake harness's run had
    launched, so the lane cancelled a run that never started and the stopped child reported a zero
    exit. The opencode case now pairs on a fifo the harness opens only once its run is up, so the
    clock ticks after the child it must stop has started; the assertions are unchanged."
   :origin "the v1.2.12 plan (tmp/point-release-plan-v1.2.11-v1.2.12.md, \"From the nova-tools certification fix\", 2026-10-11); this card nt-friend-token-cap-test-passes-repeated.w1")

  (fix "cover-provider-budget-proxy-unit" :release "v1.2.12" :status "planned"
   :title "The provider proxy's global rate budget pinned in the unit tier"
   :text "takeBudget (providerbudget.go:107), serveBudgeted (:181) and forwardBudgeted (:228) were never run by
    a unit test (0 statements each under -cover), only by the functional tier over loopback.
    pkg/swarm/providerbudget_behaviour_test.go drives (*ProviderProxy).ServeHTTP (providerproxy.go:423) with a
    budget set through an in-process RoundTripper, a ProxyBudget whose Take, Release, Renew, Log and Sleep are
    recorded fakes, and an httptest recorder: a take told to wait stalls and then grants (waits clamped into
    [10 ms, BudgetPollMax], the holder released once), an unreadable take goes open with one RATE BUDGET OPEN
    line, a stall logs one RATE BUDGET WAIT then one RATE BUDGET GRANTED with the waits, a limit refusal is
    sent again after a doubling backoff with the same method, path, query, body and ContentLength and hop
    headers dropped, out of funds passes through whole even over 64 KiB, the run ending during a stall or a
    refusal backoff is a 502, an unreadable body is a 502 before any take, a transport error is a 502 with the
    slot still released, and a send renews its slot while it runs. Statement coverage: takeBudget 0% to 92.6%,
    serveBudgeted 0% to 100.0%, forwardBudgeted 0% to 77.6%. A silent-body abort is a v1.2.14 candidate: the
    doc says each send frees its slot after, but Release is called only after forwardBudgeted returns and a
    body-silence abort panics http.ErrAbortHandler first, so the slot stays held until its lease expires."
   :origin "card cover-provider-budget-proxy-unit.w6, from tmp/v1.2.12-inventory-2026-10-11.md section 8 item C9")

  (fix "nt-release-cycle-stubs-written-under-the-fork-lock" :release "v1.2.12" :status "planned"
   :title "pkg/release's cycle stubs are written under the fork lock"
   :text "cycle_test.go wrote its fake nova-inventory scripts with os.WriteFile before the code under
    test exec'd them, so a parallel test's fork inherited the open write descriptor and the exec raced
    into ETXTBSY, \"text file busy\" (TestCycleStopsOnAFailedBench). The three executable stubs now use
    testbin.WriteExecutable, which holds syscall.ForkLock for reading while the file is open."
   :origin "card nt-release-cycle-stubs-written-under-the-fork-lock.w2 (v1.2.12 inventory card A6, section 10 note 5)")

  (fix "tla-loopredial-names-what-the-code-cites" :release "v1.2.12" :status "planned"
   :title "The LoopRedial model names what sprintredial.go cites"
   :text "Tests and models only, no product change. LoopRedial now holds the actions and properties
    sprintredial.go cites: the dial is Redial (a verb on a fresh connection whose error is how a
    restart is noticed), the backoff doubles one second to MaxBackoff and is bounded by the new
    invariant BackoffBounded, and the reversed witness nobound doubles a failed verb's backoff past
    the bound and fails BackoffBounded. NoParkOnDropped (the dead connection the code avoids) and
    BeatsAgain stay, MCLoopRedialBrokenNoRedial keeps its NoParkOnDropped failure, and the comments
    in sprintredial.go name the actions and properties the module has; DeadConnection and Backoff
    had no counterpart and left. pkg/member/member.go still does not cite the module."
   :origin "the card tla-loopredial-names-what-the-code-cites.w4 (stream v1212)")

  (fix "tla-reach-from-a-step-and-early-proof" :release "v1.2.12" :status "planned"
   :title "Reach models --from, an early proof and a refused window"
   :text "Reach.tla drifted from cmd/nova-friend/reach.go: the model started at bus and left a step
    only at its bound, where the code starts at --from (reachSteps), leaves a push at once when the
    daemon is down or the push fails, refuses (exit 2) a window without the accessibility permission,
    and accepts a proof any time inside the bound. The module now has a From constant (the ladder's
    suffix), an immediate climb on a failed push, a refused end, a proof inside the bound, and
    FailedOnlyAfterAllThree restated as tried = StepsFrom; a new reversed witness (fromtriedall) claims
    all three tried after a --from start and fails the restated invariant. Tests and models only, no
    product change."
   :origin "the card tla-reach-from-a-step-and-early-proof.w1 (stream v1212)")

  (fix "tla-friendlane-every-hand-back-is-a-request" :release "v1.2.12" :status "planned"
   :title "FriendLane models a work card's no-begin run and the row-read drop"
   :text "The FriendLane model now does what lane_take.go does: a work card goes from taken to running
    with no begin (a read keeps its begin, read_lanes.go), a row read that disagrees drops the card
    locally and the lane asks again (laneCheck), and Card.Epoch's directory-epoch fallback (the
    LocalEpoch witness's live behaviour wherever the server named none) is named. A seventh reversed
    witness, keeponmismatch, lets a lane keep running its card after a row read disagrees and fails
    WorkingOnlyOnTheServersSay. Tests and models only, no product change."
   :origin "card tla-friendlane-every-hand-back-is-a-request.w6 (stream v1212)")

  (fix "nt-retire-the-cardmachine-copy" :release "v1.2.12" :status "planned"
   :title "The CardMachine copy is retired; nova-sprint's is the one copy"
   :text "tla/CardMachine.tla modelled 02_card_move.lua, which neither repository carries any more, and
    nothing here extended or cited it; its one case, MCCardMachine.cfg, had only a timed-out record
    (exit 124 at 110 s). The module, its MC module and configuration, and its CASES.tsv and RUNS.tsv rows
    are removed; nova-sprint's tla/CardMachine.tla is the one copy, kept for its CardISA. The TLC record
    class test loses its MCCardMachine exception, so every case is now held to a current record and a debt
    declaration is refused everywhere (witness declared-debt); tla/README.md says the copy was retired."
   :origin "card nt-retire-the-cardmachine-copy-2.w1 (v1.2.12 inventory item T34; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-table-layer-headers-name-table-lua" :release "v1.2.12" :status "planned"
   :title "The table layer's headers name the table.lua they model"
   :text "EpochMemberTable and MemberTable no longer say PROPOSED: the epoch composition and member
    placement they model are what pkg/nsprint/fn/lua/table.lua implements today (STALE and EPOCHAHEAD
    :1159-1164, MEMBEREPOCH :665 and :687, the per-table place:<name> :700, OWNEDALIAS :891, OCCUPIED
    :917 and :921, PLACED :1636, DRIFT :939). MemberMove keeps its row/col shape: the batch member move
    names a destination row and column (MemberMoveOp, store.go:1491; table.lua:2806, :2816), and only
    the separate cell move verb is row-only (table.lua:1619). TableMachine says it is the baseline of
    table-pinned.lua, whose six findings hold only there (tlacheck witnesses); TableEdit cites the
    current ns_table_set :1354, ns_table_rows_add :1213, ns_table_rows_hide :1222, ns_table_row_set
    :1236 and T.reshape :1263-1300. TABLE-MODEL.md and MEMBER-TABLE-MODEL.md say the same where they
    said proposed. Tests and models only, no product change."
   :origin "the card tla-table-layer-headers-name-table-lua.w1 (stream v1212), from the v1.2.12 inventory item T35 (the table layer)")

  (fix "cover-nova-friend-run-and-serve-faults" :release "v1.2.12" :status "planned"
   :title "Unit tests pin how nova-friend run, serve and the dispatcher answer faults"
   :text "cmd/nova-friend/run_serve_faults_test.go drives the wall and hook dispatch, the coordinator's
    ping loop and the daemon's fault paths through the package rig: a wall with no command or an unknown
    flag is refused at sandbox.ExitRefused and wall -h exits 0; hook -h is the tool's help, never the
    claude hook; serve notes a store that fails one tick then answers, notes the rows it cannot re-read
    while its pings go on, and waits through a store that will not open; a limit with no seat and no
    --coordinator is said on the record, a credit refusal with no down verb (and with one that errors) is
    said and told once on the bus, and --refuse-go with an unbuildable shim runs no lane. Found defect:
    serve refuses (exit 2) when the store opens but the first friend-rows read fails, where openUntil's
    doc says a store down at the start is no reason to exit (v1.2.14)."
   :origin "card cover-nova-friend-run-and-serve-faults (stream v1212), tmp/v1.2.12-inventory-2026-10-11.md section 8 item C13")

  (fix "tla-cairnstore-no-gap-ledger-and-reopen" :release "v1.2.12" :status "planned"
   :title "CairnStore models the code as it is: no gap ledger, no publish, a refused re-open, a healed open"
   :text "The module's header cited stale line ranges, it held a gap ledger and a Liveness the code has no
    counterpart for (Index builds rows from entries/*.json and Ledger holds only counts), and it modelled
    Publish and RemoteDown over a package that implements no transport. Those are removed and the header
    says so. Open now refuses a re-open naming another publish policy, and an open interrupted before
    logging is healed on the next open, both as cairn.go does. A new OneOpenPolicy invariant holds, with
    the reversed witness BrokenReopenOther (a re-open accepted under another policy); BrokenPublishedWithoutPersisted
    left with PublishedImpliesPersisted. cairn.go now cites the module from Open and appendEntry."
   :origin "card tla-cairnstore-no-gap-ledger-and-reopen.w4 (v1.2.12 inventory item T35; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-friendnotifications-withdraw-and-coalesce" :release "v1.2.12" :status "planned"
   :title "FriendNotifications models the deal-notice withdrawal, coalescing and the wake ping"
   :text "FriendNotifications.tla gains the deal notice's withdrawal (a Withdraw action guarded by an
    allTaken ghost, acked with the ready bit cleared and nothing queued, which NoLostReady now allows),
    per-friend coalescing (the newer notice replaces the older, the older acked) and the wake ping entering
    the urgent batch with the pong line; the reversed witness MCFriendNotificationsBrokenWithdrawHeld
    withdraws while a card is still held and fails NoLostReady. FriendNotificationsCapacity's header names
    the Codex queue's read as the capacity and that held nonurgent input returns after ClaimAfter, cited
    from adapter_codex.go. Tests and models only, no product change."
   :origin "card tla-friendnotifications-withdraw-and-coalesce.w3 (v1.2.12 inventory item T35; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-friend-link-and-challenge" :release "v1.2.12" :status "planned"
   :title "Friend's link and challenge model brought level with the daemon"
   :text "Friend.tla's header names pkg/friend, not internal/friend; a ping read past the challenge
    window is dropped, never answered, instead of every ping being answered; any line the session
    writes after a ping ends the challenge and proves the push, where the model's Pong needed a seen
    nonce; and a proven flag gates delivery, so nothing is delivered until the session is proven, held
    by the new NoTurnBeforeProof invariant and its reversed witness noproof
    (MCFriendBrokenNoProof.cfg). Tests and models only, no product change."
   :origin "card tla-friend-link-and-challenge.w4 (nova-tools drift read at dev 2314d437f, 2026-10-11, inventory item T35; the first of three Friend cards, chained)")

  (fix "tla-moved-sprint-models-leave-nova-tools" :release "v1.2.12" :status "planned"
   :title "FriendReadyTake, FriendRedealLate and StopReturn retire; nova-sprint's are the ones"
   :text "FriendReadyTake, FriendRedealLate and StopReturn model nova-sprint code (the server-side take,
    deal and redeal, and the stop-return, now StopFence) that moved to mas-bandwidth/nova-sprint in
    v1.2.3, and the models move there now. The three modules, their MC modules and configurations, and
    their CASES.tsv and RUNS.tsv rows are removed; no module here extended or cited them. COVERAGE.tsv
    keeps the friend's one-shot lanes as missing (owed_by tla-friend-one-shot-lanes); tla/README.md says
    the three moved in v1.2.12."
   :origin "card tla-moved-sprint-models-leave-nova-tools.w1 (v1.2.12 inventory item T33; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-nova-tools-matching-models-cite-their-code" :release "v1.2.12" :status "planned"
   :title "Nine models name today's code and are cited by it"
   :text "BenchStage, FileLock, FirstConn, FuseBox, RedisFn, TableSession, TableFirstContact,
    TableOrder and WakeCursor match their code; only their headers' paths and line numbers, and
    the code's citations of them, were stale. Each header now names today's path and lines
    (pkg/release/install.go, pkg/filelock, pkg/redisconn/open.go, cmd/nova-fuse/check.go,
    pkg/redisfn/store.go, cmd/nova-table, cmd/nova-bus, pkg/nsprint/fn/lua/table.lua) and states
    the one difference where there is one: BenchStage's seed guard is the stage's SHA256SUMS being
    absent, not any file; FirstConn disarms only the last dial; TableFirstContact's Skipped sets
    done with no resend; WakeCursor does not tell a first arm from a re-arm. install.go cites
    tla/BenchStage.tla at its verify, check.go cites tla/FuseBox.tla on cmdCheck, pkg/filelock's
    caller list adds pkg/friend and cmd/nova-config, and tla/README.md says six FuseBox witnesses.
    The nine groups rerun and merge clean (tla/RUNS.tsv). Tests and models only, no product change."
   :origin "card tla-nova-tools-matching-models-cite-their-code.w3 (v1.2.12 inventory item T35; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-cardcontract-retire-the-sprint-actions" :release "v1.2.12" :status "planned"
   :title "CardContract retires the sprint's redeal, retire, restage, staging bound and level"
   :text "CardContract.tla's Redeal, Retire, Restage, StagingAll and Level modelled internal/sprint's side
    (the redeal after a provider finish, the retire at the redeal bound, the restage after a staging
    refusal, the staging bound and the level), which moved to nova-sprint in v1.2.3 and nothing in
    nova-tools runs. Those actions, the sprint-side variables (rt, tried, rds, fw, bj, mem, refused, sj,
    lvd) and constants (Routes, MaxRedeals, Members), and the invariants they alone reached
    (NeverOnARefuser, StagingBoundJudgedOnce, RedealBound, BoundJudgedOnce, RedealAvoidsFailedRoute,
    RedealRestagesTheSameBase) are removed, with their reversed witnesses (sameroute, unbounded,
    levelrefuser) and their CASES.tsv and RUNS.tsv rows. The member's push and the one judgment stay; a
    provider finish and a staging refusal are now terminal finishes, and EveryLaunchEnds is restated over
    them. Tests and models only, no product change."
   :origin "card tla-cardcontract-retire-the-sprint-actions.w2 (v1.2.12; drift read at dev 2314d437f, 2026-10-11; the second CardContract card)")

  (fix "tla-friend-one-shot-lanes-and-caps" :release "v1.2.12" :status "planned"
   :title "Friend's one-shot lanes and caps model the code: no limit gate, a fault kept, a cap set aside after one turn"
   :text "LaneStart no longer needs the harness up: the lanes bypass the limit gate (gatedLanes), so a lane's
    card turn starts while the harness is limited and a turn that hits a limit sends the friend down all the
    same. A card's end gains a fault outcome (exit 0, no report) that keeps the card in the lane's hand counted
    toward nothing, and a capped outcome that sets the card aside after one turn. SetAsideAfterCardTurns is
    restated: a card a lane still holds has fewer than CardTurns tries, and a card set aside by its turns had
    its CardTurns turns. The reversed witness faultcounts (MCFriendBrokenFaultCounts.cfg) counts a fault toward
    the tries and fails the restated invariant. lanes.go cites the module from laneStep and laneDone, limit.go
    from Gate. Tests and models only, no product change."
   :origin "card tla-friend-one-shot-lanes-and-caps.w7 (nova-tools drift read at dev 2314d437f, 2026-10-11; the last of three Friend cards, chained)")

  (fix "tla-coverage-names-every-module-in-nova-tools" :release "v1.2.12" :status "planned"
   :title "Every TLA+ module is named by a COVERAGE row"
   :text "Tests and models only, no product change. TestEveryStateMachineHasACurrentModel walked the rows
    of tla/COVERAGE.tsv and never saw a module no row named; a check beside it, tlaCoverageModuleProblems,
    walks every tla/<X>.tla whose name does not begin with MC and reports each one named by no row (with the
    card that owes it) or by more than one. TestTLACoverageNamesEveryModule proves both shapes over a fixture
    root under t.TempDir(). Four modules -- BusSendOnce, Delivery, FriendPresence and StopCancels -- had no
    row and now have one, partial with the card that owes the model; the notes say what each module holds and
    what is outside it. Rows the walk found drifted are marked partial with the card that owes them:
    friend-lifecycle (tla-friend-link-and-challenge), friend-stage, bus-delivery, nova-update-apply,
    cairn-store, card-contract, bench-run, config, bus-receipts, friend-lane-take, friend-notifications,
    friend-reach and nova-table (the last also names pkg/ntable/store.go and pkg/nsprint/fn/lua/table.lua,
    the table it models). No sprint-stop-fence row is added: StopReturn.tla moved to nova-sprint
    (tla-moved-sprint-models-leave-nova-tools), and the row walk refuses a module with no tla/<module>.tla;
    no friend-lanes-and-take row is added: that row was retired by tla-friend-one-shot-lanes-and-caps, and
    friend-lane-take is the row it left. No sprint-redial row is added: LoopRedial is already named by the
    reader-loop row, made current by tla-loopredial-names-what-the-code-cites. docs/SPEC-CI.md says the
    check."
   :origin "card tla-coverage-names-every-module-in-nova-tools-after-the-retire.w4 (stream v1212, 2026-10-11; the module walk the retired CardMachine copy left)")

  (fix "tla-delivery-note-from-the-seat" :release "v1.2.12" :status "planned"
   :title "Delivery's present takes its note only from the seat"
   :text "The module took any note as the newest and always delivered it in the present step, with a gap
    rule that always applied. It now carries a seat variable: the note is the newest note from the seat
    (none while the seat is unknown, and a one-shot present supersedes the note too), a present whose
    turn fails is owed again after RecheckEvery carrying the same note, and the gap rule holds only in
    batch mode, never for a passive harness, and not before the first beat. NoteFromSeat holds, with a
    new reversed witness anysender that picks a note not from the seat; the three existing witnesses keep
    their results. adapter_tmux.go's Deliver comment no longer cites Delivery for the idle pane, which
    the module does not model. Tests and models only, no product change."
   :origin "card tla-delivery-note-from-the-seat.w10 (stream v1212; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-stopcancels-held-cards-and-reads" :release "v1.2.12" :status "planned"
   :title "The stop-cancels model holds the take, the held card and the reads"
   :text "StopCancels.tla named its worker's functions internal/member machineStop and
    internal/friend/stop.go, both gone (pkg/member/member.go, pkg/friend/stop.go); its Return
    required the word STOPPED where the code sends every owed stop-return whatever the word; its
    Start took and launched in one step where the code claims a card and holds it before it launches,
    a held card given up with its return owed when the word turns (releaseHeld); and it had no reads,
    where stopReads cancels every read under way and owes its return. The module now names pkg/member
    and pkg/friend, splits Take from Start, gives the held card up on STOP, cancels reads with owed
    returns, and sends Return in either word, with a reversed witness (heldthrough: a held card
    launches through the stop, breaking NoLaunchAfterStop). NoLaunchAfterStop still holds. Only lanes
    and reads are cancelled: a batch turn already running goes on (a dealt-card turn started by
    startDealt, daemon.go), a v1.2.14 candidate."
   :origin "card tla-stopcancels-held-cards-and-reads.w7 (v1.2.12 inventory item T35; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-benchrun-an-interrupted-exec-ran" :release "v1.2.12" :status "planned"
   :title "BenchRun's exec interrupt kills the command, never refuses a run that ran"
   :text "BenchRun.tla's header names pkg/bench, not internal/bench. The Interrupt action is split into
    InterruptCopy (an interrupt during the copy refuses the run, as before) and InterruptExec (an
    interrupt during the exec sets ranCmd and exits Killed, the code's sshLine ExitCode -1, stored as
    Code by runIn and the verb's exit); the new InterruptedExecRan invariant holds that a run whose
    command ran is never refused. The reversed witness MCBenchRunBrokenInterruptRefused.cfg (an exec
    interrupt that refuses a run whose command ran) fails it. cmd/nova-ci/bench.go cites the module from
    its exit mapping. Tests and models only, no product change."
   :origin "card tla-benchrun-an-interrupted-exec-ran.w4 (v1.2.12 inventory item T35; drift read at dev 2314d437f, 2026-10-11)")

  (fix "tla-updateapply-newer-and-delivery-scopes" :release "v1.2.12" :status "planned"
   :title "UpdateApply reads NEWER for a greater release and delivers per recipient scope"
   :text "UpdateApply.tla's verdict read any installed version greater than the latest as AHEAD; the code
    reads NEWER for a greater release version and AHEAD only for the vX.Y.Z-0 pseudo-version. The module
    now reads NEWER and never AHEAD (its versions are numbers, never pseudo-versions), records a delivery
    per recipient scope instead of one delivered boolean, and corrects the header's `confirmed` and
    `writeSnapshot` names to `sendOK` and `writeSnapshotWith`. A new reversed witness aheadisnewer reads
    AHEAD for a greater release version and fails a new VerdictMatchesVersion invariant, and report.go
    and cli.go cite the module from report and verdict. Tests and models only, no product change."
   :origin "the card tla-updateapply-newer-and-delivery-scopes.w5 (nova-tools drift read at dev 2314d437f, 2026-10-11, inventory item T35)")

  (fix "tla-bus2-names-pkg-bus-and-the-idle-claim" :release "v1.2.12" :status "planned"
   :title "Bus2 names pkg/bus and models the idle claim"
   :text "Bus2's header named internal/bus2/bus2.go and docs/SPEC-BUS2.md, both gone; it now names
    pkg/bus/bus.go and redis.go and docs/SPEC-BUS.md. The model told a dead holder only by a crash; the
    code tells one by the entry's idle time past ClaimAfter, with one consumer name for every reader, so
    Bus2 gains a Stall action (a live holder idle past ClaimAfter becomes claimable by any reader, its
    holder NoOne) and HeldStaysHeld is restated as held while under ClaimAfter. A message's cc is folded
    into its recipients. A sixth reversed witness, claimfresh, claims a holder before ClaimAfter and
    fails HeldStaysHeld; the five existing witnesses and Bus2Receipts' cases keep their results. A
    message's kind and RecvKinds' release of a skipped kind stay below the module's grain. Tests and
    models only, no product change."
   :origin "card tla-bus2-names-pkg-bus-and-the-idle-claim.w9 (v1.2.12 inventory item T32; drift read at dev 2314d437f, 2026-10-11)")

  (fix "roadmap-tool-becomes-nova-roadmap" :release "v1.2.13" :status "planned"
   :title "Extract the roadmap tool into nova-roadmap"
   :text "The roadmap and fixes code exists twice: here as internal/roadmap, internal/roadmap/sexp and
    tools/roadmap (about 1,300 lines with their tests, about 930 without), and in nova-sprint as
    internal/roadmap, internal/roadmapdoc, tools/roadmap and the nova-work roadmap verbs, with two
    diverging copies of the generator. Unify the s-expression format first; then one module and one
    CLI, nova-roadmap, living in the nova-sprint repository (a module and cmd/nova-roadmap there): it
    reads and writes the data, renders ROADMAP.md and FIXES.md, and runs the sync check. Both
    repositories use it and delete their own copies: this one imports nova-roadmap from nova-sprint and
    deletes internal/roadmap and tools/roadmap. Net fewer lines. Its s-expressions stay importable
    later as a node by nova-work; that import is a v1.3 item, not this one."
   :origin "the owner, 2026-10-11: v1.2.13 contraction, pass 4 (shared code into modules); nova-roadmap lives in nova-sprint")

  (fix "brief-boilerplate-into-the-card-contract" :release "v1.2.13" :status "planned"
   :title "Brief boilerplate moves into the card contract"
   :text "The RULES, LEDGERS, WALL AND TOOLS and BUDGET blocks, about 6 KB of every 10 to 11 KB brief, are
    referenced once from the card contract instead of repeated in each brief, and the card lint
    (pkg/swarm) refuses repeated instruction text in a brief. nova-sprint's briefs-by-reference is the
    same change on its side."
   :origin "the owner, 2026-10-11: a lesson from the Cockroach Labs write-up \"Five months treating bugs like patients\" (MOLT Sinai)")

  (fix "vet-windows-tag-and-test-binary-home" :release "v1.2.10" :status "shipped"
   :title "dev's queue unblocked: the vet-windows build tag, and a launched test binary gets its own HOME"
   :origin "PR #5605")

  (fix "a-draining-member-says-so-on-its-beat" :release "v1.2.10" :status "shipped"
   :title "A draining member says so on its beat (--draining)"
   :origin "PR #5606")

  (fix "friend-acceptance-dry-run" :release "v1.2.10" :status "shipped"
   :title "nova-friend acceptance takes --dry-run"
   :origin "PR #5612")

  (fix "certification-bus-and-friend-test-rigs" :release "v1.2.10" :status "shipped"
   :title "Certification: the pkg/bus and cmd/nova-friend test rigs synchronized"
   :origin "PR #5607")

  (fix "doctor-stubs-under-the-real-temp-path" :release "v1.2.10" :status "shipped"
   :title "nova-swarm doctor stubs live under the temporary directory's real path"
   :origin "PR #5608")

  (fix "never-force-rule-skips-unmatchable-lines" :release "v1.2.10" :status "shipped"
   :title "The never-force rule skips lines no refused pattern can match"
   :origin "PR #5609")

  (fix "redis-version-sweep-skips-lines-without-redi" :release "v1.2.10" :status "shipped"
   :title "The Redis version sweep skips lines without \"redi\""
   :origin "PR #5610")

  (fix "fx-empty-fixes-file-after-the-clear" :release "v1.2.11" :status "in-progress"
   :title "The fixes ledger brought level with the code"
   :text "Fixes marked planned or in-progress that had already landed are marked shipped in the release that carried them (commit 53bc5748c). Landed on dev; ships with v1.2.11."
   :origin "card fx-empty-fixes-file-after-the-clear, landed on dev as 7b6cccbc2")

  (fix "go-mod-tidy-nightly-jobs" :release "v1.2.11" :status "in-progress"
   :title "Nightly and certification jobs fail on an untidy go.mod"
   :text "The Makefile gains a mod-tidy target that runs go mod tidy -diff and refuses an untidy go.mod or go.sum, lint depends on it so make check catches one, the ci.yml lint job runs make mod-tidy after gofmt, and TestAnUntidyModuleIsRefusedOnTheCLPath pins all three."
   :origin "issue #5497; card go-mod-tidy-nightly-jobs.w6")

  (fix "adopt-play-loops-step-works-on-every-host" :release "v1.2.11" :status "in-progress"
   :title "The adopt's loops step works on every host"
   :text "One nova-sprint live manifest command per platform (launchd agents and system-domain LaunchDaemons on Darwin, systemd units on Linux, defined once in group_vars); the loops step succeeds when every member and reader already runs the installed binary and refuses only a manifest with no member or reader; each stale agent restarts through its own target's domain, and the adoption table's disk-free read runs under --check and defaults an empty df."
   :origin "card adopt-play-loops-step-works-on-every-host.w4")

  (fix "a-run-that-ends-at-its-first-result-is-not-a-finish" :release "v1.2.14" :status "planned"
   :title "A run that ends at its first RESULT.md is a no-result finish"
   :text "A child whose harness stopped soon after it wrote the early RESULT.md its brief asks for (verdict not-done, nothing committed) was judged failed work, spending an attempt, while the same run with no RESULT.md was judged no result and dealt again. member.Judge now classes a shaped not-done run under FirstResultWall (two minutes), with nothing committed and no gate, as the same no-result kind, with a reason that keeps the \"no result:\" prefix so nova-sprint's IsNoResult routes it."
   :origin "the seat, 2026-10-11: fx-hosted-tests-green-on-mainc attempt 10, ran 62s, wrote an early RESULT.md (starting: will run ...) and stopped; re-scoped against nova-tools dev 2314d43; card a-run-that-ends-at-its-first-result-is-not-a-finish")

  (fix "a-refused-finish-never-reruns-a-pushed-generation" :release "v1.2.11" :status "in-progress"
   :title "A refused finish never reruns a pushed generation"
   :text "A finish the store refuses after its push is no longer taken as the end of the work. The
    member prints the refusal, and when the refused finish carried a pushed head it remembers the
    generation with the finish's arguments exactly as sent; recoverWorking never runs a card whose
    card@gen is held, and once a pass each due finish is sent again byte for byte with backoff
    (FinishRefusedRetry, doubling to 8m). The same refusal FinishRefusedMax (3) times hands the
    card back with stop-return instead of running it, and the memory goes when the queue no longer
    lists the card at that generation on this row. A refused finish with nothing pushed is
    forgotten as today."
   :origin "the coordinator, 2026-10-11: the v1.2.10 nova-sprint server refused every ok finish, the member forgot each refused card and reran the generation, and the second push was rejected non-fast-forward; card a-refused-finish-never-reruns-a-pushed-generation.w1")

  (fix "a-start-scheduled-before-stop-launches-no-child" :release "v1.2.14" :status "planned"
   :title "A start scheduled before STOP launches no child"
   :text "The member read the machine's word (queueOut.Machine) only at the head of the pass, so a
    start already queued behind startMu when STOP arrived still launched its child after the stop.
    The word is now kept in an atomic (stoppedWord) the launch's long work can read, and a start
    reads it again after staggerStart and before scriptOrStart, with startMu held: a STOPPED word
    starts no child, the launch is posted as cancelled by the stop and collect drops it, leaving
    the card working under this row with no child of ours for machineStop to hand back with
    stop-return, never finished."
   :origin "the audit defect \"a background start after STOP launches a child\" (nova-tools pkg/member staggerStart / scriptOrStart), confirmed by reading nova-tools dev 2314d43 on 2026-10-11; card a-start-scheduled-before-stop-launches-no-child.w2")

  (fix "doctor-checks-every-friend-daemons-setup" :release "v1.2.14" :status "planned"
   :title "nova-doctor checks every friend daemon's setup"
   :text "Six new non-Fleet checks read each friend daemon this machine loads (launchd plists on darwin, systemd user units on linux) and fail with the one fix line for a fault a person would otherwise find: --coordinator against the sprint seat, the nova-config row's mode, roles and width, a refused beat or a stale beat while the agent is loaded, --dir against the lane wall's deny-self, a claude config dir that is not signed in, and a plist plutil refuses."
   :origin "card doctor-checks-every-friend-daemons-setup.w2; the owner, 2026-10-11, nova-doctor should catch these things")

  (fix "a-friend-lane-gives-the-harness-its-own-tmpdir" :release "v1.2.15" :status "in-progress"
   :title "A friend lane gives its harness its own TMPDIR"
   :text "Every harness run in a friend lane gets a private, writable temp directory inside its own lane: <lane dir>/tmp, made 0700 before the run and removed after it, with TMPDIR, TMP, TEMP and CLAUDE_CODE_TMPDIR all naming it, so no harness ever touches the shared /tmp. The lane wall carries the directory as --tmp and grants nothing outside the lane. A run that ends within five seconds with no REPORT.md and no RESULT.md and whose output names a filesystem error (EEXIST, EACCES, EPERM, ENOENT) on a path outside its lane ends as an environment fault of the friend: the card stays in the lane's hand, its attempt not counted, her lanes pause and the coordinator gets a pushed judgment naming the error line, never a failed attempt of the card."
   :origin "card a-friend-lane-gives-the-harness-its-own-tmpdir.w2; the seat, 2026-10-11 20:06Z: every claude harness run in a friend lane on the seat's machine died in about a second with EEXIST mkdir '/tmp/claude-501'")

  (fix "the-claude-harness-runs-the-newest-model-of-each-tier-class" :release "v1.2.15" :status "planned"
   :title "The claude harness runs the newest model of each tier's class"
   :text "pkg/friend ReadModels, the one claude tier map (nova-friend's ReadModel hook reads it for every claude lane), maps flash to claude-haiku-5-5 instead of claude-haiku-4-5-20251001; pro stays claude-sonnet-5-5, heavy claude-opus-5-5, frontier claude-fable-5-1. read_lanes_models_test.go pins the whole map and that no tier maps to a model of a bigger class than its own."
   :origin "the owner, 2026-10-11 ~19:20Z: run the most recent model available for each named class; card the-claude-harness-runs-the-newest-model-of-each-tier-class.w2")

  (fix "a-friend-step-collects-every-ended-lane-and-finishes-off-the-loop" :release "v1.2.15" :status "planned"
   :title "A friend step drains every ended result and finishes off the loop"
   :text "Each daemon step took at most one ended result from the batch, lane and read channels, so k
    lanes that ended together were seen over k beats and their lanes stayed held that long; the step now
    drains all of them (capped at maxStepResults), applying each in the loop goroutine so the daemon's
    state stays single-writer. outboxStep no longer sends each finished job's tip check and finish
    serially in the loop, blocking the step's lane take behind every other card's finish: each due job's
    tip check and finish now runs in a goroutine, at most pushWidth at once, marked in flight so the
    next step neither sends it again nor counts it finished, and its answer (finished, refused with the
    reason, tip moved) is posted on a channel the step drains and applied by the loop. On shutdown the
    daemon waits for in-flight finishes up to FinishWait and leaves any unanswered to be sent again
    next run."
   :origin "the owner, 2026-10-11: what else is like serial reads, going out one at a time, when they could go out at the same time; and log the serial things that should be parallel, batching or parallel case by case. Found by the seat's audit child on nova-tools origin/dev 0f628af77 the same day, in pkg/friend/daemon.go ~756-764 and pkg/friend/outbox.go ~223-303; card a-friend-step-collects-every-ended-lane-and-finishes-off-the-loop.w4")))
