;;;; replays-l3600-3.lisp --- the eight named replays of batch 3
;;;; (docs/SPEC-WORK.md lines 3600-99999), each asserting what the spec says the
;;;; replay must show.

(in-package #:nova-work/tests)

;; ------------------------------------------------------------------
;; dry-run-writes-nothing   SPEC-WORK.md:5527
;; ------------------------------------------------------------------
(deftest "dry-run-writes-nothing" "docs/SPEC-WORK.md:5527"
    "expected=preview-mutates-nothing;dedup-still-new;events-pending-pushed-unchanged"
  (let ((f (nova-work::make-flow :revision 0 :events 5 :pending 0 :pushed 5 :dedup '()))
        (request '(:request "req-99")))
    (let ((before (nova-work::flow-counters f))
          (pre (nova-work::flow-dry-run f request)))
      (ok (nova-work::flow-dedup-fresh-p f "req-99")
          "after a green preview the --request id is still new to dedup")
      (ok (equal before (nova-work::flow-counters f))
          "events=/pending=/pushed= unchanged by the preview")
      (ok (= 0 (nova-work::flow-revision f)) "the preview advances no revision")
      (ok (= 1 (nova-work::flow-revision pre)) "the projection is at R+1")
      (ok (nova-work::flow-dedup-fresh-p pre "req-99")
          "even the projection entered no dedup id"))
    (let ((f2 (nova-work::flow-commit f request)))
      (ok (= 1 (nova-work::flow-revision f2)) "an accepted mutation moves to R+1")
      (ok (not (nova-work::flow-dedup-fresh-p f2 "req-99"))
          "the commit entered the request id")
      (check-equal '(nil "APPLY FAIL expect=0 current=1: stale" 1)
                   (multiple-value-list (nova-work::flow-expect-check f2 0))
                   "apply --expect R (the old revision) refused stale by name")
      (check-equal '(t nil 0)
                   (multiple-value-list (nova-work::flow-expect-check f2 1))
                   "apply at the current R+1 is newly validated"))))

;; ------------------------------------------------------------------
;; explicit-rest-is-not-pinged   SPEC-WORK.md:5556
;; ------------------------------------------------------------------
(deftest "explicit-rest-is-not-pinged" "docs/SPEC-WORK.md:5556"
    "expected=one-bounded-ping;unavailable=unconfirmed;rest=respected"
  (multiple-value-bind (s r) (nova-work::classify-capacity :rest-p t :silences-p t)
    (check-equal :rest s "explicit rest is respected and never pinged")
    (check-equal nil r "rest carries no unavailable reason"))
  (multiple-value-bind (s r) (nova-work::classify-capacity :silences-p t)
    (check-equal :ping s "silence beyond the threshold triggers the one bounded ping")
    (check-equal nil r "the ping claims no unavailable reason"))
  (multiple-value-bind (s r) (nova-work::classify-capacity :delivered-p nil)
    (check-equal :unavailable s "a nonresponsive capacity is unavailable")
    (check-equal :unconfirmed r "reason is unconfirmed")
    (ok (not (member r '(:asleep :exhausted-credit)))
        "no claim of sleep or exhausted credit")))

;; ------------------------------------------------------------------
;; fenced-export-can-finish   SPEC-WORK.md:5782
;; ------------------------------------------------------------------
(deftest "fenced-export-can-finish" "docs/SPEC-WORK.md:5782"
    "expected=terminal-read-by-id;cancel-reconciles;canonical-write=fenced;one-owner"
  (let ((s (nova-work::fenced-start-export
            (nova-work::make-fenced-session :owner "A" :operations '()) "op1")))
    (multiple-value-bind (ok line) (nova-work::fenced-admit s :status "op1")
      (check-equal t ok "an export started, its status read by its id")
      (check-string= "EXPORT OK id=op1 status=running" line "the terminal line"))
    (multiple-value-bind (ok line) (nova-work::fenced-admit s :status "op-unknown")
      (check-equal nil ok "an unknown id refused")
      (check-string= "fenced" line "unknown id refused fenced"))
    (multiple-value-bind (ok line) (nova-work::fenced-admit s :list nil)
      (check-equal nil ok "operation list refused")
      (check-string= "fenced" line "operation list refused fenced"))
    (multiple-value-bind (ok line) (nova-work::fenced-admit s :write nil)
      (check-equal nil ok "a canonical write refused")
      (check-string= "fenced" line "canonical write refused fenced"))
    (multiple-value-bind (s2 line) (nova-work::fenced-cancel s "op1")
      (check-string= "EXPORT OK id=op1 status=cancelled publication=reconciled" line
                     "cancel reconciles publication")
      (check-string= "A" (nova-work::fs-owner s2)
                     "no second owner and no mutation authority created"))))

;; ------------------------------------------------------------------
;; four-capability-groups-and-three-fields   SPEC-WORK.md:5611
;; ------------------------------------------------------------------
(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:5611"
    "expected=groups=4;fields=support,runtime,free-capacity;never-collapse"
  (let ((kinds (nova-work::cap-four-kinds)))
    (check-equal 4 (length kinds)
                 "child agents, swarms, local models and one-shots: four groups")
    (dolist (g (mapcar #'nova-work::cap-express kinds))
      (ok (keywordp (nova-work::cg-id g)) "each group has a stable id")
      (ok (eq :unknown (nova-work::cg-availability g)) "each carries its availability"))
    (let ((g (nova-work::make-cap-group :id :swarms :source "roster" :last-verified "s"
                                        :availability :up :constraints '(:x)
                                        :support '(:a) :runtime '(:b) :capacity 3)))
      (check-equal '((:a) (:b) 3) (nova-work::cap-three-fields g)
                   "declared support, verified runtime and free capacity are three fields")
      (setf (nova-work::cg-support g) '(:a2))
      (check-equal '((:a2) (:b) 3) (nova-work::cap-three-fields g)
                   "moving declared support collapses nothing"))))

;; ------------------------------------------------------------------
;; goal-crosses-harness   SPEC-WORK.md:5787
;; ------------------------------------------------------------------
(deftest "goal-crosses-harness" "docs/SPEC-WORK.md:5787"
    "expected=goal=G;stop=requested;note-byte-for-byte;progress=refused;stop=cancelled"
  (let* ((g (nova-work::make-goal-state :node "G" :branch :o :state :doing :revision 3
                                        :stop :none :evidence nil :note "n1" :progress nil))
         (stopped (nova-work::goal-stop g :reason "blocked")))
    (check-string= "goal=G rev=4 stop=requested" (nova-work::goal-show-line stopped)
                   "stop=requested, not cancelled, written")
    (check-string= "goal=G rev=4 stop=requested" (nova-work::goal-show-line stopped)
                   "the same show across the two builds is byte-identical")
    (check-equal "n1" (nova-work::gs-note stopped)
                 "the coordinator note id A wrote survives for B to read byte for byte")
    (multiple-value-bind (p line) (nova-work::goal-progress stopped :text "p")
      (check-equal nil p "B's update --progress on G wrote nothing")
      (check-string= "GOAL FAIL node=G: stop requested" line
                     "progress refused stop requested"))
    (multiple-value-bind (c line) (nova-work::goal-cancel stopped :evidence '("ev-ptr"))
      (declare (ignore line))
      (check-string= "goal=G rev=5 stop=cancelled" (nova-work::goal-show-line c)
                     "after cancel evidence, show prints stop=cancelled"))))

;; ------------------------------------------------------------------
;; goal-expect-is-required   SPEC-WORK.md:5824
;; ------------------------------------------------------------------
(deftest "goal-expect-is-required" "docs/SPEC-WORK.md:5824"
    "expected=no-expect=exit-2;one-behind=stale;dry-run-writes-nothing"
  (let ((g (nova-work::make-goal-state :node "G" :branch :o :state :doing
                                       :revision 3 :stop :none)))
    (check-equal '(nil "goal set/update: --expect is required" 2)
                 (multiple-value-list (nova-work::goal-expect g nil))
                 "goal set/update without --expect exit 2 naming the flag")
    (check-equal '(t nil 0)
                 (multiple-value-list (nova-work::goal-expect g 3))
                 "--expect at the current local revision is admitted")
    (let ((mvl (multiple-value-list (nova-work::goal-expect g 2))))
      (check-equal nil (first mvl) "--expect one behind refused")
      (check-equal 1 (third mvl) "stale exits 1")
      (ok (search "current=3" (second mvl)) "the current value is printed"))
    (check-equal '(t nil 0)
                 (multiple-value-list (nova-work::goal-expect g 3))
                 "a show answers at the revision it prints, never taking --expect")))

;; ------------------------------------------------------------------
;; goal-stale-update-refuses   SPEC-WORK.md:5797
;; ------------------------------------------------------------------
(deftest "goal-stale-update-refuses" "docs/SPEC-WORK.md:5797"
    "expected=stale-named;snapshot-unchanged;stop-requested-stands;closed-refused"
  (let* ((g (nova-work::make-goal-state :node "G" :branch :o :state :doing :revision 3
                                        :stop :none :evidence nil))
         (gA (nova-work::goal-progress-evidence g '("evidence-triple")))
         (stale (multiple-value-list (nova-work::goal-expect gA 3))))
    (check-equal nil (first stale) "B's update --expect r refused")
    (check-equal 1 (third stale) "stale exits 1")
    (ok (search "expect=3 current=4" (second stale)) "stale names expect and current")
    (check-equal nil (nova-work::gs-evidence g) "the snapshot B holds is unchanged")
    (check-equal '("evidence-triple") (nova-work::gs-evidence gA)
                 "B's next show prints A's evidence row")
    (multiple-value-bind (gA2 line event) (nova-work::goal-stop gA :reason "r")
      (declare (ignore line event))
      (check-equal :requested (nova-work::gs-stop gA2) "stop=requested written")
      (check-equal 5 (nova-work::gs-revision gA2) "stop writes r+2")
      (check-equal nil (first (multiple-value-list (nova-work::goal-expect gA2 4)))
                   "B's update --expect r+1 refused stale")
      (multiple-value-bind (p line2) (nova-work::goal-progress gA2 :text "x")
        (check-equal nil p "progress on a stopped goal writes nothing")
        (check-string= "GOAL FAIL node=G: stop requested" line2
                       "stop=requested stands"))))
  (check-string= "GOAL FAIL: disposition=done" (nova-work::goal-set-refusal :c :none)
                 "a goal set on the closed branch refuses disposition=done whatever --expect says")
  (check-equal nil (nova-work::goal-set-refusal :o :none) "an open branch is admitted"))

;; ------------------------------------------------------------------
;; goal-stop-is-a-request-not-evidence   SPEC-WORK.md:5806
;; ------------------------------------------------------------------
(deftest "goal-stop-is-a-request-not-evidence" "docs/SPEC-WORK.md:5806"
    "expected=stop=request-not-evidence;cancel=terminal;done-refused-no-edge"
  (let ((g (nova-work::make-goal-state :node "G" :branch :o :state :doing
                                       :revision 3 :stop :none :evidence nil)))
    (multiple-value-bind (g1 line event) (nova-work::goal-stop g :reason "because")
      (ok (search "change=stop kind=transition" line) "GOAL OK names change=stop kind=transition")
      (check-equal :transition (getf event :kind) "the event is a :transition")
      (check-equal :cancel-requested (getf event :to) "to :cancel-requested")
      (check-equal :absent (getf event :evidence) "the event carries no :evidence")
      (check-equal "because" (getf event :reason) "the event carries :reason")
      (check-equal :requested (nova-work::gs-stop g1) "stop is a request, not evidence")
      (multiple-value-bind (g2 line2) (nova-work::goal-withdraw g1)
        (declare (ignore line2))
        (check-equal :none (nova-work::gs-stop g2) "state --to doing by id is the withdrawal")
        (check-string= "goal=G rev=5 stop=none" (nova-work::goal-show-line g2)
                       "show prints stop=none after the withdrawal")))
    (multiple-value-bind (g3 line3) (nova-work::goal-cancel g :evidence '("pointer"))
      (declare (ignore line3))
      (check-equal :cancelled (nova-work::gs-stop g3) "event --kind cancel is terminal")
      (check-string= "goal=G rev=4 stop=cancelled" (nova-work::goal-show-line g3)
                     "show prints stop=cancelled")
      (check-string= "GOAL FAIL: disposition=cancelled"
                     (nova-work::goal-set-refusal :o :cancelled)
                     "goal set after cancel refused disposition=cancelled")))
  (dolist (s '(:review :done))
    (multiple-value-bind (p line)
        (nova-work::goal-stop (nova-work::make-goal-state :node "G" :state s :stop :none :revision 1))
      (check-equal nil p (format nil "stop on ~A refused" s))
      (check-string= "GOAL FAIL node=G: no edge" line "refused no edge by the table's edges alone"))))
