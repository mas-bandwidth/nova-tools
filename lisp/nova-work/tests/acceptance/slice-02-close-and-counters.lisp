;;;; slice-02-close-and-counters.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

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

(deftest "reopen-revives" "docs/SPEC-WORK.md:1584-1586,5062"
    "expected=open+1-closed-1;todo;revive-written"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "rr-1")) "close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "open after settle")
    (check-equal 1 (state-closed-count (kernel-state k)) "closed after settle")
    (multiple-value-bind (okp line code envelope) (submit k (reopen-request :request "rr-2"))
      (declare (ignore line code))
      (ok okp "reopen refused")
      (check-equal :reopen (work-event-kind (first (getf envelope :events))) "requester kind")
      (check-equal :revive (work-event-kind (second (getf envelope :events))) "session kind"))
    (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1") "reopened lands in :todo")
    (check-equal :o (node-branch (kernel-state k) "acme/work/f1/t1") "reopened is back in O")
    (check-equal 5 (state-open-count (kernel-state k)) "open +1 after the revive")
    (check-equal 0 (state-closed-count (kernel-state k)) "closed -1 after the revive")))

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:1674-1680,5055"
    "expected=settled-item-reads-holder-unowned"
  (let ((k (fresh)))
    (take-lease k "acme/work/f1/t1" "emma")
    (check-equal "emma" (node-holder (kernel-state k) "acme/work/f1/t1") "held by emma")
    ;; One live lease per node; a second take is refused and names the holder.
    (let ((refused nil))
      (handler-case (take-lease k "acme/work/f1/t1" "sam")
        (unsupported-input () (setf refused t)))
      (ok refused "a second take was accepted"))
    ;; A release from a third name is refused too.
    (let ((refused nil))
      (handler-case (release-lease k "acme/work/f1/t1" "sam")
        (unsupported-input () (setf refused t)))
      (ok refused "a third name ended the claim"))
    (ok (submit k (close-request :request "srl-1" :by "rowan")) "close refused")
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1") "the item settled")
    (check-equal nil (node-holder (kernel-state k) "acme/work/f1/t1")
                 "a settled item reads holder=unowned")
    ;; `take --node` now writes a `:lease` event of its own, so the log holds
    ;; the take and then the release it ended (nova-tools #785 rule 3).
    (let ((rel (find :release (state-lease-log (kernel-state k)) :key (lambda (e) (getf e :kind)))))
      (ok rel "the settle wrote a release")
      (check-equal "rowan" (getf rel :by) "the settling author wrote it")
      (check-equal "emma" (getf rel :holder) "it names the holder it ended"))))

(deftest "working-is-a-view" "docs/SPEC-WORK.md:1682-1689,5082"
    "expected=w-subset-o-no-verb-writes-w"
  (let ((k (fresh)))
    (check-equal 5 (state-open-count (kernel-state k)) "seed |O|")
    (check-equal 0 (working-count k) "|W| starts at zero")
    (check-equal :pending (node-disposition (kernel-state k) "acme/work/f1/t1")
                 "a :doing item with no live lease is pending")
    (take-lease k "acme/work/f1/t1" "emma")
    (take-lease k "acme/work/f1/t2" "sam")
    (check-equal 2 (working-count k) "two live leases")
    (check-equal :working (node-disposition (kernel-state k) "acme/work/f1/t1")
                 "a leased open item is working")
    (ok (<= (working-count k) (state-open-count (kernel-state k))) "|W| <= |O|")
    ;; W is a view: an independent walk agrees and no slot writes it.
    (check-equal (independent-working-count (kernel-state k)) (working-count k)
                 "W is the walk's own count")
    (ok (null (find-symbol "WSTATE-WORKING" :nova-work)) "no verb writes W")
    (release-lease k "acme/work/f1/t1" "emma")
    (check-equal 1 (working-count k) "release shrinks W")
    ;; A settled item leaves O and so leaves W; it is done, not working.
    (ok (submit k (close-request :node "acme/work/f1/t2" :request "wiv-1" :by "sam"))
        "close of a leased item refused")
    (check-equal 0 (working-count k) "the settled item left W")
    (check-equal :done (node-disposition (kernel-state k) "acme/work/f1/t2") "settled is done")
    (ok (<= (working-count k) (state-open-count (kernel-state k))) "|W| <= |O| after settle")))
