;;;; replays-e01-f02-read-bounds.lisp --- E01-F02: Uniform read bounds
;;;; and schema validation (SPEC-WORK.md:837-847, 888-955).
;;;;
;;;;   TestE01F02RequireMaxBytesMaxDepth
;;;;   TestE01F02ApplySessionBoundsTo
;;;;   TestE01F02PreserveUnknownKeysAndRefuse

(in-package #:nova-work/tests)

(defvar *bounds-test-counter* 0)

(defun test-bounds-temp-dir (name)
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames (format nil "nova-work-test-bounds/~A-~D-~D/"
                                       name (get-universal-time)
                                       (incf *bounds-test-counter*))
                               base)))
    (ensure-directories-exist dir)
    (string-right-trim "/" (namestring (truename dir)))))

(defun write-temp-bounds-file (path text)
  (ensure-directories-exist path)
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create
                            :element-type 'character :external-format :utf-8)
    (write-string text out))
  path)

;;; ------------------------------------------------------------------
;;; E01-F02-01: Require max-bytes, max-depth and max-nodes on every file read
;;; (SPEC-WORK.md:837-845)
;;; ------------------------------------------------------------------

(deftest "TestE01F02RequireMaxBytesMaxDepth" "docs/SPEC-WORK.md:837-845"
    "expected=missing-bound-refuses-guessing;exceeded-bound-refuses-naming-bound-and-file;valid-reads-clean"
  (let* ((dir (test-bounds-temp-dir "e01-f02-req"))
         (export-dir (format nil "~A/export/" dir))
         (source (make-seed-state '((:id "root" :type :work-set :parent nil))))
         (sample-file (format nil "~A/sample.sexp" dir))
         (sample-content "((:id \"root\" :type :work-set :parent nil))"))
    (write-state-export source export-dir :id "exp-bounds")
    (write-temp-bounds-file sample-file sample-content)

    ;; 1. Missing bounds refuse before parsing finishes with "refusing to guess" (exit 2)
    ;; state-load missing bounds:
    (multiple-value-bind (snap line code)
        (state-load :from export-dir :into (format nil "~A/s1/" dir) :max-depth 10 :max-nodes 100)
      (ok (null snap) "state-load without max-bytes was accepted")
      (ok (search "missing --max-bytes: refusing to guess" line)
          "state-load did not report missing --max-bytes: ~A" line)
      (check-equal 2 code "missing --max-bytes did not exit 2"))

    (multiple-value-bind (snap line code)
        (state-load :from export-dir :into (format nil "~A/s2/" dir) :max-bytes 10000 :max-nodes 100)
      (ok (null snap) "state-load without max-depth was accepted")
      (ok (search "missing --max-depth: refusing to guess" line)
          "state-load did not report missing --max-depth: ~A" line)
      (check-equal 2 code "missing --max-depth did not exit 2"))

    (multiple-value-bind (snap line code)
        (state-load :from export-dir :into (format nil "~A/s3/" dir) :max-bytes 10000 :max-depth 10)
      (ok (null snap) "state-load without max-nodes was accepted")
      (ok (search "missing --max-nodes: refusing to guess" line)
          "state-load did not report missing --max-nodes: ~A" line)
      (check-equal 2 code "missing --max-nodes did not exit 2"))

    ;; read-bounded-file missing bounds:
    (multiple-value-bind (text line code)
        (read-bounded-file sample-file :max-depth 10 :max-nodes 100 :require-all t :signal-error nil)
      (ok (null text) "read-bounded-file without max-bytes succeeded")
      (ok (search "missing --max-bytes: refusing to guess" line)
          "read-bounded-file did not refuse missing max-bytes: ~A" line)
      (check-equal 2 code "missing --max-bytes did not exit 2"))

    (multiple-value-bind (text line code)
        (read-bounded-file sample-file :max-bytes 10000 :max-nodes 100 :require-all t :signal-error nil)
      (ok (null text) "read-bounded-file without max-depth succeeded")
      (ok (search "missing --max-depth: refusing to guess" line)
          "read-bounded-file did not refuse missing max-depth: ~A" line)
      (check-equal 2 code "missing --max-depth did not exit 2"))

    (multiple-value-bind (text line code)
        (read-bounded-file sample-file :max-bytes 10000 :max-depth 10 :require-all t :signal-error nil)
      (ok (null text) "read-bounded-file without max-nodes succeeded")
      (ok (search "missing --max-nodes: refusing to guess" line)
          "read-bounded-file did not refuse missing max-nodes: ~A" line)
      (check-equal 2 code "missing --max-nodes did not exit 2"))

    ;; missing bound signaled condition:
    (let ((signaled nil))
      (handler-case
          (read-bounded-file sample-file :max-depth 10 :max-nodes 100 :require-all t :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "condition named correct bound")))
      (ok signaled "missing-read-bounds condition was not signaled"))

    ;; 2. Exceeded bounds refuse exit 2, naming bound and file
    ;; Byte overrun:
    (let ((big-file (format nil "~A/big.sexp" dir)))
      (write-temp-bounds-file big-file "(:hello :world :this :is :a :longer :payload)")
      (multiple-value-bind (text line code)
          (read-bounded-file big-file :max-bytes 10 :max-depth 10 :max-nodes 100 :signal-error nil)
        (ok (null text) "byte overrun read succeeded")
        (ok (search "--max-bytes" line) "refusal omits --max-bytes: ~A" line)
        (ok (search (file-namestring big-file) line) "refusal omits filename: ~A" line)
        (check-equal 2 code "byte overrun did not exit 2"))
      (let ((signaled nil))
        (handler-case
            (read-bounded-file big-file :max-bytes 10 :max-depth 10 :max-nodes 100 :signal-error t)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "condition bound")
            (ok (search (file-namestring big-file) (read-bounds-exceeded-file c))
                "condition filename: ~A" (read-bounds-exceeded-file c))))
        (ok signaled "read-bounds-exceeded was not signaled for byte overrun")))

    ;; Depth overrun:
    (let ((deep-file (format nil "~A/deep.sexp" dir)))
      (write-temp-bounds-file deep-file "((((((nested))))))")
      (multiple-value-bind (text line code)
          (read-bounded-file deep-file :max-bytes 1000 :max-depth 2 :max-nodes 100 :signal-error nil)
        (ok (null text) "depth overrun read succeeded")
        (ok (search "--max-depth" line) "refusal omits --max-depth: ~A" line)
        (ok (search (file-namestring deep-file) line) "refusal omits filename: ~A" line)
        (check-equal 2 code "depth overrun did not exit 2"))
      (let ((signaled nil))
        (handler-case
            (read-bounded-file deep-file :max-bytes 1000 :max-depth 2 :max-nodes 100 :signal-error t)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-depth" (read-bounds-exceeded-bound c) "condition bound")))
        (ok signaled "read-bounds-exceeded was not signaled for depth overrun")))

    ;; Node count overrun:
    (let ((nodes-file (format nil "~A/nodes.sexp" dir)))
      (write-temp-bounds-file nodes-file "(a b c d e f g h i j k l m n o p)")
      (multiple-value-bind (text line code)
          (read-bounded-file nodes-file :max-bytes 1000 :max-depth 10 :max-nodes 5 :signal-error nil)
        (ok (null text) "node overrun read succeeded")
        (ok (search "--max-nodes" line) "refusal omits --max-nodes: ~A" line)
        (ok (search (file-namestring nodes-file) line) "refusal omits filename: ~A" line)
        (check-equal 2 code "node count overrun did not exit 2"))
      (let ((signaled nil))
        (handler-case
            (read-bounded-file nodes-file :max-bytes 1000 :max-depth 10 :max-nodes 5 :signal-error t)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-nodes" (read-bounds-exceeded-bound c) "condition bound")))
        (ok signaled "read-bounds-exceeded was not signaled for node overrun")))

    ;; 3. Valid file within bounds reads cleanly without truncation
    (multiple-value-bind (text line code)
        (read-bounded-file sample-file :max-bytes 1000 :max-depth 10 :max-nodes 50 :signal-error nil)
      (check-equal 0 code "valid read exited non-zero")
      (check-string= sample-content text "valid read returned truncated or altered content")
      (ok (search "READ OK" line) "result line is not READ OK: ~A" line))))

;;; ------------------------------------------------------------------
;;; E01-F02-02: Apply session bounds to snapshots, archives, journals,
;;; caches and replay bundles (SPEC-WORK.md:839-845)
;;; ------------------------------------------------------------------

(deftest "TestE01F02ApplySessionBoundsTo" "docs/SPEC-WORK.md:839-845"
    "expected=session-bounds-stored;snapshot-archive-journal-cache-bundle-governed;overrun-refuses-naming-bound-and-file;snapshot-mode-governs-snapshot-and-cache"
  (let* ((dir (test-bounds-temp-dir "e01-f02-session"))
         (sess (session-start :max-bytes 5000 :max-depth 12 :max-nodes 300))
         (tight-sess (session-start :max-bytes 15 :max-depth 10 :max-nodes 100))
         ;; Five files representing the five session read surfaces:
         (snap-file (write-temp-bounds-file (format nil "~A/snap.sexp" dir)
                                            "((:id \"root\" :type :work-set :parent nil))"))
         (arch-file (write-temp-bounds-file (format nil "~A/archive.sexp" dir)
                                            "((:archive-day \"2026-09-19\" :records ()))"))
         (journal-file (write-temp-bounds-file (format nil "~A/journal.sexp" dir)
                                               "((:kind :node-add :id \"n1\" :type :task))"))
         (cache-file (write-temp-bounds-file (format nil "~A/cache.sexp" dir)
                                             "(:pointer \"p1\" :subject \"s1\" :resolver \"r1\" :fact :passes :stamp \"2026-09-20T00:00:00Z\")"))
         (bundle-file (write-temp-bounds-file (format nil "~A/bundle.sexp" dir)
                                              "(:bundle-id \"b1\" :requests ())")))

    ;; 1. Session start stores bounds
    (check-equal 5000 (session-max-bytes sess) "session-max-bytes stored")
    (check-equal 12 (session-max-depth sess) "session-max-depth stored")
    (check-equal 300 (session-max-nodes sess) "session-max-nodes stored")

    ;; 2. Five session file types read cleanly under session bounds
    (multiple-value-bind (text line code) (session-read-snapshot sess snap-file)
      (check-equal 0 code "session-read-snapshot failed")
      (ok (search "READ OK" line) "session-read-snapshot line: ~A" line)
      (ok (search "root" text) "snapshot content recovered"))

    (multiple-value-bind (text line code) (session-read-archive sess arch-file)
      (check-equal 0 code "session-read-archive failed")
      (ok (search "READ OK" line) "session-read-archive line: ~A" line)
      (ok (search "2026-09-19" text) "archive content recovered"))

    (multiple-value-bind (text line code) (session-read-journal sess journal-file)
      (check-equal 0 code "session-read-journal failed")
      (ok (search "READ OK" line) "session-read-journal line: ~A" line)
      (ok (search "node-add" text) "journal content recovered"))

    (multiple-value-bind (text line code) (session-read-cache sess cache-file)
      (check-equal 0 code "session-read-cache failed")
      (ok (search "READ OK" line) "session-read-cache line: ~A" line)
      (ok (search "pointer" text) "cache content recovered"))

    (multiple-value-bind (text line code) (session-read-bundle sess bundle-file)
      (check-equal 0 code "session-read-bundle failed")
      (ok (search "READ OK" line) "session-read-bundle line: ~A" line)
      (ok (search "bundle-id" text) "bundle content recovered"))

    ;; 3. Overrun in any of the five files is refused at exit 2 naming bound and file
    (dolist (entry (list (cons "snapshot" snap-file)
                         (cons "archive" arch-file)
                         (cons "journal" journal-file)
                         (cons "cache" cache-file)
                         (cons "bundle" bundle-file)))
      (let ((name (car entry))
            (file (cdr entry)))
        (multiple-value-bind (text line code)
            (case (intern (string-upcase name) :keyword)
              (:snapshot (session-read-snapshot tight-sess file))
              (:archive (session-read-archive tight-sess file))
              (:journal (session-read-journal tight-sess file))
              (:cache (session-read-cache tight-sess file))
              (:bundle (session-read-bundle tight-sess file)))
          (ok (null text) "overrun on ~A read succeeded" name)
          (check-equal 2 code (format nil "overrun on ~A did not exit 2" name))
          (ok (search "--max-bytes" line) "overrun on ~A omits bound name: ~A" name line)
          (ok (search (file-namestring file) line) "overrun on ~A omits file name: ~A" name line))

        (let ((signaled nil))
          (handler-case
              (case (intern (string-upcase name) :keyword)
                (:snapshot (session-read-snapshot tight-sess file :signal-error t))
                (:archive (session-read-archive tight-sess file :signal-error t))
                (:journal (session-read-journal tight-sess file :signal-error t))
                (:cache (session-read-cache tight-sess file :signal-error t))
                (:bundle (session-read-bundle tight-sess file :signal-error t)))
            (read-bounds-exceeded (c)
              (setf signaled t)
              (check-equal "--max-bytes" (read-bounds-exceeded-bound c)
                           (format nil "~A condition bound" name))))
          (ok signaled "read-bounds-exceeded condition was not signaled on ~A" name))))

    ;; 4. Under --snapshot the reader's own three bounds govern snapshot and cache alike (SPEC-WORK.md:843-845)
    (let* ((export-dir (format nil "~A/exp-snap/" dir))
           (snap-dir (format nil "~A/loaded-snap/" dir))
           (src-state (make-seed-state '((:id "s1" :type :work-set :parent nil)))))
      (write-state-export src-state export-dir :id "exp-s")
      (state-load :from export-dir :into snap-dir :max-bytes 100000 :max-depth 10 :max-nodes 100)

      ;; Exceeded bounds on read-loaded-snapshot:
      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir :max-bytes 5)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-bytes"))

      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir :max-depth 1)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-depth" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-depth"))

      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir :max-nodes 1)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-nodes" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-nodes"))

      ;; Valid read of loaded snapshot within bounds succeeds:
      (let ((loaded (read-loaded-snapshot snap-dir :max-bytes 100000 :max-depth 10 :max-nodes 100)))
        (ok loaded "read-loaded-snapshot failed within bounds")
        (check-equal 1 (snapshot-query loaded) "snapshot-query answered open count")))))

;;; ------------------------------------------------------------------
;;; E01-F02-03: Preserve unknown keys and refuse unknown node types
;;; (SPEC-WORK.md:845-847, 888-955)
;;; ------------------------------------------------------------------

(deftest "TestE01F02PreserveUnknownKeysAndRefuse" "docs/SPEC-WORK.md:845-847,888-955"
    "expected=refuse-unknown-node-types;admit-known-types;preserve-unknown-keys-on-nodes;ignore-unknown-keys-in-operations"
  ;; 1. Refuse unknown node types (rule 1)
  ;; Refusal in make-seed-state:
  (let ((signaled nil))
    (handler-case
        (make-seed-state '((:id "bad-node" :type :bogus-kind :parent nil)))
      (unsupported-input (c)
        (setf signaled t)
        (ok (search "rule 1: unknown node type BOGUS-KIND" (unsupported-input-what c))
            "seed refusal text: ~A" (unsupported-input-what c))))
    (ok signaled "unknown node type was admitted in seed"))

  ;; Refusal in node-add (exit 2):
  (let ((k (fresh)))
    (multiple-value-bind (ok line code)
        (node-add k :id "bad-node-2" :type :invented-kind :parent "acme/work")
      (ok (null ok) "node-add with unknown type succeeded")
      (check-equal 2 code "node-add unknown type did not exit 2")
      (ok (search "rule 1: unknown node type INVENTED-KIND" line)
          "node-add refusal message: ~A" line)))

  ;; 2. Admit known node types (:work-set :epic :feature :roadmap :task :bug)
  (dolist (expected-type '(:work-set :epic :feature :roadmap :task :bug))
    (ok (member expected-type *known-node-types*)
        "~A is in *known-node-types*" expected-type))

  (let ((multi-state (make-seed-state
                      '((:id "ws" :type :work-set :parent nil)
                        (:id "ep" :type :epic :parent "ws")
                        (:id "ft" :type :feature :parent "ep")
                        (:id "tk" :type :task :parent "ft")
                        (:id "bg" :type :bug :parent "ft")))))
    (check-equal 5 (length (state-node-ids multi-state)) "all known types seeded cleanly"))

  (let ((k (fresh)))
    (multiple-value-bind (ok-bug line-bug code-bug)
        (node-add k :id "acme/work/f1/b1" :type :bug :parent "acme/work/f1")
      (ok ok-bug "node-add with :bug type failed: ~A" line-bug)
      (check-equal 0 code-bug "node-add with :bug did not exit 0")))

  ;; 3. Preserve unknown keys on nodes (SPEC-WORK.md:845-847)
  ;; In seed:
  (let* ((seed '((:id "acme/work" :type :work-set :parent nil
                  :team-owner "platform" :cost-center 4242)))
         (state (make-seed-state seed))
         (unknown (node-unknown-keys state "acme/work")))
    (check-equal "platform" (getf unknown :team-owner) "custom team-owner preserved")
    (check-equal 4242 (getf unknown :cost-center) "custom cost-center preserved"))

  ;; In node-add:
  (let ((k (fresh)))
    (multiple-value-bind (ok line code)
        (node-add k :id "acme/work/f1/custom" :type :task :parent "acme/work/f1"
                    :assigned-team "frontend" :sprint-number 14)
      (ok ok "node-add failed: ~A" line)
      (check-equal 0 code "node-add exited non-zero")
      (let ((unknown (node-unknown-keys k "acme/work/f1/custom")))
        (check-equal "frontend" (getf unknown :assigned-team) "custom assigned-team preserved")
        (check-equal 14 (getf unknown :sprint-number) "custom sprint-number preserved"))))

  ;; 4. Unknown keys are ignored by standard node operations
  (let* ((k (fresh))
         (node-id "acme/work/f1/t1"))
    ;; Add unknown keys to an existing node
    (setf (node-unknown-keys k node-id) '(:custom-label "sec-audit" :risk-score 9))
    (let ((unknown-before (node-unknown-keys k node-id)))
      (check-equal "sec-audit" (getf unknown-before :custom-label) "label set")
      (check-equal 9 (getf unknown-before :risk-score) "score set"))

    ;; State transition to done via close-request
    (ok (submit k (close-request :node node-id :request "req-close-1" :evidence '("ev-1")))
        "close-request refused")
    (check-equal :done (node-state (kernel-state k) node-id) "node transitioned to done")
    ;; Unknown keys preserved through close
    (check-equal "sec-audit" (getf (node-unknown-keys k node-id) :custom-label)
                 "unknown key preserved through close")

    ;; Reopen via reopen-request
    (ok (submit k (reopen-request :node node-id :request "req-reopen-1"))
        "reopen-request refused")
    (check-equal :todo (node-state (kernel-state k) node-id) "node transitioned back to todo")
    ;; Unknown keys preserved through reopen
    (check-equal 9 (getf (node-unknown-keys k node-id) :risk-score)
                 "unknown key preserved through reopen")

    ;; Structural queries unaffected
    (check-equal "acme/work/f1" (node-parent (kernel-state k) node-id) "parent intact")
    (ok (member node-id (node-children (kernel-state k) "acme/work/f1") :test #'equal)
        "child containment intact")))
