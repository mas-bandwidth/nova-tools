;;;; verifier.lisp --- the operator-configured verifier and staged admission
;;;; (docs/SPEC-WORK.md:3848-3861, 5837-5840).
;;;;
;;;; A receipt needs a verifier result before the coordinator admits it. The
;;;; operator-configured verifier reads the provenance body outside the
;;;; mutation loop and returns the configured recipient identity, a stable
;;;; receipt id and the digest of the received bytes. Those bytes and that
;;;; validation result are an immutable stage; only the one writer admits one
;;;; envelope, and only after it revalidates --expect, the offer's immutable
;;;; tuple, the profile and the capacity at the expected revision. A stale or
;;;; failed stage writes no reservation, no lease, no W entry and no receipt,
;;;; and `status` answers while the stage runs (SPEC-WORK.md:3853-3859).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The operator-configured verifier (SPEC-WORK.md:3849-3856)
;;; ------------------------------------------------------------------

(defstruct (receipt-verifier
            (:constructor make-receipt-verifier (&key recipient verify)))
  "The operator configuration: the recipient identity a result must name, and
the reader that runs over the provenance body. VERIFY is a function of the
staged bytes; it returns a plist carrying at least :recipient, :receipt-id and
:digest, or NIL when it cannot vouch for the provenance."
  recipient verify)

(defun %present-value (value)
  "True when VALUE is a written, non-empty field (SPEC-WORK.md:328-337)."
  (and value (not (and (stringp value) (string= value "")))))

(defun verifier-result (verifier bytes)
  "Run VERIFIER over BYTES and return its raw result. A `receipt-verifier'
admits only a result that names its configured recipient, so a reader that
answers for another recipient is not the configured verifier's result."
  (cond
    ((receipt-verifier-p verifier)
     (let ((raw (and (receipt-verifier-verify verifier)
                     (funcall (receipt-verifier-verify verifier) bytes))))
       (when (and raw
                  (equal (getf raw :recipient) (receipt-verifier-recipient verifier)))
         raw)))
    ((functionp verifier) (funcall verifier bytes))
    (t nil)))

(defun verifier-result-valid-p (result bytes)
  "A verifier result is the session's own half only when it names the recipient,
a stable receipt id and the digest of the received bytes (SPEC-WORK.md:3851)."
  (and result
       (%present-value (getf result :recipient))
       (%present-value (getf result :receipt-id))
       (equal (getf result :digest) (sha256-hex bytes))))

;;; ------------------------------------------------------------------
;;; The immutable stage (SPEC-WORK.md:3853-3858)
;;; ------------------------------------------------------------------

(defstruct (staged-input
            (:constructor make-staged-input
                (&key bytes result valid-p rev reason digest)))
  "Immutable staged bytes and the validation result produced outside the
mutation loop. RESULT is the verifier's result; VALID-P is false when the
verifier could not vouch for the provenance; DIGEST is the digest of the
received bytes as the verifier answered it."
  bytes result valid-p rev reason digest)

(defun stage-provenance (bytes verifier &key (rev 0))
  "Run the operator-configured VERIFIER over the provenance BYTES outside the
mutation loop and freeze the immutable stage. A verifier outage, a result for
another recipient, or one that fails validation is an invalid stage that admits
nothing (SPEC-WORK.md:3852-3858)."
  (handler-case
      (let ((result (verifier-result verifier bytes)))
        (if (verifier-result-valid-p result bytes)
            (make-staged-input :bytes bytes :result result :valid-p t :rev rev
                               :digest (getf result :digest))
            (make-staged-input :bytes bytes :result nil :valid-p nil :rev rev
                               :reason "provenance unverified")))
    (error ()
      (make-staged-input :bytes bytes :result nil :valid-p nil :rev rev
                         :reason "provenance unverified"))))

(defun stage-payload (bytes)
  "Stage the offered payload outside the loop. Its reader produces the digest of
the bytes, which the writer matches against --payload-sha256 before admitting
one envelope (SPEC-WORK.md:3853-3856)."
  (make-staged-input :bytes bytes :result (list :digest (sha256-hex bytes))
                     :valid-p t :rev 0 :digest (sha256-hex bytes)))
