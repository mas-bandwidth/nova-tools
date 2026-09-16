;;;; s8-presence.lisp --- Presence: who is awake and who is asleep (S8, #500).
;;;;
;;;; docs/SPEC-WORK.md:3967-4093 — the `:presence` event, the four ranked
;;;; sources, the awake/asleep/unknown reading, and the four presence columns
;;;; of the per-harness wait table. This file holds the pure records and
;;;; functions only: the `:presence` event kind, the wait row, and the reading.
;;;; Wiring into the command thread, the journal and the wire is a later card.
;;;;
;;;; Replay `wait-table-four-presence-columns` (docs/SPEC-WORK.md:2588) is the
;;;; acceptance target of the wait row and `session-kind` here.

(in-package #:nova-work)

(define-condition presence-refusal (unsupported-input) ())

;;; ------------------------------------------------------------------
;;; The `:presence` event (SPEC-WORK.md:3990-4000). Its subject is a friend
;;; identity, never a node: `:node` is `(:absent)`.
;;; ------------------------------------------------------------------

(defstruct (presence-record (:conc-name presence-)
                            (:constructor make-presence
                              (&key friend at clock source seen by)))
  friend at clock source seen by)

(defparameter +presence-sources+ '(:bus-cursor :wake-probe :harness-hook :manual)
  "The four presence sources, ranked strongest first in *PRESENCE-RANK*.")

(defparameter *presence-rank* '(:harness-hook :bus-cursor :manual :wake-probe)
  "SPEC-WORK.md:4002-4015 — how directly the record proves a turn ran on that
  line. harness-hook is the strongest, wake-probe (another line's observation)
  the weakest.")

(defun presence-source-valid-p (source)
  (member source +presence-sources+))

(defun presence-rank (source)
  (or (position source *presence-rank*) most-positive-fixnum))

(defun newest-presence (records)
  "The newest `:presence` record, whatever its source; a tie inside one
  coalescing bucket is settled by the rank above and nothing else."
  (let ((best nil))
    (dolist (r records)
      (when (or (null best)
                (> (presence-at r) (presence-at best))
                (and (= (presence-at r) (presence-at best))
                     (< (presence-rank (presence-source r))
                        (presence-rank (presence-source best)))))
        (setf best r)))
    best))

(defun presence-reading (records &key (now 0) (window 300))
  "The derived reading: `:awake` inside the window (default 300 s, Glenn's five
  minutes), `:asleep` when the newest record is older than the window, and
  `:unknown` — never `:asleep` — where no record was ever written
  (SPEC-WORK.md:3986, 4031). Returns the state, the age, the source and `:at`."
  (let ((best (newest-presence records)))
    (cond ((null best)
           (list :state :unknown :age nil :source nil :at nil))
          ((<= (- now (presence-at best)) window)
           (list :state :awake :age (- now (presence-at best))
                 :source (presence-source best) :at (presence-at best)))
          (t
           (list :state :asleep :age (- now (presence-at best))
                 :source (presence-source best) :at (presence-at best))))))

;;; ------------------------------------------------------------------
;;; The wait table and its four presence columns (SPEC-WORK.md:2580-2588):
;;; process alive, beat written, delivery handled, parent woke.
;;; ------------------------------------------------------------------

(defstruct wait-row
  friend process-alive beat-written delivery-handled parent-woke)

(defun holds-resident-session-p (row)
  (and (wait-row-process-alive row)
       (wait-row-beat-written row)
       (wait-row-delivery-handled row)
       (wait-row-parent-woke row)))

(defun holds-duty-session-p (row)
  (and (wait-row-process-alive row)
       (wait-row-beat-written row)
       (wait-row-delivery-handled row)))

(defun session-kind (row)
  "A harness that has proven the fourth column holds a resident session; one
  that has proven only the first three may hold a duty session driven by notes
  and nothing more (SPEC-WORK.md:2583-2587)."
  (cond ((holds-resident-session-p row) :resident)
        ((holds-duty-session-p row) :duty)
        (t :none)))

;;; ------------------------------------------------------------------
;;; Printed lines (SPEC-WORK.md:4026-4028). The `FRIEND ROW` line and the
;;; `QUERY OK ask=awake` summary; the `who`/`stale` row fields are the wiring
;;; card's concern and are shared here as `presence-*` slots on the reading.
;;; ------------------------------------------------------------------

(defun format-friend-row (name state &key age source at seen wait coordinator)
  (format nil "FRIEND ROW ~A presence=~A age=~A source=~A at=~A seen=~A wait=~A coordinator=~A"
          name state (or age "-") (or source "-") (or at "-") (or seen "-")
          (or wait "none") (if coordinator "true" "false")))

(defun format-awake-query-ok (&key friends awake asleep unknown rows shown)
  (format nil "QUERY OK ask=awake friends=~A awake=~A asleep=~A unknown=~A rows=~A shown=~A"
          friends awake asleep unknown rows shown))
