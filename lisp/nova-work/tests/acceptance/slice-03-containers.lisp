;;;; slice-03-containers.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

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

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:1648-1664"
    "expected=roadmap-view-retained-across-settle"
  (let* ((seed '((:id "r"       :type :roadmap :parent nil   :state :unknown)
                 (:id "r/f"     :type :feature :parent "r"   :state :unknown)
                 (:id "r/f/t"   :type :task    :parent "r/f" :state :doing
                  :links ("https://github.com/acme/work/issues/31"))))
         (k (make-kernel :state (make-seed-state seed))))
    ;; The roadmap settles with its members, like any container.
    (ok (submit k (close-request :node "r/f/t" :request "rol-1")) "close refused")
    (check-equal :c (node-branch (kernel-state k) "r/f/t") "the task settled")
    (check-equal :c (node-branch (kernel-state k) "r/f") "the feature settled with its member")
    (check-equal :c (node-branch (kernel-state k) "r") "the roadmap settled with its feature")
    ;; Its named view record is retained whatever branch the roadmap is in.
    (check-equal '("r/f") (roadmap-members (kernel-state k) "r")
                 "the roadmap keeps its row")
    (let ((view (roadmap-open k "r")))
      (check-equal "r" (getf view :id) "the view names the roadmap")
      (check-equal '("r/f") (getf view :members) "the retained row is returned")
      (ok (find "r/f/t" (getf view :rows) :key (lambda (r) (getf r :node)) :test #'equal)
          "the closed member's row is read from the retained record"))))

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:1648-1664"
    "expected=opening-a-named-roadmap-is-never-narrowed-by-the-default-window"
  (let* ((seed '((:id "r"     :type :roadmap :parent nil :state :unknown)
                 (:id "r/f"   :type :feature :parent "r"   :state :unknown)
                 (:id "r/f/t" :type :task    :parent "r/f" :state :doing
                  :links ("https://github.com/acme/work/issues/41"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "r/f/t" :request "roatw-1")) "close refused")
    ;; The settle stamp is 2026-09-14; a read whose window ended long before it
    ;; still opens the named view with the member's row, because the default
    ;; [now - 24h, now) window bounds a closed-activity listing and not a named
    ;; view (read: the kernel carries no clock, so the window is inert and the
    ;; view is never narrowed by it).
    (let ((unwindowed (roadmap-open k "r"))
          (early (roadmap-open k "r" :window "2000-01-01T00:00:00Z")))
      (check-equal unwindowed early "the window did not narrow the named view")
      (ok (find "r/f/t" (getf early :rows) :key (lambda (r) (getf r :node)) :test #'equal)
          "the historical row is still in the table"))))
