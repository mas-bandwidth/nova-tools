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
  (fix "roadmap-and-fixes-as-data" :release "v1.2.5" :status "shipped"
   :title "ROADMAP.md and FIXES.md generated from s-expression data"
   :text "docs/roadmap.sexp and docs/fixes.sexp are the data; `make roadmap` writes both pages, and a test
    fails when a page differs from what its data renders."
   :origin "PR #5533")
  (fix "bare-merge-stream-refused" :release "v1.2.6" :status "shipped"
   :title "Bare merge stream is refused"
   :text "A bare merge stream without proper sprint release is refused."
   :origin "v1.2.5 candidate list")
  (fix "bench-tree-standalone" :release "v1.2.6" :status "shipped"
   :title "Bench tree works standalone"
   :text "The bench tree can operate standalone for testing."
   :origin "v1.2.5 candidate list")
  (fix "check-waived-read-and-where-tiers" :release "v1.2.6" :status "shipped"
   :title "Check waived read and where tiers"
   :text "Check waivers and where tiers are properly applied."
   :origin "v1.2.5 candidate list")
  (fix "done-at-base-is-brief-duplicate" :release "v1.2.6" :status "shipped"
   :title "Done at base is brief duplicate"
   :text "Done at base duplicates brief entries."
   :origin "v1.2.5 candidate list")
  (fix "draining-member-clears-no-room" :release "v1.2.6" :status "shipped"
   :title "Draining member clears no room"
   :text "Draining member clears no room conditions."
   :origin "v1.2.5 candidate list")
  (fix "draining-member-word-is-this-ticks" :release "v1.2.6" :status "shipped"
   :title "A draining member's beat carries this tick's no-room word"
   :text "The member asks Room every tick before the drain return, so a member that drained under its
    disk floor stops saying no room once the disk is freed (nova-sprint tla/NoRoom.tla,
    WordIsThisTicks)."
   :origin "v1.2.5 candidate 5; the PR #5569 follow-up")
  (fix "paths-widening-edits-card-in-place" :release "v1.2.6" :status "shipped"
   :title "Paths widening edits card in place"
   :text "Paths widening edits the card in place."
   :origin "v1.2.5 candidate list")
  (fix "pin-release-and-rule-answers" :release "v1.2.6" :status "shipped"
   :title "Pin release and rule answers"
   :text "Pin release and rule answers are aligned."
   :origin "v1.2.5 candidate list")
  (fix "provider-key-refusal-rests-until-woken" :release "v1.2.6" :status "shipped"
   :title "Provider key refusal rests until woken"
   :text "Provider key refusal rests until the provider is woken."
   :origin "v1.2.5 candidate list")
  (fix "where-json-landed-series-fast" :release "v1.2.6" :status "shipped"
   :title "Where JSON landed series fast"
   :text "Where JSON landed series fast."
   :origin "v1.2.5 candidate list")))