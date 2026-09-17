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
;;; `node move` (:3025-3068, :5360, :5364, :5367, :5370, :5379).
;;; ------------------------------------------------------------------

(defun node-parent (state id) (wnode-parent (%node-or-nil state id)))

(defun node-children (state id) (copy-list (wnode-children (%node-or-nil state id))))

;;; The three derived context reads the move guards use. Each walks the
;;; containment chain with %NODE-QUIET, so a move makes no whole-set scan and
;;; moves no counter a read would have measured (:3038-3040, :3064-3066).

(defun effective-repo (state id)
  "The repository of ID's root work-set, or NIL. A work set's repository is
immutable, so a move can never change it (:2994, :3045)."
  (let ((cur id))
    (loop for n = (%node-quiet state cur)
          while (and n (wnode-parent n))
          do (setf cur (wnode-parent n))
          finally (let ((n (%node-quiet state cur)))
                    (return (and n (wnode-repo n)))))))

(defun effective-responsible (state id)
  "The nearest `:coordinator` on ID's containment chain (self first), or NIL.
The coordination tree is the spec's responsible context (:3059-3060)."
  (let ((cur id) (found nil))
    (loop while (and cur (not found))
          do (let ((n (%node-quiet state cur)))
               (if (null n)
                   (return)
                   (let ((c (wnode-coordinator n)))
                     (if c (setf found c) (setf cur (wnode-parent n)))))))
    found))

(defun effective-private-p (state id)
  "True when ID's own marker or any containment ancestor's is set (:3051)."
  (let ((cur id) (private nil))
    (loop while cur
          do (let ((n (%node-quiet state cur)))
               (cond ((null n) (return))
                     ((member (wnode-private n) '(t :true)) (setf private t) (return))
                     (t (setf cur (wnode-parent n))))))
    private))

(defun %reparent-node (state id old new position)
  "Move ID's containment edge from OLD to NEW, inserting at POSITION in NEW's
listing (NIL appends, which is the forward move's `at the destination's end`).
Moves the open count along the two ancestor chains only, so a common ancestor
takes the net zero (:3033-3040)."
  (let* ((node (%node-quiet state id))
         (old-p (%node-quiet state old))
         (new-p (%node-quiet state new))
         (open (eq :o (wnode-branch node)))
         (delta (wnode-open-count node)))
    (setf (wnode-children old-p)
          (remove id (wnode-children old-p) :test #'equal))
    (let ((kids (copy-list (wnode-children new-p))))
      (setf (wnode-children new-p)
            (if position
                (let ((at (min position (length kids))))
                  (append (subseq kids 0 at) (list id) (nthcdr at kids)))
                (append kids (list id)))))
    (setf (wnode-parent node) new)
    (when (wnode-required node)
      (decf (wnode-required-count old-p))
      (when open (decf (wnode-required-open old-p)))
      (incf (wnode-required-count new-p))
      (when open (incf (wnode-required-open new-p))))
    (loop for p = old then (let ((n (%node-quiet state p)))
                             (and n (wnode-parent n)))
          while p
          do (decf (wnode-open-count (%node-quiet state p)) delta))
    (loop for p = new then (let ((n (%node-quiet state p)))
                             (and n (wnode-parent n)))
          while p
          do (incf (wnode-open-count (%node-quiet state p)) delta))
    nil))

(defun %move-payload (id from under reason)
  "The request's own payload, compared so a retry replays and a changed payload
under the same id refuses (:5364, :3030)."
  (list :verb :node-move :id id :from from :under under :reason reason))

(defun node-move (kernel &key id from under reason request stamp by clock generation-owner)
  "Reparent ID from FROM to UNDER (:3027-3070). `--from` is checked and never
inferred; the refusals are named and write nothing; `--from` equal to `--under`
is the no-effect receipt changed=0. A retry of the same request answers its
original line, and a different payload under the id refuses (:5364, :5367)."
  (declare (ignore stamp by clock generation-owner))
  (let* ((state (kernel-state kernel))
         (payload (%move-payload id from under reason)))
    ;; Dedup first: a lost reply's retry must replay even if the tree has since
    ;; moved on, and a changed payload under the id is a conflict, not a move.
    (when request
      (let ((prior (gethash request (kernel-applied kernel))))
        (when prior
          (if (and (eq (getf prior :verb) :node-move)
                   (equal (getf prior :payload) payload))
              (return-from node-move (values t (getf prior :line) 0))
              (return-from node-move
                (values nil
                        (format nil "NODE FAIL node=~A: reused with a different payload" id)
                        1))))))
    (flet ((refuse (why)
             (values nil (format nil "NODE FAIL node=~A: ~A" id why) 1)))
      (let ((node (%node-quiet state id)))
        (unless node (return-from node-move (refuse "no such node")))
        ;; A root container and a repository root are `not movable`.
        (when (or (null (wnode-parent node)) (wnode-repo node))
          (return-from node-move (refuse "not movable")))
        ;; A roadmap as either parent is `roadmap operation required`.
        (when (or (eq (wnode-type node) :roadmap)
                  (and under (let ((u (%node-quiet state under)))
                               (and u (eq :roadmap (wnode-type u))))))
          (return-from node-move (refuse "roadmap operation required")))
        (unless (equal (wnode-parent node) from)
          (return-from node-move (refuse "parent conflict")))
        (let ((new-p (%node-quiet state under)))
          (unless new-p
            (return-from node-move (refuse (format nil "no such parent ~A" under))))
          ;; A destination at or below the node is a cycle.
          (let ((cur under))
            (loop while cur
                  do (when (equal cur id) (return-from node-move (refuse "cycle")))
                     (let ((n (%node-quiet state cur)))
                       (setf cur (and n (wnode-parent n))))))
          ;; A destination under another repository is `repository change`.
          (let ((r-old (effective-repo state id))
                (r-new (effective-repo state under)))
            (when (and r-old r-new (not (equal r-old r-new)))
              (return-from node-move (refuse "repository change"))))
          ;; The context guards over the reached subtree, checked before any
          ;; write so a refusal leaves both parents unchanged.
          (let* ((subtree (%subtree-ids state id))
                 (priv-new (effective-private-p state under))
                 (resp-old (effective-responsible state id))
                 (resp-new (effective-responsible state under)))
            (when (and (some (lambda (x) (effective-private-p state x)) subtree)
                       (not priv-new))
              (return-from node-move (refuse "privacy reduction")))
            (when (not (equal resp-old resp-new))
              (let ((affected (remove-if-not
                               (lambda (x)
                                 (or (wnode-holder (%node-quiet state x))
                                     (live-attempt-ids kernel x)))
                               subtree)))
                (when affected
                  (return-from node-move
                    (refuse (format nil "active context change ~{~A~^,~}" affected))))))))
        ;; `--from` equal to `--under` and true: the no-effect receipt.
        (when (equal from under)
          (let ((line (format nil "NODE OK id=~A request=~A change=move changed=0 from=~A under=~A"
                              id request from under)))
            (when request
              (setf (gethash request (kernel-applied kernel))
                    (list :verb :node-move :payload payload :line line
                          :node id :from from :under under :changed 0)))
            (return-from node-move (values t line 0))))
        ;; The accepted move: append at the destination's end and remember the
        ;; preimage position and both parents' listings for the undo guard.
        (let* ((old-p (%node-quiet state from))
               (new-p (%node-quiet state under))
               (position (position id (wnode-children old-p) :test #'equal))
               (before-from (copy-list (wnode-children old-p)))
               (before-under (copy-list (wnode-children new-p))))
          (%reparent-node state id from under nil)
          (let ((line (format nil "NODE OK id=~A request=~A change=move changed=1 from=~A under=~A"
                              id request from under)))
            (when request
              (setf (gethash request (kernel-applied kernel))
                    (list :verb :node-move :payload payload :line line
                          :node id :from from :under under :changed 1
                          :position position
                          :before (list :from-children before-from
                                        :under-children before-under)
                          :after (list :from-children (copy-list (wnode-children (%node-quiet state from)))
                                       :under-children (copy-list (wnode-children (%node-quiet state under)))))))
            (values t line 0)))))))

(defun node-move-undo (kernel entry request)
  "The compensating `node move` back to ENTRY's `:from` in one envelope,
restoring the preimage order and required sets. Refuses conflict when either
parent's ordered children is no longer this event's postimage, and never
guesses an insertion point (:2865, :3063-3067, :5379)."
  (let* ((state (kernel-state kernel))
         (id (getf entry :node))
         (from (getf entry :from))
         (under (getf entry :under))
         (position (getf entry :position))
         (after (getf entry :after))
         (node (%node-quiet state id))
         (f (%node-quiet state from))
         (u (%node-quiet state under)))
    (unless (and node f u)
      (return-from node-move-undo
        (values nil (format nil "UNDO FAIL request=~A: no such node or parent" request) 1)))
    (unless (and (equal (wnode-children f) (getf after :from-children))
                 (equal (wnode-children u) (getf after :under-children)))
      (return-from node-move-undo
        (values nil
                (format nil "UNDO FAIL request=~A: conflict; a parent's children moved" request)
                1)))
    (%reparent-node state id under from position)
    (values t
            (format nil "UNDO OK request=~A of=~A node=~A change=move" request request id)
            0)))
