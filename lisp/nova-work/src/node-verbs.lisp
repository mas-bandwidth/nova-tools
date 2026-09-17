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

(defparameter *roadmap-row-kinds* '(:feature :epic :work-set)
  "The `:row-kind` a roadmap may declare (SPEC-WORK.md:2324, :3076).")

(defparameter *roadmap-aggregations* '(:required-members :all-members :leaves)
  "The `:aggregation` policies (SPEC-WORK.md:2324, :3076).")

(defparameter *roadmap-completion-policies* '(:all-required-features)
  "The one completion policy the grammar admits (SPEC-WORK.md:2324).")

(defun node-type (state id) (wnode-type (%node-or-nil state id)))

(defun node-view (state id) (wnode-view (%node-or-nil state id)))

(defun %roadmap-axis-ids-p (axes)
  "AXES is an ordered list of distinct non-empty axis id strings."
  (and (listp axes)
       (every (lambda (a) (and (stringp a) (plusp (length a)))) axes)
       (= (length axes) (length (remove-duplicates axes :test #'string=)))))

(defun roadmap-create (kernel &key id parent title (row-kind :feature)
                                    (aggregation :required-members)
                                    (completion-policy :all-required-features)
                                    axes members (permitted-roots '())
                                    reason request stamp)
  "The one creator of a roadmap node and its view (:3072-3078). Everything is
validated before either is installed, so a refusal writes neither; the create
is one `:roadmap-create` `:structure` event, its `:under` a node and never the
open root, every create starts with `:members ()`, axis ids are distinct, and
the view carries `:members`, `:permitted-roots` and `:projections`."
  (declare (ignore stamp))
  (let ((state (kernel-state kernel)))
    (flet ((refuse (what &optional (code 2))
             (return-from roadmap-create
               (values nil (format nil "ROADMAP FAIL node=~A: ~A" id what) code))))
      (unless (and (stringp id) (plusp (length id)))
        (refuse "bad id"))
      ;; Its `:under` is `(:node "<id>")` only, never the open root.
      (unless (and (stringp parent) (plusp (length parent)))
        (refuse "root create has no node under it"))
      (let ((parent-node (%node-quiet state parent)))
        (unless parent-node
          (refuse (format nil "no such parent ~A" parent) 1))
        (when (eq (wnode-type parent-node) :roadmap)
          (refuse "a roadmap is not a container for another")))
      (when (%node-quiet state id)
        (refuse "rule 1: duplicate id" 1))
      (unless (member row-kind *roadmap-row-kinds*)
        (refuse (format nil "bad row kind ~A"
                        (string-downcase (princ-to-string row-kind)))))
      (unless (member aggregation *roadmap-aggregations*)
        (refuse (format nil "bad aggregation ~A"
                        (string-downcase (princ-to-string aggregation)))))
      (unless (member completion-policy *roadmap-completion-policies*)
        (refuse (format nil "bad completion policy ~A"
                        (string-downcase (princ-to-string completion-policy)))))
      (unless (%roadmap-axis-ids-p axes)
        (refuse "axes must be distinct non-empty ids"))
      (unless (and (listp permitted-roots)
                   (every (lambda (r) (and (stringp r) (plusp (length r))))
                          permitted-roots))
        (refuse "bad permitted root"))
      ;; Every create starts with `:members ()`.
      (unless (null members)
        (refuse "members start empty"))
      (let* ((rev (kernel-next-rev kernel))
             (view (list :members '()
                         :retired '()
                         :revive-events '()
                         :axes (copy-list axes)
                         :row-kind row-kind
                         :aggregation aggregation
                         :completion-policy completion-policy
                         :permitted-roots (copy-list permitted-roots)
                         :projections '()
                         :cells '()
                         :revision 0
                         :log '()))
             (node (make-wnode :id id :type :roadmap :parent parent :children '()
                               :required t :required-count 0 :required-open 0
                               :state :unknown :branch :o :open-count 0 :links +absent+
                               :title (or title +absent+) :category +absent+
                               :private +absent+ :version +absent+
                               :repo nil :view view :meta-log '()
                               :settles 0 :revived "-")))
        (%install-open-node state node)
        ;; The one `:roadmap-create` `:structure` event, in the node's
        ;; append-only structure history. It is not a work transition, so it is
        ;; not the replay path of seed and history.
        (push (list :op :structure :kind :structure :verb :roadmap-create
                    :node id :node-type :roadmap :title (or title +absent+)
                    :under (list :node parent)
                    :row-kind row-kind :aggregation aggregation
                    :completion-policy completion-policy
                    :axes (copy-list axes) :members '()
                    :permitted-roots (copy-list permitted-roots)
                    :reason (or reason +absent+) :rev rev)
              (wnode-meta-log node))
        (setf (wstate-revision state) (max (wstate-revision state) rev))
        (setf (kernel-next-rev kernel) (1+ rev))
        (values t
                (format nil "ROADMAP OK id=~A request=~A change=create changed=1 rev=~D"
                        id request rev)
                0)))))

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


;;; ------------------------------------------------------------------
;;; folded from replays-render-priority.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-render-priority.lisp --- the pure part of the render and priority
;;;; paragraphs of docs/SPEC-WORK.md:3129-3193, the five acceptance replays
;;;; `render-refuses-a-target-outside-its-roots`, `render-artifact-is-bounded`,
;;;; `a-root-id-grants-nothing`, `priority-orders-only-the-eligible` and
;;;; `priority-inherits-and-clears` call.
;;;;
;;;; Nothing here starts a session, a socket, a file write or a worker. The
;;;; live session, the CLI plumbing and the real filesystem are later slices;
;;;; here the resolution rules, the artifact bound and the ordering are pure
;;;; functions over explicit values. The `--chat` answer is modeled as the one
;;;; bounded artifact object the wire exception names; a `--file`/`--check`
;;;; answer as a status result and whether it wrote.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Render: roots, mappings and a target's resolution
;;; (SPEC-WORK.md:3129-3154)
;;; ------------------------------------------------------------------

(defstruct (render-session
            (:constructor make-render-session (&key permissions mappings)))
  ;; permissions: alist root-id -> list of modes, e.g. (:file :chat)
  ;; mappings:    alist root-id -> (:repo "owner/name" :directory "/bench/...")
  permissions mappings)

(defstruct (render-projection
            (:constructor make-render-projection (&key repo root target)))
  repo root target)

(defstruct (render-result
            (:constructor %make-render-result (&key ok reason line artifact wrote)))
  ok reason line artifact wrote)

(defun path-within-p (path root)
  "True when PATH is ROOT or a descendant of ROOT, on whole path segments."
  (let ((rl (length root)))
    (and (>= (length path) rl)
         (string= root path :end2 rl)
         (or (= (length path) rl)
             (char= (char path rl) #\/)))))

(defun path-has-parent-segment-p (path)
  "True when PATH has a `..` segment, which could climb out of ROOT."
  (let ((n (length path)))
    (or (string= ".." path)
        (and (search "/../" path) t)
        (and (>= n 3) (string= "../" path :end2 3))
        (and (>= n 3) (string= "/.." path :start2 (- n 3))))))

(defun render-symlink-escape-p (path root symlinks)
  "SYMLINKS is an alist of absolute link path -> absolute destination. True when
resolving PATH through one of them lands outside ROOT (SPEC-WORK.md:3141)."
  (dolist (link symlinks)
    (let ((from (car link)) (to (cdr link)))
      (when (path-within-p path from)
        (let ((resolved (concatenate 'string to (subseq path (length from)))))
          (unless (path-within-p resolved root)
            (return-from render-symlink-escape-p t))))))
  nil)

(defun marker-refusal (markers)
  "A marker pair is exactly two offsets, start before end. NIL when sound
(SPEC-WORK.md:3140)."
  (cond ((null markers) "a marker missing")
        ((not (= 2 (length markers))) "a marker duplicated")
        ((> (first markers) (second markers)) "a marker reversed")
        (t nil)))

(defun render-file (session projection &key path mode current-hash new-hash
                                                markers symlinks)
  "The `--file`/`--check` read of the stored target. MODE is :file or :check.
The effective root is the mapping's directory; file mode needs both the stored
permission and that mapping, and no mutation grants filesystem access
(SPEC-WORK.md:3143). A --check writes nothing."
  (let* ((root-id (render-projection-root projection))
         (mapping (cdr (assoc root-id (render-session-mappings session))))
         (modes (cdr (assoc root-id (render-session-permissions session)))))
    (flet ((refuse (reason)
             (%make-render-result
              :ok nil :reason reason :wrote nil
              :line (format nil "RENDER FAIL view=~A: ~A"
                            (render-projection-repo projection) reason))))
      (cond
        ((null mapping) (refuse "no mapping"))
        ((not (member :file modes)) (refuse "no mapping"))
        ((not (equal (getf mapping :repo) (render-projection-repo projection)))
         (refuse "target identity"))
        ((null path) (refuse "a path outside its root"))
        ((path-has-parent-segment-p path) (refuse "a path outside its root"))
        ((not (path-within-p path (getf mapping :directory)))
         (refuse "a path outside its root"))
        ((render-symlink-escape-p path (getf mapping :directory) symlinks)
         (refuse "symlink escape"))
        ((and current-hash (not (equal current-hash new-hash)))
         (refuse "a hash moved"))
        ((marker-refusal markers) (refuse (marker-refusal markers)))
        (t (%make-render-result
            :ok t :wrote (eq mode :file)
            :line (format nil "RENDER OK view=~A target=~A was=~A now=~A"
                          (render-projection-repo projection) path
                          (or current-hash "-") (or new-hash "-"))))))))

(defparameter *render-frame-bound* 4096
  "The frame/output bound an artifact may not pass (SPEC-WORK.md:3152).")

(defun render-chat (body &key (bound *render-frame-bound*))
  "A successful `--chat` carries one bounded artifact and no status line. An
artifact past BOUND refuses and is never truncated."
  (let* ((bytes (length (utf8-octets body)))
         (artifact (list :encoding "utf8" :body body
                         :sha256 (sha256-hex body) :bytes bytes)))
    (if (> bytes bound)
        (%make-render-result
         :ok nil :reason "an artifact past its bound" :wrote nil
         :line (format nil "RENDER FAIL: artifact ~D bytes past bound ~D" bytes bound))
        (%make-render-result :ok t :artifact artifact :wrote nil :line nil))))

(defun artifact-bytes-ok-p (artifact)
  (= (getf artifact :bytes) (length (utf8-octets (getf artifact :body)))))

(defun artifact-hash-ok-p (artifact)
  (string= (getf artifact :sha256) (sha256-hex (getf artifact :body))))

(defun artifact-valid-p (artifact)
  "The client verifies the count and the hash before writing the body."
  (and (artifact-bytes-ok-p artifact) (artifact-hash-ok-p artifact)))

(defun render-correlated-batch (entries)
  "ENTRIES is a list of requests carrying :request plus either :reply or :chat.
Returns an alist request-id -> response; a chat response is `(:artifact A)` when
sound and `(:refused R)` when it is corrupt or past its bound. Ordinary replies
and the one chat artifact stay correlated by request id."
  (mapcar
   (lambda (entry)
     (let ((id (getf entry :request)))
       (cond
         ((getf entry :chat)
          (let ((r (render-chat (getf entry :chat)
                                :bound (or (getf entry :bound) *render-frame-bound*))))
            (cons id (if (render-result-ok r)
                         (list :artifact (render-result-artifact r))
                         (list :refused (render-result-reason r))))))
         (t (cons id (list :reply (getf entry :reply)))))))
   entries))

;;; A cooperative renderer lock, advisory among cooperating renderers only.

(defun acquire-render-lock (locks target)
  "The per-target lock. Returns (values t new-locks) or (values nil nil) when
another cooperating renderer holds it (SPEC-WORK.md:3146)."
  (if (assoc target locks :test #'equal)
      (values nil nil)
      (values t (cons (cons target t) locks))))

(defparameter *render-lock-guards-external-editor* nil
  "An external editor can still race the final replace; the document claims no
more than the cooperative lock (SPEC-WORK.md:3146).")

;;; ------------------------------------------------------------------
;;; Priority: the two slots, effective rank and the ready order
;;; (SPEC-WORK.md:3156-3193)
;;; ------------------------------------------------------------------

(defun priority-field (&key self subtree)
  "Every node carries `:priority (:self <rank|absent> :subtree <rank|absent>)`.
An absent slot is the restricted-data spelling (:absent)."
  (list :self (or self +absent+) :subtree (or subtree +absent+)))

(defun priority-slot (field context)
  (getf field context))

(defun priority-no-effect-p (field change context rank)
  "A same-value set or a clear of an absent slot is the no-effect receipt."
  (let ((old (priority-slot field context)))
    (ecase change
      (:set (and (integerp old) (= old rank)))
      (:clear (absentp old)))))

(defun priority-set (field rank context)
  "Set one slot. Returns (values new-field changed-p)."
  (if (priority-no-effect-p field :set context rank)
      (values field nil)
      (let ((new (copy-list field)))
        (setf (getf new context) rank)
        (values new t))))

(defun priority-clear (field context)
  "Clear one slot. Returns (values new-field changed-p)."
  (if (priority-no-effect-p field :clear context nil)
      (values field nil)
      (let ((new (copy-list field)))
        (setf (getf new context) +absent+)
        (values new t))))

(defun effective-priority (field &optional ancestors)
  "Effective rank is the nearest context: the node's own :self, else the deepest
:subtree on its containment path, else the default. ANCESTORS is nearest-first,
each `(id . field)`. Returns (values rank context source-id), rank +absent+ for
the default."
  (let ((self (getf field :self)))
    (if (integerp self)
        (values self :self "self")
        (progn
          (dolist (ancestor ancestors)
            (let ((sub (getf (cdr ancestor) :subtree)))
              (when (integerp sub)
                (return-from effective-priority
                  (values sub :subtree (car ancestor))))))
          (values +absent+ :default "default")))))

(defun priority-settle (field)
  "A settled node keeps its slots into C."
  field)
(defun priority-reopen (field)
  "A reopen restores the slots."
  field)

(defparameter *priority-event-keys*
  '(:kind :verb :node :change :context :rank :reason)
  "Its event kind is :prioritise; it is neither structure nor scope and moves no
containment, dependency, acceptance, state, generation, baseline, required set,
O or C membership, W, lease, capacity, count or percentage.")

(defun priority-event (&key node change context rank reason)
  (list :kind :prioritise :verb :verb-prioritise :node node :change change
        :context context :rank rank :reason reason))

(defun priority-only-moves-its-field-p (event)
  (null (set-difference
         (loop for (k v) on event by #'cddr collect k)
         *priority-event-keys*)))

(defun priority-lessp (a b)
  "Explicit ranks ascending, then defaults, then bytewise stable id."
  (let ((ra (getf a :priority)) (rb (getf b :priority)))
    (cond ((and (integerp ra) (integerp rb))
           (if (= ra rb)
               (string< (getf a :id) (getf b :id))
               (< ra rb)))
          ((integerp ra) t)
          ((integerp rb) nil)
          (t (string< (getf a :id) (getf b :id))))))

(defun ready-order (eligible blocked &key (order :discovery))
  "The one reader sorts only rows `ready` already admits; blocked rows are not
dropped but follow the eligible rows in discovery order. ORDER :discovery
leaves the eligible rows as given."
  (if (eq order :priority)
      (append (sort (copy-list eligible) #'priority-lessp)
              (copy-list blocked))
      (append (copy-list eligible) (copy-list blocked))))

(defun priority-order-refusal (ask order)
  "`--order priority` is refused under every ask but `ready`."
  (if (and (eq order :priority) (not (eq ask :ready)))
      (values nil
              (format nil "QUERY FAIL ask=~A: --order priority is refused" ask)
              2)
      (values t nil 0)))

(defun priority-eligible-p (row &key (capacity-p t) (approval-p t)
                                      (dependency-ok-p t) (held-p nil))
  "Eligibility is rechecked before ranking; a row that lost capacity or approval,
gained a hold or whose dependency moved is not eligible."
  (and capacity-p approval-p dependency-ok-p (not held-p)))

(defun priority-starts-nothing-p ()
  "It selects no worker and starts or interrupts no work."
  t)


;;; ------------------------------------------------------------------
;;; folded from replays-slice-05.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-slice-05.lisp --- the pure part of four slice-1 acceptance replays.
;;;;
;;;; docs/SPEC-WORK.md:
;;;;   2076-2090, :5544  merged-is-not-distributed
;;;;   2110,       :6331  ready-names-the-blocker-and-the-resolver
;;;;   2385-2402,  :5526  remove-settles-only-open-items (the kernel verb is in
;;;;                       kernel.lisp; the pure planner here names the subtree)
;;;;   2646-2653,  :5727  endpoint-is-local-and-private
;;;;
;;;; These functions carry the sentences the acceptance replays assert; the
;;;; stubs they replace were marked NEEDS-KERNEL in slice-05-durable-journal.lisp.

(in-package #:nova-work)

#+sbcl
(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix)
  (require :sb-bsd-sockets))

;;; ------------------------------------------------------------------
;;; merged-is-not-distributed (SPEC-WORK.md:2076-2090, :5544)
;;; ------------------------------------------------------------------
;;;
;;; The disposition row of a settled finding carries `landed=` -- the `:against`
;;; sha of its qualifying `:merged` evidence -- and `released=` -- the `:version`
;;; of the settled release task that names it in its `:deps`, found through the
;;; reverse-dependency index, and `-` while that task is still in O, because a
;;; merged fix is not a distributed one. Where two settled release tasks name one
;;; item, the earlier settle stamp wins.

(defstruct (release-task
             (:constructor make-release-task (id &key version deps (branch :o) settle-stamp)))
  id version deps branch settle-stamp)

(defstruct (finding
             (:constructor make-finding (id &key (branch :c) disposition evidence)))
  id branch disposition evidence)

(defun finding-landed (finding)
  "`landed=<sha|->`: the :against sha of the qualifying :merged evidence, or -."
  (let ((hit (find :merged (finding-evidence finding)
                   :key (lambda (e) (getf e :criterion)))))
    (or (and hit (getf hit :against)) "-")))

(defun reverse-release-tasks (release-tasks item-id)
  "The release tasks whose :deps name ITEM-ID (the reverse-dependency index)."
  (remove-if-not (lambda (r) (member item-id (release-task-deps r) :test #'equal))
                 release-tasks))

(defun finding-released (finding release-tasks)
  "`released=<version|->`: the version of the settled release task that first
carried the fix, or - while every task that names it is still open."
  (let* ((settled (remove-if-not (lambda (r) (eq :c (release-task-branch r)))
                                 (reverse-release-tasks release-tasks (finding-id finding))))
         (first (first (sort (copy-list settled) #'string< :key #'release-task-settle-stamp))))
    (if first (release-task-version first) "-")))

(defun disposition-row (finding release-tasks)
  "One disposition row: :id, :branch, :disposition, :landed and :released."
  (list :id (finding-id finding)
        :branch (finding-branch finding)
        :disposition (finding-disposition finding)
        :landed (finding-landed finding)
        :released (finding-released finding release-tasks)))

;;; ------------------------------------------------------------------
;;; ready-names-the-blocker-and-the-resolver (SPEC-WORK.md:2110, :6331)
;;; ------------------------------------------------------------------
;;;
;;; `ready --node X`: every row that cannot proceed names its exact reason and
;;; who can resolve it. An item is blocked by a dependency still in O; the
;;; resolver is the blocker's live holder, else its responsible, else `-`.

(defstruct (ready-item
             (:constructor make-ready-item (id &key (branch :o) state deps holder responsible)))
  id branch state deps holder responsible)

(defstruct (ready-row
             (:constructor make-ready-row (id &key ready reason resolver)))
  id ready reason resolver)

(defun ready-resolver (item)
  (or (ready-item-holder item) (ready-item-responsible item) "-"))

(defun ready-rows (items)
  "One row per item in O. A row with an open dependency is not ready, names that
dependency, and names the dependency's resolver."
  (let ((rows '()))
    (dolist (item items)
      (when (eq :o (ready-item-branch item))
        (let ((blockers '()))
          (dolist (dep (ready-item-deps item))
            (let ((d (find dep items :key #'ready-item-id :test #'equal)))
              (when (or (null d) (eq :o (ready-item-branch d)))
                (push dep blockers))))
          (setf blockers (nreverse blockers))
          (if (null blockers)
              (push (make-ready-row (ready-item-id item)
                                    :ready t :reason "-"
                                    :resolver (or (ready-item-holder item)
                                                  (ready-item-responsible item)
                                                  "-"))
                    rows)
              (let* ((blocker-id (first blockers))
                     (blocker (find blocker-id items :key #'ready-item-id :test #'equal)))
                (push (make-ready-row (ready-item-id item)
                                      :ready nil
                                      :reason (format nil "blocked by ~A" blocker-id)
                                      :resolver (if blocker (ready-resolver blocker) "-"))
                      rows))))))
    (nreverse rows)))

;;; ------------------------------------------------------------------
;;; endpoint-is-local-and-private (SPEC-WORK.md:2646-2653, :5727)
;;; ------------------------------------------------------------------
;;;
;;; The session's directory is created 0700 and its socket 0600, both owned by
;;; the running account; a pre-existing directory or socket with wider modes is
;;; refused rather than reused; and no listener is bound to any network address.

(defstruct (session-endpoint
             (:constructor %make-session-endpoint
                 (directory socket-path directory-mode socket-mode owner socket-family)))
  directory socket-path directory-mode socket-mode owner socket-family)

(defun session-endpoint-dir-mode (endpoint) (session-endpoint-directory-mode endpoint))
(defun session-endpoint-file-mode (endpoint) (session-endpoint-socket-mode endpoint))

#+sbcl
(defun %file-mode (path)
  (logand (sb-posix:stat-mode (sb-posix:stat path)) #o777))

#+sbcl
(defun local-socket-family ()
  "The family of a local socket, for the endpoint assertion."
  (sb-bsd-sockets:socket-family (make-instance 'sb-bsd-sockets:local-socket :type :stream)))

#-sbcl
(defun local-socket-family () :local)

#+sbcl
(defun current-account-uid () (sb-posix:getuid))

#-sbcl
(defun current-account-uid () 0)

#+sbcl
(defun make-session-endpoint (directory socket-path)
  "Create or reuse DIRECTORY at 0700 and bind SOCKET-PATH at 0600. A pre-existing
directory or socket with wider modes refuses; the socket is a local (AF_UNIX)
socket and never a network listener."
  (unless (probe-file directory)
    (sb-posix:mkdir directory #o700))
  (let ((dmode (%file-mode directory)))
    (unless (eql dmode #o700)
      (error 'unsupported-input
             :what (format nil "endpoint: pre-existing directory ~A has mode ~O, not 0700"
                           directory dmode))))
  (when (probe-file socket-path)
    (let ((smode (%file-mode socket-path)))
      (unless (eql smode #o600)
        (error 'unsupported-input
               :what (format nil "endpoint: pre-existing socket ~A has mode ~O, not 0600"
                             socket-path smode)))))
  (let* ((sock (make-instance 'sb-bsd-sockets:local-socket :type :stream))
         (family (sb-bsd-sockets:socket-family sock)))
    (unwind-protect
         (progn
           (sb-bsd-sockets:socket-bind sock socket-path)
           (sb-posix:chmod socket-path #o600))
      (sb-bsd-sockets:socket-close sock))
    (%make-session-endpoint directory socket-path #o700 #o600 (sb-posix:getuid) family)))

#-sbcl
(defun make-session-endpoint (directory socket-path)
  (declare (ignore directory socket-path))
  (error 'not-implemented))

(defun endpoint-network-listener-p (endpoint)
  "This scope binds no network address; the endpoint is local only."
  (declare (ignore endpoint))
  nil)
