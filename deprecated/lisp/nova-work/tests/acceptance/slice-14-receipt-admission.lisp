;;;; slice-14-receipt-admission.lisp --- the verifier result a receipt needs,
;;;; admitted by the one writer (nova-work E03 row 1).
;;;;
;;;; SPEC-WORK.md:3857-3862  a receipt is a verified observation, never a
;;;;                         request's word; the three derived fields are the
;;;;                         session's own half
;;;; SPEC-WORK.md:3863-3868  the readers run outside the mutation loop; a
;;;;                         verifier outage is `provenance unverified`, canonical
;;;;                         state unchanged
;;;; SPEC-WORK.md:2295-2296  the `acknowledge` and `decline` grammar
;;;; SPEC-WORK.md:6018-6019  the OK and FAIL lines
;;;;
;;;; These drive the real kernel: a real provenance file, the operator's
;;;; verifier as a real process, the kernel's one command thread, and the
;;;; journal. `slice-09-fleet-assignment.lisp` keeps the pure booking model of
;;;; the same paragraph; this file is the kernel half.

(in-package #:nova-work/tests)

(defun test-provenance-path (name)
  "A provenance body path under this run's own root. The old fixed directory,
`<tmpdir>/nova-work-test-provenance/`, was shared by every suite on the host
and the file name inside it keyed on the clock (nova-tools#1699)."
  (test-temp-file name "body"))

(defun write-provenance-file (path text)
  "Write the provenance body PATH names, with no trailing newline: the digest
the verifier answers is the digest of exactly these bytes."
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create :external-format :utf-8)
    (write-string text out))
  path)

(defparameter *shell-sha256*
  "if command -v sha256sum >/dev/null 2>&1; then printf '%s' \"$body\" | sha256sum | cut -d' ' -f1; else printf '%s' \"$body\" | shasum -a 256 | cut -d' ' -f1; fi"
  "The digest of the staged bytes, taken by the operator's own process. Either
tool answers; a bench with neither is a bench finding and never a pass.")

(defun operator-verifier-command (recipient receipt-id)
  "The operator's configured verifier: it reads the provenance body on stdin
and vouches for it with the recipient identity, a stable receipt id and the
digest of the bytes it actually read."
  (format nil "body=$(cat); d=$(~A); printf '%s %s %s\\n' ~A ~A \"$d\""
          *shell-sha256* recipient receipt-id))

(defun receipt-kernel (&key (offer "o-1") (node "acme/work/f1/t1") (attempt "a-1")
                            (generation 1) journal)
  "A kernel whose control state already pins the offer the receipt will name
(src/control.lisp `prepare-offer`), so the one writer has an immutable tuple to
revalidate the receipt against (SPEC-WORK.md:3864-3865)."
  (let ((k (if journal
               (make-kernel :state (make-seed-state *seed*) :journal journal)
               (fresh))))
    (prepare-offer k offer node attempt :generation generation)
    k))

(defun ack-request (&key (verb :acknowledge) (node "acme/work/f1/t1") (by "rowan")
                         (offer "o-1") (attempt "a-1") (reply "receipt-1")
                         (stage :received) provenance provenance-sha256 staged
                         expect staged-payload payload-sha256 generation
                         lease-by lease-default sender receipt-digest effect
                         (request "ack-1") (stamp "2026-09-14T12:00:00Z")
                         (generation-owner "gen-4"))
  (append (list :verb verb :node node :by by :offer offer :attempt attempt
                :reply reply :provenance provenance
                :provenance-sha256 provenance-sha256 :staged staged
                :request request :stamp stamp :clock :tool
                :generation-owner generation-owner)
          (when (eq verb :acknowledge) (list :stage stage))
          (when expect (list :expect expect))
          (when staged-payload (list :staged-payload staged-payload))
          (when payload-sha256 (list :payload-sha256 payload-sha256))
          (when generation (list :generation generation))
          (when lease-by (list :lease-by lease-by))
          (when lease-default (list :lease-default lease-default))
          (when sender (list :sender sender))
          (when receipt-digest (list :receipt-digest receipt-digest))
          (when effect (list :effect effect))))

;;; ------------------------------------------------------------------
;;; a-receipt-needs-a-verifier-over-the-kernel      SPEC-WORK.md:3857-3868
;;; ------------------------------------------------------------------

(deftest "a-receipt-needs-a-verifier-over-the-kernel" "docs/SPEC-WORK.md:3857-3868,6019"
    "expected=no-verifier-result-no-receipt;the-session-derives-sender-receipt-digest-effect;a-refusal-writes-nothing"
  (let* ((body "receipt for o-1 accepted by glenn")
         (digest (sha256-hex body))
         (path (write-provenance-file (test-provenance-path "receipt") body))
         (pointer (format nil "file:~A" path)))

    ;; (1) No verifier is configured: a copied bus body is provenance data and
    ;; never authority (SPEC-WORK.md:3857-3859).
    (let* ((k (receipt-kernel))
           (before (state-revision (kernel-state k)))
           (staged (stage-receipt k :provenance pointer :recipient "glenn")))
      (check-equal nil (staged-input-valid-p staged)
                   "a body with no configured verifier is not a valid stage")
      (multiple-value-bind (okp line code)
          (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                 :staged staged))
        (check-equal nil okp "a receipt with no verifier result is refused")
        (check-equal 2 code "the refusal is exit 2")
        (ok (search "ACKNOWLEDGE FAIL" line) "the refusal is the ACKNOWLEDGE FAIL line: ~A" line)
        (ok (search "provenance unverified" line)
            "a verifier outage reads provenance unverified: ~A" line))
      (check-equal '() (admitted-receipts (kernel-state k)) "no receipt was written")
      (check-equal before (state-revision (kernel-state k))
                   "canonical state moved on a refusal"))

    ;; (2) A verifier outage -- the operator's process exits non-zero -- is the
    ;; same refusal, and never a bus body promoted to authority (:3868).
    (let* ((k (receipt-kernel))
           (before (state-revision (kernel-state k))))
      (configure-verifier k :recipient "glenn" :command "exit 7")
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (check-equal nil (staged-input-valid-p staged) "an outage stages nothing")
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged))
          (check-equal nil okp "a verifier outage refuses the receipt")
          (check-equal 2 code "the outage refusal is exit 2")
          (ok (search "provenance unverified" line) "the refusal names it: ~A" line)))
      (check-equal before (state-revision (kernel-state k)) "canonical state unchanged")
      (check-equal '() (admitted-receipts (kernel-state k)) "no receipt was written"))

    ;; (3) A result that names another recipient is not the configured
    ;; verifier's result (SPEC-WORK.md:3859).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn"
                            :command (operator-verifier-command "rowan" "receipt-9"))
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (check-equal nil (staged-input-valid-p staged)
                     "a result for another recipient is not a valid stage")
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged))
          (check-equal nil okp "the receipt is refused")
          (check-equal 2 code "that refusal is exit 2")
          (ok (search "provenance unverified" line) "the refusal names it: ~A" line))))

    ;; (4) The three derived fields are the session's own half and are refused
    ;; when a plain request carries them (SPEC-WORK.md:3860-3862).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn"
                            :command (operator-verifier-command "glenn" "receipt-1"))
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (ok (staged-input-valid-p staged) "the configured process vouches for the body")
        (dolist (carried (list (list :sender "glenn")
                               (list :receipt-digest digest)
                               (list :effect :received)))
          (multiple-value-bind (okp line code)
              (submit k (apply #'ack-request :provenance pointer
                                             :provenance-sha256 digest
                                             :staged staged
                                             :request (format nil "ack-carried-~A"
                                                              (string-downcase
                                                               (symbol-name (first carried))))
                                             carried))
            (check-equal nil okp "a plain request carrying the session's own half is refused")
            (check-equal 2 code "that refusal is exit 2")
            (ok (search "session's own half" line)
                "the refusal names the session's own half: ~A" line))))
      (check-equal '() (admitted-receipts (kernel-state k)) "no receipt was written"))

    ;; (5) The verifier result a receipt needs: the configured recipient
    ;; identity, a stable receipt id and the digest of the received bytes; the
    ;; session derives :sender, :receipt-digest and :effect from it, and the one
    ;; writer admits one envelope (SPEC-WORK.md:3859-3865).
    (let* ((k (receipt-kernel))
           (before (state-revision (kernel-state k))))
      (configure-verifier k :recipient "glenn"
                            :command (operator-verifier-command "glenn" "receipt-1"))
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (ok (staged-input-valid-p staged) "a full verifier result stages")
        (check-equal "glenn" (getf (staged-input-result staged) :recipient)
                     "the result names the configured recipient")
        (check-equal "receipt-1" (getf (staged-input-result staged) :receipt-id)
                     "the result carries a stable receipt id")
        (check-equal digest (staged-input-digest staged)
                     "the staged digest is the digest of the received bytes")
        (check-equal body (staged-input-bytes staged) "the stage holds the bytes it read")
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer :provenance-sha256 digest
                                   :staged staged :request "ack-ok"))
          (ok okp "a verified received receipt is admitted: ~A" line)
          (check-equal 0 code "the verified receipt exits 0")
          (ok (search "ACKNOWLEDGE OK" line) "it prints the ACKNOWLEDGE OK line: ~A" line)
          (ok (search "stage=received" line) "the line names the stage: ~A" line)
          (ok (search "effect=received" line) "the line names the effect: ~A" line)
          (ok (search "offer=o-1" line) "the line names the offer: ~A" line)))
      (let ((receipt (admitted-receipt (kernel-state k) "receipt-1")))
        (ok receipt "the admitted receipt is in the ledger")
        (check-equal "glenn" (getf receipt :sender)
                     "the sender is derived from the verifier's recipient")
        (check-equal digest (getf receipt :receipt-digest)
                     "the receipt digest is the verifier's digest of the bytes")
        (check-equal :received (getf receipt :effect)
                     "the effect is the session's own half")
        (check-equal pointer (getf receipt :provenance)
                     "the receipt names the provenance body it was verified from"))
      (ok (> (state-revision (kernel-state k)) before)
          "the admitted envelope moved the revision")
      ;; The offered payload's own digest must name the bytes the verifier read.
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :provenance pointer
                                   :provenance-sha256 (sha256-hex "other bytes")
                                   :staged staged :reply "receipt-2"
                                   :request "ack-mismatch"))
          (check-equal nil okp "a --provenance-sha256 naming other bytes is refused")
          (check-equal 2 code "the mismatch refusal is exit 2")
          (ok (search "invalid stage" line) "the refusal names the stage: ~A" line)))
      (check-equal 1 (length (admitted-receipts (kernel-state k)))
                   "exactly one receipt was admitted"))

    ;; (6) The grammar's own shape: `--stage accepted` requires --by and
    ;; --default, `--stage received` refuses both (SPEC-WORK.md:2295).
    (let ((k (receipt-kernel)))
      (configure-verifier k :recipient "glenn"
                            :command (operator-verifier-command "glenn" "receipt-1"))
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :stage :accepted :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :request "ack-accepted-bare"))
          (check-equal nil okp "--stage accepted without --by and --default is refused")
          (check-equal 2 code "that refusal is exit 2")
          (ok (search "--by and --default" line) "the refusal names them: ~A" line))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :stage :received :lease-by "30m"
                                   :lease-default "release" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :request "ack-received-lease"))
          (check-equal nil okp "--stage received with --by and --default is refused")
          (check-equal 2 code "that refusal is exit 2")
          (ok (search "--by and --default" line) "the refusal names them: ~A" line))
        ;; An acceptance stands on a verified delivery: the received receipt is
        ;; admitted first (SPEC-WORK.md:3853-3855).
        (multiple-value-bind (okp line)
            (submit k (ack-request :stage :received :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :reply "receipt-2" :request "ack-received-first"))
          (ok okp "the received receipt is admitted first: ~A" line)))
      (let ((staged (stage-receipt k :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line code)
            (submit k (ack-request :stage :accepted :lease-by "30m"
                                   :lease-default "release" :provenance pointer
                                   :provenance-sha256 digest :staged staged
                                   :reply "receipt-3" :request "ack-accepted"))
          (ok okp "--stage accepted with both is admitted: ~A" line)
          (check-equal 0 code "it exits 0")
          (ok (search "effect=accepted" line) "the accepted effect is named: ~A" line))))))

;;; ------------------------------------------------------------------
;;; receipt-admission-is-journalled-and-replays    SPEC-WORK.md:307,3863-3868
;;; ------------------------------------------------------------------

(deftest "receipt-admission-is-journalled-and-replays" "docs/SPEC-WORK.md:307,3857-3868"
    "expected=one-receipt-durably-recorded-before-install;close-reopen-replay-reads-the-same-ledger;one-request-id-one-receipt"
  (let* ((body "receipt for o-7 accepted by glenn")
         (digest (sha256-hex body))
         (path (write-provenance-file (test-provenance-path "durable") body))
         (pointer (format nil "file:~A" path))
         (command (operator-verifier-command "glenn" "receipt-7"))
         (init-digest (root-digest (make-seed-state *seed*)))
         (journal-path (test-journal-path "receipt-admission"))
         (j1 (open-file-journal journal-path :initial-state-hash init-digest)))
    (unwind-protect
         (let ((k1 (receipt-kernel :offer "o-7" :attempt "a-7" :journal j1)))
           (configure-verifier k1 :recipient "glenn" :command command)
           (let ((staged (stage-receipt k1 :provenance pointer :recipient "glenn")))
             (multiple-value-bind (okp line code)
                 (submit k1 (ack-request :offer "o-7" :attempt "a-7" :reply "receipt-7"
                                         :provenance pointer :provenance-sha256 digest
                                         :staged staged :request "ack-durable-1"))
               (ok okp "the receipt is admitted over a real file journal: ~A" line)
               (check-equal 0 code "it exits 0"))
             ;; The same request id replays to the one receipt it already wrote
             ;; (SPEC-WORK.md:315).
             (multiple-value-bind (okp line code)
                 (submit k1 (ack-request :offer "o-7" :attempt "a-7" :reply "receipt-7"
                                         :provenance pointer :provenance-sha256 digest
                                         :staged staged :request "ack-durable-1"))
               (ok okp "a retry of the one request id answers OK: ~A" line)
               (check-equal 0 code "the retry exits 0"))
             (check-equal 1 (length (admitted-receipts (kernel-state k1)))
                          "the retry wrote a second receipt")
             ;; The same id with a changed payload is refused, never merged.
             (multiple-value-bind (okp line code)
                 (submit k1 (ack-request :offer "o-9" :attempt "a-7" :reply "receipt-7"
                                         :provenance pointer :provenance-sha256 digest
                                         :staged staged :request "ack-durable-1"))
               (check-equal nil okp "a reused id with a changed payload is refused")
               (check-equal 1 code "that refusal is exit 1")
               (ok (search "reused with a different payload" line)
                   "the refusal names it: ~A" line))))
      (close-file-journal j1))
    ;; Close, reopen, replay: the ledger is a projection over the journalled
    ;; envelopes, so a recovered session reads the same receipt.
    (let* ((j2 (open-file-journal journal-path :initial-state-hash init-digest))
           (k2 (make-kernel :state (make-seed-state *seed*) :journal j2)))
      (unwind-protect
           (progn
             (check-equal '() (admitted-receipts (kernel-state k2))
                          "a freshly seeded session holds no receipt")
             (replay-journal j2 k2)
             (let ((receipt (admitted-receipt (kernel-state k2) "receipt-7")))
               (ok receipt "the replayed session reads the receipt back")
               (check-equal "glenn" (getf receipt :sender) "the sender survived the replay")
               (check-equal digest (getf receipt :receipt-digest)
                            "the receipt digest survived the replay")
               (check-equal :received (getf receipt :effect) "the effect survived the replay")
               (check-equal "o-7" (getf receipt :offer) "the offer survived the replay"))
             (check-equal 1 (length (admitted-receipts (kernel-state k2)))
                          "the replay read exactly one receipt"))
        (close-file-journal j2)))
    ;; The fake journal's twin: the same admission over the ordering journal,
    ;; which tests ordering and not durability.
    (let ((k3 (receipt-kernel :offer "o-7" :attempt "a-7")))
      (configure-verifier k3 :recipient "glenn" :command command)
      (let ((staged (stage-receipt k3 :provenance pointer :recipient "glenn")))
        (multiple-value-bind (okp line)
            (submit k3 (ack-request :offer "o-7" :attempt "a-7" :reply "receipt-7"
                                    :provenance pointer :provenance-sha256 digest
                                    :staged staged :request "ack-fake-1"))
          (ok okp "the same admission over the ordering journal: ~A" line)))
      (check-equal 1 (length (admitted-receipts (kernel-state k3)))
                   "the fake-journal twin wrote the one receipt")
      ;; The receipt is in the state's own history, which is what the
      ;; reconstruction replays.
      (let ((rebuilt (reconstruct-state (canonical-string (state-canonical-form (kernel-state k3))))))
        (ok (admitted-receipt rebuilt "receipt-7")
            "a full independent reconstruction reads the receipt back")))))
