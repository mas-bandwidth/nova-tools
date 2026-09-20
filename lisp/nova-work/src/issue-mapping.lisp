;;;; issue-mapping.lisp --- mapping captured issues to nova-work tree
;;;; and generating back-pointers (SPEC-WORK.md:7560-7618, 7750-7766; Issue #2081, #2084).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Captured issue and comment data types
;;; ------------------------------------------------------------------

(defstruct (captured-comment (:constructor make-captured-comment
                                (&key id author body created-at)))
  "One comment on a captured issue thread."
  id
  author
  body
  created-at)

(defstruct (captured-issue (:constructor make-captured-issue
                              (&key (provider "github") (repo "mas-bandwidth/nova-tools")
                                    number (revision "rev-1")
                                    title body (state :open) author
                                    (labels '()) (comments '()) url)))
  "One captured GitHub issue record."
  (provider "github")
  (repo "mas-bandwidth/nova-tools")
  number     ; e.g. 2081
  (revision "rev-1")
  title
  body
  (state :open)  ; :open or :closed (or "open"/"closed")
  author
  (labels '())   ; list of strings
  (comments '()) ; list of captured-comment or plists
  url)

(defun %issue-get (issue key &optional default)
  "Extract KEY from ISSUE, whether ISSUE is a struct, a plist, or an alist."
  (cond
    ((captured-issue-p issue)
     (let ((val (case key
                  (:provider (captured-issue-provider issue))
                  (:repo (captured-issue-repo issue))
                  (:number (captured-issue-number issue))
                  (:revision (captured-issue-revision issue))
                  (:title (captured-issue-title issue))
                  (:body (captured-issue-body issue))
                  (:state (captured-issue-state issue))
                  (:author (captured-issue-author issue))
                  (:labels (captured-issue-labels issue))
                  (:comments (captured-issue-comments issue))
                  (:url (captured-issue-url issue))
                  (t default))))
       (if (null val) default val)))
    ((listp issue)
     (let ((val (if (keywordp (first issue))
                    (getf issue key default)
                    (let ((entry (assoc key issue :test #'equal)))
                      (if entry (cdr entry) default)))))
       (if (null val) default val)))
    (t default)))

(defun %normalize-state (state)
  "Normalize state to :open or :closed."
  (cond
    ((member state '(:open "open" :o "OPEN") :test #'equal) :open)
    ((member state '(:closed "closed" :c "CLOSED" :settled) :test #'equal) :closed)
    (t :open)))

(defun %normalize-comment (c)
  "Normalize a comment to (:id ... :author ... :body ... :created-at ...)."
  (if (captured-comment-p c)
      (list :id (captured-comment-id c)
            :author (captured-comment-author c)
            :body (captured-comment-body c)
            :created-at (captured-comment-created-at c))
      (let ((author (or (getf c :author) (getf c :user)
                        (cdr (assoc :author c)) (cdr (assoc "author" c :test #'equal))))
            (body (or (getf c :body) (cdr (assoc :body c)) (cdr (assoc "body" c :test #'equal)) ""))
            (id (or (getf c :id) (cdr (assoc :id c)) (cdr (assoc "id" c :test #'equal))))
            (created-at (or (getf c :created-at) (getf c :created_at)
                            (cdr (assoc :created-at c)) (cdr (assoc "created_at" c :test #'equal)))))
        (list :id id :author author :body body :created-at created-at))))

(defun %normalize-label (l)
  "Normalize a label to a string."
  (cond
    ((stringp l) l)
    ((symbolp l) (string-downcase (symbol-name l)))
    ((and (listp l) (getf l :name)) (getf l :name))
    ((and (listp l) (assoc "name" l :test #'equal)) (cdr (assoc "name" l :test #'equal)))
    (t (format nil "~A" l))))

;;; ------------------------------------------------------------------
;;; Issue -> Node mapping
;;; ------------------------------------------------------------------

(defun map-captured-issue-to-node (issue &key uid id type parent coordinator store)
  "Map one captured issue record to a nova-work node specification with generic
node attributes and a 128-bit generic UID (Issue #2081, #2084).
Maps: title, body, state (open/closed), labels, comments, author to generic
node attributes. Assigns or mints a 128-bit generic UID."
  (declare (ignore store))
  (let* ((raw-title (%issue-get issue :title ""))
         (raw-body (%issue-get issue :body ""))
         (raw-state (%normalize-state (%issue-get issue :state :open)))
         (raw-author (%issue-get issue :author "unknown"))
         (raw-labels (mapcar #'%normalize-label (%issue-get issue :labels '())))
         (raw-comments (mapcar #'%normalize-comment (%issue-get issue :comments '())))
         (provider (%issue-get issue :provider "github"))
         (repo (%issue-get issue :repo "mas-bandwidth/nova-tools"))
         (number (%issue-get issue :number 0))
         (revision (%issue-get issue :revision "1"))
         (url (or (%issue-get issue :url)
                  (format nil "https://~A.com/~A/issues/~D" provider repo number)))
         (assigned-uid (or uid (mint-uid)))
         (node-id (or id (format nil "imported/~A/~D" repo number)))
         (node-type (or type :task))
         (branch (if (eq raw-state :open) :o :c))
         (node-state (if (eq raw-state :open) :open :settled))
         (correspondence (list :provider provider
                               :repo repo
                               :number number
                               :revision revision
                               :url url))
         (generic-attributes (list :uid assigned-uid
                                   :title raw-title
                                   :body raw-body
                                   :state raw-state
                                   :author raw-author
                                   :labels (copy-list raw-labels)
                                   :comments (copy-tree raw-comments)
                                   :correspondence correspondence)))
    (unless (valid-uid-p assigned-uid)
      (error 'unsupported-input
             :what (format nil "invalid UID ~S; must be 32 lowercase hex characters" assigned-uid)))
    (values (make-wnode :id node-id
                        :type node-type
                        :parent parent
                        :coordinator coordinator
                        :children '()
                        :required t
                        :required-count 0
                        :required-open 0
                        :state node-state
                        :branch branch
                        :open-count (if (eq branch :o) 1 0)
                        :title raw-title
                        :links (list url)
                        :uid assigned-uid
                        :attributes generic-attributes)
            generic-attributes)))

;;; ------------------------------------------------------------------
;;; Back-pointer formatting and detection
;;; ------------------------------------------------------------------

(defun format-back-pointer (uid &key store)
  "Format the exact back-pointer string for GitHub issues linking to the
nova-work UID (Issue #2081):
  `nova-work: uid=<hex> store=<journal-identity-prefix>`"
  (if (and store (plusp (length (string-trim " " store))))
      (format nil "nova-work: uid=~A store=~A" uid store)
      (format nil "nova-work: uid=~A" uid)))

(defun parse-back-pointer (text)
  "Parse a back-pointer comment from TEXT, answering (values uid store) or NIL."
  (when (stringp text)
    (let* ((prefix "nova-work: uid=")
           (pos (search prefix text)))
      (when pos
        (let* ((rest-str (subseq text (+ pos (length prefix))))
               (uid-end (position-if (lambda (c)
                                       (or (char= c #\Space) (char= c #\Newline)
                                           (char= c #\Return) (char= c #\Tab)))
                                     rest-str))
               (uid (if uid-end (subseq rest-str 0 uid-end) rest-str))
               (store nil))
          (let ((store-marker "store="))
            (let ((store-pos (search store-marker rest-str)))
              (when store-pos
                (let* ((store-rest (subseq rest-str (+ store-pos (length store-marker))))
                       (store-end (position-if (lambda (c)
                                                 (or (char= c #\Space) (char= c #\Newline)
                                                     (char= c #\Return) (char= c #\Tab)))
                                               store-rest)))
                  (setf store (if store-end (subseq store-rest 0 store-end) store-rest))))))
          (values uid store))))))

(defun issue-has-back-pointer-p (issue &key uid)
  "Check whether ISSUE already carries a back-pointer to UID (or any back-pointer if UID is nil).
Inspects all comments and the issue body."
  (let ((comments (%issue-get issue :comments '()))
        (expected-marker (if uid
                             (format nil "nova-work: uid=~A" uid)
                             "nova-work: uid=")))
    (or (some (lambda (c)
                (let ((body (if (captured-comment-p c)
                                (captured-comment-body c)
                                (or (getf c :body) (cdr (assoc :body c)) ""))))
                  (and (stringp body) (search expected-marker body))))
              comments)
        (let ((body (%issue-get issue :body "")))
          (and (stringp body) (search expected-marker body))))))

;;; ------------------------------------------------------------------
;;; Back-pointer receipt generator
;;; ------------------------------------------------------------------

(defstruct (back-pointer-receipt (:constructor make-back-pointer-receipt
                                    (&key status action issue-ref uid
                                          back-pointer applied reason)))
  "A two-sided back-pointer receipt for the proving run (Issue #2081)."
  status        ; :pending, :confirmed, :skipped
  action        ; "issue.back-pointer"
  issue-ref     ; "owner/repo#number"
  uid           ; 128-bit hex
  back-pointer  ; exact back-pointer string
  applied       ; t or nil
  reason)       ; string if skipped or failed

(defun generate-back-pointer-receipt (issue uid &key store (status :confirmed))
  "Generate a two-sided back-pointer receipt for the proving run (Issue #2081).
Format: `nova-work: uid=<hex> store=<journal-identity-prefix>`.
Idempotent: if ISSUE already contains the back-pointer, detects it and writes
nothing, answering (values nil :already-present)."
  (let ((issue-ref (format nil "~A#~D"
                           (%issue-get issue :repo "mas-bandwidth/nova-tools")
                           (%issue-get issue :number 0))))
    (if (issue-has-back-pointer-p issue :uid uid)
        ;; Second pass detects existing back-pointer and writes nothing!
        (values nil :already-present)
        (let ((bp-str (format-back-pointer uid :store store)))
          (values (make-back-pointer-receipt
                   :status status
                   :action "issue.back-pointer"
                   :issue-ref issue-ref
                   :uid uid
                   :back-pointer bp-str
                   :applied t
                   :reason nil)
                  :generated)))))

(defun apply-back-pointer-receipt (issue receipt &key (author "nova-work[bot]"))
  "Apply RECEIPT to ISSUE by appending the back-pointer comment, simulating
the proving run's outbound comment creation. If receipt is NIL, returns ISSUE unchanged."
  (if (and receipt (back-pointer-receipt-applied receipt))
      (let* ((new-comment (list :id (1+ (length (%issue-get issue :comments '())))
                                :author author
                                :body (back-pointer-receipt-back-pointer receipt)
                                :created-at "2026-09-20T12:00:00Z"))
             (current-comments (%issue-get issue :comments '())))
        (if (captured-issue-p issue)
            (let ((new-issue (copy-structure issue)))
              (setf (captured-issue-comments new-issue)
                    (append current-comments (list new-comment)))
              new-issue)
            (let ((new-issue (copy-tree issue)))
              (setf (getf new-issue :comments)
                    (append current-comments (list new-comment)))
              new-issue)))
      issue))

;;; ------------------------------------------------------------------
;;; Batch import and deduplication
;;; ------------------------------------------------------------------

(defun import-issue-batch (issues &key state mapping-table checkpoint)
  "Import a batch of captured issues into STATE and MAPPING-TABLE with checkpoints (Issue #2081).
Deduplicates by correspondence key (:provider :repo :number):
- New issue: mints 128-bit generic UID, creates node, adds to state and mapping table.
- Existing issue: updates node attributes without duplicating node or reissuing UID."
  (let ((table (or mapping-table (make-hash-table :test #'equal)))
        (new-nodes '())
        (updated-nodes '())
        (last-checkpoint checkpoint))
    (dolist (issue issues)
      (let* ((provider (%issue-get issue :provider "github"))
             (repo (%issue-get issue :repo "mas-bandwidth/nova-tools"))
             (number (%issue-get issue :number 0))
             (corr-key (format nil "~A:~A#~D" provider repo number))
             (existing (gethash corr-key table)))
        (if existing
            ;; Update existing node -- never duplicate, never reissue UID!
            (let* ((uid (getf existing :uid))
                   (node-id (getf existing :node-id)))
              (multiple-value-bind (node attrs)
                  (map-captured-issue-to-node issue :uid uid :id node-id)
                (when state
                  (let ((existing-node (%node-quiet state node-id)))
                    (when existing-node
                      (setf (wnode-title existing-node) (wnode-title node)
                            (wnode-state existing-node) (wnode-state node)
                            (wnode-branch existing-node) (wnode-branch node)
                            (wnode-attributes existing-node) attrs))))
                (push node updated-nodes)))
            ;; New issue -- map and assign new 128-bit generic UID
            (multiple-value-bind (node attrs)
                (map-captured-issue-to-node issue)
              (let ((uid (wnode-uid node))
                    (node-id (wnode-id node)))
                (setf (gethash corr-key table)
                      (list :uid uid :node-id node-id :correspondence (getf attrs :correspondence)))
                (when state
                  (setf (gethash node-id (wstate-nodes state)) node)
                  (setf (gethash uid (wstate-uid-index state)) node)
                  (setf (wstate-order state) (append (wstate-order state) (list node-id)))
                  (if (eq (wnode-branch node) :o)
                      (incf (wstate-root-open state))
                      (incf (wstate-closed state))))
                (push node new-nodes))))
        (setf last-checkpoint corr-key)))
    (values (nreverse new-nodes)
            (nreverse updated-nodes)
            table
            last-checkpoint)))
