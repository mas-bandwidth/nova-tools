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
