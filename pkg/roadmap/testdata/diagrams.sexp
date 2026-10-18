; golden input for TestDiagramsGolden: every diagram, the mindmap cap, hostile labels
(roadmap "v1"
 :title "A \"quoted\" roadmap <v1> #1"
 :text "The opening paragraph."
 :scheduled ((scheduled "s1" :release "v1.3" :title "Moved" :text "Into v1.3." :date "2026-10-09"))
 :done ((done "d1" :title "Settled" :text "In place." :date "2026-10-09")
        (done "d2" :title "Also settled" :text "In place too." :date "2026-10-10"))
 :groups ((group "odd" :title "Odd: `ticks` [brackets] {braces}; a\\b" :text "Labels that break Mermaid.")
          (group "big" :title "A big group" :text "More items than the mindmap shows."))
 :items ((item "odd-1" :group "odd" :title "Ends a \"label\"
              across lines" :text "Wrapped." :date "2026-10-13")
  (item "big-1" :group "big" :title "Big item 1" :text "Item 1." :date "2026-10-14")
  (item "big-2" :group "big" :title "Big item 2" :text "Item 2." :date "2026-10-14")
  (item "big-3" :group "big" :title "Big item 3" :text "Item 3." :date "2026-10-14")
  (item "big-4" :group "big" :title "Big item 4" :text "Item 4." :date "2026-10-14")
  (item "big-5" :group "big" :title "Big item 5" :text "Item 5." :date "2026-10-14")
  (item "big-6" :group "big" :title "Big item 6" :text "Item 6." :date "2026-10-14")
  (item "big-7" :group "big" :title "Big item 7" :text "Item 7." :date "2026-10-14")
  (item "big-8" :group "big" :title "Big item 8" :text "Item 8." :date "2026-10-14")
  (item "big-9" :group "big" :title "Big item 9" :text "Item 9." :date "2026-10-14")
  (item "big-10" :group "big" :title "Big item 10" :text "Item 10." :date "2026-10-15")
  (item "big-11" :group "big" :title "Big item 11" :text "Item 11." :date "2026-10-15")
  (item "big-12" :group "big" :title "Big item 12" :text "Item 12." :date "2026-10-15")
  (item "big-13" :group "big" :title "Big item 13" :text "Item 13." :date "2026-10-15")
  (item "big-14" :group "big" :title "Big item 14" :text "Item 14." :date "2026-10-15")))
