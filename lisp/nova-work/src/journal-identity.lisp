;;;; journal-identity.lisp --- the stable logical journal identity.
;;;;
;;;; docs/SPEC-WORK.md:471-474: **A rotation opens a new physical segment of the
;;;; same logical journal**: the segment's header names the journal id, the
;;;; previous segment and the copied boundary record. A new file is not a new
;;;; journal, and the logical identity is what survives physical rotation.
;;;;
;;;; docs/SPEC-WORK.md:7176-7182: **Rotating or pruning a journal keeps a
;;;; reachable verified cut and every tail record and disposition each retained
;;;; savepoint needs, or publishes a validated replacement first**. The two
;;;; refusals Stella's ruling names are a live journal whose logical identity
;;;; differs and a live journal whose chain to the saved cut is broken; absence
;;;; of the identity selects the explicit legacy shape and is never wildcard
;;;; acceptance.
;;;;
;;;; The header hash `journal-file-identity` (src/savepoint-create.lisp) is the
;;;; SHA-256 of the whole header LINE and therefore moves when the header gains
;;;; a field. This file adds the stable logical identity BESIDE it: minted once
;;;; at journal creation, carried as an OPTIONAL `:journal-identity` header
;;;; field, and absent in a legacy journal.

(in-package #:nova-work)

(defun mint-journal-identity ()
  "Mint a stable logical journal identity: unique to one journal and stable
across every later physical rotation."
  (sha256-hex (format nil "nova-work/journal-identity/~A/~A/~A"
                      (get-universal-time)
                      (get-internal-real-time)
                      (random most-positive-fixnum))))

(defun header-journal-identity (header-plist)
  "The optional logical identity a journal header carries, or NIL for a legacy
header. HEADER-PLIST is the header's plist, or the whole (:journal-header ...)
form."
  (let ((plist (if (and (consp header-plist)
                        (eq (first header-plist) :journal-header))
                   (rest header-plist)
                   header-plist)))
    (getf plist :journal-identity)))

(defun journal-header-line (path)
  "PATH's first line -- the journal header -- or NIL when it cannot be read."
  (handler-case
      (with-open-file (in path :direction :input :element-type 'character
                               :external-format :utf-8 :if-does-not-exist nil)
        (if in (read-line in nil nil) nil))
    (error () nil)))

(defun %journal-path-of (journal-or-path)
  (cond ((stringp journal-or-path) journal-or-path)
        ((pathnamep journal-or-path) (namestring journal-or-path))
        (t (slot-value journal-or-path 'path))))

(defun journal-logical-identity (journal-or-path)
  "The stable logical identity of JOURNAL-OR-PATH, or NIL when its header is a
legacy one: absence selects the explicit legacy shape, never acceptance."
  (let* ((path (%journal-path-of journal-or-path))
         (line (journal-header-line path)))
    (when line
      (header-journal-identity
       (handler-case (read-restricted line) (error () nil))))))

(defun journal-root-header-sha (journal-or-path)
  "The SHA-256 of the ROOT segment's header line -- the header hash the logical
journal keeps across physical rotation. A journal that never rotated answers the
live header's own line, exactly as `journal-file-identity` does."
  (let ((current (namestring (merge-pathnames (%journal-path-of journal-or-path))))
        (seen '())
        (sha nil))
    (loop
      (when (or (null current) (member current seen :test #'string=))
        (return sha))
      (push current seen)
      (let ((line (journal-header-line current)))
        (when (null line) (return sha))
        (setf sha (sha256-hex line))
        (let* ((form (handler-case (read-restricted line) (error () nil)))
               (plist (and (consp form) (rest form)))
               (prev (getf plist :previous-segment)))
          (if prev
              (setf current (namestring (merge-pathnames prev)))
              (return sha)))))))

(defun journal-file-identities (journal-or-path)
  "Answer (values ROOT-HEADER-SHA LOGICAL-IDENTITY) for JOURNAL-OR-PATH. The
first is the header hash a manifest names; the second is the stable logical
identity beside it, NIL for a legacy journal."
  (values (journal-root-header-sha journal-or-path)
          (journal-logical-identity journal-or-path)))

(defun header-covers-cut-p (header cut)
  "Whether HEADER alone names CUT, a (:sequence N :sha256 H), as its copied
boundary record or among its bounded retained-cut locators. Reads no file."
  (when (and cut (not (absentp cut)))
    (let* ((plist (if (and (consp header) (eq (first header) :journal-header))
                      (rest header)
                      header))
           (seq (getf cut :sequence))
           (hash (getf cut :sha256)))
      (flet ((covers (x)
               (and (consp x)
                    (consp (cdr x))
                    (eql seq (or (getf x :sequence) (getf x :seq)))
                    (stringp hash)
                    (equal hash (getf x :sha256)))))
        (or (covers (getf plist :copied-boundary))
            (find-if #'covers (getf plist :retained-cuts)))))))

(defun journal-chain-covers-cut-p (path cut)
  "Walk PATH's segment headers toward the root through `:previous-segment`,
checking each header's copied boundary record and retained-cut locators. Answers
(values FOUND-P ROOT-HEADER-SHA ROTATED-P): FOUND-P is whether some header names
CUT; ROOT-HEADER-SHA is the root segment's header line digest; ROTATED-P is
whether the live segment names a previous segment. An unreadable link ends the
walk, so a broken chain answers NIL and never a wildcard."
  (let ((current (namestring (merge-pathnames path)))
        (found (or (absentp cut) nil))
        (rotated nil)
        (sha nil)
        (seen '()))
    (loop
      (when (or (null current) (member current seen :test #'string=))
        (return (values found sha rotated)))
      (push current seen)
      (let ((line (journal-header-line current)))
        (when (null line)
          (return (values found sha rotated)))
        (setf sha (sha256-hex line))
        (let* ((header (handler-case (read-restricted line) (error () nil)))
               (plist (and (consp header) (rest header)))
               (prev (getf plist :previous-segment)))
          (when (and cut (header-covers-cut-p header cut))
            (setf found t))
          (if prev
              (progn (setf rotated t)
                     (setf current (namestring (merge-pathnames prev))))
              (return (values found sha rotated))))))))
