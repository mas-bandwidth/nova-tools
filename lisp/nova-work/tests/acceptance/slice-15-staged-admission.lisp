;;;; slice-15-staged-admission.lisp --- staged admission of slow-I/O results
;;;; (nova-work E03 row 2).
;;;;
;;;; SPEC-WORK.md:2752-2754  slow I/O stages its inputs and results outside the
;;;;                         mutation loop, and only the owning engine admits a
;;;;                         validated result at an expected revision
;;;; SPEC-WORK.md:3863-3868  the verifier, the provenance body and the offered
;;;;                         payload are staged inputs; the one writer
;;;;                         revalidates --expect and the offer's immutable
;;;;                         tuple before admitting one envelope; a stale or
;;;;                         failed stage writes nothing and `status` answers
;;;;                         while the stage runs
;;;; SPEC-WORK.md:6019       tuple mismatch; invalid stage; no received receipt;
;;;;                         conflicting bytes for receipt <id>
;;;;
;;;; These drive the real kernel: real files, the operator's verifier as a real
;;;; process, the kernel's one command thread and a real second thread for the
;;;; stage that is still running while status is asked.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; staged-admission-refuses-over-the-kernel       SPEC-WORK.md:3863-3868
;;; ------------------------------------------------------------------

(deftest "staged-admission-refuses-over-the-kernel" "docs/SPEC-WORK.md:2752-2754,3863-3868,6019"
    "expected=a-stale-or-failed-stage-writes-nothing;expect-tuple-and-payload-revalidated-by-the-writer;no-received-receipt-and-conflicting-bytes-named"
  (let* ((body "receipt for o-1 accepted by glenn")
         (digest (sha256-hex body))
         (path (write-provenance-file (test-provenance-path "staged") body))
         (pointer (format nil "file:~A" path))
         (payload-body "the bounded assignment body for o-1")
         (payload-path (write-provenance-file (test-provenance-path "payload") payload-body))
         (payload-pointer (format nil "file:~A" payload-path))
         (command (operator-verifier-command "glenn" "receipt-1")))

    ;; (1) A STALE STAGE. The reader ran outside the loop at revision R; by the
    ;; time the writer sees it the revision has moved, and the stage admits
    ;; nothing (SPEC-WORK.md:3866-3867).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn" :command command)
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (ok (staged-input-valid-p staged) "the stage itself is valid")
        ;; an unrelated accepted mutation moves the revision under the stage
        (ok (submit k (close-request :request "staged-move-1"))
            "an unrelated mutation moves the revision")
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :request "ack-stale-stage"))
          (check-equal nil okp "a stage the writer has moved past is refused")
          (check-equal 2 code "the stale-stage refusal is exit 2")
          (ok (search "invalid stage" line) "the refusal names the stage: ~A" line)))
      (check-equal '() (admitted-receipts (kernel-state k))
                   "a stale stage wrote a receipt"))

    ;; (2) --expect. The writer refuses at a revision the requester did not
    ;; expect, and writes nothing (SPEC-WORK.md:3864-3865).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn" :command command)
      (let* ((staged (stage-receipt k :provenance pointer :recipient "glenn"))
             (current (state-revision (kernel-state k))))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :expect (1- current)
                                   :request "ack-stale-expect"))
          (check-equal nil okp "a trailing --expect is refused")
          (check-equal 1 code "the stale --expect refusal is exit 1")
          (ok (search "stale" line) "the refusal names stale: ~A" line)
          (ok (search (format nil "current=~D" current) line)
              "the refusal names the current revision: ~A" line))
        (check-equal '() (admitted-receipts (kernel-state k)) "nothing was written")
        ;; the same stage at the expected revision is admitted
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :expect current
                                   :request "ack-expect-ok"))
          (ok okp "the same stage at the expected revision is admitted: ~A" line)
          (check-equal 0 code "it exits 0"))))

    ;; (3) THE OFFER'S IMMUTABLE TUPLE, revalidated against the offer the kernel
    ;; itself pinned (SPEC-WORK.md:3864-3865, the reason at :6019).
    (let ((k (receipt-kernel :offer "o-1" :attempt "a-1")))
      (configure-verifier k :recipient "glenn" :command command)
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :attempt "a-2" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :request "ack-tuple-attempt"))
          (check-equal nil okp "an attempt that is not the offer's is refused")
          (check-equal 1 code "the tuple refusal is exit 1")
          (ok (search "tuple mismatch" line) "the refusal names it: ~A" line)
          (ok (search "a-2" line) "the refusal names the field that moved: ~A" line))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :node "acme/work/f1/t2" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :request "ack-tuple-node"))
          (check-equal nil okp "a node that is not the offer's is refused")
          (check-equal 1 code "that refusal is exit 1")
          (ok (search "tuple mismatch" line) "the refusal names it: ~A" line))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :offer "o-never" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :request "ack-tuple-offer"))
          (check-equal nil okp "an offer the kernel never pinned is refused")
          (check-equal 1 code "that refusal is exit 1")
          (ok (search "no such offer" line) "the refusal names it: ~A" line)))
      (check-equal '() (admitted-receipts (kernel-state k))
                   "no tuple refusal wrote a receipt"))

    ;; (4) THE OFFERED PAYLOAD is a second staged input, and its staged digest
    ;; must name what --payload-sha256 names (SPEC-WORK.md:3863-3866).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn" :command command)
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn"))
            (payload (stage-payload-file payload-pointer)))
        (ok (staged-input-valid-p payload) "the payload stages from its real file")
        (check-equal (sha256-hex payload-body) (staged-input-digest payload)
                     "the payload's staged digest is the digest of its bytes")
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :staged-payload payload
                                   :payload-sha256 (sha256-hex "other payload")
                                   :request "ack-payload-mismatch"))
          (check-equal nil okp "a staged payload that is not --payload-sha256's is refused")
          (check-equal 2 code "that refusal is exit 2")
          (ok (search "invalid stage" line) "the refusal names the stage: ~A" line))
        (check-equal '() (admitted-receipts (kernel-state k)) "nothing was written")
        ;; a --payload-sha256 with no stage at all admits nothing either
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged
                                   :payload-sha256 (sha256-hex payload-body)
                                   :request "ack-payload-unstaged"))
          (check-equal nil okp "an unstaged payload is refused")
          (check-equal 2 code "that refusal is exit 2")
          (ok (search "invalid stage" line) "the refusal names the stage: ~A" line))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :staged-payload payload
                                   :payload-sha256 (sha256-hex payload-body)
                                   :request "ack-payload-ok"))
          (ok okp "the matching staged payload is admitted: ~A" line)
          (check-equal 0 code "it exits 0"))))

    ;; (5) NO RECEIVED RECEIPT. An acceptance stands on a verified delivery and
    ;; never on its own word (SPEC-WORK.md:3853-3855, :6019).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn" :command command)
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :stage :accepted :lease-by "30m"
                                   :lease-default "release" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :reply "receipt-acc" :request "ack-acc-bare"))
          (check-equal nil okp "an acceptance with no verified delivery is refused")
          (check-equal 1 code "that refusal is exit 1")
          (ok (search "no received receipt" line) "the refusal names it: ~A" line)))
      (check-equal '() (admitted-receipts (kernel-state k)) "nothing was written"))

    ;; (6) CONFLICTING BYTES. One reply id names one set of received bytes
    ;; (SPEC-WORK.md:6019).
    (let* ((other-body "receipt for o-1 accepted by someone else")
           (other-path (write-provenance-file (test-provenance-path "conflict") other-body))
           (other-pointer (format nil "file:~A" other-path))
           (k (receipt-kernel)))
      (configure-verifier k :recipient "glenn" :command command)
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (ok (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :request "ack-conflict-first"))
            "the first receipt is admitted"))
      (let ((staged (stage-receipt k :provenance other-pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance other-pointer
                                   :provenance-sha256 (sha256-hex other-body)
                                   :staged staged :request "ack-conflict-second"))
          (check-equal nil okp "the same reply over other bytes is refused")
          (check-equal 1 code "that refusal is exit 1")
          (ok (search "conflicting bytes for receipt receipt-1" line)
              "the refusal names the receipt: ~A" line)))
      (check-equal 1 (length (admitted-receipts (kernel-state k)))
                   "the conflicting receipt was written anyway")
      (check-equal digest (getf (admitted-receipt (kernel-state k) "receipt-1")
                                :receipt-digest)
                   "the first receipt's bytes were overwritten"))))

;;; ------------------------------------------------------------------
;;; status-answers-while-the-stage-runs            SPEC-WORK.md:3867
;;; ------------------------------------------------------------------

(deftest "status-answers-while-the-stage-runs" "docs/SPEC-WORK.md:2752-2754,3867"
    "expected=the-reader-runs-off-the-command-thread;status-answers-during-it;the-stage-then-admits-one-envelope"
  (let* ((body "receipt for o-slow accepted by glenn")
         (digest (sha256-hex body))
         (path (write-provenance-file (test-provenance-path "slow") body))
         (pointer (format nil "file:~A" path))
         ;; The operator's verifier takes its time, as a slow reader does.
         (command (format nil "sleep 1; ~A" (operator-verifier-command "glenn" "receipt-slow")))
         (k (receipt-kernel :offer "o-slow" :attempt "a-slow"))
         (staged nil)
         (answers 0))
    (configure-verifier k :recipient "glenn" :command command)
    (let ((reader (sb-thread:make-thread
                   (lambda ()
                     (setf staged (stage-receipt k :provenance pointer
                                                   :recipient "glenn")))
                   :name "nova-work-test-stage")))
      ;; `status` answers while the stage runs: the reader is off the command
      ;; thread, so the counter read never waits for it (SPEC-WORK.md:3867).
      (loop while (sb-thread:thread-alive-p reader)
            do (multiple-value-bind (open unit scope line) (ask-size k)
                 (declare (ignore open unit scope))
                 (ok (search "QUERY OK ask=size" line)
                     "status did not answer while the stage ran: ~A" line)
                 (incf answers)
                 (sleep 0.01)))
      (sb-thread:join-thread reader))
    (ok (plusp answers) "the stage finished before status was asked even once")
    (ok (staged-input-valid-p staged) "the slow reader still produced a valid stage")
    (check-equal digest (staged-input-digest staged)
                 "the slow reader's digest is the digest of the bytes")
    (multiple-value-bind (okp line code)
        (submit k (ack-request :offer "o-slow" :attempt "a-slow" :reply "receipt-slow"
                               :provenance pointer :provenance-sha256 digest
                               :staged staged :request "ack-slow"))
      (ok okp "the settled stage admits one envelope: ~A" line)
      (check-equal 0 code "it exits 0"))
    (check-equal 1 (length (admitted-receipts (kernel-state k)))
                 "the slow stage admitted exactly one receipt")))
