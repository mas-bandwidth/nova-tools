;;;; replays-8680.lisp --- nova-work v2 estimate/actual replays (nova-tools #774).
;;;;
;;;; The smallest coherent piece of #774: every node carries an estimate
;;;; (units, hours, tokens, usd; who estimated, when) and the remaining-work
;;;; forecast is a view over the open set. That is one field and one query.
;;;; The actual fold from the evidence records and the full cost view are
;;;; named as remaining in RESULT.md; these replays do not claim them.

(in-package #:nova-work/tests)

(defparameter *estimate-seed*
  '((:id "epic" :type :feature :state :doing)
    (:id "epic/t1" :type :task :parent "epic" :state :doing
     :estimate (:units 3 :hours 2 :tokens 100000 :usd 3
                :by "glenn" :when "2026-09-16T16:45Z"))
    (:id "epic/t2" :type :task :parent "epic" :state :todo
     :estimate (:units 4 :hours 3 :tokens 150000 :usd 4
                :by "glenn" :when "2026-09-16T16:45Z"))
    (:id "epic/t3" :type :task :parent "epic" :state :unknown
     :estimate (:units 2 :hours 1 :by "glenn" :when "2026-09-16T16:45Z")))
  "Three estimated leaves under one container with no estimate of its own. The
per-node estimate names its author and its date; a node without one is absent
and not zero.")

;;; ------------------------------------------------------------------
;;; node-carries-its-estimate                 SPEC-WORK.md:4651
;;; ------------------------------------------------------------------

(deftest "node-carries-its-estimate" "docs/SPEC-WORK.md:4651"
    "expected=estimate-on-the-node;who-and-when;absent-is-not-zero"
  (let ((state (make-seed-state *estimate-seed*)))
    (let ((e (nova-work::node-estimate state "epic/t1")))
      (check-equal 3 (getf e :units) "the estimate carries its unit count")
      (check-equal 2 (getf e :hours) "the estimate carries its hours")
      (check-equal 100000 (getf e :tokens) "the estimate carries its tokens")
      (check-equal 3 (getf e :usd) "the estimate carries its dollars")
      (check-equal "glenn" (getf e :by) "the estimate names who estimated")
      (check-equal "2026-09-16T16:45Z" (getf e :when) "the estimate names when"))
    (check-equal +absent+ (nova-work::node-estimate state "epic")
                 "a node without an estimate answers absent, not zero")))

;;; ------------------------------------------------------------------
;;; forecast-reads-remaining-over-o           SPEC-WORK.md:4651
;;; ------------------------------------------------------------------

(deftest "forecast-reads-remaining-over-o" "docs/SPEC-WORK.md:4651"
    "expected=remaining-hours-and-dollars;closed-leaves-the-forecast;rates-from-the-sums"
  (let ((kernel (make-kernel :state (make-seed-state *estimate-seed*) :rev-base 1)))
    ;; The whole set is open: the forecast sums every estimate over O.
    (let ((f (nova-work::forecast (kernel-state kernel))))
      (check-equal 3 (getf f :nodes) "three estimated open nodes")
      (check-equal 9 (getf f :units) "units summed over O")
      (check-equal 6 (getf f :hours) "hours summed over O")
      (check-equal 250000 (getf f :tokens) "tokens summed over O")
      (check-equal 7 (getf f :usd) "dollars summed over O")
      (check-equal 7/9 (getf f :usd-per-unit) "dollars per work unit")
      (check-equal 2/3 (getf f :hours-per-unit) "hours per work unit")
      (check-equal 28 (getf f :usd-per-mtok) "dollars per million tokens"))
    ;; Close t1: a done node leaves the remaining forecast and is not deleted.
    (multiple-value-bind (ok line) (submit kernel
                                           (list :verb :state-to-done :node "epic/t1"
                                                 :by "rowan" :reason "shipped"
                                                 :evidence '("ev-1") :request "req-8680"
                                                 :stamp "2026-09-17T00:00Z" :clock :tool
                                                 :generation-owner "gen-1"))
      (ok ok "closing t1 is accepted: ~A" line))
    (let ((f (nova-work::forecast (kernel-state kernel))))
      (check-equal 2 (getf f :nodes) "the closed node leaves the forecast")
      (check-equal 150000 (getf f :tokens) "only remaining tokens are forecast")
      (check-equal 4 (getf f :usd) "only remaining dollars are forecast"))))

;;; ------------------------------------------------------------------
;;; TestE07F03RegenerateROADMAPFromPrimaryO    SPEC-WORK.md:115
;;; ------------------------------------------------------------------
;;; Criterion E07-F03-01 "Regenerate ROADMAP from primary O data". The
;;; contract is docs/SPEC-WORK.md:115 -- "`ROADMAP.md` is regenerated, never
;;; edited" -- with O as the primary form. The roadmap table is a projection of
;;; O, so a render taken before a fact changes in O and one taken after must
;;; differ in the way O did: a settled row flips from `open` to `done` on the
;;; next render, never from a stored, hand-edited table.

(deftest "TestE07F03RegenerateROADMAPFromPrimaryO" "docs/SPEC-WORK.md:115"
    "expected=roadmap-regenerated-from-o;row-open-before-settle;row-done-after-settle"
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/f1" :type :feature :parent "root" :state :unknown)
                 (:id "root/f1/t" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f2" :type :feature :parent "root" :state :unknown)
                 (:id "root/f2/t" :type :task :parent "root/f2" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :feature
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '() :permitted-roots '() :reason "new"
                        :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    (dolist (m '("root/f1" "root/f2"))
      (multiple-value-bind (okp line code)
          (roadmap-row k :roadmap "rm" :member m :op :add :reason "row"
                       :request (format nil "rr-~A" m))
        (ok okp "adding row ~A was refused: ~A" m line)
        (check-equal 0 code "row add exit")))
    ;; Before any mutation of O the regenerated table reads every row open.
    (let ((before (roadmap-view-render (kernel-state k) "rm")))
      (ok (search "row root/f1 state=open" before)
          "row f1 was not open in the first render: ~S" before)
      (ok (search "row root/f2 state=open" before)
          "row f2 was not open in the first render: ~S" before)
      (ok (not (search "state=done" before))
          "a settled row appeared before any mutation")
      ;; A fact in O changes: the first row's task settles.
      (multiple-value-bind (okp line)
          (submit k (close-request :node "root/f1/t" :request "done-1"))
        (ok okp "settling f1 was refused: ~A" line))
      (let ((after (roadmap-view-render (kernel-state k) "rm")))
        (ok (search "row root/f1 state=done" after)
            "row f1 did not flip to done after O changed: ~S" after)
        (ok (search "row root/f2 state=open" after)
            "row f2 did not stay open: ~S" after)))))
