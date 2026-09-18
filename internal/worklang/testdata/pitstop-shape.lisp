;;; A faithful excerpt of the real work set /Users/glenn/rowan-working/work/pitstop-2026-09-17.lisp (79 units, Glenn 2026-09-18),
;;; kept here byte-for-byte in FORM so the reader is pinned against the file a coordinator actually writes. Every key that appears
;;; anywhere in the real set appears at least once below. Nothing here is amended: this is the set as it stands BEFORE the
;;; amendment, and it is the fixture rule A14 points at when it says the real set carries no :acceptance anywhere.

(work-set "pitstop-2026-09-17"
  :title "Coordination tools: from shell sketches to Go verbs with tests and review"
  :under (:open-root)
  :inputs ((:ledger "rowan-new:reports/pitstop-tests-2026-09-17.md")
           (:review "nova-tools#1263")
           (:rule "only Go with tests; a script is a one-day sketch"))
  :done-when (:all-children-closed)
  :units

  (;; --- the verbs that replace the hand steps
   (unit "verb:hygiene"   :needs () :pr 1253 :replaces "bench-hygiene.sh" :findings ("F01" "F02") :budget (:minutes 30 :tokens 120000 :model-floor :sonnet) :affinity (:bench :linux :route "deepseek-flash"))
   (unit "verb:capacity"  :needs () :pr 1251 :replaces "capacity line in the launchers")
   (unit "verb:fill"      :needs ("verb:capacity") :pr 1248 :replaces "fill-loop2.sh" :findings ("F04" "F05"))
   (unit "verb:launch"    :needs ("spec:jobs-s1") :pr 1232 :replaces "flash-native-bench.sh muse-native-bench.sh" :findings ("F06" "F07" "F08")
         :note "pull workers under leases make the launch-over-ssh shape disappear")

   ;; --- findings become tests: derived, one unit per verb, ready only after that verb's PR merges
   (unit "tests:from-findings"
     :derive (:for-each ("verb:hygiene" "verb:fill")
              :template (unit "tests:{it}" :needs ("{it}") :inputs ((:findings-of "{it}") (:ledger-row "{it}"))
                              :title "each finding of {it} is a red test in its package, then green"
                              :budget (:minutes 30 :tokens 100000 :model-floor :sonnet))))

   ;; --- landing path, the queue this set travels through
   (unit "land:1256" :pr 1256 :title "merge gate opens only the slots a group needs")
   (unit "promote:main" :needs ("land:1256") :title "dev to main when ci and certification are green at one sha")

   ;; --- repairs owned by friends
   (unit "repair:1072" :owner "Emma" :status "closed" :evidence "commit 0eecdb38; PR #1072 15/15 checks pass" :pr 1072 :title "internal/swarm green on the Windows leg")
   (unit "docs:readme" :owner "Stella" :needs ("promote:main") :pr 1298 :status :review :title "README and docs for the verbs that landed")

   ;; --- the stack
   (unit "stack:jev-seams"  :prs (913 914 1150) :spec 1211 :title "the decide seam in every choosing verb")
   (unit "stack:kube-workers" :spec 1208 :sprint :next :title "k3s workers on the Linux benches")
   (unit "spec:worklang-s2-4" :needs ("spec:worklang-s1") :cards (9308 9309 9310) :title "needs/blocks graph and readiness, plan expand, :derive and :fold")
   (unit "spec:worklang-s1" :pr 1234 :title "the bounded reader (SPEC-WORKLANG slice 1)")
   (unit "spec:jobs-s1" :pr 1232 :title "the job graph and ready set (SPEC-JOBS slice 1)")
   (unit "verb:simulate" :needs () :card 9340 :replaces "the local merge-order simulation")

   ;; --- lanes, and the units that carry one today
   (unit "lanes:fill"      :cards (9384) :lane "pulse" :title "cards belong to lanes; nova-pulse fill deals one live card per lane")
   (unit "lanes:spec"      :needs ("lanes:fill") :lane "docs" :title "SPEC-WORKLANG: a lane is a :resource of capacity 1 over an area of the tree")
   (unit "lanes:nova-work" :needs ("lanes:spec") :lane "work" :title "nova-work admits units by lane; the fleet pulls one unit per lane")
   (unit "pull:worker"     :owner "Emma" :lane "swarm" :needs ("promote:main") :deadline "2026-09-18T18:00Z" :title "benches pull work: nova-swarm pull on leases and heartbeats")
   (unit "read:worklang"   :owner "all" :deadline "2026-09-18T15:00Z" :title "structured read of SPEC-WORKLANG, SPEC-JOBS and ideas #783 before slice 2")))
