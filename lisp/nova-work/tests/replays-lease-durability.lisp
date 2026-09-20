;;;; replays-lease-durability.lisp --- the node lease is a journaled command.
;;;;
;;;; docs/SPEC-WORK.md:2603-2616 rule 6: "a mutation outside the command loop is
;;;; a defect"; :315 the two-part request dedup; :135 and :1354-1357 the lease
;;;; itself. `take-lease` and `release-lease` did `(setf (wnode-holder n) by)`
;;;; on the live state from the CALLER's thread, so none of the three held.
;;;;
;;;; EVERY CASE HERE RUNS AGAINST THE REAL FILE JOURNAL, not the permissive
;;;; `ordering-journal` fake, except the two that are about the writer thread
;;;; itself. The fake accepts what `open-file-journal` refuses, and it is what
;;;; hid this whole class in the first place.

(in-package #:nova-work/tests)

(defparameter *lease-seed*
  '((:id "root"    :type :work-set :parent nil    :state :unknown)
    (:id "root/t1" :type :task     :parent "root" :state :doing)
    (:id "root/t2" :type :task     :parent "root" :state :doing)
    (:id "root/need" :type :task   :parent "root" :state :doing)
    (:id "root/dep" :type :task    :parent "root" :state :doing :deps ("root/need")))
  "Two independent leaves, plus a need and the dependent that names it.")

(defun lease-request (&key (node "root/t1") (by "emma") (request "lease-1")
                           (stamp "2026-09-14T12:00:00Z"))
  (list :verb :take-node :node node :by by :request request :stamp stamp :clock :tool))

(defun release-request (&key (node "root/t1") (by "emma") (request "release-1")
                             (stamp "2026-09-14T12:10:00Z"))
  (list :verb :release-node :node node :by by :request request :stamp stamp :clock :tool))

(defmacro with-lease-journal ((k j &key (name "lease") (seed '*lease-seed*)) &body body)
  "A kernel over a REAL file journal at a fresh path, closed on the way out.
PATH is bound too, so a case can reopen the same journal and replay it."
  (let ((digest (gensym "DIGEST")))
    `(let* ((path (test-journal-path ,name))
            (,digest (root-digest (make-seed-state ,seed)))
            (init-digest ,digest)
            (,j (open-file-journal path :initial-state-hash ,digest))
            (,k (make-kernel :state (make-seed-state ,seed) :journal ,j)))
       (declare (ignorable init-digest))
       (unwind-protect (progn ,@body)
         (ignore-errors (close-file-journal ,j))))))

;;; ------------------------------------------------------------------
;;; 1. The take is a record in the journal, and the lease comes back
;;; ------------------------------------------------------------------

(deftest "a-take-is-a-journaled-command-and-survives-a-replay"
    "docs/SPEC-WORK.md:2603-2616,315"
    "expected=one-durable-record;lease-log-row;holder-recovered-by-replay-journal;exactly-one-take"
  (with-lease-journal (k j :name "lease-replay")
    (multiple-value-bind (okp line) (submit k (lease-request :request "ltr-1"))
      (ok okp "the take was refused: ~A" line)
      (ok (search "LEASE OK" line) "the receipt is not a LEASE OK: ~A" line)
      (check-equal "emma" (node-holder (kernel-state k) "root/t1") "the node is held")
      ;; THE RECORD. A mutation outside the command loop leaves none, and the
      ;; ordering-journal fake would not tell you either way.
      (multiple-value-bind (found digest recorded) (journal-lookup j "ltr-1")
        (declare (ignore digest))
        (ok found "the take wrote NO journal record")
        (check-string= line recorded "the recorded line is not the receipt"))
      ;; The lease log holds the take, not only the release a settle writes.
      (let ((rows (state-lease-log (kernel-state k))))
        (check-equal 1 (length rows) "one lease-log row after one take")
        (check-equal :take (getf (first rows) :kind) "the row is the take")
        (check-equal "emma" (getf (first rows) :holder) "it names the holder")))
    ;; CLOSE, REOPEN, REPLAY. This is the assertion the old code could not
    ;; pass at all: a lease that is not in the journal is not in the order,
    ;; and a kernel rebuilt from the journal gets the node back UNOWNED.
    (close-file-journal j)
    (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
           (k2 (make-kernel :state (make-seed-state *lease-seed*) :journal j2)))
      (unwind-protect
           (progn
             (replay-journal j2 k2)
             (check-equal "emma" (node-holder (kernel-state k2) "root/t1")
                          "the lease did not survive replay-journal")
             (let ((rows (state-lease-log (kernel-state k2))))
               (check-equal 1 (length rows) "the replay applied the take twice or not at all")
               (check-equal :take (getf (first rows) :kind) "the replayed row is the take")))
        (ignore-errors (close-file-journal j2))))))

;;; ------------------------------------------------------------------
;;; 2. The replay answer comes before any mutable state
;;; ------------------------------------------------------------------

(deftest "a-retried-take-answers-its-original-receipt-after-the-state-moved"
    "docs/SPEC-WORK.md:315"
    "expected=identical-payload-answers-the-recorded-line;no-second-event;no-second-lease-row"
  (with-lease-journal (k j :name "lease-retry")
    (let (first-line)
      (multiple-value-bind (okp line) (submit k (lease-request :request "lrt-1"))
        (ok okp "the take was refused: ~A" line)
        (setf first-line line))
      ;; The set moves under the request: another node settles, the revision
      ;; advances, the history grows.
      (ok (submit k (list :verb :state-to-done :node "root/t2" :by "rowan"
                          :reason "shipped" :evidence '("ev-1") :request "lrt-move"
                          :stamp "2026-09-14T12:05:00Z" :clock :tool
                          :generation-owner "gen-4"))
          "the intervening settle was refused")
      (let ((rev (state-revision (kernel-state k)))
            (rows (length (state-lease-log (kernel-state k))))
            (hist (length (state-history (kernel-state k)))))
        ;; THE IDENTICAL REQUEST, RETRIED. The durable contract is about the
        ;; RECORDED payload and not about the state now.
        (multiple-value-bind (okp line) (submit k (lease-request :request "lrt-1"))
          (ok okp "the retry was refused: ~A" line)
          (check-string= first-line line "the retry did not answer its original receipt"))
        (check-equal rev (state-revision (kernel-state k)) "the retry advanced the revision")
        (check-equal rows (length (state-lease-log (kernel-state k)))
                     "the retry wrote a second lease row")
        (check-equal hist (length (state-history (kernel-state k)))
                     "the retry wrote a second event")))))

(deftest "a-take-id-reused-with-a-different-payload-is-refused"
    "docs/SPEC-WORK.md:315"
    "expected=exit-1-reused-with-a-different-payload;nothing-written"
  (with-lease-journal (k j :name "lease-conflict")
    (ok (submit k (lease-request :request "lc-1" :by "emma")) "the take was refused")
    (let ((rev (state-revision (kernel-state k)))
          (rows (length (state-lease-log (kernel-state k)))))
      (multiple-value-bind (okp line code)
          (submit k (lease-request :node "root/t2" :request "lc-1" :by "sam"))
        (ok (not okp) "a reused id with a different payload was accepted")
        (check-equal 1 code "the refusal is not exit 1")
        (ok (search "reused with a different payload" line)
            "the line does not name the reuse: ~A" line))
      (check-equal rev (state-revision (kernel-state k)) "the refusal advanced the revision")
      (check-equal rows (length (state-lease-log (kernel-state k)))
                   "the refusal wrote a lease row")
      (check-equal nil (node-holder (kernel-state k) "root/t2")
                   "the refusal granted a lease"))))

;;; ------------------------------------------------------------------
;;; 3. A failed durable append writes nothing
;;; ------------------------------------------------------------------

(deftest "a-take-whose-journal-append-fails-leaves-no-lease"
    "docs/SPEC-WORK.md:307"
    "expected=refusal;holder-unowned;lease-log-unchanged;history-unchanged"
  (let* ((path (test-journal-path "lease-prewrite"))
         (digest (root-digest (make-seed-state *lease-seed*)))
         ;; The record-then-apply order is the whole point: a pre-write failure
         ;; must leave the state exactly as it was, not a lease with no record.
         (j (open-file-journal path :initial-state-hash digest :fail-pre-write-on "lpw-1"))
         (k (make-kernel :state (make-seed-state *lease-seed*) :journal j)))
    (unwind-protect
         (let ((rev (state-revision (kernel-state k)))
               (hist (length (state-history (kernel-state k)))))
           (let ((condition nil))
             (multiple-value-bind (okp line)
                 (handler-case (submit k (lease-request :request "lpw-1"))
                   (error (c) (setf condition c) (values nil (princ-to-string c))))
               (declare (ignore line))
               (ok (not okp) "a take survived a failed durable append")
               ;; The refusal must be THE INJECTED APPEND FAILURE. Without
               ;; this the case passes vacuously anywhere `take` is not a
               ;; journaled command at all, which is exactly where it must fail.
               (ok condition
                   "the take was refused before the durable append was ever attempted, so this case proves nothing about the append")
               (ok (search "write or sync failed" (princ-to-string condition))
                   "the refusal is not the injected pre-write failure: ~A"
                   (princ-to-string condition))))
           (check-equal nil (node-holder (kernel-state k) "root/t1")
                        "the failed append left a holder")
           (check-equal '() (state-lease-log (kernel-state k))
                        "the failed append left a lease-log row")
           (check-equal rev (state-revision (kernel-state k))
                        "the failed append advanced the revision")
           (check-equal hist (length (state-history (kernel-state k)))
                        "the failed append wrote an event"))
      (ignore-errors (close-file-journal j)))))

;;; ------------------------------------------------------------------
;;; 4. The release is journaled too, and a settle still ends a live lease
;;; ------------------------------------------------------------------

(deftest "a-release-is-journaled-and-survives-a-replay"
    "docs/SPEC-WORK.md:1354-1357"
    "expected=release-recorded;holder-unowned-after-replay;a-third-name-is-refused"
  (with-lease-journal (k j :name "lease-release")
    (ok (submit k (lease-request :request "lrl-1")) "the take was refused")
    ;; A third name never ends a claim it did not make.
    (multiple-value-bind (okp line code)
        (submit k (release-request :by "sam" :request "lrl-x"))
      (ok (not okp) "a third name ended the claim")
      (check-equal 1 code "the refusal is not exit 1")
      (ok (search "holder=emma" line) "the refusal does not name the holder: ~A" line))
    (ok (submit k (release-request :request "lrl-2")) "the release was refused")
    (check-equal nil (node-holder (kernel-state k) "root/t1") "the release left a holder")
    (multiple-value-bind (found) (journal-lookup j "lrl-2")
      (ok found "the release wrote NO journal record"))
    (close-file-journal j)
    (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
           (k2 (make-kernel :state (make-seed-state *lease-seed*) :journal j2)))
      (unwind-protect
           (progn
             (replay-journal j2 k2)
             (check-equal nil (node-holder (kernel-state k2) "root/t1")
                          "the replayed release did not end the lease")
             (check-equal '(:take :release)
                          (mapcar (lambda (r) (getf r :kind)) (state-lease-log (kernel-state k2)))
                          "the replayed lease log is not the take then the release"))
        (ignore-errors (close-file-journal j2))))))

;;; ------------------------------------------------------------------
;;; 5. The mutation happens on the writer, never on the caller's thread
;;; ------------------------------------------------------------------

(deftest "a-take-is-applied-on-the-command-thread-and-not-the-callers"
    "docs/SPEC-WORK.md:2603-2616"
    "expected=apply-runs-on-the-kernels-one-command-thread"
  (with-lease-journal (k j :name "lease-thread")
    (let ((caller sb-thread:*current-thread*)
          (applied-on nil))
      (let ((*before-apply-hook*
              (lambda (envelope)
                (declare (ignore envelope))
                (setf applied-on sb-thread:*current-thread*))))
        (ok (submit k (lease-request :request "lth-1")) "the take was refused"))
      (ok applied-on "nothing was applied, so nothing was journaled either")
      (ok (not (eq applied-on caller))
          "the lease was applied on the CALLER's thread: this is the mutation outside the command loop that SPEC-WORK.md:2603-2616 rule 6 calls a defect")
      (check-string= "nova-work-kernel" (sb-thread:thread-name applied-on)
                     "the lease was not applied on the kernel's own command thread"))))

(deftest "concurrent-takes-of-one-node-grant-exactly-one-lease"
    "docs/SPEC-WORK.md:135,2603-2616"
    "expected=one-winner;one-lease-log-row;holder-is-the-winner"
  (with-lease-journal (k j :name "lease-race")
    (let* ((holders '("a" "b" "c" "d" "e" "f" "g" "h"))
           (results (make-array (length holders) :initial-element nil))
           (threads
             (loop for i from 0
                   for who in holders
                   collect (let ((i i) (who who))
                             (sb-thread:make-thread
                              (lambda ()
                                (setf (aref results i)
                                      (multiple-value-list
                                       (submit k (lease-request
                                                  :by who
                                                  :request (format nil "lrace-~A" who))))))
                              :name (format nil "take-~A" who))))))
      (dolist (thread threads) (sb-thread:join-thread thread))
      (let ((winners (loop for r across results when (first r) collect r)))
        (check-equal 1 (length winners) "a node granted more than one live lease")
        (let ((rows (state-lease-log (kernel-state k))))
          (check-equal 1 (length rows) "more than one take reached the lease log")
          (check-equal (node-holder (kernel-state k) "root/t1") (getf (first rows) :holder)
                       "the log's holder is not the node's")))
      (loop for r across results
            unless (first r)
              do (ok (search "held" (second r))
                     "a losing take was refused for the wrong reason: ~A" (second r))))))

;;; ------------------------------------------------------------------
;;; 6. v1 reads a need in C as terminal accepted, whatever its evidence
;;; ------------------------------------------------------------------

(deftest "a-take-over-a-need-in-c-asks-for-no-evidence"
    "docs/SPEC-WORK.md:2117"
    "expected=a-need-in-C-admits-its-dependent-with-no-view-and-no-evidence-read"
  ;; :2117 -- this kernel slice "reads a need as terminal accepted once it is in
  ;; C, whatever its disposition and whatever its evidence". The lease verb
  ;; therefore asks NOTHING of `:deps`: no needs view, no verification cache, no
  ;; reason token. This case is the register that fails the day a v2 gate is
  ;; lifted into v1 without the spec moving first.
  (with-lease-journal (k j :name "lease-need")
    (ok (submit k (list :verb :state-to-done :node "root/need" :by "rowan"
                        :reason "done" :evidence '("ev-1") :request "lneed-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
        "the need did not settle")
    (check-equal :c (node-branch (kernel-state k) "root/need") "the need is in C")
    (multiple-value-bind (okp line) (submit k (lease-request :node "root/dep" :request "lneed-2"))
      (ok okp "a need in C refused its dependent's lease: ~A" line))
    (check-equal "emma" (node-holder (kernel-state k) "root/dep") "the dependent is held")))

;;; ------------------------------------------------------------------
;;; 7. E11-F05-02: packet-is-smallest-sufficient (docs/SPEC-WORK.md:4649)
;;; ------------------------------------------------------------------
;;; "the packet carries the delta since this reader's recorded head, the rules it
;;; touches, the open findings with dispositions, the new behaviour with evidence
;;; pointers and links to the full sources; the whole diff only when this reader
;;; has never read the entry." A reader's recorded head is real here: it is the
;;; verdict row keyed by the reader and the exact head it read
;;; (docs/SPEC-WORK.md:4535, src/replays-verdict-state.lisp); a reader with no row is a
;;; first read owed the whole diff. The delta/whole distinction that makes the
;;; packet smallest-sufficient is exercised nowhere in src/, so this case is the
;;; red that proves criteria E11-F05 is unmet: it asserts the packet actor and
;;; names what it must carry, and fails while the kernel has no such actor.

(deftest "TestE11F05PacketIsSmallestSufficientThe"
    "docs/SPEC-WORK.md:4649"
    "expected=delta-since-recorded-head-plus-rules-findings-evidence-pointers-links;whole-diff-only-on-first-read"
  (let ((reader "rowan")
        (head "f01a0c42d7de34acd4cea8199e2f35510c50cde5"))
    (let ((recorded (make-verdict-row :node "root" :reader reader :head head
                                      :decision :approve)))
      (check-string= reader (verdict-row-reader recorded)
                     "the recorded head names its reader")
      (check-string= head (verdict-row-head recorded)
                     "the recorded head is the exact head the reader last read"))
    (let ((actor (or (find-symbol "BUILD-DECISION-PACKET" :nova-work)
                     (find-symbol "MAKE-DECISION-PACKET" :nova-work)
                     (find-symbol "DECISION-PACKET" :nova-work))))
      (ok (and actor (fboundp actor))
          "SPEC-WORK.md:4649 packet-is-smallest-sufficient: expected a decision-packet builder returning the delta since the reader's recorded head, the rules it touches, the open findings with dispositions, the new behaviour with evidence pointers and links to the full sources (the whole diff only on a first read); the kernel defines no such builder, so the criterion is unmet"))))
