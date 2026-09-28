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

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defun read-os-random-bytes (n)
  "Read exactly N bytes from the operating system CSPRNG and answer them as a
fresh byte vector. A short read, an end of file or any error signals
UNSUPPORTED-INPUT naming the source: this reader never pads, never retries
forever and never falls back to a weaker source, because a guessable journal id
is worse than no journal (docs/SPEC-WORK.md:451)."
  (declare (type (integer 0) n))
  #+sbcl
  (let ((fd -1)
        (buf (make-array n :element-type '(unsigned-byte 8)))
        (filled 0))
    (unwind-protect
         (progn
           (setf fd (handler-case (sb-posix:open "/dev/urandom" sb-posix:o-rdonly)
                      (error (c)
                        (error 'unsupported-input
                               :what (format nil "the OS CSPRNG source /dev/urandom could not be opened: ~A; no journal identity can be minted" c)))))
           (when (or (null fd) (minusp fd))
             (error 'unsupported-input
                    :what "the OS CSPRNG source /dev/urandom could not be opened; no journal identity can be minted"))
           (loop while (< filled n) do
             (let ((got (handler-case
                            (sb-sys:with-pinned-objects (buf)
                              (sb-posix:read fd
                                             (sb-sys:sap+ (sb-sys:vector-sap buf) filled)
                                             (- n filled)))
                          (error (c)
                            (error 'unsupported-input
                                   :what (format nil "the OS CSPRNG source /dev/urandom read failed: ~A; no journal identity can be minted" c))))))
               (when (or (null got) (<= got 0))
                 (error 'unsupported-input
                        :what (format nil "the OS CSPRNG source /dev/urandom returned a short read after ~D of ~D bytes; no journal identity can be minted" filled n)))
               (incf filled got)))
           buf)
      (when (and (integerp fd) (>= fd 0))
        (ignore-errors (sb-posix:close fd)))))
  #-sbcl
  (error 'unsupported-input
         :what "no operating system CSPRNG reader outside SBCL; the engine's platform is pinned"))

(defvar *journal-identity-byte-source* #'read-os-random-bytes
  "The seam every journal identity is minted from: a function of one integer N
that answers N random bytes. It is bound to READ-OS-RANDOM-BYTES, the operating
system CSPRNG, so a test may bind it to a known or a refusing source. There is no
fallback of any kind (docs/SPEC-WORK.md:451).")

(defun bytes-to-lowercase-hex (bytes)
  "The lowercase hex of a byte vector, two characters per byte. Bounds on BYTES
are the caller's to enforce."
  (string-downcase
   (with-output-to-string (s)
     (loop for b across bytes do (format s "~2,'0x" b)))))

(defun mint-journal-identity ()
  "Mint a stable logical journal identity: 256 random bits from the operating
system CSPRNG, printed as 64 lowercase hex characters, an identity and never an
ownership token (docs/SPEC-WORK.md:451). The bytes are hex-encoded DIRECTLY and
never hashed: a short or constant source must refuse, never hide behind a
well-formed digest. A source that falls short refuses."
  (let ((bytes (funcall *journal-identity-byte-source* 32)))
    (unless (and (vectorp bytes)
                 (= (length bytes) 32)
                 (every (lambda (b) (and (integerp b) (<= 0 b 255))) bytes))
      (error 'unsupported-input
             :what (format nil "the journal identity byte source did not answer exactly 32 bytes (~S); no journal identity can be minted" bytes)))
    (bytes-to-lowercase-hex bytes)))

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
