;;; A cut of work/pitstop-2026-09-17.lisp (Glenn 2026-09-18), taken VERBATIM: every unit
;;; below is a line copied out of the real set, comments, key soup and defects included.
;;; It is cut rather than invented so that what this package proves it reads is what a
;;; coordinator actually writes -- :pr, :budget, :affinity, :findings, :evidence and
;;; :replaces are keys no reader here knows, and a `#` inside a string or a comment is
;;; text, not a dispatch macro.
;;;
;;; The two absent needs are the real set's own, not seeded here: "repair:spec-1208-1209"
;;; (the real file defines repair:spec-1168-1169) and "merge:simulate" (it defines
;;; verb:simulate). They are why this fixture exists.

(work-set "pitstop-cut"
  :title "Coordination tools: from shell sketches to Go verbs with tests and review"
  :under (:open-root)
  :inputs ((:ledger "rowan-new:reports/pitstop-tests-2026-09-17.md")
           (:review "nova-tools#1263")            ; Emma 5, Johnny /tmp, Stella 27 findings by exact head
           (:rule "only Go with tests; a script is a one-day sketch"))
  :done-when (:all-children-closed)
  :units

  (;; the real file opens its :units list with a comment block and the first unit on the
;; same line as the paren; both are text to the reader and neither moves a unit.
(unit "verb:hygiene"   :needs () :pr 1253 :replaces "bench-hygiene.sh" :findings ("F01" "F02") :budget (:minutes 30 :tokens 120000 :model-floor :sonnet) :affinity (:bench :linux :route "deepseek-flash"))
   (unit "verb:capacity"  :needs () :pr 1251 :replaces "capacity line in the launchers")
   (unit "verb:fill"      :needs ("verb:capacity") :pr 1248 :replaces "fill-loop2.sh" :findings ("F04" "F05"))
   (unit "repair:1072" :owner "Emma" :status "closed" :evidence "commit 0eecdb38; PR #1072 15/15 checks pass" :pr 1072 :title "internal/swarm green on the Windows leg")
   (unit "repair:spec-1168-1169" :owner "Stella" :title "the three spec repairs at an exact head; dependents held")
   (unit "stack:redis-live"     :needs ("repair:spec-1208-1209") :spec 1209 :title "Redis on space holds the live state: leases, capacity, presence, the ready set as streams with consumer groups")
   (unit "batch:verb"           :needs ("batch:land" "merge:simulate") :lane "merge" :title "`nova-merge batch` is integrate.sh as a verb: gate against current dev, member list, reasons for drops, closes members on land")
   (unit "batch:land"           :needs ("promote:main") :lane "merge" :title "landing is by integration batches only: merge onto current dev on hulk, full suite, drop red members with the reason, one PR through the queue; integration-1 #1302")
   (unit "promote:main" :needs ("land:1261") :title "dev to main when ci and certification are green at one sha; TOOLS MOVED note; friends adopt and dogfood; Stella README")
   (unit "land:1261" :needs ("land:1260") :pr 1261 :title "any Mac takes the darwin leg; frees the Studio for dev CI")
   (unit "land:1260" :pr 1260 :title "fixture linked not copied; atomic writes")
   (unit "pull:queue"           :owner "Stella" :lane "work" :needs ("promote:main") :deadline "2026-09-18T18:00Z" :title "the ready set in Redis with lanes as a key and a depth ceiling; nova-work push feeds it (#1307 base)")
   (unit "lisp:isolation"       :owner "Stella" :lane "work" :title "nova-work acceptance suite: one private root per suite process; journals, exports, sockets below it; no deletion outside it (stopgap #1301 landed)")
   (unit "docs:readme" :owner "Stella" :needs ("promote:main") :pr 1298 :status :review :title "README and docs for the verbs that landed")))
