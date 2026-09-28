;;; A work set in the SPEC-WORKLANG grammar, cut from work/pitstop-2026-09-17.lisp so the
;;; reader here is held to the file a coordinator actually writes: the comments, the
;;; symbols, the nested budget forms, the short RFC3339 stamps, and NO :acceptance on any
;;; unit -- which is the shape that refused a real ask on 2026-09-18.

(work-set "pitstop-2026-09-17"
  :title "Coordination tools: from shell sketches to Go verbs with tests and review"
  :under (:open-root)
  :done-when (:all-children-closed)
  :units

  ((unit "verb:hygiene" :needs () :pr 1253 :replaces "bench-hygiene.sh"
         :budget (:minutes 30 :tokens 120000 :model-floor :sonnet)
         :affinity (:bench :linux :route "deepseek-flash"))

   (unit "pull:queue" :owner "Stella" :lane "work" :needs ("promote:main")
         :deadline "2026-09-18T18:00Z"
         :title "the ready set in Redis with lanes as a key and a depth ceiling; nova-work push feeds it (#1307 base)")

   (unit "docs:readme" :owner "Stella" :needs ("promote:main") :pr 1298 :status :review
         :lane "docs"
         :title "README and docs for the verbs that landed")))
