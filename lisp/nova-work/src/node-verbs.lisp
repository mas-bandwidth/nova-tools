;;;; node-verbs.lisp --- the node verbs five replays of nova-tools #362 name.
;;;;
;;;; `node edit` and its undo (SPEC-WORK.md:3001-3023), `node add` with the
;;;; `--repo` held only at the root (:2846, :5341), `roadmap create` as the one
;;;; creator of a roadmap node and its view (:3070-3082, :5344), and `node
;;;; move` (:3025-3068, :5360). Each is the smallest surface the paragraph
;;;; promises; the CLI, the session and the wire are still outside this slice.
;;;;
;;;; The link path is deliberately inert: no verb here fetches a URL. Editing a
;;;; link, storing it and rendering it touch the string only (:5357), which is
;;;; what `edit-never-fetches-a-link` asserts.

(in-package #:nova-work)

(defparameter *meta-fields* '(:title :category :links :private :version)
  "The five fields `node edit` owns, in the spec's `:structure` order
(SPEC-WORK.md:3005).")

(defun %node-or-nil (state id)
  (%node-quiet state id))

;;; ------------------------------------------------------------------
;;; `node edit` and its undo (SPEC-WORK.md:3001-3023, :5355, :5357).
;;; ------------------------------------------------------------------

(defun node-metadata (state id)
  "The five-field metadata map, in order. The caller's copy is its own."
  (let ((node (%node-or-nil state id)))
    (unless node (error 'unsupported-input :what (format nil "no such node ~A" id)))
    (list :title (wnode-title node)
          :category (wnode-category node)
          :links (copy-list (wnode-links node))
          :private (wnode-private node)
          :version (wnode-version node))))

(defun node-edit-log (state id)
  "The append-only edit events for ID, newest first."
  (let ((node (%node-or-nil state id)))
    (remove-if-not (lambda (e) (eq :edit (getf e :op)))
                   (wnode-meta-log node))))

(defun node-edit (kernel id &key changes reason request stamp)
  "One tagged patch over the five metadata fields. Answers (values OK-P LINE
EXIT-CODE). A malformed patch names the field and writes nothing; a private
node's refusal prints `private=1` and never the value (:3017, :5357)."
  (declare (ignore reason stamp))
  (let ((node (%node-or-nil (kernel-state kernel) id)))
    (unless node
      (return-from node-edit
        (values nil (format nil "NODE FAIL node=~A: no such node" id) 1)))
    (let ((private (and (wnode-private node) (not (absentp (wnode-private node)))
                        (not (eq (wnode-private node) :false))))
          (before (list :title (wnode-title node)
                        :category (wnode-category node)
                        :links (copy-list (wnode-links node))
                        :private (wnode-private node)
                        :version (wnode-version node))))
      (flet ((refuse (what)
               (values nil
                       (format nil "NODE FAIL node=~A: ~A~@[ private=1~]"
                               id what private)
                       1)))
        (loop for (key value) on changes by #'cddr
              do (case key
                   (:title
                    (unless (or (eq value :clear) (stringp value))
                      (return-from node-edit (refuse "bad patch title")))
                    (setf (wnode-title node) (if (eq value :clear) nil value)))
                   (:category
                    (unless (or (eq value :clear) (stringp value))
                      (return-from node-edit (refuse "bad patch category")))
                    (setf (wnode-category node) (if (eq value :clear) nil value)))
                   (:version
                    (unless (or (eq value :clear) (stringp value))
                      (return-from node-edit (refuse "bad patch version")))
                    (setf (wnode-version node) (if (eq value :clear) nil value)))
                   (:private
                    (unless (or (eq value :clear) (eq value t) (null value))
                      (return-from node-edit (refuse "bad patch private")))
                    (setf (wnode-private node) (if (eq value :clear) nil value)))
                   (:links
                    (cond ((eq value :clear)
                           (setf (wnode-links node) nil))
                          ((and (listp value)
                                (every (lambda (u)
                                         (and (stringp u) (null (find #\Nul u))))
                                       value))
                           (setf (wnode-links node) (copy-list value)))
                          (t
                           (return-from node-edit (refuse "bad link")))))
                   (otherwise
                    (return-from node-edit
                      (refuse (format nil "bad patch ~A"
                                      (string-downcase (symbol-name key))))))))
        (let* ((after (list :title (wnode-title node)
                            :category (wnode-category node)
                            :links (copy-list (wnode-links node))
                            :private (wnode-private node)
                            :version (wnode-version node)))
               (changed (loop for field in *meta-fields*
                              count (not (equal (getf before field)
                                                (getf after field))))))
          (push (list :op :edit :request request :before before :after after)
                (wnode-meta-log node))
          (values t
                  (format nil "NODE OK id=~A request=~A change=edit changed=~D"
                          id request changed)
                  0))))))

(defun node-undo (kernel id &key undo-of request reason stamp)
  "The compensating edit restoring the named edit's `:before`. Admitted only
while the node's current metadata still equals that event's postimage; an
intervening edit is a conflict, never overwritten (:3019-3020, :5355)."
  (declare (ignore reason stamp))
  (let ((node (%node-or-nil (kernel-state kernel) id)))
    (unless node
      (return-from node-undo
        (values nil (format nil "NODE FAIL node=~A: no such node" id) 1)))
    (let ((entry (find undo-of (wnode-meta-log node)
                       :key (lambda (e) (getf e :request))
                       :test #'equal)))
      (cond ((null entry)
             (values nil
                     (format nil "NODE FAIL node=~A: no such edit ~A" id undo-of)
                     1))
            ((not (eq entry (car (wnode-meta-log node))))
             (values nil
                     (format nil "NODE FAIL node=~A: conflict; later work stands"
                             id)
                     1))
            (t
             (let ((before (getf entry :before)))
               (setf (wnode-title node) (getf before :title)
                     (wnode-category node) (getf before :category)
                     (wnode-links node) (copy-list (getf before :links))
                     (wnode-private node) (getf before :private)
                     (wnode-version node) (getf before :version)))
             (push (list :op :undo :request request :undo-of undo-of)
                   (wnode-meta-log node))
             (values t
                     (format nil "NODE OK id=~A request=~A change=undo" id request)
                     0))))))

;;; ------------------------------------------------------------------
;;; Links and rendering: no request is ever made (:5357).
;;; ------------------------------------------------------------------

(defvar *link-fetch-count* 0
  "A counting endpoint's tally. It moves only if `fetch-link` is called, and
no verb in this file calls it: editing, storing and rendering a link read the
string alone.")

(defun fetch-link (url)
  "The only path that would touch the network, kept so the test can prove it is
never taken. Editing and rendering never call it (SPEC-WORK.md:5357)."
  (incf *link-fetch-count*)
  (format nil "fetched ~A" url))

(defun render-node (kernel id)
  "Render one node's links as text. A private node leaves the public render and
prints only `private=1` (:3016-3017). No link is resolved."
  (let ((node (%node-or-nil (kernel-state kernel) id)))
    (unless node
      (error 'unsupported-input :what (format nil "no such node ~A" id)))
    (if (and (wnode-private node) (not (absentp (wnode-private node)))
             (not (eq (wnode-private node) :false)))
        "private=1"
        (with-output-to-string (s)
          (when (wnode-title node) (format s "~A " (wnode-title node)))
          (format s "~{~A~^ ~}" (wnode-links node))))))

;;; ------------------------------------------------------------------
;;; `node add`, and `--repo` held only at the root (:2846, :5341).
;;; ------------------------------------------------------------------

(defun node-repo (state id)
  (wnode-repo (%node-or-nil state id)))

(defun repo-holder (state repo)
  "The id already holding REPO, or NIL. Repo uniqueness is a property of the
collection, so this reads the collection rather than a per-node counter."
  (loop for id in (wstate-order state)
        for node = (gethash id (wstate-nodes state))
        when (equal repo (wnode-repo node)) return id))

(defun %install-open-node (state node)
  "Insert an already-constructed open node and move only its own counters along
its containment ancestors, exactly as the seed does."
  (setf (gethash (wnode-id node) (wstate-nodes state)) node)
  (setf (wstate-order state) (append (wstate-order state) (list (wnode-id node))))
  (let ((parent (and (wnode-parent node)
                     (%node-quiet state (wnode-parent node)))))
    (when parent
      (setf (wnode-children parent)
            (append (wnode-children parent) (list (wnode-id node))))
      (when (wnode-required node)
        (incf (wnode-required-count parent))
        (incf (wnode-required-open parent)))))
  ;; The node is born open, so no closed counter moves.
  (let ((cur (wnode-id node)))
    (loop while cur
          do (let ((n (%node-quiet state cur)))
               (unless n (return))
               (incf (wnode-open-count n))
               (setf cur (wnode-parent n)))))
  (incf (wstate-root-open state))
  (when (member (wnode-type node) '(:task :bug))
    (incf (wstate-leaf-open state)))
  (incf (wstate-issue-open state) (length (wnode-links node)))
  state)

(defun node-add (kernel &key id type parent repo title)
  "Add one node. `--repo` is admitted only with `--under-root` and a work-set
and only once per collection; a roadmap is refused, naming its one creator."
  (let ((state (kernel-state kernel)))
    (when (eq type :roadmap)
      (return-from node-add
        (values nil
                (format nil "NODE FAIL node=~A: roadmap create" id)
                2)))
    (when (and repo parent)
      (return-from node-add
        (values nil
                (format nil "NODE FAIL node=~A: repo outside root" id)
                1)))
    (when repo
      (let ((holder (repo-holder state repo)))
        (when holder
          (return-from node-add
            (values nil
                    (format nil "NODE FAIL node=~A: repo held by ~A" id holder)
                    1)))))
    (when (and parent (null (%node-quiet state parent)))
      (return-from node-add
        (values nil
                (format nil "NODE FAIL node=~A: no such parent ~A" id parent)
                1)))
    (when (%node-quiet state id)
      (return-from node-add
        (values nil
                (format nil "NODE FAIL node=~A: rule 1: duplicate id" id)
                2)))
    (let ((node (make-wnode :id id :type type :parent parent :children '()
                            :required t :required-count 0 :required-open 0
                            :state :unknown :branch :o :open-count 0 :links nil
                            :title title :category nil :private nil :version nil
                            :repo repo :view nil :meta-log '()
                            :settles 0 :revived "-")))
      (%install-open-node state node)
      (values t
              (format nil "NODE OK id=~A request=- change=add repo=~A"
                      id (or repo "-"))
              0))))

;;; ------------------------------------------------------------------
;;; `roadmap create`, the one creator of node and view (:3070-3082, :5344).
;;; ------------------------------------------------------------------

(defun node-type (state id) (wnode-type (%node-or-nil state id)))

(defun node-view (state id) (wnode-view (%node-or-nil state id)))

(defun roadmap-create (kernel &key id parent title axes members reason request stamp)
  "Write one roadmap node and its initial view in one call. Everything is
validated before either is installed, so a refusal writes neither (:5344)."
  (declare (ignore reason stamp))
  (let ((state (kernel-state kernel)))
    (when (and parent (null (%node-quiet state parent)))
      (return-from roadmap-create
        (values nil
                (format nil "ROADMAP FAIL node=~A: no such parent ~A" id parent)
                1)))
    (when (%node-quiet state id)
      (return-from roadmap-create
        (values nil
                (format nil "ROADMAP FAIL node=~A: rule 1: duplicate id" id)
                1)))
    (let ((node (make-wnode :id id :type :roadmap :parent parent :children '()
                            :required t :required-count 0 :required-open 0
                            :state :unknown :branch :o :open-count 0 :links nil
                            :title title :category nil :private nil :version nil
                            :repo nil :view nil :meta-log '()
                            :settles 0 :revived "-")))
      (setf (wnode-view node)
            (list :members (or members '())
                  :axes (or axes '())
                  :permitted-roots '()
                  :projections '()))
      (%install-open-node state node)
      (values t
              (format nil "ROADMAP OK id=~A request=~A change=create" id request)
              0))))

;;; ------------------------------------------------------------------
;;; `node move` (:3025-3068, :5360).
;;; ------------------------------------------------------------------

(defun node-parent (state id) (wnode-parent (%node-or-nil state id)))

(defun node-children (state id) (copy-list (wnode-children (%node-or-nil state id))))

(defun node-move (kernel &key id from under reason request stamp)
  "Reparent ID from FROM to UNDER. `--from` is checked, not inferred; the
subtree's open count moves along the two ancestor chains only, so a common
ancestor takes the net zero and no whole-set scan happens (:3030-3040)."
  (declare (ignore reason stamp))
  (let* ((state (kernel-state kernel))
         (node (%node-or-nil state id)))
    (unless node
      (return-from node-move
        (values nil (format nil "NODE FAIL node=~A: no such node" id) 1)))
    (let ((old (wnode-parent node)))
      (unless (equal old from)
        (return-from node-move
          (values nil (format nil "NODE FAIL node=~A: parent conflict" id) 1)))
      (unless (and under (%node-quiet state under))
        (return-from node-move
          (values nil (format nil "NODE FAIL node=~A: no such parent ~A" id under) 1)))
      ;; A destination at or below the node is a cycle.
      (let ((cur under))
        (loop while cur
              do (when (equal cur id)
                   (return-from node-move
                     (values nil (format nil "NODE FAIL node=~A: cycle" id) 1)))
                 (let ((n (%node-quiet state cur)))
                   (setf cur (and n (wnode-parent n))))))
      (when (equal from under)
        (return-from node-move
          (values t
                  (format nil "NODE OK id=~A request=~A change=move changed=0 from=~A under=~A"
                          id request from under)
                  0)))
      (let ((old-p (%node-quiet state old))
            (new-p (%node-quiet state under))
            (open (eq :o (wnode-branch node)))
            (delta (wnode-open-count node)))
        (setf (wnode-children old-p)
              (remove id (wnode-children old-p) :test #'equal))
        (setf (wnode-children new-p)
              (append (wnode-children new-p) (list id)))
        (setf (wnode-parent node) under)
        (when (wnode-required node)
          (decf (wnode-required-count old-p))
          (when open (decf (wnode-required-open old-p)))
          (incf (wnode-required-count new-p))
          (when open (incf (wnode-required-open new-p))))
        (loop for p = old then (let ((n (%node-quiet state p)))
                                 (and n (wnode-parent n)))
              while p
              do (decf (wnode-open-count (%node-quiet state p)) delta))
        (loop for p = under then (let ((n (%node-quiet state p)))
                                   (and n (wnode-parent n)))
              while p
              do (incf (wnode-open-count (%node-quiet state p)) delta)))
      (values t
              (format nil "NODE OK id=~A request=~A change=move changed=1 from=~A under=~A"
                      id request from under)
              0))))
