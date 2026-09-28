;;;; replays-restart-and-recovery.lisp --- kill the writer, restart, converge.
;;;;
;;;; docs/SPEC-WORK.md:307 fixes the order and says why: the session "appends it
;;;; with its request id to the local recovery journal, acknowledges only after
;;;; the journal is durable, THEN applies it". A stop between the two leaves the
;;;; record written and nothing applied, which is the order the two-part retry of
;;;; :315 rests on. This file kills the writer at each of the three points that
;;;; order creates and asserts what comes back.
;;;;
;;;; NO SLEEPS AND NO WALL-CLOCK ASSERTIONS. The cut points are barriers: an
;;;; injected pre-write failure for the first, `*before-apply-hook*` for the
;;;; second, and `replay-journal`'s own `stop-at-seq` for reading back any
;;;; prefix of the order.
;;;;
;;;; MEASURED BY MUTATION against dev, one edit each, fresh isolated ASDF cache,
;;;; on the suite as it stood. Each left it FULLY GREEN:
;;;;
;;;;   src/journal.lisp:467  `replay-journal`'s stop-at-seq cut       -> deletable
;;;;   src/journal.lisp:382  the uncertain-journal refusal            -> deletable
;;;;   src/journal.lisp:386  `journal-record`'s pending-envelope match -> deletable
;;;;
;;;; The last of those is the guard that caught the `dep` verb's double-record
;;;; defect in nova-tools#1589 -- the guard did its job in the field while having
;;;; no witness of its own.

(in-package #:nova-work/tests)

(defparameter *recovery-seed*
  '((:id "rr"     :type :work-set :parent nil   :state :unknown)
    (:id "rr/t1"  :type :task     :parent "rr"  :state :doing)
    (:id "rr/t2"  :type :task     :parent "rr"  :state :doing)
    (:id "rr/t3"  :type :task     :parent "rr"  :state :doing)))

(defun %recovery-commands ()
  "Four ordinary journaled commands, in one fixed order."
  (list (list :verb :state-to-done :node "rr/t1" :by "rowan" :reason "one"
              :evidence '("ev-1") :request "rr-1"
              :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-4")
        (list :verb :event-reopen :node "rr/t1" :by "rowan" :reason "back"
              :request "rr-2"
              :stamp "2026-09-14T12:01:00Z" :clock :tool :generation-owner "gen-4")
        (list :verb :state-to-done :node "rr/t2" :by "rowan" :reason "two"
              :evidence '("ev-2") :request "rr-3"
              :stamp "2026-09-14T12:02:00Z" :clock :tool :generation-owner "gen-4")
        (list :verb :state-to-done :node "rr/t3" :by "rowan" :reason "three"
              :evidence '("ev-3") :request "rr-4"
              :stamp "2026-09-14T12:03:00Z" :clock :tool :generation-owner "gen-4")))

(defun %clean-run-form (n)
  "The canonical bytes a CLEAN run of the first N commands reaches, with no
journal file in it at all: the answer a restart has to agree with."
  (let ((k (make-kernel :state (make-seed-state *recovery-seed*))))
    (loop for request in (%recovery-commands)
          repeat n
          do (multiple-value-bind (okp line) (submit k request)
               (ok okp "the clean run refused a command: ~A" line)))
    (state-canonical-form (kernel-state k))))

(defun %replayed-form (path digest &key stop-at-seq)
  "Reopen the journal at PATH and replay it into a fresh kernel over the seed,
answering the canonical bytes it reaches."
  (let* ((j (open-file-journal path :initial-state-hash digest))
         (k (make-kernel :state (make-seed-state *recovery-seed*) :journal j)))
    (unwind-protect
         (progn (replay-journal j k :stop-at-seq stop-at-seq)
                (state-canonical-form (kernel-state k)))
      (ignore-errors (close-file-journal j)))))

(deftest "every-prefix-of-the-journal-restarts-to-the-clean-runs-state"
    "docs/SPEC-WORK.md:307,2603-2616"
    "expected=stop-at-each-seq-equals-the-clean-run-of-that-many-commands"
  ;; The journal's sequence numbers ARE the total order, so replaying the first
  ;; N of them must land exactly where a clean run of the first N commands
  ;; lands -- for EVERY N, not only the last. A kill is a cut at some N, so this
  ;; is every kill point at once.
  (let* ((path (test-journal-path "restart-prefix"))
         (digest (root-digest (make-seed-state *recovery-seed*)))
         (j (open-file-journal path :initial-state-hash digest)))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *recovery-seed*) :journal j)))
           (dolist (request (%recovery-commands))
             (multiple-value-bind (okp line) (submit k request)
               (ok okp "the live run refused a command: ~A" line))))
      (ignore-errors (close-file-journal j)))
    (loop for n from 0 to 4
          do (check-equal (%clean-run-form n)
                          (%replayed-form path digest :stop-at-seq (if (zerop n) 0 n))
                          (format nil "a restart cut after ~D command(s) is not where a clean run of ~D lands" n n)))
    (ignore-errors (delete-file path))))

(deftest "a-kill-after-the-record-and-before-the-apply-recovers-and-answers-its-receipt"
    "docs/SPEC-WORK.md:307,315"
    "expected=record-durable;apply-lost;restart-applies-it;the-retry-answers-the-original-receipt"
  ;; THE WINDOW :307 EXISTS TO MAKE SAFE. The record is durable and the apply
  ;; never happened. A restart must apply it -- the work was acknowledged -- and
  ;; a retry of that request id must answer the receipt the journal holds and
  ;; apply nothing a second time.
  (let* ((path (test-journal-path "restart-mid"))
         (digest (root-digest (make-seed-state *recovery-seed*)))
         (j (open-file-journal path :initial-state-hash digest))
         (killed nil)
         (recorded-line nil))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *recovery-seed*) :journal j)))
           ;; two commands land normally
           (dolist (request (subseq (%recovery-commands) 0 2))
             (ok (submit k request) "a setup command was refused"))
           ;; the third is killed between the record and the apply
           (let ((*before-apply-hook*
                   (lambda (envelope)
                     (declare (ignore envelope))
                     (setf killed t)
                     (error "the writer stopped between the record and the apply"))))
             (handler-case (submit k (third (%recovery-commands)))
               (error () nil)))
           (ok killed "the hook never ran, so nothing was killed and this case proves nothing")
           ;; the record IS durable, with its receipt, although nothing applied
           (multiple-value-bind (found d line) (journal-lookup j "rr-3")
             (declare (ignore d))
             (ok found "the record was not durable before the apply: the order of :307 is reversed")
             (setf recorded-line line))
           (check-equal :o (node-branch (kernel-state k) "rr/t2")
                        "the apply happened after all, so the kill point is not the one named"))
      (ignore-errors (close-file-journal j)))
    ;; RESTART. The journal is the truth; the lost apply comes back.
    (check-equal (%clean-run-form 3) (%replayed-form path digest)
                 "a restart after a kill between the record and the apply is not where the clean run lands")
    ;; AND THE RETRY. Same id, same payload, after the restart: the original
    ;; receipt, and no second event.
    (let* ((j2 (open-file-journal path :initial-state-hash digest))
           (k2 (make-kernel :state (make-seed-state *recovery-seed*) :journal j2)))
      (unwind-protect
           (progn
             (replay-journal j2 k2)
             (let ((rev (state-revision (kernel-state k2)))
                   (hist (length (state-history (kernel-state k2)))))
               (multiple-value-bind (okp line) (submit k2 (third (%recovery-commands)))
                 (ok okp "the retry of a recorded request was refused: ~A" line)
                 (check-string= recorded-line line
                                "the retry did not answer its original receipt"))
               (check-equal rev (state-revision (kernel-state k2))
                            "the retry advanced the revision")
               (check-equal hist (length (state-history (kernel-state k2)))
                            "the retry applied a second event")))
        (ignore-errors (close-file-journal j2))))
    (ignore-errors (delete-file path))))

(deftest "a-kill-before-the-record-leaves-the-journal-uncertain-and-refusing"
    "docs/SPEC-WORK.md:307"
    "expected=nothing-recorded;nothing-applied;the-journal-refuses-every-later-append;restart-is-the-prefix"
  ;; THE OTHER SIDE OF :307. The append failed, so the session does not know
  ;; whether the bytes reached the disk. It must not carry on writing past a
  ;; record it cannot account for: every later append is refused until a
  ;; recovery, and the restart lands on the prefix that IS accounted for.
  (let* ((path (test-journal-path "restart-prewrite"))
         (digest (root-digest (make-seed-state *recovery-seed*)))
         (j (open-file-journal path :initial-state-hash digest :fail-pre-write-on "rr-3")))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *recovery-seed*) :journal j)))
           (dolist (request (subseq (%recovery-commands) 0 2))
             (ok (submit k request) "a setup command was refused"))
           (handler-case (submit k (third (%recovery-commands)))
             (error () nil))
           (check-equal :o (node-branch (kernel-state k) "rr/t2")
                        "a failed append applied its command anyway")
           (multiple-value-bind (found) (journal-lookup j "rr-3")
             (ok (not found) "a failed append left a record behind"))
           ;; THE UNCERTAIN JOURNAL REFUSES WHAT COMES NEXT. Carrying on would
           ;; write seq N+1 over a seq N nobody can account for.
           ;;
           ;; TWO GUARDS, and only the first is on this path: `journal-accept`
           ;; refuses an uncertain journal before `journal-record` is reached at
           ;; all, so `journal-record`'s own uncertain check -- the second line
           ;; of defence, and the one a direct caller meets -- is asked for
           ;; separately below.
           (multiple-value-bind (okp line) (submit k (fourth (%recovery-commands)))
             (ok (not okp) "the journal accepted an append after an uncertain one")
             (ok (search "recovery required" line)
                 "the refusal does not say a recovery is required: ~A" line))
           (check-equal :o (node-branch (kernel-state k) "rr/t3")
                        "the refused append applied its command anyway")
           ;; the second guard, asked directly
           (let ((refused nil))
             (handler-case (journal-record j "rr-4" "any-digest" "LINE" 9)
               (error (c) (setf refused (princ-to-string c))))
             (ok refused "journal-record wrote to an uncertain journal")
             (ok (search "recovery" refused)
                 "the refusal does not say a recovery is required: ~A" refused)))
      (ignore-errors (close-file-journal j)))
    ;; The restart lands on the two commands that ARE accounted for.
    (check-equal (%clean-run-form 2) (%replayed-form path digest)
                 "a restart after a failed append is not the prefix that was recorded")
    (ignore-errors (delete-file path))))

(deftest "journal-record-refuses-a-request-the-pending-envelope-does-not-name"
    "docs/SPEC-WORK.md:307"
    "expected=accept-then-record-a-different-id-is-refused;the-accepted-one-still-records"
  ;; The guard that caught nova-tools#1589's double-record defect in the field
  ;; -- `dep` recorded a placeholder and then recorded again over a cleared
  ;; pending envelope, and the permissive `ordering-journal` fake allowed it
  ;; while `open-file-journal` did not. The guard itself had no witness.
  (let* ((path (test-journal-path "pending-envelope"))
         (digest (root-digest (make-seed-state *recovery-seed*)))
         (j (open-file-journal path :initial-state-hash digest))
         (event (make-work-event :kind :transition :node "rr/t1" :by "rowan"
                                 :fields (list :to :done :reason "one"
                                               :blocked-by +absent+ :evidence '("ev-1"))
                                 :stamp "2026-09-14T12:00:00Z" :clock :tool
                                 :request "pe-1" :generation-owner "gen-4" :rev 1)))
    (unwind-protect
         (let* ((payload (payload-digest (list event)))
                (envelope (list :request "pe-1" :digest payload
                                :events (list event) :settle nil)))
           (ok (journal-accept j envelope) "the envelope was not accepted")
           ;; a record for an id the pending envelope does not name
           (let ((refused nil))
             (handler-case (journal-record j "pe-OTHER" payload "LINE" 1)
               (error (c) (setf refused (princ-to-string c))))
             (ok refused "the journal recorded an id its pending envelope never named")
             (ok (search "pending envelope" refused)
                 "the refusal does not name the pending envelope: ~A" refused))
           ;; a record for the right id with the wrong digest
           (let ((refused nil))
             (handler-case (journal-record j "pe-1" "not-the-digest" "LINE" 1)
               (error (c) (setf refused (princ-to-string c))))
             (ok refused "the journal recorded a digest its pending envelope never named"))
           ;; and the accepted one still records, so the guard refused the wrong
           ;; call and not every call
           (journal-record j "pe-1" payload "STATE OK id=1 request=pe-1" 1)
           (multiple-value-bind (found) (journal-lookup j "pe-1")
             (ok found "the accepted envelope did not record after the refusals")))
      (ignore-errors (close-file-journal j)))
    (ignore-errors (delete-file path))))
