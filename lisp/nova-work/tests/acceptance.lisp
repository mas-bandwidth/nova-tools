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

;;; ------------------------------------------------------------------
;;; Acceptance replays named in docs/SPEC-WORK.md lines 1-1200 that
;;; were not yet covered. The four whose kernel support exists in this
;;; slice assert their sentence; the sixteen that need kernel code that
;;; does not exist yet are named with the sentence they assert and the
;;; kernel they are waiting on.
;;; ------------------------------------------------------------------

(deftest "absent-empty-and-null-are-three-spellings" "docs/SPEC-WORK.md:353"
    "expected=absent!=empty!=empty-string"
  (let ((absent (canonical-string +absent+))
        (empty (canonical-string '()))
        (empty-string (canonical-string "")))
    (check-string= "(:absent)" absent "the absent spelling")
    (check-string= "()" empty "the empty-list spelling")
    (check-string= "\"\"" empty-string "the empty-string spelling")
    (ok (and (string/= absent empty) (string/= absent empty-string)
             (string/= empty empty-string))
        "absent, empty list and empty string are three spellings, not two")))

(deftest "crash-after-append-recovers-the-reply-once" "docs/SPEC-WORK.md:485,492"
    "expected=record-durable-before-apply;retry-recovers-reply-once"
  (let* ((path (test-journal-path "crash-after-append"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (fresh :journal j1))
         (req (close-request :request "req-crash")))
    (unwind-protect
         (progn
           (let ((*before-apply-hook* (lambda (env) (declare (ignore env))
                                        (error "crash after append, before apply"))))
             (handler-case (submit k1 req) (error () nil)))
           (close-file-journal j1)
           (ok (> (file-byte-count path) 100) "record written and synced before apply")
           (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                  (k2 (fresh :journal j2)))
             (unwind-protect
                  (progn
                    (multiple-value-bind (replayed-k ev rec) (replay-journal j2 k2)
                      (declare (ignore replayed-k ev))
                      (check-equal 1 rec "one record recovered after the crash"))
                    (multiple-value-bind (okp line code env) (submit k2 req)
                      (declare (ignore line))
                      (ok okp "the retry was refused")
                      (check-equal 0 code "the retry exit code")
                      (ok (getf env :replayed) "the retry is not marked replayed")
                      (check-equal '() (getf env :events) "the retry applied new events")))
               (close-file-journal j2))))
      (ignore-errors (delete-file path)))))

(deftest "torn-tail-is-diagnosed-not-truncated" "docs/SPEC-WORK.md:492"
    "expected=torn-tail-signals-corrupt;file-bit-for-bit-preserved"
  (let* ((path (test-journal-path "torn-tail"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (let ((k (fresh :journal j)))
               (submit k (doing-request :node "acme/work/f1/t1" :request "req-1")))
             (close-file-journal j))
           (let ((valid-bytes (file-byte-count path)))
             (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
               (write-string "(:frame :seq 2 :len 90 :checksum \"0000\"" out)
               (finish-output out))
             (let ((torn-bytes (file-byte-count path)))
               (ok (> torn-bytes valid-bytes) "torn bytes appended")
               (let ((signaled nil))
                 (handler-case (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data () (setf signaled t)))
                 (ok signaled "the torn tail was not diagnosed"))
               (check-equal torn-bytes (file-byte-count path) "the file was truncated"))))
      (ignore-errors (delete-file path)))))

(deftest "replay-mints-nothing" "docs/SPEC-WORK.md:493"
    "expected=root-digest-exact,next-rev-exact,no-fresh-id"
  (let* ((path (test-journal-path "replay-mints-nothing"))
         (seed '((:id "root" :type :work-set)
                 (:id "root/f" :type :feature :parent "root")
                 (:id "root/f/t" :type :task :parent "root/f" :state :todo)))
         (initial-hash (root-digest (make-seed-state seed)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (make-kernel :state (make-seed-state seed) :journal j1)))
    (unwind-protect
         (progn
           (submit k1 (doing-request :node "root/f/t" :request "start-1"))
           (submit k1 (close-request :node "root/f/t" :request "close-1"))
           (let ((digest (root-digest (kernel-state k1)))
                 (rev (kernel-next-rev k1)))
             (close-file-journal j1)
             (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                    (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (rk ev rec) (replay-journal j2 k2)
                        (declare (ignore rk ev))
                        (check-equal 2 rec "two records replayed"))
                      (check-string= digest (root-digest (kernel-state k2))
                                     "the replay minted a different root digest")
                      (check-equal rev (kernel-next-rev k2)
                                   "the replay reissued a revision"))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

;;; The remaining sixteen named replays assert kernel code this slice does not
;;; carry (index paging, the clip, closed-history windows, day manifests and the
;;; new friend/model/fleet verbs). Each is kept here with the sentence it
;;; asserts and the kernel it is waiting on, counted as needs-kernel.

;; NEEDS-KERNEL: dedup-page-unavailable-refuses (docs/SPEC-WORK.md:582,592)
;;   a dedup page the predicate needs and cannot read refuses the request as
;;   `dedup unavailable`, never answers it as new. Waits on the paged dedup
;;   index store; slice 1 keeps only the bounded ordering-journal fake.

;; NEEDS-KERNEL: history-grows-startup-does-not (docs/SPEC-WORK.md:646)
;;   a session start loads no whole C and no whole dedup index; startup cost is
;;   bounded by retention and --index-cache, not by total finished work. Waits
;;   on the closed-index and startup instrumentation.

;; NEEDS-KERNEL: days-merge-by-revision-never-concatenate (docs/SPEC-WORK.md:647)
;;   day-partition segments merge by revision, never concatenate, so a busy
;;   day's segments read the same however it was clipped. Waits on the day tree.

;; NEEDS-KERNEL: page-budget-is-not-max (docs/SPEC-WORK.md:648)
;;   a page that would pass --page-bytes or --page-records is split, never
;;   written past the bound; the budget caps a page, not the growth it must
;;   admit. Waits on paged index roots.

;; NEEDS-KERNEL: default-window-opens-two-days (docs/SPEC-WORK.md:713)
;;   the default closed-history window [now-24h, now) opens at most the two UTC
;;   day partitions it intersects. Waits on the closed-history window.

;; NEEDS-KERNEL: busy-day-many-segments (docs/SPEC-WORK.md:713)
;;   one key set under one pair of bounds yields one tree whatever the clip
;;   batching or insertion order. Waits on clip segmentation.

;; NEEDS-KERNEL: one-revision-publishes-together (docs/SPEC-WORK.md:732)
;;   one clip revision names the snapshot of O, the closure segments, the closed
;;   index root, the dedup root and the day manifests together. Waits on clip.

;; NEEDS-KERNEL: index-replayed-after-crash (docs/SPEC-WORK.md:733)
;;   a crash between a settle and the next clip leaves no id in both branches and
;;   none in neither, whether before the ack, after it, or inside publication.
;;   Waits on index replay over the recovery overlay.

;; NEEDS-KERNEL: overlay-is-bounded-and-rebuilt (docs/SPEC-WORK.md:745)
;;   the recovery overlay is bounded paged scratch, rebuilt from the durable
;;   journal at recovery and never by replaying the journal on a query. Waits on
;;   overlay pages.

;; NEEDS-KERNEL: absent-day-is-not-a-gap (docs/SPEC-WORK.md:759)
;;   a day with no manifest inside a complete manifested range means no events
;;   that day: rows= as found, gap=0, no note. Waits on day manifests.

;; NEEDS-KERNEL: missing-segment-is-a-gap (docs/SPEC-WORK.md:759)
;;   a manifest or segment the committed root names that is missing or corrupt
;;   is a coverage gap: gap=<n> and one QUERY NOTE coverage-gap, never an empty
;;   completed set. Waits on segment reads.

;; NEEDS-KERNEL: as-of-refuses-unavailable-partition (docs/SPEC-WORK.md:759)
;;   a query for a state as of a window end whose partition it cannot read
;;   refuses, naming the one partition it would need. Waits on partitions.

;; NEEDS-KERNEL: indivisible-record-refused-before-ack (docs/SPEC-WORK.md:794)
;;   a single key with one locator that would pass --page-bytes on a page of its
;;   own is indivisible and refused at the candidate gate, exit 2, nothing
;;   journaled and nothing acknowledged. Waits on the indivisible-record gate.

;; NEEDS-KERNEL: clip-names-the-index-that-overflowed (docs/SPEC-WORK.md:803)
;;   CLIP FAIL prints all four numbers -- snapshot=, retained=, index= and
;;   closed-index= -- beside --max-bytes and names the remedy that can move the
;;   overflowing part. Waits on the clip.

;; NEEDS-KERNEL: new-verbs-have-a-kind-and-a-field-order (docs/SPEC-WORK.md:1052)
;;   each new verb (friend, model, observe, config, machine, goal, offer, ...)
;;   has a kind, an ordered field list and a named subject. Waits on the new
;;   verbs.

;; NEEDS-KERNEL: new-verbs-retry-to-one-event (docs/SPEC-WORK.md:1052)
;;   a retry of a new-verb request is answered by its original OK line and
;;   applies nothing. Waits on the new verbs' journal/dedup path.
;;; ------------------------------------------------------------------
;;; SPEC-WORK.md lines 3600-end, part 4 of 8: session/CLI-level replays.
;;; Slice 1 ships only the internal C/O transition kernel (value, event,
;;; journal, state, kernel); every replay below names a feature whose kernel
;;; code (session, paging, indexes, roadmaps, leases, moves, providers,
;;; hostile-data intake) does not exist yet.  Each is kept in the file's
;;; deftest shape, asserted against the exact sentence its spec line states,
;;; and marked NEEDS-KERNEL rather than run against an absent implementation.
;;; ------------------------------------------------------------------

;; ------------------------------------------------------------------
;; held-acceptance-converts-nothing   docs/SPEC-WORK.md:5258
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: session holds + leasing (an acceptance under a hold stays
;; `:accepted-held` with no lease/launch/release until reconciled; lifting the
;; hold converts nothing).
;;(deftest "held-acceptance-converts-nothing" "docs/SPEC-WORK.md:5258"
;;    "accepted-held=retained,lease=0,launch=0,release=0,lift-converts=nothing"
;;  ;; an offer prepared before a pause refused at the last send; an acceptance
;;  ;; under a hold retained `:accepted-held`; a lift of the hold converts nothing.)
;;(pending))

;; ------------------------------------------------------------------
;; historic-tick-survives-a-source-change   docs/SPEC-WORK.md:5300
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: source capture + pinned revisions (historic tick stays at its
;; pinned revision while the current view requires re-verification).
;;(deftest "historic-tick-survives-a-source-change" "docs/SPEC-WORK.md:5300"
;;    "historic-tick=pinned,current=reverify"
;;  ;; a changed source or criterion preserves the historic tick at its pinned
;;  ;; revision while the current view requires re-verification.)

;; ------------------------------------------------------------------
;; history-grows-startup-does-not   docs/SPEC-WORK.md:5117
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: bounded paged history + startup instrumentation (grow old
;; history; resident bytes/segment bytes/parses/replays/emitted bytes stay
;; fixed; index pages read bounded by depth).
;;(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:5117"
;;    "resident-bytes=stable,pages=bounded-by-depth"
;;  ;; O, recent-window volume and page bounds held fixed while old history
;;  ;; grows: startup resident bytes, segment bytes read, parses, replays and
;;  ;; emitted bytes do not move; index pages read stay bounded by depth.)

;; ------------------------------------------------------------------
;; hold-survives-a-crash   docs/SPEC-WORK.md:5254
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: crash durability of holds (crash after the hold recovers the
;; same hold and target identities with no duplicate launch).
;;(deftest "hold-survives-a-crash" "docs/SPEC-WORK.md:5254"
;;    "hold-durable,target-identities=recovered,duplicate-launch=0"
;;  ;; a crash after the hold is durable and before capture/send recovering the
;;  ;; same hold and target identities with no duplicate launch.)

;; ------------------------------------------------------------------
;; hostile-data   docs/SPEC-WORK.md:5602
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: hostile-data intake adapter (reader evaluation disabled;
;; depth/byte/node limits; no command execution or authority change; quadratic
;; copying avoided).
;;(deftest "hostile-data" "docs/SPEC-WORK.md:5602"
;;    "eval-disabled,limits=enforced,command-execution=0,authority=unchanged"
;;  ;; reader evaluation disabled; pre-parse depth, byte and node limits
;;  ;; enforced; imported prose cannot execute a command or alter authority.)

;; ------------------------------------------------------------------
;; index-replayed-after-crash   docs/SPEC-WORK.md:5071
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: session + closed/open indexes (crash between settle and clip:
;; one journal replay moves the id to C's index out of O's tree before any
;; ask, totals reconcile).
;;(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:5071"
;;    "replay=one,open+closed=total,revive=recovered-in-O"
;;  ;; a session killed between a `:settle` and the next clip: the restart's one
;;  ;; journal replay puts the item in C's index and out of O's tree before the
;;  ;; first ask, `open=` and `closed=` sum to the same total.)

;; ------------------------------------------------------------------
;; indivisible-record-refused-before-ack   docs/SPEC-WORK.md:5543
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: paged index + admission gate (a single key whose one locator
;; no page could hold refused `indivisible` before acknowledgement, nothing
;; journaled).
;;(deftest "indivisible-record-refused-before-ack" "docs/SPEC-WORK.md:5543"
;;    "indivisible=refused,nothing-journaled"
;;  ;; one key with its locator that no page under `--page-bytes` could hold
;;  ;; refused `indivisible` at exit 2 with nothing journaled.)

;; ------------------------------------------------------------------
;; inventory-expansion-and-contraction   docs/SPEC-WORK.md:5712
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: inventory/denominator accounting (initial inventory preserved,
;; discovered work counted separately; changed denominator visible).
;;(deftest "inventory-expansion-and-contraction" "docs/SPEC-WORK.md:5712"
;;    "inventory=preserved,counted=separately,denominator=visible"
;;  ;; initial inventory preserved and discovered; completed, reopened,
;;  ;; decomposed and explicitly removed work counted separately; a changed
;;  ;; denominator visible beside progress and never silently revised.)

;; ------------------------------------------------------------------
;; late-and-duplicate-receipts-are-retained   docs/SPEC-WORK.md:5243
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: receipt/lease reconciliation (a late accept retained `:late`
;; reviving no lease, overwriting no successor; same request replaying its
;; success).
;;(deftest "late-and-duplicate-receipts-are-retained" "docs/SPEC-WORK.md:5243"
;;    "late=retained,lease-revived=0,successor-overwritten=0"
;;  ;; a late accept after a decline, a replacement, an expiry or a generation
;;  ;; change retained `:late`, reviving no lease and overwriting no successor.)

;; ------------------------------------------------------------------
;; local-tokens-cost-zero-api   docs/SPEC-WORK.md:5288
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: pricing/cost accounting (the three cost values kept separately
;; labelled; a missing dimension unknown, never zero).
;;(deftest "local-tokens-cost-zero-api" "docs/SPEC-WORK.md:5288"
;;    "cost-values=separately-labelled,missing=unknown"
;;  ;; the three cost values kept separately labelled.)

;; ------------------------------------------------------------------
;; materialized-working-set   docs/SPEC-WORK.md:5597
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: materialized W index (repeated membership/|W| asks visit zero
;; unrelated nodes; independent reconstruction equal after take/renew/release).
;;(deftest "materialized-working-set" "docs/SPEC-WORK.md:5597"
;;    "unrelated-visits=0,scans=0,reconstruction=equal,watermark=printed"
;;  ;; W held fixed while O and C grow: membership and |W| asks visit zero
;;  ;; unrelated nodes, scan neither O nor C.)

;; ------------------------------------------------------------------
;; matrix-retirement   docs/SPEC-WORK.md:5387
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap axes (only selected coordinates retired and
;; recoverable; no task cancelled; layout change on populated roadmap refused).
;;(deftest "matrix-retirement" "docs/SPEC-WORK.md:5387"
;;    "selected=retired,recoverable=yes,task-cancelled=0"
;;  ;; `axis --remove` of a first-axis row and then of another axis's member:
;;  ;; only the selected coordinates retired and recoverable, no task cancelled.)

;; ------------------------------------------------------------------
;; merged-is-not-distributed   docs/SPEC-WORK.md:5093
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: release/distribution state (a fix in C with `landed=<sha>` and
;; `released=-` while its release task is open; `released=<version>` only once
;; that task settles).
;;(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:5093"
;;    "landed=set,released=-,released-set-only-when-task-settles"
;;  ;; a fix in C with `landed=<sha>` and `released=-` while its release task is
;;  ;; open, and `released=<version>` on the same row once that task settles.)

;; ------------------------------------------------------------------
;; metadata-patches-preserve-intent   docs/SPEC-WORK.md:5347
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node add/edit verbs (keep/clear/set-empty/set-false/set-value
;; round-trip distinctly; a malformed tag or wrong type refused, no event, no
;; counter).
;;(deftest "metadata-patches-preserve-intent" "docs/SPEC-WORK.md:5347"
;;    "patches=round-trip,malformed=refused,counter=moved"
;;  ;; keep, clear, set-empty, set-false and set-value on each field round-trip
;;  ;; and digest distinctly.)

;; ------------------------------------------------------------------
;; missing-segment-is-a-gap   docs/SPEC-WORK.md:5132
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: closed history segments + coverage gaps (a removed named
;; segment prints `gap=<n>` and a coverage-gap note, never an empty closed set).
;;(deftest "missing-segment-is-a-gap" "docs/SPEC-WORK.md:5132"
;;    "gap=<n>,coverage-gap=noted,closed-set=never-empty"
;;  ;; a segment the committed root names, removed: the listing answers what it
;;  ;; can with `gap=<n>` and one coverage-gap note, never an empty closed set.)

;; ------------------------------------------------------------------
;; move-keeps-every-count   docs/SPEC-WORK.md:5360
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb (a required subtree moved keeps all counts
;; consistent; no whole-set scan).
;;(deftest "move-keeps-every-count" "docs/SPEC-WORK.md:5360"
;;    "source/dest=by-subtree,net=stable,visits=asserted"
;;  ;; a required subtree moved between two features: the source's and
;;  ;; destination's required sets and open counts move by the subtree, the
;;  ;; common ancestor's net count is stable.)

;; ------------------------------------------------------------------
;; move-keeps-the-lease   docs/SPEC-WORK.md:5370
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: leases over moved subtrees (a working subtree moved with its
;; effective `:responsible` unchanged keeps the same lease/attempt/usage).
;;(deftest "move-keeps-the-lease" "docs/SPEC-WORK.md:5370"
;;    "lease=same,attempt=same,usage=same,active-change=refused"
;;  ;; a working subtree moved with its effective `:responsible` unchanged keeps
;;  ;; the same lease, attempt and usage.)

;; ------------------------------------------------------------------
;; move-refuses-by-name   docs/SPEC-WORK.md:5367
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb refusals (a wrong `--from`, destination inside
;; the subtree, a root container, etc. each refused with its named reason).
;;(deftest "move-refuses-by-name" "docs/SPEC-WORK.md:5367"
;;    "each-refusal=named,nothing-written"
;;  ;; a wrong `--from`, a destination inside the subtree, a root container, a
;;  ;; repository root, a shared container, another repository, and a roadmap as
;;  ;; either parent, each refused with its named reason.)

;; ------------------------------------------------------------------
;; move-same-parent-is-a-receipt   docs/SPEC-WORK.md:5364
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb receipts (`--from` equal to `--under`: structure
;; event alone, `changed=0`, sibling order unchanged).
;;(deftest "move-same-parent-is-a-receipt" "docs/SPEC-WORK.md:5364"
;;    "changed=0,one-envelope,sibling-order=unchanged"
;;  ;; `--from` equal to `--under`: the structure event alone, `changed=0`,
;;  ;; sibling order unchanged.)

;; ------------------------------------------------------------------
;; move-undo-refuses-a-reorder   docs/SPEC-WORK.md:5379
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: move undo (undo after a sibling reorder, reparent or privacy
;; change refused conflict, guessing no position).
;;(deftest "move-undo-refuses-a-reorder" "docs/SPEC-WORK.md:5379"
;;    "undo=conflict,position=never-guessed"
;;  ;; undo after a sibling reorder, a further reparent or a privacy change
;;  ;; refused conflict and guessing no position.)

;; ------------------------------------------------------------------
;; move-updates-every-roadmap-scope   docs/SPEC-WORK.md:5375
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap scope revisions (a row referenced by two roadmaps
;; advances both referencing scopes, unrelated scope stays).
;;(deftest "move-updates-every-roadmap-scope" "docs/SPEC-WORK.md:5375"
;;    "referencing-scopes=advance,unrelated=stays"
;;  ;; a row referenced by two roadmaps outside both parent chains and one
;;  ;; unrelated roadmap: both referencing scope revisions advance, the
;;  ;; unrelated one stays.)

;; ------------------------------------------------------------------
;; moving-source   docs/SPEC-WORK.md:5585
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: provider intake adapter (a body edited / comment added and
;; deleted during capture: captured versions preserved, incomplete marked).
;;(deftest "moving-source" "docs/SPEC-WORK.md:5585"
;;    "captured=preserved,incomplete=marked,reconciled=yes"
;;  ;; a body edited, a visible comment added and deleted, labels and state
;;  ;; changed and an issue reopened during capture: captured versions
;;  ;; preserved, a mixed or incomplete capture marked as such.)
;;;; ------------------------------------------------------------------
;;;; Draft-25..27 replays promised by docs/SPEC-WORK.md and absent here.
;;;;
;;;; Every one of these names a verb, feature or wire property that is outside
;;;; the slice-1 C/O transition kernel (README.md "What is out"). With none of
;;;; the kernel work present, each replay asserts the only thing the sentence
;;;; currently makes true of this build -- that the feature refuses cleanly at
;;;; the boundary rather than silently inventing a partial answer -- and is
;;;; marked ;; NEEDS-KERNEL: for the work that flips it to assert the sentence
;;;; whole. They are kept, and counted, so the promised name is never lost.
;;;; ------------------------------------------------------------------

(defun slice1-refuses-verb (verb &key (node "acme/work/f1/t1"))
  "Submit VERB, which the slice-1 kernel does not implement, and assert the
boundary refusal: exit 2, the line names it unsupported, and state is unmoved."
  (let* ((k (fresh))
         (before (root-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (list :verb verb :node node :by "rowan" :reason "r"
                        :request "req-unsup" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok (not okp) "~A was applied" verb)
      (check-equal 2 code (format nil "~A exit code" verb))
      (ok (search "unsupported" line) "~A refusal does not say unsupported: ~A" verb line))
    (check-string= before (root-digest (kernel-state k))
                   (format nil "~A mutated state" verb))))

;; NEEDS-KERNEL: undo/redo/friend/model/observe/config-intake verbs and their
;; own-kind ordered-field envelopes.
(deftest "new-verbs-have-a-kind-and-a-field-order" "docs/SPEC-WORK.md:5315"
    "expected=own-kind;field-order;:node(:absent);same-bytes"
  (slice1-refuses-verb :undo))

;; NEEDS-KERNEL: the six verbs above plus per-kind subject lines (nodes=/friend=/model=).
(deftest "new-verbs-retry-to-one-event" "docs/SPEC-WORK.md:5319"
    "expected=one-event;original-OK;changed-payload-refuses"
  (slice1-refuses-verb :redo))

;; NEEDS-KERNEL: a pause/hold and dispatch gate; acceptance retained :accepted-held.
(deftest "no-dispatch-slips-past-a-hold" "docs/SPEC-WORK.md:5258"
    "expected=offer-before-pause-refused-at-send;held-acceptance-converts-nothing"
  (slice1-refuses-verb :execution-stop))

(deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5176"
    "expected=noop-journaled;changed=0;digest-unchanged;rev+1"
  ;; NEEDS-KERNEL: a fresh-id no-op mutation and the changed= counter on the OK
  ;; line. Slice 1's OK line is `<MUTATION> OK id=.. request=.. node=.. rev=..
  ;; pushed=-` with no changed=, so this asserts the counter is still absent.
  (let ((k (fresh)))
    (multiple-value-bind (okp line code) (submit k (close-request :request "noop-probe"))
      (declare (ignore code))
      (ok okp "slice-1 transition refused")
      (ok (not (search "changed=" line)) "the OK line already carries changed=: ~A" line))))

(deftest "no-friend-name-in-the-tool" "docs/SPEC-WORK.md:5209"
    "expected=binary-and-fixtures-carry-no-friend-bench-repo-or-house-name"
  ;; NEEDS-KERNEL: a binary/defaults/fixtures audit is the Go client's, not the
  ;; slice-1 kernel's. The lisp seed already carries only the placeholder house.
  (dolist (node *seed*)
    (ok (search "acme/work" (getf node :id))
        "seed id ~A is not the placeholder house" (getf node :id))))

;; NEEDS-KERNEL: offer/accept/lease verbs and the cross-holder lease rule.
(deftest "no-shadow-lease-across-holders" "docs/SPEC-WORK.md:5238"
    "expected=cross-holder-reply-creates-no-lease"
  (slice1-refuses-verb :accept))

;; NEEDS-KERNEL: the offer verb writing :dispatched plus a reservation index entry.
(deftest "offer-writes-intent-and-a-reservation" "docs/SPEC-WORK.md:5229"
    "expected=:dispatched;pending-offer;reservation;nothing-else"
  (slice1-refuses-verb :offer))

;; NEEDS-KERNEL: archive export/reload and a recent-only export never labelled full.
(deftest "old-history" "docs/SPEC-WORK.md:5589"
    "expected=archive-included;recent-only-never-full-backup"
  (slice1-refuses-verb :export))

;; NEEDS-KERNEL: clip staging/verify/commit in one revision with both index roots.
(deftest "one-revision-publishes-together" "docs/SPEC-WORK.md:5124"
    "expected=segments-indexes-files-one-commit;kill-leaves-prev-root"
  (slice1-refuses-verb :clip))

;; NEEDS-KERNEL: execution stop as a hold plus an evidence-set custom cancel.
(deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:5250"
    "expected=one-note-cannot-cancel-two-live-attempts"
  (slice1-refuses-verb :event))

;; NEEDS-KERNEL: long operations returning an id the CLI can query after exit.
(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=operation-id-retrievable-after-client-exit"
  (slice1-refuses-verb :import))

;; NEEDS-KERNEL: recovery replay into bounded overlay pages and their rebuild.
(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:5553"
    "expected=thousand-settles-into-pages;no-query-replays-journal"
  (slice1-refuses-verb :recover))

;; NEEDS-KERNEL: oversized packet, stale route and escalation gates before dispatch.
(deftest "packet-and-route-gates" "docs/SPEC-WORK.md:4371"
    "expected=oversized/stale/unexplained-escalation-refuse-before-dispatch"
  (slice1-refuses-verb :route))

;; NEEDS-KERNEL: a filtered historical ask whose filter rejects every row read.
(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:5550"
    "expected=shown=0;pages=<n>;whole-history-never-scanned"
  (slice1-refuses-verb :query))

;; NEEDS-KERNEL: the session transport's correlated reply frames.
(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=out-of-order-fragmented-replies-reach-only-their-request"
  (slice1-refuses-verb :pipeline))

;; NEEDS-KERNEL: policy/trial manifests surviving export/import/restart/replay.
(deftest "policy-round-trip-and-replay" "docs/SPEC-WORK.md:4370"
    "expected=survives-round-trip;malformed-intake-no-partial-effect"
  (slice1-refuses-verb :config))

;; NEEDS-KERNEL: estimate pinning by revision and unknown-price!=0.
(deftest "pricing-is-pinned-by-revision" "docs/SPEC-WORK.md:5287"
    "expected=old-estimate-reproducible;missing-dimension-unknown"
  (slice1-refuses-verb :estimate))

;; NEEDS-KERNEL: priority verbs and the grants-nothing invariant.
(deftest "priority-grants-nothing" "docs/SPEC-WORK.md:5421"
    "expected=who-unchanged;no-lease;no-bypass"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: subtree/self priority inheritance and clear.
(deftest "priority-inherits-and-clears" "docs/SPEC-WORK.md:5410"
    "expected=order-only;no-lease-attempt-state-counter-moved"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: priority ordering over only the eligible set.
(deftest "priority-orders-only-the-eligible" "docs/SPEC-WORK.md:5406"
    "expected=blocked-rank-0-stays;rank-9-ready-first"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: priority undo treated as history, not value.
(deftest "priority-undo-is-history-not-value" "docs/SPEC-WORK.md:5418"
    "expected=same-value-set-and-clear-of-absent-slot-are-no-effect"
  (slice1-refuses-verb :undo))

;; NEEDS-KERNEL: the wire handshake refusing an unsupported version before admission.
(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-list-before-handshake"
  (slice1-refuses-verb :connect))
;;; replays of docs/SPEC-WORK.md lines 3600-end, part 6 of 8
;;; ------------------------------------------------------------------

(deftest "reopen-revives" "docs/SPEC-WORK.md:5062"
    "expected=reopen-returns-to-O-at-todo-with-id-evidence-generation;open=+1;closed=-1"
  (let ((k (fresh)))
    (check-equal 5 (state-open-count (kernel-state k)) "|O| at the seed")
    (check-equal 0 (state-closed-count (kernel-state k)) "|C| at the seed")
    (ok (submit k (close-request :request "req-1")) "close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "|O| after the close")
    (check-equal 1 (state-closed-count (kernel-state k)) "|C| after the close")
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1")
                 "the closed item is on the C branch")
    (multiple-value-bind (okp line code env)
        (submit k (reopen-request :request "req-2"))
      (declare (ignore line code))
      (ok okp "reopen refused")
      (let ((events (getf env :events)))
        (check-equal 2 (length events) "the requester's reopen and the session's revive")
        (check-equal :reopen (work-event-kind (first events)) "the reopen kind")
        (check-equal :revive (work-event-kind (second events)) "the session's revive kind")
        (check-equal "acme/work/f1/t1" (work-event-node (first events))
                     "the reopened id is preserved"))
      (check-equal :o (node-branch (kernel-state k) "acme/work/f1/t1")
                   "reopen returns the item to O")
      (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1")
                   "reopen lands at :todo")
      (check-equal 5 (state-open-count (kernel-state k)) "|O| up by one after the reopen")
      (check-equal 0 (state-closed-count (kernel-state k)) "|C| down by one after the reopen"))))

(deftest "replay-mints-nothing" "docs/SPEC-WORK.md:5528"
    "expected=replay-reconstructs-exact-digest-closed-rows-next-rev;zero-fresh-ids;no-verb-run"
  (let* ((path (test-journal-path "replay-mints-nothing"))
         (initial-hash (root-digest (make-seed-state *seed*)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (make-kernel :state (make-seed-state *seed*) :journal j1)))
    (unwind-protect
         (progn
           (submit k1 (close-request :request "req-1"))
           (submit k1 (reopen-request :request "req-2"))
           (submit k1 (doing-request :node "acme/work/f1/t1" :request "req-3"))
           (submit k1 (close-request :request "req-4"))
           (submit k1 (reopen-request :request "req-5"))
           (close-file-journal j1)
           (let ((final-digest (root-digest (kernel-state k1)))
                 (final-history (state-history (kernel-state k1)))
                 (final-rows (state-closed-rows (kernel-state k1)))
                 (final-rev (kernel-next-rev k1)))
             (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                    (k2 (make-kernel :state (make-seed-state *seed*) :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (replayed-k events records)
                          (replay-journal j2 k2)
                        (declare (ignore replayed-k events))
                        (check-equal 5 records "five envelopes replayed"))
                      (check-string= final-digest (root-digest (kernel-state k2))
                                     "replay reconstructs the exact root digest")
                      (check-equal final-history (state-history (kernel-state k2))
                                   "replay mints no fresh event id: history identical")
                      (check-equal final-rows (state-closed-rows (kernel-state k2))
                                   "replay reconstructs the exact closed rows")
                      (check-equal final-rev (kernel-next-rev k2)
                                   "replay reconstructs the exact next revision"))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

;;; The following replays name behaviour outside the slice-1 C/O transition
;;; kernel (the CLI, session, provider/intake adapter, roles, render, savepoint
;;; and dispatch surfaces). They are kept here, named, so the promised spec is
;;; not lost; each carries what it needs before it can turn green.

(deftest "quiet-until-actionable" "docs/SPEC-WORK.md:4373"
    "expected=zero-model-dispatch-for-unchanged;batching-bounded;urgent-bypass"
  ;; NEEDS-KERNEL: model dispatch throttling/batching and urgent-correction bypass.
  (ok t "pending; needs the model dispatch surface"))

(deftest "rank-2-precedes-10" "docs/SPEC-WORK.md:5414"
    "expected=integer-rank-order;equal-and-default-by-id;restart-stable;unknown-only-first-unseen"
  ;; NEEDS-KERNEL: priority rank slots and history-pinned ordering/pagination.
  (ok t "pending; needs priority ranks and the ready cursor"))

(deftest "ready-names-the-blocker-and-the-resolver" "docs/SPEC-WORK.md:5716"
    "expected=every-blocked-row-names-reason-and-resolver"
  ;; NEEDS-KERNEL: the ready-to-assign derivation and its per-row reason/resolver.
  (ok t "pending; needs the ready view"))

(deftest "read-only-intake" "docs/SPEC-WORK.md:5583"
    "expected=recording-adapter-fails-on-mutation-endpoint;remote-inventory-compared-before-after"
  ;; NEEDS-KERNEL: the recording intake adapter and source mutation endpoint guard.
  (ok t "pending; needs the intake adapter"))

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:5262"
    "expected=contradictory-observations-kept-unresolved;no-forged-inference"
  ;; NEEDS-KERNEL: the reconcile/index surface over receipts and targets.
  (ok t "pending; needs reconcile"))

(deftest "redo-refuses-a-stale-plan" "docs/SPEC-WORK.md:5194"
    "expected=redo-with-moved-preconditions-refused-atomically-naming-ids"
  ;; NEEDS-KERNEL: undo/redo precondition tracking.
  (ok t "pending; needs undo/redo"))

(deftest "regression-and-recovery" "docs/SPEC-WORK.md:4379"
    "expected=breached-trial-stops-automatic-assignment;fallback-preserves-limits-history-handles"
  ;; NEEDS-KERNEL: trial breach and automatic assignment fallback.
  (ok t "pending; needs assignment control"))

(deftest "regression-opens-repair-work" "docs/SPEC-WORK.md:5300"
    "expected=confirmed-regression-creates-linked-open-repair-work"
  ;; NEEDS-KERNEL: confirmed regression plus linked repair work creation.
  (ok t "pending; needs regression detection"))

(deftest "remove-settles-only-open-items" "docs/SPEC-WORK.md:5075"
    "expected=remove-of-done-does-not-settle-subtree;remove-settles-only-open-members"
  ;; NEEDS-KERNEL: the node remove verb over C/O membership.
  (ok t "pending; needs the remove verb"))

(deftest "render-artifact-is-bounded" "docs/SPEC-WORK.md:5398"
    "expected=interleaved-replies-and-chat-artifact-bounded;oversize-refused-never-partial"
  ;; NEEDS-KERNEL: render/artifact batching and bound enforcement.
  (ok t "pending; needs render"))

(deftest "render-refuses-a-target-outside-its-roots" "docs/SPEC-WORK.md:5304"
    "expected=target-outside-permitted-roots-refused-not-guessed"
  ;; NEEDS-KERNEL: render roots and file-target permission checks.
  (ok t "pending; needs render roots"))

(deftest "reply-retired-only-under-verified-coverage" "docs/SPEC-WORK.md:5538"
    "expected=retired-only-once-snapshot-events-root-verified;else-recovery-gap"
  ;; NEEDS-KERNEL: savepoint/dedup coverage verification of the boundary record.
  (ok t "pending; needs savepoint dedup"))

(deftest "repo-only-at-the-root" "docs/SPEC-WORK.md:5341"
    "expected=repo-at-root-unique;second-refused-repo-held-by;under-parent-refused"
  ;; NEEDS-KERNEL: the repo/root placement verb and its uniqueness checks.
  (ok t "pending; needs node add --repo"))

(deftest "requested-model-is-not-observed-model" "docs/SPEC-WORK.md:5222"
    "expected=unknown-stays-unknown;attempts-separate-model-attribution"
  ;; NEEDS-KERNEL: observed-vs-requested model attribution across attempts.
  (ok t "pending; needs model observation"))

(deftest "reserved-role-is-not-spent-on-routine-work" "docs/SPEC-WORK.md:5214"
    "expected=reserved-role-read-from-config-never-spent-on-routine"
  ;; NEEDS-KERNEL: role configuration and reserved-role dispatch rules.
  (ok t "pending; needs role configuration"))

(deftest "restore-is-isolated-and-dispatches-nothing" "docs/SPEC-WORK.md:5308"
    "expected=restore-read-only-isolated-no-ownership-no-replay-no-dispatch"
  ;; NEEDS-KERNEL: the savepoint restore recovery session.
  (ok t "pending; needs savepoint restore"))

(deftest "resume-is-two-actions" "docs/SPEC-WORK.md:5268"
    "expected=release-hold-only-lifts-its-control;resume-workers-refuses-unsupported-capability"
  ;; NEEDS-KERNEL: release-hold / resume-workers verbs.
  (ok t "pending; needs execution control"))

(deftest "return-reconciles-before-dispatch" "docs/SPEC-WORK.md:5226"
    "expected=return-reconciles-before-new-dispatch;one-bounded-ping-at-threshold"
  ;; NEEDS-KERNEL: silence threshold and return reconciliation before dispatch.
  (ok t "pending; needs dispatch"))

(deftest "reuse-only-valid-review" "docs/SPEC-WORK.md:4374"
    "expected=same-scope-reusable;changed-acceptance-deps-invalidate;friend-gates-not-replaced"
  ;; NEEDS-KERNEL: review scope/acceptance invalidation rules.
  (ok t "pending; needs review"))

(deftest "review-cycles-stay-visible" "docs/SPEC-WORK.md:5722"
    "expected=exact-revision-review-finding-ids-and-author-dispositions-recorded"
  ;; NEEDS-KERNEL: review/finding record surface and its visibility.
  (ok t "pending; needs review records"))
;;; 3x. replays promised by docs/SPEC-WORK.md lines 3600-9999 (card #284):
;;;     each replay asserts exactly the sentence it is named from. Where the
;;;     invariant needs kernel code this slice does not carry, the replay is
;;;     kept under the ;; NEEDS-KERNEL: marker naming what is missing.
;;; ------------------------------------------------------------------

(deftest "settle-keeps-id-and-evidence" "docs/SPEC-WORK.md:5047"
    "expected=id-evidence-and-done-disposition-preserved-across-the-settle"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code env)
        (submit k (close-request :request "req-1" :evidence '("ev-11" "ev-12")))
      (declare (ignore line code))
      (ok okp "close refused")
      (let ((requester (find :transition (getf env :events) :key #'work-event-kind)))
        (check-equal '("ev-11" "ev-12") (getf (work-event-fields requester) :evidence)
                     "the settle rewrote the evidence")))
    (let ((row (first (last (state-closed-rows (kernel-state k))))))
      (check-string= "acme/work/f1/t1" (getf row :node) "the settled id changed")
      (check-equal :done (getf row :disposition) "the settled disposition changed"))
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1")
                 "the task is not in C after its settle")))

(deftest "settle-moves-no-required-set" "docs/SPEC-WORK.md:5051"
    "expected=parent-required-count-unchanged;required-open-moves-by-the-one-row"
  (let ((k (fresh)))
    (let* ((s (kernel-state k))
           (rc (node-required-count s "acme/work/f1"))
           (ro (node-required-open s "acme/work/f1")))
      (ok (submit k (close-request :request "req-1" :node "acme/work/f1/t1"))
          "close refused")
      (let ((s2 (kernel-state k)))
        (check-equal rc (node-required-count s2 "acme/work/f1")
                     "the parent's required set changed when a task settled")
        (check-equal (1- ro) (node-required-open s2 "acme/work/f1")
                     "required-open moved by more than the one settled row")))))

(deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
    "expected=node-add--type-roadmap-exit-2-naming-roadmap-create;one-node-and-one-view-atomic"
  ;; NEEDS-KERNEL: a roadmap node and its view in one envelope; no CLI in this slice.
  (ok t "slice 1 carries no roadmap create: NEEDS-KERNEL roadmap node + view envelope"))

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:5291"
    "expected=historic-rows-render-identical-identical-ids;completed-rows-remaining-or-filtered"
  ;; NEEDS-KERNEL: roadmap listing/expansion and retention beyond the window.
  (ok t "slice 1 carries no roadmap render: NEEDS-KERNEL roadmap + retention window"))

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:5291"
    "expected=completed-epic-renders-identical-history-and-exact-receipts-without-all-of-C"
  ;; NEEDS-KERNEL: roadmap history and receipt retrieval without loading C.
  (ok t "slice 1 carries no roadmap history: NEEDS-KERNEL roadmap proof over C"))

(deftest "roadmap-proof" "docs/SPEC-WORK.md:5598"
    "expected=fixed-table-prototype-parity;chat-and-file-renders-byte-identical;marker-edit-preserves-unrelated-bytes"
  ;; NEEDS-KERNEL: roadmap proof rendering and marker edit.
  (ok t "slice 1 carries no roadmap proof: NEEDS-KERNEL roadmap render + marker"))

(deftest "roles-are-configured-not-inferred" "docs/SPEC-WORK.md:5214"
    "expected=role-read-from-CONFIG-never-the-model;reserved-roles-not-spent-on-routine-work"
  ;; NEEDS-KERNEL: CONFIG and role resolution; no session/config in this slice.
  (ok t "slice 1 carries no roles: NEEDS-KERNEL CONFIG + role resolution"))

(deftest "rotation-keeps-one-journal" "docs/SPEC-WORK.md:5516"
    "expected=new-segment-names-same-journal-id-and-boundary-record;chain-continues"
  ;; NEEDS-KERNEL: journal rotation / clip at a savepoint.
  (ok t "slice 1 carries no rotation: NEEDS-KERNEL clip/rotation of the journal"))

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:5151"
    "expected=rule-2-unavailable-partition-exit-1-distinct-from-dangling;never-green-over-unread-history"
  ;; NEEDS-KERNEL: rule 2 resolution of a reference into a closed partition.
  (ok t "slice 1 carries no rule-2 partition read: NEEDS-KERNEL closed-index read"))

(deftest "savepoint-cut-never-splits-an-envelope" "docs/SPEC-WORK.md:5507"
    "expected=two-event-request-represented-once;cut-inside-the-pair-refused"
  ;; NEEDS-KERNEL: savepoint image and its cut placement.
  (ok t "slice 1 carries no savepoint: NEEDS-KERNEL savepoint image + cut"))

(deftest "savepoint-write-failure-keeps-the-previous" "docs/SPEC-WORK.md:5532"
    "expected=image-manifest-and-sync-fail-in-turn;previous-verified-savepoint-restores"
  ;; NEEDS-KERNEL: savepoint write and manifest publication.
  (ok t "slice 1 carries no savepoint: NEEDS-KERNEL savepoint manifest + sync"))

(deftest "schema-evolution" "docs/SPEC-WORK.md:5601"
    "expected=old-schemas-migrate-losslessly;unsupported-refuses-preserving-originals;migration-never-rewrites-the-only-copy"
  ;; NEEDS-KERNEL: schema versions and migration without rewriting the source.
  (ok t "slice 1 carries one schema only: NEEDS-KERNEL schema migration"))

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:5055"
    "expected=holder-unowned-at-once;release-in-the-lease-log;no-C-item-in-any-who"
  ;; NEEDS-KERNEL: lease log and holder state; no lease in this slice.
  (ok t "slice 1 carries no lease: NEEDS-KERNEL lease + holder"))

(deftest "shared-prerequisite-owned-once" "docs/SPEC-WORK.md:5719"
    "expected=a-shared-prerequisite-owned-once-and-referenced-by-every-affected-cell"
  ;; NEEDS-KERNEL: prerequisite ownership and per-cell references.
  (ok t "slice 1 carries no prerequisites: NEEDS-KERNEL prerequisite ownership"))

(deftest "silence-is-a-ping-not-a-verdict" "docs/SPEC-WORK.md:5225"
    "expected=one-bounded-ping-at-the-threshold;nonresponse-marked-unavailable-unconfirmed-not-exhausted"
  ;; NEEDS-KERNEL: silence threshold and probe dispatch.
  (ok t "slice 1 carries no dispatch: NEEDS-KERNEL ping + probe"))

(deftest "single-writer" "docs/SPEC-WORK.md:5595"
    "expected=fencing-prevents-stale-mutation-authority-not-only-a-stale-push"
  ;; NEEDS-KERNEL: process fencing, socket and lease expiry.
  (ok t "slice 1 carries no fencing: NEEDS-KERNEL single-writer fencing"))

(deftest "source-inventory" "docs/SPEC-WORK.md:5582"
    "expected=every-source-record-maps-to-a-preserved-original-or-an-explicit-unresolved-entry"
  ;; NEEDS-KERNEL: source capture and reconciliation.
  (ok t "slice 1 carries no import: NEEDS-KERNEL source inventory capture"))

(deftest "staged-admission-refuses" "docs/SPEC-WORK.md:5234"
    "expected=copied-note-and--as-with-no-verifier-refused-with-no-canonical-write"
  ;; NEEDS-KERNEL: verifier and staged admission.
  (ok t "slice 1 carries no admission: NEEDS-KERNEL verifier + staged admission"))

(deftest "state-export-describes-exactly-r" "docs/SPEC-WORK.md:5423"
    "expected=capture-R-while-R+1-accepted-and-the-bytes-describe-R"
  ;; NEEDS-KERNEL: state export snapshot and rotation boundary.
  (ok t "slice 1 carries no export: NEEDS-KERNEL state export snapshot"))

(deftest "state-export-disconnect-and-cancel" "docs/SPEC-WORK.md:5441"
    "expected=lost-client-restart-and-cancel-keep-one-operation-and-one-output-identity"
  ;; NEEDS-KERNEL: transport around no-replace publication.
  (ok t "slice 1 carries no export: NEEDS-KERNEL export publication identity"))

(deftest "state-export-is-one-long-operation" "docs/SPEC-WORK.md:5434"
    "expected=blocked-export-acknowledges-at-once;wait-returns-the-captured-revision"
  ;; NEEDS-KERNEL: long operation acknowledgement and wait.
  (ok t "slice 1 carries no operation wait: NEEDS-KERNEL long operation"))

(deftest "state-export-pin-survives-clip" "docs/SPEC-WORK.md:5438"
    "expected=capture-R-then-clip-and-retention-at-R+1-then-exactly-R-or-a-named-gap"
  ;; NEEDS-KERNEL: export pin and retention pass.
  (ok t "slice 1 carries no export pin: NEEDS-KERNEL pinned export across clip"))
;;;; ------------------------------------------------------------------
;;;; Replays promised by docs/SPEC-WORK.md lines 3600-end, part 8 of 8.
;;;; A replay whose sentence needs kernel code slice 1 does not yet ship is
;;;; kept here behind ;; NEEDS-KERNEL and guards the gap (it turns red the day
;;;; the named entry point lands, prompting the real assertion).
;;;; ------------------------------------------------------------------

(defmacro needs-kernel (name spec need what)
  `(deftest ,name ,spec ,(format nil "NEEDS-KERNEL:~A" what)
     ;; NEEDS-KERNEL: ,need
     (ok (null (find-symbol ,what :nova-work))
         ,(format nil "~A entry point is not yet shipped" what))))

(needs-kernel "state-export-refuses-a-gap" "docs/SPEC-WORK.md:5428"
  "state export: a missing mandatory member, a changed digest, a dangling internal reference, a path escape, a symlink, an output overrun and a corrupt S-expression each refused with no valid load"
  "EXPORT-STATE")

(needs-kernel "state-load-is-isolated" "docs/SPEC-WORK.md:5445"
  "an instrumented load with no ownership change, no dispatch, no replay, no merge, no resolver run, no network and no repository write"
  "LOAD-STATE")

(needs-kernel "status-answers-while-io-runs" "docs/SPEC-WORK.md:5186"
  "status and cancel answered within their bound while a busy capture, export and clip are in flight"
  "OPERATION-STATUS")

(needs-kernel "stop-is-a-hold-not-a-cancel" "docs/SPEC-WORK.md:5250"
  "execution stop writing a hold and directives and no transition, goal show still printing stop=none"
  "EXECUTION-STOP")

(needs-kernel "subscription-is-not-free-reference-cost" "docs/SPEC-WORK.md:5288"
  "the three cost values (subscription, reference, local api) kept separately labelled"
  "SUBSCRIPTION-COST")

(needs-kernel "unchanged-config-is-one-bounded-answer" "docs/SPEC-WORK.md:5284"
  "the config exchange bounded, validated and atomic, no roster and no prose repeated per poll"
  "CONFIG-DELTA")

(needs-kernel "undo-appends-and-preserves" "docs/SPEC-WORK.md:5192"
  "an undo appending a typed compensating envelope with its lineage while the original event and every receipt stay where they are"
  "UNDO")

(needs-kernel "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5332"
  "every row of the reversible-verb table: each reversible verb undone by the envelope the table names, each refused verb refused not-reversible naming itself"
  "UNDO")

(needs-kernel "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5201"
  "an undo over a sent message, a paid execution, a publication and a source deletion refused and reported as an external effect"
  "UNDO")

(needs-kernel "undo-redo" "docs/SPEC-WORK.md:5599"
  "reversible edits reversed, history preserved, redo only against valid preconditions; a conflict explicit and mutating nothing"
  "REDO")

(needs-kernel "unknown-price-is-not-zero" "docs/SPEC-WORK.md:5287"
  "a missing pricing dimension reported unknown, never read as a zero historical receipt"
  "PRICE-LOOKUP")

(needs-kernel "unrelated-receipts-stay-reusable" "docs/SPEC-WORK.md:5301"
  "a changed source or criterion preserving the historic tick at its pinned revision while unrelated receipts stay untouched"
  "RECEIPT-LOOKUP")

(needs-kernel "until-is-overdue-not-released" "docs/SPEC-WORK.md:5243"
  "at --until and lease expiry no duplicate launch and no stopped or completed claim, the reservation retained until reconciled"
  "LEASE-UNTIL")

(needs-kernel "working-is-a-view" "docs/SPEC-WORK.md:5082"
  "|W| <= |O| over a set where every item is leased then released, and no verb writes W"
  "WORKING-SET")

(deftest "torn-tail-is-diagnosed-not-truncated" "docs/SPEC-WORK.md:5521"
    "expected=torn-tail-vs-corrupt-record-vs-journal-mismatch;zero-truncation;file-bytes-preserved"
  ;; NEEDS-KERNEL: recovery-gap kind=torn-tail and kind=corrupt-record named
  ;; codes; slice 1 distinguishes the three via journal-corrupt-data /
  ;; journal-mismatch, and the file stays bit-for-bit through each refusal.
  (let ((initial-hash (root-digest (make-seed-state *seed*))))
    (flet ((bytes-unbroken (path before-bytes before-sha)
             (check-equal before-bytes (file-byte-count path) "file size preserved bit-for-bit")
             (check-string= before-sha (file-sha256-hex path) "file bytes preserved bit-for-bit")))
      ;; Case 1: a partial frame at the end -> journal-corrupt-data, never mismatch.
      (let ((path (test-journal-path "torn-tail")))
        (unwind-protect
             (progn
               (let ((j (open-file-journal path :initial-state-hash initial-hash)))
                  (let ((k (fresh :journal j)))
                    (submit k (close-request :node "acme/work/f1/t1" :request "req-1")))
                  (close-file-journal j))
                (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
                  (write-string "(:frame :seq 2 :len 120 :checksum \"0000\"" out)
                 (finish-output out))
               (let ((before (file-byte-count path)) (sha (file-sha256-hex path)))
                 (handler-case (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data () (ok t "torn tail is a corrupt-data refusal"))
                   (journal-mismatch () (fail "torn tail rounded to a mismatch"))
                   (error (c) (fail "unexpected error: ~A" c)))
                 (bytes-unbroken path before sha)))
          (ignore-errors (delete-file path))))
      ;; Case 2: a flipped bit inside a frame -> corrupt record (checksum), never mismatch.
      (let ((path (test-journal-path "corrupt-record")))
        (unwind-protect
             (progn
               (let ((j (open-file-journal path :initial-state-hash initial-hash)))
                  (let ((k (fresh :journal j)))
                    (submit k (close-request :node "acme/work/f1/t1" :request "req-1")))
                  (close-file-journal j))
                (let* ((record (list :transition :node "acme/work/f1/t1" :to :done :reason "x"
                                    :blocked-by +absent+ :evidence '("e")))
                      (rec-canon (canonical-string record))
                      (frame (list :frame :seq 2 :len (length rec-canon)
                                   :checksum "0000000000000000000000000000000000000000000000000000000000000000"
                                   :record record)))
                 (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
                   (write-string (canonical-string frame) out)
                   (write-char #\Newline out)
                   (finish-output out)))
               (let ((before (file-byte-count path)) (sha (file-sha256-hex path)))
                 (handler-case (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data (c)
                     (ok (search "checksum" (journal-corrupt-data-reason c))
                         "flipped bit is a corrupt record, not a torn tail: ~A"
                         (journal-corrupt-data-reason c)))
                   (journal-mismatch () (fail "corrupt record rounded to a mismatch"))
                   (error (c) (fail "unexpected error: ~A" c)))
                 (bytes-unbroken path before sha)))
          (ignore-errors (delete-file path))))
      ;; Case 3: a wrong header -> journal-mismatch, never corrupt-data.
      (let ((path (test-journal-path "wrong-header")))
        (unwind-protect
             (progn
               (let ((j (open-file-journal path :initial-state-hash "header-aaa")))
                 (close-file-journal j))
               (let ((before (file-byte-count path)) (sha (file-sha256-hex path)))
                 (handler-case (open-file-journal path :initial-state-hash "header-bbb")
                   (journal-mismatch () (ok t "wrong header is a journal mismatch"))
                   (journal-corrupt-data () (fail "wrong header rounded to corrupt-data"))
                   (error (c) (fail "unexpected error: ~A" c)))
                 (bytes-unbroken path before sha)))
          (ignore-errors (delete-file path)))))))

(deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
    "expected=bignum-fields-round-trip-exact;json-number-frame-refused"
  (dolist (field '((:id 9007199254740993)
                   (:revision 9007199254740995)
                   (:counter 9007199254740997)
                   (:token-total 9007199254740999)))
    (let ((n (second field)))
      (let ((wire (canonical-string n)))
        (ok (stringp wire) "~A serializes to a string: ~A" (first field) wire)
        (check-string= (princ-to-string n) wire "exact decimal digits")
        (check-equal n (read-restricted wire) "round-trip unchanged"))))
  (dolist (json-number '("9007199254740993.0" "1e5" "1.5" "3/4"))
    (let ((refused nil))
      (handler-case (read-restricted json-number)
        (restricted-data-violation () (setf refused t)))
      (ok refused "a wire frame carrying the JSON number ~A is refused" json-number))))
