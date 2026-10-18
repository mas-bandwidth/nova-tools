; golden input for TestFeaturesGolden: an axis area, a done-column area, a work-set
; cell, a later row, extra keys that are kept and not rendered, a closing section,
; and an evidence overlay (roadmap-evidence/) that moves one task to done.
(:schema 1
 :root "goal"
 :page (:title "  Example   roadmap "
        :roadmaps ("wire" "tools")
        :closing ((:title "Fund this work" :text ("Line one
                                                  wrapped." "Line two."))))
 :nodes
 ((:id "goal" :type :work-set :children ("wire" "tools"))
  (:id "wire" :type :roadmap :title "Packet wire"
   :text "Bit-packed packets
          in every language."
   :rows (("ints" "integers") ("floats" "floats") ("unions" "tagged unions" :later t))
   :columns (("go" "go") ("cs" "cs"))
   :cells (("ints" "go" "go/ints") ("ints" "cs" "cs/ints")
           ("floats" "go" "go/floats") ("floats" "cs" "cs/floats")
           ("unions" "go" "go/unions") ("unions" "cs" "cs/unions")))
  (:id "go/ints" :type :task :title "Go integers" :state :done :evidence ("go/ints_test.go: TestInts"))
  (:id "cs/ints" :type :task :title "C# integers" :state :done :evidence ("commit 1a2b3c4"))
  (:id "go/floats" :type :work-set :children ("go/f32" "go/f64"))
  (:id "go/f32" :type :task :title "Go float32" :state :done :evidence ("go/float_test.go: TestF32"))
  (:id "go/f64" :type :task :title "Go float64" :state :unknown :note "proved by the overlay")
  (:id "cs/floats" :type :task :title "C# floats" :state :todo :sprint-card "cs-floats")
  (:id "go/unions" :type :task :title "Go unions" :state :todo)
  (:id "cs/unions" :type :task :title "C# unions" :state :doing)
  (:id "tools" :type :roadmap :title "Tools"
   :text "The command-line tools."
   :rows (("render" "render a page") ("check" "check for drift"))
   :columns (("done" "done"))
   :cells (("render" "done" "t-render") ("check" "done" "t-check")))
  (:id "t-render" :type :task :title "Render" :state :done :evidence ("tools/roadmap: TestRender"))
  (:id "t-check" :type :task :title "Check" :state :todo)))
