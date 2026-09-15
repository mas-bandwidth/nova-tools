;;;; acceptance.lisp --- the five slice-1 cases.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md at
;;;; 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64 it comes from, and carries either
;;;; the acceptance table's own expected= string or the executable invariant the
;;;; table names where that row has none.

(in-package #:nova-work/tests)

(defparameter *seed*
  '((:id "acme/work"       :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/f1"    :type :feature  :parent "acme/work"    :state :unknown)
    (:id "acme/work/f1/t1" :type :task     :parent "acme/work/f1" :state :doing
     :links ("https://github.com/acme/work/issues/11"
             "https://github.com/acme/work/issues/12"))
    (:id "acme/work/f1/t2" :type :task     :parent "acme/work/f1" :state :review
     :links ("https://github.com/acme/work/issues/21"))
    (:id "acme/work/f2"    :type :feature  :parent "acme/work"    :state :unknown))
  "Five canonical item ids, all in O at the seed: |O| = 5, of which two are
leaf tasks and three are open linked issues (SPEC-WORK.md:1577 keeps the three
counters apart).")

(defparameter *cyclic-seed*
  '((:id "a" :type :task :parent "b" :state :doing)
    (:id "b" :type :task :parent "a" :state :doing))
  "a -> b -> a. SPEC-WORK.md:3347 referential-integrity: cycles fail BEFORE
publication.")

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
    (restricted-data-violation () t))
  ;; The printer downcases a keyword's name, so a keyword whose name is not
  ;; already upper case would collide with one that is. It refuses instead.
  (handler-case (progn (canonical-string (intern "done" :keyword))
                       (fail "a lower-case keyword serialized instead of refusing"))
    (restricted-data-violation () t))
  (ok (string/= (canonical-string :done)
                (handler-case (canonical-string (intern "Done" :keyword))
                  (restricted-data-violation () "refused")))
      "a case-differing keyword printed the same bytes as :done")
  ;; Evaluation syntax and a malformed form refuse at the boundary, with the
  ;; byte offset SPEC-WORK.md:678-681 asks for -- never as a raw reader error.
  (dolist (bad '("(:a ,x)" "(:a `b)" "(:a 'b)" "(:a" "(:a))" "(:a |b|)"))
    (handler-case
        (progn (read-restricted bad)
               (fail "~S was read instead of refused" bad))
      (restricted-data-violation (c)
        (ok (search "byte" (princ-to-string c))
            "~S refused without a byte offset: ~A" bad c))))
  ;; A well-formed restricted form still reads.
  (check-equal '(:a 1 "b" (:absent) ()) (read-restricted "(:a 1 \"b\" (:absent) ())")
                "a well-formed restricted form"))

;;; ------------------------------------------------------------------
;;; 1b. legal line comments (SPEC-WORK.md:678-679)
;;; ------------------------------------------------------------------

(deftest "line-comments-accepted" "docs/SPEC-WORK.md:678-679"
    "expected=semicolon-starts-line-comment"
  (check-equal '(:X 1) (read-restricted "(:X 1) ; comment") "line comment skipped"))

;;; ------------------------------------------------------------------
;;; 1c. UTF-8 byte offsets (SPEC-WORK.md:680)
;;; ------------------------------------------------------------------

(deftest "utf8-byte-offsets" "docs/SPEC-WORK.md:680"
    "expected=byte-count-not-char-count"
  (check-equal '(:X "é") (read-restricted "(:X \"é\")") "unicode string round-trips"))

;;; ------------------------------------------------------------------
;;; 1d. Stella's comment-family reproductions (SPEC-WORK.md:678-679)
;;; ------------------------------------------------------------------

(deftest "comment-text-is-text" "docs/SPEC-WORK.md:678-679"
    "expected=forbidden-looking-comment-is-text"
  (check-equal '(:X 1)
               (read-restricted "(:X 1) ; #. forbidden-looking comment
")
               "a #. inside a comment is text, not a dispatch macro"))

(deftest "quote-in-comment-is-text" "docs/SPEC-WORK.md:678-679"
    "expected=quote-inside-comment-is-text"
  (check-equal '(:X 1)
               (read-restricted "(:X 1) ; \" in a comment
")
               "a quote inside a comment does not change string state"))

(deftest "comment-inside-form" "docs/SPEC-WORK.md:678-679"
    "expected=comment-inside-form-is-skipped"
  (check-equal '(:X 1)
               (read-restricted "(:X 1 ; #. forbidden
)")
               "a comment inside a form is skipped"))

(deftest "semicolon-in-string-is-literal" "docs/SPEC-WORK.md:678-679"
    "expected=semicolon-inside-string-stays-literal"
  (check-equal '(:X "a;b") (read-restricted "(:X \"a;b\")")
               "a semicolon inside a string is not a comment"))

(deftest "forbidden-after-unicode-comment" "docs/SPEC-WORK.md:678-680"
    "expected=quote-after-unicode-comment-refused-at-byte-16"
  (handler-case
      (progn (read-restricted "(:X 1) ; é end
'quote")
             (fail "a quote after a Unicode comment was read"))
    (restricted-data-violation (c)
      (ok (search "byte 16" (princ-to-string c))
          "quote byte offset after a Unicode comment wrong: ~A" c))))

;;; ------------------------------------------------------------------
;;; 1e. Stella's byte-offset reproductions (SPEC-WORK.md:680)
;;; ------------------------------------------------------------------

(deftest "eof-byte-offset-utf8" "docs/SPEC-WORK.md:680"
    "expected=eof-byte-8"
  (handler-case
      (progn (read-restricted "(:X \"é\"")
             (fail "(:X \"é\" was read instead of ending at EOF"))
    (restricted-data-violation (c)
      (ok (search "byte 8" (princ-to-string c))
          "EOF byte offset wrong: ~A" c))))

(deftest "trailing-byte-offset-utf8" "docs/SPEC-WORK.md:680"
    "expected=trailing-byte-11"
  (handler-case
      (progn (read-restricted "(:X \"é\") )")
             (fail "(:X \"é\") ) was read instead of refusing trailing bytes"))
    (restricted-data-violation (c)
      (ok (search "byte 11" (princ-to-string c))
          "trailing byte offset wrong: ~A" c))))

(deftest "dispatch-byte-offset-utf8" "docs/SPEC-WORK.md:680"
    "expected=dispatch-byte-9"
  (handler-case
      (progn (read-restricted "(:X \"é\" #.)")
             (fail "(:X \"é\" #.) was read instead of refusing the dispatch macro"))
    (restricted-data-violation (c)
      (ok (search "byte 9" (princ-to-string c))
          "dispatch byte offset wrong: ~A" c))))

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
    (check-equal 1 (open-issue-count k) "the linked-issue counter after the close")
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
    (check-equal 3 (open-issue-count k) "the linked-issue counter after the reopen")
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
    (check-equal 2 (open-issue-count k) "the open linked-issue counter")
    (check-equal 1 (open-leaf-count k) "the open leaf-task counter")
    (ok (/= (open-leaf-count k) (state-open-count (kernel-state k)))
        "the leaf counter was silently labelled |O|")
    (ok (/= (open-issue-count k) (state-open-count (kernel-state k)))
        "the linked-issue counter was silently labelled |O|")
    ;; The QUERY OK line prints the counters this ask measured, not a literal.
    (multiple-value-bind (open unit scope line) (ask-size k)
      (declare (ignore open unit scope))
      (ok (search "parses=0" line) "the ask did not print its measured parses: ~A" line)
      (ok (search "replays=0" line) "the ask did not print its measured replays: ~A" line))))

;;; ------------------------------------------------------------------
;;; 4. full independent reconstruction after close then revive
;;;    SPEC-WORK.md:3353 (indexes-and-counters); rows per :3103
;;; ------------------------------------------------------------------

(deftest "reconstruction-after-close-and-revive" "docs/SPEC-WORK.md:3353"
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
;;;    SPEC-WORK.md:3348 (atomic-mutation)
;;; ------------------------------------------------------------------

(deftest "two-event-candidate-is-all-or-none" "docs/SPEC-WORK.md:3348"
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
    ;; A container settles with its members; that cascade is out of slice 1, so
    ;; it refuses at exit 2 rather than closing a container on its own.
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "acme/work/f1" :request "req-v"))
      (ok (not okp) "a container close was applied")
      (check-equal 2 code "a container close's exit code")
      (ok (search "slice 1" line) "the refusal does not name the boundary: ~A" line))
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
      (check-equal 1 code "a done-without-evidence exit code"))
    ;; A reopened item lands at :todo, and :todo has no edge to :done, so the
    ;; second close is rule 10 and not a second settle.
    (ok (submit k (close-request :request "req-a1")) "close refused")
    (ok (submit k (reopen-request :request "req-a2")) "reopen refused")
    (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1")
                 "the state a reopen lands in")
    (multiple-value-bind (okp line code) (submit k (close-request :request "req-a3"))
      (ok (not okp) "a :todo item closed")
      (check-equal 1 code "an invalid transition's exit code")
      (ok (search "rule 10" line) "the refusal does not name rule 10: ~A" line))))

;;; ------------------------------------------------------------------
;;; 6. cycles fail before publication            SPEC-WORK.md:3347
;;; ------------------------------------------------------------------

(deftest "referential-integrity-refuses-a-cycle" "docs/SPEC-WORK.md:3347"
    "expected=cycle=refused-before-publication,terminates=yes,rule=3"
  (let ((refusal nil))
    (handler-case
        (sb-ext:with-timeout 10
          (make-seed-state *cyclic-seed*)
          (fail "a cyclic :parent chain was published"))
      (sb-ext:timeout ()
        (fail "a cyclic :parent chain neither refused nor terminated"))
      (unsupported-input (c) (setf refusal (princ-to-string c))))
    (ok refusal "no refusal was signalled")
    (ok (search "rule 3" refusal) "the refusal does not name rule 3: ~A" refusal))
  ;; A node that is its own parent is the same finding.
  (handler-case
      (sb-ext:with-timeout 10
        (make-seed-state '((:id "a" :type :task :parent "a" :state :doing)))
        (fail "a self-parenting node was published"))
    (sb-ext:timeout () (fail "a self-parenting node neither refused nor terminated"))
    (unsupported-input () t))
  ;; Rules 1 and 2 still refuse, and the acyclic seed still publishes.
  (handler-case
      (progn (make-seed-state '((:id "a" :type :task :parent nil :state :doing)
                                (:id "a" :type :task :parent nil :state :doing)))
             (fail "a duplicate id was published"))
    (unsupported-input (c) (ok (search "rule 1" (princ-to-string c))
                               "a duplicate id did not name rule 1")))
  (handler-case
      (progn (make-seed-state '((:id "a" :type :task :parent "nope" :state :doing)))
             (fail "a dangling parent was published"))
    (unsupported-input (c) (ok (search "rule 2" (princ-to-string c))
                               "a dangling parent did not name rule 2")))
  (check-equal 5 (state-open-count (make-seed-state *seed*)) "the acyclic seed still publishes"))

;;; ------------------------------------------------------------------
;;; 7. an id's newest closed row carries revived= and settles=
;;;    SPEC-WORK.md:3103; the requirement is :1222
;;; ------------------------------------------------------------------

(deftest "closed-rows-carry-revived-and-settles" "docs/SPEC-WORK.md:3103"
    "expected=settle:settles=1,revived=-;revive:revived=<rev>,settles=1;settle2:settles=2,earlier-rows=unchanged"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let* ((rows1 (state-closed-rows (kernel-state k)))
           (settle-row (first (last rows1))))
      (check-equal 1 (length rows1) "one row after the settle")
      (check-equal 1 (getf settle-row :settles) "settles= on the settle row")
      (check-string= "-" (getf settle-row :revived) "revived= on the settle row")
      (ok (submit k (reopen-request :request "req-2")) "reopen refused")
      (let* ((rows2 (state-closed-rows (kernel-state k)))
             (revive-row (first (last rows2))))
        (check-equal 2 (length rows2) "two rows after the revive")
        (check-equal settle-row (first rows2) "the settle row was rewritten by the revive")
        (check-equal :revive (getf revive-row :kind) "the newest row's kind")
        (check-equal (getf revive-row :rev) (getf revive-row :revived)
                     "revived= on the revive row is that event's own revision")
        (check-equal 1 (getf revive-row :settles) "settles= on the revive row")
        ;; A second id keeps its own chain.
        (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2"
                                     :evidence '("ev-2")))
            "the other close refused")
        (let ((other (first (last (state-closed-rows (kernel-state k))))))
          (check-equal 1 (getf other :settles) "the other id's own settles=")
          (check-string= "-" (getf other :revived) "the other id's own revived=")
          (check-equal settle-row (first (state-closed-rows (kernel-state k)))
                       "the first row moved when another id settled")))))
  ;; settles=2 -- the third row of SPEC-WORK.md:3104-3106 -- needs one id
  ;; settled twice, and a :reopen lands at :todo, which has no edge to :done
  ;; (:994-1005). Reaching it through the kernel's gate would need
  ;; `state --to doing`, which is outside this slice's transition subset, so it
  ;; is exercised HERE ON APPLY-EVENT, the primitive the live path and the
  ;; replay path share. This is the row counter, not the kernel's gate, and
  ;; README.md says so under partial coverage.
  (let* ((state (make-seed-state *seed*))
         (id "acme/work/f1/t1")
         (event (lambda (kind rev fields)
                  (make-work-event :kind kind :node id :by "rowan" :fields fields
                                   :stamp "2026-09-14T12:00:00Z" :clock :tool
                                   :request "req-p" :generation-owner "gen-4"
                                   :rev rev :session-written-p t))))
    (setf state (apply-event state (funcall event :settle 1 '(:disposition :done :reason "a"))))
    (setf state (apply-event state (funcall event :revive 2 '(:reason "b"))))
    (let ((first-two (copy-seq (state-closed-rows state))))
      (setf state (apply-event state (funcall event :settle 3 '(:disposition :done :reason "c"))))
      (let* ((rows (state-closed-rows state))
             (newest (first (last rows))))
        (check-equal 3 (length rows) "three rows after the second settle")
        (check-equal (first first-two) (first rows) "the first row moved")
        (check-equal (second first-two) (second rows) "the second row moved")
        (check-equal 2 (getf newest :settles) "settles= on the newest row")
        (check-string= "-" (getf newest :revived) "revived= on the newest row")))))

;;; ------------------------------------------------------------------
;;; 8. the journal is appended before the apply
;;;    SPEC-WORK.md:3348; the ordering is :307, the retry rests on it at :315
;;; ------------------------------------------------------------------

(deftest "journal-records-before-it-applies" "docs/SPEC-WORK.md:3348"
    "expected=record-then-apply,stop-between:record=present,applied=0,on-reject:record=absent,applied=0"
  ;; A stop injected between the journal append and the apply leaves the record
  ;; written and nothing applied. The reverse ordering cannot produce that.
  (let* ((journal (make-ordering-journal))
         (k (fresh :journal journal))
         (before-digest (root-digest (kernel-state k)))
         (before-open (state-open-count (kernel-state k)))
         (before-history (length (state-history (kernel-state k)))))
    (handler-case
        (let ((*before-apply-hook* (lambda (envelope)
                                     (declare (ignore envelope))
                                     (error "injected stop between append and apply"))))
          (submit k (close-request :request "req-1"))
          (fail "the injected stop did not fire"))
      (simple-error () t))
    (multiple-value-bind (found digest line) (journal-lookup journal "req-1")
      (ok (eq t found) "the journal holds no record for the stopped request")
      (ok (and (stringp digest) (plusp (length digest))) "the record carries no digest")
      (ok (and (stringp line) (search "STATE OK" line)) "the record carries no OK line"))
    (check-string= before-digest (root-digest (kernel-state k)) "the root digest after the stop")
    (check-equal before-open (state-open-count (kernel-state k)) "|O| after the stop")
    (check-equal before-history (length (state-history (kernel-state k))) "history after the stop")
    (check-equal '("req-1") (journal-order journal) "the journal's order after the stop"))
  ;; An acceptance failure leaves no record and no apply.
  (let* ((journal (make-rejecting-journal :reject-on "req-x"))
         (k (fresh :journal journal)))
    (submit k (close-request :request "req-x"))
    (multiple-value-bind (found) (journal-lookup journal "req-x")
      (ok (not (eq t found)) "a refused envelope was recorded"))
    (check-equal '() (journal-order journal) "the journal's order after a refusal")))

;;; ------------------------------------------------------------------
;;; 9. a kernel over a reconstructed state reissues no id
;;;    SPEC-WORK.md:3353; the row key is :1216-1218, the rebuild is :1578
;;; ------------------------------------------------------------------

(deftest "reconstructed-kernel-does-not-reissue-ids" "docs/SPEC-WORK.md:3353"
    "expected=ids=distinct,row-keys=distinct,revision=monotone"
  (let* ((k (fresh))
         (first-id nil))
    (multiple-value-bind (okp line code env) (submit k (close-request :request "req-1"))
      (declare (ignore line code))
      (ok okp "close refused")
      (setf first-id (event-id (first (getf env :events)))))
    (let* ((text (canonical-string (state-canonical-form (kernel-state k))))
           (rebuilt (reconstruct-state text))
           (k2 (make-kernel :state rebuilt :journal (make-ordering-journal))))
      (ok (> (state-revision rebuilt) 0) "the reconstructed revision is zero")
      (multiple-value-bind (okp line code env) (submit k2 (reopen-request :request "req-2"))
        (declare (ignore line code))
        (ok okp "reopen over the reconstructed state refused")
        (let ((next-id (event-id (first (getf env :events)))))
          (ok (string/= first-id next-id)
              "the reconstructed kernel reissued event id ~A" next-id)))
      (ok (> (state-revision (kernel-state k2)) (state-revision rebuilt))
          "the revision did not move past the reconstructed one")
      (let ((keys (mapcar (lambda (row) (getf row :key))
                          (state-closed-rows (kernel-state k2)))))
        (check-equal 2 (length keys) "two rows after the reopen")
        (ok (= (length keys) (length (remove-duplicates keys :test #'string=)))
            "two closed rows share a key: ~S" keys)))
    ;; A revision base at or below the state's own revision is refused rather
    ;; than reissued.
    (let ((rebuilt (reconstruct-state
                    (canonical-string (state-canonical-form (kernel-state k))))))
      (handler-case (progn (make-kernel :state rebuilt :rev-base 1)
                           (fail "a revision base below the state's own was accepted"))
        (unsupported-input () t)))))

;;; ------------------------------------------------------------------
;;; 10. request fields refuse rather than drop
;;;     SPEC-WORK.md:3227 every-field-has-an-owning-verb
;;; ------------------------------------------------------------------

(deftest "request-fields-refuse-rather-than-drop" "docs/SPEC-WORK.md:3227"
    "expected=unknown-key=refused,forbidden-field=refused,note-pointer=refused,digest-differs=n/a"
  (let ((k (fresh)))
    ;; An unknown key is refused, never dropped into an identical digest.
    (multiple-value-bind (okp line code)
        (submit k (append (close-request :request "req-1") (list :priority "high")))
      (ok (not okp) "an unknown request key was accepted")
      (check-equal 2 code "an unknown key's exit code")
      (ok (search "priority" line) "the refusal does not name the key: ~A" line))
    ;; A field the transition forbids is refused, never silently overwritten.
    (multiple-value-bind (okp line code)
        (submit k (append (close-request :request "req-2")
                          (list :blocked-by "acme/work/f1/t2")))
      (ok (not okp) "a :blocked-by on a :to :done was accepted")
      (check-equal 2 code "a forbidden field's exit code")
      (ok (search "blocked-by" line) "the refusal does not name the field: ~A" line))
    ;; A note: pointer is never completion evidence (SPEC-WORK.md:1052, rule 5).
    (multiple-value-bind (okp line code)
        (submit k (close-request :request "req-3" :evidence '("note:bus:emma-841138a3b056")))
      (ok (not okp) "a note: pointer was accepted as evidence")
      (check-equal 1 code "a note: evidence exit code")
      (ok (search "rule 5" line) "the refusal does not name rule 5: ~A" line))
    ;; An evidence entry that is not an id at all is refused.
    (multiple-value-bind (okp line code)
        (submit k (close-request :request "req-4" :evidence '("")))
      (declare (ignore line))
      (ok (not okp) "an empty evidence id was accepted")
      (check-equal 1 code "an empty evidence id's exit code"))
    ;; And the legal request still passes.
    (ok (submit k (close-request :request "req-5")) "the legal close refused")))

;;; ------------------------------------------------------------------
;;; 11. the bounded dedup store refuses past its bound
;;;     SPEC-WORK.md:3349 retry-protocol; the refusal is :517
;;; ------------------------------------------------------------------

(deftest "dedup-refuses-past-its-bound" "docs/SPEC-WORK.md:3349"
    "expected=within-bound=answered,past-bound=dedup-unavailable,never=treated-as-new"
  (let* ((journal (make-ordering-journal :capacity 2))
         (k (fresh :journal journal)))
    (ok (submit k (close-request :request "req-1")) "first close refused")
    (ok (submit k (close-request :request "req-2" :node "acme/work/f1/t2"
                                 :evidence '("ev-2")))
        "second close refused")
    ;; Within the bound, the retry is still answered by its original OK line.
    (multiple-value-bind (okp line code) (submit k (close-request :request "req-1"))
      (declare (ignore line))
      (ok okp "a retry inside the bound was refused")
      (check-equal 0 code "a retry inside the bound's exit code"))
    ;; A third record evicts the oldest; nothing is then reported as new.
    (ok (submit k (reopen-request :request "req-3")) "reopen refused")
    (multiple-value-bind (okp line code) (submit k (reopen-request :request "req-9"
                                                                  :node "acme/work/f1/t2"))
      (ok (not okp) "a request past the bound was treated as new")
      (check-equal 1 code "the past-bound exit code")
        (ok (search "dedup unavailable" line)
            "the refusal does not say dedup unavailable: ~A" line))))

;;; ------------------------------------------------------------------
;; 12. malformed trailing suffixes must be refused
;; ------------------------------------------------------------------

(deftest "malformed-trailing-unclosed-list" "docs/SPEC-WORK.md:678-679"
    "expected=refused-by-reader"
  (handler-case
      (progn (read-restricted "(:X 1) (")
             (fail "(:X 1) ( was read instead of refusing malformed trailing form"))
    (restricted-data-violation (c)
      (ok (search "trailing bytes" (princ-to-string c))
          "(:X 1) ( refused without trailing bytes message: ~A" c))))

(deftest "malformed-trailing-unclosed-form" "docs/SPEC-WORK.md:678-679"
    "expected=refused-by-reader"
  (handler-case
      (progn (read-restricted "(:X 1) (:Y")
             (fail "(:X 1) (:Y was read instead of refusing malformed trailing form"))
    (restricted-data-violation (c)
      (ok (search "trailing bytes" (princ-to-string c))
          "(:X 1) (:Y refused without trailing bytes message: ~A" c))))

(deftest "empty-suffix-accepted" "docs/SPEC-WORK.md:678-679"
    "expected=value-returned"
  (check-equal '(:X 1) (read-restricted "(:X 1)") "empty suffix accepted"))

(deftest "whitespace-suffix-accepted" "docs/SPEC-WORK.md:678-679"
    "expected=value-returned"
  (check-equal '(:X 1) (read-restricted "(:X 1)  ") "whitespace suffix accepted"))

(deftest "comment-suffix-accepted" "docs/SPEC-WORK.md:678-679"
    "expected=value-returned"
  (check-equal '(:X 1) (read-restricted "(:X 1) ; comment") "comment suffix accepted"))

(deftest "reader-eof-sentinel-is-not-payload" "docs/SPEC-WORK.md:678-681"
    "expected=trailing-keyword-refused;single-keyword=value-returned"
  (check-equal :END-OF-INPUT (read-restricted ":end-of-input")
               "valid keyword colliding with the internal EOF name")
  (handler-case
      (progn (read-restricted "(:x 1) :end-of-input")
             (fail "a forged EOF sentinel was accepted as trailing whitespace"))
    (restricted-data-violation (c)
      (ok (search "trailing bytes" (princ-to-string c))
          "forged EOF sentinel refused with the wrong diagnostic: ~A" c))))

(defun refusal-byte (condition)
  "Extract the diagnostic byte as an integer, so byte 1 cannot match byte 10."
  (let* ((message (princ-to-string condition))
         (marker (search "byte " message)))
    (and marker
         (parse-integer message :start (+ marker 5) :junk-allowed t))))

(deftest "unterminated-string-start-byte" "docs/SPEC-WORK.md:678-681"
    "expected=opening-quote-utf8-byte-9"
  (handler-case
      (progn (read-restricted "(:x \"é\" \"unterminated")
             (fail "unterminated string was read instead of refused"))
    (restricted-data-violation (c)
      (check-equal 9 (refusal-byte c)
                   "unterminated string opening-quote byte"))))

;;; ------------------------------------------------------------------
;; 13. forbidden payload tokens refuse before interning
;;     SPEC-WORK.md:678-681; validator rule 12 reader payload
;;; ------------------------------------------------------------------

(deftest "forbidden-token-boundary-before-interning" "docs/SPEC-WORK.md:678-681"
    "expected=symbols,ratios,floats,characters,dispatch-refused-at-token-byte;interned=0"
  (labels ((refuses-at (text byte)
             (handler-case
                 (progn (read-restricted text)
                        (fail "~S was read instead of refused" text))
               (restricted-data-violation (c)
                 (check-equal byte (refusal-byte c)
                              (format nil "~S forbidden token-start byte" text))))))
    ;; The byte is the forbidden token's first byte, including after a
    ;; multi-byte character earlier in the form.
    (dolist (case '(("foo" 0)
                    ("(:x foo)" 4)
                    ("nil" 0)
                    ("(:x t)" 4)
                    ("(:x \"é\" cl:car)" 9)
                    ("1/2" 0)
                    ("(:x 1.0)" 4)
                    ("#\\a" 0)
                    ("(:x \"é\" #'car)" 9)))
      (refuses-at (first case) (second case)))

    ;; A refused bare symbol must not mutate the otherwise empty reader
    ;; package by interning itself before validation rejects it.
    (let ((name "NO-INTERN-READER-TOKEN-20260914"))
      (multiple-value-bind (symbol status) (find-symbol name "NOVA-WORK.READ")
        (declare (ignore symbol))
        (ok (null status) "the no-intern test token already exists"))
      (refuses-at name 0)
      (multiple-value-bind (symbol status) (find-symbol name "NOVA-WORK.READ")
        (declare (ignore symbol))
        (ok (null status) "a refused bare symbol was interned with status ~S" status)))

    ;; Adjacent approved forms and forbidden-looking text inside data/comments
    ;; remain data.
    (check-equal :FOO (read-restricted ":foo") "a keyword was refused")
    (check-equal -12 (read-restricted "-12") "a signed integer was refused")
    (check-equal 12 (read-restricted "+12") "a signed integer was refused")
    (check-equal 1 (read-restricted "1.") "a trailing-dot integer was refused")
    (check-equal -1 (read-restricted "-1.") "a signed trailing-dot integer was refused")
    (check-equal '(:X "foo cl:car 1/2 1.0")
                 (read-restricted "(:x \"foo cl:car 1/2 1.0\")")
                 "forbidden-looking string text was refused")
    (check-equal '(:X 1)
                 (read-restricted "(:x 1) ; foo cl:car 1/2 1.0")
                 "forbidden-looking comment text was refused")))

;;; ------------------------------------------------------------------
;;; 13. static required-member containers settle and revive on the path
;;; ------------------------------------------------------------------

(defun cascade-record-identities (records)
  "Ordered identities retain membership, multiplicity and append order."
  (mapcar (lambda (record)
            (list (getf record :kind) (getf record :node) (getf record :rev)))
          records))

(defun cascade-history-identities (state)
  (mapcar (lambda (record)
            (list (getf record :request)
                  (cascade-record-identities (getf record :events))))
          (state-history state)))

(defparameter *cascade-seed*
  '((:id "root"            :type :work-set :parent nil      :state :unknown)
    (:id "root/f"          :type :feature  :parent "root"   :state :unknown)
    (:id "root/f/a"        :type :task     :parent "root/f" :state :doing)
    (:id "root/f/b"        :type :task     :parent "root/f" :state :doing)
    (:id "root/f/optional" :type :task     :parent "root/f" :state :doing :required nil)
    (:id "root/empty"      :type :feature  :parent "root"   :state :unknown :required nil)
    (:id "root/g"          :type :feature  :parent "root"   :state :unknown)
    (:id "root/g/one"      :type :task     :parent "root/g" :state :doing)))

(deftest "containers-settle-with-their-members" "docs/SPEC-WORK.md:1272-1283,3128-3131"
    "expected=path-cascade;optional-and-empty-stay-open;one-envelope-and-request"
  (let ((k (make-kernel :state (make-seed-state *cascade-seed*))))
    (labels ((close-node (node request)
               (multiple-value-bind (okp line code envelope)
                   (submit k (close-request :node node :request request))
                 (declare (ignore line code))
                 (ok okp "close of ~A refused" node)
                 envelope)))
      (let ((first (close-node "root/f/a" "cascade-a")))
        (check-equal '("root/f/a" "root/f/a")
                     (mapcar #'work-event-node (getf first :events))
                     "a non-final member cascaded"))
      (check-equal :o (node-branch (kernel-state k) "root/f")
                   "feature settled with a required sibling open")
      (let ((feature (close-node "root/f/b" "cascade-b")))
        (check-equal '("root/f/b" "root/f/b" "root/f")
                     (mapcar #'work-event-node (getf feature :events))
                     "the feature settle path")
        (check-equal 1 (length (remove-duplicates
                                (mapcar #'work-event-request (getf feature :events))
                                :test #'string=))
                     "the feature cascade used more than one request"))
      (check-equal :c (node-branch (kernel-state k) "root/f")
                   "feature did not settle with all required members")
      (check-equal :o (node-branch (kernel-state k) "root")
                   "root settled with required sibling feature open")
      (let ((whole (close-node "root/g/one" "cascade-g")))
        (check-equal '("root/g/one" "root/g/one" "root/g" "root")
                     (mapcar #'work-event-node (getf whole :events))
                     "the multi-level settle path"))
      (check-equal :c (node-branch (kernel-state k) "root") "root did not settle")
      (check-equal :o (node-branch (kernel-state k) "root/f/optional")
                   "optional child was settled")
      (check-equal :o (node-branch (kernel-state k) "root/empty")
                   "empty optional container was settled")
      (check-equal 2 (state-open-count (kernel-state k))
                   "open count after the settle cascade")
      ;; Optional work may close and reopen in its own right. Since it is not
      ;; in the parent's required set, neither move changes settled ancestors.
      (close-node "root/f/optional" "cascade-optional-close")
      (check-equal :c (node-branch (kernel-state k) "root/f")
                   "optional child close revived its parent")
      (multiple-value-bind (okp line code optional-reopen)
          (submit k (reopen-request :node "root/f/optional"
                                    :request "cascade-optional-reopen"))
        (declare (ignore line code))
        (ok okp "optional child reopen refused")
        (check-equal '("root/f/optional" "root/f/optional")
                     (mapcar #'work-event-node (getf optional-reopen :events))
                     "optional child reopen cascaded"))
      (check-equal :c (node-branch (kernel-state k) "root/f")
                   "optional child reopen revived its parent")
      (check-equal :c (node-branch (kernel-state k) "root")
                   "optional child reopen revived its root")
      (multiple-value-bind (okp line code reopened)
          (submit k (reopen-request :node "root/f/b" :request "cascade-reopen"))
        (declare (ignore line code))
        (ok okp "required member reopen refused")
        (check-equal '("root/f/b" "root/f/b" "root/f" "root")
                     (mapcar #'work-event-node (getf reopened :events))
                     "the reverse revive path"))
      (check-equal :o (node-branch (kernel-state k) "root") "root did not revive")
      (check-equal :c (node-branch (kernel-state k) "root/g")
                   "unaffected sibling container revived")
      (check-equal 5 (state-open-count (kernel-state k))
                   "open count after the revive cascade")
      (check-equal
       '(("cascade-a" ((:transition "root/f/a" 1) (:settle "root/f/a" 2)))
         ("cascade-b" ((:transition "root/f/b" 3) (:settle "root/f/b" 4)
                       (:settle "root/f" 5)))
         ("cascade-g" ((:transition "root/g/one" 6) (:settle "root/g/one" 7)
                       (:settle "root/g" 8) (:settle "root" 9)))
         ("cascade-optional-close" ((:transition "root/f/optional" 10)
                                    (:settle "root/f/optional" 11)))
         ("cascade-optional-reopen" ((:reopen "root/f/optional" 12)
                                     (:revive "root/f/optional" 13)))
         ("cascade-reopen" ((:reopen "root/f/b" 14) (:revive "root/f/b" 15)
                            (:revive "root/f" 16) (:revive "root" 17))))
       (cascade-history-identities (kernel-state k))
       "exact history requests and event identities")
      (check-equal
       '((:settle "root/f/a" 2) (:settle "root/f/b" 4) (:settle "root/f" 5)
         (:settle "root/g/one" 7) (:settle "root/g" 8) (:settle "root" 9)
         (:settle "root/f/optional" 11) (:revive "root/f/optional" 13)
         (:revive "root/f/b" 15) (:revive "root/f" 16) (:revive "root" 17))
       (cascade-record-identities (state-closed-rows (kernel-state k)))
       "exact immutable settle and revive row identities")
      (let ((rebuilt (reconstruct-state
                      (canonical-string (state-canonical-form (kernel-state k))))))
        (check-string= (root-digest (kernel-state k)) (root-digest rebuilt)
                       "cascade reconstruction root")
        (check-equal (state-closed-rows (kernel-state k)) (state-closed-rows rebuilt)
                     "cascade reconstruction rows"))))
  ;; A required empty container is itself never complete and therefore blocks
  ;; its parent after every other required member settles.
  (let* ((seed '((:id "empty-root" :type :work-set :parent nil :state :unknown)
                 (:id "empty-root/empty" :type :feature :parent "empty-root" :state :unknown)
                 (:id "empty-root/task" :type :task :parent "empty-root" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "empty-root/task" :request "empty-required"))
        "sibling of required empty container refused")
    (check-equal :o (node-branch (kernel-state k) "empty-root/empty")
                 "required empty container settled")
    (check-equal :o (node-branch (kernel-state k) "empty-root")
                 "parent settled over a required empty container")))

(deftest "static-seed-required-is-a-boolean" "docs/SPEC-WORK.md:799"
    "expected=absent-is-true;nil-is-false;lookalikes-refused;input-unchanged"
  (dolist (bad '(:false "false" 0))
    (let* ((seed (list (list :id "root" :type :work-set :parent nil :state :unknown)
                       (list :id "root/bad" :type :task :parent "root"
                             :state :doing :required bad)))
           (before (copy-tree seed)))
      (handler-case
          (progn (make-seed-state seed)
                 (fail "malformed :required ~S was accepted" bad))
        (unsupported-input (c)
          (ok (search "required" (princ-to-string c))
              "malformed :required refusal did not name the field: ~A" c)))
      (check-equal before seed "seed mutated while refusing malformed :required")))
  ;; Absent defaults true, while explicit NIL is the internal seed spelling of
  ;; false. Closing the default-required child settles its parent despite the
  ;; optional sibling remaining open.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/required" :type :task :parent "root" :state :doing)
                 (:id "root/optional" :type :task :parent "root" :state :doing
                  :required nil)))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "root/required" :request "boolean-controls"))
        "default-required child close refused")
    (check-equal :c (node-branch (kernel-state k) "root")
                 "absent :required did not default true")
    (check-equal :o (node-branch (kernel-state k) "root/optional")
                 "explicit NIL did not remain optional")))

(deftest "container-cascade-journal-rejection-and-retry-stability" "docs/SPEC-WORK.md:1272-1283,3128-3131"
    "expected=journal-rejection-applies-0;accept-all;retry-applies-0"
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/f" :type :feature :parent "root" :state :unknown)
                 (:id "root/f/a" :type :task :parent "root/f" :state :doing)))
         (journal (make-rejecting-journal :reject-on "cascade-reject"))
         (k (make-kernel :state (make-seed-state seed) :journal journal))
         (before (root-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "root/f/a" :request "cascade-reject"))
      (declare (ignore line))
      (ok (not okp) "rejected cascade was accepted")
      (check-equal 1 code "rejected cascade exit code"))
    (check-string= before (root-digest (kernel-state k)) "root after cascade rejection")
    (check-equal 3 (state-open-count (kernel-state k)) "open after cascade rejection")
    (check-equal 0 (state-closed-count (kernel-state k)) "closed after cascade rejection")
    (check-equal '() (state-history (kernel-state k)) "history after cascade rejection")
    (check-equal '() (state-closed-rows (kernel-state k)) "rows after cascade rejection")
    (let ((request (close-request :node "root/f/a" :request "cascade-accept")))
      (multiple-value-bind (okp line code envelope) (submit k request)
        (declare (ignore line code))
        (ok okp "cascade after rejection refused")
        (check-equal '((:transition "root/f/a" 1) (:settle "root/f/a" 2)
                       (:settle "root/f" 3) (:settle "root" 4))
                     (cascade-record-identities
                      (mapcar #'event-record-form (getf envelope :events)))
                     "exact accepted cascade event identities"))
      (let ((accepted (root-digest (kernel-state k))))
        (multiple-value-bind (okp line code replay) (submit k request)
          (declare (ignore line code))
          (ok okp "same-request retry refused")
          (check-equal '() (getf replay :events) "retry applied cascade events"))
        (check-string= accepted (root-digest (kernel-state k)) "root after retry")
        (check-equal 0 (state-open-count (kernel-state k)) "open after retry")
        (check-equal 3 (state-closed-count (kernel-state k)) "closed after retry")
        (check-equal '(("cascade-accept"
                        ((:transition "root/f/a" 1) (:settle "root/f/a" 2)
                         (:settle "root/f" 3) (:settle "root" 4))))
                     (cascade-history-identities (kernel-state k))
                     "exact history after retry")
        (check-equal '((:settle "root/f/a" 2) (:settle "root/f" 3)
                       (:settle "root" 4))
                     (cascade-record-identities (state-closed-rows (kernel-state k)))
                     "exact rows after retry")))))

;;; State-to-doing closes the live-kernel lifecycle gap named in README.md.
;;; SPEC-WORK.md@7db3b95c:994-1005; unchanged in spec head 9c120a3b.
(defun doing-request (&key (node "t") (reason "starting") (request "start-1")
                          (evidence +absent+))
  (list :verb :state-to-doing :node node :by "stella" :reason reason
        :evidence evidence :request request :stamp "2026-09-14T22:00:00Z"
        :clock :tool :generation-owner "gen-1"))

(deftest "doing-admits-only-the-reviewed-incoming-edges" "SPEC-WORK.md:994-1005"
    "expected=allowed-edges-only;refusal-no-state-or-journal-change"
  (dolist (from '(:todo :blocked :review :cancel-requested :unknown
                 :doing :done :deferred :cancelled :superseded))
    (let* ((k (make-kernel :state (make-seed-state
                                 (list (list :id "t" :type :task :state from)))))
           (before (root-digest (kernel-state k)))
           (allowed (member from '(:todo :blocked :review :cancel-requested :unknown))))
      (multiple-value-bind (yes line code envelope) (submit k (doing-request))
        (if allowed
            (progn
              (ok yes "~A -> doing should pass: ~A" from line)
              (check-equal 0 code "accepted exit")
              (check-equal :doing (node-state (kernel-state k) "t") "state")
              (check-equal 1 (length (getf envelope :events)) "one requester event")
              (ok (null (getf envelope :settle)) "nonterminal transition has no settle")
              (check-equal 1 (state-open-count (kernel-state k)) "open unchanged")
              (check-equal 0 (state-closed-count (kernel-state k)) "closed unchanged"))
            (progn
              (ok (not yes) "~A -> doing must refuse" from)
              (check-equal 1 code "invalid edge exit")
              (check-string= before (root-digest (kernel-state k)) "refusal state")
              (check-equal nil (journal-order (kernel-journal k)) "refusal journal")))))))

(deftest "doing-from-unknown-needs-reason-or-evidence" "SPEC-WORK.md:994-1005"
    "expected=unknown-never-silently-initialized"
  (dolist (proof (list (list +absent+ +absent+ nil)
                      (list "" nil nil)
                      (list "observed started" +absent+ t)
                      (list +absent+ '("ev-start") t)
                      (list +absent+ '("") nil)))
    (destructuring-bind (reason evidence expected) proof
      (let ((k (make-kernel :state (make-seed-state '((:id "t" :type :task))))))
        (multiple-value-bind (yes line) (submit k (doing-request :reason reason :evidence evidence))
          (check-equal expected yes (format nil "unknown proof ~S: ~A" proof line)))))))

(deftest "doing-retry-and-journal-refusal-use-the-existing-boundary" "SPEC-WORK.md:315,3348-3349"
    "expected=one-event-on-retry;zero-events-on-journal-refusal"
  (let* ((k (make-kernel :state (make-seed-state '((:id "t" :type :task :state :todo)))))
         (request (doing-request)))
    (multiple-value-bind (yes line) (submit k request)
      (ok yes "first start")
      (let ((before (root-digest (kernel-state k))))
        (multiple-value-bind (again replay) (submit k request)
          (ok again "retry precedes now-invalid doing->doing validation")
          (check-string= line replay "original response"))
        (check-string= before (root-digest (kernel-state k)) "retry no mutation")
        (check-equal '("start-1") (journal-order (kernel-journal k)) "one journal entry")
        (multiple-value-bind (changed) (submit k (doing-request :reason "different"))
          (ok (not changed) "same ID with changed payload refuses")))))
  (let* ((k (make-kernel :state (make-seed-state '((:id "t" :type :task :state :todo)))
                         :journal (make-rejecting-journal :reject-on "start-1")))
         (before (root-digest (kernel-state k))))
    (multiple-value-bind (yes) (submit k (doing-request))
      (ok (not yes) "journal rejection"))
    (check-string= before (root-digest (kernel-state k)) "rejection no mutation")
    (check-equal nil (journal-order (kernel-journal k)) "rejection no journal entry")))

(deftest "doing-keeps-field-ownership-and-container-boundaries" "SPEC-WORK.md:3227"
    "expected=no-generic-field-or-container-state-escape"
  (dolist (request (list (append (doing-request) '(:blocked-by "other"))
                         (append (doing-request) '(:to :done))
                         (doing-request :node "root")))
    (let* ((k (make-kernel :state (make-seed-state
                                 '((:id "root" :type :work-set)
                                   (:id "t" :type :task :parent "root" :state :todo)))))
           (before (root-digest (kernel-state k))))
      (multiple-value-bind (yes line code) (submit k request)
        (declare (ignore line))
        (ok (not yes) "unsupported field/container refuses")
        (check-equal 2 code "unsupported exit"))
      (check-string= before (root-digest (kernel-state k)) "unsupported no mutation")
      (check-equal nil (journal-order (kernel-journal k)) "unsupported no journal"))))

(deftest "second-settle-through-submit-keeps-container-history" "SPEC-WORK.md:3103-3106,1272-1283"
    "expected=todo-doing-done-reopen-doing-done;settles=2;reconstruction-equal"
  (let ((k (make-kernel :state (make-seed-state
                              '((:id "root" :type :work-set)
                                (:id "root/f" :type :feature :parent "root")
                                (:id "root/f/t" :type :task :parent "root/f" :state :todo))))))
    (flet ((admit (request)
             (multiple-value-bind (yes line) (submit k request)
               (ok yes "cycle mutation: ~A" line))))
      (admit (doing-request :node "root/f/t" :request "start-first"))
      (admit (close-request :node "root/f/t" :request "finish-first"))
      (check-equal 0 (state-open-count (kernel-state k)) "first cascade closes all")
      (admit (reopen-request :node "root/f/t" :request "reopen-cycle"))
      (check-equal 3 (state-open-count (kernel-state k)) "revival opens all")
      (admit (doing-request :node "root/f/t" :request "start-second"))
      (check-equal 3 (state-open-count (kernel-state k)) "doing does not change O")
      (admit (close-request :node "root/f/t" :request "finish-second")))
    (check-equal 0 (state-open-count (kernel-state k)) "second cascade closes all")
    (check-equal 3 (state-closed-count (kernel-state k)) "three canonical closed IDs")
    (check-equal 9 (length (state-closed-rows (kernel-state k))) "two settles and one revive per ID")
    (dolist (id '("root" "root/f" "root/f/t"))
      (let ((rows (remove-if-not (lambda (r) (equal id (getf r :node)))
                                 (state-closed-rows (kernel-state k)))))
        (check-equal '(1 1 2) (mapcar (lambda (r) (getf r :settles)) rows) "settle history")))
    (let ((reconstructed (reconstruct-state (canonical-string (state-canonical-form (kernel-state k))))))
      (check-string= (root-digest (kernel-state k)) (root-digest reconstructed) "reconstruction")
      (check-equal (state-history (kernel-state k)) (state-history reconstructed) "history retained")
      (check-equal (state-closed-rows (kernel-state k)) (state-closed-rows reconstructed) "closed rows retained"))))

;;;; ------------------------------------------------------------------
;;;; Durable Filesystem Journal and Replay Acceptance
;;;; ------------------------------------------------------------------

(defvar *journal-test-counter* 0)

(defun test-journal-path (name)
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames "nova-work-test-journals/" base)))
    (ensure-directories-exist dir)
    (format nil "~A~A-~D-~D.journal" (namestring dir) name (get-universal-time) (incf *journal-test-counter*))))

(defun file-byte-count (path)
  (with-open-file (in path :direction :input :element-type '(unsigned-byte 8))
    (file-length in)))

(defun file-sha256-hex (path)
  (with-open-file (in path :direction :input :element-type '(unsigned-byte 8))
    (let ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8))))
      (read-sequence bytes in)
      (sha256-hex bytes))))

(deftest "durable-journal-pre-append-failure-writes-nothing" "docs/SPEC-WORK.md:307,3348"
    "expected=pre-append-failure-writes-nothing;file-size-unchanged;entries=0"
  (let* ((path (test-journal-path "pre-append"))
         (k (fresh :journal (open-file-journal path :initial-state-hash (root-digest (make-seed-state *seed*))))))
    (unwind-protect
         (let ((initial-size (file-byte-count path)))
           ;; Case 1: Validation failure prior to journal accept
           (multiple-value-bind (ok-1 line-1) (submit k (close-request :node "nonexistent-node" :request "req-fail-1"))
             (declare (ignore line-1))
             (ok (not ok-1) "validation failure refused")
             (check-equal initial-size (file-byte-count path) "validation failure wrote nothing"))
           ;; Case 2: Injected journal acceptance refusal
           (setf (journal-reject-on (kernel-journal k)) "req-fail-2")
           (multiple-value-bind (ok-2 line-2) (submit k (doing-request :node "acme/work/f1/t1" :request "req-fail-2"))
             (declare (ignore line-2))
             (ok (not ok-2) "journal rejection refused")
             (check-equal initial-size (file-byte-count path) "journal rejection wrote nothing"))
           ;; Verify journal still has 0 entries
           (check-equal nil (journal-order (kernel-journal k)) "zero entries recorded"))
      (close-file-journal (kernel-journal k))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-append-plus-lost-reply-recovers-once" "docs/SPEC-WORK.md:307,315,3348"
    "expected=lost-reply-recovers-once;original-response-equal;state-mutates-once"
  (let* ((path (test-journal-path "lost-reply"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (fresh :journal j1))
         (req (close-request :request "req-lost-reply")))
    (unwind-protect
         (progn
           ;; 1. Simulate crash immediately after durable journal record before reply
           (let ((*before-apply-hook* (lambda (env)
                                       (declare (ignore env))
                                       (error "simulated crash after durable append before apply/reply"))))
             (handler-case (submit k1 req)
               (error () nil)))
           (close-file-journal j1)
           ;; Verify file now holds durable record
           (ok (> (file-byte-count path) 100) "journal record was durable")
           ;; 2. Process restarts: new session opens existing journal and replays it
           (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                  (k2 (fresh :journal j2)))
             (unwind-protect
                  (progn
                    (multiple-value-bind (replayed-k events-count records-count)
                        (replay-journal j2 k2)
                      (declare (ignore replayed-k))
                      (check-equal 1 records-count "one record replayed")
                      (ok (> events-count 0) "cascade events replayed"))
                    (let ((state-after-replay (root-digest (kernel-state k2))))
                      ;; 3. Client retries lost request
                      (multiple-value-bind (retry-ok retry-line retry-code retry-env)
                          (submit k2 req)
                        (ok retry-ok "retry succeeds")
                        (check-equal 0 retry-code "retry exit 0")
                        (ok (getf retry-env :replayed) "retry marked replayed")
                        (check-equal '() (getf retry-env :events) "retry generated zero new events")
                        (check-string= state-after-replay (root-digest (kernel-state k2)) "retry did not mutate state")
                        (check-equal '("req-lost-reply") (journal-order j2) "journal still holds single entry"))))
               (close-file-journal j2))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-changed-payload-refuses" "docs/SPEC-WORK.md:315,3348"
    "expected=same-request-id-different-payload-refused;no-state-change;no-journal-append"
  (let* ((path (test-journal-path "changed-payload"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash initial-hash))
         (k (fresh :journal j)))
    (unwind-protect
         (let ((req1 (close-request :node "acme/work/f1/t1" :request "req-conflict" :reason "first-reason")))
           (multiple-value-bind (ok1 line1) (submit k req1)
             (ok ok1 (format nil "initial submit: ~A" line1)))
           (let ((size-after-first (file-byte-count path))
                 (state-after-first (root-digest (kernel-state k)))
                 (req2 (close-request :node "acme/work/f1/t1" :request "req-conflict" :reason "different-conflicting-reason")))
             (multiple-value-bind (ok2 line2 code2) (submit k req2)
               (ok (not ok2) "changed payload refused")
               (check-equal 1 code2 "exit code 1 on conflict")
               (ok (search "reused with a different payload" line2) "refusal message explains collision")
               (check-equal size-after-first (file-byte-count path) "no journal append on refusal")
               (check-string= state-after-first (root-digest (kernel-state k)) "state unchanged on refusal"))))
      (close-file-journal j)
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-multi-event-envelope-never-partly-publishes" "docs/SPEC-WORK.md:1272-1283,3348"
    "expected=multi-event-atomic-frame;all-or-none;single-checksum-envelope"
  (let* ((path (test-journal-path "multi-event"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash initial-hash))
         (k (fresh :journal j)))
    (unwind-protect
         (progn
           (multiple-value-bind (ok1 line1 code1 env)
               (submit k (close-request :node "acme/work/f1/t1" :request "req-cascade"))
             (declare (ignore line1 code1))
             (ok ok1 "submit cascade")
             (ok (>= (length (getf env :events)) 1) "events in candidate"))
           ;; Read back journal frames directly: verify exactly one frame exists after header
           (with-open-file (in path :direction :input :element-type 'character)
             (let ((header (read-header in path initial-hash))
                   (frame (read-record-frame in path 1))
                   (trailer (read-line in nil :eof)))
               (declare (ignore header))
               (ok frame "frame 1 exists")
               (check-equal 1 (getf (rest frame) :seq) "seq is 1")
               (let ((events (getf (getf (rest frame) :record) :events)))
                 (ok (listp events) "events is list")
                 (check-equal 2 (length events) "two events in atomic frame")))))
      (close-file-journal j)
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-corrupt-data-refuses-without-truncation" "docs/SPEC-WORK.md:3348,3357"
    "expected=corrupt-or-torn-data-refuses;zero-truncation;file-bytes-preserved"
  (let* ((path (test-journal-path "corrupt-data"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           ;; 1. Create a journal and populate with 2 valid entries
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (let ((k (fresh :journal j)))
               (submit k (doing-request :node "acme/work/f1/t1" :request "req-1"))
               (submit k (close-request :node "acme/work/f1/t1" :request "req-2")))
             (close-file-journal j))
           ;; Record pre-corruption byte count and SHA-256
           (let ((valid-bytes (file-byte-count path)))
             ;; Case A: Append torn partial frame bytes to file tail
             (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
               (write-string "(:frame :seq 3 :len 120 :checksum \"0000\"" out)
               (finish-output out))
             (let ((torn-bytes (file-byte-count path))
                   (torn-sha (file-sha256-hex path)))
               (ok (> torn-bytes valid-bytes) "torn bytes appended")
               ;; Attempt to open corrupt file: must signal journal-corrupt-data
               (let ((signaled nil))
                 (handler-case
                     (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data () (setf signaled t))
                   (error (c) (fail "unexpected error type: ~A" c)))
                 (ok signaled "journal-corrupt-data signaled on torn tail"))
               ;; CRITICAL INVARIANT: File is NOT truncated!
               (check-equal torn-bytes (file-byte-count path) "torn file byte count preserved without truncation")
               (check-string= torn-sha (file-sha256-hex path) "torn file SHA256 preserved bit-for-bit")))
           ;; Case B: Header mismatch
           (let ((mismatch-path (test-journal-path "header-mismatch")))
             (unwind-protect
                  (progn
                    (let ((j (open-file-journal mismatch-path :initial-state-hash "expected-hash-aaa")))
                      (close-file-journal j))
                    (let ((mismatch-signaled nil))
                      (handler-case
                          (open-file-journal mismatch-path :initial-state-hash "different-hash-bbb")
                        (journal-mismatch () (setf mismatch-signaled t)))
                      (ok mismatch-signaled "journal-mismatch signaled on wrong initial state")))
               (ignore-errors (delete-file mismatch-path)))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-replay-generates-no-fresh-ids" "docs/SPEC-WORK.md:3348,3353"
    "expected=replay-preserves-revisions-and-identities;zero-fresh-ids;state-exact"
  (let* ((path (test-journal-path "replay-identity"))
         (seed '((:id "root" :type :work-set)
                 (:id "root/f" :type :feature :parent "root")
                 (:id "root/f/t" :type :task :parent "root/f" :state :todo)))
         (initial-hash (root-digest (make-seed-state seed)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (make-kernel :state (make-seed-state seed) :journal j1)))
    (unwind-protect
         (progn
           ;; Execute a sequence of mutations on k1
           (submit k1 (doing-request :node "root/f/t" :request "start-1"))
           (submit k1 (close-request :node "root/f/t" :request "close-1"))
           (submit k1 (reopen-request :node "root/f/t" :request "reopen-1"))
           (submit k1 (doing-request :node "root/f/t" :request "start-2"))
           (submit k1 (close-request :node "root/f/t" :request "close-2"))
           (close-file-journal j1)
           (let ((final-digest (root-digest (kernel-state k1)))
                 (final-history (state-history (kernel-state k1)))
                 (final-rows (state-closed-rows (kernel-state k1)))
                 (final-rev (kernel-next-rev k1)))
             ;; Create a fresh kernel k2 with the original seed and replay j1 into it
             (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                    (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (replayed-k events records)
                          (replay-journal j2 k2)
                        (declare (ignore replayed-k))
                        (check-equal 5 records "five records replayed")
                        (ok (> events 5) "all events replayed"))
                      (check-string= final-digest (root-digest (kernel-state k2)) "replayed root digest exact")
                      (check-equal final-history (state-history (kernel-state k2)) "replayed history exact")
                      (check-equal final-rows (state-closed-rows (kernel-state k2)) "replayed closed rows exact")
                      (check-equal final-rev (kernel-next-rev k2) "replayed next-rev exact"))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-uncertain-write-refuses-until-recovery" "docs/SPEC-WORK.md:307,3348"
    "expected=uncertain-write-refuses-until-recovery;seq-unadvanced;record-preserved-on-reopen"
  (let* ((path (test-journal-path "uncertain-write"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash initial-hash :fail-sync-on "req-fail"))
         (k (fresh :journal j)))
    (unwind-protect
         (progn
           ;; 1. Submit valid req-1: succeeds, seq becomes 1
           (multiple-value-bind (ok1 line1) (submit k (close-request :node "acme/work/f1/t1" :request "req-1"))
             (declare (ignore line1))
             (ok ok1 "req-1 succeeds")
             (check-equal 1 (journal-seq j) "seq is 1 after req-1"))
           (let ((size-after-req1 (file-byte-count path)))
             ;; 2. Submit req-fail where sync failure is injected AFTER write and flush
             (let ((signaled nil))
               (handler-case
                   (submit k (reopen-request :node "acme/work/f1/t1" :request "req-fail"))
                 (journal-uncertain-write (c)
                   (declare (ignore c))
                   (setf signaled t)))
               (ok signaled "journal-uncertain-write signaled on post-flush sync failure"))
             ;; Verify seq was NOT advanced on live instance: remains 1!
             (check-equal 1 (journal-seq j) "seq was not advanced after failed sync")
             (ok (journal-uncertain-p j) "journal marked uncertain")
             ;; 3. Subsequent submit on same instance must be refused without writing
             (multiple-value-bind (ok3 line3) (submit k (reopen-request :node "acme/work/f1/t1" :request "req-3"))
               (ok (not ok3) "subsequent submit refused while uncertain")
               (ok (search "uncertain-write state" line3) "refusal cites uncertain state"))
             ;; 4. Complete frame was flushed to disk before sync failure: byte count strictly greater than size-after-req1
             (ok (> (file-byte-count path) size-after-req1) "frame was flushed to disk before sync failure")
             (close-file-journal j)
             ;; 5. Recovery via clean reopen: validates complete frames including req-fail, seq is 2!
             (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                    (k2 (fresh :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (replayed-k ev rec) (replay-journal j2 k2)
                        (declare (ignore replayed-k ev))
                        (check-equal 2 rec "recovered 2 complete records including uncertain append"))
                      (check-equal 2 (journal-seq j2) "recovered journal seq is 2")
                      (ok (not (journal-uncertain-p j2)) "recovered journal not uncertain")
                      ;; 6. Retry of uncertain request req-fail succeeds with original response from dedup!
                      (multiple-value-bind (retry-ok line-ret code-ret env-ret)
                          (submit k2 (reopen-request :node "acme/work/f1/t1" :request "req-fail"))
                        (declare (ignore line-ret code-ret))
                        (ok retry-ok "retry of uncertain request succeeds")
                        (ok (getf env-ret :replayed) "retry marked replayed"))
                      ;; 7. Subsequent new request after recovery succeeds and advances seq to 3
                      (multiple-value-bind (ok-new line-new)
                          (submit k2 (doing-request :node "acme/work/f1/t1" :request "req-after-recovery"))
                        (declare (ignore line-new))
                        (ok ok-new "request after recovery succeeds")
                        (check-equal 3 (journal-seq j2) "seq advances to 3 after recovery submit")))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-partial-write-refuses-without-truncation" "docs/SPEC-WORK.md:307,3348,3357"
    "expected=partial-write-signals-uncertain;untruncated-on-disk;reopen-refuses-corrupt"
  (let* ((path (test-journal-path "partial-write"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash initial-hash :fail-partial-write-on "req-torn"))
         (k (fresh :journal j)))
    (unwind-protect
         (progn
           ;; 1. Submit req-1: succeeds, seq becomes 1
           (multiple-value-bind (ok1 line1) (submit k (close-request :node "acme/work/f1/t1" :request "req-1"))
             (declare (ignore line1))
             (ok ok1 "req-1 succeeds")
             (check-equal 1 (journal-seq j) "seq is 1"))
           (let ((size-after-req1 (file-byte-count path)))
             ;; 2. Submit req-torn where partial write occurs
             (let ((signaled nil))
               (handler-case
                   (submit k (reopen-request :node "acme/work/f1/t1" :request "req-torn"))
                 (journal-uncertain-write (c)
                   (declare (ignore c))
                   (setf signaled t)))
               (ok signaled "journal-uncertain-write signaled on partial write"))
             ;; 3. Verify sequence was NOT advanced and instance is marked uncertain
             (check-equal 1 (journal-seq j) "seq not advanced on partial write")
             (ok (journal-uncertain-p j) "marked uncertain")
             ;; 4. Partial bytes were flushed to disk (strictly > size-after-req1)
             (let ((torn-size (file-byte-count path))
                   (torn-hash (file-sha256-hex path)))
               (ok (> torn-size size-after-req1) "partial frame written to disk")
               (close-file-journal j)
               ;; 5. Reopen must signal journal-corrupt-data
               (let ((corrupt-signaled nil))
                 (handler-case
                     (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data (c)
                     (declare (ignore c))
                     (setf corrupt-signaled t)))
                 (ok corrupt-signaled "reopen of partial frame signals journal-corrupt-data"))
               ;; 6. ZERO TRUNCATION: file bytes on disk are preserved exactly as left
               (check-equal torn-size (file-byte-count path) "file size not truncated by failed reopen")
               (check-string= torn-hash (file-sha256-hex path) "file bytes bit-for-bit preserved"))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-capacity-boundary-refuses-new-preserves-retained" "docs/SPEC-WORK.md:517,2117,3349"
    "expected=capacity-boundary-refuses;all-retained-retriable;memory-bounded"
  (let* ((path (test-journal-path "capacity-boundary"))
         (seed '((:id "root" :type :work-set)
                 (:id "root/f" :type :feature :parent "root")
                 (:id "root/f/t1" :type :task :parent "root/f" :state :todo)
                 (:id "root/f/t2" :type :task :parent "root/f" :state :todo)
                 (:id "root/f/t3" :type :task :parent "root/f" :state :todo)))
         (initial-hash (root-digest (make-seed-state seed)))
         (capacity 3)
         (j (open-file-journal path :capacity capacity :initial-state-hash initial-hash))
         (k (make-kernel :state (make-seed-state seed) :journal j)))
    (unwind-protect
         (progn
           ;; 1. Submit up to capacity (3 distinct requests)
           (multiple-value-bind (ok1 l1) (submit k (doing-request :node "root/f/t1" :request "req-cap-1"))
             (declare (ignore l1)) (ok ok1 "req 1 accepted"))
           (multiple-value-bind (ok2 l2) (submit k (doing-request :node "root/f/t2" :request "req-cap-2"))
             (declare (ignore l2)) (ok ok2 "req 2 accepted"))
           (multiple-value-bind (ok3 l3) (submit k (doing-request :node "root/f/t3" :request "req-cap-3"))
             (declare (ignore l3)) (ok ok3 "req 3 accepted"))
           (check-equal 3 (journal-seq j) "seq is 3")
           (let ((size-at-capacity (file-byte-count path))
                 (state-at-capacity (root-digest (kernel-state k))))
             ;; 2. Attempt request 4 (new distinct request past capacity): must be refused!
             (multiple-value-bind (ok4 line4 code4)
                 (submit k (close-request :node "root/f/t1" :request "req-cap-4"))
               (ok (not ok4) "request past capacity refused")
               (check-equal 1 code4 "exit code 1 on capacity refusal")
               (ok (search "capacity exceeded" line4) "refusal message explains capacity bound")
               (check-equal size-at-capacity (file-byte-count path) "no journal write on capacity refusal")
               (check-string= state-at-capacity (root-digest (kernel-state k)) "state untouched"))
             ;; 3. Verify all 3 retained requests can still be retried cleanly
             (multiple-value-bind (retry1-ok l1-ret code1-ret env1)
                 (submit k (doing-request :node "root/f/t1" :request "req-cap-1"))
               (declare (ignore l1-ret code1-ret))
               (ok retry1-ok "oldest request req-cap-1 retry succeeds")
               (ok (getf env1 :replayed) "req-cap-1 marked replayed"))
             (multiple-value-bind (retry2-ok l2-ret code2-ret env2)
                 (submit k (doing-request :node "root/f/t2" :request "req-cap-2"))
               (declare (ignore l2-ret code2-ret))
               (ok retry2-ok "req-cap-2 retry succeeds")
               (ok (getf env2 :replayed) "req-cap-2 marked replayed"))
             (multiple-value-bind (retry3-ok l3-ret code3-ret env3)
                 (submit k (doing-request :node "root/f/t3" :request "req-cap-3"))
               (declare (ignore l3-ret code3-ret))
               (ok retry3-ok "req-cap-3 retry succeeds")
               (ok (getf env3 :replayed) "req-cap-3 marked replayed"))
             ;; 4. Close and reopen journal: exactly 3 entries preserved and retriable
             (close-file-journal j)
             (let* ((j2 (open-file-journal path :capacity capacity :initial-state-hash initial-hash))
                    (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (rep-k ev rec) (replay-journal j2 k2)
                        (declare (ignore rep-k ev))
                        (check-equal 3 rec "replayed 3 records")
                        (check-string= state-at-capacity (root-digest (kernel-state k2)) "state restored"))
                      ;; Retrying oldest request on reopened kernel still works
                      (multiple-value-bind (rep-retry-ok l-rep code-rep env-rep)
                          (submit k2 (doing-request :node "root/f/t1" :request "req-cap-1"))
                        (declare (ignore l-rep code-rep))
                        (ok rep-retry-ok "reopened oldest request retry succeeds")
                        (ok (getf env-rep :replayed) "reopened retry marked replayed")))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-sync-contract-and-directory-sync" "docs/SPEC-WORK.md:307,3348"
    "expected=darwin-fullfsync-or-fsync;dir-synced-on-create;invalid-fd-refuses"
  (let* ((path (test-journal-path "sync-contract"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           ;; 1. Creating a new journal synchronizes parent directory and stream without error
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (ok (probe-file path) "journal file created")
             (close-file-journal j))
           ;; 2. Direct sync-directory on existing directory succeeds
           (let ((parent-dir (directory-namestring (merge-pathnames path))))
             (ok (sync-directory parent-dir) "sync-directory on valid directory succeeds"))
           ;; 3. sync-directory on nonexistent directory signals journal-sync-failed
           (let ((signaled nil))
             (handler-case
                 (sync-directory "/nonexistent/directory/that/cannot/exist/")
               (journal-sync-failed (c)
                 (declare (ignore c))
                 (setf signaled t)))
             (ok signaled "sync-directory on nonexistent directory signals journal-sync-failed"))
           ;; 4. sync-stream on non-file descriptor stream signals journal-sync-failed
           (let ((signaled nil)
                 (str-stream (make-string-output-stream)))
             (handler-case
                 (sync-stream str-stream :path "string-stream")
               (journal-sync-failed (c)
                 (declare (ignore c))
                 (setf signaled t)))
             (ok signaled "sync-stream on memory stream signals journal-sync-failed")))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-creation-sync-and-cleanup" "docs/SPEC-WORK.md:307,3348"
    "expected=creation-sync-closes-stream;preserves-file;directory-fsync-contract"
  (let* ((path (test-journal-path "creation-sync"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           ;; 1. Normal creation: directory entry synced via POSIX fsync, regular file synced
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (ok (probe-file path) "journal file created on disk")
             (close-file-journal j))
           ;; 2. Injected creation directory sync failure: closes stream, preserves file
           (let* ((fail-path (test-journal-path "creation-fail"))
                  (signaled nil))
             (unwind-protect
                  (progn
                    (handler-case
                        (open-file-journal fail-path :initial-state-hash initial-hash :fail-creation-sync-on t)
                      (journal-sync-failed (c)
                        (declare (ignore c))
                        (setf signaled t)))
                    (ok signaled "creation failure signaled journal-sync-failed")
                    ;; Preserves the created file on disk for operator inspection/recovery
                    (ok (probe-file fail-path) "file preserved on disk after creation sync failure"))
               (ignore-errors (delete-file fail-path)))))
      (ignore-errors (delete-file path)))))

(deftest "durable-journal-replay-failure-isolates-target-kernel" "docs/SPEC-WORK.md:307,3348,3353"
    "expected=replay-semantic-error-isolates-target;state-unchanged;rev-unchanged"
  (let* ((path (test-journal-path "replay-isolation"))
         (seed '((:id "root" :type :work-set)
                 (:id "root/f" :type :feature :parent "root")
                 (:id "root/f/t" :type :task :parent "root/f" :state :todo)))
         (initial-hash (root-digest (make-seed-state seed))))
    (unwind-protect
         (progn
           ;; 1. Write frame 1 (valid doing event)
           (let* ((j1 (open-file-journal path :initial-state-hash initial-hash))
                  (k1 (make-kernel :state (make-seed-state seed) :journal j1)))
             (submit k1 (doing-request :node "root/f/t" :request "req-valid-1"))
             (close-file-journal j1))
           ;; 2. Manually append frame 2 with a semantic error: an event referencing a nonexistent node
           (let* ((bad-record (list :request "req-bad-2"
                                    :digest "0000000000000000000000000000000000000000000000000000000000000000"
                                    :line "ok"
                                    :rev 2
                                    :events (list (list :kind :transition
                                                        :node "nonexistent-node"
                                                        :by "rowan"
                                                        :to :done
                                                        :reason "bad"
                                                        :blocked-by '(:absent)
                                                        :evidence '("e1")))))
                  (bad-canon (canonical-string bad-record))
                  (bad-frame (list :frame :seq 2 :len (length bad-canon)
                                   :checksum (sha256-hex bad-canon) :record bad-record))
                  (bad-frame-str (canonical-string bad-frame)))
             (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
               (write-string bad-frame-str out)
               (write-char #\Newline out)
               (finish-output out)))
           ;; 3. Create fresh target kernel
           (let* ((target-k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal)))
                  (init-digest (root-digest (kernel-state target-k)))
                  (init-rev (kernel-next-rev target-k))
                  (init-history (state-history (kernel-state target-k)))
                  (j-replay (open-file-journal path :initial-state-hash initial-hash))
                  (signaled nil))
             (unwind-protect
                  (progn
                    ;; 4. Attempt replay-journal: frame 1 applies to working state, but frame 2 signals error
                    (handler-case
                        (replay-journal j-replay target-k)
                      (error (c)
                        (declare (ignore c))
                        (setf signaled t)))
                    (ok signaled "error signaled during replay of invalid frame")
                    ;; 5. CRITICAL INVARIANT: target-k is completely UNTOUCHED
                    (check-string= init-digest (root-digest (kernel-state target-k)) "target state root digest unchanged")
                    (check-equal init-rev (kernel-next-rev target-k) "target next-rev unchanged")
                    (check-equal init-history (state-history (kernel-state target-k)) "target history unchanged"))
               (close-file-journal j-replay))))
      (ignore-errors (delete-file path)))))

(deftest "session-ownership-taking-and-generation-bump"
  "docs/SPEC-WORK.md:202-214"
  "expected=vacant-or-expired-owner-takes-and-bumps-generation"
  ;; 1. Taking on vacant/nil record
  (multiple-value-bind (action record line)
      (evaluate-ownership-claim nil "emma"
                                :now "2026-09-14T12:00:00Z"
                                :every "30s"
                                :skew "5s"
                                :token "tok-init-1")
    (declare (ignore line))
    (check-equal :take action "vacant claim is take")
    (check-equal "emma" (owner-owner record) "owner is emma")
    (check-equal 1 (owner-generation record) "initial generation is 1")
    (check-string= "tok-init-1" (owner-token record) "token matches")
    (check-string= "2026-09-14T12:01:00Z" (owner-until record) "until is now + 2*every"))

  ;; 2. Taking on expired record (until + skew < now)
  (let ((expired (make-ownership-record
                  :owner "stella"
                  :generation 4
                  :token "tok-stella"
                  :stamp "2026-09-14T10:00:00Z"
                  :until "2026-09-14T10:01:00Z")))
    (multiple-value-bind (action record line)
        (evaluate-ownership-claim expired "emma"
                                  :now "2026-09-14T12:00:00Z"
                                  :every "30s"
                                  :skew "5s"
                                  :token "tok-emma-2")
      (declare (ignore line))
      (check-equal :take action "expired claim is take")
      (check-equal "emma" (owner-owner record) "new owner is emma")
      (check-equal 5 (owner-generation record) "generation bumped to 5")
      (check-string= "tok-emma-2" (owner-token record) "token matches")
      (check-string= "2026-09-14T12:01:00Z" (owner-until record) "until is now + 2*every"))))

(deftest "session-ownership-resume-with-token"
  "docs/SPEC-WORK.md:202-214"
  "expected=matching-token-resumes-generation-and-advances-until"
  (let ((current (make-ownership-record
                  :owner "emma"
                  :generation 3
                  :token "tok-emma-secret"
                  :stamp "2026-09-14T11:59:00Z"
                  :until "2026-09-14T12:01:00Z")))
    ;; Resuming with matching journal token
    (multiple-value-bind (action record line)
        (evaluate-ownership-claim current "emma"
                                  :now "2026-09-14T12:00:30Z"
                                  :every "30s"
                                  :skew "5s"
                                  :journal-token "tok-emma-secret")
      (declare (ignore line))
      (check-equal :resume action "matching token resumes")
      (check-equal "emma" (owner-owner record) "owner is emma")
      (check-equal 3 (owner-generation record) "generation preserved (not bumped)")
      (check-string= "tok-emma-secret" (owner-token record) "token preserved")
      (check-string= "2026-09-14T12:01:30Z" (owner-until record) "until advanced to now + 2*every"))))

(deftest "session-competing-owner-fenced"
  "docs/SPEC-WORK.md:213-214"
  "expected=competing-live-owner-refused-exit-1"
  (let ((live (make-ownership-record
               :owner "stella"
               :generation 2
               :token "tok-stella-live"
               :stamp "2026-09-14T12:00:00Z"
               :until "2026-09-14T12:05:00Z")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim live "emma"
                                  :now "2026-09-14T12:01:00Z"
                                  :every "30s"
                                  :skew "5s"
                                  :token "tok-emma-new")
      (declare (ignore record))
      (check-equal :fenced action "competing live owner is refused")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "SESSION FAIL" line) "line contains SESSION FAIL")
      (ok (search "stella" line) "line names current owner")
      (ok (search "held" line) "line says held"))))

(deftest "session-self-fencing-on-expired-until"
  "docs/SPEC-WORK.md:234-238"
  "expected=clock-past-until-fences-session-and-refuses-mutation"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal)))
         (sess (make-session :owner "emma"
                             :generation 1
                             :token "tok-sess-1"
                             :until "2026-09-14T12:01:00Z"
                             :kernel k
                             :state :live
                             :file "work.s"
                             :base "sha-base-1"
                             :journal "work.journal"
                             :every "30s"
                             :skew "5s")))
    ;; 1. Request arrived before until -> admitted
    (multiple-value-bind (admitted-p reason exit-code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:00:50Z")
      (declare (ignore reason exit-code))
      (ok admitted-p "admitted before until"))
    ;; 2. Request arrived after until -> self-fenced!
    (multiple-value-bind (admitted-p reason exit-code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:01:05Z")
      (ok (not admitted-p) "refused after until")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "fenced" reason) "refusal says fenced"))
    (check-equal :fenced (session-state sess) "session state transitioned to fenced")
    ;; 3. Submit mutation on fenced session is refused
    (multiple-value-bind (ok-p line exit-code)
        (session-submit sess
                        '(:verb :state-to-doing :node "t1" :by "emma" :request "r-fence-1"
                          :stamp "2026-09-14T12:01:10Z" :clock :tool :generation-owner "emma")
                        :now "2026-09-14T12:01:10Z")
      (ok (not ok-p) "submit refused while fenced")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "fenced" line) "submit line says fenced"))))

(deftest "session-status-inspects-live-and-fenced"
  "docs/SPEC-WORK.md:234-238,4155"
  "expected=status-answers-exit-0-live-and-fenced"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal)))
         (sess (make-session :owner "emma"
                             :generation 2
                             :token "tok-sess-status"
                             :until "2026-09-14T12:01:00Z"
                             :kernel k
                             :state :live
                             :file "work.s"
                             :base "abc1234"
                             :journal "work.journal"
                             :every "30s"
                             :skew "5s")))
    ;; 1. Status while live
    (multiple-value-bind (ok-p line exit-code)
        (session-status sess)
      (ok ok-p "status succeeds")
      (check-equal 0 exit-code "status exits 0")
      (ok (search "SESSION OK" line) "line begins with SESSION OK")
      (ok (search "owner=emma" line) "status carries owner")
      (ok (search "state=live" line) "status carries state=live"))
    ;; 2. Transition to fenced
    (setf (session-state sess) :fenced)
    ;; 3. Status while fenced MUST still succeed with exit 0
    (multiple-value-bind (ok-p line exit-code)
        (session-status sess)
      (ok ok-p "status succeeds while fenced")
      (check-equal 0 exit-code "status exits 0 while fenced")
      (ok (search "state=fenced" line) "status carries state=fenced"))))

(deftest "execution-lease-take-and-materialized-w"
  "docs/SPEC-WORK.md:1354-1358,1682-1709"
  "expected=take-acquires-lease-and-materializes-w-counter"
  (let* ((seed '((:id "t1" :type :task :state :todo)
                 (:id "t2" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    ;; Baseline working view
    (check-equal 0 (state-working-count (kernel-state k)) "initial |W| is 0")
    (ok (not (is-working-p (kernel-state k) "t1")) "t1 not working")
    (check-equal '() (working-ids (kernel-state k)) "working-ids empty")
    ;; Submit take on t1
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :take
                    :node "t1"
                    :by "emma"
                    :deadline "2026-09-14T18:00:00Z"
                    :default :release
                    :request "req-take-t1"
                    :stamp "2026-09-14T12:00:00Z"
                    :clock :tool
                    :generation-owner "emma"))
      (declare (ignore env))
      (ok ok-p "take succeeded")
      (check-equal 0 exit-code "exit 0")
      (ok (search "LEASE OK" line) "line is LEASE OK")
      (ok (search "holder=emma" line) "line names holder")
      (ok (search "live=1" line) "line names live count"))
    ;; Materialized working view updated eagerly
    (check-equal 1 (state-working-count (kernel-state k)) "|W| is 1")
    (ok (is-working-p (kernel-state k) "t1") "t1 is working")
    (ok (not (is-working-p (kernel-state k) "t2")) "t2 is not working")
    (check-equal '("t1") (working-ids (kernel-state k)) "working-ids has t1")
    ;; Node state inspectable
    (let ((node (nova-work::%node-quiet (kernel-state k) "t1")))
      (check-string= "emma" (wnode-holder node) "holder is emma")
      (check-string= "2026-09-14T18:00:00Z" (wnode-deadline node) "deadline matches")
      (check-equal :release (wnode-default-action node) "default action is release"))))

(deftest "execution-lease-competing-take-refuses-held"
  "docs/SPEC-WORK.md:1379-1382"
  "expected=second-take-on-active-lease-refused"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    ;; 1. Emma takes lease
    (submit k '(:verb :take :node "t1" :by "emma" :deadline "2026-09-14T18:00:00Z"
                :default :release :request "req-take-emma" :stamp "2026-09-14T12:00:00Z"
                :clock :tool :generation-owner "emma"))
    ;; 2. Rowan attempts to take same node while active
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :take :node "t1" :by "rowan" :deadline "2026-09-14T19:00:00Z"
                    :default :release :request "req-take-rowan" :stamp "2026-09-14T12:05:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore env))
      (ok (not ok-p) "competing take refused")
      (check-equal 1 exit-code "exit 1")
      (ok (search "LEASE FAIL" line) "line is LEASE FAIL")
      (ok (search "holder=emma" line) "line names current holder emma")
      (ok (search "held" line) "line says held"))
    ;; 3. Holder and |W| remain intact
    (check-equal 1 (state-working-count (kernel-state k)) "|W| still 1")
    (let ((node (nova-work::%node-quiet (kernel-state k) "t1")))
      (check-string= "emma" (wnode-holder node) "holder still emma"))))

(deftest "execution-lease-heartbeat-updates-evidence"
  "docs/SPEC-WORK.md:1359-1362"
  "expected=holder-heartbeat-updates-evidence-third-party-refused"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    (submit k '(:verb :take :node "t1" :by "emma" :deadline "2026-09-14T18:00:00Z"
                :default :release :request "req-take-hb" :stamp "2026-09-14T12:00:00Z"
                :clock :tool :generation-owner "emma"))
    ;; 1. Third party heartbeat refused
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :heartbeat :node "t1" :by "stella" :evidence "note:bus:stella-1"
                    :request "req-hb-stella" :stamp "2026-09-14T12:10:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore env))
      (ok (not ok-p) "third party heartbeat refused")
      (check-equal 1 exit-code "exit 1")
      (ok (search "HEARTBEAT FAIL" line) "HEARTBEAT FAIL line")
      (ok (search "not held by stella" line) "line says not held by stella"))
    ;; 2. Holder heartbeat succeeds
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :heartbeat :node "t1" :by "emma" :evidence "note:bus:emma-123"
                    :request "req-hb-emma" :stamp "2026-09-14T12:15:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore env))
      (ok ok-p "holder heartbeat succeeded")
      (check-equal 0 exit-code "exit 0")
      (ok (search "HEARTBEAT OK" line) "HEARTBEAT OK line"))
    ;; Verify node heartbeat fields updated
    (let ((node (nova-work::%node-quiet (kernel-state k) "t1")))
      (check-string= "2026-09-14T12:15:00Z" (wnode-last-heartbeat node) "heartbeat stamp updated")
      (check-string= "note:bus:emma-123" (wnode-heartbeat-evidence node) "evidence updated"))))

(deftest "execution-lease-release-clears-w"
  "docs/SPEC-WORK.md:1379-1382,1682-1709"
  "expected=release-clears-lease-and-working-view"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    (submit k '(:verb :take :node "t1" :by "emma" :deadline "2026-09-14T18:00:00Z"
                :default :release :request "req-take-rel" :stamp "2026-09-14T12:00:00Z"
                :clock :tool :generation-owner "emma"))
    (check-equal 1 (state-working-count (kernel-state k)) "|W| is 1")
    ;; 1. Third party release refused
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :release :node "t1" :by "stella"
                    :request "req-rel-stella" :stamp "2026-09-14T12:30:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore env))
      (ok (not ok-p) "third party release refused")
      (check-equal 1 exit-code "exit 1")
      (ok (search "LEASE FAIL" line) "line is LEASE FAIL"))
    ;; 2. Holder release succeeds
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :release :node "t1" :by "emma"
                    :request "req-rel-emma" :stamp "2026-09-14T12:31:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore env))
      (ok ok-p "holder release succeeded")
      (check-equal 0 exit-code "exit 0")
      (ok (search "RELEASE OK" line) "line is RELEASE OK"))
    ;; Working view and node state cleared
    (check-equal 0 (state-working-count (kernel-state k)) "|W| cleared to 0")
    (ok (not (is-working-p (kernel-state k) "t1")) "t1 no longer working")
    (check-equal '() (working-ids (kernel-state k)) "working-ids empty")
    (let ((node (nova-work::%node-quiet (kernel-state k) "t1")))
      (ok (null (wnode-holder node)) "holder is nil/unowned")
      (ok (null (wnode-deadline node)) "deadline is nil"))))

(deftest "settle-releases-the-lease"
  "docs/SPEC-WORK.md:1674-1680,4453"
  "expected=settling-node-with-live-lease-releases-lease-and-clears-w"
  (let* ((seed '((:id "t1" :type :task :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    ;; 1. Take lease and transition to doing
    (submit k '(:verb :take :node "t1" :by "emma" :deadline "2026-09-14T18:00:00Z"
                :default :release :request "req-take-settle" :stamp "2026-09-14T12:00:00Z"
                :clock :tool :generation-owner "emma"))
    (submit k '(:verb :state-to-doing :node "t1" :by "emma" :reason "starting"
                :request "req-doing-settle" :stamp "2026-09-14T12:01:00Z"
                :clock :tool :generation-owner "emma"))
    (check-equal 1 (state-working-count (kernel-state k)) "|W| is 1")
    (ok (is-working-p (kernel-state k) "t1") "t1 is working")
    ;; 2. Settle the node
    (multiple-value-bind (ok-p line exit-code env)
        (submit k '(:verb :state-to-done :node "t1" :by "emma" :evidence ("ev1")
                    :request "req-done-settle" :stamp "2026-09-14T12:02:00Z"
                    :clock :tool :generation-owner "emma"))
      (declare (ignore line env))
      (ok ok-p "settle succeeded")
      (check-equal 0 exit-code "exit 0"))
    ;; 3. Node is settled in C, and lease is RELEASED
    (check-equal :c (node-branch (kernel-state k) "t1") "t1 is in C")
    (check-equal 0 (state-working-count (kernel-state k)) "|W| is 0")
    (ok (not (is-working-p (kernel-state k) "t1")) "t1 is not in working view")
    (let ((node (nova-work::%node-quiet (kernel-state k) "t1")))
      (ok (null (wnode-holder node)) "holder reads unowned/nil")
      (ok (null (wnode-deadline node)) "deadline reads nil"))))

(deftest "working-set-reads-visit-zero-nodes"
  "docs/SPEC-WORK.md:1702-1709,4995"
  "expected=is-working-and-working-count-visit-zero-nodes"
  (let* ((seed '((:id "ws" :type :work-set)
                 (:id "feat" :type :feature :parent "ws")
                 (:id "t1" :type :task :parent "feat" :state :todo)
                 (:id "t2" :type :task :parent "feat" :state :todo)
                 (:id "t3" :type :task :parent "feat" :state :todo)))
         (k (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal))))
    (submit k '(:verb :take :node "t2" :by "emma" :deadline "2026-09-14T18:00:00Z"
                :default :release :request "req-take-inst" :stamp "2026-09-14T12:00:00Z"
                :clock :tool :generation-owner "emma"))
    (check-equal 1 (state-working-count (kernel-state k)) "|W| is 1")
    ;; Invariant: reading |W| and is-working-p visits ZERO nodes!
    (with-instrumentation
      (let ((w-count (state-working-count (kernel-state k)))
            (t1-w (is-working-p (kernel-state k) "t1"))
            (t2-w (is-working-p (kernel-state k) "t2"))
            (t3-w (is-working-p (kernel-state k) "t3"))
            (w-ids (working-ids (kernel-state k))))
        (check-equal 1 w-count "|W| read")
        (ok (not t1-w) "t1 not working")
        (ok t2-w "t2 working")
        (ok (not t3-w) "t3 not working")
        (check-equal '("t2") w-ids "working-ids is t2")
        (check-equal 0 *visits* "ZERO nodes in O visited for working-set queries")))))
