;;;; acceptance.lisp --- the five slice-1 cases.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md at
;;;; 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64 it comes from, and carries either
;;;; the acceptance table's own expected= string or the executable invariant the
;;;; table names where that row has none.

(in-package #:nova-work/tests)

(defparameter *seed*
  '((:id "acme/work"       :type :work-set :parent nil               :state :unknown)
    (:id "acme/work/f1"    :type :feature  :parent "acme/work"       :state :unknown)
    (:id "acme/work/f1/t1" :type :task     :parent "acme/work/f1"    :state :doing)
    (:id "acme/work/f1/t2" :type :task     :parent "acme/work/f1"    :state :review)
    (:id "acme/work/f2"    :type :feature  :parent "acme/work"       :state :unknown))
  "Five canonical item ids, all in O at the seed: |O| = 5.")

(defun fresh (&key (journal (make-ordering-journal)) (rev-base 1))
  (make-kernel :state (make-seed-state *seed*) :journal journal :rev-base rev-base))

(defun close-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "shipped")
                           (evidence '("ev-1")) (request "req-1")
                           (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :state-to-done :node node :by by :reason reason :evidence evidence
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun reopen-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "regressed")
                            (request "req-2") (stamp "2026-09-14T13:00:00Z")
                            (generation-owner "gen-4"))
  (list :verb :event-reopen :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun independent-open-count (state)
  "Walk every node and count the ones in O. This is the full count the counter
is compared against; it is never the path `query --ask size` takes."
  (let ((n 0))
    (dolist (node *seed*)
      (when (eq :o (node-branch state (getf node :id))) (incf n)))
    n))

;;; ------------------------------------------------------------------
;;; 1. supported-subset format-determinism      SPEC-WORK.md:3345
;;; ------------------------------------------------------------------

(deftest "supported-subset-format-determinism" "docs/SPEC-WORK.md:3345"
    "expected=bytes=identical,absent!=empty!=empty-string,bignum=exact,escapes=exact,nfc!=nfd"
  ;; The digest primitive itself, against the published vectors.
  (check-string= "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
                 (sha256-hex "") "sha256 of the empty string")
  (check-string= "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
                 (sha256-hex "abc") "sha256 of abc")
  ;; One state and schema produce identical canonical bytes.
  (let ((form '(:kind :transition :node "acme/work/f1/t1" :by "rowan"
                :to :done :reason "shipped" :blocked-by (:absent) :evidence ("ev-1"))))
    (check-string=
     "(:kind :transition :node \"acme/work/f1/t1\" :by \"rowan\" :to :done :reason \"shipped\" :blocked-by (:absent) :evidence (\"ev-1\"))"
     (canonical-string form) "canonical bytes of a transition event")
    (check-string= (canonical-string form) (canonical-string form)
                   "two serializations of one form"))
  ;; null, absent and empty stay distinct: three serializations, no two alike.
  (check-string= "(:absent)" (canonical-string +absent+) "absent")
  (check-string= "()" (canonical-string '()) "empty list")
  (check-string= "\"\"" (canonical-string "") "empty string")
  (let ((a (sha256-hex (canonical-string +absent+)))
        (e (sha256-hex (canonical-string '())))
        (s (sha256-hex (canonical-string ""))))
    (ok (and (string/= a e) (string/= a s) (string/= e s))
        "absent, empty list and empty string digested alike: ~A ~A ~A" a e s))
  ;; Large integers, timestamps, escaping and multiline text survive.
  (check-string= "9007199254740993" (canonical-string 9007199254740993) "integer above 2^53")
  (check-string= "-9007199254740993" (canonical-string -9007199254740993) "negative bignum")
  (check-string= "\"2026-09-14T12:00:00Z\"" (canonical-string "2026-09-14T12:00:00Z") "stamp")
  (check-string= "\"a\\\"b\\\\c\"" (canonical-string "a\"b\\c") "escaping")
  (let* ((multi (format nil "one~%two"))
         (text (canonical-string (list multi))))
    (check-equal (list multi) (read-restricted text) "multiline text round trip"))
  ;; Unicode normalisation differences survive rather than being folded away.
  (let* ((nfc (concatenate 'string "caf" (string (code-char #xE9))))
         (nfd (concatenate 'string "cafe" (string (code-char #x301)))))
    (ok (string/= (sha256-hex (canonical-string nfc))
                  (sha256-hex (canonical-string nfd)))
        "NFC and NFD digested alike"))
  ;; Unsupported input refuses; it never serializes as something else.
  (handler-case (progn (canonical-string 'a-plain-symbol)
                       (fail "a non-keyword symbol serialized instead of refusing"))
    (restricted-data-violation () t))
  (handler-case (progn (read-restricted "(:kind #.(error \"x\"))")
                       (fail "a dispatch macro was read instead of refused"))
    (restricted-data-violation () t)))

;;; ------------------------------------------------------------------
;;; 2. settle-outside-the-digest                SPEC-WORK.md:3122
;;; ------------------------------------------------------------------

(deftest "settle-outside-the-digest" "docs/SPEC-WORK.md:3122"
    "expected=digests=1,stamps=2,settle-ids=2,retry=original-ok-line,retry-applied=0,changed-payload=refused"
  (let* ((ka (fresh :rev-base 100))
         (kb (fresh :rev-base 900)))
    (multiple-value-bind (ok-a line-a code-a env-a)
        (submit ka (close-request :request "req-1" :stamp "2026-09-14T12:00:00Z"))
      (multiple-value-bind (ok-b line-b code-b env-b)
          (submit kb (close-request :request "req-9" :stamp "2026-09-14T18:30:00Z"))
        (declare (ignore line-b))
        (ok ok-a "first close refused: ~A" line-a)
        (ok ok-b "second close refused")
        (check-equal 0 code-a "exit code of the accepted close")
        (check-equal 0 code-b "exit code of the accepted close")
        ;; One request, one digest, from two independent serializations.
        (check-string= (getf env-a :digest) (getf env-b :digest)
                       "two builds digested one request to two values")
        ;; The session's own half differs on both benches and is not digested.
        (let ((sa (getf env-a :settle)) (sb (getf env-b :settle)))
          (ok (string/= (event-id sa) (event-id sb))
              "the two settle event ids are the same: ~A" (event-id sa))
          (ok (string/= (work-event-stamp sa) (work-event-stamp sb))
              "the two settle stamps are the same")
          (check-equal :settle (work-event-kind sa) "the session's own event kind")
          (ok (work-event-session-written-p sa) "the settle is not marked session-written")))
      ;; The retry is answered by the original OK line and applies nothing.
      (let ((open-before (state-open-count (kernel-state ka)))
            (history-before (length (state-history (kernel-state ka))))
            (rows-before (length (state-closed-rows (kernel-state ka)))))
        (multiple-value-bind (ok2 line2 code2)
            (submit ka (close-request :request "req-1" :stamp "2026-09-14T12:44:00Z"))
          (ok ok2 "the retry was refused: ~A" line2)
          (check-equal 0 code2 "the retry's exit code")
          (check-string= line-a line2 "the retry was not answered by the original OK line"))
        (check-equal open-before (state-open-count (kernel-state ka)) "|O| after the retry")
        (check-equal history-before (length (state-history (kernel-state ka))) "history after the retry")
        (check-equal rows-before (length (state-closed-rows (kernel-state ka))) "closed rows after the retry")
        ;; The same id with a different payload is refused at exit 1.
        (multiple-value-bind (ok3 line3 code3)
            (submit ka (close-request :request "req-1" :reason "a different reason"))
          (ok (not ok3) "a reused id with a changed payload was accepted")
          (check-equal 1 code3 "the changed-payload exit code")
          (check-string= "STATE FAIL request=req-1: reused with a different payload" line3
                         "the changed-payload refusal line"))
        (check-equal open-before (state-open-count (kernel-state ka))
                     "|O| after the changed-payload refusal")
        (check-equal history-before (length (state-history (kernel-state ka)))
                     "history after the changed-payload refusal")))))

;;; ------------------------------------------------------------------
;;; 3. open-count-is-read-not-computed          SPEC-WORK.md:3229
;;; ------------------------------------------------------------------

(deftest "open-count-is-read-not-computed" "docs/SPEC-WORK.md:3229"
    "expected=visits=0,parses=0,replays=0,counter=independent-full-count,separate-counters=yes"
  (let ((k (fresh)))
    (check-equal 5 (state-open-count (kernel-state k)) "|O| at the seed")
    ;; A close.
    (ok (submit k (close-request :request "req-1")) "close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "|O| after the close")
    (check-equal 1 (state-closed-count (kernel-state k)) "|C| after the close")
    ;; Mutate, then ask repeatedly: zero visits, zero parses, zero replays.
    (with-instrumentation
      (dotimes (i 100)
        (multiple-value-bind (open unit scope line) (ask-size k)
          (declare (ignore unit scope))
          (check-equal 4 open "the counter the ask read")
          (ok (search "open=4" line) "the QUERY OK line does not carry open=: ~A" line)
          (ok (search "unit=" line) "the QUERY OK line does not carry unit=: ~A" line)
          (ok (search "scope=" line) "the QUERY OK line does not carry scope=: ~A" line)))
      (check-equal 0 *visits* "node visits during 100 size asks")
      (check-equal 0 *parses* "parses during 100 size asks")
      (check-equal 0 *replays* "replays during 100 size asks"))
    (check-equal (independent-open-count (kernel-state k)) (state-open-count (kernel-state k))
                 "the counter against an independent full count after a close")
    ;; A reopen.
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (check-equal 5 (state-open-count (kernel-state k)) "|O| after the reopen")
    (check-equal 0 (state-closed-count (kernel-state k)) "|C| after the reopen")
    (with-instrumentation
      (ask-size k)
      (check-equal 0 *visits* "node visits during the ask after a reopen")
      (check-equal 0 *parses* "parses during the ask after a reopen")
      (check-equal 0 *replays* "replays during the ask after a reopen"))
    (check-equal (independent-open-count (kernel-state k)) (state-open-count (kernel-state k))
                 "the counter against an independent full count after a reopen")
    ;; The container counters beneath the root are maintained on write too.
    (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2"
                                 :evidence '("ev-2")))
        "second close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "|O| after the second close")
    (check-equal 2 (node-open-count (kernel-state k) "acme/work/f1")
                 "the container's own open count")
    (check-equal 4 (node-open-count (kernel-state k) "acme/work")
                 "the work set's own open count")
    ;; The open-issue and open-leaf counters are separate and neither is |O|.
    (check-equal 0 (open-issue-count k) "the open linked-issue counter")
    (check-equal 1 (open-leaf-count k) "the open leaf-task counter")
    (ok (/= (open-leaf-count k) (state-open-count (kernel-state k)))
        "the leaf counter was silently labelled |O|")))

;;; ------------------------------------------------------------------
;;; 4. full independent reconstruction after close then revive
;;;    SPEC-WORK.md:3348 (indexes-and-counters); rows per :3104
;;; ------------------------------------------------------------------

(deftest "reconstruction-after-close-and-revive" "docs/SPEC-WORK.md:3348"
    "expected=root-digest=equal,open=equal,closed=equal,container-counters=equal,rows=append-only"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let ((rows-after-settle (copy-seq (state-closed-rows (kernel-state k)))))
      (ok (submit k (reopen-request :request "req-2")) "reopen refused")
      (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2" :evidence '("ev-2")))
          "second close refused")
      ;; C is append-only: the settle's row is still there, byte for byte.
      (let ((rows (state-closed-rows (kernel-state k))))
        (check-equal 3 (length rows) "one row per transition")
        (check-equal (first rows-after-settle) (first rows) "the first row was rewritten")
        (ok (= (length rows) (length (remove-duplicates rows :test #'equal)))
            "two closed rows share a key")))
    ;; Reconstruct from the canonical bytes alone.
    (let* ((live (kernel-state k))
           (text (canonical-string (state-canonical-form live)))
           (rebuilt (with-instrumentation
                      (prog1 (reconstruct-state text)
                        (ok (plusp *parses*) "the reconstruction parsed nothing")
                        (ok (plusp *replays*) "the reconstruction replayed nothing")))))
      (check-string= (root-digest live) (root-digest rebuilt) "the reconstructed root digest")
      (check-equal (state-open-count live) (state-open-count rebuilt) "|O| after reconstruction")
      (check-equal (state-closed-count live) (state-closed-count rebuilt) "|C| after reconstruction")
      (check-equal (state-revision live) (state-revision rebuilt) "the revision after reconstruction")
      (dolist (id '("acme/work" "acme/work/f1" "acme/work/f2"))
        (check-equal (node-open-count live id) (node-open-count rebuilt id)
                     (format nil "the container counter for ~A" id)))
      (dolist (node *seed*)
        (let ((id (getf node :id)))
          (check-equal (node-state live id) (node-state rebuilt id)
                       (format nil "the state of ~A" id))
          (check-equal (node-branch live id) (node-branch rebuilt id)
                       (format nil "the branch of ~A" id))))
      (check-equal (state-closed-rows live) (state-closed-rows rebuilt)
                   "the closed rows after reconstruction"))))

;;; ------------------------------------------------------------------
;;; 5. all-or-none two-event candidate application
;;;    SPEC-WORK.md:3346 (atomic-mutation)
;;; ------------------------------------------------------------------

(deftest "two-event-candidate-is-all-or-none" "docs/SPEC-WORK.md:3346"
    "expected=events=2,request-ids=1,on-reject:applied=0,digest=unchanged,history=unchanged,rows=unchanged,journal=empty"
  ;; The accepted case: one envelope, two events, one request id.
  (let ((k (fresh)))
    (multiple-value-bind (okp line code env) (submit k (close-request :request "req-1"))
      (declare (ignore line))
      (ok okp "close refused") (check-equal 0 code "exit code")
      (let ((events (getf env :events)))
        (check-equal 2 (length events) "the envelope's event count")
        (check-equal :transition (work-event-kind (first events)) "the requester's event kind")
        (check-equal :settle (work-event-kind (second events)) "the session's event kind")
        (ok (not (work-event-session-written-p (first events)))
            "the requester's event was marked session-written")
        (check-equal 1 (length (remove-duplicates (mapcar #'work-event-request events)
                                                  :test #'string=))
                     "the envelope carried more than one request id")
        (ok (= 1 (- (work-event-rev (second events)) (work-event-rev (first events))))
            "the two events did not take consecutive revisions")))
    ;; The reopen envelope is the same shape.
    (multiple-value-bind (okp line code env) (submit k (reopen-request :request "req-2"))
      (declare (ignore line code))
      (ok okp "reopen refused")
      (let ((events (getf env :events)))
        (check-equal 2 (length events) "the reopen envelope's event count")
        (check-equal :reopen (work-event-kind (first events)) "the requester's reopen kind")
        (check-equal :revive (work-event-kind (second events)) "the session's revive kind"))))
  ;; The injected acceptance failure: nothing applied.
  (let* ((journal (make-rejecting-journal :reject-on "req-x"))
         (k (fresh :journal journal))
         (before-digest (root-digest (kernel-state k)))
         (before-open (state-open-count (kernel-state k)))
         (before-history (length (state-history (kernel-state k))))
         (before-rows (length (state-closed-rows (kernel-state k))))
         (before-rev (state-revision (kernel-state k))))
    (multiple-value-bind (okp line code) (submit k (close-request :request "req-x"))
      (ok (not okp) "a rejected envelope was applied")
      (check-equal 1 code "the rejection's exit code")
      (ok (search "journal" line) "the refusal does not name the journal: ~A" line))
    (check-string= before-digest (root-digest (kernel-state k)) "the root digest after a rejection")
    (check-equal before-open (state-open-count (kernel-state k)) "|O| after a rejection")
    (check-equal before-history (length (state-history (kernel-state k))) "history after a rejection")
    (check-equal before-rows (length (state-closed-rows (kernel-state k))) "closed rows after a rejection")
    (check-equal before-rev (state-revision (kernel-state k)) "the revision after a rejection")
    (check-equal '() (journal-order journal) "the journal recorded a rejected envelope")
    ;; The same kernel still accepts the next legal request, so the refusal
    ;; left no half-applied candidate behind.
    (ok (submit k (close-request :request "req-y")) "the kernel refused after a clean rejection")
    (check-equal (1- before-open) (state-open-count (kernel-state k)) "|O| after the next accept"))
  ;; Unsupported inputs refuse; they never bypass the validation that is missing.
  (let ((k (fresh)))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :state-to-blocked :node "acme/work/f1/t1" :by "rowan"
                        :reason "r" :request "req-u" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok (not okp) "an unsupported verb was applied")
      (check-equal 2 code "an unsupported verb's exit code")
      (ok (search "unsupported" line) "the refusal does not say unsupported: ~A" line))
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "acme/work/f1" :request "req-v"))
      (declare (ignore line))
      (ok (not okp) "a close from :unknown by an unsupported edge was applied")
      (check-equal 1 code "an invalid transition's exit code"))
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "acme/work/nope" :request "req-w"))
      (declare (ignore line))
      (ok (not okp) "a close of a node that does not exist was applied")
      (check-equal 1 code "an unknown node's exit code"))
    (multiple-value-bind (okp line code)
        (submit k (reopen-request :node "acme/work/f1/t1" :request "req-z"))
      (declare (ignore line))
      (ok (not okp) "a reopen of an item that never left O was applied")
      (check-equal 1 code "an illegal reopen's exit code"))
    (multiple-value-bind (okp line code)
        (submit k (close-request :request "req-no-evidence" :evidence nil))
      (declare (ignore line))
      (ok (not okp) "a :to :done naming no evidence was applied")
      (check-equal 1 code "a done-without-evidence exit code"))))
