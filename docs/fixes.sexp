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
  (fix "roadmap-and-fixes-as-data" :release "v1.2.6" :status "shipped"
   :title "ROADMAP.md and FIXES.md generated from s-expression data"
   :text "docs/roadmap.sexp and docs/fixes.sexp are the data; `make roadmap` writes both pages, and a test
    fails when a page differs from what its data renders."
   :origin "PR #5533")
  (fix "walled-lane-tokens-read" :release "v1.2.6" :status "shipped"
   :title "A walled lane tokens are read where its harness writes them"
   :text "Walled sessions live under the wall home, tokens are read from there first, and a session in no
    database is unread rather than zero, so the token cap can trip."
   :origin "nova-tools PR #5590, head 0c4fd7ea; landed in dev as 1babe4a43f via PR #5595")
  (fix "provider-key-refusal-rests-until-woken" :release "v1.2.6" :status "shipped"
   :title "A provider that refuses its key rests until woken, never for a time"
   :text "A route whose provider refuses the key rests until routes wake, never for a time, and the rest
    reason reads until woken rather than until paid: the providers table, the stop and the judgment all
    say woken. TLA+ model RouteRest, property AuthEndsOnlyWoken, with a reversed witness. Tests cover
    routes wake on a key rest."
   :origin "nova-tools PR #5574, head 0c6706f2d; the providers table's until-paid wording is v1.2.5 candidate 6")
  (fix "bare-merge-stream-refused" :release "v1.2.6" :status "shipped"
   :title "A bare merge --stream is refused on a real store"
   :text "Landings are recorded by name or by land; a bare merge --stream on a real store is refused."
   :origin "nova-tools PR #5565, head 52618fa02")
  (fix "pin-release-and-rule-answers" :release "v1.2.6" :status "shipped"
   :title "A hard pin past its bound is offered on; a rule answer is consumed by its attempt"
   :text "A hard pin past its bound is offered to others, a card cannot ask once it waits, and a rule
    answer is consumed by its attempt. The pin release clock comment is corrected: the hour runs from
    the stop's latest note, so a coordinator's ack or wait starts it again, and a test says so."
   :origin "nova-tools PR #5573, head daab75763, re-applied here")
  (fix "paths-widening-edits-card-in-place" :release "v1.2.6" :status "shipped"
   :title "A PATHS widening on a reader finding edits the card in place"
   :text "A reader finding that widens PATHS edits the card in place, never a twin. Three review notes are
    fixed before merge: the finding reader is unset only when the card holds it, the bound subtest
    reaches the attempt cap, and the friend card claim says a widen does not pin her next attempt."
   :origin "nova-tools PR #5570, head 22d72154d, re-applied here")
  (fix "check-waived-read-and-where-tiers" :release "v1.2.6" :status "shipped"
   :title "check honours a waived second read; where shows tiers once"
   :text "check judges an accept made before the second-read count by the count then; where shows a tier
    once and hides an expired until. A stale health seen time beside a live status is also fixed."
   :origin "nova-tools PR #5584, head 881df6ff")
  (fix "where-json-landed-series-fast" :release "v1.2.6" :status "shipped"
   :title "where --json reads no log line for its landed series"
   :text "The tick counts landings into the where record, so where --json answers in well under a second
    (8.9 to 12.8 s before, 0.4 to 0.7 s after, measured on a copy of the live store); released sentinels
    are not counted as landings. The public dashboard mirror's server.py (internal/sprintdash/stopgap)
    is now versioned here: it stamps fetchedAt with the snapshot's own time and adds dataAgeSeconds.
    AFTER THE DEPLOY, in the final adopt: rebuild the dashboard's nova-sprint-int2
    (~/nova-bench/dashboard/bin/nova-sprint-int2) from this release, copy
    internal/sprintdash/stopgap/server.py over ~/nova-bench/dashboard/server.py, and restart the
    public mirror unit."
   :origin "nova-tools PR #5588, head 08f6af5a8")
  (fix "done-at-base-is-brief-duplicate" :release "v1.2.6" :status "shipped"
   :title "A card already done at its base is a duplicate brief, not a worker failure"
   :text "Nothing to do because the base already contains the change is a brief defect, a duplicate of
    landed work, outside ok%, and raises a re-cut judgment."
   :origin "nova-tools PR #5585, head 0beb0508")
  (fix "draining-member-clears-no-room" :release "v1.2.6" :status "shipped"
   :title "A draining member does not keep a stale no-room word"
   :text "The room check runs after the drain return, so a draining member does not keep a stale no-room
    word; the deal and read disk floor fix also gets its TLA+ reversed witness."
   :origin "v1.2.5 candidate list; nova-tools PR #5569 follow-up")
  (fix "draining-member-word-is-this-ticks" :release "v1.2.6" :status "shipped"
   :title "A draining member's beat carries this tick's no-room word"
   :text "The member asks Room every tick before the drain return, so a member that drained under its
    disk floor stops saying no room once the disk is freed (nova-sprint tla/NoRoom.tla,
    WordIsThisTicks)."
   :origin "v1.2.5 candidate 5; the PR #5569 follow-up")
  (fix "friend-wall-reads-its-pinned-harness" :release "v1.2.6" :status "shipped"
   :title "A friend's lane wall reads the pinned harness it runs"
   :text "nova-friend install takes --wall-reads and writes it into the agent, and the seat play passes
    nova_friend_wall_reads to a reinstall whose plist records none, so a pinned harness whose shim
    execs a directory under the home no longer exits 126 (Operation not permitted) for every lane."
   :origin "a one-shot friend host, 2026-10-10: every lane exit 126 after the harness pin; the wall denied reading the pinned harness directory")
  (fix "bench-tree-standalone" :release "v1.2.6" :status "shipped"
   :title "A bench tree is a standalone clone, and one that is not is refused with its reason"
   :text "The mirror stage ends by checking that the staged tree's .git is a directory in the tree, and a
    .git file (a worktree pointer at a path of another machine) is refused naming the gitdir and whether
    it exists on the host. The tar copy refuses a worktree's .git file instead of carrying it over, so git
    no longer exits 128 on a fleet host with nothing saying why."
   :origin "lander fault 5579, fleet git exit 128 (held-PR ledger row 5579)")))
