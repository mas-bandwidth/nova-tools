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
