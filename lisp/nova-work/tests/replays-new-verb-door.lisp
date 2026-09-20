;;;; replays-new-verb-door.lisp --- the new verbs go through the one door.
;;;;
;;;; docs/SPEC-WORK.md:2603-2616, rule 6: "a mutation outside the command loop
;;;; is a defect". `submit-new-verb` was a SECOND door: it read
;;;; `(kernel-state kernel)`, built a candidate and installed it with
;;;; `(setf (kernel-state kernel) ...)` and `(setf (kernel-next-rev kernel) ...)`
;;;; -- a read-modify-write on the CALLER's thread, beside a writer doing the
;;;; same. Two of those interleaved lose an event whichever one wins.
;;;;
;;;; It journaled correctly. The defect was never the record; it was the thread.

(in-package #:nova-work/tests)

(defparameter *door-seed*
  '((:id "d"    :type :work-set :parent nil :state :unknown)
    (:id "d/t1" :type :task     :parent "d" :state :doing)
    (:id "d/t2" :type :task     :parent "d" :state :doing)))

(defun friend-request (&key (who "emma") (request "fr-1"))
  (list :verb :friend :change :add :friend who :role "reviewer"
        :request request :by "rowan" :stamp "2026-09-14T12:00:00Z" :clock :tool))

(deftest "a-new-verb-reaches-the-kernel-through-submit"
    "docs/SPEC-WORK.md:2603-2616,1014-1058"
    "expected=submit-accepts-friend-model-observe-config;the-same-line-either-door"
  ;; `submit` is the one door rule 6 names. A mutating verb the kernel
  ;; implements and `submit` refuses is a verb with a second door.
  (let ((k (make-kernel :state (make-seed-state *door-seed*))))
    (multiple-value-bind (okp line code) (submit k (friend-request :request "door-1"))
      (ok okp "submit refused a verb the kernel implements: ~A (code ~A)" line code)
      (ok (search "FRIEND OK" line) "the receipt is not a FRIEND OK: ~A" line)))
  ;; and each of the other three the grammar names
  (let ((k (make-kernel :state (make-seed-state *door-seed*))))
    (dolist (req (list (list :verb :model :change :add :model "astra" :provider "p"
                             :request "door-m" :by "rowan"
                             :stamp "2026-09-14T12:00:00Z" :clock :tool)
                       (list :verb :observe :change :add :friend "emma" :state "awake"
                             :source "probe" :request "door-o" :by "rowan"
                             :stamp "2026-09-14T12:00:00Z" :clock :tool)
                       (list :verb :config :friend "emma" :base "b" :revision "1"
                             :hash "h" :parts "p" :request "door-c" :by "rowan"
                             :stamp "2026-09-14T12:00:00Z" :clock :tool)))
      (multiple-value-bind (okp line) (submit k req)
        (ok okp "submit refused ~A: ~A" (getf req :verb) line))))
  ;; The public entry answers the same, because it IS this door now.
  (let ((k (make-kernel :state (make-seed-state *door-seed*))))
    (multiple-value-bind (okp line) (submit-new-verb k (friend-request :request "door-2"))
      (ok okp "submit-new-verb refused: ~A" line)
      (ok (search "FRIEND OK" line) "the receipt is not a FRIEND OK: ~A" line))))

(deftest "new-verbs-and-transitions-interleave-without-losing-an-event"
    "docs/SPEC-WORK.md:2603-2616"
    "expected=history-length-equals-the-successful-commands;revision-is-the-last-event"
  ;; THE READ-MODIFY-WRITE. Half these threads go through the writer and half
  ;; used to go around it. Every successful command is one envelope and one
  ;; history entry, so a dropped entry IS a lost update -- no timing assertion,
  ;; no sleep, just a count that cannot lie.
  (let* ((k (make-kernel :state (make-seed-state *door-seed*)
                         ;; The default ordering-journal holds 64 records and
                         ;; refuses past that; this case wants 80 accepted.
                         :journal (make-ordering-journal :capacity 512)))
         (rounds 40)
         (results (make-array (* 2 rounds) :initial-element nil))
         (threads
           (append
            (loop for i below rounds
                  collect (let ((i i))
                            (sb-thread:make-thread
                             (lambda ()
                               (setf (aref results i)
                                     (first (multiple-value-list
                                             (submit-new-verb
                                              k (friend-request
                                                 :who (format nil "f~D" i)
                                                 :request (format nil "door-n-~D" i)))))))
                             :name (format nil "newverb-~D" i))))
            (loop for i below rounds
                  collect (let ((i i))
                            (sb-thread:make-thread
                             (lambda ()
                               (setf (aref results (+ rounds i))
                                     (first (multiple-value-list
                                             (submit k (list :verb :model :change :add
                                                             :model (format nil "m~D" i)
                                                             :provider "p"
                                                             :request (format nil "door-m-~D" i)
                                                             :by "rowan"
                                                             :stamp "2026-09-14T12:00:00Z"
                                                             :clock :tool))))))
                             :name (format nil "model-~D" i)))))))
    (dolist (thread threads) (sb-thread:join-thread thread))
    (let ((wins (loop for r across results count r)))
      (check-equal (* 2 rounds) wins "a command with a fresh request id was refused")
      (check-equal wins (length (state-history (kernel-state k)))
                   "the history is shorter than the commands that succeeded: an envelope was installed over another one and lost")
      ;; Every revision is distinct and the counter is past all of them.
      (let ((revs (loop for entry in (state-history (kernel-state k))
                        append (mapcar (lambda (e) (getf e :rev)) (getf entry :events)))))
        (check-equal (length revs) (length (remove-duplicates revs))
                     "two events share a revision")
        (ok (> (kernel-next-rev k) (reduce #'max revs))
            "the kernel's next revision is not past every event it applied")))))

(deftest "a-new-verb-refusal-names-the-verb-it-refused"
    "docs/SPEC-WORK.md:1014-1058"
    "expected=the-refusal-line-names-the-unsupported-verb"
  ;; src/new-verbs.lisp carried a leftover diff marker -- a bare `+` opening
  ;; line 68 -- which made the refusal `(format nil "... verb ~A ..." + (if verb
  ;; ...))`. `~A` consumed the CL REPL history variable `+` instead of the verb,
  ;; so every refusal read `verb NIL` whatever was asked for. It compiled and
  ;; loaded because `+` is a bound standard variable.
  (let ((k (make-kernel :state (make-seed-state *door-seed*))))
    (multiple-value-bind (okp line code)
        (submit-new-verb k (list :verb :nosuchverb :request "door-x" :by "rowan"))
      (ok (not okp) "an unknown verb was accepted")
      (check-equal 2 code "the refusal is not exit 2")
      (ok (search "nosuchverb" line)
          "the refusal does not name the verb it refused: ~A" line))))
