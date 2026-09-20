;;;; replays-issue-mapping.lisp --- tests proving mapping fidelity,
;;;; comment preservation, label set mapping, generic 128-bit UID, and
;;;; idempotent back-pointers (SPEC-WORK.md:7560-7618, 7750-7766; Issue #2081, #2084).

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; 1. Mapping fidelity: title, body, state, author, correspondence
;;; ------------------------------------------------------------------

(deftest "issue-mapping-fidelity" "docs/SPEC-WORK.md:7560"
    "expected=title-body-state-author-correspondence-mapped-faithfully"
  (let* ((open-issue (make-captured-issue
                      :provider "github"
                      :repo "mas-bandwidth/nova-tools"
                      :number 2081
                      :revision "rev-4c793b55"
                      :title "nova-work E09-F02/F03: Map captured issues"
                      :body "Map captured GitHub issue records to nova-work nodes."
                      :state :open
                      :author "glenn"
                      :labels '("e09" "p1")
                      :comments '()))
         (closed-issue (make-captured-issue
                        :provider "github"
                        :repo "mas-bandwidth/nova-tools"
                        :number 2080
                        :revision "rev-86abcf23"
                        :title "Closed prior work"
                        :body "This issue was closed on GitHub."
                        :state :closed
                        :author "rowan-claude"
                        :labels '("done")
                        :comments '())))
    ;; Test open issue mapping
    (multiple-value-bind (node attrs) (map-captured-issue-to-node open-issue)
      (ok (wnode-p node) "open issue mapped to a wnode")
      (check-equal "nova-work E09-F02/F03: Map captured issues" (wnode-title node)
                   "node title preserved")
      (check-equal :o (wnode-branch node) "open issue maps to branch :o")
      (check-equal :open (wnode-state node) "open issue maps to state :open")
      (check-equal "glenn" (getf attrs :author) "author preserved in generic attributes")
      (check-equal "Map captured GitHub issue records to nova-work nodes." (getf attrs :body)
                   "body preserved in generic attributes")
      (check-equal :open (getf attrs :state) "state preserved in generic attributes")
      (let ((corr (getf attrs :correspondence)))
        (check-equal "github" (getf corr :provider) "correspondence provider preserved")
        (check-equal "mas-bandwidth/nova-tools" (getf corr :repo) "correspondence repo preserved")
        (check-equal 2081 (getf corr :number) "correspondence number preserved")
        (check-equal "rev-4c793b55" (getf corr :revision) "correspondence revision preserved")))

    ;; Test closed issue mapping
    (multiple-value-bind (c-node c-attrs) (map-captured-issue-to-node closed-issue)
      (ok (wnode-p c-node) "closed issue mapped to a wnode")
      (check-equal :c (wnode-branch c-node) "closed issue maps to branch :c")
      (check-equal :settled (wnode-state c-node) "closed issue maps to state :settled")
      (check-equal :closed (getf c-attrs :state) "closed state preserved in generic attributes")
      (check-equal "rowan-claude" (getf c-attrs :author) "closed author preserved"))))

;;; ------------------------------------------------------------------
;;; 2. Comment preservation: multi-comment threads, markdown, timestamps
;;; ------------------------------------------------------------------

(deftest "issue-mapping-comment-preservation" "docs/SPEC-WORK.md:7595"
    "expected=comments-preserved-with-authors-bodies-timestamps-intact"
  (let* ((comments (list
                    (make-captured-comment :id 1 :author "rowan"
                                           :body "First note on the issue."
                                           :created-at "2026-09-20T10:00:00Z")
                    (make-captured-comment :id 2 :author "stella"
                                           :body "Second note: multi-line\nwith `code` and symbols."
                                           :created-at "2026-09-20T10:15:00Z")
                    (make-captured-comment :id 3 :author "johnny"
                                           :body "Third note confirming acceptance."
                                           :created-at "2026-09-20T10:30:00Z")))
         (issue (make-captured-issue
                 :repo "mas-bandwidth/nova-tools"
                 :number 876
                 :title "Issue with 3 comments"
                 :body "Description here."
                 :author "rowan"
                 :comments comments)))
    (multiple-value-bind (node attrs) (map-captured-issue-to-node issue)
      (declare (ignore node))
      (let ((saved (getf attrs :comments)))
        (check-equal 3 (length saved) "three comments preserved")
        (check-equal 1 (getf (first saved) :id) "comment 1 id preserved")
        (check-equal "rowan" (getf (first saved) :author) "comment 1 author preserved")
        (check-equal "First note on the issue." (getf (first saved) :body) "comment 1 body preserved")
        (check-equal "2026-09-20T10:00:00Z" (getf (first saved) :created-at) "comment 1 timestamp preserved")

        (check-equal 2 (getf (second saved) :id) "comment 2 id preserved")
        (check-equal "stella" (getf (second saved) :author) "comment 2 author preserved")
        (check-equal "Second note: multi-line\nwith `code` and symbols." (getf (second saved) :body)
                     "comment 2 multi-line body preserved")

        (check-equal 3 (getf (third saved) :id) "comment 3 id preserved")
        (check-equal "johnny" (getf (third saved) :author) "comment 3 author preserved")))))

;;; ------------------------------------------------------------------
;;; 3. Label set mapping: label sets, empty labels, special characters
;;; ------------------------------------------------------------------

(deftest "issue-mapping-label-set-mapping" "docs/SPEC-WORK.md:7596"
    "expected=label-sets-mapped-and-preserved-exactly"
  (let* ((labels '("bug" "e09" "p1" "area:intake" "needs-review"))
         (issue (make-captured-issue
                 :repo "mas-bandwidth/nova-tools"
                 :number 2081
                 :title "Labeled issue"
                 :labels labels))
         (no-label-issue (make-captured-issue
                          :repo "mas-bandwidth/nova-tools"
                          :number 2082
                          :title "Unlabeled issue"
                          :labels '())))
    (multiple-value-bind (node1 attrs1) (map-captured-issue-to-node issue)
      (declare (ignore node1))
      (check-equal labels (getf attrs1 :labels) "labels preserved in order and content"))
    (multiple-value-bind (node2 attrs2) (map-captured-issue-to-node no-label-issue)
      (declare (ignore node2))
      (check-equal '() (getf attrs2 :labels) "empty labels preserved as empty list"))))

;;; ------------------------------------------------------------------
;;; 4. Generic 128-bit UID (#2084): minting, validity, fast lookup, refusal
;;; ------------------------------------------------------------------

(deftest "issue-mapping-128bit-generic-uid" "Issue #2084"
    "expected=128-bit-hex-generic-untyped-no-collisions-refuses-short-source"
  ;; Valid UID minted by kernel
  (let ((uid (mint-uid)))
    (ok (valid-uid-p uid) "minted UID is 32 lowercase hex characters: ~A" uid)
    (check-equal 32 (length uid) "UID length is exactly 32 hex chars (128 bits)"))

  ;; Statistical test: 1,000 minted UIDs never collide
  (let ((seen (make-hash-table :test #'equal)))
    (dotimes (i 1000)
      (let ((u (mint-uid)))
        (ok (valid-uid-p u) "UID ~A valid" u)
        (ok (null (gethash u seen)) "collision detected on UID ~A at iteration ~D" u i)
        (setf (gethash u seen) t))))

  ;; Short byte source refuses with unsupported-input
  (let ((*journal-identity-byte-source* (lambda (n)
                                          (declare (ignore n))
                                          (make-array 8 :element-type '(unsigned-byte 8)
                                                        :initial-element 0))))
    (handler-case
        (progn (mint-uid)
               (fail "a short byte source was admitted"))
      (unsupported-input (c)
        (ok (search "16 bytes" (format nil "~A" c))
            "short source refuses naming 16 bytes: ~A" c))))

  ;; Failing byte source refuses with unsupported-input
  (let ((*journal-identity-byte-source* (lambda (n)
                                          (declare (ignore n))
                                          (error 'unsupported-input :what "CSPRNG unavailable"))))
    (handler-case
        (progn (mint-uid)
               (fail "a failing byte source was admitted"))
      (unsupported-input (c)
        (ok (search "CSPRNG unavailable" (format nil "~A" c))
            "failing source refuses naming reason: ~A" c))))

  ;; Node receives UID and allows fast O(1) lookup in wstate
  (let* ((issue (make-captured-issue :number 100 :title "Fast lookup test"))
         (fixed-uid "0123456789abcdef0123456789abcdef"))
    (multiple-value-bind (node attrs)
        (map-captured-issue-to-node issue :uid fixed-uid :id "fast/test")
      (check-equal fixed-uid (wnode-uid node) "node carries assigned UID")
      (check-equal fixed-uid (getf attrs :uid) "attributes carry assigned UID")
      (let ((state (make-seed-state (list (list :id "fast/test" :type :task
                                                :uid fixed-uid :state :open)))))
        (let ((found (node-by-uid state fixed-uid)))
          (ok (not (null found)) "node found by UID in O(1) index")
          (check-equal "fast/test" (wnode-id found) "found node matches id"))))))

;;; ------------------------------------------------------------------
;;; 5. Two-sided back-pointer receipt format
;;; ------------------------------------------------------------------

(deftest "issue-mapping-back-pointer-format" "Issue #2081"
    "expected=exact-format-nova-work-uid-hex-store-prefix"
  (let ((uid "a1b2c3d4e5f60718293a4b5c6d7e8f90")
        (store "6a28bf78"))
    ;; Exact format with store
    (let ((formatted (format-back-pointer uid :store store)))
      (check-equal "nova-work: uid=a1b2c3d4e5f60718293a4b5c6d7e8f90 store=6a28bf78"
                   formatted "exact back-pointer string with store matches"))
    ;; Exact format without store
    (let ((formatted-no-store (format-back-pointer uid)))
      (check-equal "nova-work: uid=a1b2c3d4e5f60718293a4b5c6d7e8f90"
                   formatted-no-store "exact back-pointer string without store matches"))
    ;; Parse back-pointer
    (multiple-value-bind (parsed-uid parsed-store)
        (parse-back-pointer "nova-work: uid=a1b2c3d4e5f60718293a4b5c6d7e8f90 store=6a28bf78")
      (check-equal uid parsed-uid "parsed UID matches")
      (check-equal store parsed-store "parsed store matches"))))

;;; ------------------------------------------------------------------
;;; 6. Idempotent application: second pass detects back-pointer and writes nothing
;;; ------------------------------------------------------------------

(deftest "issue-mapping-back-pointer-idempotency" "Issue #2081"
    "expected=first-pass-generates-receipt;second-pass-detects-pointer-writes-nothing"
  (let* ((uid "fedcba9876543210fedcba9876543210")
         (store "store-alpha")
         (issue (make-captured-issue
                 :repo "mas-bandwidth/nova-tools"
                 :number 2081
                 :title "Proving run back-pointer"
                 :body "Issue body without back-pointer."
                 :comments '())))
    ;; First pass: no back-pointer present -> receipt generated
    (multiple-value-bind (receipt status)
        (generate-back-pointer-receipt issue uid :store store)
      (ok (not (null receipt)) "first pass generates receipt")
      (check-equal :generated status "status is :generated")
      (check-equal t (back-pointer-receipt-applied receipt) "receipt applied is true")
      (check-equal "mas-bandwidth/nova-tools#2081" (back-pointer-receipt-issue-ref receipt)
                   "issue ref matches")
      (check-equal uid (back-pointer-receipt-uid receipt) "receipt UID matches")
      (check-equal "nova-work: uid=fedcba9876543210fedcba9876543210 store=store-alpha"
                   (back-pointer-receipt-back-pointer receipt)
                   "receipt back-pointer text matches")

      ;; Simulate applying outbound write: comment appended to issue
      (let ((updated-issue (apply-back-pointer-receipt issue receipt)))
        (check-equal 1 (length (captured-issue-comments updated-issue))
                     "updated issue has one comment added")
        (ok (issue-has-back-pointer-p updated-issue :uid uid)
            "updated issue detects back-pointer")

        ;; Second pass: detects existing back-pointer and writes NOTHING!
        (multiple-value-bind (receipt2 status2)
            (generate-back-pointer-receipt updated-issue uid :store store)
          (check-equal nil receipt2 "second pass generates no receipt (writes nothing)")
          (check-equal :already-present status2 "second pass status is :already-present"))))))

;;; ------------------------------------------------------------------
;;; 7. Batch import and update deduplication
;;; ------------------------------------------------------------------

(deftest "issue-mapping-batch-deduplication" "docs/SPEC-WORK.md:7759"
    "expected=batch-import-creates-n-nodes;re-import-updates-zero-duplicates"
  (let* ((i1 (make-captured-issue :number 1 :title "Issue 1" :state :open))
         (i2 (make-captured-issue :number 2 :title "Issue 2" :state :open))
         (i3 (make-captured-issue :number 3 :title "Issue 3" :state :closed))
         (batch (list i1 i2 i3)))
    ;; Initial batch import
    (multiple-value-bind (new-nodes updated table cp)
        (import-issue-batch batch)
      (check-equal 3 (length new-nodes) "three new nodes created on first import")
      (check-equal 0 (length updated) "zero updated nodes on first import")
      (check-equal "github:mas-bandwidth/nova-tools#3" cp "checkpoint recorded")

      (let ((uid1 (wnode-uid (first new-nodes)))
            (uid2 (wnode-uid (second new-nodes)))
            (uid3 (wnode-uid (third new-nodes))))
        (ok (valid-uid-p uid1) "uid1 valid")
        (ok (valid-uid-p uid2) "uid2 valid")
        (ok (valid-uid-p uid3) "uid3 valid")
        (ok (not (equal uid1 uid2)) "uid1 != uid2")
        (ok (not (equal uid2 uid3)) "uid2 != uid3")

        ;; Second import pass with updated title on issue 1
        (let* ((i1-updated (make-captured-issue :number 1 :title "Issue 1 Renamed" :state :open))
               (second-batch (list i1-updated i2 i3)))
          (multiple-value-bind (new-nodes-2 updated-2 table-2 cp-2)
              (import-issue-batch second-batch :mapping-table table)
            (declare (ignore table-2 cp-2))
            (check-equal 0 (length new-nodes-2) "zero new nodes on re-import (no duplicates!)")
            (check-equal 3 (length updated-2) "three nodes updated on re-import")
            (check-equal "Issue 1 Renamed" (wnode-title (first updated-2))
                         "updated title reflected on re-imported node")
            (check-equal uid1 (wnode-uid (first updated-2))
                         "re-imported node retains its original UID (no reissuing!)")))))))
