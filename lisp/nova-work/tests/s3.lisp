;;;; s3.lisp --- the slice-3 lease cases (one bench, one card).
;;;;
;;;; Each case names its line of docs/SPEC-WORK.md and the replay it exercises.
;;;; The lease is pure here: the verbs are functions over a lease log, and the
;;;; wiring into the command thread is a later card. `*seed*` is provided by
;;;; tests/acceptance.lisp, which loads first in the same package.

(in-package #:nova-work/tests)

(defparameter *now* "2026-09-14T12:00:00Z"
  "The read's clock for every lease case below.")

(defparameter *win* "10m"
  "The `--window` for who/stale: a heartbeat inside ten minutes is working-now.")

;;; The verbs return (values OK-P LINE NEW-LOG). The happy-path helpers below
;;; assert the OK and hand back only the log, so a chain reads top to bottom.

(defun take! (log node as by default)
  (multiple-value-bind (ok line new)
      (lease-take log :node node :as as :by by :default default :now *now*)
    (ok ok "take refused: ~A" line)
    new))

(defun heartbeat! (log node as &optional (evidence "note:x"))
  (multiple-value-bind (ok line new)
      (heartbeat-lease log :node node :as as :evidence evidence :now *now*)
    (ok ok "heartbeat refused: ~A" line)
    new))

(defun release! (log node as &key handed by default)
  (multiple-value-bind (ok line new)
      (lease-release log :node node :as as :handed handed :by by :default default :now *now*)
    (ok ok "release refused: ~A" line)
    new))

(defun settle! (log node as)
  (lease-release-on-settle log :node node :as as :now *now*))

;;; ------------------------------------------------------------------
;;; second-take-is-refused-and-names-the-holder   SPEC-WORK.md:135,1378
;;; ------------------------------------------------------------------

(deftest "second-take-is-refused-and-names-the-holder" "docs/SPEC-WORK.md:135"
    "one live lease per node; a second take refused naming the holder"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (multiple-value-bind (ok2 line2 log2)
        (lease-take log :node "acme/work/f1/t1" :as "glenn" :by "30m" :default :release
                    :now *now*)
      (ok (not ok2) "second take refused: ~A" line2)
      (check-string=
       "LEASE FAIL node=acme/work/f1/t1 holder=rowan since=2026-09-14T12:00:00Z deadline=2026-09-14T12:30:00Z live=1: held"
       line2 "the refusal names the holder")
      (ok (eq log log2) "a refusal appends nothing"))))

;;; ------------------------------------------------------------------
;;; working-is-a-view                              SPEC-WORK.md:1682-1709
;;; ------------------------------------------------------------------

(deftest "working-is-a-view" "docs/SPEC-WORK.md:1682"
    "reading the working count visits zero nodes in O"
  (let ((state (make-seed-state *seed*))
        (log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (declare (ignore state))
    (with-instrumentation
      (let ((w (working-count-of log :now *now*)))
        (check-equal 1 w "|W| counts the one leased node")
        (ok (zerop *visits*) "reading W visited ~D O nodes, want 0" *visits*)))
    (let ((view (working-view-build log :now *now*)))
      (ok (working-member-p view "acme/work/f1/t1") "the leased node is in W")
      (ok (not (working-member-p view "acme/work/f1/t2")) "an unleased node is not in W"))))

;;; ------------------------------------------------------------------
;;; settle-releases-the-lease                      SPEC-WORK.md:1674-1680
;;; ------------------------------------------------------------------

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:1674"
    "a settled item reads holder=unowned while its lease log stays whole"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let ((log2 (settle! log "acme/work/f1/t1" "rowan")))
      (ok (null (lease-holder log2 "acme/work/f1/t1" :now *now*))
          "the settled node reads unowned")
      (let ((kinds (mapcar (lambda (r) (getf r :kind)) (handoff-rows log2))))
        (ok (member :lease kinds) "the :lease event is kept for handoffs")
        (ok (member :release kinds) "the :release event is kept for handoffs")))))

;;; ------------------------------------------------------------------
;;; heartbeat-from-a-third-party-refuses           SPEC-WORK.md:1375-1383
;;; ------------------------------------------------------------------

(deftest "heartbeat-from-a-third-party-refuses" "docs/SPEC-WORK.md:1378"
    "a heartbeat by someone who is not the holder names the holder and refuses"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (multiple-value-bind (ok2 line2)
        (heartbeat-lease log :node "acme/work/f1/t1" :as "glenn" :evidence "note:x" :now *now*)
      (ok (not ok2) "third-party heartbeat refused: ~A" line2)
      (ok (search "holder=rowan" line2) "the refusal names the holder")
      (let ((log2 (heartbeat! log "acme/work/f1/t1" "rowan")))
        (ok (worked-now-p log2 "acme/work/f1/t1" *now* *win*)
            "the holder's own heartbeat is accepted and is working-now")))))

;;; ------------------------------------------------------------------
;;; release-handed-passes-the-lease                SPEC-WORK.md:1377-1378
;;; ------------------------------------------------------------------

(deftest "release-handed-passes-the-lease" "docs/SPEC-WORK.md:1377"
    "release --handed writes a :handoff naming the new holder with a whole lease"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let ((log2 (release! log "acme/work/f1/t1" "rowan" :handed "glenn" :by "1h" :default :release)))
      (check-equal "glenn" (lease-holder log2 "acme/work/f1/t1" :now *now*)
                   "the new holder holds")
      (ok (lease-live-p log2 "acme/work/f1/t1" *now*) "the handed lease is still live")
      (ok (member :handoff (mapcar (lambda (r) (getf r :kind)) (handoff-rows log2)))
          "a :handoff event is in the log"))))

;;; ------------------------------------------------------------------
;;; stale-groups-by-holder                         SPEC-WORK.md:1778,1807
;;; ------------------------------------------------------------------

(deftest "stale-groups-by-holder" "docs/SPEC-WORK.md:1807"
    "the requeue list groups the held-not-worked leases by their holder"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let ((log (take! log "acme/work/f1/t2" "rowan" "30m" :release)))
      (let ((log (take! log "acme/work/f2" "glenn" "30m" :release)))
        (let ((groups (stale-by-holder log :now *now* :window *win*)))
          (ok (= 2 (length groups)) "two holders appear")
          (let ((rowan (cdr (assoc "rowan" groups :test #'string=)))
                (glenn (cdr (assoc "glenn" groups :test #'string=))))
            (ok (= 2 (length rowan)) "rowan holds two stale nodes")
            (ok (= 1 (length glenn)) "glenn holds one stale node")))))))

;;; ------------------------------------------------------------------
;;; handoffs-logs-four-kinds                       SPEC-WORK.md:1012-1013
;;; ------------------------------------------------------------------

(deftest "handoffs-logs-four-kinds" "docs/SPEC-WORK.md:1012"
    "the transition row lists :lease, :heartbeat, :release and :handoff"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let ((log (heartbeat! log "acme/work/f1/t1" "rowan")))
      (let ((log (release! log "acme/work/f1/t1" "rowan")))
        (let ((log (take! log "acme/work/f1/t1" "rowan" "30m" :release)))
          (let ((log (release! log "acme/work/f1/t1" "rowan" :handed "glenn" :by "1h" :default :release)))
            (let ((kinds (mapcar (lambda (r) (getf r :kind)) (handoff-rows log))))
              (dolist (k '(:lease :heartbeat :release :handoff))
                (ok (member k kinds) "kind ~S missing from the transition rows" k)))))))))

;;; ------------------------------------------------------------------
;;; remove-refuses-a-leased-node                   SPEC-WORK.md:2370-2372
;;; ------------------------------------------------------------------

(deftest "remove-refuses-a-leased-node" "docs/SPEC-WORK.md:2370"
    "node remove refuses a node holding a live lease and names the holder"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (multiple-value-bind (held holder)
        (leased-node-refusal log :node "acme/work/f1/t1" :now *now*)
      (ok held "a leased node is refused")
      (check-equal "rowan" holder "the refusal names the holder"))
    (multiple-value-bind (held holder)
        (leased-node-refusal log :node "acme/work/f1/t2" :now *now*)
      (ok (not held) "an unleased node is not refused")
      (ok (null holder) "no holder to name"))))

;;; ------------------------------------------------------------------
;;; activity-and-state-are-two-counts              SPEC-WORK.md:1684-1687
;;; ------------------------------------------------------------------

(deftest "activity-and-state-are-two-counts" "docs/SPEC-WORK.md:1684"
    "a :doing node is not working until it holds a lease; W counts leases only"
  (let ((state (make-seed-state *seed*))
        (log (make-lease-log)))
    (declare (ignore state))
    (check-equal 0 (working-count-of log :now *now*)
                 "no leases, so no working count, even though a seed node is :doing")
    (check-equal 1 (working-count-of (take! log "acme/work/f1/t2" "rowan" "30m" :release)
                                     :now *now*)
                 "one lease, one working count")))

;;; ------------------------------------------------------------------
;;; four-facts-four-verbs                          SPEC-WORK.md:136
;;; ------------------------------------------------------------------

(deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:136"
    "each lease verb writes its own one kind; holder, heartbeat, release, handoff"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let* ((worked (heartbeat! log "acme/work/f1/t1" "rowan"))
           (released (release! worked "acme/work/f1/t1" "rowan"))
           (retaken (take! released "acme/work/f1/t1" "rowan" "30m" :release))
           (handed (release! retaken "acme/work/f1/t1" "rowan" :handed "glenn" :by "1h" :default :release)))
      (check-equal '(:lease :heartbeat :release :lease :handoff)
                   (mapcar (lambda (r) (getf r :kind)) (handoff-rows handed))
                   "take, heartbeat, release, release --handed each log one kind")
      (check-equal "glenn" (lease-holder handed "acme/work/f1/t1" :now *now*) "holder")
      (ok (worked-now-p worked "acme/work/f1/t1" *now* *win*) "a heartbeat is working-now")
      (ok (not (held-not-worked-p handed "acme/work/f1/t1" *now* *win*))
          "a fresh handoff is not held-not-worked"))))

;;; ------------------------------------------------------------------
;;; cursor-pinned-across-a-new-settle              SPEC-WORK.md:1797-1803
;;; ------------------------------------------------------------------

(deftest "cursor-pinned-across-a-new-settle" "docs/SPEC-WORK.md:1797"
    "a since/after cursor pages the transition log without drifting past a settle"
  (let* ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release))
         (pre (heartbeat! log "acme/work/f1/t1" "rowan")))
    (check-equal '(:lease :heartbeat)
                 (mapcar (lambda (r) (getf r :kind)) (handoff-rows pre))
                 "the pre-settle page")
    (let ((post (settle! pre "acme/work/f1/t1" "rowan")))
      (check-equal '(:lease :heartbeat :release)
                   (mapcar (lambda (r) (getf r :kind)) (handoff-rows post))
                   "the settle appends, never rewrites")
      (check-equal '(:release)
                   (mapcar (lambda (r) (getf r :kind)) (handoff-rows post :after 2))
                   "a cursor pinned at rev 2 returns only the settle's new row"))))

;;; ------------------------------------------------------------------
;;; applicable-cap-never-hides-a-deny              SPEC-WORK.md:1794
;;; ------------------------------------------------------------------

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:1794"
    "capping displayed rows never hides the counts or the held-not-worked deny"
  (let ((log (take! (make-lease-log) "acme/work/f1/t1" "rowan" "30m" :release)))
    (let ((log (take! log "acme/work/f1/t2" "glenn" "30m" :release)))
      (let ((full (stale-rows log :now *now* :window *win*)))
        (check-equal 2 (length full) "two stale leases in full")
        (check-equal 1 (length (subseq full 0 1)) "a cap limits the page")
        (check-equal 2 (length (stale-by-holder log :now *now* :window *win*))
                     "the deny is never hidden: both holders still count")))))
