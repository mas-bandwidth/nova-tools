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

;;;; acceptance.lisp --- loader for the per-slice replay files (nova-tools #560).
;;;;
;;;; One file per replay slice so parallel PRs stop conflicting: each slice
;;;; lives in acceptance/<slice>.lisp and this top file only loads them.
;;;; An amendment edits one slice file, never this loader and never two
;;;; slices at once. The shared seed and request helpers stay here so every
;;;; slice sees the same canonical fixtures.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (unless (find-package :asdf)
    (require :asdf)))

;; The slice files, in canonical order. Each is one replay slice; an
;; amendment edits one of them (nova-tools #560).
(defparameter *acceptance-slices*
  '("slice-01-reader.lisp"
    "slice-02-close-and-counters.lisp"
    "slice-03-containers.lisp"
    "slice-04-doing-and-journal.lisp"
    "slice-05-durable-journal.lisp"
    "slice-06-replays-early.lisp"
    "slice-07-replays-mid.lisp"
    "slice-08-replays-late.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-10-fleet.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
;;;; The wire, protocol-version, disconnect, pipeline-correlation and
;;;; long-operation replays (nova-tools card 8608). The slice files keep
;;;; the names as placeholders; these are the executable paragraphs.
;;;; ------------------------------------------------------------------

(deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
    "expected=bignum-fields-round-trip-exact;json-number-frame-refused;null-and-absent-alike;empty-string-and-array-are-values"
  ;; An id, a revision, a counter and a token total, each above 2^53, cross
  ;; the wire and return unchanged (SPEC-WORK.md:2663-2666).
  (dolist (n '(9007199254740993 9007199254740995 9007199254740997 9007199254740999))
    (let ((wire (wire-encode-integer n)))
      (ok (stringp wire) "the integer ~D is a JSON string, not a number" n)
      (check-string= (format nil "\"~D\"" n) wire "exact decimal digits")
      (check-equal n (wire-decode-value wire) "round-trip unchanged")))
  ;; A frame carrying a JSON number is refused.
  (let ((refused nil))
    (handler-case (wire-decode-value "9007199254740993")
      (unsupported-input () (setf refused t)))
    (ok refused "a frame carrying a bare JSON number is refused"))
  ;; null and an absent key read alike; an empty string and an empty array
  ;; are values (:2669-2672).
  (check-equal +absent+ (wire-decode-value "null") "null reads as not given")
  (check-equal +absent+ (wire-field '(("id" . 1)) "missing") "an absent key reads as not given")
  (check-equal (wire-decode-value "null") (wire-field '() "missing")
               "null and an absent key read alike")
  (check-string= "" (wire-decode-value "\"\"") "an empty string is a value")
  (check-equal '() (wire-decode-value "[]") "an empty array is a value")
  (ok (not (absentp (wire-decode-value "[]"))) "an empty array is not absent")
  (let ((object (wire-object-decode "{\"revision\":\"9007199254740995\"}")))
    (check-equal 9007199254740995 (wire-field object "revision")
                 "an object field above 2^53 round-trips as a string")
    (check-equal +absent+ (wire-field object "missing") "an absent object key is absent")))

(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-supported-list;no-request-before-handshake;oversized-frame-refused-with-one-framed-error"
  ;; A client offering an unsupported version is refused with the supported
  ;; list named and the connection closed (SPEC-WORK.md:2682-2685).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("2"))
      (ok (null version) "an unsupported version is refused")
      (check-equal '("1") refusal "the supported list is named")
      (ok (protocol-session-closed-p sess) "the connection is closed"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok (null admitted) "no request is admitted after the refusal")
      (ok (stringp why) "with a reason")))
  ;; No request is admitted before the handshake.
  (let ((sess (make-protocol-session :supported '("1"))))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-0")
      (ok (null admitted) "no request is admitted before the handshake")
      (ok (stringp why) "with a reason")))
  ;; A supported version is answered; an oversized frame is refused with one
  ;; framed error before the close (:2661-2663, :2682).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("1"))
      (check-string= "1" version "the one version the session will speak")
      (ok (null refusal) "no refusal for a supported version"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok admitted "a request after the handshake is admitted")
      (ok (null why) "with no reason"))
    (ok (not (protocol-frame-ok-p sess 17)) "an oversized frame is not ok")
    (ok (protocol-frame-ok-p sess 16) "a frame at the bound is ok")
    (let ((line (protocol-framed-error sess "frame exceeds max-frame-bytes")))
      (ok (search "request=null" line) "one framed error carries a null id")
      (ok (protocol-session-closed-p sess) "the connection is closed after it"))))

(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=every-response-reaches-only-its-request;operation-id-distinct;unknown-duplicate-absent-close;batches"
  (let ((conn (make-wire-connection :supported '("1"))))
    (multiple-value-bind (version refusal) (protocol-hello (wire-connection-protocol conn) '("1"))
      (ok (and (stringp version) (null refusal)) "the handshake finishes first"))
    ;; Pipeline two queries, a mutation and a long-operation acceptance.
    (dolist (entry '(("q-1" . :query) ("q-2" . :query)
                     ("m-1" . :mutation) ("op-1" . :operation)))
      (check-equal (car entry) (wire-pipeline-request conn (car entry) (cdr entry))
                   "the request is in flight"))
    ;; A fragment dispatches nothing until its frame is complete, and frames
    ;; delivered out of order still reach only their matching request.
    (check-equal '() (wire-feed conn "q-2 ") "an incomplete frame dispatches nothing")
    (check-equal '() (wire-feed conn "bo") "a second fragment still dispatches nothing")
    (let ((frames (wire-feed conn (format nil "dy~%op-1 accept state=running~%")))
          (seen '()))
      (check-equal '("q-2 body" "op-1 accept state=running") frames "two complete frames")
      (dolist (frame frames)
        (multiple-value-bind (id body) (wire-dispatch conn frame)
          (ok (stringp id) "a complete response is correlated to a request")
          (ok (stringp body) "with its own body")
          (push id seen)))
      (check-equal '("q-2" "op-1") (reverse seen) "each response reached only its request"))
    ;; The operation id remains distinct from the request id.
    (check-equal '("q-1" "m-1") (mapcar #'car (wire-connection-outstanding conn))
                 "the operation id is not the mutation request id")
    ;; An independent batch stops at the first refusal and marks the rest.
    (let ((out (independent-batch-results
                '(("b-1" . :applied) ("b-2") ("b-3" . :applied)))))
      (check-equal '("b-1" :applied) (first out) "the first entry is applied")
      (check-equal '("b-2" :refused) (second out) "the refusal is named")
      (check-equal '("b-3" :not-attempted) (third out) "the rest is not attempted"))
    ;; An atomic batch names its validation failure and does not partially apply.
    (multiple-value-bind (all-ok failed) (atomic-batch-validate
                                           '(("a-1" . t) ("a-2") ("a-3" . t)))
      (ok (null all-ok) "the atomic batch is refused whole")
      (check-string= "a-2" failed "the failing entry is named"))
    (multiple-value-bind (all-ok failed) (atomic-batch-validate '(("a-1" . t) ("a-2" . t)))
      (ok all-ok "a valid atomic batch is admitted")
      (ok (null failed) "with no failing entry")))
  ;; An unknown response id closes the connection without settling anything.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-1" :query)
    (multiple-value-bind (id why) (wire-dispatch conn "nope body")
      (ok (null id) "an unknown response id settles no outstanding request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; A duplicate response id closes the connection too.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-1" :query)
    (wire-dispatch conn "q-1 first")
    (multiple-value-bind (id why) (wire-dispatch conn "q-1 second")
      (ok (null id) "a duplicate response id settles no second request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; A frame with no decodable id gets a null-id refusal and admits nothing.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-9" :query)
    (multiple-value-bind (id why) (wire-dispatch conn "null oops")
      (ok (null id) "a null response id acknowledges no queued request")
      (ok (search "request=null" why) "the refusal carries a null id")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; Reconnect after a lost mutation response: same-id reconciliation applies
  ;; no second event.
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "lost-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (setf (wire-session-client-alive-p sess) nil)
      (let ((before (length (state-history (kernel-state kernel))))
            (reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2) (wire-session-mutate reconnected request)
          (ok ok2 "the retry on the new connection is accepted")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the recorded disposition is returned")
          (check-equal before (length (state-history (kernel-state kernel)))
                       "same-id reconciliation applies no second event"))))))

(deftest "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
    "expected=event-stands;same-id-same-disposition;changed-args-refused;rev!=pushed"
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "disc-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code response) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (check-equal 1 (length (state-history (kernel-state kernel)))
                   "the accepted event stands")
      (ok (getf response :rev) "the response carries rev=")
      (ok (getf response :pushed) "the response carries pushed=")
      (ok (not (equal (getf response :rev) (getf response :pushed)))
          "rev= and pushed= are distinct in the response")
      ;; The client is killed; a fresh connection retries the same id and body.
      (setf (wire-session-client-alive-p sess) nil)
      (let ((reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2 response2) (wire-session-mutate reconnected request)
          (ok ok2 "the same request id and body return the recorded disposition")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the disposition is unchanged")
          (check-equal 1 (length (state-history (kernel-state kernel)))
                       "the event stands and no second event is applied")
          (ok (not (equal (getf response2 :rev) (getf response2 :pushed)))
              "rev= and pushed= stay distinct")))
      ;; The same id with different arguments is refused.
      (let ((different (copy-list request)))
        (setf (getf different :reason) "something else")
        (multiple-value-bind (ok3 line3 code3) (wire-session-mutate sess different)
          (ok (null ok3) "the same id with different arguments is refused")
          (check-equal 1 code3 "exit 1")
          (ok (search "different payload" line3) "naming the payload reuse"))))))

(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=import-returns-op-id;cli-exit-leaves-work;result-by-id;wait-timeout-leaves-running"
  ;; A long import returns a durable operation id at once, recorded before it
  ;; is printed (SPEC-WORK.md:2719-2727).
  (let* ((registry (make-operation-registry))
         (op-id (operation-accept registry :id "op-1" :kind :import
                                   :request "req-import" :author "rowan"
                                   :stamp "2026-09-14T12:00:00Z")))
    (check-string= "op-1" op-id "a long import returns an operation id at once")
    (check-equal 1 (operation-journal-length registry)
                 "the id is durable before it is printed")
    (check-equal :running (operation-state registry op-id) "the work is running")
    ;; The CLI exits while the work continues.
    (operation-client-exit registry)
    (check-equal :running (operation-state registry op-id)
                 "the work continues after the client exits")
    ;; operation wait times out and leaves the operation running (:2733).
    (multiple-value-bind (result state) (operation-wait registry op-id :timeout 5)
      (ok (null result) "a wait timeout returns no result")
      (check-equal :timeout state "the wait reports the timeout")
      (check-equal :running (operation-state registry op-id)
                   "the operation is left running"))
    ;; A completed result is retrievable by its id afterwards.
    (operation-complete registry op-id "imported 42 items")
    (check-equal :done (operation-state registry op-id) "the operation completes")
    (check-string= "imported 42 items" (operation-result registry op-id)
                   "the result is retrievable by id afterwards")
    ;; An id no journal holds has a line of its own (:2727-2729).
    (multiple-value-bind (state line code) (operation-state registry "op-missing")
      (ok (null state) "an unknown operation has no state")
      (check-equal 2 code "exit 2")
      (ok (search "no such operation" line) "and its own line"))))
