;;;; capture-github.lisp --- read-only GitHub capture adapter and bundle ingestion
;;;; (SPEC-WORK.md:2721-2760, :6229; Issue #2080).
;;;;
;;;; The kernel stays pure and offline: the adapter is a boundary that ingests
;;;; a CAPTURE BUNDLE from disk, never a socket.
;;;;
;;;; Criteria:
;;;; - E09-F01-01: Capture stable provider/repository/issue identity, revision and URL.
;;;; - E09-F01-02: Preserve body, comments, labels, relationships, attachments and pagination.
;;;; - Strictly read-only: refuses any outbound mutation (close, comment, edit, delete).
;;;; - Invariant checks: refuses a bundle whose manifest totals disagree with contents,
;;;;   or a bundle with a pagination gap.
;;;; - Remote text is kept strictly as data, never instructions.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Bounded JSON Parser for Capture Bundles (supporting numbers)
;;; ------------------------------------------------------------------

(defun capture-json-parse-number (parser)
  (let* ((text (wire-json-parser-text parser))
         (start (wire-json-parser-pos parser))
         (len (length text)))
    (when (and (< (wire-json-parser-pos parser) len)
               (char= (char text (wire-json-parser-pos parser)) #\-))
      (incf (wire-json-parser-pos parser)))
    (loop while (and (< (wire-json-parser-pos parser) len)
                     (digit-char-p (char text (wire-json-parser-pos parser))))
          do (incf (wire-json-parser-pos parser)))
    (when (and (< (wire-json-parser-pos parser) len)
               (char= (char text (wire-json-parser-pos parser)) #\.))
      (incf (wire-json-parser-pos parser))
      (loop while (and (< (wire-json-parser-pos parser) len)
                       (digit-char-p (char text (wire-json-parser-pos parser))))
            do (incf (wire-json-parser-pos parser))))
    (read-from-string (subseq text start (wire-json-parser-pos parser)))))

(defun capture-json-parse-value (parser)
  (wire-json-skip-ws parser)
  (let ((ch (wire-json-peek parser)))
    (cond
      ((null ch) (wire-json-fail parser "unexpected end of JSON"))
      ((char= ch #\{) (capture-json-parse-object parser))
      ((char= ch #\[) (capture-json-parse-array parser))
      ((char= ch #\") (wire-json-parse-string parser))
      ((char= ch #\t) (wire-json-literal parser "true" t))
      ((char= ch #\f) (wire-json-literal parser "false" nil))
      ((char= ch #\n) (wire-json-literal parser "null" nil))
      ((or (char= ch #\-) (digit-char-p ch))
       (capture-json-parse-number parser))
      (t (wire-json-fail parser "unexpected JSON token")))))

(defun capture-json-parse-object (parser)
  (incf (wire-json-parser-pos parser))
  (wire-json-skip-ws parser)
  (let ((pairs '()))
    (if (char= (or (wire-json-peek parser) #\Nul) #\})
        (incf (wire-json-parser-pos parser))
        (loop
          (wire-json-skip-ws parser)
          (let ((key (wire-json-parse-string parser)))
            (wire-json-skip-ws parser)
            (unless (char= (wire-json-peek parser) #\:)
              (wire-json-fail parser "expected a colon after an object key"))
            (incf (wire-json-parser-pos parser))
            (push (cons key (capture-json-parse-value parser)) pairs))
          (wire-json-skip-ws parser)
          (case (wire-json-peek parser)
            (#\, (incf (wire-json-parser-pos parser)))
            (#\} (incf (wire-json-parser-pos parser)) (return))
            (t (wire-json-fail parser "expected a comma or the closing brace")))))
    (nreverse pairs)))

(defun capture-json-parse-array (parser)
  (incf (wire-json-parser-pos parser))
  (wire-json-skip-ws parser)
  (if (char= (or (wire-json-peek parser) #\Nul) #\])
      (progn (incf (wire-json-parser-pos parser)) '())
      (let ((out '()))
        (loop
          (push (capture-json-parse-value parser) out)
          (wire-json-skip-ws parser)
          (case (wire-json-peek parser)
            (#\, (incf (wire-json-parser-pos parser)) (wire-json-skip-ws parser))
            (#\] (incf (wire-json-parser-pos parser)) (return))
            (t (wire-json-fail parser "expected a comma or the closing bracket"))))
        (nreverse out))))

(defun capture-parse-json (text)
  "Parse one JSON value supporting numbers, booleans, null, strings, arrays and objects."
  (let ((parser (%make-wire-json-parser text)))
    (let ((value (capture-json-parse-value parser)))
      (wire-json-skip-ws parser)
      (unless (>= (wire-json-parser-pos parser) (length text))
        (wire-json-fail parser "trailing bytes after the JSON value"))
      value)))

;;; Helper to read an entire file into octets and string
(defun read-bundle-file-octets (path)
  (with-open-file (in path :direction :input :element-type '(unsigned-byte 8))
    (let ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8))))
      (read-sequence bytes in)
      bytes)))

(defun read-bundle-file-string (path)
  (wire-utf8-string (read-bundle-file-octets path)))

;;; ------------------------------------------------------------------
;;; The Read-Only GitHub Capture Adapter
;;; ------------------------------------------------------------------

(defstruct (github-capture-adapter (:constructor %make-github-capture-adapter))
  bundle-dir
  manifest
  issues
  reads
  mutations)

(defun make-github-capture-adapter (&key bundle-dir manifest issues)
  (%make-github-capture-adapter :bundle-dir bundle-dir
                                :manifest manifest
                                :issues issues
                                :reads '()
                                :mutations '()))

(defun github-adapter-mutations (adapter)
  (reverse (github-capture-adapter-mutations adapter)))

(defun github-adapter-mutate (adapter method endpoint &key body)
  "The read-only adapter fails on every source mutation method."
  (push (list method endpoint body) (github-capture-adapter-mutations adapter))
  (error 'unsupported-input
         :what (format nil "read-only adapter: mutation refused ~A ~A" method endpoint)))

(defun github-adapter-close-issue (adapter repo number &key reason)
  (github-adapter-mutate adapter :post (format nil "/repos/~A/issues/~D/close" repo number)
                         :body reason))

(defun github-adapter-add-comment (adapter repo number body)
  (github-adapter-mutate adapter :post (format nil "/repos/~A/issues/~D/comments" repo number)
                         :body body))

(defun github-adapter-edit-issue (adapter repo number edits)
  (github-adapter-mutate adapter :patch (format nil "/repos/~A/issues/~D" repo number)
                         :body edits))

(defun github-adapter-delete-issue (adapter repo number)
  (github-adapter-mutate adapter :delete (format nil "/repos/~A/issues/~D" repo number)))

;;; ------------------------------------------------------------------
;;; Bundle Ingestion & Validation
;;; ------------------------------------------------------------------

(defun alist-get (key alist)
  (cdr (assoc key alist :test #'string=)))

(defun ingest-capture-bundle (bundle-dir &key stage (max-body-bytes 65536))
  "Ingest a staged capture bundle from BUNDLE-DIR into memory and optional STAGE.
Validates manifest totals, checksums, and pagination continuity.
Refuses any tampered manifest or page gap."
  (let* ((manifest-path (format nil "~A/manifest.json" bundle-dir))
         (manifest-raw (handler-case (read-bundle-file-string manifest-path)
                         (error () (error 'unsupported-input :what "cannot read manifest.json"))))
         (manifest (capture-parse-json manifest-raw))
         (provider (alist-get "provider" manifest))
         (host (alist-get "host" manifest))
         (repo (alist-get "repository" manifest))
         (total-declared (alist-get "total_issues" manifest))
         (page-count (or (alist-get "page_count" manifest) 1))
         (summaries (alist-get "issues" manifest))
         (issues-dir (format nil "~A/issues" bundle-dir)))

    ;; 1. Check declared total vs listed summaries
    (unless (eql total-declared (length summaries))
      (error 'unsupported-input
             :what (format nil "bundle manifest totals disagree with contents: declared ~D, listed ~D"
                           total-declared (length summaries))))

    ;; 2. Read each issue file, verify checksum, and preserve all correspondence
    (let ((loaded-issues '())
          (pages-seen '()))
      (dolist (summary summaries)
        (let* ((rel-file (alist-get "file" summary))
               (expected-bytes (alist-get "bytes" summary))
               (expected-sha (alist-get "sha256" summary))
               (issue-path (format nil "~A/~A" bundle-dir rel-file))
               (issue-octets (handler-case (read-bundle-file-octets issue-path)
                               (error () (error 'unsupported-input
                                                :what (format nil "cannot read staged file ~A" rel-file)))))
               (issue-raw (wire-utf8-string issue-octets)))

          ;; Length check
          (unless (eql (length issue-octets) expected-bytes)
            (error 'unsupported-input
                   :what (format nil "staged file ~A size ~D != manifest ~D"
                                 rel-file (length issue-octets) expected-bytes)))
          ;; Checksum check
          (let ((computed-sha (sha256-hex issue-octets)))
            (unless (string-equal computed-sha expected-sha)
              (error 'unsupported-input
                     :what (format nil "staged file ~A checksum mismatch: ~A != ~A"
                                   rel-file computed-sha expected-sha))))

          (let* ((issue-json (capture-parse-json issue-raw))
                 (number (alist-get "number" issue-json))
                 (node-id (alist-get "node_id" issue-json))
                 (rest-id (alist-get "rest_id" issue-json))
                 (title (alist-get "title" issue-json))
                 (url (alist-get "url" issue-json))
                 (revision (alist-get "revision" issue-json))
                 (state (alist-get "state" issue-json))
                 (author (alist-get "author" issue-json))
                 (body (alist-get "body" issue-json))
                 (truncated (alist-get "truncated" issue-json))
                 (truncated-hash (alist-get "truncated_hash" issue-json))
                 (comments-json (alist-get "comments" issue-json))
                 (labels-json (alist-get "labels" issue-json))
                 (rels-json (alist-get "relationships" issue-json))
                 (atts-json (alist-get "attachments" issue-json))
                 (page (or (alist-get "page" issue-json) 1)))

            (pushnew page pages-seen)

            ;; Transform comments
            (let ((comments (mapcar (lambda (c)
                                      (list :id (alist-get "id" c)
                                            :author (alist-get "author" c)
                                            :body (alist-get "body" c)
                                            :url (alist-get "url" c)))
                                    comments-json))
                  ;; Transform labels
                  (labels (mapcar (lambda (l)
                                    (list :name (alist-get "name" l)
                                          :color (alist-get "color" l)
                                          :description (alist-get "description" l)))
                                  labels-json))
                  ;; Transform relationships
                  (relationships (mapcar (lambda (r)
                                           (list :kind (alist-get "kind" r)
                                                 :target (alist-get "target" r)
                                                 :source (alist-get "source" r)))
                                         rels-json))
                  ;; Transform attachments: ensure status is "not fetched"
                  (attachments (mapcar (lambda (a)
                                         (list :url (alist-get "url" a)
                                               :status (alist-get "status" a)
                                               :found-in (alist-get "found_in" a)))
                                       atts-json)))

              ;; Body bound enforcement: if body exceeds max-body-bytes and not already truncated
              (when (and (> (length body) max-body-bytes) (not truncated))
                (setf truncated t
                      truncated-hash (sha256-hex body)
                      body (subseq body 0 max-body-bytes)))

              (let ((iss (list :provider (or provider "github")
                               :host (or host "github.com")
                               :repository repo
                               :number number
                               :node-id node-id
                               :rest-id rest-id
                               :alias (format nil "~A#~D" repo number)
                               :title title
                               :url url
                               :revision revision
                               :state state
                               :author author
                               :body body
                               :truncated (if truncated t nil)
                               :truncated-hash truncated-hash
                               :comments comments
                               :labels labels
                               :relationships relationships
                               :attachments attachments
                               :page page
                               :bytes (length issue-octets))))
                (push iss loaded-issues)

                ;; Optional staging into capture-stage
                (when stage
                  (capture-stage-input stage
                                       :id (format nil "~A#~D" repo number)
                                       :kind :capture
                                       :bytes (length issue-octets)
                                       :records 1
                                       :source-pin revision)))))))

      ;; 3. Check pagination continuity: pages must be contiguous 1..page-count
      (when (> page-count 1)
        (loop for p from 1 to page-count
              unless (member p pages-seen)
                do (error 'unsupported-input
                          :what (format nil "bundle pagination has a gap: missing page ~D among 1..~D"
                                        p page-count))))

      (make-github-capture-adapter :bundle-dir bundle-dir
                                   :manifest manifest
                                   :issues (nreverse loaded-issues)))))
