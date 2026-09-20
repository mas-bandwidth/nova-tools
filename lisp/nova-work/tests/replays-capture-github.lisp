;;;; replays-capture-github.lisp --- E09-F01 read-only GitHub capture replays.
;;;;
;;;; E09-F01-01: Capture stable provider/repository/issue identity, revision and URL.
;;;; E09-F01-02: Preserve body, comments, labels, relationships, attachments and pagination.
;;;;
;;;; Verifies:
;;;; - capture-stable-identity         SPEC-WORK.md:6229
;;;; - capture-preserve-correspondence SPEC-WORK.md:6229

(in-package #:nova-work/tests)

(defvar *capture-test-counter* 0)

(defun make-test-capture-dir (name)
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames (format nil "nova-work-capture-test/~A-~D-~D/"
                                       name (get-universal-time)
                                       (incf *capture-test-counter*))
                               base)))
    (ensure-directories-exist dir)
    (string-right-trim "/" (namestring (truename dir)))))

(defun write-test-capture-file (path text)
  (ensure-directories-exist path)
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create
                            :element-type 'character :external-format :utf-8)
    (write-string text out))
  path)

;;; ------------------------------------------------------------------
;;; capture-stable-identity                   SPEC-WORK.md:6229
;;; ------------------------------------------------------------------

(deftest "capture-stable-identity" "docs/SPEC-WORK.md:6229"
    "expected=stable-provider-repo-id-revision-url;canonical-keys-preserved"
  (let* ((fixture-dir (namestring (merge-pathnames "testdata/capture-fixture"
                                                   (asdf:system-source-directory :nova-work))))
         (adapter (ingest-capture-bundle fixture-dir))
         (issues (github-capture-adapter-issues adapter)))
    (ok (github-capture-adapter-p adapter) "adapter is a github-capture-adapter")
    (check-equal 3 (length issues) "three issues loaded from fixture")

    ;; Verify issue 2080
    (let ((iss (find 2080 issues :key (lambda (i) (getf i :number)))))
      (ok iss "issue 2080 was found")
      (check-string= "github" (getf iss :provider) "provider is github")
      (check-string= "github.com" (getf iss :host) "host is github.com")
      (check-string= "mas-bandwidth/nova-tools" (getf iss :repository) "repo matches")
      (check-equal 2080 (getf iss :number) "number matches")
      (check-string= "I_kwDOTxuLoc8AAAABSPCpDw" (getf iss :node-id) "node-id matches")
      (check-equal 5518698767 (getf iss :rest-id) "rest-id matches")
      (check-string= "mas-bandwidth/nova-tools#2080" (getf iss :alias) "alias is owner/repo#number")
      (check-string= "2026-09-20T15:24:03Z" (getf iss :revision) "revision is updated_at timestamp")
      (check-string= "https://github.com/mas-bandwidth/nova-tools/issues/2080" (getf iss :url) "url matches")
      (check-string= "open" (getf iss :state) "state is open")
      (check-string= "rowan-claude" (getf iss :author) "author matches"))

    ;; Verify issue 2084
    (let ((iss (find 2084 issues :key (lambda (i) (getf i :number)))))
      (ok iss "issue 2084 was found")
      (check-string= "I_kwDOTxuLoc8AAAABSOzS1w" (getf iss :node-id) "node-id matches")
      (check-equal 5518600000 (getf iss :rest-id) "rest-id matches")
      (check-string= "mas-bandwidth/nova-tools#2084" (getf iss :alias) "alias matches")
      (check-string= "2026-09-20T15:00:00Z" (getf iss :revision) "revision matches")
      (check-string= "https://github.com/mas-bandwidth/nova-tools/issues/2084" (getf iss :url) "url matches"))

    ;; Verify issue 2085
    (let ((iss (find 2085 issues :key (lambda (i) (getf i :number)))))
      (ok iss "issue 2085 was found")
      (check-string= "I_kwDOTxuLoc8AAAABSPEgTQ" (getf iss :node-id) "node-id matches")
      (check-equal 5518729293 (getf iss :rest-id) "rest-id matches")
      (check-string= "mas-bandwidth/nova-tools#2085" (getf iss :alias) "alias matches")
      (check-string= "2026-09-20T15:30:10Z" (getf iss :revision) "revision matches"))

    ;; Bounded staging integration: capture-stage accepts the bundle
    (let ((stage (make-capture-stage :limits '(:staged-inputs 100 :staged-bytes 1000000 :retained-results 100))))
      (ingest-capture-bundle fixture-dir :stage stage)
      (check-equal 3 (capture-input-count stage) "three inputs staged")
      (ok (> (capture-stage-bytes stage) 0) "staged bytes is non-zero"))))

;;; ------------------------------------------------------------------
;;; capture-preserve-correspondence           SPEC-WORK.md:6229
;;; ------------------------------------------------------------------

(deftest "capture-preserve-correspondence" "docs/SPEC-WORK.md:6229"
    "expected=body-comments-labels-relationships-attachments-pagination-preserved;mutations-refused;tamper-refused;body-bound-truncated-with-hash"
  (let* ((fixture-dir (namestring (merge-pathnames "testdata/capture-fixture"
                                                   (asdf:system-source-directory :nova-work))))
         (adapter (ingest-capture-bundle fixture-dir))
         (issues (github-capture-adapter-issues adapter)))

    ;; 1. Preserve body as data
    (let ((iss2080 (find 2080 issues :key (lambda (i) (getf i :number)))))
      (ok (stringp (getf iss2080 :body)) "body is string")
      (ok (search "Glenn 2026-09-20:" (getf iss2080 :body)) "body content preserved")
      (check-equal nil (getf iss2080 :truncated) "issue 2080 is not truncated under default bound")
      ;; 2. Preserve relationships
      (let ((rels (getf iss2080 :relationships)))
        (check-equal 4 (length rels) "four relationships on 2080")
        (check-string= "cross-referenced" (getf (first rels) :kind) "cross-referenced relationship kind")
        (check-string= "https://github.com/mas-bandwidth/nova-tools/issues/2082" (getf (first rels) :target) "target URL")
        (check-string= "https://github.com/mas-bandwidth/nova-tools/issues/2080" (getf (first rels) :source) "source URL")))

    ;; 3. Preserve comments, labels, attachments, pagination on 2084
    (let ((iss2084 (find 2084 issues :key (lambda (i) (getf i :number)))))
      ;; Comments
      (let ((comments (getf iss2084 :comments)))
        (check-equal 1 (length comments) "one comment on 2084")
        (check-equal 101 (getf (first comments) :id) "comment id 101")
        (check-string= "emma" (getf (first comments) :author) "comment author emma")
        (ok (search "Audit comment" (getf (first comments) :body)) "comment body preserved")
        (check-string= "https://github.com/mas-bandwidth/nova-tools/issues/2084#issuecomment-101"
                       (getf (first comments) :url) "comment url preserved"))
      ;; Labels
      (let ((labels (getf iss2084 :labels)))
        (check-equal 1 (length labels) "one label on 2084")
        (check-string= "efficiency" (getf (first labels) :name) "label name")
        (check-string= "0e8a16" (getf (first labels) :color) "label color")
        (check-string= "cuts coordination or swarm token spend" (getf (first labels) :description) "label description"))
      ;; Attachments
      (let ((atts (getf iss2084 :attachments)))
        (check-equal 2 (length atts) "two attachments on 2084")
        (check-string= "not fetched" (getf (first atts) :status) "attachment 1 status is not fetched")
        (check-string= "body" (getf (first atts) :found-in) "attachment 1 found in body")
        (check-string= "https://github.com/user-attachments/assets/diagram-1234.png" (getf (first atts) :url) "attachment 1 url")
        (check-string= "not fetched" (getf (second atts) :status) "attachment 2 status is not fetched")
        (check-string= "comment:101" (getf (second atts) :found-in) "attachment 2 found in comment"))
      ;; Pagination
      (check-equal 2 (getf iss2084 :page) "issue 2084 was on page 2"))

    ;; 4. Strictly read-only: refuse outbound mutations
    (ok (handler-case (progn (github-adapter-close-issue adapter "mas-bandwidth/nova-tools" 2080) nil)
          (unsupported-input () t))
        "close-issue must be refused with unsupported-input")
    (ok (handler-case (progn (github-adapter-add-comment adapter "mas-bandwidth/nova-tools" 2080 "mutation") nil)
          (unsupported-input () t))
        "add-comment must be refused with unsupported-input")
    (ok (handler-case (progn (github-adapter-edit-issue adapter "mas-bandwidth/nova-tools" 2080 '((:title . "new"))) nil)
          (unsupported-input () t))
        "edit-issue must be refused with unsupported-input")
    (ok (handler-case (progn (github-adapter-delete-issue adapter "mas-bandwidth/nova-tools" 2080) nil)
          (unsupported-input () t))
        "delete-issue must be refused with unsupported-input")
    (check-equal 4 (length (github-adapter-mutations adapter)) "four mutations recorded in adapter log")

    ;; 5. Byte bound enforcement (oversized body)
    (let* ((bounded-adapter (ingest-capture-bundle fixture-dir :max-body-bytes 100))
           (bounded-issues (github-capture-adapter-issues bounded-adapter))
           (iss2080 (find 2080 bounded-issues :key (lambda (i) (getf i :number)))))
      (check-equal t (getf iss2080 :truncated) "oversized body marked truncated")
      (check-equal 100 (length (getf iss2080 :body)) "oversized body clamped to bound")
      (check-equal 64 (length (getf iss2080 :truncated-hash)) "truncated-hash is 64 hex sha256"))

    ;; 6. Manifest totals mismatch refusal
    (let ((tamper-dir (make-test-capture-dir "manifest-mismatch")))
      (write-test-capture-file (format nil "~A/manifest.json" tamper-dir)
                               "{\"provider\":\"github\",\"repository\":\"mas-bandwidth/nova-tools\",\"total_issues\":5,\"issues\":[]}")
      (ok (handler-case (progn (ingest-capture-bundle tamper-dir) nil)
            (unsupported-input () t))
          "manifest totals mismatch must be refused"))

    ;; 7. Staged file checksum mismatch refusal
    (let ((tamper-dir (make-test-capture-dir "checksum-mismatch")))
      (ensure-directories-exist (format nil "~A/issues/" tamper-dir))
      (write-test-capture-file (format nil "~A/issues/issue-1.json" tamper-dir)
                               "{\"number\":1,\"title\":\"test\"}")
      (write-test-capture-file (format nil "~A/manifest.json" tamper-dir)
                               (format nil "{\"provider\":\"github\",\"repository\":\"mas-bandwidth/nova-tools\",\"total_issues\":1,\"issues\":[{\"number\":1,\"file\":\"issues/issue-1.json\",\"bytes\":~D,\"sha256\":\"badc0ffee\"}]}"
                                       (length "{\"number\":1,\"title\":\"test\"}")))
      (ok (handler-case (progn (ingest-capture-bundle tamper-dir) nil)
            (unsupported-input () t))
          "tampered checksum must be refused"))

    ;; 8. Pagination gap refusal
    (let ((tamper-dir (make-test-capture-dir "page-gap")))
      (ensure-directories-exist (format nil "~A/issues/" tamper-dir))
      (let* ((iss-content "{\"number\":1,\"title\":\"test\",\"page\":1}")
             (iss-bytes (length iss-content))
             (iss-sha (sha256-hex iss-content)))
        (write-test-capture-file (format nil "~A/issues/issue-1.json" tamper-dir) iss-content)
        (write-test-capture-file (format nil "~A/manifest.json" tamper-dir)
                                 (format nil "{\"provider\":\"github\",\"repository\":\"mas-bandwidth/nova-tools\",\"page_count\":2,\"total_issues\":1,\"issues\":[{\"number\":1,\"file\":\"issues/issue-1.json\",\"bytes\":~D,\"sha256\":\"~A\"}]}"
                                         iss-bytes iss-sha))
        (ok (handler-case (progn (ingest-capture-bundle tamper-dir) nil)
              (unsupported-input () t))
            "page gap must be refused")))))
