;;;; replays-8645.lisp --- the five acceptance replays of nova-tools #362.
;;;;
;;;; Each deftest is named exactly as its paragraph in docs/SPEC-WORK.md names
;;;; it, sets up the state the paragraph describes, drives the kernel's goal
;;;; model through its verbs, and asserts the outcome the paragraph promises:
;;;;
;;;;   goal-stale-update-refuses               docs/SPEC-WORK.md:5948
;;;;   goal-stop-is-a-request-not-evidence     docs/SPEC-WORK.md:5957
;;;;   goal-world-update-writes-only-existing-kinds  docs/SPEC-WORK.md:5966
;;;;   historic-tick-survives-a-source-change  docs/SPEC-WORK.md:5782
;;;;   hostile-data                            docs/SPEC-WORK.md:6248
;;;;
;;;; The pure model these drive is src/replays-8645.lisp. Where the paragraph
;;;; reaches a live session, a CLI, a snapshot or an evidence resolver, this
;;;; file exercises the part of it the kernel owns and states the reading in
;;;; RESULT.md.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; fixtures
;;; ------------------------------------------------------------------

(defun goal-world-fixture ()
  "A world at revision 5 with a goal leaf G :doing, siblings, a :todo node and
a closed node D, under the coordinator scope \"coord\"."
  (make-goal-world
   :rev 5
   :nodes '(( "G"  . (:state :doing   :branch :o))
            ( "G2" . (:state :doing   :branch :o))
            ( "T"  . (:state :todo    :branch :o))
            ( "R"  . (:state :review  :branch :o))
            ( "D"  . (:state :done    :branch :c)))
   :goals '(("coord" . "G"))))

;;; ------------------------------------------------------------------
;;; goal-stale-update-refuses                        docs/SPEC-WORK.md:5948
;;; ------------------------------------------------------------------

(deftest "goal-stale-update-refuses" "docs/SPEC-WORK.md:5948"
    "expected=stale-named;snapshot-unchanged;stop-requested-stands;closed-refused"
  (let ((w (goal-world-fixture)))
    ;; A and B both show at revision r.
    (check-equal 5 (getf (goal-world-show w :scope "coord") :rev)
                 "A and B both show at r")
    ;; A writes update --progress with the evidence triple on G, r+1.
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect 5 :form :progress
                     :progress "step" :pointer "test:run" :criterion "c1"
                     :against "sha-1" :request "reqA1")
      (ok ok "A's evidence update: ~A" line)
      (check-equal 0 code "A's update exit code")
      (check-equal 6 (goal-world-rev w) "A's update moved the revision to r+1"))
    ;; B's update --progress --expect r is refused stale, the snapshot unchanged.
    (let ((events (goal-world-events w))
          (rev (goal-world-rev w)))
      (multiple-value-bind (ok line code)
          (goal-world-update w :scope "coord" :expect 5 :form :progress
                       :progress "p" :pointer "test:run2" :criterion "c1"
                       :against "sha-2" :request "reqB1")
        (ok (null ok) "B's stale update is refused")
        (check-equal 1 code "the stale refusal is exit 1")
        (ok (search "expect=5 current=6: stale" line)
            "the refusal names expect and current: ~A" line))
      (check-equal events (goal-world-events w) "the snapshot is unchanged")
      (check-equal rev (goal-world-rev w) "the revision did not move"))
    ;; B's next show prints A's evidence row.
    (check-equal :evidence (getf (last-event w) :kind)
                 "B's next show sees A's evidence row")
    (ok (>= (getf (goal-world-show w :scope "coord") :rev) 6)
        "the show is at or after A's write")
    ;; A then update --stop --reason --expect r+1, r+2.
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect 6 :form :stop :reason "halted"
                     :request "reqA2")
      (ok ok "A's stop: ~A" line)
      (check-equal 0 code "A's stop exit code")
      (check-equal 7 (goal-world-rev w) "A's stop moved the revision to r+2"))
    ;; B's update --progress --expect r+1 is refused stale; next show stop=requested.
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect 6 :form :progress :progress "p2")
      (ok (null ok) "B's update at r+1 is refused")
      (check-equal 1 code "the refusal is exit 1")
      (ok (search "stale" line) "the refusal names stale: ~A" line))
    (check-equal "requested" (getf (goal-world-show w :scope "coord") :stop)
                 "B's next show prints stop=requested")
    ;; B's update --progress --expect r+2 is refused `stop requested`
    ;; and stop=requested stands until a :cancel with evidence or state --to doing.
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect 7 :form :progress :progress "p3")
      (ok (null ok) "B's update after the stop is refused")
      (check-equal 1 code "the stop-requested refusal is exit 1")
      (ok (search "stop requested" line) "the refusal names stop requested: ~A" line))
    (check-equal "requested" (getf (goal-world-show w :scope "coord") :stop)
                 "stop=requested stands")
    ;; A goal set to a node on the closed branch is refused disposition=done
    ;; whatever --expect says, and the node stays closed.
    (multiple-value-bind (ok line code)
        (goal-world-set w :scope "coord" :goal "D" :expect 7 :request "reqset")
      (ok (null ok) "a goal set to a closed node is refused")
      (ok (search "disposition=done" line)
          "the refusal names the disposition: ~A" line))
    (check-equal :c (gw-branch w "D") "the node stays closed")))

;;; ------------------------------------------------------------------
;;; goal-stop-is-a-request-not-evidence              docs/SPEC-WORK.md:5957
;;; ------------------------------------------------------------------

(deftest "goal-stop-is-a-request-not-evidence" "docs/SPEC-WORK.md:5957"
    "expected=stop=request-not-evidence;cancel=terminal;done-refused-no-edge"
  (let ((w (goal-world-fixture)))
    ;; goal update --stop writes GOAL OK change=stop kind=transition rev=r+1.
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect 5 :form :stop :reason "stop now"
                     :request "r1")
      (ok ok "the stop prints GOAL OK: ~A" line)
      (check-equal 0 code "the stop exit code")
      (ok (search "change=stop kind=transition rev=6" line)
          "the OK line names the change and the event kind: ~A" line))
    ;; The event is a :transition :to :cancel-requested carrying :reason and no :evidence.
    (let ((stop-event (last-event w)))
      (check-equal :transition (getf stop-event :kind) "the stop event is a :transition")
      (check-equal :cancel-requested (getf (getf stop-event :fields) :to)
                   "the transition is :to :cancel-requested")
      (ok (stringp (getf (getf stop-event :fields) :reason)) "the transition carries :reason")
      (ok (absentp (getf (getf stop-event :fields) :evidence))
          "the transition carries no :evidence")
      (ok (own-fields-ok-p stop-event) "the event carries exactly its kind's field list")
      ;; check has no finding.
      (check-equal '() (goal-check w) "check has no finding")
      ;; The same request `state --to cancel-requested --reason` writes on a sibling
      ;; is the same kind and field list.
      (let ((sib (gw-state-to w "G2" :to :cancel-requested :reason "sibling stop")))
        (check-equal :transition (getf sib :kind) "the sibling event is a :transition")
        (check-equal :cancel-requested (getf (getf sib :fields) :to)
                     "the sibling transition is :to :cancel-requested too")
        (check-equal (event-fields-keys sib) (event-fields-keys stop-event)
                     "the sibling writes the same kind and fields")))
    ;; state --to doing --reason by id is admitted (the withdrawal): stop=none.
    (gw-state-to w "G" :to :doing :reason "withdrawn")
    (check-equal "none" (getf (goal-world-show w :scope "coord") :stop)
                 "the withdrawal clears stop=requested")
    ;; Stopped again, then event --kind cancel --evidence: show prints stop=cancelled.
    (goal-world-update w :scope "coord" :expect (goal-world-rev w) :form :stop
                 :reason "again" :request "r2")
    (check-equal "requested" (getf (goal-world-show w :scope "coord") :stop)
                 "the second stop is requested")
    (gw-cancel w "G" :evidence '("ev-1"))
    (check-equal "cancelled" (getf (goal-world-show w :scope "coord") :stop)
                 "cancel with evidence is terminal: stop=cancelled")
    ;; goal set --goal G is then refused disposition=cancelled.
    (multiple-value-bind (ok line code)
        (goal-world-set w :scope "coord" :goal "G" :expect (goal-world-rev w)
                  :request "s-cancel")
      (ok (null ok) "a goal set to a cancelled goal is refused")
      (ok (search "disposition=cancelled" line) "the refusal names cancelled: ~A" line))
    ;; --stop on a :review node is refused `no edge`, as state --to cancel-requested is there.
    (goal-world-set w :scope "coord" :goal "R" :expect (goal-world-rev w) :request "s-r")
    (multiple-value-bind (ok line code)
        (goal-world-update w :scope "coord" :expect (goal-world-rev w) :form :stop
                     :reason "x" :request "r-r")
      (ok (null ok) "a stop on :review is refused")
      (ok (search "no edge" line) "the :review refusal is no edge: ~A" line))
    ;; and on a :done node the same: the edges are the table's and no other.
    (let ((d (make-goal-world
              :rev 3
              :nodes '(("D" . (:state :done :branch :c)))
              :goals '(("coord" . "D")))))
      (multiple-value-bind (ok line code)
          (goal-world-update d :scope "coord" :expect 3 :form :stop :reason "x"
                       :request "r-d")
        (ok (null ok) "a stop on :done is refused")
        (ok (search "no edge" line) "the :done refusal is no edge: ~A" line)))))

;;; ------------------------------------------------------------------
;;; goal-world-update-writes-only-existing-kinds           docs/SPEC-WORK.md:5966
;;; ------------------------------------------------------------------

(deftest "goal-update-writes-only-existing-kinds" "docs/SPEC-WORK.md:5966"
    "expected=only-transition-or-evidence;exact-kind-fields;goal-node-only"
  (let ((w (goal-world-fixture)))
    ;; --progress <text> alone on a :todo node writes :to :doing with the text as :reason.
    (goal-world-set w :scope "coord" :goal "T" :expect 5 :request "setT")
    (goal-world-update w :scope "coord" :expect (goal-world-rev w) :form :progress
                 :progress "starting")
    (let ((e (last-event w)))
      (check-equal :transition (getf e :kind) "a progress-only update is a :transition")
      (check-equal :doing (getf (getf e :fields) :to) "the transition is :to :doing")
      (check-equal "starting" (getf (getf e :fields) :reason) "the text is the :reason")
      (check-equal "T" (getf e :node) "the event is on the goal node and no other")
      (ok (own-fields-ok-p e) "exactly the transition field list"))
    ;; The same event `state --to doing --reason` writes.
    (let ((w2 (goal-world-fixture)))
      (gw-state-to w2 "T" :to :doing :reason "starting")
      (check-equal (getf (last-event w2) :kind) (getf (last-event w) :kind)
                   "the progress-only event matches state --to doing")
      (check-equal (getf (last-event w2) :fields) (getf (last-event w) :fields)
                   "and carries the same fields"))
    ;; On a :doing node, --progress alone is refused `no edge`, nothing written.
    (let ((before (goal-world-events w)))
      (multiple-value-bind (ok line code)
          (goal-world-update w :scope "coord" :expect (goal-world-rev w) :form :progress
                       :progress "again")
        (ok (null ok) "progress alone on :doing is refused")
        (ok (search "no edge" line) "the refusal is no edge: ~A" line))
      (check-equal before (goal-world-events w) "nothing was written"))
    ;; --progress with the evidence triple on a :doing node writes the :evidence event.
    (goal-world-update w :scope "coord" :expect (goal-world-rev w) :form :progress
                 :progress "e" :pointer "test:x" :criterion "c1" :against "sha")
    (let ((e (last-event w)))
      (check-equal :evidence (getf e :kind) "the triple writes an :evidence event")
      (ok (own-fields-ok-p e) "exactly the evidence field list")
      (check-equal "T" (getf e :node) "the evidence event is on the goal node"))
    ;; goal set and goal set --clear each write one :goal event with :node (:absent).
    (goal-world-set w :scope "coord" :goal "T" :expect (goal-world-rev w) :request "setT2")
    (let ((e (last-event w)))
      (check-equal :goal (getf e :kind) "goal set writes a :goal event")
      (ok (absentp (getf e :node)) "a :goal event's :node is (:absent)")
      (ok (own-fields-ok-p e) "exactly the goal field list"))
    (goal-world-set w :scope "coord" :clear t :expect (goal-world-rev w) :request "clearT")
    (let ((e (last-event w)))
      (check-equal :goal (getf e :kind) "goal set --clear writes a :goal event")
      (ok (absentp (getf e :node)) "a clear's :node is (:absent)")
      (ok (absentp (getf (getf e :fields) :goal)) "a clear's :goal is (:absent)"))
    ;; accept --add on the node moves its scope revision and goal update never does.
    (let ((w3 (goal-world-fixture)))
      (let ((s0 (goal-world-scope-rev w3)))
        (goal-world-update w3 :scope "coord" :expect 5 :form :stop :reason "s")
        (check-equal s0 (goal-world-scope-rev w3)
                     "goal update never moves the scope revision")
        (gw-accept-add w3 "G")
        (check-equal (1+ s0) (goal-world-scope-rev w3)
                     "accept --add moves the scope revision")))
    ;; A retried set with the same --request id and payload returns the same event id once.
    (let ((w4 (goal-world-fixture)))
      (goal-world-set w4 :scope "coord" :goal "G" :expect 5 :request "same")
      (let ((n1 (length (goal-world-events w4)))
            (rev1 (goal-world-rev w4)))
        (goal-world-set w4 :scope "coord" :goal "G" :expect 5 :request "same")
        (check-equal n1 (length (goal-world-events w4))
                     "the retried set writes no second event")
        (check-equal rev1 (goal-world-rev w4) "the retried set moves no revision")))))

;;; ------------------------------------------------------------------
;;; historic-tick-survives-a-source-change           docs/SPEC-WORK.md:5782
;;; ------------------------------------------------------------------

(deftest "historic-tick-survives-a-source-change" "docs/SPEC-WORK.md:5782"
    "expected=historic-tick=pinned,current=reverify"
  (let ((tick (make-tick-record :id "r1" :pinned-rev 7 :source-sha "sha-A"
                                :scope "feat1" :historic-tick t
                                :current-verification :verified)))
    ;; A changed source preserves the historic tick at its pinned revision while
    ;; the current view requires re-verification.
    (let ((after (source-change tick :new-source-sha "sha-B")))
      (check-equal 7 (tick-record-pinned-rev after)
                   "the historic tick stays at its pinned revision")
      (check-equal t (tick-record-historic-tick after) "the historic tick survives")
      (check-equal "sha-A" (tick-record-source-sha after)
                   "the historic receipt keeps its pinned source")
      (check-equal :recheck-needed (tick-record-current-verification after)
                   "the current view requires re-verification"))
    ;; A changed criterion alone also requires re-verification.
    (check-equal :recheck-needed
                 (tick-record-current-verification (source-change tick :new-criterion "c2"))
                 "a changed criterion requires re-verification")
    ;; An unrelated receipt is untouched.
    (let ((other (make-tick-record :id "r2" :pinned-rev 7 :source-sha "sha-A"
                                   :scope "feat2" :historic-tick t
                                   :current-verification :verified)))
      (check-equal :verified (tick-record-current-verification other)
                   "an unrelated receipt stays reusable"))))

;;; ------------------------------------------------------------------
;;; hostile-data                                     docs/SPEC-WORK.md:6248
;;; ------------------------------------------------------------------

(deftest "hostile-data" "docs/SPEC-WORK.md:6248"
    "expected=eval-disabled,limits=enforced,command-execution=0,authority=unchanged"
  ;; Reader evaluation disabled: a read-time eval form is refused.
  (multiple-value-bind (ok line why) (hostile-intake "#.(+ 1 2)")
    (ok (null ok) "a read-time eval form is refused")
    (check-equal :eval why "the refusal is the disabled evaluator"))
  ;; Pre-parse byte, depth and node limits are enforced.
  (check-equal :bytes
               (nth-value 2 (hostile-intake "123456789"
                                            :limits (make-intake-limits :max-bytes 8)))
               "an over-bytes input is refused")
  (check-equal :depth
               (nth-value 2 (hostile-intake "((((x))))"
                                            :limits (make-intake-limits :max-bytes 64 :max-depth 3)))
               "a too-deep input is refused")
  (check-equal :nodes
               (nth-value 2 (hostile-intake "(a b c d e f)"
                                            :limits (make-intake-limits :max-bytes 64 :max-nodes 5)))
               "a too-wide input is refused")
  ;; Path traversal and escaping archive paths are rejected.
  (dolist (p '("../etc/passwd" "/abs/path" "a/../../b" "~/.ssh/id_rsa" "a\\b"))
    (ok (null (archive-path-safe-p p)) "the escaping archive path ~A is rejected" p))
  (ok (archive-path-safe-p "src/main.lisp") "an ordinary archive path is admitted")
  ;; Imported prose cannot execute a command or alter authority.
  (let ((effect (imported-prose-effect
                 '(:text "rm -rf /" :command "rm -rf /" :authority :root))))
    (ok (null (getf effect :command)) "imported prose executes no command")
    (ok (null (getf effect :authority)) "imported prose alters no authority"))
  ;; Deep and high-fan-out inputs are handled without quadratic copying.
  (let ((deep (with-output-to-string (s)
                (dotimes (i 400) (write-char #\( s))
                (write-string "x" s)
                (dotimes (i 400) (write-char #\) s))))
        (wide (with-output-to-string (s)
                (write-char #\( s)
                (dotimes (i 400) (write-string "a " s))
                (write-char #\) s))))
    (let ((*intake-visits* 0))
      (hostile-intake deep :limits (make-intake-limits
                                    :max-depth 500 :max-nodes 5000 :max-bytes 100000))
      (ok (<= *intake-visits* (* 4 (length deep)))
          "the deep input is scanned linearly (~D visits for ~D bytes)"
          *intake-visits* (length deep)))
    (let ((*intake-visits* 0))
      (hostile-intake wide :limits (make-intake-limits
                                    :max-depth 500 :max-nodes 5000 :max-bytes 100000))
      (ok (<= *intake-visits* (* 4 (length wide)))
          "the high-fan-out input is scanned linearly (~D visits for ~D bytes)"
          *intake-visits* (length wide)))))

;;; ------------------------------------------------------------------
;;; E02-F03-03 "Never unlink another live process lock or endpoint"
;;; (docs/roadmaps/nova-work.sexp:619) — docs/SPEC-WORK.md:166:
;;; a start that cannot take the journal lock refuses and "never unlinks
;;; another process's lock"; release keeps the `<journal>.lock` file too,
;;; so a next taker can still name the holder and the file is never
;;; removed under anyone (the endpoint half is SPEC-WORK.md:174-175,
;;; where only a socket whose own lock is free and answers nothing is
;;; unlinked).
;;; ------------------------------------------------------------------

(deftest "TestE02F03NeverUnlinkAnotherLiveProcess" "docs/SPEC-WORK.md:166"
    "expected=refused-taker-never-unlinks-the-lock-file;release-never-unlinks-it"
  (let* ((path (test-journal-path "never-unlink"))
         (lock-path (concatenate 'string path ".lock"))
         (foreign-lock nil))
    (unwind-protect
         (progn
           ;; A first holder takes the journal lock and its lock file appears.
           (setf foreign-lock (take-journal-lock path :socket "holder.sock"))
           (ok foreign-lock "a first holder takes the journal lock")
           (ok (probe-file lock-path)
               "the holder's lock file exists while the lock is held")
           ;; A second, refused taker must not unlink the holder's lock file.
           (ok (null (take-journal-lock path :socket "wanna-be.sock"))
               "a second taker is refused the held lock")
           (ok (probe-file lock-path)
               "the refused taker left the holder's lock file in place")
           ;; Releasing the lock leaves the file behind, never unlinks it.
           (release-journal-lock foreign-lock)
           (ok (probe-file lock-path)
               "releasing the lock leaves the lock file (never unlinked)"))
      (release-journal-lock foreign-lock)
      (ignore-errors (delete-file path))
      (ignore-errors (delete-file lock-path)))))
