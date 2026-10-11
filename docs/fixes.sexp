; The nova-tools point releases: the data FIXES.md is generated from.
; Read by internal/roadmap through internal/roadmap/sexp (data, never evaluated).
; Edit this file, then run `make roadmap`; never edit FIXES.md by hand.
; The shape is in internal/roadmap/fixes.go.
(fixes "v1"
 :title "nova-tools fixes"
 :text "The point releases after v1.2.2: what each one shipped, is shipping or will ship. A point release
  carries fixes only; new work is in ROADMAP.md. nova-sprint's point releases are in that repository's
  FIXES.md."
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
    :text "A worker card whose last turn is a question ends as asked instead of holding its slot."))
 :items
 ((fix "split-sprint-tools-leave" :release "v1.2.3" :status "shipped"
   :title "The sprint tools leave nova-tools"
   :text "Their commands, packages, models and docs move to mas-bandwidth/nova-sprint, and CI refuses their paths here."
   :origin "the split, layers L5 to L7; PR #5591")
  (fix "lua-digest-restore" :release "v1.2.4" :status "shipped"
   :title "Six Lua comment lines restored"
   :text "The function library's digest is its v1.2.2 value again, so the adopt's shadow tick accepts it."
   :origin "PR #5592, head 7d1dc0c06")
  (fix "roadmap-and-fixes-as-data" :release "v1.2.5" :status "in-progress"
   :title "ROADMAP.md and FIXES.md generated from s-expression data"
   :text "docs/roadmap.sexp and docs/fixes.sexp are the data; `make roadmap` writes both pages, and a test
    fails when a page differs from what its data renders."
   :origin "PR #5533")
  (fix "card-tells-child-its-wall" :release "v1.2.6" :status "in-progress"
   :title "The card tells the child its wall: deadline and temporary directory"
   :text "The worker card tells the child its deadline, and the native wall passes the slot temporary
    directory so TMPDIR is the slot one."
   :origin "nova-tools PR #5575, head 4725d7d7d; landed in dev as 7dc8304560 via PR #5594")
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
  (fix "friend-provider-key-after-move" :release "v1.2.6" :status "planned"
   :title "A friend AI moved to another machine uses the provider key that worked"
   :text "After a move the friend AI ran with a stored provider key that differs from the working one, and
    the provider refused it. The stored key is replaced by the owner, then the friend AI restarts
    through the play."
   :origin "v1.2.5 candidate list")
  (fix "release-build-refuses-without-sprint-release" :release "v1.2.6" :status "in-progress"
   :title "release build refuses to ship without the sprint release"
   :text "nova-update release build without --sprint-release ships no sprint tools and succeeds; it
    refuses unless an explicit opt-out flag is given."
   :origin "nova-tools PR #5571 cold read")
  (fix "changelog-lists-merge-queue-merges" :release "v1.2.6" :status "in-progress"
   :title "The release changelog lists merge-queue merges"
   :text "The cut changelog says no pull request merged since the previous tag although the merge queue
    merged many; the listing includes merge-queue merges."
   :origin "v1.2.4 cut note")
  (fix "worker-writes-result-before-deadline" :release "v1.2.6" :status "planned"
   :title "A child ended at its deadline with a commit and no result gets a report"
   :text "The card already tells the child to finish at 80 percent of its wall; for a child that works on
    to the deadline anyway, the run now writes a RESULT.md naming the branch, the commit count and the
    head when the deadline ended it with commits in the repository and no report of its own.
    The report has no findings head, so it is never scored as the child's own work."
   :origin "fault 9, 2026-10-10")
  (fix "draining-member-word-is-this-ticks" :release "v1.2.6" :status "in-progress"
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
  (fix "adopt-removes-friend-units-that-moved" :release "v1.2.6" :status "in-progress"
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
   (fix "fleet-loops-refuses-version-mismatch" :release "v1.2.6" :status "planned"
    :title "A fleet loops play refuses when its checkout version differs from the adopted release"
    :text "Fleet plays run only from the checkout of the adopted release, and refuse when the
     checkout's version differs from the release installed on the host."
    :origin "dogfood 2026-10-10 evening: fleet-only restart")
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
  (fix "friends-snap-to-the-new-epoch" :release "v1.2.6" :status "in-progress"
   :title "A friend's new epoch retires her older-epoch items and starts empty"
   :text "On the sprint's new epoch the daemon moves every older-epoch inbox and outbox item to the
    retired folder named for its epoch, kept, never deleted, and the live folders start the new epoch
    empty; on the same signal the lanes forget their sessions and the present is owed, so a full or
    stale session never carries across a clear."
   :origin "the seat ledger v1.2.4-held-2026-10-10.md friend snap, 2026-10-10: 323 inbox items and 535 outbox REPORTs of epoch 15, each refused (not on her row)")
  (fix "a-friend-session-full-or-missing-starts-fresh" :release "v1.2.6" :status "in-progress"
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
  (fix "friend-width-fits-slots" :release "v1.2.6" :status "planned"
   :title "A friend's width fits within her slots"
   :text "nova-config refuses a friend width larger than her slots and names both values, so one-shot
    lanes cannot silently halve the friend's available capacity."
   :origin "dogfood 2026-10-10: one-shot friend at width 32 with 16 slots")
  (fix "no-friend-runs-in-batch-mode" :release "v1.2.6" :status "in-progress"
   :title "No friend runs in batch mode"
   :text "A friend's delivery mode is one-shot always: nova-config refuses --mode batch, a stored batch row
    reads as one-shot (config.FriendMode, the reader nova-sprint's friend sync writes onto her roster
    row), and her daemon runs one card per lane."
   :origin "dogfood 2026-10-10: a friend row still mode=batch")
  (fix "worker-question-ending-asked" :release "v1.2.9" :status "planned"
   :title "End a card as asked when its last turn is a question"
   :text "A headless card whose last turn asks the user a question ends at once as asked instead of
     holding its slot to the deadline."
   :origin "issue #2548")
  (fix "fleet-built-from-release-not-source" :release "v1.2.8" :status "in-progress"
   :title "Every installed tool is checked against the release's bytes"
   :text "After the install and the friends' reinstall, tools.yml lists each machine's bin directory with
    the sha256 of every file and compares it with the platform's SHA256SUMS; a tool built or copied on
    the machine, which prints the release's version and holds another toolchain's bytes, refuses the
    play (TOOLS REFUSED step=identity) and is named. --check says WOULD-REPLACE and refuses nothing.
    fleet/tools_test.go pins the check and that no task compiles a tool."
   :origin "dogfood 2026-10-10: after the v1.2.5 adopt one machine's nova-friend reported go1.27.1 while every other tool reported go1.26.6")
  (fix "adopt-window-and-dry-run-fixes" :release "v1.2.9" :status "shipped"
   :title "Adopt waits only for agents it stopped and dry-runs honestly"
   :text "The adopt window waits only for the agents the adopt itself stopped, and its dry run reports
    what each step would do instead of saying would for every step."
   :origin "card from the sprint store (2026-10-10)")
  (fix "swarm-idle-reap-verdict" :release "v1.2.9" :status "planned"
   :title "Idle-reaped cards report their real cause and are not wrongly reaped"
   :text "A reaped card reports a wall refusal that names nothing, and the harness shell cap silenced
    working cards. The verdict states idle reaping and the idle watch accounts for the shell cap."
   :origin "issue #2157; issue #2158; issue #2577; issue #2579; issue #2585")
  (fix "swarm-hold-zero-findings-malformed" :release "v1.2.9" :status "planned"
   :title "A HOLD report with zero findings is malformed"
   :text "A verdict of HOLD cannot have no findings, so the result check classes it as malformed."
   :origin "PR #4635")))
