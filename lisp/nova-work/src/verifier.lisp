;;;; verifier.lisp --- the operator-configured verifier and staged admission
;;;; (docs/SPEC-WORK.md:3848-3861, 5837-5840), and the `verify` verb and the
;;;; verification cache (SPEC-WORK.md:1242-1338, :2302, :5368-5371, :5490-5491,
;;;; :5650-5653).
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
;;;;
;;;; The `verify` verb and the verification cache carry the pure model: a
;;;; resolver registry keyed by the command string the operator named, a cache
;;;; of raw resolutions keyed by the pointer, the subject and the resolver's
;;;; identity, and the derived per-evidence verdicts the verb prints. Nothing
;;;; here starts a session, a socket or a subprocess; see README.md for the
;;;; boundary.
;;;;
;;;; The cache holds raw resolutions, never verdicts (SPEC-WORK.md:1294). One
;;;; raw fact answers two criteria with two verdicts, and a `correct` or an
;;;; `accept` edit changes a verdict with no fetch at all (:1326-1338).

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

;;;; ------------------------------------------------------------------
;;;; The `verify` verb and the verification cache (SPEC-WORK.md:1242-1338,
;;;; :2302, :5368-5371, :5490-5491, :5650-5653).
;;;; ------------------------------------------------------------------

;;; ------------------------------------------------------------------
;;; Resolvers and the resolver seam
;;; ------------------------------------------------------------------
;;;
;;; A resolver's identity is the command string `session start` was given after
;;; the `=` in `--resolver <scheme>=<command>`, verbatim and unnormalized
;;; (SPEC-WORK.md:1295-1301). The command itself is run outside the mutation
;;; loop by the transport, which is not in this slice, so executing it is a
;;; seam: `fetch-resolver-fact` is the protocol and the one in-process method
;;; runs a configured Lisp function instead of the operator's command.

(define-condition verification-unreachable (error)
  ((what :initarg :what :reader verification-unreachable-what))
  (:report (lambda (c s) (write-string (verification-unreachable-what c) s)))
  (:documentation "A pointer the fetch cannot reach; never cached (:1263)."))

(defstruct (verification-resolver
            (:constructor make-verification-resolver (scheme command &key function)))
  ;; the scheme token the pointer names, e.g. \"test\"
  scheme
  ;; the command string after `=`; the resolver's identity and cache-key field
  command
  ;; the configured resolver body, invoked by the in-process seam
  function)

(defgeneric fetch-resolver-fact (resolver pointer subject &key timeout)
  (:documentation
   "seam: the operator's configured resolver command, run outside the mutation
loop and never through a shell (SPEC-WORK.md:1254-1256). The one method here
runs the resolver's configured in-process function and returns (values FACT
STAMP); a real session runs the command directly. Signals VERIFICATION-UNREACHABLE
when the answer is not one the specification accepts."))

(defmethod fetch-resolver-fact ((resolver verification-resolver) pointer subject
                                &key timeout)
  (declare (ignore timeout))
  (let ((function (verification-resolver-function resolver)))
    (unless function
      (error 'verification-unreachable
             :what (format nil "resolver ~A has no configured implementation"
                           (verification-resolver-command resolver))))
    (multiple-value-bind (fact stamp) (funcall function pointer subject)
      (unless (and (member fact '(:holds :absent)) (stringp stamp) (plusp (length stamp)))
        (error 'verification-unreachable
               :what (format nil "resolver ~A did not establish one line"
                             (verification-resolver-command resolver))))
      (values fact stamp))))

;;; ------------------------------------------------------------------
;;; Raw resolutions and the cache
;;; ------------------------------------------------------------------

(defstruct (verification-fact
            (:constructor make-verification-fact (pointer subject resolver fact stamp)))
  pointer subject resolver fact stamp)

(defstruct (verification-cache
            (:constructor %make-verification-cache (&key resolvers)))
  ;; (pointer subject resolver) -> raw resolution
  (facts (make-hash-table :test #'equal))
  ;; the resolver identities this reader was started with; a fact attributed to
  ;; a resolver not named here is counted unverified (:1311-1316).
  (resolvers '()))

(defun make-verification-cache (&key resolvers)
  "Build an empty verification cache. RESOLVERS, when given, is the set of
resolver identities a snapshot header names; a lookup then accepts a fact only
where its resolver identity is one of them (SPEC-WORK.md:1311-1316)."
  (%make-verification-cache :resolvers resolvers))

(defun verification-cache-key (pointer subject resolver)
  "The cache key: the pointer, the subject the resolver was passed, and the
identity of the resolver that answered it (SPEC-WORK.md:1294-1301)."
  (list pointer subject resolver))

(defun verification-cache-store (cache pointer subject resolver fact stamp)
  "Store one raw resolution under (POINTER SUBJECT RESOLVER) and return it."
  (setf (gethash (verification-cache-key pointer subject resolver)
                 (verification-cache-facts cache))
        (make-verification-fact pointer subject resolver fact stamp)))

(defun verification-cache-lookup (cache pointer subject resolver)
  "The raw resolution under (POINTER SUBJECT RESOLVER), or nil. A reader given a
resolver set accepts a fact only where its resolver identity is named there."
  (let ((named (verification-cache-resolvers cache)))
    (when (or (null named) (member resolver named :test #'equal))
      (gethash (verification-cache-key pointer subject resolver)
               (verification-cache-facts cache)))))

(defun verification-cache-size (cache)
  (hash-table-count (verification-cache-facts cache)))

;;; ------------------------------------------------------------------
;;; The session, the evidence and the verb
;;; ------------------------------------------------------------------

(defstruct (verification-session
            (:constructor make-verification-session
                (&key cache resolvers offline max-fetch fetch-timeout source-revision)))
  ;; the session's one cache, named by `session start --cache` (:1320-1325)
  cache
  ;; the operator's resolvers, one per scheme this session may fetch (:1251-1253)
  resolvers
  ;; `verify --offline`: derive from cached facts, fetch nothing (:1291-1292)
  offline
  ;; `--max-fetch <n>`: the fetch budget, one resolver run per cache key (:1289-1293)
  max-fetch
  ;; `--fetch-timeout <seconds>`: the resolver's own bound
  fetch-timeout
  ;; the node's current source revision, which makes a stale `:against` local
  source-revision)

(defstruct (verify-evidence
            (:constructor make-verify-evidence
                (event-id &key pointer criterion subject against generation
                             attestation node)))
  event-id pointer criterion subject against generation attestation node)

(defun pointer-scheme (pointer)
  "The scheme token a pointer names, before its first colon, or nil."
  (let ((colon (and (stringp pointer) (position #\: pointer))))
    (and colon (subseq pointer 0 colon))))

(defun pointer-revision (pointer)
  "The `@<sha>` a pointer carries, or nil."
  (let ((at (and (stringp pointer) (position #\@ pointer :from-end t))))
    (and at (subseq pointer (1+ at)))))

(defun verify-evidence-stale-p (session evidence)
  "Stale evidence is local and needs no fetch (SPEC-WORK.md:1338): its `:against`
is not the node's current source revision."
  (let ((current (verification-session-source-revision session))
        (against (verify-evidence-against evidence)))
    (and current against (not (equal current against)))))

(defun verify-qualifies-p (evidence)
  "Whether the pointer's scheme can qualify the criterion's kind, before the raw
fact is read (SPEC-WORK.md:1278-1286)."
  (let* ((pointer (verify-evidence-pointer evidence))
         (against (verify-evidence-against evidence))
         (scheme (pointer-scheme pointer))
         (revision (pointer-revision pointer))
         (criterion (verify-evidence-criterion evidence)))
    (case criterion
      (:test (equal scheme "test"))
      (:job (and (equal scheme "run") revision against (string= revision against)))
      (:merged (equal scheme "pr"))
      (:attested
       (let ((attestation (verify-evidence-attestation evidence)))
         (and attestation
              (let ((reviewer (getf attestation :reviewer)))
                (and (stringp reviewer) (plusp (length reviewer))))
              (equal (getf attestation :result-pointer) pointer)
              (equal (getf attestation :revision) against))))
      (t nil))))

(defun verify (session evidence &key node)
  "Run the `verify` verb over EVIDENCE (a list of VERIFY-EVIDENCE), returning
(values COUNT-LINE ROW-LINES EXIT-CODE). Rows are one per evidence event under
NODE; COUNT-LINE is `VERIFY OK` when `unverified=0` and `VERIFY FAIL` above zero
(SPEC-WORK.md:5368-5371, :5490-5491). `verify` names no cache and no resolver of
its own: both come from the session, and a `--max-fetch` or `--fetch-timeout`
under `--offline` is refused (SPEC-WORK.md:1289-1292, :1320-1325)."
  (when (and (verification-session-offline session)
             (or (verification-session-max-fetch session)
                 (verification-session-fetch-timeout session)))
    (error 'unsupported-input
           :what "verify: --max-fetch and --fetch-timeout are refused under --offline"))
  (let ((cache (verification-session-cache session))
        (resolvers (verification-session-resolvers session))
        (source (verification-session-source-revision session))
        (fetched 0)
        (cached 0)
        (verified 0)
        (unverified 0)
        (stale 0)
        (pointers (make-hash-table :test #'equal))
        (rows '()))
    (dolist (event evidence)
      (when (or (null node) (equal node (verify-evidence-node event)))
        (let* ((pointer (verify-evidence-pointer event))
               (subject (verify-evidence-subject event))
               (scheme (pointer-scheme pointer))
               (resolver (and scheme
                              (find scheme resolvers
                                    :key #'verification-resolver-scheme
                                    :test #'equal)))
               (identity (and resolver (verification-resolver-command resolver)))
               (fact (and resolver
                          (verification-cache-lookup cache pointer subject identity)))
               (reachable fact)
               (stale-p (and source (verify-evidence-against event)
                             (not (equal source (verify-evidence-against event))))))
          (setf (gethash pointer pointers) t)
          ;; Stale evidence is local and fetches nothing (:1338).
          (unless stale-p
            (when fact (incf cached))
            (cond
              ;; answered from the cache: a raw fact, never a verdict
              (fact)
              ((null resolver))
              ((verification-session-offline session))
              ((and (verification-session-max-fetch session)
                    (>= fetched (verification-session-max-fetch session))))
              (t
               (incf fetched)
               (handler-case
                   (multiple-value-bind (resolved stamp)
                       (fetch-resolver-fact resolver pointer subject
                                            :timeout (verification-session-fetch-timeout session))
                     (setf fact (verification-cache-store cache pointer subject identity
                                                          resolved stamp)
                           reachable t))
                 (error ()
                   ;; any answer the specification does not accept is
                   ;; unreachable, and an unreachable answer is never cached
                   ;; (:1263).
                   (setf reachable nil))))))
          (let ((verdict (cond (stale-p :stale)
                               ((not reachable) :unverified)
                               ((not (eq (verification-fact-fact fact) :holds)) :unverified)
                               ((not (verify-qualifies-p event)) :unverified)
                               (t :verified)))
                (at (if (and reachable (not stale-p))
                        (verification-fact-stamp fact)
                        "-")))
            (ecase verdict
              (:verified (incf verified))
              (:unverified (incf unverified))
              (:stale (incf stale)))
            (push (format nil "VERIFY ROW ~A pointer=~A verdict=~A at=~A"
                          (verify-evidence-event-id event) pointer
                          (string-downcase (symbol-name verdict)) at)
                  rows)))))
    (setf rows (nreverse rows))
    (let* ((emitted (reduce #'+ rows :key #'length :initial-value 0))
           (head (if (zerop unverified) "OK" "FAIL"))
           (line (format nil "VERIFY ~A pointers=~D verified=~D unverified=~D stale=~D fetched=~D cached=~D pushed=- emitted=~D"
                         head (hash-table-count pointers) verified unverified stale
                         fetched cached emitted)))
      (values line rows (if (zerop unverified) 0 1)))))
