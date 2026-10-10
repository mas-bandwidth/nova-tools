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
   :text "The roadmap and this page come back as data, and the open issues move into the roadmap."))
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
   :origin "PR #5533")))
