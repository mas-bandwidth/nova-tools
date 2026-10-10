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
    this repository, then cut as one release."))
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
  (fix "card-tells-child-its-wall" :release "v1.2.6" :status "planned"
   :title "The card tells the child its wall: deadline and temporary directory"
   :text "The worker card tells the child its deadline, and the native wall passes the slot temporary
    directory so TMPDIR is the slot one."
   :origin "nova-tools PR #5575, head 4725d7d7d")
  (fix "walled-lane-tokens-read" :release "v1.2.6" :status "planned"
   :title "A walled lane tokens are read where its harness writes them"
   :text "Walled sessions live under the wall home, tokens are read from there first, and a session in no
    database is unread rather than zero, so the token cap can trip."
   :origin "nova-tools PR #5590, head 0c4fd7ea")
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
  (fix "release-build-refuses-without-sprint-release" :release "v1.2.6" :status "planned"
   :title "release build refuses to ship without the sprint release"
   :text "nova-update release build without --sprint-release ships no sprint tools and succeeds; it
    refuses unless an explicit opt-out flag is given."
   :origin "nova-tools PR #5571 cold read")
  (fix "changelog-lists-merge-queue-merges" :release "v1.2.6" :status "planned"
   :title "The release changelog lists merge-queue merges"
   :text "The cut changelog says no pull request merged since the previous tag although the merge queue
    merged many; the listing includes merge-queue merges."
   :origin "v1.2.4 cut note")
  (fix "worker-writes-result-before-deadline" :release "v1.2.6" :status "planned"
   :title "A worker writes its result before the route deadline"
   :text "Children on some direct routes work to the route deadline and never write their result file;
    give the route a longer deadline or reserve a margin to write the result."
   :origin "fault inventory, 2026-10-10")
  (fix "draining-member-word-is-this-ticks" :release "v1.2.6" :status "in-progress"
   :title "A draining member's beat carries this tick's no-room word"
   :text "The member asks Room every tick before the drain return, so a member that drained under its
    disk floor stops saying no room once the disk is freed (nova-sprint tla/NoRoom.tla,
    WordIsThisTicks)."
   :origin "v1.2.5 candidate 5; the PR #5569 follow-up")))
