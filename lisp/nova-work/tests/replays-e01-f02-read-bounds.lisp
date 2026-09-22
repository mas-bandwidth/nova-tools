;;;; replays-e01-f02-read-bounds.lisp --- E01-F02: Uniform read bounds
;;;; and schema validation (SPEC-WORK.md:837-847, 888-955).
;;;;
;;;;   TestE01F02RequireMaxBytesMaxDepth
;;;;   TestE01F02ApplySessionBoundsTo
;;;;   TestE01F02PreserveUnknownKeysAndRefuse
;;;;   TestE01F02LexicalIntakeScanner

(in-package #:nova-work/tests)

(defvar *bounds-test-counter* 0)

#+sbcl
(defclass %test-counting-stream (sb-gray:fundamental-binary-input-stream)
  ((count :initform 0 :accessor %test-counting-stream-count)
   (limit :initform 1000000 :initarg :limit :accessor %test-counting-stream-limit)))

#+sbcl
(defmethod cl:stream-element-type ((stream %test-counting-stream))
  '(unsigned-byte 8))

#+sbcl
(defmethod sb-gray:stream-read-byte ((stream %test-counting-stream))
  (if (>= (%test-counting-stream-count stream) (%test-counting-stream-limit stream))
      :eof
      (progn
        (incf (%test-counting-stream-count stream))
        65)))

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

    ;; 0. Incremental stream reading: reading halts at max-bytes + 1 without consuming the entire stream
    (let ((s (make-instance '%test-counting-stream)))
      (multiple-value-bind (octets line code)
          (read-bounded-octets s :max-bytes 10 :signal-error nil)
        (ok (null octets) "reading oversized stream succeeded")
        (check-equal 2 code "oversized stream did not exit 2")
        (ok (search "--max-bytes" line) "refusal message omits --max-bytes: ~A" line)
        (check-equal 11 (%test-counting-stream-count s)
                     (format nil "stream read was not incremental: read ~D bytes instead of 11"
                             (%test-counting-stream-count s)))))

    (let ((s (make-instance '%test-counting-stream))
          (signaled nil))
      (handler-case
          (read-bounded-octets s :max-bytes 10 :signal-error t)
        (read-bounds-exceeded (c)
          (setf signaled t)
          (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "stream condition bound")
          (check-equal 10 (read-bounds-exceeded-limit c) "stream condition limit")
          (check-equal 11 (read-bounds-exceeded-observed c) "stream condition observed")
          (ok (search "--max-bytes" (unsupported-input-what c))
              "unsupported-input-what describes condition: ~A" (unsupported-input-what c))))
      (ok signaled "read-bounds-exceeded not signaled for stream")
      (check-equal 11 (%test-counting-stream-count s)
                   (format nil "stream read was not incremental when signaling: read ~D bytes instead of 11"
                           (%test-counting-stream-count s))))

    ;; Single-open file reading: incremental bounds enforcement prevents reading beyond bound
    (let ((growth-file (format nil "~A/growth.sexp" dir)))
      (write-temp-bounds-file growth-file "123456789012345")
      (multiple-value-bind (text line code)
          (read-bounded-file growth-file :max-bytes 10 :max-depth 10 :max-nodes 10 :signal-error nil)
        (ok (null text) "read-bounded-file exceeded max-bytes")
        (check-equal 2 code "file overrun did not exit 2")
        (ok (search "--max-bytes" line) "file overrun refusal omits --max-bytes: ~A" line)))

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
         (default-sess (session-start))
         (export-dir (format nil "~A/exp-snap/" dir))
         (snap-dir (format nil "~A/loaded-snap/" dir))
         ;; Five files representing the five session read surfaces:
         (snap-file (write-temp-bounds-file (format nil "~A/snap.sexp" dir)
                                            "((:id \"root\" :type :work-set :parent nil))"))
         (arch-file (write-temp-bounds-file (format nil "~A/archive.sexp" dir)
                                            "((:archive-day \"2026-09-19\" :records ()))"))
         (journal-file (write-temp-bounds-file (format nil "~A/journal.sexp" dir)
                                               "((:kind :node-add :id \"n1\" :type :task))"))
         (cache-file (write-temp-bounds-file (format nil "~A/cache.sexp" dir)
                                             "(:pointer \"p1\" :subject \"s1\" :resolver \"r1\" :fact :holds :stamp \"2026-09-20T00:00:00Z\")"))
         (bundle-file (write-temp-bounds-file (format nil "~A/bundle.sexp" dir)
                                              "(:bundle-id \"b1\" :requests ())")))

    ;; 1. Session start stores bounds and applies defaults when omitted
    (check-equal 5000 (session-max-bytes sess) "session-max-bytes stored")
    (check-equal 12 (session-max-depth sess) "session-max-depth stored")
    (check-equal 300 (session-max-nodes sess) "session-max-nodes stored")

    (check-equal nil (session-max-bytes default-sess)
                 "default session-max-bytes is nil when omitted")
    (check-equal nil (session-max-depth default-sess)
                 "default session-max-depth is nil when omitted")
    (check-equal nil (session-max-nodes default-sess)
                 "default session-max-nodes is nil when omitted")
    (multiple-value-bind (mb md mn) (session-bounds default-sess)
      (check-equal nil mb "session-bounds default mb is nil")
      (check-equal nil md "session-bounds default md is nil")
      (check-equal nil mn "session-bounds default mn is nil"))
    (multiple-value-bind (mb md mn) (session-bounds nil)
      (check-equal nil mb "session-bounds nil mb is nil")
      (check-equal nil md "session-bounds nil md is nil")
      (check-equal nil mn "session-bounds nil mn is nil"))

    ;; Missing bounds under default session refuse guessing with exit 2:
    (multiple-value-bind (text line code) (session-read-file default-sess snap-file)
      (ok (null text) "session-read-file under default session succeeded")
      (check-equal 2 code "session-read-file did not exit 2")
      (ok (search "missing --max-bytes: refusing to guess" line)
          "session-read-file line: ~A" line))

    (let ((signaled nil))
      (handler-case (session-read-file default-sess snap-file :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "missing bound in session-read-file")))
      (ok signaled "session-read-file did not signal missing-read-bounds"))

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
            (read-loaded-snapshot snap-dir :max-bytes 5 :max-depth 10 :max-nodes 100)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-bytes"))

      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir :max-bytes 100000 :max-depth 1 :max-nodes 100)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-depth" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-depth"))

      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir :max-bytes 100000 :max-depth 10 :max-nodes 1)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-nodes" (read-bounds-exceeded-bound c) "snapshot mode bound")))
        (ok signaled "read-loaded-snapshot did not enforce max-nodes"))

      ;; Missing bounds on read-loaded-snapshot refuse guessing:
      (let ((signaled nil))
        (handler-case
            (read-loaded-snapshot snap-dir)
          (missing-read-bounds (c)
            (setf signaled t)
            (check-equal "--max-bytes" (missing-read-bounds-bound c) "read-loaded-snapshot missing bound")))
        (ok signaled "read-loaded-snapshot did not signal missing-read-bounds when bounds omitted"))

      ;; Valid read of loaded snapshot within bounds succeeds:
      (let ((loaded (read-loaded-snapshot snap-dir :max-bytes 100000 :max-depth 10 :max-nodes 100)))
        (ok loaded "read-loaded-snapshot failed within bounds")
        (check-equal 1 (snapshot-query loaded) "snapshot-query answered open count"))

      ;; State-load depth and node boundary enforcement on MANIFEST.sexp:
      (let ((dest (format nil "~A/fail-manifest-depth/" dir)))
        (multiple-value-bind (snap line code)
            (state-load :from export-dir :into dest :max-bytes 100000 :max-depth 1 :max-nodes 100)
          (ok (null snap) "state-load succeeded when MANIFEST.sexp exceeded max-depth")
          (check-equal 2 code "manifest depth overrun did not exit 2")
          (ok (search "--max-depth" line) "refusal did not name --max-depth: ~A" line)
          (ok (search "MANIFEST.sexp" line) "refusal did not name MANIFEST.sexp: ~A" line)
          (ok (null (probe-file dest)) "failed load created destination")))

      (let ((dest (format nil "~A/fail-manifest-nodes/" dir)))
        (multiple-value-bind (snap line code)
            (state-load :from export-dir :into dest :max-bytes 100000 :max-depth 10 :max-nodes 5)
          (ok (null snap) "state-load succeeded when MANIFEST.sexp exceeded max-nodes")
          (check-equal 2 code "manifest node overrun did not exit 2")
          (ok (search "--max-nodes" line) "refusal did not name --max-nodes: ~A" line)
          (ok (search "MANIFEST.sexp" line) "refusal did not name MANIFEST.sexp: ~A" line)
          (ok (null (probe-file dest)) "failed load created destination")))

      ;; State-load depth and node boundary enforcement on reconstructed root:
      (let* ((deep-export-dir (format nil "~A/exp-deep-root/" dir))
             (deep-dest (format nil "~A/dest-deep-root/" dir))
             (deep-bytes "((((((((:id \"deep\" :type :task :parent nil))))))))")
             (manifest-list (list :version "nova-work-state-export-v1"
                                  :kind :captured-state
                                  :id "exp-deep"
                                  :captured (list :revision 0)
                                  :schema (list :id "work-v1" :sha256 (sha256-hex "work-v1"))
                                  :scope (list :open :all :closed-history :none)
                                  :members (list (list :path "state/snapshot.sexp" :kind :state-root
                                                       :bytes (length deep-bytes) :sha256 (sha256-hex deep-bytes)))
                                  :omissions '()))
             (m-bytes (canonical-string manifest-list)))
        (ensure-directories-exist (%state-load-join deep-export-dir "state/"))
        (%state-load-write-string (%state-load-join deep-export-dir "state/snapshot.sexp") deep-bytes)
        (%state-load-write-string (%state-load-join deep-export-dir "MANIFEST.sexp") m-bytes)
        (multiple-value-bind (snap line code)
            (state-load :from deep-export-dir :into deep-dest :max-bytes 100000 :max-depth 5 :max-nodes 100)
          (ok (null snap) "state-load succeeded when state root exceeded max-depth")
          (check-equal 2 code "root depth overrun did not exit 2")
          (ok (search "--max-depth" line) "refusal did not name --max-depth: ~A" line)
          (ok (null (probe-file deep-dest)) "failed load created destination")))

      (let* ((nodes-export-dir (format nil "~A/exp-nodes-root/" dir))
             (nodes-dest (format nil "~A/dest-nodes-root/" dir))
             (nodes-bytes "((:id \"n1\" :type :task :parent nil) (:id \"n2\" :type :task :parent nil) (:id \"n3\" :type :task :parent nil) (:id \"n4\" :type :task :parent nil))")
             (nodes-manifest (list :version "nova-work-state-export-v1"
                                   :kind :captured-state
                                   :id "exp-nodes"
                                   :captured (list :revision 0)
                                   :schema (list :id "work-v1" :sha256 (sha256-hex "work-v1"))
                                   :scope (list :open :all :closed-history :none)
                                   :members (list (list :path "state/snapshot.sexp" :kind :state-root
                                                        :bytes (length nodes-bytes) :sha256 (sha256-hex nodes-bytes)))
                                   :omissions '()))
             (m-bytes (canonical-string nodes-manifest)))
        (ensure-directories-exist (%state-load-join nodes-export-dir "state/"))
        (%state-load-write-string (%state-load-join nodes-export-dir "state/snapshot.sexp") nodes-bytes)
        (%state-load-write-string (%state-load-join nodes-export-dir "MANIFEST.sexp") m-bytes)
        (multiple-value-bind (snap line code)
            (state-load :from nodes-export-dir :into nodes-dest :max-bytes 100000 :max-depth 10 :max-nodes 28)
          (ok (null snap) "state-load succeeded when state root exceeded max-nodes")
          (check-equal 2 code "root node overrun did not exit 2")
          (ok (search "--max-nodes" line) "refusal did not name --max-nodes: ~A" line)
          (ok (null (probe-file nodes-dest)) "failed load created destination"))))

    ;; 5. Direct verification of real production readers under session bounds
    ;; A. Journal readers (open-file-journal and replay-journal):
    (let ((signaled nil))
      (handler-case
          (open-file-journal journal-file :session tight-sess)
        (read-bounds-exceeded (c)
          (setf signaled t)
          (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "open-file-journal bound")))
      (ok signaled "open-file-journal did not signal read-bounds-exceeded under tight session"))

    (let ((signaled nil))
      (handler-case
          (replay-journal journal-file (fresh) :session tight-sess)
        (read-bounds-exceeded (c)
          (setf signaled t)
          (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "replay-journal bound")))
      (ok signaled "replay-journal did not signal read-bounds-exceeded under tight session"))

    (let* ((valid-j-path (format nil "~A/valid.journal" dir))
           (j (open-file-journal valid-j-path :session sess)))
      (ok j "open-file-journal failed under valid session bounds")
      (close-file-journal j)
      (let ((j2 (open-file-journal valid-j-path :session sess)))
        (ok j2 "reopening valid journal under session bounds failed")
        (close-file-journal j2)))

    ;; B. Verification cache reader (read-verification-cache):
    (let ((signaled nil))
      (handler-case
          (read-verification-cache cache-file :session tight-sess)
        (read-bounds-exceeded (c)
          (setf signaled t)
          (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "read-verification-cache bound")))
      (ok signaled "read-verification-cache did not signal read-bounds-exceeded under tight session"))

    (let ((cache (read-verification-cache cache-file :session sess)))
      (ok cache "read-verification-cache failed under valid session bounds")
      (ok (verification-cache-p cache) "read-verification-cache returned valid verification-cache"))

    ;; C. Request bundle readers (read-request-bundle-file and replay-request-bundle):
    (let ((valid-bundle-file (write-temp-bounds-file
                              (format nil "~A/valid-bundle.sexp" dir)
                              "(:request-bundle :base \"base-sha\" :clipped-revision 0 :requests ())")))
      (let ((bundle (read-request-bundle-file valid-bundle-file :session tight-sess :signal-error nil)))
        (ok (null bundle) "read-request-bundle-file under tight session succeeded"))

      (let ((signaled nil))
        (handler-case
            (read-request-bundle-file valid-bundle-file :session tight-sess :signal-error t)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "bundle file bound")))
        (ok signaled "read-request-bundle-file did not signal read-bounds-exceeded under tight session"))

      (let ((bundle (read-request-bundle-file valid-bundle-file :session sess :signal-error nil)))
        (ok bundle "read-request-bundle-file failed under valid session bounds"))

      (let ((signaled nil))
        (handler-case
            (replay-request-bundle valid-bundle-file (fresh) :session tight-sess)
          (read-bounds-exceeded (c)
            (setf signaled t)
            (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "replay-request-bundle bound")))
        (ok signaled "replay-request-bundle did not signal read-bounds-exceeded under tight session")))

    ;; D. Snapshot reader under session bounds (read-loaded-snapshot):
    (let ((signaled nil))
      (handler-case
          (read-loaded-snapshot snap-dir :session tight-sess)
        (read-bounds-exceeded (c)
          (setf signaled t)
          (check-equal "--max-bytes" (read-bounds-exceeded-bound c) "read-loaded-snapshot session bound")))
      (ok signaled "read-loaded-snapshot did not enforce tight session bounds"))

    (let ((loaded (read-loaded-snapshot snap-dir :session sess)))
      (ok loaded "read-loaded-snapshot failed under valid session bounds")
      (check-equal 1 (snapshot-query loaded) "snapshot-query answered open count"))

    ;; E. Missing bounds signaled across entrypoints when bounds omitted:
    ;; 1. session-start with named existing cache file:
    (let ((signaled nil))
      (handler-case
          (session-start :cache cache-file)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "session-start cache all omitted bound")))
      (ok signaled "session-start did not signal missing-read-bounds with all bounds omitted"))

    (let ((signaled nil))
      (handler-case
          (session-start :cache cache-file :max-depth 20 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "session-start cache omitted max-bytes")))
      (ok signaled "session-start did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (session-start :cache cache-file :max-bytes 100000 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "session-start cache omitted max-depth")))
      (ok signaled "session-start did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (session-start :cache cache-file :max-bytes 100000 :max-depth 20)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "session-start cache omitted max-nodes")))
      (ok signaled "session-start did not signal missing-read-bounds when max-nodes omitted"))

    (let ((sess-ok (session-start :cache cache-file :max-bytes 100000 :max-depth 20 :max-nodes 500)))
      (ok sess-ok "session-start failed when all required bounds were supplied")
      (ok (session-verification sess-ok) "session carries verification session"))

    ;; 2. read-verification-cache:
    (let ((signaled nil))
      (handler-case
          (read-verification-cache cache-file)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "read-verification-cache all omitted bound")))
      (ok signaled "read-verification-cache did not signal missing-read-bounds with all bounds omitted"))

    (let ((signaled nil))
      (handler-case
          (read-verification-cache cache-file :max-depth 20 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "read-verification-cache omitted max-bytes")))
      (ok signaled "read-verification-cache did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (read-verification-cache cache-file :max-bytes 100000 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "read-verification-cache omitted max-depth")))
      (ok signaled "read-verification-cache did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (read-verification-cache cache-file :max-bytes 100000 :max-depth 20)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "read-verification-cache omitted max-nodes")))
      (ok signaled "read-verification-cache did not signal missing-read-bounds when max-nodes omitted"))

    (let ((cache-ok (read-verification-cache cache-file :max-bytes 100000 :max-depth 20 :max-nodes 500)))
      (ok cache-ok "read-verification-cache failed when all bounds supplied"))

    ;; 3. open-file-journal:
    (let ((signaled nil))
      (handler-case
          (open-file-journal journal-file)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "open-file-journal all omitted bound")))
      (ok signaled "open-file-journal did not signal missing-read-bounds with all bounds omitted"))

    (let ((signaled nil))
      (handler-case
          (open-file-journal journal-file :max-depth 20 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "open-file-journal omitted max-bytes")))
      (ok signaled "open-file-journal did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (open-file-journal journal-file :max-bytes 100000 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "open-file-journal omitted max-depth")))
      (ok signaled "open-file-journal did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (open-file-journal journal-file :max-bytes 100000 :max-depth 20)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "open-file-journal omitted max-nodes")))
      (ok signaled "open-file-journal did not signal missing-read-bounds when max-nodes omitted"))

    ;; 4. replay-journal:
    (let ((signaled nil))
      (handler-case
          (replay-journal journal-file (fresh))
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "replay-journal all omitted bound")))
      (ok signaled "replay-journal did not signal missing-read-bounds with all bounds omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-journal journal-file (fresh) :max-depth 20 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "replay-journal omitted max-bytes")))
      (ok signaled "replay-journal did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-journal journal-file (fresh) :max-bytes 100000 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "replay-journal omitted max-depth")))
      (ok signaled "replay-journal did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-journal journal-file (fresh) :max-bytes 100000 :max-depth 20)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "replay-journal omitted max-nodes")))
      (ok signaled "replay-journal did not signal missing-read-bounds when max-nodes omitted"))

    ;; 5. read-request-bundle-file:
    (let ((signaled nil))
      (handler-case
          (read-request-bundle-file bundle-file :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "read-request-bundle-file all omitted bound")))
      (ok signaled "read-request-bundle-file did not signal missing-read-bounds with all bounds omitted"))

    (multiple-value-bind (val msg code) (read-request-bundle-file bundle-file :signal-error nil)
      (ok (null val) "read-request-bundle-file with signal-error nil returned val")
      (check-equal 2 code "read-request-bundle-file with signal-error nil exit 2")
      (ok (search "missing --max-bytes" msg) "read-request-bundle-file message names missing bound"))

    (let ((signaled nil))
      (handler-case
          (read-request-bundle-file bundle-file :max-depth 20 :max-nodes 500 :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "read-request-bundle-file omitted max-bytes")))
      (ok signaled "read-request-bundle-file did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (read-request-bundle-file bundle-file :max-bytes 100000 :max-nodes 500 :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "read-request-bundle-file omitted max-depth")))
      (ok signaled "read-request-bundle-file did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (read-request-bundle-file bundle-file :max-bytes 100000 :max-depth 20 :signal-error t)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "read-request-bundle-file omitted max-nodes")))
      (ok signaled "read-request-bundle-file did not signal missing-read-bounds when max-nodes omitted"))

    ;; 6. replay-request-bundle:
    (let ((signaled nil))
      (handler-case
          (replay-request-bundle bundle-file (fresh))
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "replay-request-bundle all omitted bound")))
      (ok signaled "replay-request-bundle did not signal missing-read-bounds with all bounds omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-request-bundle bundle-file (fresh) :max-depth 20 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-bytes" (missing-read-bounds-bound c) "replay-request-bundle omitted max-bytes")))
      (ok signaled "replay-request-bundle did not signal missing-read-bounds when max-bytes omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-request-bundle bundle-file (fresh) :max-bytes 100000 :max-nodes 500)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-depth" (missing-read-bounds-bound c) "replay-request-bundle omitted max-depth")))
      (ok signaled "replay-request-bundle did not signal missing-read-bounds when max-depth omitted"))

    (let ((signaled nil))
      (handler-case
          (replay-request-bundle bundle-file (fresh) :max-bytes 100000 :max-depth 20)
        (missing-read-bounds (c)
          (setf signaled t)
          (check-equal "--max-nodes" (missing-read-bounds-bound c) "replay-request-bundle omitted max-nodes")))
      (ok signaled "replay-request-bundle did not signal missing-read-bounds when max-nodes omitted"))))

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

;;; ------------------------------------------------------------------
;;; E01-F02-04: Common Lisp lexical rules in intake scanner
;;; (SPEC-WORK.md:837-845, :6248)
;;; ------------------------------------------------------------------

(deftest "TestE01F02LexicalIntakeScanner" "docs/SPEC-WORK.md:837-845,6248"
    "expected=shallow-string-parens;depth-after-close-strings;tabs-and-whitespace;comments-ignored;escaped-quotes"
  ;; 1. String of 5 '(' inside a shallow list: '(list "(((((" 1)' must report depth 1 (the list nesting), not 6!
  (multiple-value-bind (depth nodes) (intake-scan "(list \"(((((\" 1)")
    (check-equal 1 depth "string parens must not inflate peak depth")
    (check-equal 3 nodes "node count for '(list \"(((((\" 1)'"))

  ;; 2. Genuine nesting depth after close-paren strings: '("))" (((1))))' true nesting 4 must report 4, not 3!
  (multiple-value-bind (depth nodes) (intake-scan "(\"))\" (((1))))")
    (check-equal 4 depth "close parens inside string must not decrement depth")
    (check-equal 2 nodes "node count for '(\"))\" (((1))))'"))

  ;; 3. Whitespace tokenization: three tab-separated integers '1\t2\t3' reports depth 0, nodes 3 (not 1)
  (let ((tab-text (format nil "1~C2~C3" #\Tab #\Tab)))
    (multiple-value-bind (depth nodes) (intake-scan tab-text)
      (check-equal 0 depth "tab-separated integers depth")
      (check-equal 3 nodes "tab-separated integers nodes")))

  ;; 4. Line comments: parens and tokens in line comments must be ignored
  (let ((comment-text (format nil "(foo ; (bar baz) ~% 1)")))
    (multiple-value-bind (depth nodes) (intake-scan comment-text)
      (check-equal 1 depth "comment parens must not affect depth")
      (check-equal 2 nodes "comment tokens must not be counted as nodes")))

  ;; Line comment terminated by Return (#\Return):
  (let ((comment-cr-text (format nil "(foo ; (bar baz) ~C 1)" #\Return)))
    (multiple-value-bind (depth nodes) (intake-scan comment-cr-text)
      (check-equal 1 depth "comment parens with CR must not affect depth")
      (check-equal 2 nodes "comment tokens with CR must not be counted as nodes")))

  ;; 5. Escaped quotes and backslashes in strings:
  (let ((escaped-text "(list \"hello \\\" (world) \\\"\" 1)"))
    (multiple-value-bind (depth nodes) (intake-scan escaped-text)
      (check-equal 1 depth "escaped quotes in strings must not terminate string")
      (check-equal 3 nodes "node count with escaped quotes in string")))

  ;; 6. All Common Lisp whitespace characters: Space, Tab, Newline, Return, Page
  (let ((ws-text (format nil "a~Cb~Cc~Cd~Ce" #\Space #\Tab #\Newline #\Return #\Page)))
    (multiple-value-bind (depth nodes) (intake-scan ws-text)
      (check-equal 0 depth "whitespace-separated depth")
      (check-equal 5 nodes "all 5 CL whitespace characters correctly delimit tokens"))))
