;;;; render.lisp --- the render targets: --chat, --file, --check and the stored
;;;; projection's permitted-root mapping.
;;;;
;;;; docs/SPEC-WORK.md:2313, :3080-3085, :3131-3156, :5304, :5398, :5853.
;;;; A stored projection names an opaque permitted root id, the repository
;;;; identity it belongs to and the clean relative path of its target; the
;;;; session's `--render-root` mapping resolves that root id to a bench
;;;; directory. A root id alone grants nothing: file mode needs both the stored
;;;; permission and the mapping, and no mutation grants filesystem access.
;;;; `render --file` replaces the region between the projection's two markers
;;;; after a hash recheck and records the target identity, the old and new
;;;; hashes and the render revision; `render --check` performs the same read and
;;;; verification and writes nothing. A `--chat` render carries one bounded
;;;; artifact, never a status line.
;;;;
;;;; The target store is a seam: `render-target-read`/`render-target-write` are
;;;; the two protocol functions the resident session's filesystem mapping will
;;;; implement; the in-process methods hold the bytes in the session, which is
;;;; all this slice needs.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The stored projection, the permitted root mapping and the result
;;; (docs/SPEC-WORK.md:3080-3085, :3131-3156)
;;; ------------------------------------------------------------------

(defstruct (render-session
            (:constructor make-render-session (&key permissions mappings files)))
  ;; permissions: alist root-id -> list of modes, e.g. (:file :chat)
  ;; mappings:    alist root-id -> (:repo "owner/name" :directory "/bench/...")
  ;; files:       alist absolute target path -> its bytes (the in-process store)
  permissions mappings files)

(defstruct (render-projection
            (:constructor make-render-projection
                (&key id repo root path start end policy
                      row-axis column-axis fixed)))
  ;; the stored projection record (SPEC-WORK.md:3080): an opaque root id, the
  ;; repository identity, the clean relative target path, the two distinct and
  ;; nonempty markers that bound the region, and the display selection.
  id repo root path start end policy row-axis column-axis fixed)

(defstruct (render-result
            (:constructor %make-render-result
                (&key ok reason line artifact wrote receipt)))
  ok reason line artifact wrote receipt)

;;; ------------------------------------------------------------------
;;; Paths: containment, parents and symlink escapes (:3131-3156)
;;; ------------------------------------------------------------------

(defun path-within-p (path root)
  "True when PATH is ROOT or a descendant of ROOT, on whole path segments."
  (let ((rl (length root)))
    (and (>= (length path) rl)
         (string= root path :end2 rl)
         (or (= (length path) rl)
             (char= (char path rl) #\/)))))

(defun path-absolute-p (path)
  "True when PATH begins at the filesystem root."
  (and (> (length path) 0) (char= (char path 0) #\/)))

(defun path-has-parent-segment-p (path)
  "True when PATH has a `..` segment, which could climb out of ROOT."
  (let ((n (length path)))
    (or (string= ".." path)
        (and (search "/../" path) t)
        (and (>= n 3) (string= "../" path :end2 3))
        (and (>= n 3) (string= "/.." path :start2 (- n 3))))))

(defun render-join (directory relative)
  "The effective target: DIRECTORY and the clean RELATIVE path."
  (if (and (> (length directory) 0)
           (char= (char directory (1- (length directory))) #\/))
      (concatenate 'string directory relative)
      (concatenate 'string directory "/" relative)))

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

;;; ------------------------------------------------------------------
;;; The target store seam: the session's mapping reads and writes bytes
;;; ------------------------------------------------------------------

(defgeneric render-target-escape-p (session target root symlinks)
  (:documentation "True when TARGET escapes ROOT. The pure session takes the
links as a handed-in alist; a session whose store is the filesystem resolves
TARGET and ROOT through the OS instead (SPEC-WORK.md:3151-3154)."))

(defmethod render-target-escape-p ((session render-session) target root symlinks)
  (render-symlink-escape-p target root symlinks))

(defgeneric render-target-read (session target)
  (:documentation "The stored target's bytes, or NIL when it is missing. The
resident session implements this against the filesystem; the in-process method
reads the session's store."))

(defgeneric render-target-write (session target content)
  (:documentation "Atomically replace TARGET with CONTENT. The resident session
implements this against the filesystem; the in-process method writes the
session's store."))

(defmethod render-target-read ((session render-session) target)
  (cdr (assoc target (render-session-files session) :test #'equal)))

(defmethod render-target-write ((session render-session) target content)
  (let ((pair (assoc target (render-session-files session) :test #'equal)))
    (if pair
        (setf (cdr pair) content)
        (push (cons target content) (render-session-files session))))
  content)

;;; ------------------------------------------------------------------
;;; The marker region and its replacement (:3131-3156)
;;; ------------------------------------------------------------------

(defun render-second-occurrence (needle haystack from)
  "NIL, or the index of a second NEEDLE at or after FROM."
  (search needle haystack :start2 from))

(defun render-marker-offsets (content start end)
  "The region between the two markers: (values region-start region-end nil), or
(values nil nil reason) for a missing, duplicated or reversed pair. The start
marker is matched whole and the region begins after it; the end marker is the
end of the region (SPEC-WORK.md:3140)."
  (cond
    ((or (null start) (string= start "")) (values nil nil "a marker missing"))
    ((or (null end) (string= end "")) (values nil nil "a marker missing"))
    ((string= start end) (values nil nil "a marker duplicated"))
    (t
     (let ((s (search start content)))
       (cond
         ((null s) (values nil nil "a marker missing"))
         ((render-second-occurrence start content (1+ s))
          (values nil nil "a marker duplicated"))
         (t
          (let ((e (search end content)))
            (cond
              ((null e) (values nil nil "a marker missing"))
              ((render-second-occurrence end content (1+ e))
               (values nil nil "a marker duplicated"))
              ((< e s) (values nil nil "a marker reversed"))
              (t (values (+ s (length start)) e nil))))))))))

(defun render-replace-region (content start end body)
  "CONTENT with the bytes in [START, END) replaced by BODY; every other byte is
preserved."
  (concatenate 'string (subseq content 0 start) body (subseq content end)))

;;; ------------------------------------------------------------------
;;; render --file / render --check over the stored target (:3131-3156)
;;; ------------------------------------------------------------------

(defun render-file (session projection &key mode body expected-hash
                                              symlinks revision)
  "The `--file`/`--check` read of the stored target. MODE is :file or :check.
The effective root is the mapping's directory; file mode needs both the stored
permission and that mapping, and no mutation grants filesystem access
(SPEC-WORK.md:3143). `--check` writes nothing; `--file` replaces the marker
region after the hash recheck. The result's receipt records the target identity,
the old and new hashes and the render revision."
  (let* ((root-id (render-projection-root projection))
         (mapping (cdr (assoc root-id (render-session-mappings session))))
         (modes (cdr (assoc root-id (render-session-permissions session))))
         (repo (render-projection-repo projection))
         (rel (render-projection-path projection)))
    (labels ((refuse (reason)
               (%make-render-result
                :ok nil :reason reason :wrote nil
                :line (format nil "RENDER FAIL view=~A: ~A" repo reason)))
             (finish (content target start end)
               (let* ((old (sha256-hex content))
                      (new-body (or body ""))
                      (new-content (render-replace-region content start end
                                                          new-body))
                      (new (sha256-hex new-content)))
                 (when (and expected-hash (not (equal expected-hash old)))
                   (return-from render-file (refuse "a hash moved")))
                 (when (eq mode :file)
                   (render-target-write session target new-content))
                 (%make-render-result
                  :ok t :wrote (eq mode :file)
                  :receipt (list :target target :repo repo
                                 :old old :new new :revision revision
                                 :offsets (list start end))
                  :line (format nil "RENDER OK view=~A target=~A was=~A now=~A"
                                repo target old new)))))
      (cond
        ((null mapping) (refuse "no mapping"))
        ((not (member :file modes)) (refuse "no mapping"))
        ((not (equal (getf mapping :repo) repo)) (refuse "target identity"))
        ((or (null rel) (string= rel "")) (refuse "a path outside its root"))
        ((path-absolute-p rel) (refuse "a path outside its root"))
        ((path-has-parent-segment-p rel) (refuse "a path outside its root"))
        (t
         (let ((target (render-join (getf mapping :directory) rel)))
           (cond
             ((not (path-within-p target (getf mapping :directory)))
              (refuse "a path outside its root"))
             ((render-target-escape-p session target (getf mapping :directory) symlinks)
              (refuse "symlink escape"))
             (t
              (let ((content (render-target-read session target)))
                (if (null content)
                    (refuse "the stored target is missing")
                    (multiple-value-bind (start end why)
                        (render-marker-offsets content
                                               (render-projection-start projection)
                                               (render-projection-end projection))
                      (if why
                          (refuse why)
                          (finish content target start end)))))))))))))

(defun render-check (session projection &key body expected-hash symlinks revision)
  "`render --check`: the same read and verification as `--file`, writing nothing
(SPEC-WORK.md:3141)."
  (render-file session projection :mode :check :body body
               :expected-hash expected-hash :symlinks symlinks :revision revision))

;;; ------------------------------------------------------------------
;;; render --chat: the one bounded artifact (:3152)
;;; ------------------------------------------------------------------

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
