;;; The same excerpt of the real work set, with the amendment's keys added: :resources (a lane as a resource of capacity 1, and the
;;; rest of the vector), :writes, :tools, :collects, :warm, :attempts, :state and :acceptance. This fixture is the proof of rule A1:
;;; the file a coordinator writes today still reads after the amendment, because every new key is additive and nothing moved.

(work-set "pitstop-2026-09-17"
  :title "Coordination tools: from shell sketches to Go verbs with tests and review"
  :under (:open-root)
  :inputs ((:ledger "rowan-new:reports/pitstop-tests-2026-09-17.md")
           (:review "nova-tools#1263")
           (:rule "only Go with tests; a script is a one-day sketch"))
  :done-when (:all-children-closed)
  :units

  ((unit "verb:hygiene"
     :needs () :pr 1253 :replaces "bench-hygiene.sh" :findings ("F01" "F02")
     :owner "swarm:flash"
     :resources ((:lane "pulse") (:cpu 2) (:memory-gb 4) (:disk-gb 10))
     :writes ("cmd/nova-pulse/" "internal/pulse/hygiene.go")
     :tools ((:nova-pulse :at "0.9.2") (:go :at "1.26"))
     :collects ((:name "receipts" :under "out/receipts" :members :unknown-before-run))
     :warm (:retained ((:worktree "mas-bandwidth/nova-tools")) :active ((:cpu 2)))
     :acceptance ((:id "a1" :kind :test :subject "test:internal/pulse@HEAD" :predicate :passes))
     :attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T09:10Z"
                 :outcome :red :proof (:kind :exit :value "1")))
     :budget (:minutes 30 :tokens 120000 :model-floor :sonnet)
     :affinity (:bench :linux :route "deepseek-flash"))

   (unit "verb:fill"
     :needs ("verb:capacity") :pr 1248 :replaces "fill-loop2.sh" :findings ("F04" "F05")
     :owner "Emma"
     :resources ((:lane "pulse") (:cpu 2) (:class "darwin-runner" :n 0))
     :writes ("internal/pulse/fill.go")
     :acceptance ((:id "a1" :kind :test :subject "test:internal/pulse/fill@HEAD" :predicate :passes))
     :state :uncertain
     :attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T10:02Z" :outcome :uncertain)))

   (unit "verb:capacity" :needs () :pr 1251 :replaces "capacity line in the launchers"
     :resources ((:lane "pulse")) :writes ("internal/pulse/capacity.go")
     :acceptance ((:id "a1" :kind :test :subject "test:internal/pulse/capacity@HEAD" :predicate :passes))
     :state :closed
     :attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T08:00Z"
                 :outcome :green :proof (:kind :merged :value "pr:1251"))))

   (unit "lanes:spec" :needs ("verb:capacity") :lane "docs"
     :owner "child:opus"
     :resources ((:lane "docs"))
     :writes ("docs/SPEC-WORKLANG.md" "docs/SPEC-JOBS.md")
     :tools ((:nova-work :at "0.4.0" :key "worklang-reader-2026-09-18"))
     :acceptance ((:id "a1" :kind :test :subject "test:internal/docs@HEAD" :predicate :passes))
     :title "a lane is a :resource of capacity 1 over an area of the tree")

   (unit "read:worklang" :owner "all" :deadline "2026-09-18T15:00Z"
     :acceptance ((:id "a1" :kind :attested :subject "read:worklang" :predicate :attested-by))
     :title "structured read of SPEC-WORKLANG, SPEC-JOBS and ideas #783 before slice 2")))
