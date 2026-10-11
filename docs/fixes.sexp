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
    :text "The next small fixes, one concern each, including the defects v1.2.12's tests find."))
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
   :origin "issue #5497; card go-mod-tidy-nightly-jobs.w6")))
